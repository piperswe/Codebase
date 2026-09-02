package moviedb

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"codebase.bid/lib/go/cache"
	"codebase.bid/projects/datasite/internal/metrics"
	tmdb "github.com/cyruzin/golang-tmdb"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// metricAttrs wraps attributes as a measurement option usable by both counters
// and histograms.
func metricAttrs(attrs ...attribute.KeyValue) metric.MeasurementOption {
	return metric.WithAttributes(attrs...)
}

type MovieDB interface {
	GetMovieByID(ctx context.Context, id int) (*tmdb.MovieDetails, error)
	SearchMovie(ctx context.Context, title string, year int) (int64, string, error)
}

// MovieDetailsGetter is the slice of *tmdb.Client that CachedMovieDB uses.
type MovieDetailsGetter interface {
	GetMovieDetails(id int, urlOptions map[string]string) (*tmdb.MovieDetails, error)
}

// MovieSearcher is the slice of *tmdb.Client that CachedMovieDB uses.
type MovieSearcher interface {
	GetSearchMovies(query string, urlOptions map[string]string) (*tmdb.SearchMovies, error)
}

type CachedMovieDB struct {
	tmdb        MovieDetailsGetter
	searcher    MovieSearcher
	cache       *cache.Queries
	instruments *metrics.Instruments
	tracer      trace.Tracer
}

func NewCachedMovieDB(tmdb MovieDetailsGetter, searcher MovieSearcher, cache *cache.Queries, instruments *metrics.Instruments, tracer trace.Tracer) *CachedMovieDB {
	return &CachedMovieDB{
		tmdb,
		searcher,
		cache,
		instruments,
		tracer,
	}
}

func (c *CachedMovieDB) GetMovieByID(ctx context.Context, id int) (*tmdb.MovieDetails, error) {
	ctx, span := c.tracer.Start(ctx, "moviedb.GetMovieByID",
		trace.WithAttributes(attribute.Int("movie.id", id)),
	)
	defer span.End()

	cacheKey := strconv.Itoa(id)
	fromCache, err := cache.Lookup[tmdb.MovieDetails](ctx, c.cache, cache.PARTITION_MOVIE_DETAILS, cacheKey)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	if fromCache != nil {
		c.recordCacheLookup(ctx, "hit")
		span.SetAttributes(attribute.Bool("cache.hit", true))
		return fromCache, err
	}
	c.recordCacheLookup(ctx, "miss")
	span.SetAttributes(attribute.Bool("cache.hit", false))

	details, err := c.getMovieDetails(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	err = cache.Write(ctx, c.cache, cache.PARTITION_MOVIE_DETAILS, cacheKey, details, time.Now().Add(time.Hour*72))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	return details, nil
}

func (c *CachedMovieDB) SearchMovie(ctx context.Context, title string, year int) (int64, string, error) {
	ctx, span := c.tracer.Start(ctx, "moviedb.SearchMovie",
		trace.WithAttributes(
			attribute.String("movie.title", title),
			attribute.Int("movie.year", year),
		),
	)
	defer span.End()

	results, err := c.searchMovies(ctx, title)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return 0, "", err
	}
	yearStr := strconv.Itoa(year)
	for _, r := range results.Results {
		if len(r.ReleaseDate) >= 4 && r.ReleaseDate[:4] == yearStr {
			return r.ID, r.Title, nil
		}
	}
	if len(results.Results) > 0 {
		return results.Results[0].ID, results.Results[0].Title, nil
	}
	err = fmt.Errorf("movie not found: %s (%d)", title, year)
	span.SetStatus(codes.Error, err.Error())
	return 0, "", err
}

// getMovieDetails wraps the context-less TMDB library call in a child span and
// records its latency into the TMDB duration histogram.
func (c *CachedMovieDB) getMovieDetails(ctx context.Context, id int) (*tmdb.MovieDetails, error) {
	ctx, span := c.tracer.Start(ctx, "tmdb.GetMovieDetails")
	defer span.End()

	start := time.Now()
	details, err := c.tmdb.GetMovieDetails(id, nil)
	c.recordTMDBDuration(ctx, "get_movie", err, start)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return details, err
}

// searchMovies wraps the context-less TMDB search call in a child span.
func (c *CachedMovieDB) searchMovies(ctx context.Context, title string) (*tmdb.SearchMovies, error) {
	ctx, span := c.tracer.Start(ctx, "tmdb.GetSearchMovies")
	defer span.End()

	start := time.Now()
	results, err := c.searcher.GetSearchMovies(title, nil)
	c.recordTMDBDuration(ctx, "search", err, start)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return results, err
}

func (c *CachedMovieDB) recordCacheLookup(ctx context.Context, result string) {
	if c.instruments == nil {
		return
	}
	c.instruments.CacheLookups.Add(ctx, 1, metricAttrs(metrics.ResultKey.String(result)))
}

func (c *CachedMovieDB) recordTMDBDuration(ctx context.Context, operation string, err error, start time.Time) {
	if c.instruments == nil {
		return
	}
	status := "ok"
	if err != nil {
		status = "error"
	}
	elapsedMs := float64(time.Since(start)) / float64(time.Millisecond)
	c.instruments.TMDBRequestDuration.Record(ctx, elapsedMs, metricAttrs(
		metrics.OperationKey.String(operation),
		metrics.StatusKey.String(status),
	))
}
