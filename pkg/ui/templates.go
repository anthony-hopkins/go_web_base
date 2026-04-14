package ui

import (
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
)

func (a *App) renderPanel(w http.ResponseWriter, panel string, model panelViewModel) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, panel, model); err != nil {
		http.Error(w, "Failed to render panel", http.StatusInternalServerError)
		slog.Error("Failed to render panel", "panel", panel, "error", err)
	}
}

func (a *App) renderPanelTemplate(panel string, model panelViewModel) (string, error) {
	var b strings.Builder
	err := a.templates.ExecuteTemplate(&b, panel, model)
	return b.String(), err
}

func loadTemplates() (*template.Template, error) {
	pattern := filepath.Join("web", "templates", "*.gohtml")
	return template.ParseGlob(pattern)
}
