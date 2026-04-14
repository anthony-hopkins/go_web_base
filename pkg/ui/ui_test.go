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

	// Dashboard route.
	rec = httptest.NewRecorder()
	app.handleDashboard(rec, httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Service Dashboard") {
		t.Fatalf("unexpected dashboard response: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// Tasks GET route.
	rec = httptest.NewRecorder()
	app.handleTasks(rec, httptest.NewRequest(http.MethodGet, "/ui/tasks", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Operations Tasks") {
		t.Fatalf("unexpected tasks response: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// Tasks POST route with value.
	form := url.Values{}
	form.Set("task", "  new task  ")
	req := httptest.NewRequest(http.MethodPost, "/ui/tasks", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	app.handleCreateTask(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "new task") {
		t.Fatalf("unexpected task create response: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// Settings route.
	rec = httptest.NewRecorder()
	app.handleSettings(rec, httptest.NewRequest(http.MethodGet, "/ui/settings", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Environment Settings") {
		t.Fatalf("unexpected settings response: code=%d body=%q", rec.Code, rec.Body.String())
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

func TestLoadTemplatesFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := loadTemplates()
	if err == nil {
		t.Fatal("expected loadTemplates to fail in empty temp dir")
	}
}

func TestNewFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := New()
	if err == nil {
		t.Fatal("expected New to fail when templates are unavailable")
	}
}

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

func templateMust(t *testing.T) *template.Template {
	t.Helper()
	tmpl := template.New("shell")
	template.Must(tmpl.New("shell").Parse("{{define \"shell\"}}ok{{end}}"))
	template.Must(tmpl.New("dashboard").Parse("{{define \"dashboard\"}}ok{{end}}"))
	template.Must(tmpl.New("tasks").Parse("{{define \"tasks\"}}ok{{end}}"))
	template.Must(tmpl.New("settings").Parse("{{define \"settings\"}}ok{{end}}"))
	return tmpl
}
