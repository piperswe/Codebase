// Package telemetry wires up OpenTelemetry tracing and Prometheus metrics for
// datasite. It registers global providers so that already-instrumented code
// (the ogen API server, otelchi middleware, otelsql, and the moviedb layer)
// exports without any further wiring.
//
// Metrics are always exported to a dedicated Prometheus registry served on the
// admin port. Traces are exported via OTLP only when an OTLP endpoint is
// configured (OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_TRACES_ENDPOINT);
// otherwise the global no-op tracer is left in place at near-zero cost.
package telemetry

import (
	"context"
	"net/http"
	"os"

	"github.com/go-faster/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Telemetry holds the configured providers and the Prometheus registry backing
// the /metrics endpoint. Call Shutdown to flush and stop the exporters.
type Telemetry struct {
	registry *prometheus.Registry
	tracer   *sdktrace.TracerProvider // nil when no OTLP endpoint is configured
	meter    *sdkmetric.MeterProvider
}

// Setup initializes OpenTelemetry: a shared resource, the W3C TraceContext
// propagator, a Prometheus-backed MeterProvider (always), and — when an OTLP
// endpoint is configured — an OTLP TracerProvider. Both providers are registered
// as OTel globals so instrumented libraries pick them up automatically.
func Setup(ctx context.Context, serviceName, serviceVersion string) (*Telemetry, error) {
	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, errors.Wrap(err, "build resource")
	}

	// Propagation is always safe to configure, even without a trace exporter.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	t := &Telemetry{}

	// MeterProvider (always on) backed by a dedicated Prometheus registry.
	t.registry = prometheus.NewRegistry()
	t.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	metricExporter, err := promexporter.New(promexporter.WithRegisterer(t.registry))
	if err != nil {
		return nil, errors.Wrap(err, "create prometheus exporter")
	}
	t.meter = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(metricExporter),
	)
	otel.SetMeterProvider(t.meter)

	// TracerProvider only when an OTLP endpoint is configured. Leaving the
	// global no-op tracer in place keeps spans near-free in dev.
	if otlpTracesConfigured() {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			// Don't fail startup on a bad collector; metrics still work.
			return t, errors.Wrap(err, "create otlp trace exporter")
		}
		t.tracer = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exp),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		)
		otel.SetTracerProvider(t.tracer)
	}

	return t, nil
}

// otlpTracesConfigured reports whether the standard OTLP env vars point at an
// endpoint we should export traces to.
func otlpTracesConfigured() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// Handler returns the Prometheus /metrics HTTP handler backed by this
// telemetry's registry.
func (t *Telemetry) Handler() http.Handler {
	return promhttp.HandlerFor(t.registry, promhttp.HandlerOpts{})
}

// Shutdown flushes and stops the meter and (if present) tracer providers.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var errs []error
	if t.tracer != nil {
		if err := t.tracer.Shutdown(ctx); err != nil {
			errs = append(errs, errors.Wrap(err, "shutdown tracer provider"))
		}
	}
	if t.meter != nil {
		if err := t.meter.Shutdown(ctx); err != nil {
			errs = append(errs, errors.Wrap(err, "shutdown meter provider"))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
