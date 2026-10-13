// Package services wires the concrete personal services into the codemode
// registry. Adding a service: write a package with New/Register/Setup, add
// one line in Enabled below.
package services

import (
	"context"
	"database/sql"
	"log/slog"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/services/calendar"
	"codebase.bid/projects/memode/internal/services/contacts"
	"codebase.bid/projects/memode/internal/services/kagi"
	"codebase.bid/projects/memode/internal/services/mail"
	"codebase.bid/projects/memode/internal/services/telegram"
)

// Service is one personal service exposed to scripts.
type Service interface {
	// Register adds the service's functions to the registry.
	Register(reg *codemode.Registry)
	// Setup connects/verifies and starts background workers; ready reports
	// readiness to the server health.
	Setup(ctx context.Context, ready func(bool))
}

// Enabled builds the services whose credentials are configured.
func Enabled(cfg config.Config, db *sql.DB) []Service {
	configured := cfg.ServiceConfigured()
	var out []Service
	if configured["mail"] {
		out = append(out, mail.New(cfg, db))
	}
	if configured["calendar"] {
		out = append(out, calendar.New(cfg))
	}
	if configured["contacts"] {
		out = append(out, contacts.New(cfg))
	}
	if configured["telegram"] {
		out = append(out, telegram.New(cfg))
	}
	if configured["search"] {
		out = append(out, kagi.New(cfg))
	}
	// Additional services are wired here as they are implemented.
	return out
}

// SetupAll registers each service's functions and starts it. ready receives
// per-service readiness updates (the server's health keeps them).
func SetupAll(ctx context.Context, svcs []Service, reg *codemode.Registry, ready func(service string, ok bool)) {
	for _, svc := range svcs {
		svc.Register(reg)
		name := nameOf(svc)
		svc.Setup(ctx, func(ok bool) {
			if name != "" && ready != nil {
				ready(name, ok)
			}
		})
	}
}

func nameOf(svc Service) string {
	switch svc.(type) {
	case *mail.Service:
		return "mail"
	case *calendar.Service:
		return "calendar"
	case *contacts.Service:
		return "contacts"
	case *telegram.Service:
		return "telegram"
	case *kagi.Service:
		return "search"
	default:
		slog.Warn("unknown service type in wiring")
		return ""
	}
}
