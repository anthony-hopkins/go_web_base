package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/anthony-hopkins/rest_api_template/pkg/server"
	"github.com/anthony-hopkins/rest_api_template/pkg/ui"
)

type appServer interface {
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
	Handle(pattern string, handler http.Handler)
	Start() error
}

var (
	loadConfigFunc = server.LoadConfig
	newServerFunc  = func(cfg server.Config) appServer { return server.New(cfg) }
	newUIFunc      = ui.New
	exitFunc       = os.Exit
)

// main is the application's entry point.
// It performs the following initialization steps:
// 1. Sets up structured JSON logging using slog.
// 2. Loads application configuration from environment variables.
// 3. Initializes the core server component.
// 4. Defines HTTP routes and their respective handlers.
// 5. Starts the server lifecycle and blocks until termination.
func main() {
	if err := run(); err != nil {
		slog.Error("Application startup failed", "error", err)
		exitFunc(1)
	}
}

func run() error {
	// Initialize structured logging.
	// We use slog.NewJSONHandler to output logs in a format that's easily
	// parsed by log aggregation tools like ELK, Splunk, or Datadog.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Load and validate application configuration.
	// This function handles reading from .env files and ensuring required
	// variables like API_KEY and DOMAIN are present.
	cfg, err := loadConfigFunc()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	// Create a new Server instance.
	// The Server struct (from the pkg/server package) encapsulates the
	// http.Server, routing logic, and middleware stack.
	srv := newServerFunc(cfg)
	spaApp, err := newUIFunc()
	if err != nil {
		return fmt.Errorf("failed to initialize SPA app: %w", err)
	}

	// Define the /health endpoint.
	// /health provides a detailed report suitable for diagnostics.
	srv.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		report := spaApp.Health()
		statusCode := http.StatusOK
		if report.Status != "ok" {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(report); err != nil {
			slog.Error("Failed to write health response", "error", err)
		}
	})
	// /livez is a lightweight liveness probe for process health.
	srv.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	// /readyz is a readiness probe with the same concrete checks as /health.
	srv.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		report := spaApp.Health()
		statusCode := http.StatusOK
		if report.Status != "ok" {
			statusCode = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(statusCode)
		if err := json.NewEncoder(w).Encode(report); err != nil {
			slog.Error("Failed to write readiness response", "error", err)
		}
	})

	// Register all SPA routes and UI asset handlers from the ui package.
	spaApp.RegisterRoutes(srv)

	// Start the server.
	// This call is blocking and will only return after the server
	// shuts down gracefully (or fails to start).
	if err := srv.Start(); err != nil {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}
