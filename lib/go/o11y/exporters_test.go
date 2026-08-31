package o11y

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/sdk/resource"
)

func TestNewResourceContainsServiceAndEnvironmentAttributes(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=wrong,custom.attribute=present")

	res, err := newResource(context.Background(), "catalog", "2.4.6")
	if err != nil {
		t.Fatal(err)
	}
	assertResourceAttribute(t, res, "service.name", "catalog")
	assertResourceAttribute(t, res, "service.version", "2.4.6")
	assertResourceAttribute(t, res, "custom.attribute", "present")
	assertResourceAttribute(t, res, "telemetry.sdk.language", "go")
}

func TestNewMeterProviderExportsMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	provider, err := newMeterProvider(registry, resource.Empty())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	counter, err := provider.Meter("o11y-test").Int64Counter("o11y.constructor.counter")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 7)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	var names []string
	for _, family := range families {
		names = append(names, family.GetName())
		if family.GetName() == "o11y_constructor_counter_total" {
			found = true
		}
	}
	if !found {
		t.Errorf("exported metric not found in %v", names)
	}
}

func TestNewTracerProviderBuildsAndShutsDown(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:4318")
	provider, err := newTracerProvider(context.Background(), resource.Empty())
	if err != nil {
		t.Fatal(err)
	}
	if provider == nil {
		t.Fatal("newTracerProvider returned nil")
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
