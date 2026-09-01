package db

import (
	"database/sql"
	"embed"

	"github.com/piperswe/Codebase/lib/go/dbmigrate"
)

//go:embed migrations
var migrationsFS embed.FS

// Migrate applies all pending schema migrations to the main database.
func Migrate(conn *sql.DB) error {
	return dbmigrate.Up(conn, migrationsFS, "migrations", "main")
}
