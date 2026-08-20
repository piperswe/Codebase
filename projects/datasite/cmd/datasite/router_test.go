package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codeberg.org/pmc/Codebase/projects/datasite/internal/db"
	tmdb "github.com/cyruzin/golang-tmdb"
	"github.com/go-chi/httplog/v3"
	_ "modernc.org/sqlite"
)

type fakeMovieDB struct {
	details *tmdb.MovieDetails
	err     error
	panic   bool
}

func (f *fakeMovieDB) GetMovieByID(ctx context.Context, id int) (*tmdb.MovieDetails, error) {
	if f.panic {
		panic("boom")
	}
	return f.details, f.err
}

func (f *fakeMovieDB) SearchMovie(ctx context.Context, title string, year int) (int64, string, error) {
	return 0, "", fmt.Errorf("unimplemented")
}

func newTestMux(t *testing.T, movies *fakeMovieDB) (http.Handler, *db.Queries) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// Each connection to :memory: gets its own database, so keep the pool
	// at a single connection.
	conn.SetMaxOpenConns(1)
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	queries := db.New(conn)
	mux := SetupMux(&universe{
		logger:      slog.New(slog.DiscardHandler),
		logFormat:   httplog.SchemaOTEL,
		dbConn:      conn,
		db:          queries,
		movies:      movies,
		serverSrc:   "https://example.org/src",
		adminAPIKey: "test-key",
	})
	return mux, queries
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

func get(t *testing.T, mux http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func TestMuxServesHome(t *testing.T) {
	mux, queries := newTestMux(t, &fakeMovieDB{details: &tmdb.MovieDetails{Title: "Some Movie"}})
	logMovieOn(t, queries, 550, 20260702)
	w := get(t, mux, "/")
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Some Movie") {
		t.Errorf("body does not contain movie title: %s", w.Body.String())
	}
	if got := w.Header().Get("Server-Src"); got != "https://example.org/src" {
		t.Errorf("got Server-Src header %q, want %q", got, "https://example.org/src")
	}
}

func TestMuxServesStaticFiles(t *testing.T) {
	mux, _ := newTestMux(t, &fakeMovieDB{})
	w := get(t, mux, "/static/css/bootstrap.min.css")
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("got Content-Type %q, want text/css", ct)
	}
	if got := w.Header().Get("Server-Src"); got != "https://example.org/src" {
		t.Errorf("got Server-Src header %q on static route, want %q", got, "https://example.org/src")
	}
}

func TestMuxServesCinemasAPI(t *testing.T) {
	mux, _ := newTestMux(t, &fakeMovieDB{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cinemas", strings.NewReader(`{"name":"Music Box"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/cinemas", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Music Box") {
		t.Errorf("body does not contain created cinema: %s", w.Body.String())
	}
}

func TestMuxServesMovieLogsAPI(t *testing.T) {
	mux, queries := newTestMux(t, &fakeMovieDB{})
	logMovieOn(t, queries, 550, 20260702)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/movie_logs", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"movie_id":550`) {
		t.Errorf("body does not contain logged movie: %s", w.Body.String())
	}
}

func TestMuxServesOpenAPISpec(t *testing.T) {
	mux, _ := newTestMux(t, &fakeMovieDB{})
	w := get(t, mux, "/api/v1/openapi.yaml")
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/yaml") {
		t.Errorf("got Content-Type %q, want text/yaml", ct)
	}
	if !strings.Contains(w.Body.String(), "openapi: 3.0.3") {
		t.Errorf("body does not look like an OpenAPI spec: %s", w.Body.String())
	}
}

func TestMuxAPIRequiresAuthorization(t *testing.T) {
	mux, _ := newTestMux(t, &fakeMovieDB{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cinemas", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", w.Code)
	}
}

func TestMuxAPIAuthorizesViaCookie(t *testing.T) {
	mux, _ := newTestMux(t, &fakeMovieDB{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cinemas", nil)
	req.AddCookie(&http.Cookie{
		Name:  "datasite-admin-api-key",
		Value: "test-key",
	})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}
}

func TestMuxReturns404ForUnknownPath(t *testing.T) {
	mux, _ := newTestMux(t, &fakeMovieDB{})
	w := get(t, mux, "/does-not-exist")
	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestMuxRecoversFromHandlerPanics(t *testing.T) {
	mux, queries := newTestMux(t, &fakeMovieDB{panic: true})
	logMovieOn(t, queries, 550, 20260702)
	w := get(t, mux, "/")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", w.Code)
	}
}
