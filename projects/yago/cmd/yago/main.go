package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codebase.bid/lib/go/notify"
	"codebase.bid/lib/go/o11y"
	"codebase.bid/projects/yago/internal/db"
	"codebase.bid/projects/yago/internal/version"
	"github.com/go-chi/httplog/v3"
	"github.com/joho/godotenv"
	"github.com/valkey-io/valkey-go"
)

type universe struct {
	o11y      *o11y.O11y
	logger    *slog.Logger
	logFormat *httplog.Schema
	queries   *db.Queries
	vk        valkey.Client
}

func (u *universe) O11y() *o11y.O11y {
	return u.o11y
}

func (u *universe) Logger() *slog.Logger {
	return u.logger
}

func (u *universe) LogFormat() *httplog.Schema {
	return u.logFormat
}

func (u *universe) Queries() *db.Queries {
	return u.queries
}

func (u *universe) Valkey() valkey.Client {
	return u.vk
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "smoke" {
		fmt.Printf("yago %s", version.Version)
		return
	}
	rootCtx := context.Background()
	ctx := rootCtx
	envErr := godotenv.Load()
	ob, logger, err := o11y.Setup(ctx, o11y.Config{
		ServiceName:    "yago",
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
	logFormat := ob.LogSchema()
	// appTracer := ob.Tracer(metrics.ScopeName)
	q, err := db.NewFromEnvironment(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer q.Close(context.Background())
	err = q.Migrate(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to migrate database: %v\n", err)
		os.Exit(1)
	}
	valkeyAddr, ok := os.LookupEnv("VALKEY_ADDR")
	if !ok {
		logger.Warn("VALKEY_ADDR environment variable is not set, defaulting to localhost:6379")
		valkeyAddr = "localhost:6379"
	}
	vk, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{valkeyAddr}})
	if err != nil {
		logger.Error("failed to initialize Valkey client", slog.Any("err", err))
		os.Exit(1)
	}
	u := universe{
		ob,
		logger,
		logFormat,
		q,
		vk,
	}
	router := NewRouter(&u)
	notify.Ready()
	logger.Info("Hello, world!")
	userCount, err := q.GetUserCount(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get user count: %v\n", err)
		os.Exit(1)
	}
	logger.Info("User count", slog.Int64("userCount", userCount))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	exitCode := 0
	go func() {
		if err := ob.ServeAdmin(ctx, logger); err != nil {
			logger.Error("admin server returned", slog.Any("err", err))
			exitCode = 1
			stop()
		}
	}()
	go func() {
		if err := http.ListenAndServe(":3000", router); err != nil {
			logger.Error("http server returned", slog.Any("err", err))
			exitCode = 1
			stop()
		}
	}()
	<-ctx.Done()
	logger.Info("shutting down")
	notify.Stopping()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// if err := srv.Shutdown(shutdownCtx); err != nil {
	// 	logger.Error("failed to gracefully shut down http server", slog.Any("err", err))
	// }
	if err := ob.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shut down observability", slog.Any("err", err))
		exitCode = 1
	}
	os.Exit(exitCode)
}
