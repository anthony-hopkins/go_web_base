package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// requestIDKey is a custom, unexported type used to store and retrieve the request ID
// from the request context. Using a dedicated type prevents key collisions with other packages.
type requestIDKey struct{}

// loggerKey is a custom, unexported type used to store and retrieve a contextual slog.Logger
// from the request context.
type loggerKey struct{}

// requestIDMiddleware generates a globally unique identifier (GUID) for every incoming request.
// This ID is injected into the response headers (X-Request-ID) and the request context.
// It also attaches a contextual logger that automatically includes the request ID in all log messages,
// enabling easy request tracing across multiple handlers and background tasks.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Generate 8 bytes of random data for the request ID.
		// We use crypto/rand for high-quality entropy to minimize collisions.
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			// Fallback: If the cryptographic RNG fails (rare), use nanosecond timestamp as entropy.
			slog.Error("Failed to generate cryptographic request ID, using timestamp fallback", "error", err)
			b = []byte(fmt.Sprintf("%08x", time.Now().UnixNano()))[:8]
		}
		requestID := hex.EncodeToString(b)

		// Set the X-Request-ID header so the client can reference this ID for debugging.
		w.Header().Set("X-Request-ID", requestID)

		// Create a logger instance that is 'pre-populated' with the current request ID.
		// All subsequent log calls using this logger will have this ID attached as metadata.
		logger := slog.Default().With("request_id", requestID)

		// Store both the raw ID and the logger in the request context for downstream handlers.
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = context.WithValue(ctx, loggerKey{}, logger)

		// Continue the middleware chain with the newly enriched context.
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// getLogger is a helper function that safely retrieves the contextual logger
// from an http.Request. If no logger is found (e.g., in tests), it returns the default logger.
func getLogger(r *http.Request) *slog.Logger {
	if logger, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// recoveryMiddleware provides a safety net for the application. It uses a deferred 'recover()'
// to catch any runtime panics during request processing, logs the error with its request ID
// and stack trace context, and returns a generic '500 Internal Server Error' to the client.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Log the panic details using the contextual logger.
				getLogger(r).Error("Request handler panicked", "error", err, "path", r.URL.Path)

				// Increment the Prometheus panic counter for monitoring alerts.
				panicsTotal.Inc()

				// Ensure the client receives a valid HTTP error response.
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware enforces modern security best practices by injecting
// various HTTP security headers into every outgoing response. These headers help
// protect against Cross-Site Scripting (XSS), Clickjacking, and other common attacks.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent browsers from MIME-sniffing the response content.
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Prevent the site from being embedded in frames/iframes on other sites (clickjacking protection).
		w.Header().Set("X-Frame-Options", "DENY")

		// Content Security Policy (CSP): Restrict where scripts, styles, and other resources can be loaded from.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'https:'; style-src 'self' 'https:'; img-src 'self' 'https:'; font-src 'self' 'https:'; connect-src 'self' 'https:'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self';")

		// Control how much referrer information is passed when navigating away from the site.
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// HTTP Strict Transport Security (HSTS): Tell browsers to only use HTTPS for the next year.
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")

		// Modern cross-origin isolation headers.
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")

		next.ServeHTTP(w, r)
	})
}

// clientLimiter wraps a standard Go rate limiter with a timestamp of the last time it was accessed.
// This allows the server to identify and remove stale limiters to free up memory.
type clientLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

var (
	// limiters is a global thread-safe map that stores rate limiters for every unique client IP.
	limiters = make(map[string]*clientLimiter)
	// mu is a mutex used to synchronize access to the limiters map.
	mu sync.Mutex
)

// getLimiter retrieves an existing rate limiter for a specific IP address or creates a new one
// if it's the first time seeing this client. It also updates the 'lastSeen' timestamp.
func getLimiter(ip string, cfg Config) *rate.Limiter {
	mu.Lock()
	defer mu.Unlock()

	v, exists := limiters[ip]
	if !exists {
		// Create a new token-bucket limiter based on the application's configuration.
		limiter := rate.NewLimiter(rate.Limit(cfg.RateLimit), cfg.RateBurst)
		limiters[ip] = &clientLimiter{limiter, time.Now()}
		return limiter
	}

	// Update the last seen time for cache eviction logic.
	v.lastSeen = time.Now()
	return v.limiter
}

// rateLimitMiddleware implements per-IP request throttling. This helps protect the server
// from unintentional over-usage or malicious Denial of Service (DoS) attacks.
func rateLimitMiddleware(next http.Handler, cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract the client's IP address from the request.
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		// If the server is behind a Load Balancer or Proxy (e.g., Cloudflare, Nginx),
		// we may need to trust the 'X-Forwarded-For' header to find the real client IP.
		if cfg.TrustProxy {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				if parts := strings.Split(xff, ","); len(parts) > 0 {
					ip = strings.TrimSpace(parts[0])
				}
			}
		}

		// Check if the current IP address has exceeded its allowed request rate.
		limiter := getLimiter(ip, cfg)
		if !limiter.Allow() {
			getLogger(r).Warn("Rate limit exceeded by client", "ip", ip)

			// Increment the Prometheus metric for rate limit hits.
			rateLimitHitsTotal.WithLabelValues(ip).Inc()

			// Return a '429 Too Many Requests' error.
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// responseWriter is a custom wrapper around http.ResponseWriter.
// It captures the HTTP status code sent back to the client, allowing the
// logging middleware to record it accurately.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code before passing it to the underlying response writer.
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// loggingMiddleware provides high-level observability for every request.
// It records:
// 1. Request execution time (latency).
// 2. HTTP method, path, and remote address.
// 3. Response status code.
// It also updates Prometheus metrics (http_requests_total and http_request_duration_seconds).
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap the standard ResponseWriter with our custom responseWriter to capture the status code.
		// Default to 200 OK if WriteHeader is never called.
		rw := &responseWriter{w, http.StatusOK}

		// Execute the next handler in the middleware chain.
		next.ServeHTTP(rw, r)

		// Calculate the total duration of the request.
		duration := time.Since(start)

		// Extract the contextual logger that includes the unique request ID.
		logger := getLogger(r)

		// Log the structured request data.
		logger.Info("Request handled",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration", duration,
			"ip", r.RemoteAddr,
		)

		// Update Prometheus metrics for observability.
		statusStr := strconv.Itoa(rw.statusCode)
		httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, statusStr).Inc()
		httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())
	})
}
