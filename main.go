package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/anthony-hopkins/rest_api_template/pkg/server"
)

// main is the application's entry point.
// It performs the following initialization steps:
// 1. Sets up structured JSON logging using slog.
// 2. Loads application configuration from environment variables.
// 3. Initializes the core server component.
// 4. Defines HTTP routes and their respective handlers.
// 5. Starts the server lifecycle and blocks until termination.
func main() {
	// Initialize structured logging.
	// We use slog.NewJSONHandler to output logs in a format that's easily
	// parsed by log aggregation tools like ELK, Splunk, or Datadog.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Load and validate application configuration.
	// This function handles reading from .env files and ensuring required
	// variables like API_KEY and DOMAIN are present.
	cfg, err := server.LoadConfig()
	if err != nil {
		slog.Error("Configuration error", "error", err)
		os.Exit(1)
	}

	// Create a new Server instance.
	// The Server struct (from the pkg/server package) encapsulates the
	// http.Server, routing logic, and middleware stack.
	srv := server.New(cfg)

	// Define the /health endpoint.
	// This is a common pattern for "Liveness" and "Readiness" checks in
	// containerized environments like Kubernetes.
	srv.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Define the root endpoint (/).
	// We use the enhanced http.ServeMux (introduced in Go 1.22) which
	// allows specifying the HTTP method directly in the pattern string.
	srv.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("Hello over HTTPS"))
		if err != nil {
			slog.Error("Failed to write response", "error", err)
		}
	})

	// Start the server.
	// This call is blocking and will only return after the server
	// shuts down gracefully (or fails to start).
	if err := srv.Start(); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
