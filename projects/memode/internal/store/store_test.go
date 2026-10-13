package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndFTS5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// FTS5 must be compiled into modernc.org/sqlite for the mail index.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE t USING fts5(body)`); err != nil {
		t.Fatalf("fts5 unavailable: %v", err)
	}

	// Reopening applies no further migrations and succeeds.
	db2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM settings`).Scan(&n); err != nil {
		t.Fatalf("settings table: %v", err)
	}
}
