package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/anthony-hopkins/rest_api_template/pkg/server"
)

// main is the application's entry point. It sets up structured logging,
// loads the application configuration, initializes the server, defines
// the HTTP routes, and then starts the server lifecycle.
func main() {
	// Initialize structured logging with JSON format and output to stdout.
	// This is a modern Go 1.26 idiom using the log/slog package for
	// high-performance, structured logging suitable for production environments.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Load and validate application configuration from environment variables
	// and/or a .env file using the server package's LoadConfig function.
	cfg, err := server.LoadConfig()
	if err != nil {
		// Log the error and exit the application if configuration fails.
		slog.Error("Configuration error", "error", err)
		os.Exit(1)
	}

	// Create a new Server instance using the loaded configuration.
	// The server package provides a robust HTTP/HTTPS server implementation.
	srv := server.New(cfg)

	// Define the /health endpoint to provide a simple way for monitoring
	// systems (like Kubernetes or status checkers) to verify the service is up.
	srv.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Define the root endpoint (/) as a simple welcome message served over HTTPS.
	// This uses the method-based routing introduced in Go 1.22's http.ServeMux.
	srv.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("Hello over HTTPS"))
		if err != nil {
			slog.Error("Failed to write response", "error", err)
		}
	})

	// Start the server and block the main goroutine until it shuts down.
	// Start() handles the server's lifecycle, including TLS setup (ACME or manual),
	// applying middlewares, and managing graceful shutdown on SIGINT/SIGTERM.
	if err := srv.Start(); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
