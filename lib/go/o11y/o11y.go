// Package o11y unifies observability infrastructure — structured logging,
// OpenTelemetry tracing, and Prometheus metrics — behind a single Setup call.
//
// Setup honors these environment variables:
//   - OTEL_SERVICE_NAME overrides Config.ServiceName.
//   - OTEL_EXPORTER_OTLP_ENDPOINT and OTEL_EXPORTER_OTLP_TRACES_ENDPOINT enable
//     OTLP trace export.
//   - LOG_FORMAT selects text (default), JSON, or systemd journal logging.
//   - METRICS_PORT selects the ServeAdmin port (default 9090).
package o11y

import (
	"context"
	"log/slog"

	"github.com/go-chi/httplog/v3"
	"github.com/go-faster/errors"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Config describes the service being instrumented. ServiceName is used for the
// OTel resource and log attributes unless OTEL_SERVICE_NAME is set, which takes
// precedence.
type Config struct {
	ServiceName    string
	ServiceVersion string
}

// O11y holds the configured providers, the Prometheus registry backing the
// /metrics endpoint, and the logger and schema for request logging. Call
// Shutdown to flush and stop the exporters.
type O11y struct {
	registry    *prometheus.Registry
	tracer      tracerProvider // nil when no OTLP endpoint is configured
	meter       meterProvider
	logger      *slog.Logger
	logSchema   *httplog.Schema
	serviceName string
}

type meterProvider interface {
	metric.MeterProvider
	Shutdown(context.Context) error
}

type tracerProvider interface {
	trace.TracerProvider
	Shutdown(context.Context) error
}

// Tracer returns a tracer for the given instrumentation scope. It resolves
// against whatever global TracerProvider is registered (the OTLP one, or the
// no-op default).
func (o *O11y) Tracer(scopeName string) trace.Tracer {
	return otel.Tracer(scopeName)
}

// LogSchema returns the httplog schema to use for request-logging middleware.
func (o *O11y) LogSchema() *httplog.Schema {
	return o.logSchema
}

// Shutdown flushes and stops the meter and (if present) tracer providers.
func (o *O11y) Shutdown(ctx context.Context) error {
	if o == nil {
		return nil
	}
	var errs []error
	if o.tracer != nil {
		if err := o.tracer.Shutdown(ctx); err != nil {
			errs = append(errs, errors.Wrap(err, "shutdown tracer provider"))
		}
	}
	if o.meter != nil {
		if err := o.meter.Shutdown(ctx); err != nil {
			errs = append(errs, errors.Wrap(err, "shutdown meter provider"))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
