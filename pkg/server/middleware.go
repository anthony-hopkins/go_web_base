package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

var randRead = rand.Read

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
		if _, err := randRead(b); err != nil {
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

// corsMiddleware enforces explicit cross-origin browser access rules.
// It supports exact-origin allowlists, optional wildcard mode ("*"), credential gating,
// and full CORS preflight responses for OPTIONS requests.
func corsMiddleware(next http.Handler, cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		allowedOrigin, ok := allowedCORSOrigin(origin, cfg.CORSAllowedOrigins)
		if !ok {
			if isCORSPreflight(r) {
				http.Error(w, "Forbidden origin", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		headers := w.Header()
		headers.Set("Vary", "Origin")
		headers.Set("Access-Control-Allow-Origin", allowedOrigin)
		if cfg.CORSAllowCredentials {
			headers.Set("Access-Control-Allow-Credentials", "true")
		}
		if len(cfg.CORSExposedHeaders) > 0 {
			headers.Set("Access-Control-Expose-Headers", strings.Join(cfg.CORSExposedHeaders, ", "))
		}

		if !isCORSPreflight(r) {
			next.ServeHTTP(w, r)
			return
		}

		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		if len(cfg.CORSAllowedMethods) > 0 && !slices.Contains(cfg.CORSAllowedMethods, requestedMethod) {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		headers.Add("Vary", "Access-Control-Request-Method")
		headers.Add("Vary", "Access-Control-Request-Headers")
		if len(cfg.CORSAllowedMethods) > 0 {
			headers.Set("Access-Control-Allow-Methods", strings.Join(cfg.CORSAllowedMethods, ", "))
		}
		if len(cfg.CORSAllowedHeaders) > 0 {
			headers.Set("Access-Control-Allow-Headers", strings.Join(cfg.CORSAllowedHeaders, ", "))
		}
		if cfg.CORSMaxAgeSeconds > 0 {
			headers.Set("Access-Control-Max-Age", strconv.Itoa(cfg.CORSMaxAgeSeconds))
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func allowedCORSOrigin(origin string, allowedOrigins []string) (string, bool) {
	if len(allowedOrigins) == 0 {
		return "", false
	}
	if slices.Contains(allowedOrigins, "*") {
		return "*", true
	}
	if slices.Contains(allowedOrigins, origin) {
		return origin, true
	}
	return "", false
}

func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// clientIP returns the best-effort client address for logging. When trustProxy is true
// (typical when Nginx or another reverse proxy terminates TLS and forwards requests),
// the first hop in X-Forwarded-For is used; otherwise TCP RemoteAddr is parsed.
func clientIP(r *http.Request, trustProxy bool) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if parts := strings.Split(xff, ","); len(parts) > 0 {
				ip = strings.TrimSpace(parts[0])
			}
		}
	}
	return ip
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
func loggingMiddleware(next http.Handler, trustProxy bool) http.Handler {
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
			"ip", clientIP(r, trustProxy),
		)

		// Update Prometheus metrics for observability.
		statusStr := strconv.Itoa(rw.statusCode)
		httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, statusStr).Inc()
		httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())
	})
}

// apiKeyAuthMiddleware enforces endpoint-level authentication using a shared API key.
// The key must be supplied in the X-API-Key header.
func apiKeyAuthMiddleware(expectedKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providedKey := r.Header.Get("X-API-Key")
		if len(providedKey) == 0 || subtle.ConstantTimeCompare([]byte(providedKey), []byte(expectedKey)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
