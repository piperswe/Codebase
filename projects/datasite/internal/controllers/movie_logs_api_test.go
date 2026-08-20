package controllers

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"codeberg.org/pmc/Codebase/projects/datasite/internal/db"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/oas"
	_ "modernc.org/sqlite"
)

type movieLogBody struct {
	ID                     int64    `json:"id"`
	MovieID                int64    `json:"movie_id"`
	Date                   *int64   `json:"date"`
	CinemaID               *int64   `json:"cinema_id"`
	Rating                 *float64 `json:"rating"`
	Review                 *string  `json:"review"`
	ReviewContainsSpoilers bool     `json:"review_contains_spoilers"`
}

func newMovieLogsAPI(t *testing.T) (http.Handler, *db.Queries, *sql.DB) {
	t.Helper()
	conn, queries := openTestDB(t)
	h := &APIHandler{DB: queries, DBConn: conn, AdminAPIKey: "test"}
	server, err := oas.NewServer(h, h)
	if err != nil {
		t.Fatal(err)
	}
	return mountAt("/movie_logs", server), queries, conn
}

func TestMovieLogsListEmptyReturnsEmptyArray(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodGet, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("got body %q, want []", got)
	}
}

func TestMovieLogsListReturnsLogsNewestFirst(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 1, 20240101)
	logMovieOn(t, queries, 2, 20260702)

	w := doJSON(t, mux, http.MethodGet, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	logs := decodeJSON[[]movieLogBody](t, w)
	if len(logs) != 2 {
		t.Fatalf("got %d logs, want 2", len(logs))
	}
	if logs[0].MovieID != 2 || logs[1].MovieID != 1 {
		t.Errorf("got movie IDs %d, %d; want 2, 1", logs[0].MovieID, logs[1].MovieID)
	}
}

func TestMovieLogsCreateWithAllFields(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	c := createCinema(t, queries, "Music Box")

	w := doJSON(t, mux, http.MethodPost, "/", `{
		"movie_id": 550,
		"date": 20260702,
		"cinema_id": `+strconv.Itoa(int(c.ID))+`,
		"rating": 4.5,
		"review": "great",
		"review_contains_spoilers": true
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", w.Code, w.Body.String())
	}
	created := decodeJSON[movieLogBody](t, w)
	if created.ID == 0 {
		t.Error("got zero ID, want assigned ID")
	}
	if created.MovieID != 550 {
		t.Errorf("got movie_id %d, want 550", created.MovieID)
	}
	if created.Date == nil || *created.Date != 20260702 {
		t.Errorf("got date %v, want 20260702", created.Date)
	}
	if created.CinemaID == nil || *created.CinemaID != c.ID {
		t.Errorf("got cinema_id %v, want %d", created.CinemaID, c.ID)
	}
	if created.Rating == nil || *created.Rating != 4.5 {
		t.Errorf("got rating %v, want 4.5", created.Rating)
	}
	if created.Review == nil || *created.Review != "great" {
		t.Errorf("got review %v, want great", created.Review)
	}
	if !created.ReviewContainsSpoilers {
		t.Error("got review_contains_spoilers false, want true")
	}

	stored, err := queries.GetMovieLog(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.MovieID != 550 || stored.Date.Int64 != 20260702 || stored.CinemaID.Int64 != c.ID {
		t.Errorf("stored log %+v does not match request", stored)
	}
}

func TestMovieLogsCreateWithOnlyMovieID(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)

	w := doJSON(t, mux, http.MethodPost, "/", `{"movie_id": 550}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", w.Code, w.Body.String())
	}
	created := decodeJSON[movieLogBody](t, w)
	if created.MovieID != 550 {
		t.Errorf("got movie_id %d, want 550", created.MovieID)
	}
	if created.Date != nil || created.CinemaID != nil || created.Rating != nil || created.Review != nil {
		t.Errorf("got non-null optional fields: %+v", created)
	}
	if created.ReviewContainsSpoilers {
		t.Error("got review_contains_spoilers true, want false")
	}
}

func TestMovieLogsCreateRejectsInvalidJSON(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodPost, "/", `{"movie_id":`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestMovieLogsCreateRejectsMissingMovieID(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodPost, "/", `{"rating": 4.5}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestMovieLogsCreateRejectsUnknownCinema(t *testing.T) {
	discardLogs(t)
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodPost, "/", `{"movie_id": 550, "cinema_id": 999}`)
	if w.Code != http.StatusConflict {
		t.Errorf("got status %d, want 409: %s", w.Code, w.Body.String())
	}
}

func TestMovieLogsGetReturnsLog(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodGet, "/"+strconv.Itoa(int(logs[0].ID)), "")
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	got := decodeJSON[movieLogBody](t, w)
	if got.ID != logs[0].ID || got.MovieID != 550 {
		t.Errorf("got %+v, want id %d movie_id 550", got, logs[0].ID)
	}
	if got.Date == nil || *got.Date != 20260702 {
		t.Errorf("got date %v, want 20260702", got.Date)
	}
}

func TestMovieLogsGetReturns404ForMissing(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodGet, "/42", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestMovieLogsGetReturns400ForBadID(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodGet, "/notanumber", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestMovieLogsUpdateReturnsUpdatedLog(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	l, err := queries.LogMovie(context.Background(), db.LogMovieParams{
		MovieID: 550,
		Date:    sql.NullInt64{Int64: 20240101, Valid: true},
		Rating:  sql.NullFloat64{Float64: 2.0, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodPut, "/"+strconv.Itoa(int(l.ID)), `{
		"movie_id": 550,
		"date": 20260702,
		"rating": 4.0,
		"review": "better on rewatch"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[movieLogBody](t, w)
	if got.ID != l.ID {
		t.Errorf("got id %d, want %d", got.ID, l.ID)
	}
	if got.Date == nil || *got.Date != 20260702 {
		t.Errorf("got date %v, want 20260702", got.Date)
	}
	if got.Rating == nil || *got.Rating != 4.0 {
		t.Errorf("got rating %v, want 4.0", got.Rating)
	}

	stored, err := queries.GetMovieLog(context.Background(), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Date.Int64 != 20260702 || stored.Review.String != "better on rewatch" {
		t.Errorf("stored log %+v does not match update", stored)
	}
}

func TestMovieLogsUpdateReturns404ForMissing(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodPut, "/42", `{"movie_id": 550}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestMovieLogsUpdateRejectsMissingMovieID(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	w := doJSON(t, mux, http.MethodPut, "/"+strconv.Itoa(int(logs[0].ID)), `{"rating": 4.5}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

// fullMovieLog inserts a log with every field populated and returns it.
func fullMovieLog(t *testing.T, queries *db.Queries, cinemaID int64) db.MovieLog {
	t.Helper()
	l, err := queries.LogMovie(context.Background(), db.LogMovieParams{
		MovieID:                550,
		Date:                   sql.NullInt64{Int64: 20240101, Valid: true},
		CinemaID:               sql.NullInt64{Int64: cinemaID, Valid: true},
		Rating:                 sql.NullFloat64{Float64: 2.0, Valid: true},
		Review:                 sql.NullString{String: "meh", Valid: true},
		ReviewContainsSpoilers: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestMovieLogsPatchUpdatesOnlyProvidedFields(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	c := createCinema(t, queries, "Music Box")
	l := fullMovieLog(t, queries, c.ID)

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(l.ID)), `{"rating": 4.5}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[movieLogBody](t, w)
	if got.Rating == nil || *got.Rating != 4.5 {
		t.Errorf("got rating %v, want 4.5", got.Rating)
	}
	if got.MovieID != 550 {
		t.Errorf("got movie_id %d, want 550 unchanged", got.MovieID)
	}
	if got.Date == nil || *got.Date != 20240101 {
		t.Errorf("got date %v, want 20240101 unchanged", got.Date)
	}
	if got.CinemaID == nil || *got.CinemaID != c.ID {
		t.Errorf("got cinema_id %v, want %d unchanged", got.CinemaID, c.ID)
	}
	if got.Review == nil || *got.Review != "meh" {
		t.Errorf("got review %v, want meh unchanged", got.Review)
	}
	if !got.ReviewContainsSpoilers {
		t.Error("got review_contains_spoilers false, want true unchanged")
	}

	stored, err := queries.GetMovieLog(context.Background(), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Rating.Float64 != 4.5 || stored.Review.String != "meh" {
		t.Errorf("stored log %+v does not match patch", stored)
	}
}

func TestMovieLogsPatchClearsNullableFieldWithNull(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	c := createCinema(t, queries, "Music Box")
	l := fullMovieLog(t, queries, c.ID)

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(l.ID)), `{"date": null, "cinema_id": null}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[movieLogBody](t, w)
	if got.Date != nil {
		t.Errorf("got date %v, want null", got.Date)
	}
	if got.CinemaID != nil {
		t.Errorf("got cinema_id %v, want null", got.CinemaID)
	}
	if got.Rating == nil || *got.Rating != 2.0 {
		t.Errorf("got rating %v, want 2.0 unchanged", got.Rating)
	}

	stored, err := queries.GetMovieLog(context.Background(), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Date.Valid || stored.CinemaID.Valid {
		t.Errorf("stored log %+v still has date or cinema_id set", stored)
	}
}

func TestMovieLogsPatchUpdatesMovieID(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(logs[0].ID)), `{"movie_id": 603}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[movieLogBody](t, w)
	if got.MovieID != 603 {
		t.Errorf("got movie_id %d, want 603", got.MovieID)
	}
}

func TestMovieLogsPatchRejectsNullMovieID(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(logs[0].ID)), `{"movie_id": null}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestMovieLogsPatchRejectsWrongType(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(logs[0].ID)), `{"rating": "high"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestMovieLogsPatchEmptyObjectLeavesLogUnchanged(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	c := createCinema(t, queries, "Music Box")
	l := fullMovieLog(t, queries, c.ID)

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(l.ID)), `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[movieLogBody](t, w)
	if got.MovieID != 550 || got.Date == nil || *got.Date != 20240101 ||
		got.CinemaID == nil || got.Rating == nil || got.Review == nil || !got.ReviewContainsSpoilers {
		t.Errorf("got %+v, want log unchanged", got)
	}
}

func TestMovieLogsPatchReturns404ForMissing(t *testing.T) {
	mux, _, _ := newMovieLogsAPI(t)
	w := doJSON(t, mux, http.MethodPatch, "/42", `{"rating": 4.5}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestMovieLogsPatchRejectsUnknownCinema(t *testing.T) {
	discardLogs(t)
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(logs[0].ID)), `{"cinema_id": 999}`)
	if w.Code != http.StatusConflict {
		t.Errorf("got status %d, want 409: %s", w.Code, w.Body.String())
	}
}

func TestMovieLogsPatchRejectsInvalidJSON(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(logs[0].ID)), `{"rating":`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestMovieLogsDeleteRemovesLog(t *testing.T) {
	mux, queries, _ := newMovieLogsAPI(t)
	logMovieOn(t, queries, 550, 20260702)
	logs, err := queries.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodDelete, "/"+strconv.Itoa(int(logs[0].ID)), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want 204", w.Code)
	}

	_, err = queries.GetMovieLog(context.Background(), logs[0].ID)
	if err != sql.ErrNoRows {
		t.Errorf("got err %v after delete, want sql.ErrNoRows", err)
	}
}

func TestMovieLogsListReturns500OnDBError(t *testing.T) {
	discardLogs(t)
	mux, _, conn := newMovieLogsAPI(t)
	conn.Close()
	w := doJSON(t, mux, http.MethodGet, "/", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", w.Code)
	}
}
