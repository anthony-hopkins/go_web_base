package server

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"github.com/joho/godotenv"
)

// Config holds the application configuration loaded from environment variables.
// It centralizes all settings needed for server operation and security.
// Request rate limiting is expected to be enforced by the reverse proxy (e.g. Nginx), not in-process.
// Struct tags can be used for automated configuration binding or documentation.
type Config struct {
	APIKey              string        // APIKey is a mandatory security setting used for internal or secure requests.
	Domain              string        // Domain is the target domain name for the server, used in host whitelisting or identifying the service.
	HTTPSPort           string        // HTTPSPort is the network address (e.g., ":443") where the server listens for secure HTTPS traffic.
	TLSCertFile         string        // TLSCertFile is the file path to the manually-provided X.509 certificate file.
	TLSKeyFile          string        // TLSKeyFile is the file path to the private key matching the manually-provided certificate.
	TrustProxy          bool          // TrustProxy, when true, uses X-Forwarded-For (first hop) for the client IP in structured logs (use behind Nginx/reverse proxies).
	MaxHeaderBytes      int           // MaxHeaderBytes specifies the maximum size in bytes that the server will accept in HTTP headers.
	MaxBodyBytes        int64         // MaxBodyBytes restricts the maximum allowed size of the HTTP request body to prevent memory issues.
	ShutdownTimeout     time.Duration // ShutdownTimeout is the maximum time to allow for active requests to finish during a graceful shutdown.
	CORSAllowedOrigins  []string      // CORSAllowedOrigins defines which browser origins may call this API (exact match or "*" when credentials are off).
	CORSAllowedMethods  []string      // CORSAllowedMethods defines allowed CORS request methods for preflight and response headers.
	CORSAllowedHeaders  []string      // CORSAllowedHeaders defines allowed request headers for CORS preflight checks.
	CORSExposedHeaders  []string      // CORSExposedHeaders defines response headers exposed to browser JavaScript.
	CORSAllowCredentials bool         // CORSAllowCredentials controls Access-Control-Allow-Credentials for trusted cross-origin calls.
	CORSMaxAgeSeconds   int           // CORSMaxAgeSeconds caches successful preflight checks in browsers.
}

// LoadConfig attempts to read configuration from environment variables.
// It tries to load a '.env' file from the current directory if it exists, using the 'godotenv' library.
// It uses Go 1.26's 'cmp.Or' for providing default values for optional settings.
func LoadConfig() (Config, error) {
	// Attempt to load settings from a local .env file.
	if err := godotenv.Load(); err != nil {
		// Log a warning if the file is missing; the application will still attempt
		// to load settings from system-level environment variables.
		slog.Warn("No .env file found, relying on system environment variables")
	}

	// API_KEY is a mandatory security setting.
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return Config{}, errors.New("API_KEY environment variable is required")
	}

	// DOMAIN is required for server identity and potentially for host-based filtering.
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return Config{}, errors.New("DOMAIN environment variable is required")
	}

	// Optional configurations with safe defaults using cmp.Or.
	// Default to standard HTTPS port :443 if not specified.
	httpsPort := cmp.Or(os.Getenv("HTTPS_PORT"), ":443")

	// TLS certificate and key file paths.
	tlsCertFile := os.Getenv("TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("TLS_KEY_FILE")

	// Parse the TRUST_PROXY boolean flag. Defaults to false.
	trustProxy, _ := strconv.ParseBool(cmp.Or(os.Getenv("TRUST_PROXY"), "false"))

	// Parse numeric settings for request limits with detailed error reporting.
	// MAX_HEADER_BYTES: Default 1MB.
	maxHeaderBytes, err := strconv.Atoi(cmp.Or(os.Getenv("MAX_HEADER_BYTES"), "1048576"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_HEADER_BYTES: %w", err)
	}

	// MAX_BODY_BYTES: Default 10MB.
	maxBodyBytes, err := strconv.ParseInt(cmp.Or(os.Getenv("MAX_BODY_BYTES"), "10485760"), 10, 64)
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_BODY_BYTES: %w", err)
	}

	// Parse duration-based settings for lifecycle and cleanup.
	// SHUTDOWN_TIMEOUT: Default 30 seconds.
	shutdownTimeout, err := time.ParseDuration(cmp.Or(os.Getenv("SHUTDOWN_TIMEOUT"), "30s"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid SHUTDOWN_TIMEOUT: %w", err)
	}

	corsAllowedOrigins := parseCSV(os.Getenv("CORS_ALLOWED_ORIGINS"))
	corsAllowedMethods := parseCSV(cmp.Or(os.Getenv("CORS_ALLOWED_METHODS"), "GET,POST,PUT,PATCH,DELETE,OPTIONS"))
	corsAllowedHeaders := parseCSV(cmp.Or(os.Getenv("CORS_ALLOWED_HEADERS"), "Accept,Authorization,Content-Type,X-API-Key,X-Requested-With"))
	corsExposedHeaders := parseCSV(cmp.Or(os.Getenv("CORS_EXPOSED_HEADERS"), "X-Request-ID"))
	corsAllowCredentials, err := strconv.ParseBool(cmp.Or(os.Getenv("CORS_ALLOW_CREDENTIALS"), "false"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid CORS_ALLOW_CREDENTIALS: %w", err)
	}
	corsMaxAgeSeconds, err := strconv.Atoi(cmp.Or(os.Getenv("CORS_MAX_AGE_SECONDS"), "600"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid CORS_MAX_AGE_SECONDS: %w", err)
	}
	if corsMaxAgeSeconds < 0 {
		return Config{}, errors.New("CORS_MAX_AGE_SECONDS must be >= 0")
	}
	if corsAllowCredentials && slices.Contains(corsAllowedOrigins, "*") {
		return Config{}, errors.New("CORS_ALLOWED_ORIGINS cannot contain '*' when CORS_ALLOW_CREDENTIALS=true")
	}

	// Return the fully populated Config struct.
	return Config{
		APIKey:               apiKey,
		Domain:               domain,
		HTTPSPort:            httpsPort,
		TLSCertFile:          tlsCertFile,
		TLSKeyFile:           tlsKeyFile,
		TrustProxy:           trustProxy,
		MaxHeaderBytes:       maxHeaderBytes,
		MaxBodyBytes:         maxBodyBytes,
		ShutdownTimeout:      shutdownTimeout,
		CORSAllowedOrigins:   corsAllowedOrigins,
		CORSAllowedMethods:   corsAllowedMethods,
		CORSAllowedHeaders:   corsAllowedHeaders,
		CORSExposedHeaders:   corsExposedHeaders,
		CORSAllowCredentials: corsAllowCredentials,
		CORSMaxAgeSeconds:    corsMaxAgeSeconds,
	}, nil
}

func parseCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	rawValues := strings.Split(value, ",")
	values := make([]string, 0, len(rawValues))
	for _, raw := range rawValues {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		values = append(values, trimmed)
	}
	return values
}
