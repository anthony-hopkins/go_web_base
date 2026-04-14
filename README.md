# Secure Go REST API Template

A robust, production-ready Go REST API template featuring modern Go 1.26 idioms, manual TLS certificate loading, and observability. **HTTP rate limiting is intentionally not implemented in the application**; it is expected to be enforced by a reverse proxy such as **Nginx** in front of this service.

## Project Overview

This project provides a solid foundation for building secure web services in Go. It handles the “boring but critical” parts of a production service: TLS management, security headers, structured logging, and monitoring. Per-client throttling and abuse protection at the edge are delegated to the proxy tier so the Go process stays simpler and policy stays centralized.

### Key Features

- **TLS Management**: Manual loading of X.509 certificates for secure communication.
- **Modern Security**:
    - Enforced **TLS 1.3** and secure cipher suites.
    - Comprehensive security headers (HSTS, CSP, X-Frame-Options, etc.) to mitigate XSS and clickjacking.
    - Request body size limiting to reduce memory exhaustion risk from huge uploads.
    - Panic recovery middleware for high availability and failure containment.
- **Observability**:
    - Structured JSON logging using `log/slog`, optimized for log aggregation.
    - Native Prometheus metrics endpoint (`/metrics`) tracking request volume, latency, errors, and panics.
    - Standardized request tracing with unique Request IDs (`X-Request-ID`).
- **Reverse-proxy friendly**:
    - Optional `TRUST_PROXY=true` uses the first hop in `X-Forwarded-For` for the `ip` field in structured logs (use when Nginx or another proxy forwards the real client address).
- **Modern Go Idioms**: Uses Go 1.26 features like `cmp.Or` for configuration defaults, `slog` for logging, and the enhanced `http.ServeMux` for clean, method-based routing.

## Deployment note: rate limiting

Configure request limits (e.g. `limit_req`, connection limits, or WAF rules) in **Nginx** (or your edge) rather than in this binary. The app does not emit `429` from an internal limiter.

### Example: Nginx `limit_req`

Below is a minimal example showing per-client request throttling at the edge. Adjust rates/bursts for your traffic profile.

```nginx
# http {}
limit_req_zone $binary_remote_addr zone=api_ratelimit:10m rate=10r/s;

server {
    # ... TLS / upstream config ...

    location / {
        # Allow short bursts while still enforcing a sustained rate.
        limit_req zone=api_ratelimit burst=20 nodelay;

        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

If a client exceeds the policy, **Nginx will return `429`** (and the Go service will never see the request).

## Project Structure

The project follows a clean, modular structure:

```text
rest_api/
├── main.go              # Application entry point; wires up the server and routes.
├── pkg/
│   └── server/          # Core server logic and middleware.
│       ├── config.go    # Environment-based configuration loading and validation.
│       ├── metrics.go   # Prometheus metrics definitions and collectors.
│       ├── middleware.go# HTTP middleware chain (logging, security, request ID, recovery).
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

Loads settings from the environment or a `.env` file using `godotenv`. It validates mandatory fields like `API_KEY` and `DOMAIN` and provides sensible defaults using `cmp.Or`. Rate-limit-related environment variables are not used; configure throttling at the proxy.

### 3. Middlewares (`pkg/server/middleware.go`)

A layered chain of HTTP handlers:

- **Recovery**: Uses `recover()` to catch panics and return 500 status codes.
- **Request ID**: Assigns an 8-byte unique hex identifier to every request.
- **Security Headers**: Sets modern browser policies (HSTS, CSP, NoSniff, etc.).
- **Logging**: Records request metadata (including client IP, honoring `TRUST_PROXY`) and updates Prometheus histograms and counters.

### 4. Metrics (`pkg/server/metrics.go`)

Initializes global Prometheus metrics: `http_requests_total`, `http_request_duration_seconds`, and `panics_total`.

## Environment Variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `API_KEY` | Secret key for API access | - | Yes |
| `DOMAIN` | Server domain name | - | Yes |
| `HTTPS_PORT` | Port for HTTPS traffic | `:443` | No |
| `TLS_CERT_FILE`| Path to .pem cert file | - | No* |
| `TLS_KEY_FILE` | Path to .pem key file | - | No* |
| `TRUST_PROXY` | Use first `X-Forwarded-For` hop for log `ip` | `false` | No |
| `MAX_HEADER_BYTES` | Max HTTP header size (bytes) | `1048576` | No |
| `MAX_BODY_BYTES` | Max request body size (bytes) | `10485760` | No |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown timeout | `30s` | No |

*\*If either TLS file is missing, the server runs in insecure HTTP mode.*

## Getting Started

### Prerequisites

- Go 1.26 or later.
- (Recommended) Valid TLS certificate and private key.
- (Production) Nginx or similar reverse proxy for TLS termination, rate limiting, and load balancing as needed.

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
