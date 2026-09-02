package cache

import (
	"database/sql"
	"embed"

	"codebase.bid/lib/go/dbmigrate"
)

//go:embed migrations
var migrationsFS embed.FS

// Migrate applies all pending schema migrations to the cache database.
func Migrate(conn *sql.DB) error {
	return dbmigrate.Up(conn, migrationsFS, "migrations", "cache")
}
