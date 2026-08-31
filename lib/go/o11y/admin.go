package o11y

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-faster/errors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// defaultMetricsPort is the admin server port used when METRICS_PORT is unset.
const defaultMetricsPort = "9090"

// adminAddr returns the admin server listen address derived from METRICS_PORT
// (default 9090).
func adminAddr() string {
	return adminAddrFromEnv(os.Getenv)
}

func adminAddrFromEnv(getenv func(string) string) string {
	port := getenv("METRICS_PORT")
	if port == "" {
		port = defaultMetricsPort
	}
	return ":" + port
}

// Handler returns the Prometheus /metrics HTTP handler backed by this
// telemetry's registry.
func (o *O11y) Handler() http.Handler {
	return promhttp.HandlerFor(o.registry, promhttp.HandlerOpts{})
}

func (o *O11y) adminHandler() http.Handler {
	mux := http.NewServeMux()
	if o.registry != nil {
		mux.Handle("/metrics", o.Handler())
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

type adminServer interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

// ServeAdmin runs the admin HTTP server exposing Prometheus /metrics and a
// static /healthz endpoint. It listens on METRICS_PORT (default 9090) and
// blocks until ctx is cancelled, after which it gracefully shuts down. It
// returns any error other than http.ErrServerClosed.
func (o *O11y) ServeAdmin(ctx context.Context, _ *slog.Logger) error {
	return serveAdmin(ctx, &http.Server{Addr: adminAddr(), Handler: o.adminHandler()})
}

func serveAdmin(ctx context.Context, srv adminServer) error {
	serveErr := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return errors.Wrap(err, "shutdown admin server")
		}
		return nil
	}
}
