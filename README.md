# Secure Go REST API Template

A robust, production-ready Go REST API template featuring modern Go 1.26 idioms, automated HTTPS, rate limiting, and observability.

## Project Overview

This project provides a solid foundation for building secure web services in Go. It handles the "boring but critical" parts of a production service: TLS management, security headers, rate limiting, structured logging, and monitoring.

### Key Features

- **Automated HTTPS**: Integrated with Let's Encrypt (ACME) via `autocert` for zero-touch certificate management.
- **Modern Security**: 
    - Enforced **TLS 1.3** and secure cipher suites.
    - Comprehensive security headers (HSTS, CSP, X-Frame-Options, etc.).
    - Request body size limiting to prevent Denial of Service (DoS).
    - Panic recovery middleware for high availability.
- **Observability**:
    - Structured JSON logging using `log/slog`.
    - Native Prometheus metrics endpoint (`/metrics`) tracking request volume, latency, errors, and rate limit hits.
- **Traffic Management**:
    - Smart per-IP rate limiting with configurable burst capacity.
    - Automatic memory management: stale rate limiters are cleared in the background.
    - Support for `X-Forwarded-For` when running behind load balancers.
- **Modern Go Idioms**: Uses Go 1.26 features like `cmp.Or` for configuration defaults, `slog` for logging, and the enhanced `http.ServeMux` for clean, method-based routing.

## Project Structure

The project is organized into a clean, modular structure:

```text
rest_api/
├── main.go              # Application entry point; wires up the server and routes.
├── pkg/
│   └── server/          # Core server logic and middleware.
│       ├── config.go    # Environment-based configuration loading and validation.
│       ├── metrics.go   # Prometheus metrics definitions.
│       ├── middleware.go# HTTP middleware chain (logging, security, auth, etc.).
│       └── server.go    # Server lifecycle management (Start, TLS, Shutdown).
├── go.mod               # Go module definition.
├── go.sum               # Dependency checksums.
└── README.md            # You are here.
```

## Detailed Component Breakdown

### 1. Server Lifecycle (`pkg/server/server.go`)
Manages the HTTP and HTTPS servers. It handles the ACME challenge responder for Let's Encrypt and ensures the primary server shuts down gracefully when an OS signal (SIGINT/SIGTERM) is received.

### 2. Configuration (`pkg/server/config.go`)
Loads settings from the environment or a `.env` file. It validates mandatory fields like `API_KEY` and `DOMAIN` and provides sensible defaults for optional settings using `cmp.Or`.

### 3. Middlewares (`pkg/server/middleware.go`)
A layered chain of handlers:
- **Recovery**: Catch panics and return 500s.
- **Request ID**: Assigns a GUID to every request for end-to-end tracing.
- **Security Headers**: Sets modern browser security policies.
- **Rate Limit**: Throttles clients based on IP to prevent abuse.
- **Logging**: Records request details and updates Prometheus metrics.

### 4. Metrics (`pkg/server/metrics.go`)
Defines the `http_requests_total`, `http_request_duration_seconds`, `rate_limit_hits_total`, and `panics_total` metrics.

## Getting Started

### Prerequisites
- Go 1.26 or later.
- A valid domain name (for ACME/Let's Encrypt).
- Ports 80 and 443 open in your firewall.

### Installation & Run

1. **Clone the repository**:
   ```bash
   git clone <repository-url>
   cd https://github.com/anthony-hopkins/rest_api_template
   ```

2. **Configure Environment**:
   Create a `.env` file in the root:
   ```env
   API_KEY=your-secret-key
   DOMAIN=yourdomain.com
   ACME_ENABLED=true
   ```

3. **Run**:
   ```bash
   go run main.go
   ```

## Local Development (No ACME)

To run locally without a domain or Let's Encrypt:
1. Set `ACME_ENABLED=false` in your `.env`.
2. Optionally set `HTTPS_PORT=:8443` or similar.
3. If no certificates are provided, the server will log a warning and run over HTTP (useful for local testing behind a local proxy).
