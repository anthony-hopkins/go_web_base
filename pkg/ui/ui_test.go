// Tests for template loading, health aggregation, HTMX vs full-page routing, state
// mutation, and error branches in shell rendering. Many tests chdir to the module root
// so web/templates and web/static resolve as in production.
package ui

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestNewAndHealth ensures New succeeds from the repo root and Health reports ok.
func TestNewAndHealth(t *testing.T) {
	t.Chdir(projectRoot(t))
	app, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	health := app.Health()
	if health.Status != "ok" {
		t.Fatalf("expected healthy app, got %#v", health)
	}
	if health.UptimeSec < 0 {
		t.Fatalf("expected non-negative uptime, got %d", health.UptimeSec)
	}
}

// TestHealthDegradedWhenCSSMissing simulates a deployment without web/static on disk.
func TestHealthDegradedWhenCSSMissing(t *testing.T) {
	t.Chdir(t.TempDir())
	app := &App{
		templates: templateMust(t),
		state: &state{
			tasks:        []string{"a"},
			lastUpdated:  time.Now().UTC(),
			serviceState: "Healthy",
		},
		startedAt: time.Now().UTC(),
	}
	health := app.Health()
	if health.Status != "degraded" {
		t.Fatalf("expected degraded health when css missing, got %#v", health)
	}
}

// TestHealthDegradedForMissingTemplateAndState uses incomplete templates and empty state.
func TestHealthDegradedForMissingTemplateAndState(t *testing.T) {
	t.Chdir(t.TempDir())
	tmpl := template.New("shell")
	template.Must(tmpl.New("shell").Parse("{{define \"shell\"}}ok{{end}}"))
	app := &App{
		templates: tmpl,
		state:     &state{},
		startedAt: time.Now().UTC(),
	}
	health := app.Health()
	if health.Status != "degraded" {
		t.Fatalf("expected degraded health, got %#v", health)
	}
}

// TestTemplateHelpers covers renderPanelTemplate and renderPanel success/failure paths.
func TestTemplateHelpers(t *testing.T) {
	t.Chdir(projectRoot(t))
	app, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	panel := panelViewModel{
		Now:          time.Now().UTC().Format(time.RFC1123),
		Tasks:        []string{"x"},
		LastUpdated:  time.Now().UTC().Format(time.RFC1123),
		ServiceState: "Healthy",
	}
	html, err := app.renderPanelTemplate("dashboard", panel)
	if err != nil {
		t.Fatalf("renderPanelTemplate failed: %v", err)
	}
	if !strings.Contains(html, "Service Dashboard") {
		t.Fatalf("unexpected rendered dashboard content: %s", html)
	}

	rec := httptest.NewRecorder()
	app.renderPanel(rec, "settings", panel)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected successful panel render, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	app.renderPanel(rec, "missing-panel", panel)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing template, got %d", rec.Code)
	}
}

// TestRouteHandlers exercises each HTTP handler for full-page vs HTMX behavior.
func TestRouteHandlers(t *testing.T) {
	t.Chdir(projectRoot(t))
	app, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Shell route.
	rec := httptest.NewRecorder()
	app.handleShell(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), appTitle) {
		t.Fatalf("unexpected shell response: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// Dashboard route — direct GET serves full shell + CSS.
	rec = httptest.NewRecorder()
	app.handleDashboard(rec, httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil))
	dashBody := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(dashBody, "Service Dashboard") || !strings.Contains(dashBody, "app.css") {
		t.Fatalf("unexpected dashboard response: code=%d body=%q", rec.Code, dashBody)
	}

	// Dashboard HTMX — fragment only (no document shell).
	rec = httptest.NewRecorder()
	hxDash := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	hxDash.Header.Set("HX-Request", "true")
	app.handleDashboard(rec, hxDash)
	dashFrag := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(dashFrag, "Service Dashboard") || strings.Contains(strings.ToLower(dashFrag), "<!doctype") {
		t.Fatalf("unexpected HTMX dashboard response: code=%d body=%q", rec.Code, dashFrag)
	}

	// Tasks GET route — full page.
	rec = httptest.NewRecorder()
	app.handleTasks(rec, httptest.NewRequest(http.MethodGet, "/ui/tasks", nil))
	tasksBody := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(tasksBody, "Operations Tasks") || !strings.Contains(tasksBody, "app.css") {
		t.Fatalf("unexpected tasks response: code=%d body=%q", rec.Code, tasksBody)
	}

	// Tasks POST route with value — full page when not HTMX.
	form := url.Values{}
	form.Set("task", "  new task  ")
	req := httptest.NewRequest(http.MethodPost, "/ui/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	app.handleCreateTask(rec, req)
	postBody := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(postBody, "new task") || !strings.Contains(postBody, "app.css") {
		t.Fatalf("unexpected task create response: code=%d body=%q", rec.Code, postBody)
	}

	// Tasks POST with HTMX — fragment only.
	form = url.Values{}
	form.Set("task", "  htmx task  ")
	req = httptest.NewRequest(http.MethodPost, "/ui/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	app.handleCreateTask(rec, req)
	htmxPost := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(htmxPost, "htmx task") || strings.Contains(strings.ToLower(htmxPost), "<!doctype") {
		t.Fatalf("unexpected HTMX task create response: code=%d body=%q", rec.Code, htmxPost)
	}

	// Settings route — full page.
	rec = httptest.NewRecorder()
	app.handleSettings(rec, httptest.NewRequest(http.MethodGet, "/ui/settings", nil))
	setBody := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(setBody, "Environment Settings") || !strings.Contains(setBody, "app.css") {
		t.Fatalf("unexpected settings response: code=%d body=%q", rec.Code, setBody)
	}

	// Empty task input should still return tasks fragment.
	form = url.Values{}
	form.Set("task", "   ")
	req = httptest.NewRequest(http.MethodPost, "/ui/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	app.handleCreateTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected empty task submit to still return 200, got %d", rec.Code)
	}
}

// TestHandleShellErrorBranches verifies 500 when dashboard or shell template is missing.
func TestHandleShellErrorBranches(t *testing.T) {
	t.Parallel()
	// Missing dashboard should fail initial fragment render.
	tmpl := template.New("shell")
	template.Must(tmpl.New("shell").Parse(`{{define "shell"}}ok{{end}}`))
	app := &App{
		templates: tmpl,
		state: &state{
			tasks:        []string{"a"},
			lastUpdated:  time.Now().UTC(),
			serviceState: "Healthy",
		},
	}
	rec := httptest.NewRecorder()
	app.handleShell(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing dashboard template, got %d", rec.Code)
	}

	// Missing shell should fail second render stage.
	tmpl = template.New("shell")
	template.Must(tmpl.New("dashboard").Parse(`{{define "dashboard"}}ok{{end}}`))
	app.templates = tmpl
	rec = httptest.NewRecorder()
	app.handleShell(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing shell template, got %d", rec.Code)
	}
}

// TestRegisterRoutes checks static asset wiring through a live ServeMux.
func TestRegisterRoutes(t *testing.T) {
	t.Chdir(projectRoot(t))
	app, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	mux := http.NewServeMux()
	app.RegisterRoutes(struct {
		*http.ServeMux
	}{mux})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected static css route to resolve, got %d", rec.Code)
	}
}

// TestStateSnapshotAndMutation validates addTask ordering and snapshot copy behavior.
func TestStateSnapshotAndMutation(t *testing.T) {
	t.Parallel()
	s := &state{
		tasks:        []string{"a"},
		lastUpdated:  time.Now().UTC(),
		serviceState: "Healthy",
	}
	before := s.snapshot()
	s.addTask("b")
	after := s.snapshot()
	if len(after.Tasks) != len(before.Tasks)+1 {
		t.Fatalf("expected task append, before=%d after=%d", len(before.Tasks), len(after.Tasks))
	}
}

// TestHealthJSONMarshaling is a smoke test for HealthResponse JSON field names.
func TestHealthJSONMarshaling(t *testing.T) {
	t.Parallel()
	h := HealthResponse{
		Status:    "ok",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		UptimeSec: 1,
		Checks:    map[string]string{"a": "ok"},
	}
	b, err := json.Marshal(h)
	if err != nil || !strings.Contains(string(b), `"status":"ok"`) {
		t.Fatalf("unexpected marshal result: %s err=%v", string(b), err)
	}
}

// TestLoadTemplatesFailure expects ParseGlob to fail when no template files exist.
func TestLoadTemplatesFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := loadTemplates()
	if err == nil {
		t.Fatal("expected loadTemplates to fail in empty temp dir")
	}
}

// TestNewFailure ensures New returns an error when templates cannot be loaded.
func TestNewFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := New()
	if err == nil {
		t.Fatal("expected New to fail when templates are unavailable")
	}
}

// TestHandleCreateTaskParseError returns 400 when the form body is not parseable.
func TestHandleCreateTaskParseError(t *testing.T) {
	t.Chdir(projectRoot(t))
	app, err := New()
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/ui/tasks", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	app.handleCreateTask(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed form body, got %d", rec.Code)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller")
	}
	// pkg/ui/ui_test.go -> project root is two dirs up.
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// templateMust builds a minimal valid template set for health/degraded tests in isolation.
func templateMust(t *testing.T) *template.Template {
	t.Helper()
	tmpl := template.New("shell")
	template.Must(tmpl.New("shell").Parse("{{define \"shell\"}}ok{{end}}"))
	template.Must(tmpl.New("dashboard").Parse("{{define \"dashboard\"}}ok{{end}}"))
	template.Must(tmpl.New("tasks").Parse("{{define \"tasks\"}}ok{{end}}"))
	template.Must(tmpl.New("settings").Parse("{{define \"settings\"}}ok{{end}}"))
	return tmpl
}
