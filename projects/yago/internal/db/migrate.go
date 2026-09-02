package db

import (
	"context"
	"embed"

	"github.com/jackc/pgx/v5"
	"github.com/piperswe/Codebase/lib/go/dbmigrate"
)

//go:embed migrations
var migrationsFS embed.FS

type Migrator struct{}

func (m *Migrator) Migrate(ctx context.Context, conn *pgx.Conn) error {
	return dbmigrate.UpPostgres(conn, migrationsFS, "migrations", "main")
}

func Migrate(conn *pgx.Conn) error {
	return (&Migrator{}).Migrate(context.Background(), conn)
}
