package server

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// This package initializes and exposes Prometheus metrics to monitor the health,
// performance, and security of the REST API.

var (
	// httpRequestsTotal is a Prometheus counter that tracks the total number of HTTP requests processed.
	// It is partitioned by:
	// - method: The HTTP verb (GET, POST, etc.)
	// - path: The request URI path.
	// - status: The HTTP response status code (e.g., 200, 404, 500).
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests processed by the server, partitioned by method, path, and status code.",
	}, []string{"method", "path", "status"})

	// httpRequestDuration is a Prometheus histogram that tracks the duration of HTTP requests in seconds.
	// It provides insights into latency across different endpoints and methods.
	// It is partitioned by:
	// - method: The HTTP verb.
	// - path: The request URI path.
	// It uses default Prometheus buckets for distribution.
	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Duration of HTTP requests in seconds, partitioned by method and path.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	// panicsTotal is a Prometheus counter that tracks the total number of unexpected application panics
	// that were successfully caught and recovered by the recoveryMiddleware.
	// A non-zero value here indicates a critical bug in the request handling logic.
	panicsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "panics_total",
		Help: "Total number of application panics recovered by the server's recovery middleware.",
	})
)
