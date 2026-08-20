package cache

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type testValue struct {
	Name  string
	Count int
}

func newTestQueries(t *testing.T) *Queries {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// Each connection to :memory: gets its own database, so keep the pool
	// at a single connection.
	conn.SetMaxOpenConns(1)
	if err := Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return New(conn)
}

func TestWriteLookupRoundTrip(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	want := testValue{Name: "dune", Count: 2}
	err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", want, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Lookup[testValue](ctx, q, PARTITION_MOVIE_DETAILS, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected cached value, got nil")
	}
	if *got != want {
		t.Errorf("got %+v, want %+v", *got, want)
	}
}

func TestLookupMissingKeyReturnsNil(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	got, err := Lookup[testValue](ctx, q, PARTITION_MOVIE_DETAILS, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing key, got %+v", *got)
	}
}

func TestLookupSkipsRecentlyExpired(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "stale"}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Lookup[testValue](ctx, q, PARTITION_MOVIE_DETAILS, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for entry expired 1m ago, got %+v", *got)
	}
}

func TestLookupSkipsLongExpired(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "stale"}, time.Now().Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Lookup[testValue](ctx, q, PARTITION_MOVIE_DETAILS, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for entry expired 72h ago, got %+v", *got)
	}
}

func TestExpiresStoredAsUnixTimestamp(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	expires := time.Now().Add(time.Hour)
	err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "v"}, expires)
	if err != nil {
		t.Fatal(err)
	}
	var storageType string
	var stored any
	row := q.db.QueryRowContext(ctx, "SELECT typeof(expires), expires FROM cache_items WHERE cache_key = 'key'")
	if err := row.Scan(&storageType, &stored); err != nil {
		t.Fatal(err)
	}
	if storageType != "integer" {
		t.Errorf("expires stored as %s, want integer", storageType)
	}
	if got, ok := stored.(int64); !ok || got != expires.Unix() {
		t.Errorf("stored expires = %v (%T), want %d", stored, stored, expires.Unix())
	}
}

func TestLookupQueryExcludesExpiredRows(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "stale"}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	items, err := q.Lookup(ctx, LookupParams{Partition: PARTITION_MOVIE_DETAILS, CacheKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("expected SQL query to exclude expired row, got %d rows", len(items))
	}
}

func TestWriteOverwritesExistingKey(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	expires := time.Now().Add(time.Hour)
	if err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "old"}, expires); err != nil {
		t.Fatal(err)
	}
	if err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "new"}, expires); err != nil {
		t.Fatal(err)
	}
	got, err := Lookup[testValue](ctx, q, PARTITION_MOVIE_DETAILS, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected cached value, got nil")
	}
	if got.Name != "new" {
		t.Errorf("got %q, want %q", got.Name, "new")
	}
}

func TestWriteRefreshesExpiry(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	if err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "v"}, time.Now().Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "key", testValue{Name: "v"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := Lookup[testValue](ctx, q, PARTITION_MOVIE_DETAILS, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Error("expected refreshed entry to be returned, got nil")
	}
}

func TestPartitionsAreIsolated(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	expires := time.Now().Add(time.Hour)
	if err := Write(ctx, q, 1, "key", testValue{Name: "one"}, expires); err != nil {
		t.Fatal(err)
	}
	if err := Write(ctx, q, 2, "key", testValue{Name: "two"}, expires); err != nil {
		t.Fatal(err)
	}
	got, err := Lookup[testValue](ctx, q, 1, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name != "one" {
		t.Errorf("partition 1: got %+v, want Name=one", got)
	}
	got, err = Lookup[testValue](ctx, q, 2, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name != "two" {
		t.Errorf("partition 2: got %+v, want Name=two", got)
	}
	got, err = Lookup[testValue](ctx, q, 3, "key")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("partition 3: expected nil, got %+v", *got)
	}
}

func TestDeleteExpiredRemovesOnlyExpiredRows(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	if err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "stale", testValue{Name: "stale"}, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := Write(ctx, q, PARTITION_MOVIE_DETAILS, "fresh", testValue{Name: "fresh"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := q.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := q.db.QueryContext(ctx, "SELECT cache_key FROM cache_items ORDER BY cache_key")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "fresh" {
		t.Errorf("remaining keys = %v, want [fresh]", keys)
	}
}

func TestLookupCorruptValueReturnsError(t *testing.T) {
	ctx := context.Background()
	q := newTestQueries(t)
	err := q.Write(ctx, WriteParams{
		Partition:  PARTITION_MOVIE_DETAILS,
		CacheKey:   "key",
		CacheValue: []byte("not json"),
		Expires:    time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Lookup[testValue](ctx, q, PARTITION_MOVIE_DETAILS, "key")
	if err == nil {
		t.Error("expected error for corrupt cache value, got nil")
	}
}
