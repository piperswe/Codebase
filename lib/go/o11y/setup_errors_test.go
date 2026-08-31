package o11y

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/go-chi/httplog/v3"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestSetupFailurePaths(t *testing.T) {
	wantErr := errors.New("injected failure")

	t.Run("logging", func(t *testing.T) {
		deps, _, _ := successfulSetupDependencies()
		deps.setupLogging = func(string, string) (*slog.Logger, error) { return nil, wantErr }
		o, logger, err := setup(context.Background(), Config{}, deps)
		if o != nil || logger != nil {
			t.Error("logging failure returned partially initialized values")
		}
		assertWrappedError(t, err, wantErr, "setup logging")
	})

	t.Run("resource", func(t *testing.T) {
		deps, _, _ := successfulSetupDependencies()
		deps.newResource = func(context.Context, string, string) (*resource.Resource, error) {
			return nil, wantErr
		}
		o, logger, err := setup(context.Background(), Config{}, deps)
		if o == nil || logger == nil || o.LogSchema() != httplog.SchemaOTEL {
			t.Error("resource failure did not return logging's partial setup")
		}
		assertWrappedError(t, err, wantErr, "build resource")
	})

	t.Run("meter", func(t *testing.T) {
		deps, _, _ := successfulSetupDependencies()
		deps.newMeterProvider = func(prometheus.Registerer, *resource.Resource) (meterProvider, error) {
			return nil, wantErr
		}
		o, logger, err := setup(context.Background(), Config{}, deps)
		if o == nil || logger == nil || o.registry == nil {
			t.Error("meter failure did not return the initialized logger and registry")
		}
		assertWrappedError(t, err, wantErr, "create prometheus exporter")
	})

	t.Run("tracer", func(t *testing.T) {
		deps, meter, _ := successfulSetupDependencies()
		deps.getenv = func(key string) string {
			if key == "OTEL_EXPORTER_OTLP_ENDPOINT" {
				return "configured"
			}
			return ""
		}
		deps.newTracerProvider = func(context.Context, *resource.Resource) (tracerProvider, error) {
			return nil, wantErr
		}
		o, logger, err := setup(context.Background(), Config{}, deps)
		if o == nil || logger == nil || o.meter != meter {
			t.Error("tracer failure did not retain the working meter setup")
		}
		assertWrappedError(t, err, wantErr, "create otlp trace exporter")
	})
}
