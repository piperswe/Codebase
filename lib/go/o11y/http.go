package o11y

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/httplog/v3"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// HTTPMiddleware instruments an HTTP handler with slog request logs,
// OpenTelemetry server spans, and standard HTTP server metrics. Routers that
// set Request.Pattern attach their low-cardinality route template to the
// telemetry after routing.
func (o *O11y) HTTPMiddleware(next http.Handler) http.Handler {
	var options []otelhttp.Option
	operation := "http.server"
	logger := slog.Default()
	logSchema := httplog.SchemaOTEL
	if o != nil {
		if o.meter != nil {
			options = append(options, otelhttp.WithMeterProvider(o.meter))
		}
		if o.tracer != nil {
			options = append(options, otelhttp.WithTracerProvider(o.tracer))
		}
		if o.serviceName != "" {
			operation = o.serviceName
		}
		if o.logger != nil {
			logger = o.logger
		}
		if o.logSchema != nil {
			logSchema = o.logSchema
		}
	}
	routeAware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if route := httpRoute(r.Pattern); route != "" {
				attr := semconv.HTTPRoute(route)
				span := trace.SpanFromContext(r.Context())
				span.SetName(r.Method + " " + route)
				span.SetAttributes(attr)
				if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
					labeler.Add(attr)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
	requestLogger := httplog.RequestLogger(logger, &httplog.Options{
		Level:         slog.LevelInfo,
		Schema:        logSchema,
		RecoverPanics: true,
	})
	return otelhttp.NewHandler(requestLogger(routeAware), operation, options...)
}

func httpRoute(pattern string) string {
	if index := strings.IndexByte(pattern, '/'); index >= 0 {
		return pattern[index:]
	}
	return ""
}
