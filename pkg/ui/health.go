package ui

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// HealthResponse is the JSON body for /health and /readyz when those endpoints return
// detailed diagnostics. Status is "ok" only when every check passes; otherwise "degraded".
type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp string            `json:"timestamp"`
	UptimeSec int64             `json:"uptime_sec"`
	Checks    map[string]string `json:"checks"`
}

// Health evaluates template availability, in-memory state, and static CSS on disk.
// Orchestrators can use the aggregated status for readiness; /livez remains a trivial OK.
func (a *App) Health() HealthResponse {
	checks := map[string]string{
		"templates_loaded":   "ok",
		"state_initialized":  "ok",
		"static_css_present": "ok",
	}

	if a.templates.Lookup("shell") == nil ||
		a.templates.Lookup("dashboard") == nil ||
		a.templates.Lookup("tasks") == nil ||
		a.templates.Lookup("settings") == nil {
		checks["templates_loaded"] = "failed"
	}

	if a.state == nil || a.state.serviceState == "" {
		checks["state_initialized"] = "failed"
	}

	if _, err := os.Stat("web/static/app.css"); err != nil {
		checks["static_css_present"] = fmt.Sprintf("failed: %v", err)
	}

	// Any check value beginning with "failed" downgrades the whole report so probes fail loudly.
	status := "ok"
	for _, v := range checks {
		if strings.HasPrefix(v, "failed") {
			status = "degraded"
			break
		}
	}

	return HealthResponse{
		Status:    status,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		UptimeSec: int64(time.Since(a.startedAt).Seconds()),
		Checks:    checks,
	}
}
