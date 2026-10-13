// Command memode runs the MeMode unified personal-assistant MCP server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/oauthstore"
	"codebase.bid/projects/memode/internal/server"
	"codebase.bid/projects/memode/internal/services"
	"codebase.bid/projects/memode/internal/store"
	"codebase.bid/projects/memode/internal/version"
)

func main() {
	slog.SetLogLoggerLevel(slog.LevelInfo)
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid config", "err", err)
		os.Exit(1)
	}
	db, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		slog.Error("store", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	reg := &codemode.Registry{}
	ostore := oauthstore.New(db)
	if err := ostore.EnsureClients(ctx, toOAuthClients(cfg.Clients)); err != nil {
		slog.Error("ensure clients", "err", err)
		os.Exit(1)
	}
	srv := server.New(cfg, reg, ostore)
	services.SetupAll(ctx, services.Enabled(cfg, db), reg, srv.Health.SetReady)

	addr := cfg.ListenAddr
	httpSrv := &http.Server{Addr: addr, Handler: srv.HTTPHandler()}
	go func() {
		slog.Info("memode listening", "version", version.Version, "addr", addr, "base_url", cfg.BaseURL)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	_ = httpSrv.Shutdown(ctx)
}

func toOAuthClients(cs []config.Client) []oauthstore.ClientConfig {
	out := make([]oauthstore.ClientConfig, len(cs))
	for i, c := range cs {
		out[i] = oauthstore.ClientConfig{ClientID: c.ClientID, Secret: c.Secret, Name: c.Name, RedirectURIs: c.RedirectURIs}
	}
	return out
}
