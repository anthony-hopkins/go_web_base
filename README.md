# Secure Go REST API Template

A robust, production-ready Go REST API template featuring modern Go 1.26 idioms, manual TLS certificate loading, rate limiting, and observability.

## Project Overview

This project provides a solid foundation for building secure web services in Go. It handles the "boring but critical" parts of a production service: TLS management, security headers, rate limiting, structured logging, and monitoring.

### Key Features

- **TLS Management**: Manual loading of X.509 certificates for secure communication.
- **Modern Security**: 
    - Enforced **TLS 1.3** and secure cipher suites.
    - Comprehensive security headers (HSTS, CSP, X-Frame-Options, etc.) to mitigate XSS and clickjacking.
    - Request body size limiting to prevent Denial of Service (DoS).
    - Panic recovery middleware for high availability and failure containment.
- **Observability**:
    - Structured JSON logging using `log/slog`, optimized for log aggregation.
    - Native Prometheus metrics endpoint (`/metrics`) tracking request volume, latency, errors, and rate limit hits.
    - Standardized request tracing with unique Request IDs (X-Request-ID).
- **Traffic Management**:
    - Per-IP request throttling with configurable burst capacity using token-bucket algorithm.
    - Efficient memory management: stale rate limiters are cleared in the background.
    - Full support for `X-Forwarded-For` when running behind load balancers or CDNs.
- **Modern Go Idioms**: Uses Go 1.26 features like `cmp.Or` for configuration defaults, `slog` for logging, and the enhanced `http.ServeMux` for clean, method-based routing.

## Project Structure

The project follows a clean, modular structure:

```text
rest_api/
├── main.go              # Application entry point; wires up the server and routes.
├── pkg/
│   └── server/          # Core server logic and middleware.
│       ├── config.go    # Environment-based configuration loading and validation.
│       ├── metrics.go   # Prometheus metrics definitions and collectors.
│       ├── middleware.go# HTTP middleware chain (logging, security, rate limiting).
│       └── server.go    # Server lifecycle management (TLS setup, graceful shutdown).
├── bin/                 # Compiled binaries (optional).
├── .env                 # Local environment variables configuration.
├── go.mod               # Go module definition.
├── go.sum               # Dependency checksums.
└── README.md            # You are here.
```

## Detailed Component Breakdown

### 1. Server Lifecycle (`pkg/server/server.go`)
Manages the HTTP and HTTPS server instantiation. It ensures the primary listener shuts down gracefully when an OS signal (SIGINT/SIGTERM) is received, allowing active connections to complete before exit.

### 2. Configuration (`pkg/server/config.go`)
Loads settings from the environment or a `.env` file using `godotenv`. It validates mandatory fields like `API_KEY` and `DOMAIN` and provides sensible, production-ready defaults using `cmp.Or`.

### 3. Middlewares (`pkg/server/middleware.go`)
A robust, layered chain of HTTP handlers:
- **Recovery**: Uses `recover()` to catch panics and return 500 status codes.
- **Request ID**: Assigns an 8-byte unique hex identifier to every request.
- **Security Headers**: Sets modern browser policies (HSTS, CSP, NoSniff, etc.).
- **Rate Limit**: Throttles clients using IP-based token buckets.
- **Logging**: Records request metadata and updates Prometheus histograms/counters.

### 4. Metrics (`pkg/server/metrics.go`)
Initializes global Prometheus metrics including `http_requests_total`, `http_request_duration_seconds`, `rate_limit_hits_total`, and `panics_total`.

## Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `API_KEY` | Secret key for API access | - | Yes |
| `DOMAIN` | Server domain name | - | Yes |
| `HTTPS_PORT` | Port for HTTPS traffic | `:443` | No |
| `TLS_CERT_FILE`| Path to .pem cert file | - | No* |
| `TLS_KEY_FILE` | Path to .pem key file | - | No* |
| `TRUST_PROXY` | Trust X-Forwarded-For | `false` | No |
| `RATE_LIMIT` | Requests per second | `10.0` | No |
| `RATE_BURST` | Max burst capacity | `20` | No |

*\*If either is missing, the server runs in insecure HTTP mode.*

## Getting Started

### Prerequisites
- Go 1.26 or later.
- (Recommended) Valid TLS certificate and private key.

### Installation & Run

1. **Clone the repository**:
   ```bash
   git clone <repository-url>
   cd rest_api
   ```

2. **Configure Environment**:
   Create a `.env` file in the root:
   ```env
   API_KEY=your-secret-key
   DOMAIN=yourdomain.com
   TLS_CERT_FILE=/path/to/cert.pem
   TLS_KEY_FILE=/path/to/key.pem
   ```

3. **Run**:
   ```bash
   go run main.go
   ```

4. **Verify**:
   - Health check: `curl http://localhost/health`
   - Metrics: `curl http://localhost/metrics`

## Local Development (No TLS)

To run locally without HTTPS:
1. Ensure `TLS_CERT_FILE` and `TLS_KEY_FILE` are not set in your `.env`.
2. Optionally set `HTTPS_PORT=:8080`.
3. The server will log a warning and run over HTTP on the specified port.
