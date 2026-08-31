package o11y

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAdminAddrFromEnv(t *testing.T) {
	tests := []struct {
		port string
		want string
	}{
		{port: "", want: ":9090"},
		{port: "8081", want: ":8081"},
		{port: "0", want: ":0"},
	}
	for _, test := range tests {
		if got := adminAddrFromEnv(func(string) string { return test.port }); got != test.want {
			t.Errorf("port %q: got %q, want %q", test.port, got, test.want)
		}
	}
}

func TestAdminHandlerHealth(t *testing.T) {
	o := &O11y{}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	o.adminHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status=%d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "ok" {
		t.Errorf("body=%q, want ok", response.Body.String())
	}
}

func TestAdminHandlerMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "o11y_test_value", Help: "A test value."})
	gauge.Set(42)
	registry.MustRegister(gauge)
	o := &O11y{registry: registry}

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	o.adminHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status=%d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), "o11y_test_value 42") {
		t.Errorf("metrics body does not contain test gauge:\n%s", response.Body.String())
	}
}

func TestAdminHandlerOmitsMetricsWithoutRegistry(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	(&O11y{}).adminHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Errorf("status=%d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestServeAdminReturnsInvalidAddressError(t *testing.T) {
	t.Setenv("METRICS_PORT", "99999")
	err := (&O11y{}).ServeAdmin(context.Background(), slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("ServeAdmin returned no error for an invalid port")
	}
}

type fakeAdminServer struct {
	listenErr     error
	shutdownErr   error
	started       chan struct{}
	release       chan struct{}
	shutdownCalls int
	shutdownCtx   context.Context
	releaseOnce   sync.Once
}

func (s *fakeAdminServer) ListenAndServe() error {
	if s.started != nil {
		close(s.started)
	}
	if s.release != nil {
		<-s.release
	}
	return s.listenErr
}

func (s *fakeAdminServer) Shutdown(ctx context.Context) error {
	s.shutdownCalls++
	s.shutdownCtx = ctx
	if s.release != nil {
		s.releaseOnce.Do(func() { close(s.release) })
	}
	return s.shutdownErr
}

func TestServeAdminReturnsListenError(t *testing.T) {
	wantErr := errors.New("bind failed")
	err := serveAdmin(context.Background(), &fakeAdminServer{listenErr: wantErr})
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestServeAdminIgnoresServerClosed(t *testing.T) {
	if err := serveAdmin(context.Background(), &fakeAdminServer{listenErr: http.ErrServerClosed}); err != nil {
		t.Errorf("got %v, want nil", err)
	}
}

func TestServeAdminShutsDownWhenContextIsCancelled(t *testing.T) {
	server := &fakeAdminServer{
		listenErr: http.ErrServerClosed,
		started:   make(chan struct{}),
		release:   make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- serveAdmin(ctx, server) }()
	<-server.started
	cancel()

	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if server.shutdownCalls != 1 {
		t.Errorf("shutdown called %d times, want 1", server.shutdownCalls)
	}
	if _, ok := server.shutdownCtx.Deadline(); !ok {
		t.Error("shutdown context has no deadline")
	}
}

func TestServeAdminWrapsShutdownError(t *testing.T) {
	wantErr := errors.New("shutdown failed")
	server := &fakeAdminServer{
		listenErr:   http.ErrServerClosed,
		shutdownErr: wantErr,
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- serveAdmin(ctx, server) }()
	<-server.started
	cancel()
	assertWrappedError(t, <-result, wantErr, "shutdown admin server")
}
