package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/anthony-hopkins/rest_api_template/pkg/server"
	"github.com/anthony-hopkins/rest_api_template/pkg/ui"
)

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

func TestRunSuccess(t *testing.T) {
	t.Chdir(projectRootMain(t))

	oldLoad, oldServer, oldUI := loadConfigFunc, newServerFunc, newUIFunc
	t.Cleanup(func() {
		loadConfigFunc, newServerFunc, newUIFunc = oldLoad, oldServer, oldUI
	})

	fs := &fakeServer{}
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
	newServerFunc = func(cfg server.Config) appServer { return fs }
	newUIFunc = ui.New

	if err := run(); err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if fs.funcs["GET /health"] == nil || fs.funcs["GET /livez"] == nil || fs.funcs["GET /readyz"] == nil {
		t.Fatalf("expected health handlers to be registered")
	}

	// Cover handler internals registered in run().
	rec := httptest.NewRecorder()
	fs.funcs["GET /livez"](rec, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected livez 200, got %d", rec.Code)
	}
	fs.funcs["GET /livez"](&failingWriter{}, httptest.NewRequest(http.MethodGet, "/livez", nil))

	rec = httptest.NewRecorder()
	fs.funcs["GET /health"](rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected health 200, got %d", rec.Code)
	}
	fs.funcs["GET /health"](&failingWriter{}, httptest.NewRequest(http.MethodGet, "/health", nil))

	t.Chdir(t.TempDir())
	rec = httptest.NewRecorder()
	fs.funcs["GET /health"](rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected degraded health 503, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	fs.funcs["GET /readyz"](rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected degraded readyz 503, got %d", rec.Code)
	}
	fs.funcs["GET /readyz"](&failingWriter{}, httptest.NewRequest(http.MethodGet, "/readyz", nil))
}

func TestRunConfigError(t *testing.T) {
	old := loadConfigFunc
	t.Cleanup(func() { loadConfigFunc = old })
	loadConfigFunc = func() (server.Config, error) { return server.Config{}, errors.New("bad config") }
	if err := run(); err == nil {
		t.Fatal("expected run to fail on config error")
	}
}

func TestRunUIInitError(t *testing.T) {
	oldLoad, oldUI := loadConfigFunc, newUIFunc
	t.Cleanup(func() { loadConfigFunc, newUIFunc = oldLoad, oldUI })
	loadConfigFunc = func() (server.Config, error) {
		return server.Config{APIKey: "k", Domain: "d"}, nil
	}
	newUIFunc = func() (*ui.App, error) { return nil, errors.New("ui failed") }
	if err := run(); err == nil {
		t.Fatal("expected run to fail on ui error")
	}
}

func TestRunServerStartError(t *testing.T) {
	oldLoad, oldServer, oldUI := loadConfigFunc, newServerFunc, newUIFunc
	t.Cleanup(func() { loadConfigFunc, newServerFunc, newUIFunc = oldLoad, oldServer, oldUI })

	loadConfigFunc = func() (server.Config, error) {
		return server.Config{APIKey: "k", Domain: "d"}, nil
	}
	newServerFunc = func(cfg server.Config) appServer { return &fakeServer{startErr: errors.New("boom")} }
	newUIFunc = func() (*ui.App, error) { return &ui.App{}, nil }

	if err := run(); err == nil {
		t.Fatal("expected run to fail on server start")
	}
}

func TestMainSuccess(t *testing.T) {
	t.Chdir(projectRootMain(t))
	oldLoad, oldServer, oldUI := loadConfigFunc, newServerFunc, newUIFunc
	t.Cleanup(func() { loadConfigFunc, newServerFunc, newUIFunc = oldLoad, oldServer, oldUI })

	loadConfigFunc = func() (server.Config, error) { return server.Config{APIKey: "k", Domain: "d"}, nil }
	newServerFunc = func(cfg server.Config) appServer { return &fakeServer{} }
	newUIFunc = ui.New
	main()
}

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

func projectRootMain(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller")
	}
	return filepath.Clean(filepath.Dir(file))
}
