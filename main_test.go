// Tests for main package bootstrap: dependency injection of config/server/UI factories,
// health handler wiring, degraded readiness when static fs has no app.css, and process
// exit codes. fakeServer records registered routes; failingWriter forces write errors
// for handler error branches.
package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/anthony-hopkins/rest_api_template/pkg/server"
	"github.com/anthony-hopkins/rest_api_template/pkg/ui"
)

// fakeServer implements appServer for tests: it stores Handle/HandleFunc registrations
// without listening, and Start returns startErr when set (e.g. to simulate startup failure).
type fakeServer struct {
	startErr error
	handles  map[string]http.Handler
	funcs    map[string]func(http.ResponseWriter, *http.Request)
}

func (f *fakeServer) Handle(pattern string, handler http.Handler) {
	if f.handles == nil {
		f.handles = map[string]http.Handler{}
	}
	f.handles[pattern] = handler
}

func (f *fakeServer) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	if f.funcs == nil {
		f.funcs = map[string]func(http.ResponseWriter, *http.Request){}
	}
	f.funcs[pattern] = handler
}

func (f *fakeServer) Start() error { return f.startErr }

// failingWriter implements http.ResponseWriter with Write always failing, used to
// exercise error logging paths in small JSON handlers without panicking.
type failingWriter struct {
	header http.Header
}

func (w *failingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *failingWriter) Write([]byte) (int, error)  { return 0, errors.New("write failed") }
func (w *failingWriter) WriteHeader(statusCode int) {}

// TestRunSuccess asserts run() registers health/livez/readyz and exercises their handlers
// including json encode failure paths.
func TestRunSuccess(t *testing.T) {
	t.Chdir(projectRootMain(t))

	oldLoad, oldServer, oldUI := loadConfigFunc, newServerFunc, newUIFunc
	t.Cleanup(func() {
		loadConfigFunc, newServerFunc, newUIFunc = oldLoad, oldServer, oldUI
	})

	mockSrv := &fakeServer{}
	loadConfigFunc = func() (server.Config, error) {
		return server.Config{
			APIKey:          "k",
			Domain:          "example.com",
			HTTPSPort:       ":0",
			MaxHeaderBytes:  1024,
			MaxBodyBytes:    1024,
			ShutdownTimeout: 1,
		}, nil
	}
	newServerFunc = func(cfg server.Config) appServer { return mockSrv }
	newUIFunc = func(templateFS, staticFS fs.FS) (*ui.App, error) { return ui.New(templateFS, staticFS) }

	if err := run(); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if mockSrv.funcs["GET /health"] == nil || mockSrv.funcs["GET /livez"] == nil || mockSrv.funcs["GET /readyz"] == nil {
		t.Fatalf("expected health handlers to be registered")
	}

	// Cover handler internals registered in run().
	rec := httptest.NewRecorder()
	mockSrv.funcs["GET /livez"](rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected livez 200, got %d", rec.Code)
	}
	mockSrv.funcs["GET /livez"](&failingWriter{}, httptest.NewRequest(http.MethodGet, "/livez", nil))

	rec = httptest.NewRecorder()
	mockSrv.funcs["GET /health"](rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d", rec.Code)
	}
	mockSrv.funcs["GET /health"](&failingWriter{}, httptest.NewRequest(http.MethodGet, "/health", nil))

	rec = httptest.NewRecorder()
	mockSrv.funcs["GET /readyz"](rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected readyz 200, got %d", rec.Code)
	}
	mockSrv.funcs["GET /readyz"](&failingWriter{}, httptest.NewRequest(http.MethodGet, "/readyz", nil))
}

// TestRunHealthDegradedWhenStaticMissing asserts /health and /readyz return 503 when the UI
// is wired with an empty static tree (no app.css), while templates come from embed.
func TestRunHealthDegradedWhenStaticMissing(t *testing.T) {
	t.Chdir(projectRootMain(t))

	oldLoad, oldServer, oldUI := loadConfigFunc, newServerFunc, newUIFunc
	t.Cleanup(func() {
		loadConfigFunc, newServerFunc, newUIFunc = oldLoad, oldServer, oldUI
	})

	mockSrv := &fakeServer{}
	loadConfigFunc = func() (server.Config, error) {
		return server.Config{
			APIKey:          "k",
			Domain:          "example.com",
			HTTPSPort:       ":0",
			MaxHeaderBytes:  1024,
			MaxBodyBytes:    1024,
			ShutdownTimeout: 1,
		}, nil
	}
	newServerFunc = func(cfg server.Config) appServer { return mockSrv }
	newUIFunc = func(tpl fs.FS, _ fs.FS) (*ui.App, error) {
		return ui.New(tpl, os.DirFS(t.TempDir()))
	}

	if err := run(); err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	mockSrv.funcs["GET /health"](rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected degraded health 503, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mockSrv.funcs["GET /readyz"](rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected degraded readyz 503, got %d", rec.Code)
	}
}

// TestRunConfigError ensures configuration failures surface from run() without starting the server.
func TestRunConfigError(t *testing.T) {
	old := loadConfigFunc
	t.Cleanup(func() { loadConfigFunc = old })
	loadConfigFunc = func() (server.Config, error) { return server.Config{}, errors.New("bad config") }
	if err := run(); err == nil {
		t.Fatal("expected run to fail on config error")
	}
}

// TestRunUIInitError covers failure to construct the UI app (e.g. missing templates).
func TestRunUIInitError(t *testing.T) {
	oldLoad, oldUI := loadConfigFunc, newUIFunc
	t.Cleanup(func() { loadConfigFunc, newUIFunc = oldLoad, oldUI })
	loadConfigFunc = func() (server.Config, error) {
		return server.Config{APIKey: "k", Domain: "d"}, nil
	}
	newUIFunc = func(_, _ fs.FS) (*ui.App, error) { return nil, errors.New("ui failed") }
	if err := run(); err == nil {
		t.Fatal("expected run to fail on ui error")
	}
}

// TestRunServerStartError propagates errors when the server fails immediately at Start().
func TestRunServerStartError(t *testing.T) {
	oldLoad, oldServer, oldUI := loadConfigFunc, newServerFunc, newUIFunc
	t.Cleanup(func() { loadConfigFunc, newServerFunc, newUIFunc = oldLoad, oldServer, oldUI })

	loadConfigFunc = func() (server.Config, error) {
		return server.Config{APIKey: "k", Domain: "d"}, nil
	}
	newServerFunc = func(cfg server.Config) appServer { return &fakeServer{startErr: errors.New("boom")} }
	newUIFunc = func(_, _ fs.FS) (*ui.App, error) { return &ui.App{}, nil }

	if err := run(); err == nil {
		t.Fatal("expected run to fail on server start")
	}
}

// TestMainSuccess invokes main() with stubbed dependencies that do not exit the process.
func TestMainSuccess(t *testing.T) {
	t.Chdir(projectRootMain(t))
	oldLoad, oldServer, oldUI := loadConfigFunc, newServerFunc, newUIFunc
	t.Cleanup(func() { loadConfigFunc, newServerFunc, newUIFunc = oldLoad, oldServer, oldUI })

	loadConfigFunc = func() (server.Config, error) { return server.Config{APIKey: "k", Domain: "d"}, nil }
	newServerFunc = func(cfg server.Config) appServer { return &fakeServer{} }
	newUIFunc = func(templateFS, staticFS fs.FS) (*ui.App, error) { return ui.New(templateFS, staticFS) }
	main()
}

// TestMainErrorPathExits verifies main exits with code 1 when run fails at startup.
func TestMainErrorPathExits(t *testing.T) {
	oldLoad, oldExit := loadConfigFunc, exitFunc
	t.Cleanup(func() { loadConfigFunc, exitFunc = oldLoad, oldExit })
	loadConfigFunc = func() (server.Config, error) { return server.Config{}, errors.New("nope") }

	exitCode := -1
	exitFunc = func(code int) { exitCode = code }
	main()
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
}

// projectRootMain returns the directory containing main.go so tests can chdir to the module root.
func projectRootMain(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller")
	}
	return filepath.Clean(filepath.Dir(file))
}
