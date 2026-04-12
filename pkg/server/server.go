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

// Start orchestrates the complete server lifecycle. It performs the following steps:
// 1. Registers the /metrics endpoint for Prometheus scraping.
// 2. Initializes the middleware stack (Recovery, Request ID, Security, Rate Limiting, Logging).
// 3. Configures TLS settings, loading manual certificates if provided.
// 4. Launches a background routine to clean up stale rate limiters.
// 5. Starts the main HTTP(S) listener in a goroutine.
// 6. Blocks until an OS termination signal is received, then initiates a graceful shutdown.
func (s *Server) Start() error {
	// Register the Prometheus metrics endpoint.
	// This allows monitoring tools to scrape data about server performance and health.
	s.mux.Handle("GET /metrics", promhttp.Handler())

	// Build the Middleware Chain.
	// The order of execution is from the bottom of this list to the top (outermost to innermost).
	// 1. recoveryMiddleware: Catches and logs panics.
	// 2. requestIDMiddleware: Assigns a unique ID to each request.
	// 3. securityHeadersMiddleware: Adds security-related HTTP headers.
	// 4. rateLimitMiddleware: Throttles requests based on client IP.
	// 5. loggingMiddleware: Records request details and metrics.
	handler := recoveryMiddleware(s.mux)
	handler = requestIDMiddleware(handler)
	handler = securityHeadersMiddleware(handler)
	handler = rateLimitMiddleware(handler, s.cfg)
	handler = loggingMiddleware(handler)

	// Wrap the final handler with MaxBytesHandler.
	// This provides a hard limit on the request body size at the network level,
	// protecting against memory exhaustion attacks.
	handler = http.MaxBytesHandler(handler, s.cfg.MaxBodyBytes)

	// Configure secure TLS defaults.
	// We enforce TLS 1.3 as the minimum version for better security and performance.
	// NextProtos enables HTTP/2 support via ALPN.
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{"h2", "http/1.1"},
	}

	// errChan allows goroutines to report fatal startup errors back to the main thread.
	errChan := make(chan error, 1)

	// --- TLS Configuration ---
	if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
		// Attempt to load the X.509 certificate and private key from the specified files.
		cert, err := tls.LoadX509KeyPair(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		if err != nil {
			return fmt.Errorf("failed to load custom TLS certificates: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	} else {
		// If no certificates are provided, the server will fall back to insecure HTTP.
		slog.Warn("No custom certificates provided; running in insecure mode (HTTP)")
	}

	// Initialize the underlying http.Server with production-ready timeouts.
	s.server = &http.Server{
		Addr:           s.cfg.HTTPSPort,
		Handler:        handler,
		ReadTimeout:    10 * time.Second,  // Max duration for reading the entire request.
		WriteTimeout:   10 * time.Second,  // Max duration before timing out writes of the response.
		IdleTimeout:    120 * time.Second, // Max time to wait for the next request when keep-alive is enabled.
		MaxHeaderBytes: s.cfg.MaxHeaderBytes,
		TLSConfig:      tlsConfig,
	}

	// --- Background Maintenance ---
	// Start a dedicated goroutine for periodic memory cleanup.
	go func() {
		ticker := time.NewTicker(s.cfg.RateCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			// Iterate through the limiters map and remove entries that haven't been seen recently.
			for ip, limiter := range limiters {
				if time.Since(limiter.lastSeen) > s.cfg.RateExpiration {
					delete(limiters, ip)
				}
			}
			mu.Unlock()
		}
	}()

	// --- Graceful Shutdown Setup ---
	// Notify the 'quit' channel on OS interrupt or termination signals.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// Start the server in a non-blocking goroutine.
	go func() {
		isTLS := s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != ""
		slog.Info("Starting primary server", "addr", s.cfg.HTTPSPort, "tls", isTLS)

		var err error
		if isTLS {
			// ListenAndServeTLS starts the server with HTTPS.
			// Cert/Key files are empty here because they were already loaded into tlsConfig.
			err = s.server.ListenAndServeTLS("", "")
		} else {
			// ListenAndServe starts the server with plain HTTP.
			err = s.server.ListenAndServe()
		}

		// http.ErrServerClosed is returned when Shutdown() is called, so it's not a fatal error.
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- fmt.Errorf("Primary server failed: %w", err)
		}
	}()

	// Wait for a shutdown signal or a fatal error from the server goroutine.
	select {
	case sig := <-quit:
		slog.Info("Termination signal received, initiating graceful shutdown", "signal", sig.String())
	case err := <-errChan:
		return err
	}

	// Create a context for the shutdown process with a configured timeout.
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	// Shutdown() gracefully stops the server by closing all listeners and
	// then waiting for active connections to become idle or for the context to timeout.
	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("server forced to shutdown: %w", err)
	}

	slog.Info("Server successfully stopped")
	return nil
}
