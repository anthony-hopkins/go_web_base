package ui

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func (a *App) handleShell(w http.ResponseWriter, r *http.Request) {
	fragment, err := a.renderPanelTemplate("dashboard", a.state.snapshot())
	if err != nil {
		http.Error(w, "Failed to render dashboard", http.StatusInternalServerError)
		slog.Error("Failed to render dashboard panel", "error", err)
		return
	}

	view := shellViewModel{
		Title:   appTitle,
		Now:     time.Now().UTC().Format(time.RFC1123),
		Content: template.HTML(fragment),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, "shell", view); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		slog.Error("Failed to render shell template", "error", err)
	}
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	a.renderPanel(w, "dashboard", a.state.snapshot())
}

func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	a.renderPanel(w, "tasks", a.state.snapshot())
}

func (a *App) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form payload", http.StatusBadRequest)
		return
	}

	task := strings.TrimSpace(r.FormValue("task"))
	if task != "" {
		a.state.addTask(task)
	}
	a.renderPanel(w, "tasks", a.state.snapshot())
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	a.renderPanel(w, "settings", a.state.snapshot())
}
