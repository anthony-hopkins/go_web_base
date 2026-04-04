package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/time/rate"
)

// Config holds the application configuration loaded from environment variables.
// It uses the `cmp.Or` pattern for default values and `strconv` for type conversion.
type Config struct {
	APIKey              string        // API key for authentication (currently unused in middleware, but configured)
	Domain              string        // The domain name for Let's Encrypt (ACME) certificates
	HTTPSPort           string        // Port to listen on for HTTPS traffic (e.g., ":443")
	HTTPPort            string        // Port to listen on for HTTP traffic, primarily for ACME challenges (e.g., ":80")
	CertCacheDir        string        // Directory to store Let's Encrypt certificates
	ACMEEnabled         bool          // Flag to enable/disable Let's Encrypt (ACME) certificate management
	TLSCertFile         string        // Path to a custom TLS certificate file (if ACME is disabled)
	TLSKeyFile          string        // Path to a custom TLS key file (if ACME is disabled)
	TrustProxy          bool          // Whether to trust X-Forwarded-For headers for identifying client IPs
	MaxHeaderBytes      int           // Maximum size of HTTP request headers in bytes
	MaxBodyBytes        int64         // Maximum size of HTTP request bodies in bytes
	ShutdownTimeout     time.Duration // Time to wait for active connections to close during graceful shutdown
	RateLimit           float64       // Average number of requests allowed per second per IP
	RateBurst           int           // Maximum number of requests allowed in a single burst per IP
	RateCleanupInterval time.Duration // How often to clean up stale rate limiters from memory
	RateExpiration      time.Duration // Time since last seen before a rate limiter is considered stale
}

var (
	// httpRequestsTotal is a Prometheus counter that tracks the total number of HTTP requests processed,
	// partitioned by HTTP method, path, and response status code.
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests.",
	}, []string{"method", "path", "status"})

	// httpRequestDuration is a Prometheus histogram that tracks the duration of HTTP requests in seconds,
	// partitioned by method and path. It uses default Prometheus buckets.
	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// rateLimitHitsTotal is a Prometheus counter that tracks the total number of requests that were
	// blocked by the rate limiter, partitioned by the client's IP address.
	rateLimitHitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rate_limit_hits_total",
		Help: "Total number of rate limit hits.",
	}, []string{"remote_addr"})

	// panicsTotal is a Prometheus counter that tracks the total number of application panics
	// that were recovered by the recoveryMiddleware.
	panicsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "panics_total",
		Help: "Total number of panics recovered.",
	})
)

// loadConfig reads configuration from the environment (optionally from a .env file)
// and validates it. It uses modern Go 1.26 patterns like `cmp.Or` for concise defaults.
func loadConfig() (Config, error) {
	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found, relying on system environment variables")
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return Config{}, errors.New("API_KEY environment variable is required")
	}

	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return Config{}, errors.New("DOMAIN environment variable is required")
	}

	httpsPort := cmp.Or(os.Getenv("HTTPS_PORT"), ":443")
	httpPort := cmp.Or(os.Getenv("HTTP_PORT"), ":80")
	certCacheDir := cmp.Or(os.Getenv("CERT_CACHE_DIR"), "cert-cache")
	acmeEnabled, _ := strconv.ParseBool(cmp.Or(os.Getenv("ACME_ENABLED"), "true"))
	tlsCertFile := os.Getenv("TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("TLS_KEY_FILE")
	trustProxy, _ := strconv.ParseBool(cmp.Or(os.Getenv("TRUST_PROXY"), "false"))

	maxHeaderBytes, err := strconv.Atoi(cmp.Or(os.Getenv("MAX_HEADER_BYTES"), "1048576")) // 1MB default
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_HEADER_BYTES: %w", err)
	}

	maxBodyBytes, err := strconv.ParseInt(cmp.Or(os.Getenv("MAX_BODY_BYTES"), "10485760"), 10, 64) // 10MB default
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_BODY_BYTES: %w", err)
	}

	shutdownTimeout, err := time.ParseDuration(cmp.Or(os.Getenv("SHUTDOWN_TIMEOUT"), "30s"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid SHUTDOWN_TIMEOUT: %w", err)
	}

	rateLimit, err := strconv.ParseFloat(cmp.Or(os.Getenv("RATE_LIMIT"), "10"), 64)
	if err != nil {
		return Config{}, fmt.Errorf("invalid RATE_LIMIT: %w", err)
	}

	rateBurst, err := strconv.Atoi(cmp.Or(os.Getenv("RATE_BURST"), "20"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid RATE_BURST: %w", err)
	}

	rateCleanupInterval, err := time.ParseDuration(cmp.Or(os.Getenv("RATE_CLEANUP_INTERVAL"), "10m"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid RATE_CLEANUP_INTERVAL: %w", err)
	}

	rateExpiration, err := time.ParseDuration(cmp.Or(os.Getenv("RATE_EXPIRATION"), "1h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid RATE_EXPIRATION: %w", err)
	}

	return Config{
		APIKey:              apiKey,
		Domain:              domain,
		HTTPSPort:           httpsPort,
		HTTPPort:            httpPort,
		CertCacheDir:        certCacheDir,
		ACMEEnabled:         acmeEnabled,
		TLSCertFile:         tlsCertFile,
		TLSKeyFile:          tlsKeyFile,
		TrustProxy:          trustProxy,
		MaxHeaderBytes:      maxHeaderBytes,
		MaxBodyBytes:        maxBodyBytes,
		ShutdownTimeout:     shutdownTimeout,
		RateLimit:           rateLimit,
		RateBurst:           rateBurst,
		RateCleanupInterval: rateCleanupInterval,
		RateExpiration:      rateExpiration,
	}, nil
}

// main is the entry point of the application. It initializes the logger,
// loads environment variables, sets up the HTTPS server with autocert,
// and implements graceful shutdown.
func main() {
	// Initialize structured logging with JSON output for production environments.
	// This allows logs to be easily parsed by log management systems like ELK or Datadog.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Load and validate configuration.
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("Configuration error", "error", err)
		os.Exit(1)
	}

	// http.NewServeMux is a standard request multiplexer (router).
	// It matches the URL of each incoming request against a list of registered patterns.
	mux := http.NewServeMux()

	// Metrics endpoint: Prometheus standard for observability.
	mux.Handle("GET /metrics", promhttp.Handler())

	// Health endpoint: Used by load balancers or monitoring tools (like Kubernetes Liveness/Readiness probes)
	// to verify that the application is running and responsive.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Root endpoint: The primary landing page for the application.
	// In a real-world scenario, this would likely serve a specific resource or a status overview.
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("Hello over HTTPS"))
		if err != nil {
			getLogger(r).Error("Failed to write response", "error", err)
		}
	})

	// Middleware Chain: Handlers are wrapped from inner to outer.
	// The order of execution for an incoming request is:
	// logging -> rateLimit -> securityHeaders -> requestID -> recovery -> mux (router)
	handler := recoveryMiddleware(mux)
	handler = requestIDMiddleware(handler)
	handler = securityHeadersMiddleware(handler)
	handler = rateLimitMiddleware(handler, cfg)
	handler = loggingMiddleware(handler)

	// Wrap the final handler with http.MaxBytesHandler to limit request body size.
	// This prevents memory exhaustion from excessively large request bodies.
	handler = http.MaxBytesHandler(handler, cfg.MaxBodyBytes)

	// TLS configuration for the HTTPS server.
	tlsConfig := &tls.Config{
		// Enforce modern TLS security (TLS 1.3 only for maximum security).
		MinVersion: tls.VersionTLS13,
		// NextProtos enables support for HTTP/2.
		NextProtos: []string{"h2", "http/1.1"},
	}

	// Error channel to capture fatal errors from background goroutines.
	errChan := make(chan error, 1)

	// Configure certificate management.
	if cfg.ACMEEnabled {
		// autocert.Manager handles the lifecycle of SSL/TLS certificates using Let's Encrypt.
		m := &autocert.Manager{
			Cache:      autocert.DirCache(cfg.CertCacheDir),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Domain),
		}
		tlsConfig.GetCertificate = m.GetCertificate

		// Start an HTTP server on port 80 to handle Let's Encrypt ACME challenges.
		// It also automatically redirects non-ACME traffic from HTTP to HTTPS.
		httpSrv := &http.Server{
			Addr:           cfg.HTTPPort,
			Handler:        m.HTTPHandler(nil),
			MaxHeaderBytes: cfg.MaxHeaderBytes,
			ReadTimeout:    5 * time.Second,
		}
		go func() {
			slog.Info("Starting HTTP-01 challenge responder and redirector", "addr", cfg.HTTPPort)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errChan <- fmt.Errorf("HTTP-01 challenge server failed: %w", err)
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := httpSrv.Shutdown(shutdownCtx); err != nil {
				slog.Error("HTTP-01 challenge server shutdown failed", "error", err)
			}
		}()
	} else {
		// Load certificates from disk if ACME is disabled.
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
			if err != nil {
				slog.Error("Failed to load TLS certificates", "error", err)
				os.Exit(1)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		} else {
			slog.Warn("ACME disabled and no TLS certificates provided; server will run without TLS if HTTPS_PORT is reached via HTTP")
		}
	}

	// http.Server configuration defines how the server behaves at the network level.
	s := &http.Server{
		Addr:           cfg.HTTPSPort,
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: cfg.MaxHeaderBytes,
		TLSConfig:      tlsConfig,
	}

	// Start a background goroutine to periodically clean up stale rate limiters.
	go func() {
		ticker := time.NewTicker(cfg.RateCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			for ip, limiter := range limiters {
				if time.Since(limiter.lastSeen) > cfg.RateExpiration {
					delete(limiters, ip)
				}
			}
			mu.Unlock()
		}
	}()

	// Channel to capture operating system signals for graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// Run the main HTTPS server in a background goroutine.
	go func() {
		slog.Info("Serving secure HTTPS traffic", "addr", cfg.HTTPSPort)
		var err error
		if cfg.ACMEEnabled || (cfg.TLSCertFile != "" && cfg.TLSKeyFile != "") {
			err = s.ListenAndServeTLS("", "")
		} else {
			err = s.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("HTTPS server failed: %w", err)
		}
	}()

	// Execution blocks here until a signal is received or a fatal error occurs.
	select {
	case sig := <-quit:
		slog.Info("Shutting down server...", "signal", sig.String())
	case err := <-errChan:
		slog.Error("Fatal server error", "error", err)
	}

	// Shutdown context with a grace period.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// Shutdown gracefully shuts down the server.
	if err := s.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("Server exiting")
}

// requestIDKey is a custom type for the context key to avoid collisions with other packages.
type requestIDKey struct{}

// loggerKey is a custom type for the context key to avoid collisions.
type loggerKey struct{}

// requestIDMiddleware generates a unique ID for each request and adds it to the request context
// and the response headers. It also initializes a contextual logger with the request ID for tracing.
// This is critical for debugging logs across multiple services or concurrent requests.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate a unique 8-byte ID (16 hex characters).
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			// If rand.Read fails, use current time as a fallback for entropy.
			// This is rare but ensures the server continues to function.
			slog.Error("Failed to generate random request ID", "error", err)
			b = []byte(fmt.Sprintf("%08x", time.Now().UnixNano()))[:8]
		}
		requestID := hex.EncodeToString(b)

		// Set the request ID in the response headers.
		w.Header().Set("X-Request-ID", requestID)

		// Create a contextual logger with the request ID.
		logger := slog.Default().With("request_id", requestID)

		// Add the request ID and logger to the context.
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = context.WithValue(ctx, loggerKey{}, logger)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// getLogger retrieves the contextual logger from the request.
// If no logger is found, it returns the default logger.
func getLogger(r *http.Request) *slog.Logger {
	if logger, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// recoveryMiddleware catches any panics that occur during request handling.
// It logs the panic details and returns a generic 500 Internal Server Error,
// ensuring the server doesn't crash entirely due to an unexpected bug.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log the error and the path where it happened for easier debugging.
				getLogger(r).Error("Panic recovered", "error", err, "path", r.URL.Path)
				panicsTotal.Inc()
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware injects common security headers into every response.
// This implements security best practices to protect users from common web attacks.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevents the browser from MIME-sniffing a response away from the declared content-type.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Prevents the site from being embedded in frames or iframes (Clickjacking protection).
		w.Header().Set("X-Frame-Options", "DENY")
		// Restricts where resources (scripts, images, etc.) can be loaded from.
		// Modern baseline that allows resources from 'self' and 'https:' for flexibility.
		// Added connect-src for metrics and potential API calls.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'https:'; style-src 'self' 'https:'; img-src 'self' 'https:'; font-src 'self' 'https:'; connect-src 'self' 'https:'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self';")
		// Controls how much referrer information is included with requests.
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Enforces HTTPS for the domain for one year (HSTS).
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		// Isolation headers (COOP, COEP, CORP)
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

		next.ServeHTTP(w, r)
	})
}

// clientLimiter holds a rate limiter and the time it was last accessed.
// This is used to track and potentially clean up stale limiters from memory.
type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var (
	// limiters is a map of client IP addresses to their respective rate limiters.
	limiters = make(map[string]*clientLimiter)
	// mu protects the limiters map from concurrent access.
	mu sync.Mutex
)

// getLimiter retrieves or creates a rate limiter for the given client IP address.
func getLimiter(ip string, cfg Config) *rate.Limiter {
	mu.Lock()
	defer mu.Unlock()

	v, exists := limiters[ip]
	if !exists {
		// Create a new limiter allowing requests based on configuration.
		limiter := rate.NewLimiter(rate.Limit(cfg.RateLimit), cfg.RateBurst)
		limiters[ip] = &clientLimiter{limiter, time.Now()}
		return limiter
	}

	v.lastSeen = time.Now()
	return v.limiter
}

// rateLimitMiddleware is a middleware that applies per-IP rate limiting.
// It uses a thread-safe map to maintain unique limiters for each client IP address.
func rateLimitMiddleware(next http.Handler, cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Identify the client's IP address.
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			// Fallback to RemoteAddr if SplitHostPort fails.
			ip = r.RemoteAddr
		}

		// Support for trusted proxies (e.g., Load Balancers, Cloudflare).
		// If TRUST_PROXY=true, we prioritize the X-Forwarded-For header.
		if cfg.TrustProxy {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				// X-Forwarded-For can contain multiple IPs (client, proxy1, proxy2).
				// The first IP is generally the original client IP.
				if parts := strings.Split(xff, ","); len(parts) > 0 {
					ip = strings.TrimSpace(parts[0])
				}
			}
		}

		// Retrieve or create a limiter for this specific IP.
		limiter := getLimiter(ip, cfg)
		if !limiter.Allow() {
			// Log rate limit hits for monitoring and security analysis.
			getLogger(r).Warn("Rate limit exceeded", "ip", ip)
			rateLimitHitsTotal.WithLabelValues(ip).Inc()
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// responseWriter is a wrapper around http.ResponseWriter that captures the HTTP status code
// written by the handler. This allows the logger to include the status code in the logs.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before sending it to the underlying ResponseWriter.
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware logs details about each incoming HTTP request.
// It tracks the method, path, remote address, status code, and duration.
// It also records these metrics in Prometheus for real-time monitoring.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap the ResponseWriter to capture the status code.
		// Default status code is 200 OK if not explicitly set.
		rw := &responseWriter{w, http.StatusOK}

		// Pass control to the next handler in the chain.
		next.ServeHTTP(rw, r)

		duration := time.Since(start)

		// Log the results after the request has been handled.
		getLogger(r).Info("Request handled",
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
			"status", rw.statusCode,
			"duration", duration,
		)

		// Update Prometheus metrics.
		httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(rw.statusCode)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())
	})
}
