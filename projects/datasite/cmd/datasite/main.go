package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"codeberg.org/pmc/Codebase/projects/datasite/internal/cache"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/db"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/moviedb"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/telemetry"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/version"
	"github.com/XSAM/otelsql"
	"github.com/coreos/go-systemd/v22/daemon"
	tmdb "github.com/cyruzin/golang-tmdb"
	"github.com/go-chi/httplog/v3"
	"github.com/joho/godotenv"
	slogjournal "github.com/systemd/slog-journal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type universe struct {
	logger      *slog.Logger
	logFormat   *httplog.Schema
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
	logFormat := httplog.SchemaOTEL
	var h slog.Handler
	if version.Version == "dev" {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			ReplaceAttr: logFormat.ReplaceAttr,
		})
	} else {
		var err error
		h, err = slogjournal.NewHandler(&slogjournal.Options{
			ReplaceGroup: func(k string) string {
				return strings.ReplaceAll(strings.ToUpper(k), "-", "_")
			},
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				a = logFormat.ReplaceAttr(groups, a)
				a.Key = strings.ReplaceAll(strings.ToUpper(a.Key), "-", "_")
				a.Key = strings.ReplaceAll(a.Key, ".", "_")
				return a
			},
		})
		if err != nil {
			log.Fatalf("failed to initialize logging: %v", err)
		}
	}
	logger := slog.New(h).With(
		slog.String("app", "datasite"),
		slog.String("version", version.Version),
	)
	slog.SetDefault(logger)

	err := godotenv.Load()
	if err != nil {
		logger.Warn("failed to load .env file", slog.Any("err", err))
	}

	rootCtx := context.Background()
	tel, err := telemetry.Setup(rootCtx, "datasite", version.Version)
	if err != nil {
		// Log and continue: a bad OTLP endpoint or exporter must not take the
		// app down. Metrics still work as long as the meter provider came up.
		logger.Error("failed to fully initialize telemetry", slog.Any("err", err))
	}
	instruments, err := telemetry.NewInstruments()
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
	movies := moviedb.NewCachedMovieDB(tmdbClient, tmdbClient, cacheQueries, instruments)
	portStr := getenvOr("PORT", "3084")
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		logger.Error("failed to parse port", slog.Any("err", err))
		os.Exit(1)
	}
	serverSrc := getenvOr("SERVER_SRC", "https://codeberg.org/pmc/Codebase/projects/datasite")
	adminAPIKey := getenvOr("ADMIN_API_KEY", "1234")
	u := universe{
		logger,
		logFormat,
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

	watchdogInterval, err := daemon.SdWatchdogEnabled(false)
	if err == nil {
		go func(watchdogInterval time.Duration) {
			for {
				time.Sleep(watchdogInterval / 2)
				_, _ = daemon.SdNotify(false, daemon.SdNotifyWatchdog)
			}
		}(watchdogInterval)
	}
	_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)

	go func(c *cache.Queries, logger *slog.Logger, inst *telemetry.Instruments) {
		t := time.NewTicker(time.Hour)
		for range t.C {
			runCacheCleanup(c, logger, inst)
		}
	}(u.cache, logger, instruments)

	r := SetupMux(&u)

	// Public application server.
	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: r}
	// Dedicated admin server for Prometheus metrics + health, kept off the
	// public router.
	metricsMux := http.NewServeMux()
	if tel != nil {
		metricsMux.Handle("/metrics", tel.Handler())
	}
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	metricsSrv := &http.Server{Addr: fmt.Sprintf(":%s", getenvOr("METRICS_PORT", "9090")), Handler: metricsMux}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server returned", slog.Any("err", err))
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
	_, _ = daemon.SdNotify(false, daemon.SdNotifyStopping)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to gracefully shut down http server", slog.Any("err", err))
	}
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to gracefully shut down metrics server", slog.Any("err", err))
	}
	if err := tel.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shut down telemetry", slog.Any("err", err))
	}
}

// runCacheCleanup deletes expired cache rows within a fresh root span and
// records the outcome to metrics.
func runCacheCleanup(c *cache.Queries, logger *slog.Logger, inst *telemetry.Instruments) {
	ctx, span := telemetry.Tracer().Start(context.Background(), "cache.cleanup")
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
	return metric.WithAttributes(telemetry.StatusKey.String(status))
}
