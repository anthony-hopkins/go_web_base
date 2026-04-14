package ui

import (
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
)

// renderPanel writes a single panel template (dashboard, tasks, or settings) to the
// response. Used for HTMX fragment responses and low-level tests; does not wrap the shell.
func (a *App) renderPanel(w http.ResponseWriter, panel string, model panelViewModel) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, panel, model); err != nil {
		http.Error(w, "Failed to render panel", http.StatusInternalServerError)
		slog.Error("Failed to render panel", "panel", panel, "error", err)
	}
}

// renderPanelTemplate renders the named panel into a string so the result can be embedded
// in the shell template as template.HTML. Errors propagate to renderShell for consistent handling.
func (a *App) renderPanelTemplate(panel string, model panelViewModel) (string, error) {
	var b strings.Builder
	err := a.templates.ExecuteTemplate(&b, panel, model)
	return b.String(), err
}

// loadTemplates parses all files matching web/templates/*.gohtml at process startup.
// Misconfigured templates therefore fail fast during ui.New rather than at first request.
func loadTemplates() (*template.Template, error) {
	pattern := filepath.Join("web", "templates", "*.gohtml")
	return template.ParseGlob(pattern)
}
