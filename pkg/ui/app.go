package ui

import (
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

// appTitle is shown in the shell header and HTML document title.
const appTitle = "DHS Labs Control Center"

// App contains all SPA concerns and exposes a single registration entrypoint
// for wiring UI routes into the shared server instance.
type App struct {
	templates *template.Template
	staticFS  fs.FS
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
// templateFS is the root containing *.gohtml files (typically embedded from web/templates).
// staticFS is the root for files served under /assets/ (e.g. app.css); use embed.FS or
// other fs.FS so serving does not depend on process working directory.
func New(templateFS, staticFS fs.FS) (*App, error) {
	templates, err := loadTemplates(templateFS)
	if err != nil {
		return nil, err
	}

	return &App{
		templates: templates,
		staticFS:  staticFS,
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
// Static files are served from staticFS under the URL prefix /assets/.
func (a *App) RegisterRoutes(srv routeRegistrar) {
	// Browsers request /assets/app.css etc.; StripPrefix maps URL path to names in staticFS.
	srv.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(a.staticFS))))
	srv.HandleFunc("GET /", a.handleShell)
	srv.HandleFunc("GET /ui/dashboard", a.handleDashboard)
	srv.HandleFunc("GET /ui/tasks", a.handleTasks)
	srv.HandleFunc("POST /ui/tasks", a.handleCreateTask)
	srv.HandleFunc("GET /ui/settings", a.handleSettings)
}
