package o11y

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httplog/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestHTTPMiddlewareLogsRequest(t *testing.T) {
	var output bytes.Buffer
	logger, err := newLogger("test-service", "1.2.3", "json", &output, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := &O11y{logger: logger, logSchema: httplog.SchemaOTEL}
	handler := o.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}))

	request := httptest.NewRequest(http.MethodPost, "http://example.test/widgets/42", strings.NewReader("payload"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var got map[string]any
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("decode request log %q: %v", output.String(), err)
	}
	want := map[string]any{
		"severity_text":             "INFO",
		"app":                       "test-service",
		"version":                   "1.2.3",
		"http.request.method":       http.MethodPost,
		"url.path":                  "/widgets/42",
		"server.address":            "example.test",
		"http.request.body.size":    float64(len("payload")),
		"http.response.status_code": float64(http.StatusCreated),
		"http.response.body.size":   float64(len("created")),
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("request log %q=%v, want %v", key, got[key], value)
		}
	}
	if _, ok := got["http.server.request.duration"]; !ok {
		t.Error("request log is missing http.server.request.duration")
	}
}

func TestHTTPMiddlewareRouterPatterns(t *testing.T) {
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })

	tests := []struct {
		name string
		mux  func(http.Handler) http.Handler
	}{
		{
			name: "ServeMux",
			mux: func(handler http.Handler) http.Handler {
				mux := http.NewServeMux()
				mux.Handle("POST /widgets/{id}", handler)
				return mux
			},
		},
		{
			name: "Chi",
			mux: func(handler http.Handler) http.Handler {
				mux := chi.NewRouter()
				mux.Method(http.MethodPost, "/widgets/{id}", handler)
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					routeContext := chi.NewRouteContext()
					if mux.Match(routeContext, r.Method, r.URL.Path) {
						r.Pattern = routeContext.RoutePattern()
					}
					mux.ServeHTTP(w, r)
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := metric.NewManualReader()
			meterProvider := metric.NewMeterProvider(metric.WithReader(reader))
			spanRecorder := tracetest.NewSpanRecorder()
			tracerProvider := trace.NewTracerProvider(trace.WithSpanProcessor(spanRecorder))
			t.Cleanup(func() {
				_ = tracerProvider.Shutdown(context.Background())
				_ = meterProvider.Shutdown(context.Background())
			})

			var pattern string
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pattern = r.Pattern
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("created"))
			})
			o := &O11y{meter: meterProvider, tracer: tracerProvider, serviceName: "test-service"}
			instrumented := o.HTTPMiddleware(test.mux(handler))

			request := httptest.NewRequest(http.MethodPost, "http://example.test/widgets/42", strings.NewReader("payload"))
			request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
			response := httptest.NewRecorder()
			instrumented.ServeHTTP(response, request)

			if response.Code != http.StatusCreated {
				t.Fatalf("status=%d, want %d", response.Code, http.StatusCreated)
			}
			if httpRoute(pattern) != "/widgets/{id}" {
				t.Errorf("request route=%q, want %q", httpRoute(pattern), "/widgets/{id}")
			}

			spans := spanRecorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("recorded %d spans, want 1", len(spans))
			}
			span := spans[0]
			if span.Name() != "POST /widgets/{id}" {
				t.Errorf("span name=%q, want %q", span.Name(), "POST /widgets/{id}")
			}
			if span.SpanKind() != oteltrace.SpanKindServer {
				t.Errorf("span kind=%v, want %v", span.SpanKind(), oteltrace.SpanKindServer)
			}
			if got := span.Parent().SpanID().String(); got != "00f067aa0ba902b7" {
				t.Errorf("parent span ID=%q, want propagated parent", got)
			}
			spanAttrs := attribute.NewSet(span.Attributes()...)
			assertStringAttribute(t, spanAttrs, "http.route", "/widgets/{id}")

			var resourceMetrics metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &resourceMetrics); err != nil {
				t.Fatal(err)
			}
			assertHTTPMetrics(t, resourceMetrics)
		})
	}
}

func assertHTTPMetrics(t *testing.T, resourceMetrics metricdata.ResourceMetrics) {
	t.Helper()
	want := map[string]bool{
		"http.server.request.duration":   false,
		"http.server.request.body.size":  false,
		"http.server.response.body.size": false,
	}
	for _, scope := range resourceMetrics.ScopeMetrics {
		for _, m := range scope.Metrics {
			if _, ok := want[m.Name]; !ok {
				continue
			}
			want[m.Name] = true
			var attrs attribute.Set
			var count uint64
			switch data := m.Data.(type) {
			case metricdata.Histogram[int64]:
				if len(data.DataPoints) != 1 {
					t.Fatalf("%s has %d data points, want 1", m.Name, len(data.DataPoints))
				}
				attrs, count = data.DataPoints[0].Attributes, data.DataPoints[0].Count
			case metricdata.Histogram[float64]:
				if len(data.DataPoints) != 1 {
					t.Fatalf("%s has %d data points, want 1", m.Name, len(data.DataPoints))
				}
				attrs, count = data.DataPoints[0].Attributes, data.DataPoints[0].Count
			default:
				t.Fatalf("%s has aggregation type %T, want histogram", m.Name, m.Data)
			}
			if count != 1 {
				t.Errorf("%s count=%d, want 1", m.Name, count)
			}
			assertStringAttribute(t, attrs, "http.request.method", http.MethodPost)
			assertIntAttribute(t, attrs, "http.response.status_code", http.StatusCreated)
			assertStringAttribute(t, attrs, "server.address", "example.test")
			assertStringAttribute(t, attrs, "http.route", "/widgets/{id}")
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("metric %q was not recorded", name)
		}
	}
}

func assertStringAttribute(t *testing.T, attrs attribute.Set, key, want string) {
	t.Helper()
	value, ok := attrs.Value(attribute.Key(key))
	if !ok {
		t.Errorf("attribute %q is missing", key)
		return
	}
	if got := value.AsString(); got != want {
		t.Errorf("attribute %s=%q, want %q", key, got, want)
	}
}

func assertIntAttribute(t *testing.T, attrs attribute.Set, key string, want int64) {
	t.Helper()
	value, ok := attrs.Value(attribute.Key(key))
	if !ok {
		t.Errorf("attribute %q is missing", key)
		return
	}
	if got := value.AsInt64(); got != want {
		t.Errorf("attribute %s=%d, want %d", key, got, want)
	}
}
