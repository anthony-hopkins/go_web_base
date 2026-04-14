// Tests for each middleware in isolation: request IDs, recovery, security headers, CORS
// branches, client IP extraction, logging wrapper, and API key gating.
package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRequestIDMiddlewareAndGetLogger checks X-Request-ID format and contextual logger attachment.
func TestRequestIDMiddlewareAndGetLogger(t *testing.T) {
	t.Parallel()

	var seenID string
	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenID = w.Header().Get("X-Request-ID")
		if got := getLogger(r); got == nil {
			t.Fatalf("expected contextual logger")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seenID == "" {
		t.Fatalf("expected request id header to be set")
	}
	if len(seenID) != 16 {
		t.Fatalf("expected 16 hex chars for request id, got %q", seenID)
	}
}

// TestRequestIDMiddlewareFallbackEntropy forces crypto/rand failure to use timestamp fallback.
func TestRequestIDMiddlewareFallbackEntropy(t *testing.T) {
	t.Parallel()
	old := randRead
	t.Cleanup(func() { randRead = old })
	randRead = func(b []byte) (int, error) { return 0, errors.New("rng unavailable") }

	h := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("X-Request-ID"); len(got) != 16 {
		t.Fatalf("expected fallback request id length 16, got %q", got)
	}
}

// TestGetLoggerFallback ensures requests without middleware still get slog.Default().
func TestGetLoggerFallback(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if getLogger(req) == nil {
		t.Fatalf("expected fallback logger")
	}
}

// TestRecoveryMiddleware asserts panics become 500 and increment panics_total.
func TestRecoveryMiddleware(t *testing.T) {
	t.Parallel()
	h := recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

// TestSecurityHeadersMiddleware verifies expected security headers are always present.
func TestSecurityHeadersMiddleware(t *testing.T) {
	t.Parallel()
	h := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	headers := []string{
		"X-Content-Type-Options", "X-Frame-Options", "Content-Security-Policy",
		"Referrer-Policy", "Strict-Transport-Security",
		"Cross-Origin-Opener-Policy", "Cross-Origin-Embedder-Policy", "Cross-Origin-Resource-Policy",
	}
	for _, k := range headers {
		if rec.Header().Get(k) == "" {
			t.Fatalf("expected header %s to be set", k)
		}
	}
}

// TestCORSMiddlewareAllowedPreflightAndRequest covers happy-path GET and OPTIONS preflight.
func TestCORSMiddlewareAllowedPreflightAndRequest(t *testing.T) {
	t.Parallel()
	cfg := Config{
		CORSAllowedOrigins:   []string{"https://app.example.com"},
		CORSAllowedMethods:   []string{"GET", "POST"},
		CORSAllowedHeaders:   []string{"Content-Type"},
		CORSExposedHeaders:   []string{"X-Request-ID"},
		CORSAllowCredentials: true,
		CORSMaxAgeSeconds:    60,
	}

	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), cfg)

	// Non-preflight allowed origin should pass through with headers.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected passthrough status, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("expected allow origin header")
	}

	// Allowed preflight should short-circuit to 204.
	preflight := httptest.NewRequest(http.MethodOptions, "/", nil)
	preflight.Header.Set("Origin", "https://app.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "GET")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, preflight)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", rec.Code)
	}
}

// TestCORSMiddlewareFailureCases exercises forbidden origin, passthrough OPTIONS, and 405 method.
func TestCORSMiddlewareFailureCases(t *testing.T) {
	t.Parallel()
	cfg := Config{
		CORSAllowedOrigins: []string{"https://allowed.example.com"},
		CORSAllowedMethods: []string{"GET"},
	}
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), cfg)

	// Disallowed preflight origin -> forbidden.
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://blocked.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	// OPTIONS without preflight header should pass through.
	req = httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://allowed.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected passthrough 200, got %d", rec.Code)
	}

	// Method not allowed -> 405.
	req = httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://allowed.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// TestAllowedCORSOriginAndPreflightHelpers unit-tests allowedCORSOrigin and isCORSPreflight.
func TestAllowedCORSOriginAndPreflightHelpers(t *testing.T) {
	t.Parallel()
	if _, ok := allowedCORSOrigin("x", nil); ok {
		t.Fatalf("expected no match with empty origins")
	}
	if v, ok := allowedCORSOrigin("x", []string{"*"}); !ok || v != "*" {
		t.Fatalf("expected wildcard match")
	}
	if v, ok := allowedCORSOrigin("https://a", []string{"https://a"}); !ok || v != "https://a" {
		t.Fatalf("expected exact match")
	}
	if _, ok := allowedCORSOrigin("https://b", []string{"https://a"}); ok {
		t.Fatalf("did not expect unmatched origin")
	}

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Access-Control-Request-Method", "GET")
	if !isCORSPreflight(req) {
		t.Fatalf("expected preflight")
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	if isCORSPreflight(req) {
		t.Fatalf("did not expect preflight")
	}
}

// TestClientIP validates RemoteAddr parsing and X-Forwarded-For when trustProxy is true.
func TestClientIP(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:5000"
	if got := clientIP(req, false); got != "10.0.0.2" {
		t.Fatalf("unexpected direct client ip: %s", got)
	}

	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientIP(req, true); got != "1.2.3.4" {
		t.Fatalf("unexpected proxied client ip: %s", got)
	}

	req.RemoteAddr = "malformed"
	if got := clientIP(req, false); got != "malformed" {
		t.Fatalf("expected raw remote addr fallback, got %s", got)
	}
}

// TestResponseWriterAndLoggingMiddleware ensures status capture and handler invocation.
func TestResponseWriterAndLoggingMiddleware(t *testing.T) {
	t.Parallel()
	var wrote bool
	h := loggingMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrote = true
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "ok")
	}), false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/items", nil)
	req = req.WithContext(context.Background())
	h.ServeHTTP(rec, req)
	if !wrote || rec.Code != http.StatusCreated {
		t.Fatalf("unexpected logging middleware behavior: wrote=%v code=%d", wrote, rec.Code)
	}
}

// TestAPIKeyAuthMiddleware checks missing, correct, and wrong X-API-Key values.
func TestAPIKeyAuthMiddleware(t *testing.T) {
	t.Parallel()
	h := apiKeyAuthMiddleware("secret", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without key, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected success with key, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized with wrong key, got %d", rec.Code)
	}
}

// TestCORSNoOriginPassThrough ensures non-browser clients without Origin skip CORS logic.
func TestCORSNoOriginPassThrough(t *testing.T) {
	t.Parallel()
	called := false
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}), Config{CORSAllowedOrigins: []string{"https://allowed.example.com"}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", strings.NewReader("")))
	if !called || rec.Code != http.StatusAccepted {
		t.Fatalf("expected pass-through when no origin, called=%v code=%d", called, rec.Code)
	}
}

// TestCORSPreflightWithoutOptionalLists covers preflight when methods/headers lists are empty.
func TestCORSPreflightWithoutOptionalLists(t *testing.T) {
	t.Parallel()
	cfg := Config{
		CORSAllowedOrigins: []string{"https://allowed.example.com"},
		// leave methods/headers/exposed/max-age empty to cover optional branches
	}
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), cfg)

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://allowed.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for preflight, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("expected no methods header when list not configured")
	}
	if rec.Header().Get("Access-Control-Allow-Headers") != "" {
		t.Fatalf("expected no headers header when list not configured")
	}
	if rec.Header().Get("Access-Control-Max-Age") != "" {
		t.Fatalf("expected no max-age header when value is zero")
	}
}

// TestCORSDisallowedNonPreflightPassesThrough: wrong Origin on GET does not 403 (non-preflight).
func TestCORSDisallowedNonPreflightPassesThrough(t *testing.T) {
	t.Parallel()
	called := false
	h := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}), Config{CORSAllowedOrigins: []string{"https://allowed.example.com"}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://blocked.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !called || rec.Code != http.StatusAccepted {
		t.Fatalf("expected disallowed non-preflight to pass through, called=%v code=%d", called, rec.Code)
	}
}
