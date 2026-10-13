// Package store opens and migrates the memode SQLite database, shared by the
// mail index, the OAuth authorization server and the Telegram session.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // driver for "sqlite" Data Source Names

	"codebase.bid/lib/go/dbmigrate"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens the SQLite database at path, applies pending migrations and
// returns the handle. Callers must Close it.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory %s: %w", dir, err)
		}
	}
	dsn := url.Values{
		"_pragma": {"busy_timeout(10000)", "journal_mode(WAL)"},
	}.Encode()
	db, err := sql.Open("sqlite", "file:"+path+"?"+dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // SQLite: one connection serializes writes and avoids SQLITE_BUSY
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	if err := dbmigrate.Up(db, migrations, "migrations", path); err != nil {
		db.Close()
		return nil, err
	}
	slog.Info("database ready", "path", path)
	return db, nil
}
