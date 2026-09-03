package o11y

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-chi/httplog/v3"
	"github.com/go-faster/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type setupDependencies struct {
	getenv            func(string) string
	setupLogging      func(string, string) (*slog.Logger, error)
	newResource       func(context.Context, string, string) (*resource.Resource, error)
	newMeterProvider  func(prometheus.Registerer, *resource.Resource) (meterProvider, error)
	newTracerProvider func(context.Context, *resource.Resource) (tracerProvider, error)
	setDefaultLogger  func(*slog.Logger)
	setPropagator     func(propagation.TextMapPropagator)
	setMeterProvider  func(metric.MeterProvider)
	setTracerProvider func(trace.TracerProvider)
}

func defaultSetupDependencies() setupDependencies {
	return setupDependencies{
		getenv:            os.Getenv,
		setupLogging:      setupLogging,
		newResource:       newResource,
		newMeterProvider:  newMeterProvider,
		newTracerProvider: newTracerProvider,
		setDefaultLogger:  slog.SetDefault,
		setPropagator:     otel.SetTextMapPropagator,
		setMeterProvider:  otel.SetMeterProvider,
		setTracerProvider: otel.SetTracerProvider,
	}
}

// Setup initializes logging, OpenTelemetry, and Prometheus. It builds and sets
// the default slog logger, registers the W3C TraceContext propagator, installs
// a Prometheus-backed MeterProvider (always) and — when an OTLP endpoint is
// configured — an OTLP TracerProvider. Both providers are registered as OTel
// globals so instrumented libraries pick them up automatically. The returned
// logger is also the process-wide default.
func Setup(ctx context.Context, cfg Config) (*O11y, *slog.Logger, error) {
	return setup(ctx, cfg, defaultSetupDependencies())
}

func setup(ctx context.Context, cfg Config, deps setupDependencies) (*O11y, *slog.Logger, error) {
	serviceName := cfg.ServiceName
	if envName := deps.getenv("OTEL_SERVICE_NAME"); envName != "" {
		serviceName = envName
	}

	o := &O11y{serviceName: serviceName}
	logger, err := deps.setupLogging(serviceName, cfg.ServiceVersion)
	if err != nil {
		return nil, nil, errors.Wrap(err, "setup logging")
	}
	deps.setDefaultLogger(logger)
	o.logger = logger
	o.logSchema = httplog.SchemaOTEL

	res, err := deps.newResource(ctx, serviceName, cfg.ServiceVersion)
	if err != nil {
		return o, logger, errors.Wrap(err, "build resource")
	}

	deps.setPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	o.registry = prometheus.NewRegistry()
	o.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	o.meter, err = deps.newMeterProvider(o.registry, res)
	if err != nil {
		return o, logger, errors.Wrap(err, "create prometheus exporter")
	}
	deps.setMeterProvider(o.meter)

	if otlpTracesConfigured(deps.getenv) {
		o.tracer, err = deps.newTracerProvider(ctx, res)
		if err != nil {
			return o, logger, errors.Wrap(err, "create otlp trace exporter")
		}
		deps.setTracerProvider(o.tracer)
	}

	return o, logger, nil
}

func newResource(ctx context.Context, serviceName, serviceVersion string) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
}

func newMeterProvider(registerer prometheus.Registerer, res *resource.Resource) (meterProvider, error) {
	metricExporter, err := promexporter.New(promexporter.WithRegisterer(registerer))
	if err != nil {
		return nil, err
	}
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(metricExporter),
	), nil
}

func newTracerProvider(ctx context.Context, res *resource.Resource) (tracerProvider, error) {
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exp),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	), nil
}

// otlpTracesConfigured reports whether the standard OTLP environment variables
// point at an endpoint we should export traces to.
func otlpTracesConfigured(getenv func(string) string) bool {
	return getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}
