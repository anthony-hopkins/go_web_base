// Tests for Server route registration, TLS load failures, Start/shutdown lifecycle with
// injected listeners and signals, and PEM generation helpers for the TLS code path.
package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestServerRouteRegistrationAndProtection exercises the raw mux: public, protected, and direct routes.
func TestServerRouteRegistrationAndProtection(t *testing.T) {
	t.Parallel()
	cfg := Config{
		APIKey:             "secret",
		Domain:             "example.com",
		HTTPSPort:          ":0",
		MaxHeaderBytes:     1024,
		MaxBodyBytes:       1024,
		ShutdownTimeout:    2 * time.Second,
		CORSAllowedOrigins: []string{"*"},
		CORSAllowedMethods: []string{"GET", "OPTIONS"},
		CORSAllowedHeaders: []string{"Content-Type"},
		CORSExposedHeaders: []string{"X-Request-ID"},
		CORSMaxAgeSeconds:  60,
	}

	s := New(cfg)
	s.HandleFunc("GET /public", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	s.HandleProtectedFunc("GET /protected", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	s.HandleProtected("GET /protected2", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	s.Handle("GET /direct", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/public", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for public route, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/protected", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without api key, got %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected protected route success, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/protected2", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected protected2 route success, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/direct", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected direct route status, got %d", rec.Code)
	}
}

// TestServerStartTLSLoadError expects Start to fail when cert paths do not exist.
func TestServerStartTLSLoadError(t *testing.T) {
	t.Parallel()
	s := New(Config{
		APIKey:          "secret",
		Domain:          "example.com",
		HTTPSPort:       ":0",
		TLSCertFile:     "/non-existent/cert.pem",
		TLSKeyFile:      "/non-existent/key.pem",
		MaxHeaderBytes:  1024,
		MaxBodyBytes:    1024,
		ShutdownTimeout: time.Second,
	})

	err := s.Start()
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected cert loading error, got %v", err)
	}
}

// TestServerStartAndShutdownBySignal fakes signal delivery and HTTP listener for graceful exit.
func TestServerStartAndShutdownBySignal(t *testing.T) {
	oldNotify := signalNotify
	oldListen, oldShutdown := listenAndServe, shutdownServer
	t.Cleanup(func() {
		signalNotify = oldNotify
		listenAndServe = oldListen
		shutdownServer = oldShutdown
	})
	signalNotify = func(c chan<- os.Signal, sig ...os.Signal) {
		go func() {
			time.Sleep(100 * time.Millisecond)
			c <- os.Interrupt
		}()
	}
	listenAndServe = func(s *http.Server) error { return http.ErrServerClosed }

	s := New(Config{
		APIKey:             "secret",
		Domain:             "example.com",
		HTTPSPort:          ":0",
		MaxHeaderBytes:     2048,
		MaxBodyBytes:       2048,
		ShutdownTimeout:    2 * time.Second,
		CORSAllowedOrigins: []string{"*"},
	})
	s.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	done := make(chan error, 1)
	go func() {
		done <- s.Start()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected graceful shutdown, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

// TestServerStartErrorFromListener surfaces listener failures that occur before shutdown.
func TestServerStartErrorFromListener(t *testing.T) {
	oldNotify := signalNotify
	oldListen := listenAndServe
	t.Cleanup(func() {
		signalNotify = oldNotify
		listenAndServe = oldListen
	})
	signalNotify = func(c chan<- os.Signal, sig ...os.Signal) {}
	listenAndServe = func(s *http.Server) error { return errors.New("listen failed") }

	s := New(Config{
		APIKey:          "secret",
		Domain:          "example.com",
		HTTPSPort:       ":0",
		MaxHeaderBytes:  1024,
		MaxBodyBytes:    1024,
		ShutdownTimeout: time.Second,
	})
	err := s.Start()
	if err == nil || !strings.Contains(err.Error(), "Primary server failed") {
		t.Fatalf("expected listener startup error, got %v", err)
	}
}

// TestServerShutdownError propagates errors from http.Server.Shutdown.
func TestServerShutdownError(t *testing.T) {
	oldNotify := signalNotify
	oldListen := listenAndServe
	oldShutdown := shutdownServer
	t.Cleanup(func() {
		signalNotify = oldNotify
		listenAndServe = oldListen
		shutdownServer = oldShutdown
	})
	signalNotify = func(c chan<- os.Signal, sig ...os.Signal) {
		go func() { c <- os.Interrupt }()
	}
	listenAndServe = func(s *http.Server) error { return http.ErrServerClosed }
	shutdownServer = func(s *http.Server, ctx context.Context) error { return errors.New("shutdown failed") }

	s := New(Config{
		APIKey:          "secret",
		Domain:          "example.com",
		HTTPSPort:       ":0",
		MaxHeaderBytes:  1024,
		MaxBodyBytes:    1024,
		ShutdownTimeout: time.Second,
	})
	err := s.Start()
	if err == nil || !strings.Contains(err.Error(), "server forced to shutdown") {
		t.Fatalf("expected shutdown failure, got %v", err)
	}
}

// TestServerStartWithTLSBranch covers ListenAndServeTLS with real PEM files on disk.
func TestServerStartWithTLSBranch(t *testing.T) {
	oldNotify := signalNotify
	oldListenTLS := listenAndServeTLS
	t.Cleanup(func() {
		signalNotify = oldNotify
		listenAndServeTLS = oldListenTLS
	})
	signalNotify = func(c chan<- os.Signal, sig ...os.Signal) {
		go func() { c <- os.Interrupt }()
	}
	listenAndServeTLS = func(s *http.Server) error { return http.ErrServerClosed }

	certFile, keyFile := writeSelfSignedCert(t)
	s := New(Config{
		APIKey:          "secret",
		Domain:          "example.com",
		HTTPSPort:       ":0",
		TLSCertFile:     certFile,
		TLSKeyFile:      keyFile,
		MaxHeaderBytes:  1024,
		MaxBodyBytes:    1024,
		ShutdownTimeout: time.Second,
	})
	if err := s.Start(); err != nil {
		t.Fatalf("expected TLS startup path to succeed, got %v", err)
	}
}

// writeSelfSignedCert creates a temporary RSA cert/key pair for TLS integration tests.
func writeSelfSignedCert(t *testing.T) (string, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed generating key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed creating certificate: %v", err)
	}

	certOut, err := os.CreateTemp(t.TempDir(), "cert-*.pem")
	if err != nil {
		t.Fatalf("failed creating cert file: %v", err)
	}
	keyOut, err := os.CreateTemp(t.TempDir(), "key-*.pem")
	if err != nil {
		t.Fatalf("failed creating key file: %v", err)
	}
	defer certOut.Close()
	defer keyOut.Close()

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		t.Fatalf("failed writing cert pem: %v", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("failed marshaling key: %v", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		t.Fatalf("failed writing key pem: %v", err)
	}
	return certOut.Name(), keyOut.Name()
}
