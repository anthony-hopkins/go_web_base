package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/acme/autocert"
)

// Server represents the core HTTP/HTTPS server component.
// It manages the request router (mux), the underlying http.Server,
// and the application's configuration.
type Server struct {
	cfg    Config
	mux    *http.ServeMux
	server *http.Server
}

// New initializes a new Server instance with a fresh ServeMux and the provided configuration.
func New(cfg Config) *Server {
	mux := http.NewServeMux()
	return &Server{
		cfg: cfg,
		mux: mux,
	}
}

// Handle registers a standardized http.Handler for a specific pattern (e.g., "GET /api/v1/resource").
func (s *Server) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// HandleFunc registers a simple function as a request handler for a specific pattern.
func (s *Server) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	s.mux.HandleFunc(pattern, handler)
}

// Start orchestrates the complete server lifecycle. It performs the following:
// 1. Configures the /metrics endpoint for Prometheus.
// 2. Builds the middleware chain (Recovery, Request ID, Security Headers, Rate Limiting, Logging).
// 3. Configures TLS (either via Let's Encrypt/ACME or manual certificates).
// 4. Starts a background cleaner for the rate limiters.
// 5. Listens for OS interrupt signals to perform a graceful shutdown.
func (s *Server) Start() error {
	// Register the Prometheus metrics endpoint.
	// This is where monitoring tools like Prometheus or Grafana scrape data.
	s.mux.Handle("GET /metrics", promhttp.Handler())

	// Initialize the Middleware Chain in reverse order of execution.
	// Logging is the outermost layer, followed by rate limiting, security headers, etc.
	handler := recoveryMiddleware(s.mux)
	handler = requestIDMiddleware(handler)
	handler = securityHeadersMiddleware(handler)
	handler = rateLimitMiddleware(handler, s.cfg)
	handler = loggingMiddleware(handler)

	// Wrap the final handler with MaxBytesHandler to prevent large payload attacks
	// that could lead to memory exhaustion.
	handler = http.MaxBytesHandler(handler, s.cfg.MaxBodyBytes)

	// Define secure TLS defaults (TLS 1.3 only, HTTP/2 and HTTP/1.1 support).
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2", "http/1.1"},
	}

	errChan := make(chan error, 1)

	// --- TLS Configuration Logic ---
	if s.cfg.ACMEEnabled {
		// Set up Let's Encrypt (ACME) automated certificate management.
		m := &autocert.Manager{
			Cache:      autocert.DirCache(s.cfg.CertCacheDir), // Persist certificates on disk.
			Prompt:     autocert.AcceptTOS,                    // Automatically accept Let's Encrypt TOS.
			HostPolicy: autocert.HostWhitelist(s.cfg.Domain),  // Only issue certs for our configured domain.
		}
		tlsConfig.GetCertificate = m.GetCertificate

		// Start a secondary HTTP server to handle ACME HTTP-01 challenges and
		// potentially redirect HTTP to HTTPS.
		httpSrv := &http.Server{
			Addr:           s.cfg.HTTPPort,
			Handler:        m.HTTPHandler(nil),
			MaxHeaderBytes: s.cfg.MaxHeaderBytes,
			ReadTimeout:    5 * time.Second,
		}
		go func() {
			slog.Info("Starting ACME challenge responder", "addr", s.cfg.HTTPPort)
			if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errChan <- fmt.Errorf("HTTP-01 challenge server failed: %w", err)
			}
		}()
		// Ensure the challenge server is shut down when Start() returns.
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := httpSrv.Shutdown(shutdownCtx); err != nil {
				slog.Error("ACME challenge server shutdown failed", "error", err)
			}
		}()
	} else if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
		// Load certificates manually if ACME is disabled but paths are provided.
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("failed to load custom TLS certificates: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	} else {
		slog.Warn("ACME disabled and no custom certificates provided; running in insecure/hybrid mode")
	}

	// Initialize the primary HTTPS server.
	s.server = &http.Server{
		Addr:           s.cfg.HTTPSPort,
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: s.cfg.MaxHeaderBytes,
		TLSConfig:      tlsConfig,
	}

	// --- Background Maintenance Tasks ---
	// Start a goroutine to periodically clean up expired rate limiters from memory.
	go func() {
		ticker := time.NewTicker(s.cfg.RateCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			for ip, limiter := range limiters {
				if time.Since(limiter.lastSeen) > s.cfg.RateExpiration {
					delete(limiters, ip)
				}
			}
			mu.Unlock()
		}
	}()

	// --- Graceful Shutdown Setup ---
	// Create a channel to catch OS signals for graceful termination.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// Launch the primary server in its own goroutine so we can block on the quit channel.
	go func() {
		slog.Info("Starting primary server", "addr", s.cfg.HTTPSPort, "tls", (s.cfg.ACMEEnabled || s.cfg.TLSCertFile != ""))
		var err error
		if s.cfg.ACMEEnabled || (s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "") {
			err = s.server.ListenAndServeTLS("", "")
		} else {
			err = s.server.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("Primary server failed: %w", err)
		}
	}()

	// Block until we receive a termination signal or a fatal error occurs.
	select {
	case sig := <-quit:
		slog.Info("Termination signal received, initiating graceful shutdown", "signal", sig.String())
	case err := <-errChan:
		return err
	}

	// Create a context with a timeout for the shutdown process.
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	// Shut down the server, giving active connections time to finish.
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("server forced to shutdown: %w", err)
	}

	slog.Info("Server successfully stopped")
	return nil
}
