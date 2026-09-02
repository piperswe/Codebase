package testdb

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	_ "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

type Migrator interface {
	Migrate(ctx context.Context, db *pgx.Conn) error
}

type TestDatabase interface {
	GetConn() *pgx.Conn
}

type testDatabase struct {
	container testcontainers.Container
	conn      *pgx.Conn
}

func (td *testDatabase) GetConn() *pgx.Conn {
	return td.conn
}

func New(t *testing.T, migrator Migrator) TestDatabase {
	t.Helper()
	ctx := t.Context()
	container, err := postgres.Run(ctx,
		"docker.io/library/postgres:18.6@sha256:4ef4dbc939d61acea57712655ddb4b4ab27419c913f94cca0cd57cb3ea3c2280",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testdb"),
		postgres.WithPassword("testdb"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(5*time.Second)),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrator.Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return &testDatabase{
		container: container,
		conn:      conn,
	}
}
