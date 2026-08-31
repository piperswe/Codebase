package o11y

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type fakeMeterProvider struct {
	metric.MeterProvider
	shutdownErr   error
	shutdownCalls int
	shutdownOrder *[]string
}

func newFakeMeterProvider() *fakeMeterProvider {
	return &fakeMeterProvider{MeterProvider: metricnoop.NewMeterProvider()}
}

func (p *fakeMeterProvider) Shutdown(context.Context) error {
	p.shutdownCalls++
	if p.shutdownOrder != nil {
		*p.shutdownOrder = append(*p.shutdownOrder, "meter")
	}
	return p.shutdownErr
}

type fakeTracerProvider struct {
	trace.TracerProvider
	shutdownErr   error
	shutdownCalls int
	shutdownOrder *[]string
}

func newFakeTracerProvider() *fakeTracerProvider {
	return &fakeTracerProvider{TracerProvider: tracenoop.NewTracerProvider()}
}

func (p *fakeTracerProvider) Shutdown(context.Context) error {
	p.shutdownCalls++
	if p.shutdownOrder != nil {
		*p.shutdownOrder = append(*p.shutdownOrder, "tracer")
	}
	return p.shutdownErr
}

func successfulSetupDependencies() (setupDependencies, *fakeMeterProvider, *fakeTracerProvider) {
	meter := newFakeMeterProvider()
	tracer := newFakeTracerProvider()
	return setupDependencies{
		getenv: func(string) string { return "" },
		setupLogging: func(_, _ string) (*slog.Logger, error) {
			return slog.New(slog.DiscardHandler), nil
		},
		newResource: func(context.Context, string, string) (*resource.Resource, error) {
			return resource.Empty(), nil
		},
		newMeterProvider: func(prometheus.Registerer, *resource.Resource) (meterProvider, error) {
			return meter, nil
		},
		newTracerProvider: func(context.Context, *resource.Resource) (tracerProvider, error) {
			return tracer, nil
		},
		setDefaultLogger:  func(*slog.Logger) {},
		setPropagator:     func(propagation.TextMapPropagator) {},
		setMeterProvider:  func(metric.MeterProvider) {},
		setTracerProvider: func(trace.TracerProvider) {},
	}, meter, tracer
}

func assertWrappedError(t *testing.T, got, want error, message string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Errorf("error %v does not wrap %v", got, want)
	}
	if got == nil || !strings.Contains(got.Error(), message) {
		t.Errorf("error %v does not contain %q", got, message)
	}
}

func assertResourceAttribute(t *testing.T, res *resource.Resource, key, want string) {
	t.Helper()
	got, ok := res.Set().Value(attribute.Key(key))
	if !ok {
		t.Errorf("resource is missing %q", key)
		return
	}
	if got.AsString() != want {
		t.Errorf("resource %q=%q, want %q", key, got.AsString(), want)
	}
}
