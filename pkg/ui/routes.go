package ui

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// handleShell serves GET /. It always embeds the dashboard panel inside the full shell
// template (navigation, stylesheet link, HTMX script). Used for the home page and tests
// the same rendering path as renderShell for the "dashboard" panel name.
func (a *App) handleShell(w http.ResponseWriter, r *http.Request) {
	if err := a.renderShell(w, "dashboard", a.state.snapshot()); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		slog.Error("Failed to render shell", "panel", "dashboard", "error", err)
	}
}

// renderShell renders a full HTML document: it executes the named panel template into a
// string, injects that HTML into shellViewModel.Content, then executes the "shell"
// template. Callers must handle errors with 500 responses; successful writes set
// Content-Type to text/html.
func (a *App) renderShell(w http.ResponseWriter, panel string, model panelViewModel) error {
	fragment, err := a.renderPanelTemplate(panel, model)
	if err != nil {
		return err
	}
	view := shellViewModel{
		Title:   appTitle,
		Now:     time.Now().UTC().Format(time.RFC1123),
		Content: template.HTML(fragment),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return a.templates.ExecuteTemplate(w, "shell", view)
}

// isHTMXFragment reports whether the request should receive a panel fragment only.
// HTMX sets HX-Request: true on boosted navigation and form posts so the response can be
// swapped into #spa-content without duplicating the outer shell or reloading CSS.
func isHTMXFragment(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// handleDashboard serves GET /ui/dashboard. Full page for direct visits; fragment when HTMX.
func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	model := a.state.snapshot()
	if isHTMXFragment(r) {
		a.renderPanel(w, "dashboard", model)
		return
	}
	if err := a.renderShell(w, "dashboard", model); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		slog.Error("Failed to render shell", "panel", "dashboard", "error", err)
	}
}

// handleTasks serves GET /ui/tasks. Full page for direct visits; fragment when HTMX.
func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	model := a.state.snapshot()
	if isHTMXFragment(r) {
		a.renderPanel(w, "tasks", model)
		return
	}
	if err := a.renderShell(w, "tasks", model); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		slog.Error("Failed to render shell", "panel", "tasks", "error", err)
	}
}

// handleCreateTask serves POST /ui/tasks. Parses form field "task", prepends non-empty
// trimmed values to the task list, then re-renders the tasks panel (fragment or shell).
func (a *App) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form payload", http.StatusBadRequest)
		return
	}

	task := strings.TrimSpace(r.FormValue("task"))
	if task != "" {
		a.state.addTask(task)
	}
	model := a.state.snapshot()
	if isHTMXFragment(r) {
		a.renderPanel(w, "tasks", model)
		return
	}
	if err := a.renderShell(w, "tasks", model); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		slog.Error("Failed to render shell", "panel", "tasks", "error", err)
	}
}

// handleSettings serves GET /ui/settings. Full page for direct visits; fragment when HTMX.
func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	model := a.state.snapshot()
	if isHTMXFragment(r) {
		a.renderPanel(w, "settings", model)
		return
	}
	if err := a.renderShell(w, "settings", model); err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		slog.Error("Failed to render shell", "panel", "settings", "error", err)
	}
}
