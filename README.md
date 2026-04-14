# Secure Go Web Template

Production-oriented Go web template built with standard library primitives, server-rendered HTML templates, HTMX-driven SPA-style interactions, and explicit security middleware.  
Rate limiting is **not** implemented in-process; enforce it at the edge (for example, Nginx).

## What this template provides

- TLS-ready HTTP server lifecycle with graceful shutdown.
- Security middleware (headers, panic recovery, request IDs, body/header limits).
- Explicit CORS policy controls through environment variables.
- Structured JSON logging with request correlation and Prometheus metrics.
- Server-rendered SPA-style UI using Go `html/template` + HTMX (no frontend framework).
- Modular UI package and separated templates/CSS for maintainability.

## Architecture at a glance

The app is intentionally split into two runtime layers:

1. **Core HTTP platform** (`pkg/server`)
   - Configuration loading and validation.
   - Middleware chain and server startup/shutdown.
   - Metrics endpoint registration.
2. **UI module** (`pkg/ui`)
   - SPA route registration.
   - In-memory UI state management.
   - Template loading and fragment rendering.

`main.go` is intentionally small: initialize logger -> load config -> build server -> build UI module -> register routes -> start server.

## Architecture diagram

Diagrams follow [GitHub Mermaid](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/creating-diagrams#creating-mermaid-diagrams): use a fenced code block with the `mermaid` tag as the language (same as GitHub Docs).

```mermaid
graph TD
    N["Nginx or edge proxy"] --> U["Client request"]
    U --> MW["Middleware stack"]
    MW --> R["http.ServeMux"]
    R --> UI["pkg/ui"]
    R --> PS["pkg/server routes"]
    UI --> T["web/templates"]
    UI --> C["web/static"]
    UI --> ST["In-memory UI state"]
    PS --> CFG["Configuration"]
    PS --> MET["Prometheus /metrics"]
    PS --> TLS["TLS and graceful shutdown"]
```

Request path through middleware (outer layer first, then inward toward the mux) matches `pkg/server/server.go`:

```mermaid
graph LR
    MB["MaxBytesHandler"] --> LG["loggingMiddleware"]
    LG --> CR["corsMiddleware"]
    CR --> SH["securityHeadersMiddleware"]
    SH --> RI["requestIDMiddleware"]
    RI --> RC["recoveryMiddleware"]
    RC --> MX["ServeMux"]
```

### End-to-end request sequence

1. Client sends request to the service (commonly through Nginx).
2. Server middleware applies recovery, request ID, security headers, CORS, logging, and body limits.
3. `http.ServeMux` dispatches to UI routes (`pkg/ui`) or platform routes (`/health`, `/readyz`, `/livez`, `/metrics`, etc.).
4. UI handlers read/update in-memory state and render templates from `web/templates`.
5. HTML (full shell or HTMX fragment) is returned; CSS is served from `web/static`.

## HTMX fragment lifecycle

```mermaid
sequenceDiagram
    participant B as Browser
    participant S as GoServer
    participant U as PkgUI
    participant T as Templates
    participant ST as UIState

    B->>S: GET /
    S->>U: handleShell
    U->>ST: snapshot
    U->>T: render dashboard fragment
    U->>T: render shell with initial fragment
    U-->>B: Full HTML shell

    B->>S: hx-get /ui/tasks
    S->>U: handleTasks
    U->>ST: snapshot
    U->>T: render tasks fragment
    U-->>B: HTML fragment into spa-content target

    B->>S: hx-post /ui/tasks form data
    S->>U: handleCreateTask
    U->>ST: addTask
    U->>ST: snapshot
    U->>T: render updated tasks fragment
    U-->>B: Updated fragment in-place swap
```

### What this means operationally

- Initial page load (`GET /`) renders both shell chrome and first content server-side.
- HTMX navigation (`hx-get`) fetches only panel fragments, reducing payload size.
- HTMX form posts (`hx-post`) mutate server state and immediately return updated HTML.
- No frontend build pipeline or framework runtime is required for interactive flows.

### HTMX troubleshooting quick reference

| Symptom | Likely cause | Quick check | Fix |
|---|---|---|---|
| Clicking nav button causes full page reload | HTMX script not loaded or blocked | Open page source/devtools and confirm `htmx.org` script is present and loaded | Ensure script tag in `web/templates/shell.gohtml` and allow outbound access to CDN (or vendor HTMX locally) |
| Fragment does not swap into content area | Wrong `hx-target` selector | Inspect button/form attrs and verify `#spa-content` exists in shell | Keep `id="spa-content"` in shell and `hx-target="#spa-content"` on controls |
| POST form appears to do nothing | Form parse error or empty `task` value | Check network response status/body for `POST /ui/tasks` | Ensure input has `name="task"` and submit valid non-empty text |
| Browser console shows CORS errors | Origin not allowed by CORS config | Compare request Origin with `CORS_ALLOWED_ORIGINS` | Add exact origin(s) and restart app; avoid `*` with credentials |
| Preflight request fails with 403 | Disallowed origin/method/header | Run curl preflight from README and inspect response headers | Update `CORS_ALLOWED_ORIGINS`, `CORS_ALLOWED_METHODS`, `CORS_ALLOWED_HEADERS` |
| CSS not applied | Static asset route/path mismatch | Request `GET /assets/app.css` directly | Keep file at `web/static/app.css` and route registration in `pkg/ui/app.go` |
| Template changes not reflected | Server still running old process | Check process start time/logs | Restart `go run main.go` so templates are reloaded at startup |

## Project structure

```text
go_web_template/
├── main.go
├── tls/
│   └── certs/             # Place TLS PEM files here (see Security model → TLS).
│       └── .gitkeep       # Directory tracked in git; certificate files stay local/untracked.
├── web/
│   ├── templates/
│   │   ├── shell.gohtml
│   │   ├── dashboard.gohtml
│   │   ├── tasks.gohtml
│   │   └── settings.gohtml
│   └── static/
│       └── app.css
├── pkg/
│   ├── server/
│   │   ├── config.go
│   │   ├── middleware.go
│   │   ├── metrics.go
│   │   └── server.go
│   └── ui/
│       ├── app.go
│       ├── routes.go
│       ├── state.go
│       └── templates.go
├── .env
├── go.mod
└── README.md
```

## How requests flow through the system

For every request, the server wraps handlers in this middleware order:

1. `recoveryMiddleware`
2. `requestIDMiddleware`
3. `securityHeadersMiddleware`
4. `corsMiddleware`
5. `loggingMiddleware`
6. `http.MaxBytesHandler` (body size enforcement)

Practical result:

- Panics are recovered and counted (`panics_total`).
- Each request receives `X-Request-ID`.
- Security headers are always set.
- CORS rules are enforced before handler execution.
- Request logs include method/path/status/duration/client IP.
- Oversized request bodies are rejected early.

## Server and UI responsibilities

### `pkg/server` responsibilities

- Load env configuration with defaults (`cmp.Or`) and validation.
- Build `http.Server` with strict timeouts.
- Load TLS certificates when provided (paths typically under `./tls/certs/`); otherwise run HTTP mode with warning.
- Register `/metrics` and run graceful shutdown on `SIGINT/SIGTERM`.
- Provide helper methods for protected routes (`HandleProtected*`) using `X-API-Key`.

### `pkg/ui` responsibilities

- Parse templates from `web/templates/*.gohtml` at startup (fail-fast).
- Register UI routes:
  - `GET /` -> full shell HTML
  - `GET /ui/dashboard` -> fragment
  - `GET /ui/tasks` -> fragment
  - `POST /ui/tasks` -> task mutation + refreshed fragment
  - `GET /ui/settings` -> fragment
  - `GET /assets/*` -> static assets from `web/static`
- Maintain a thread-safe in-memory state (`sync.RWMutex`) for SPA data.
- Render fragments and full page using Go templates only.

## Frontend approach (without a JS framework)

The UI is "SPA-style" rather than a client-rendered SPA:

- The shell page (`/`) loads once.
- HTMX requests server-rendered fragments for panel navigation.
- HTML swaps happen in-page (`hx-target="#spa-content"`).
- Form submissions (`POST /ui/tasks`) return updated HTML fragments.

This keeps interactivity high while preserving:

- server-side rendering simplicity,
- no hydration/runtime bundle complexity,
- strong alignment with Go stdlib and template security model.

## Security model

### TLS

- TLS 1.3 minimum is enforced when cert/key are provided.
- If TLS cert variables are unset, app runs plain HTTP (intended for local/dev or behind trusted TLS-terminating proxy).
- **Certificate location (convention):** store PEM files under **`./tls/certs/`** at the project root (relative to where you run the binary). Point `TLS_CERT_FILE` and `TLS_KEY_FILE` at those paths—for example:

```env
TLS_CERT_FILE=./tls/certs/your-domain.crt
TLS_KEY_FILE=./tls/certs/your-domain.key
```

The `tls/certs/` directory is present in the repo (via `.gitkeep`) so the layout is consistent; do not commit real private keys—add them locally or inject them in deployment.

### Security headers

The middleware sets:

- `Strict-Transport-Security`
- `Content-Security-Policy`
- `X-Frame-Options`
- `X-Content-Type-Options`
- `Referrer-Policy`
- `Cross-Origin-Opener-Policy`
- `Cross-Origin-Embedder-Policy`
- `Cross-Origin-Resource-Policy`

### CORS

- CORS is explicit and deny-by-default for cross-origin browser requests.
- Allowed origins/methods/headers are controlled by env vars.
- Preflight (`OPTIONS` + `Access-Control-Request-Method`) is handled in middleware.
- Credentials mode and wildcard origins are validated for safe combinations.

### Rate limiting

No in-process request throttling is included by design.

Use edge controls (Nginx, gateway, WAF), for example:

```nginx
limit_req_zone $binary_remote_addr zone=api_ratelimit:10m rate=10r/s;

server {
    location / {
        limit_req zone=api_ratelimit burst=20 nodelay;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Host $host;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

## Observability

- Structured logs via `log/slog` JSON handler.
- Request logs include client IP and respect `TRUST_PROXY` (first hop of `X-Forwarded-For`).
- Health endpoints:
  - `GET /livez` is lightweight process liveness (`200` when process is running).
  - `GET /readyz` returns readiness checks and `503` when degraded.
  - `GET /health` returns the same detailed readiness report for diagnostics.
- Prometheus metrics:
  - `http_requests_total`
  - `http_request_duration_seconds`
  - `panics_total`

## Environment variables

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `API_KEY` | Shared key for protected route helpers | - | Yes |
| `DOMAIN` | Service domain identifier | - | Yes |
| `HTTPS_PORT` | Bind address/port | `:443` | No |
| `TLS_CERT_FILE` | Path to TLS certificate PEM (convention: `./tls/certs/*.crt`) | - | No* |
| `TLS_KEY_FILE` | Path to TLS private key PEM (convention: `./tls/certs/*.key`) | - | No* |
| `TRUST_PROXY` | Trust `X-Forwarded-For` first hop for logged client IP | `false` | No |
| `MAX_HEADER_BYTES` | Max allowed request header size | `1048576` | No |
| `MAX_BODY_BYTES` | Max allowed request body size | `10485760` | No |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown timeout | `30s` | No |
| `CORS_ALLOWED_ORIGINS` | Comma-separated exact origins (or `*` when credentials are off) | empty | No |
| `CORS_ALLOWED_METHODS` | Comma-separated allowed CORS methods | `GET,POST,PUT,PATCH,DELETE,OPTIONS` | No |
| `CORS_ALLOWED_HEADERS` | Comma-separated allowed request headers | `Accept,Authorization,Content-Type,X-API-Key,X-Requested-With` | No |
| `CORS_EXPOSED_HEADERS` | Comma-separated response headers exposed to browser JS | `X-Request-ID` | No |
| `CORS_ALLOW_CREDENTIALS` | Include `Access-Control-Allow-Credentials` | `false` | No |
| `CORS_MAX_AGE_SECONDS` | Browser preflight cache duration | `600` | No |

\* If either TLS file is missing, the app runs insecure HTTP mode.

## Recommended CORS presets

### Local development

```env
CORS_ALLOWED_ORIGINS=http://localhost:3000,http://127.0.0.1:3000
CORS_ALLOWED_METHODS=GET,POST,PUT,PATCH,DELETE,OPTIONS
CORS_ALLOWED_HEADERS=Accept,Authorization,Content-Type,X-API-Key,X-Requested-With
CORS_EXPOSED_HEADERS=X-Request-ID
CORS_ALLOW_CREDENTIALS=false
CORS_MAX_AGE_SECONDS=300
```

### Production (single trusted frontend)

```env
CORS_ALLOWED_ORIGINS=https://app.example.com
CORS_ALLOWED_METHODS=GET,POST,PUT,PATCH,DELETE,OPTIONS
CORS_ALLOWED_HEADERS=Accept,Authorization,Content-Type,X-API-Key,X-Requested-With
CORS_EXPOSED_HEADERS=X-Request-ID
CORS_ALLOW_CREDENTIALS=true
CORS_MAX_AGE_SECONDS=600
```

## Quick verification

### Start app

```bash
go run main.go
```

### Verify endpoints

```bash
curl -i http://localhost/health
curl -i http://localhost/readyz
curl -i http://localhost/livez
curl -i http://localhost/metrics
curl -i http://localhost/
curl -i http://localhost/ui/dashboard
```

Example healthy `/readyz` or `/health` response:

```json
{
  "status": "ok",
  "timestamp": "2026-04-13T20:10:33Z",
  "uptime_sec": 42,
  "checks": {
    "templates_loaded": "ok",
    "state_initialized": "ok",
    "static_css_present": "ok"
  }
}
```

### Verify CORS preflight

```bash
curl -i -X OPTIONS "http://localhost/" \
  -H "Origin: http://localhost:3000" \
  -H "Access-Control-Request-Method: GET" \
  -H "Access-Control-Request-Headers: Authorization,Content-Type"
```

Expected:

- Allowed origin/method -> `204 No Content` + `Access-Control-Allow-*` headers.
- Disallowed preflight origin -> `403 Forbidden`.

## Local offline mode (no CDN dependency)

By default, the shell template references HTMX from the public CDN. For air-gapped or offline environments, you can vendor HTMX locally.

1. Download `htmx.min.js` and place it at `web/static/htmx.min.js`.
2. Update `web/templates/shell.gohtml`:

```html
<!-- Replace CDN script with local asset -->
<script src="/assets/htmx.min.js"></script>
```

3. Restart the app (`go run main.go`) so template changes are reloaded.

Because `/assets/*` is already served from `web/static`, no additional route changes are needed.

## Local development without TLS

1. Unset `TLS_CERT_FILE` and `TLS_KEY_FILE`.
2. Optionally set `HTTPS_PORT=:8080`.
3. Run `go run main.go`.
4. Access `http://localhost:8080/`.

## Testing

The project now includes unit tests across:

- `main` bootstrap logic and health/readiness/liveness handler registration.
- `pkg/server` config parsing, middleware behavior, route protection, and server lifecycle branches.
- `pkg/ui` route handlers, template rendering, state mutation, and health checks.

### Run tests

```bash
go test ./...
```

### Generate coverage report

```bash
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out
```

### Enforce 100% coverage

Use this command in CI/local checks:

```bash
go test ./... -coverprofile=coverage.out && \
  go tool cover -func=coverage.out | rg "^total:" | rg "100.0%"
```

If the final `rg` command does not match, coverage is below target and the command exits non-zero.

### How to add more tests

1. **Pick the behavior boundary first**  
   Test outcomes at handler/middleware boundaries (`status`, headers, body, side effects), not implementation details.

2. **Use `httptest` for HTTP behavior**  
   Build requests with `httptest.NewRequest`, capture output with `httptest.NewRecorder`, assert response contract.

3. **Use dependency injection seams where needed**  
   The project exposes injectable function vars in `main` and `pkg/server` for hard-to-reach branches (startup errors, shutdown errors, fallback paths).

4. **Cover both success and failure paths**  
   For each new function, add at least one passing and one failing/degraded test case.

5. **Keep tests deterministic**  
   Use `t.TempDir()` and `t.Chdir()` when testing template/static file discovery, and avoid reliance on external services.

6. **Update docs with behavior changes**  
   When tests reveal or enforce new behavior, update this README and endpoint examples in the same change.

## Notes for production hardening

- Put Nginx/API gateway in front for TLS termination policy, rate limiting, and request filtering.
- Keep CORS origin lists narrow and explicit.
- Keep `TRUST_PROXY=false` unless requests always pass through trusted proxy hops.
- Move in-memory SPA state to a persistent store when data durability is required.
