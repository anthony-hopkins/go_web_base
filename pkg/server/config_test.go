// Tests for LoadConfig and parseCSV: default expansion, validation errors, and CORS edge cases.
package server

import (
	"os"
	"strings"
	"testing"
)

// TestParseCSV verifies trimming, empty segments, and nil vs empty for unset env.
func TestParseCSV(t *testing.T) {
	t.Parallel()

	if got := parseCSV(""); got != nil {
		t.Fatalf("expected nil for empty input, got %#v", got)
	}

	got := parseCSV(" one, two , ,three ")
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("unexpected length: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected item at %d: got %q want %q", i, got[i], want[i])
		}
	}
}

// TestLoadConfigSuccessAndDefaults checks required vars and representative defaults.
func TestLoadConfigSuccessAndDefaults(t *testing.T) {
	t.Setenv("API_KEY", "test-key")
	t.Setenv("DOMAIN", "example.com")
	t.Setenv("HTTPS_PORT", "")
	t.Setenv("TLS_CERT_FILE", "")
	t.Setenv("TLS_KEY_FILE", "")
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("MAX_HEADER_BYTES", "2048")
	t.Setenv("MAX_BODY_BYTES", "4096")
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("CORS_ALLOWED_METHODS", "GET,POST")
	t.Setenv("CORS_ALLOWED_HEADERS", "Content-Type")
	t.Setenv("CORS_EXPOSED_HEADERS", "X-Request-ID")
	t.Setenv("CORS_ALLOW_CREDENTIALS", "false")
	t.Setenv("CORS_MAX_AGE_SECONDS", "120")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}

	if cfg.APIKey != "test-key" || cfg.Domain != "example.com" {
		t.Fatalf("unexpected required config values: %#v", cfg)
	}
	if cfg.HTTPSPort != ":443" {
		t.Fatalf("expected default https port :443, got %q", cfg.HTTPSPort)
	}
	if !cfg.TrustProxy || cfg.MaxHeaderBytes != 2048 || cfg.MaxBodyBytes != 4096 {
		t.Fatalf("unexpected numeric/bool parsing: %#v", cfg)
	}
	if cfg.ShutdownTimeout.String() != "5s" {
		t.Fatalf("unexpected shutdown timeout: %s", cfg.ShutdownTimeout)
	}
}

// TestLoadConfigValidationErrors table-tests validation failures and CORS credential rules.
func TestLoadConfigValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantError string
	}{
		{
			name:      "missing API key",
			env:       map[string]string{"API_KEY": "", "DOMAIN": "example.com"},
			wantError: "API_KEY environment variable is required",
		},
		{
			name:      "missing domain",
			env:       map[string]string{"API_KEY": "k", "DOMAIN": ""},
			wantError: "DOMAIN environment variable is required",
		},
		{
			name: "invalid max header",
			env: map[string]string{
				"API_KEY":          "k",
				"DOMAIN":           "example.com",
				"MAX_HEADER_BYTES": "not-a-number",
			},
			wantError: "invalid MAX_HEADER_BYTES",
		},
		{
			name: "invalid max body",
			env: map[string]string{
				"API_KEY":        "k",
				"DOMAIN":         "example.com",
				"MAX_BODY_BYTES": "oops",
			},
			wantError: "invalid MAX_BODY_BYTES",
		},
		{
			name: "invalid shutdown timeout",
			env: map[string]string{
				"API_KEY":          "k",
				"DOMAIN":           "example.com",
				"SHUTDOWN_TIMEOUT": "invalid",
			},
			wantError: "invalid SHUTDOWN_TIMEOUT",
		},
		{
			name: "invalid cors allow credentials",
			env: map[string]string{
				"API_KEY":                "k",
				"DOMAIN":                 "example.com",
				"CORS_ALLOW_CREDENTIALS": "invalid",
			},
			wantError: "invalid CORS_ALLOW_CREDENTIALS",
		},
		{
			name: "invalid cors max age",
			env: map[string]string{
				"API_KEY":              "k",
				"DOMAIN":               "example.com",
				"CORS_MAX_AGE_SECONDS": "invalid",
			},
			wantError: "invalid CORS_MAX_AGE_SECONDS",
		},
		{
			name: "negative cors max age",
			env: map[string]string{
				"API_KEY":              "k",
				"DOMAIN":               "example.com",
				"CORS_MAX_AGE_SECONDS": "-1",
			},
			wantError: "CORS_MAX_AGE_SECONDS must be >=",
		},
		{
			name: "wildcard with credentials",
			env: map[string]string{
				"API_KEY":                "k",
				"DOMAIN":                 "example.com",
				"CORS_ALLOWED_ORIGINS":   "*",
				"CORS_ALLOW_CREDENTIALS": "true",
			},
			wantError: "cannot contain '*'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := LoadConfig()
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
			}
		})
	}
}

// clearConfigEnv resets known config-related env keys so parallel tests do not leak state.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"API_KEY", "DOMAIN", "HTTPS_PORT", "TLS_CERT_FILE", "TLS_KEY_FILE",
		"TRUST_PROXY", "MAX_HEADER_BYTES", "MAX_BODY_BYTES", "SHUTDOWN_TIMEOUT",
		"CORS_ALLOWED_ORIGINS", "CORS_ALLOWED_METHODS", "CORS_ALLOWED_HEADERS",
		"CORS_EXPOSED_HEADERS", "CORS_ALLOW_CREDENTIALS", "CORS_MAX_AGE_SECONDS",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
	_ = os.Unsetenv("PWD")
}
