// Package dbmigrate runs embedded golang-migrate migrations against an open
// SQLite database.
package dbmigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type slogLogger struct{}

func (*slogLogger) Printf(format string, v ...interface{}) {
	slog.Info(fmt.Sprintf(format, v...))
}

func (*slogLogger) Verbose() bool {
	return true
}

// Up applies all pending migrations found at path within fsys to conn. It
// does not close conn; callers keep using it afterwards.
func Up(conn *sql.DB, fsys fs.FS, path string, dbName string) error {
	src, err := iofs.New(fsys, path)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	drv, err := sqlite.WithInstance(conn, &sqlite.Config{DatabaseName: dbName})
	if err != nil {
		return fmt.Errorf("init migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, dbName, drv)
	if err != nil {
		return fmt.Errorf("init migrations: %w", err)
	}
	m.Log = &slogLogger{}
	// Deliberately no m.Close(): the sqlite driver's Close() closes the
	// underlying *sql.DB, which the application continues to use.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return src.Close()
}

type PostgresMigrator struct {
	FS     fs.FS
	Path   string
	DBName string
}

func (pm *PostgresMigrator) Migrate(ctx context.Context, conn *pgx.Conn) error {
	src, err := iofs.New(pm.FS, pm.Path)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	db := stdlib.OpenDB(*conn.Config())
	drv, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{DatabaseName: pm.DBName})
	if err != nil {
		return fmt.Errorf("init migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, pm.DBName, drv)
	if err != nil {
		return fmt.Errorf("init migrations: %w", err)
	}
	m.Log = &slogLogger{}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return src.Close()
}
