package o11y

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestShutdownStopsTracerThenMeterAndJoinsErrors(t *testing.T) {
	tracerErr := errors.New("tracer failed")
	meterErr := errors.New("meter failed")
	var order []string
	tracer := newFakeTracerProvider()
	tracer.shutdownErr = tracerErr
	tracer.shutdownOrder = &order
	meter := newFakeMeterProvider()
	meter.shutdownErr = meterErr
	meter.shutdownOrder = &order

	err := (&O11y{tracer: tracer, meter: meter}).Shutdown(context.Background())
	if !errors.Is(err, tracerErr) || !errors.Is(err, meterErr) {
		t.Errorf("joined error %v does not contain both provider errors", err)
	}
	if !strings.Contains(err.Error(), "shutdown tracer provider") ||
		!strings.Contains(err.Error(), "shutdown meter provider") {
		t.Errorf("joined error lacks context: %v", err)
	}
	if strings.Join(order, ",") != "tracer,meter" {
		t.Errorf("shutdown order=%v, want [tracer meter]", order)
	}
	if tracer.shutdownCalls != 1 || meter.shutdownCalls != 1 {
		t.Errorf("shutdown calls: tracer=%d meter=%d, want 1 each", tracer.shutdownCalls, meter.shutdownCalls)
	}
}

func TestShutdownHandlesMissingProvidersAndNilReceiver(t *testing.T) {
	var nilO11y *O11y
	if err := nilO11y.Shutdown(context.Background()); err != nil {
		t.Errorf("nil receiver: got %v, want nil", err)
	}
	if err := (&O11y{}).Shutdown(context.Background()); err != nil {
		t.Errorf("missing providers: got %v, want nil", err)
	}
}

type recordingTracerProvider struct {
	trace.TracerProvider
	scope string
}

func (p *recordingTracerProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	p.scope = name
	return p.TracerProvider.Tracer(name, options...)
}

func TestTracerUsesGlobalProvider(t *testing.T) {
	previous := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	provider := &recordingTracerProvider{TracerProvider: tracenoop.NewTracerProvider()}
	otel.SetTracerProvider(provider)

	tracer := (&O11y{}).Tracer("github.com/example/scope")
	if tracer == nil {
		t.Fatal("Tracer returned nil")
	}
	if provider.scope != "github.com/example/scope" {
		t.Errorf("scope=%q, want github.com/example/scope", provider.scope)
	}
}
