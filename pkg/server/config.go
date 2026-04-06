package server

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"log/slog"

	"github.com/joho/godotenv"
)

// Config holds the application configuration loaded from environment variables.
// It centralizes all settings needed for server operation, security, and traffic control.
// Struct tags can be used for automated configuration binding or documentation.
type Config struct {
	APIKey              string        // API key required for internal or secure requests (currently unreferenced in middlewares).
	Domain              string        // The target domain name used for Let's Encrypt (ACME) certificate issuance.
	HTTPSPort           string        // The network address (e.g., ":443") for incoming secure HTTPS traffic.
	HTTPPort            string        // The network address (e.g., ":80") used primarily for the ACME HTTP-01 challenge.
	CertCacheDir        string        // Path to the directory where ACME certificates and metadata will be cached on disk.
	ACMEEnabled         bool          // Boolean flag: if true, automatic certificate management via Let's Encrypt is used.
	TLSCertFile         string        // File path to a manually-provided X.509 certificate file (if ACME is disabled).
	TLSKeyFile          string        // File path to the private key matching the manually-provided certificate.
	TrustProxy          bool          // If true, the server trusts the 'X-Forwarded-For' header for determining the client's real IP.
	MaxHeaderBytes      int           // Specifies the maximum size in bytes that the server will accept in HTTP headers.
	MaxBodyBytes        int64         // Restricts the maximum allowed size of the HTTP request body to prevent memory issues.
	ShutdownTimeout     time.Duration // The maximum duration to allow for active requests to complete during a graceful shutdown.
	RateLimit           float64       // Defines the average number of requests per second allowed for a single IP address.
	RateBurst           int           // Defines the maximum number of requests a single IP can make in a single burst.
	RateCleanupInterval time.Duration // Interval at which the server clears out old rate limiters from its internal memory map.
	RateExpiration      time.Duration // Time since a rate limiter was last used before it's considered eligible for cleanup.
}

// LoadConfig attempts to read configuration from environment variables.
// It also tries to load a '.env' file from the current directory if it exists, using the 'godotenv' library.
// It employs modern Go 1.26 idioms like 'cmp.Or' to provide fallback default values for optional environment variables.
func LoadConfig() (Config, error) {
	// Attempt to load settings from a local .env file.
	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found, relying on system environment variables")
	}

	// API_KEY is a mandatory security setting.
	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return Config{}, errors.New("API_KEY environment variable is required")
	}

	// DOMAIN is required for TLS certificate management, whether via ACME or manual configuration.
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		return Config{}, errors.New("DOMAIN environment variable is required")
	}

	// Optional configurations with safe defaults using cmp.Or.
	httpsPort := cmp.Or(os.Getenv("HTTPS_PORT"), ":443")
	httpPort := cmp.Or(os.Getenv("HTTP_PORT"), ":80")
	certCacheDir := cmp.Or(os.Getenv("CERT_CACHE_DIR"), "cert-cache")

	// Convert environment strings to boolean values.
	acmeEnabled, _ := strconv.ParseBool(cmp.Or(os.Getenv("ACME_ENABLED"), "true"))
	tlsCertFile := os.Getenv("TLS_CERT_FILE")
	tlsKeyFile := os.Getenv("TLS_KEY_FILE")
	trustProxy, _ := strconv.ParseBool(cmp.Or(os.Getenv("TRUST_PROXY"), "false"))

	// Parse numeric settings with detailed error reporting.
	maxHeaderBytes, err := strconv.Atoi(cmp.Or(os.Getenv("MAX_HEADER_BYTES"), "1048576")) // Default: 1MB
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_HEADER_BYTES: %w", err)
	}

	maxBodyBytes, err := strconv.ParseInt(cmp.Or(os.Getenv("MAX_BODY_BYTES"), "10485760"), 10, 64) // Default: 10MB
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_BODY_BYTES: %w", err)
	}

	// Parse duration-based settings.
	shutdownTimeout, err := time.ParseDuration(cmp.Or(os.Getenv("SHUTDOWN_TIMEOUT"), "30s"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid SHUTDOWN_TIMEOUT: %w", err)
	}

	// Rate limiting parameters for security and quality of service.
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

	// Return the populated Config struct.
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
