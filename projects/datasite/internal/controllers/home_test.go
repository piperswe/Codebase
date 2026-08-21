package controllers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tmdb "github.com/cyruzin/golang-tmdb"
	"github.com/piperswe/Codebase/projects/datasite/internal/db"
	_ "modernc.org/sqlite"
)

type fakeMovieDB struct {
	gotIDs []int
	titles map[int]string
	err    error
}

func (f *fakeMovieDB) GetMovieByID(ctx context.Context, id int) (*tmdb.MovieDetails, error) {
	f.gotIDs = append(f.gotIDs, id)
	if f.err != nil {
		return nil, f.err
	}
	title, ok := f.titles[id]
	if !ok {
		title = fmt.Sprintf("Movie %d", id)
	}
	return &tmdb.MovieDetails{Title: title}, nil
}

func (f *fakeMovieDB) SearchMovie(ctx context.Context, title string, year int) (int64, string, error) {
	return 0, "", fmt.Errorf("unimplemented")
}

func openTestDB(t *testing.T) (*sql.DB, *db.Queries) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// Each connection to :memory: gets its own database, so keep the pool
	// at a single connection.
	conn.SetMaxOpenConns(1)
	// Match production (main.go), which enables foreign key enforcement.
	if _, err := conn.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return conn, db.New(conn)
}

func logMovieOn(t *testing.T, q *db.Queries, movieID int64, date int64) {
	t.Helper()
	_, err := q.LogMovie(context.Background(), db.LogMovieParams{
		MovieID: movieID,
		Date:    sql.NullInt64{Int64: date, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func serveHome(t *testing.T, queries *db.Queries, movies *fakeMovieDB) *httptest.ResponseRecorder {
	t.Helper()
	c := &HomeController{
		ServerSrc: "https://example.org/src",
		DB:        queries,
		Movies:    movies,
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	return w
}

func discardLogs(t *testing.T) {
	t.Helper()
	old := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { slog.SetDefault(old) })
}

// mountAt wraps h so that requests arrive with prefix prepended to their path.
// Useful for testing an ogen server whose spec defines absolute paths.
func mountAt(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			r.URL.Path = prefix
		} else {
			r.URL.Path = prefix + r.URL.Path
		}
		r.URL.RawPath = ""
		h.ServeHTTP(w, r)
	})
}

func TestHomeRendersRecentMovieLogs(t *testing.T) {
	_, queries := openTestDB(t)
	logMovieOn(t, queries, 550, 20240101)
	logMovieOn(t, queries, 603, 20260702)
	movies := &fakeMovieDB{titles: map[int]string{
		550: "Fight Club",
		603: "The Matrix",
	}}

	w := serveHome(t, queries, movies)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "The Matrix") {
		t.Errorf("body does not contain newer log entry: %s", body)
	}
	if !strings.Contains(body, "2026-07-02") {
		t.Errorf("body does not contain newer log date: %s", body)
	}
	if !strings.Contains(body, "Fight Club") {
		t.Errorf("body does not contain older log entry: %s", body)
	}
	if !strings.Contains(body, "2024-01-01") {
		t.Errorf("body does not contain older log date: %s", body)
	}
	if strings.Index(body, "The Matrix") > strings.Index(body, "Fight Club") {
		t.Errorf("logs are not ordered newest first: %s", body)
	}
}

func TestHomeLooksUpMoviesForLoggedIDs(t *testing.T) {
	_, queries := openTestDB(t)
	logMovieOn(t, queries, 550, 20240101)
	logMovieOn(t, queries, 603, 20260702)
	movies := &fakeMovieDB{}

	serveHome(t, queries, movies)
	if len(movies.gotIDs) != 2 {
		t.Fatalf("movie db called %d times, want 2", len(movies.gotIDs))
	}
	if movies.gotIDs[0] != 603 || movies.gotIDs[1] != 550 {
		t.Errorf("got movie IDs %v, want [603 550]", movies.gotIDs)
	}
}

func TestHomeLimitsToTenMostRecentLogs(t *testing.T) {
	_, queries := openTestDB(t)
	for i := int64(1); i <= 12; i++ {
		logMovieOn(t, queries, i, 20260600+i)
	}
	movies := &fakeMovieDB{}

	w := serveHome(t, queries, movies)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if len(movies.gotIDs) != 10 {
		t.Errorf("movie db called %d times, want 10", len(movies.gotIDs))
	}
	body := w.Body.String()
	if strings.Contains(body, "Movie 1<") || strings.Contains(body, "Movie 2<") {
		t.Errorf("body contains logs beyond the most recent ten: %s", body)
	}
}

func TestHomeRendersWithNoLogs(t *testing.T) {
	_, queries := openTestDB(t)
	movies := &fakeMovieDB{}

	w := serveHome(t, queries, movies)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if len(movies.gotIDs) != 0 {
		t.Errorf("movie db called %d times, want 0", len(movies.gotIDs))
	}
	if !strings.Contains(w.Body.String(), "Recent movie logs") {
		t.Errorf("body does not contain heading: %s", w.Body.String())
	}
}

func TestHomeReturns500OnDBError(t *testing.T) {
	discardLogs(t)
	conn, queries := openTestDB(t)
	conn.Close()
	movies := &fakeMovieDB{}

	w := serveHome(t, queries, movies)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", w.Code)
	}
	if len(movies.gotIDs) != 0 {
		t.Errorf("movie db called %d times, want 0", len(movies.gotIDs))
	}
}

func TestHomeReturns500OnMovieDBError(t *testing.T) {
	discardLogs(t)
	_, queries := openTestDB(t)
	logMovieOn(t, queries, 550, 20260702)
	movies := &fakeMovieDB{err: errors.New("tmdb down")}

	w := serveHome(t, queries, movies)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", w.Code)
	}
}
