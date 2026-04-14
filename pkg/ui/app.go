package ui

import (
	"html/template"
	"net/http"
	"time"
)

const appTitle = "DHS Labs Control Center"

// App contains all SPA concerns and exposes a single registration entrypoint
// for wiring UI routes into the shared server instance.
type App struct {
	templates *template.Template
	state     *state
	startedAt time.Time
}

// routeRegistrar is the minimal routing surface needed by the UI module.
type routeRegistrar interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// New builds a fully initialized SPA app and parses all templates up front so
// invalid template syntax fails fast during startup.
func New() (*App, error) {
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	return &App{
		templates: templates,
		state: &state{
			tasks:        []string{"Review deployment health", "Rotate API key in staging", "Verify proxy rate limits"},
			lastUpdated:  time.Now().UTC(),
			serviceState: "Healthy",
		},
		startedAt: time.Now().UTC(),
	}, nil
}

// RegisterRoutes wires all SPA routes and static asset serving onto the shared
// server mux. This keeps main.go focused on bootstrap and lifecycle concerns.
func (a *App) RegisterRoutes(srv routeRegistrar) {
	srv.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("web/static"))))
	srv.HandleFunc("GET /", a.handleShell)
	srv.HandleFunc("GET /ui/dashboard", a.handleDashboard)
	srv.HandleFunc("GET /ui/tasks", a.handleTasks)
	srv.HandleFunc("POST /ui/tasks", a.handleCreateTask)
	srv.HandleFunc("GET /ui/settings", a.handleSettings)
}
