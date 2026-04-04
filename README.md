# Secure Go REST API Template

A robust, production-ready Go REST API template featuring modern Go 1.26 idioms, automated HTTPS, rate limiting, and observability.

## Features

- **Automated HTTPS**: Integrated with Let's Encrypt (ACME) via `autocert` for automatic certificate management.
- **Modern Security**: 
    - Enforced TLS 1.3.
    - Automatic security headers (HSTS, CSP, X-Frame-Options, etc.).
    - Request body size limiting to prevent memory exhaustion.
    - Panic recovery middleware to ensure server stability.
- **Observability**:
    - Structured logging using `log/slog` with JSON output.
    - Prometheus metrics endpoint (`/metrics`) for real-time monitoring of request counts, duration, rate limit hits, and panics.
- **Traffic Management**:
    - Per-IP rate limiting with configurable burst capacity and automatic cleanup of stale limiters.
    - Support for trusted proxies (e.g., Load Balancers) via `X-Forwarded-For`.
- **Modern Go Idioms**: Uses Go 1.26 features like `cmp.Or` for defaults, `slog` for logging, and `http.NewServeMux` with method-based routing.

## Prerequisites

- Go 1.26 or later.
- A valid domain name (if using ACME/Let's Encrypt).
- Ports 80 and 443 open for ACME challenges and HTTPS traffic.

## Configuration

Configuration is managed via environment variables. You can also use a `.env` file in the project root.

| Variable | Description | Default |
|----------|-------------|---------|
| `API_KEY` | **Required**. API key for the application. | - |
| `DOMAIN` | **Required**. Domain name for Let's Encrypt. | - |
| `HTTPS_PORT` | Port for HTTPS traffic. | `:443` |
| `HTTP_PORT` | Port for HTTP traffic (ACME challenges). | `:80` |
| `ACME_ENABLED` | Enable Let's Encrypt. | `true` |
| `TRUST_PROXY` | Trust `X-Forwarded-For` headers. | `false` |
| `RATE_LIMIT` | Requests per second per IP. | `10` |
| `RATE_BURST` | Burst capacity per IP. | `20` |
| `MAX_BODY_BYTES` | Max request body size. | `10MB` |

## Getting Started

1. **Clone the repository**:
   ```bash
   git clone <repository-url>
   cd rest_api
   ```

2. **Set up environment variables**:
   Create a `.env` file:
   ```env
   API_KEY=your-secret-key
   DOMAIN=example.com
   ```

3. **Run the application**:
   ```bash
   go run main.go
   ```

4. **Access the API**:
   - Application: `https://example.com/`
   - Health Check: `https://example.com/health`
   - Metrics: `https://example.com/metrics`

## Code Structure

The project is contained within `main.go` for simplicity, following a clean, middleware-driven architecture:

- `Config`: Handles environment-based configuration.
- `main()`: Entry point; initializes the router, middlewares, and servers (HTTP/HTTPS).
- **Middlewares**:
    - `loggingMiddleware`: Logs requests and updates Prometheus metrics.
    - `rateLimitMiddleware`: Manages per-IP traffic limits.
    - `securityHeadersMiddleware`: Injects security-best-practice headers.
    - `requestIDMiddleware`: Assigns a unique ID to every request for tracing.
    - `recoveryMiddleware`: Safely handles application panics.

## Local Development (No ACME)

If you are developing locally and don't have a domain or want to use ACME:

1. Set `ACME_ENABLED=false` in your `.env`.
2. (Optional) Provide `TLS_CERT_FILE` and `TLS_KEY_FILE` to use your own certificates.
3. If no certificates are provided, the server will log a warning and run over HTTP if reached.
