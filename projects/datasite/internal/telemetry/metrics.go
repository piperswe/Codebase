package telemetry

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// ScopeName is the instrumentation scope used for datasite's own spans and
// metrics. It matches the module path by convention.
const ScopeName = "codeberg.org/pmc/Codebase/projects/datasite"

// Tracer returns the datasite application tracer. It resolves against whatever
// global TracerProvider is registered (the OTLP one, or the no-op default).
func Tracer() trace.Tracer {
	return otel.Tracer(ScopeName)
}

// Instruments holds datasite's custom application metrics. Build once at
// startup with NewInstruments and pass where needed.
type Instruments struct {
	// CacheLookups counts moviedb cache lookups, tagged result=hit|miss.
	CacheLookups metric.Int64Counter
	// TMDBRequestDuration records the latency of outbound TMDB calls in
	// milliseconds, tagged operation and status.
	TMDBRequestDuration metric.Float64Histogram
	// CacheCleanupRuns counts background cache-cleanup executions, tagged
	// status=ok|error.
	CacheCleanupRuns metric.Int64Counter
}

// NewInstruments creates datasite's custom metric instruments against the
// global MeterProvider.
func NewInstruments() (*Instruments, error) {
	m := otel.Meter(ScopeName)

	cacheLookups, err := m.Int64Counter(
		"moviedb.cache.lookups",
		metric.WithDescription("Number of moviedb cache lookups by result."),
	)
	if err != nil {
		return nil, err
	}
	tmdbDuration, err := m.Float64Histogram(
		"moviedb.tmdb.request.duration",
		metric.WithDescription("Duration of outbound TMDB requests."),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}
	cleanupRuns, err := m.Int64Counter(
		"cache.cleanup.runs",
		metric.WithDescription("Number of background cache-cleanup runs by status."),
	)
	if err != nil {
		return nil, err
	}

	return &Instruments{
		CacheLookups:        cacheLookups,
		TMDBRequestDuration: tmdbDuration,
		CacheCleanupRuns:    cleanupRuns,
	}, nil
}

// Attribute-key helpers shared across instrumentation call sites.
var (
	// ResultKey tags cache lookups (hit|miss).
	ResultKey = attribute.Key("result")
	// OperationKey tags TMDB requests (get_movie|search).
	OperationKey = attribute.Key("operation")
	// StatusKey tags an operation outcome (ok|error).
	StatusKey = attribute.Key("status")
)
