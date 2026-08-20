package moviedb

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"codeberg.org/pmc/Codebase/projects/datasite/internal/cache"
	tmdb "github.com/cyruzin/golang-tmdb"
	_ "modernc.org/sqlite"
)

type fakeTMDB struct {
	details *tmdb.MovieDetails
	err     error
	calls   int
}

func (f *fakeTMDB) GetMovieDetails(id int, urlOptions map[string]string) (*tmdb.MovieDetails, error) {
	f.calls++
	return f.details, f.err
}

func (f *fakeTMDB) GetSearchMovies(query string, urlOptions map[string]string) (*tmdb.SearchMovies, error) {
	return nil, nil
}

func newTestCache(t *testing.T) *cache.Queries {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	if err := cache.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return cache.New(conn)
}

func TestGetMovieByIDFetchesOnCacheMiss(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTMDB{details: &tmdb.MovieDetails{Title: "Dune"}}
	m := NewCachedMovieDB(fake, fake, newTestCache(t), nil)
	got, err := m.GetMovieByID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Dune" {
		t.Errorf("got title %q, want %q", got.Title, "Dune")
	}
	if fake.calls != 1 {
		t.Errorf("tmdb called %d times, want 1", fake.calls)
	}
}

func TestGetMovieByIDServesSecondCallFromCache(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTMDB{details: &tmdb.MovieDetails{Title: "Dune"}}
	m := NewCachedMovieDB(fake, fake, newTestCache(t), nil)
	if _, err := m.GetMovieByID(ctx, 42); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetMovieByID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Dune" {
		t.Errorf("got title %q, want %q", got.Title, "Dune")
	}
	if fake.calls != 1 {
		t.Errorf("tmdb called %d times, want 1 (second call should hit cache)", fake.calls)
	}
}

func TestGetMovieByIDServesFromExistingCache(t *testing.T) {
	ctx := context.Background()
	q := newTestCache(t)
	err := cache.Write(ctx, q, cache.PARTITION_MOVIE_DETAILS, "42",
		tmdb.MovieDetails{Title: "Cached"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeTMDB{details: &tmdb.MovieDetails{Title: "Fresh"}}
	m := NewCachedMovieDB(fake, fake, q, nil)
	got, err := m.GetMovieByID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Cached" {
		t.Errorf("got title %q, want %q", got.Title, "Cached")
	}
	if fake.calls != 0 {
		t.Errorf("tmdb called %d times, want 0", fake.calls)
	}
}

func TestGetMovieByIDRefetchesExpiredEntry(t *testing.T) {
	ctx := context.Background()
	q := newTestCache(t)
	err := cache.Write(ctx, q, cache.PARTITION_MOVIE_DETAILS, "42",
		tmdb.MovieDetails{Title: "Stale"}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeTMDB{details: &tmdb.MovieDetails{Title: "Fresh"}}
	m := NewCachedMovieDB(fake, fake, q, nil)
	got, err := m.GetMovieByID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Fresh" {
		t.Errorf("got title %q, want %q", got.Title, "Fresh")
	}
	if fake.calls != 1 {
		t.Errorf("tmdb called %d times, want 1", fake.calls)
	}
}

func TestGetMovieByIDPropagatesFetchError(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("tmdb down")
	fake := &fakeTMDB{err: wantErr}
	m := NewCachedMovieDB(fake, fake, newTestCache(t), nil)
	_, err := m.GetMovieByID(ctx, 42)
	if !errors.Is(err, wantErr) {
		t.Errorf("got err %v, want %v", err, wantErr)
	}
}

func TestGetMovieByIDDoesNotCacheErrors(t *testing.T) {
	ctx := context.Background()
	fake := &fakeTMDB{err: errors.New("tmdb down")}
	m := NewCachedMovieDB(fake, fake, newTestCache(t), nil)
	if _, err := m.GetMovieByID(ctx, 42); err == nil {
		t.Fatal("expected error")
	}
	fake.err = nil
	fake.details = &tmdb.MovieDetails{Title: "Recovered"}
	got, err := m.GetMovieByID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Recovered" {
		t.Errorf("got title %q, want %q", got.Title, "Recovered")
	}
	if fake.calls != 2 {
		t.Errorf("tmdb called %d times, want 2", fake.calls)
	}
}
