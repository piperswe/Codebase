package cache

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrate(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// Each connection to :memory: gets its own database, so keep the pool
	// at a single connection.
	conn.SetMaxOpenConns(1)
	err = Migrate(conn)
	if err != nil {
		t.Fatal(err)
	}
}
