package o11y

import (
	"context"
	"log/slog"
	"testing"

	"github.com/go-chi/httplog/v3"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

func TestSetupConfiguresRequiredComponents(t *testing.T) {
	deps, meter, _ := successfulSetupDependencies()
	wantResource := resource.NewSchemaless()

	var logName, logVersion string
	deps.setupLogging = func(name, version string) (*slog.Logger, error) {
		logName, logVersion = name, version
		return slog.New(slog.DiscardHandler), nil
	}
	var resourceName, resourceVersion string
	deps.newResource = func(_ context.Context, name, version string) (*resource.Resource, error) {
		resourceName, resourceVersion = name, version
		return wantResource, nil
	}
	var registered prometheus.Registerer
	var meterResource *resource.Resource
	deps.newMeterProvider = func(r prometheus.Registerer, res *resource.Resource) (meterProvider, error) {
		registered, meterResource = r, res
		return meter, nil
	}
	var defaultLogger *slog.Logger
	deps.setDefaultLogger = func(logger *slog.Logger) { defaultLogger = logger }
	var propagator propagation.TextMapPropagator
	deps.setPropagator = func(p propagation.TextMapPropagator) { propagator = p }
	var installedMeter metric.MeterProvider
	deps.setMeterProvider = func(p metric.MeterProvider) { installedMeter = p }
	deps.newTracerProvider = func(context.Context, *resource.Resource) (tracerProvider, error) {
		t.Fatal("tracer provider must not be created without an OTLP endpoint")
		return nil, nil
	}
	deps.setTracerProvider = func(trace.TracerProvider) {
		t.Fatal("tracer provider must not be installed without an OTLP endpoint")
	}

	o, logger, err := setup(context.Background(), Config{
		ServiceName:    "catalog",
		ServiceVersion: "1.2.3",
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if logger == nil || defaultLogger != logger {
		t.Fatal("returned logger was not installed as the default")
	}
	if o.logger != logger {
		t.Fatal("returned logger was not retained for request logging")
	}
	if logName != "catalog" || resourceName != "catalog" {
		t.Errorf("service name: logging=%q resource=%q, want catalog", logName, resourceName)
	}
	if logVersion != "1.2.3" || resourceVersion != "1.2.3" {
		t.Errorf("service version: logging=%q resource=%q, want 1.2.3", logVersion, resourceVersion)
	}
	if o.serviceName != "catalog" {
		t.Errorf("serviceName=%q, want catalog", o.serviceName)
	}
	if o.LogSchema() != httplog.SchemaOTEL {
		t.Error("OTEL log schema was not configured")
	}
	if registered != o.registry || meterResource != wantResource {
		t.Error("meter provider did not receive the setup registry and resource")
	}
	if o.meter != meter || installedMeter != meter {
		t.Error("meter provider was not retained and globally installed")
	}
	if o.tracer != nil {
		t.Error("unexpected tracer provider without an OTLP endpoint")
	}
	if propagator == nil {
		t.Fatal("text-map propagator was not installed")
	}
	wantFields := map[string]bool{"traceparent": true, "tracestate": true, "baggage": true}
	for _, field := range propagator.Fields() {
		delete(wantFields, field)
	}
	if len(wantFields) != 0 {
		t.Errorf("propagator is missing fields %v", wantFields)
	}

	metricFamilies, err := o.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(metricFamilies))
	for _, family := range metricFamilies {
		names[family.GetName()] = true
	}
	if !names["go_goroutines"] || !names["process_cpu_seconds_total"] {
		t.Errorf("default collectors missing from registry; got %v", names)
	}
}

func TestSetupUsesEnvironmentServiceName(t *testing.T) {
	deps, _, _ := successfulSetupDependencies()
	deps.getenv = func(key string) string {
		if key == "OTEL_SERVICE_NAME" {
			return "from-env"
		}
		return ""
	}
	var gotName string
	deps.setupLogging = func(name, _ string) (*slog.Logger, error) {
		gotName = name
		return slog.New(slog.DiscardHandler), nil
	}

	o, _, err := setup(context.Background(), Config{ServiceName: "from-config"}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "from-env" || o.serviceName != "from-env" {
		t.Errorf("service name: logger=%q o11y=%q, want from-env", gotName, o.serviceName)
	}
}

func TestSetupConfiguresTracerForEitherOTLPEndpoint(t *testing.T) {
	tests := []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"}
	for _, endpointVariable := range tests {
		t.Run(endpointVariable, func(t *testing.T) {
			deps, _, tracer := successfulSetupDependencies()
			deps.getenv = func(key string) string {
				if key == endpointVariable {
					return "http://collector:4318"
				}
				return ""
			}
			var installed trace.TracerProvider
			deps.setTracerProvider = func(p trace.TracerProvider) { installed = p }

			o, _, err := setup(context.Background(), Config{ServiceName: "svc"}, deps)
			if err != nil {
				t.Fatal(err)
			}
			if o.tracer != tracer || installed != tracer {
				t.Error("tracer provider was not retained and globally installed")
			}
		})
	}
}

func TestOTLPTracesConfigured(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   bool
	}{
		{name: "unset", values: map[string]string{}, want: false},
		{name: "general endpoint", values: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "collector"}, want: true},
		{name: "traces endpoint", values: map[string]string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "collector"}, want: true},
		{name: "empty endpoints", values: map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": ""}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := otlpTracesConfigured(func(key string) string { return test.values[key] })
			if got != test.want {
				t.Errorf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestSetupProductionPath(t *testing.T) {
	for _, key := range []string{
		"LOG_FORMAT",
		"OTEL_SERVICE_NAME",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_RESOURCE_ATTRIBUTES",
	} {
		t.Setenv(key, "")
	}
	previousLogger := slog.Default()
	previousMeter := otel.GetMeterProvider()
	previousPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		slog.SetDefault(previousLogger)
		otel.SetMeterProvider(previousMeter)
		otel.SetTextMapPropagator(previousPropagator)
	})

	o, logger, err := Setup(context.Background(), Config{
		ServiceName:    "production-path-test",
		ServiceVersion: "0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o == nil || logger == nil || o.registry == nil || o.meter == nil {
		t.Fatal("Setup returned incomplete observability state")
	}
	if o.tracer != nil {
		t.Error("Setup created a tracer without an OTLP endpoint")
	}
	if slog.Default() != logger || otel.GetMeterProvider() != o.meter {
		t.Error("Setup did not install its logger and meter provider globally")
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
