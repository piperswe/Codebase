package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"codebase.bid/lib/go/cache"
	"codebase.bid/lib/go/notify"
	"codebase.bid/lib/go/o11y"
	"codebase.bid/projects/datasite/internal/db"
	"codebase.bid/projects/datasite/internal/metrics"
	"codebase.bid/projects/datasite/internal/moviedb"
	"codebase.bid/projects/datasite/internal/version"
	"github.com/XSAM/otelsql"
	tmdb "github.com/cyruzin/golang-tmdb"
	"github.com/joho/godotenv"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type universe struct {
	logger      *slog.Logger
	dbConn      *sql.DB
	db          *db.Queries
	cacheConn   *sql.DB
	cache       *cache.Queries
	tmdbClient  *tmdb.Client
	movies      moviedb.MovieDB
	serverSrc   string
	adminAPIKey string
}

func getenvOr(key string, def string) string {
	v, ok := os.LookupEnv(key)
	if ok {
		return v
	} else {
		return def
	}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "smoke" {
		fmt.Printf("datasite %s", version.Version)
		return
	}
	// godotenv must load before o11y.Setup so LOG_FORMAT and OTEL_* env vars
	// from .env are visible to the observability setup. Buffer the warning and
	// emit it once the logger exists.
	envErr := godotenv.Load()

	rootCtx := context.Background()
	ob, logger, err := o11y.Setup(rootCtx, o11y.Config{
		ServiceName:    "datasite",
		ServiceVersion: version.Version,
	})
	if logger == nil {
		// Logging failed to initialize entirely; fall back so we can report it.
		logger = slog.Default()
	}
	if err != nil {
		// Log and continue: a bad OTLP endpoint or exporter must not take the
		// app down. Metrics still work as long as the meter provider came up.
		logger.Error("failed to fully initialize observability", slog.Any("err", err))
	}
	if envErr != nil {
		logger.Warn("failed to load .env file", slog.Any("err", envErr))
	}

	if ob == nil {
		logger.Error("observability failed to initialize; cannot continue")
		os.Exit(1)
	}
	appTracer := ob.Tracer(metrics.ScopeName)

	instruments, err := metrics.NewInstruments()
	if err != nil {
		logger.Error("failed to create metric instruments", slog.Any("err", err))
		os.Exit(1)
	}

	dbConn, err := openSQLite(getenvOr("DB_PATH", "./datasite-db.db"), "main")
	if err != nil {
		logger.Error("failed to open database", slog.Any("err", err))
		os.Exit(1)
	}
	_, err = dbConn.Exec("PRAGMA foreign_keys = ON;")
	if err != nil {
		logger.Error("failed to enable foreign keys", slog.Any("err", err))
		os.Exit(1)
	}
	err = db.Migrate(dbConn)
	if err != nil {
		logger.Error("failed to migrate database", slog.Any("err", err))
		os.Exit(1)
	}
	dbQueries := db.New(dbConn)
	cacheConn, err := openSQLite(getenvOr("CACHE_PATH", "./datasite-cache.db"), "cache")
	if err != nil {
		logger.Error("failed to open cache", slog.Any("err", err))
		os.Exit(1)
	}
	err = cache.Migrate(cacheConn)
	if err != nil {
		logger.Error("failed to migrate cache", slog.Any("err", err))
		os.Exit(1)
	}
	cacheQueries := cache.New(cacheConn)
	tmdbClient, err := tmdb.Init(os.Getenv("TMDB_API_KEY"))
	if err != nil {
		logger.Error("failed to create tmdb client", slog.Any("err", err))
		os.Exit(1)
	}
	tmdbClient.SetClientAutoRetry()
	movies := moviedb.NewCachedMovieDB(tmdbClient, tmdbClient, cacheQueries, instruments, appTracer)
	portStr := getenvOr("PORT", "3084")
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		logger.Error("failed to parse port", slog.Any("err", err))
		os.Exit(1)
	}
	serverSrc := getenvOr("SERVER_SRC", "https://codebase.bid/projects/datasite")
	adminAPIKey := getenvOr("ADMIN_API_KEY", "1234")
	u := universe{
		logger,
		dbConn,
		dbQueries,
		cacheConn,
		cacheQueries,
		tmdbClient,
		movies,
		serverSrc,
		adminAPIKey,
	}
	logger.Info("Config loaded")

	notify.Ready()

	go func(c *cache.Queries, logger *slog.Logger, inst *metrics.Instruments, tracer trace.Tracer) {
		t := time.NewTicker(time.Hour)
		for range t.C {
			runCacheCleanup(c, logger, inst, tracer)
		}
	}(u.cache, logger, instruments, appTracer)

	r := SetupMux(&u)

	// Public application server.
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: ob.HTTPMiddleware(withChiRoutePattern(r))}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := ob.ServeAdmin(ctx, logger); err != nil {
			logger.Error("admin server returned", slog.Any("err", err))
		}
	}()
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server returned", slog.Any("err", err))
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	notify.Stopping()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to gracefully shut down http server", slog.Any("err", err))
	}
	if err := ob.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shut down observability", slog.Any("err", err))
	}
}

// runCacheCleanup deletes expired cache rows within a fresh root span and
// records the outcome to metrics.
func runCacheCleanup(c *cache.Queries, logger *slog.Logger, inst *metrics.Instruments, tracer trace.Tracer) {
	ctx, span := tracer.Start(context.Background(), "cache.cleanup")
	defer span.End()

	err := c.DeleteExpired(ctx)
	status := "ok"
	if err != nil {
		status = "error"
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		logger.Error("failed to delete expired cache items", slog.Any("err", err))
	}
	if inst != nil {
		inst.CacheCleanupRuns.Add(ctx, 1, metricStatus(status))
	}
}

// openSQLite opens an OTel-instrumented SQLite connection and registers its
// database/sql pool stats as metrics. name distinguishes the pool (main|cache).
func openSQLite(dsn, name string) (*sql.DB, error) {
	conn, err := otelsql.Open("sqlite", dsn,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", name),
		),
	)
	if err != nil {
		return nil, err
	}
	if _, err := otelsql.RegisterDBStatsMetrics(conn,
		otelsql.WithAttributes(
			semconv.DBSystemSqlite,
			attribute.String("db.name", name),
		),
	); err != nil {
		return nil, err
	}
	return conn, nil
}

// metricStatus builds a status=ok|error measurement option.
func metricStatus(status string) metric.MeasurementOption {
	return metric.WithAttributes(metrics.StatusKey.String(status))
}
