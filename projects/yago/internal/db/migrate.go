package db

import (
	"embed"

	"github.com/piperswe/Codebase/lib/go/dbmigrate"
)

//go:embed migrations
var migrationsFS embed.FS

var Migrator = dbmigrate.PostgresMigrator{
	FS:     migrationsFS,
	Path:   "migrations",
	DBName: "main",
}
