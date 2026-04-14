// Package server implements the shared HTTP platform for this template: environment-driven
// configuration, a composable middleware stack, Prometheus metrics, TLS-aware listening,
// and graceful shutdown. Routing uses net/http.ServeMux; rate limiting is expected at
// the reverse proxy, not inside the Go process.
//
// Typical usage: construct a Server with New(LoadConfig()), register application handlers
// on the embedded mux via Handle/HandleFunc (and HandleProtected* when API-key auth is
// required), then call Start to run until SIGINT/SIGTERM.
package server
