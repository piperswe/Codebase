package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"codebase.bid/projects/datasite/internal/db"
	"codebase.bid/projects/datasite/internal/oas"
	_ "modernc.org/sqlite"
)

type cinemaBody struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func doJSON(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decodeJSON[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("got Content-Type %q, want application/json", ct)
	}
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("failed to decode body %q: %v", w.Body.String(), err)
	}
	return v
}

func createCinema(t *testing.T, q *db.Queries, name string) db.Cinema {
	t.Helper()
	c, err := q.CreateCinema(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newCinemasAPI(t *testing.T) (http.Handler, *db.Queries, *sql.DB) {
	t.Helper()
	conn, queries := openTestDB(t)
	h := &APIHandler{DB: queries, DBConn: conn, AdminAPIKey: "test"}
	server, err := oas.NewServer(h, h)
	if err != nil {
		t.Fatal(err)
	}
	return mountAt("/cinemas", server), queries, conn
}

func TestCinemasListEmptyReturnsEmptyArray(t *testing.T) {
	mux, _, _ := newCinemasAPI(t)
	w := doJSON(t, mux, http.MethodGet, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Errorf("got body %q, want []", got)
	}
}

func TestCinemasListReturnsCinemas(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	createCinema(t, queries, "Siskel")
	createCinema(t, queries, "Alamo")

	w := doJSON(t, mux, http.MethodGet, "/", "")
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	cinemas := decodeJSON[[]cinemaBody](t, w)
	if len(cinemas) != 2 {
		t.Fatalf("got %d cinemas, want 2", len(cinemas))
	}
	if cinemas[0].Name != "Alamo" || cinemas[1].Name != "Siskel" {
		t.Errorf("got cinemas %+v, want Alamo then Siskel", cinemas)
	}
}

func TestCinemasCreateReturns201AndPersists(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)

	w := doJSON(t, mux, http.MethodPost, "/", `{"name":"Music Box"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201: %s", w.Code, w.Body.String())
	}
	created := decodeJSON[cinemaBody](t, w)
	if created.Name != "Music Box" {
		t.Errorf("got name %q, want %q", created.Name, "Music Box")
	}
	if created.ID == 0 {
		t.Error("got zero ID, want assigned ID")
	}

	stored, err := queries.GetCinema(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Music Box" {
		t.Errorf("stored name %q, want %q", stored.Name, "Music Box")
	}
}

func TestCinemasCreateRejectsInvalidJSON(t *testing.T) {
	mux, _, _ := newCinemasAPI(t)
	w := doJSON(t, mux, http.MethodPost, "/", `{"name":`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestCinemasCreateRejectsEmptyName(t *testing.T) {
	mux, _, _ := newCinemasAPI(t)
	w := doJSON(t, mux, http.MethodPost, "/", `{"name":""}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestCinemasGetReturnsCinema(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")

	w := doJSON(t, mux, http.MethodGet, "/"+strconv.Itoa(int(c.ID)), "")
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	got := decodeJSON[cinemaBody](t, w)
	if got.ID != c.ID || got.Name != "Siskel" {
		t.Errorf("got %+v, want id %d name Siskel", got, c.ID)
	}
}

func TestCinemasGetReturns404ForMissing(t *testing.T) {
	mux, _, _ := newCinemasAPI(t)
	w := doJSON(t, mux, http.MethodGet, "/42", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestCinemasGetReturns400ForBadID(t *testing.T) {
	mux, _, _ := newCinemasAPI(t)
	w := doJSON(t, mux, http.MethodGet, "/notanumber", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestCinemasUpdateReturnsUpdatedCinema(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Old Name")

	w := doJSON(t, mux, http.MethodPut, "/"+strconv.Itoa(int(c.ID)), `{"name":"New Name"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[cinemaBody](t, w)
	if got.ID != c.ID || got.Name != "New Name" {
		t.Errorf("got %+v, want id %d name New Name", got, c.ID)
	}

	stored, err := queries.GetCinema(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "New Name" {
		t.Errorf("stored name %q, want %q", stored.Name, "New Name")
	}
}

func TestCinemasUpdateReturns404ForMissing(t *testing.T) {
	mux, _, _ := newCinemasAPI(t)
	w := doJSON(t, mux, http.MethodPut, "/42", `{"name":"New Name"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestCinemasUpdateRejectsEmptyName(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")
	w := doJSON(t, mux, http.MethodPut, "/"+strconv.Itoa(int(c.ID)), `{"name":""}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestCinemasPatchUpdatesName(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Old Name")

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(c.ID)), `{"name":"New Name"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[cinemaBody](t, w)
	if got.ID != c.ID || got.Name != "New Name" {
		t.Errorf("got %+v, want id %d name New Name", got, c.ID)
	}

	stored, err := queries.GetCinema(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "New Name" {
		t.Errorf("stored name %q, want %q", stored.Name, "New Name")
	}
}

func TestCinemasPatchEmptyObjectLeavesCinemaUnchanged(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")

	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(c.ID)), `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodeJSON[cinemaBody](t, w)
	if got.Name != "Siskel" {
		t.Errorf("got name %q, want Siskel", got.Name)
	}
}

func TestCinemasPatchRejectsInvalidJSON(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")
	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(c.ID)), `{"name":`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestCinemasPatchRejectsEmptyName(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")
	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(c.ID)), `{"name":""}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestCinemasPatchRejectsNullName(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")
	w := doJSON(t, mux, http.MethodPatch, "/"+strconv.Itoa(int(c.ID)), `{"name":null}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want 400", w.Code)
	}
}

func TestCinemasPatchReturns404ForMissing(t *testing.T) {
	mux, _, _ := newCinemasAPI(t)
	w := doJSON(t, mux, http.MethodPatch, "/42", `{"name":"New Name"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("got status %d, want 404", w.Code)
	}
}

func TestCinemasDeleteRemovesCinema(t *testing.T) {
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")

	w := doJSON(t, mux, http.MethodDelete, "/"+strconv.Itoa(int(c.ID)), "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want 204", w.Code)
	}

	_, err := queries.GetCinema(context.Background(), c.ID)
	if err != sql.ErrNoRows {
		t.Errorf("got err %v after delete, want sql.ErrNoRows", err)
	}
}

func TestCinemasDeleteReturns409WhenReferenced(t *testing.T) {
	discardLogs(t)
	mux, queries, _ := newCinemasAPI(t)
	c := createCinema(t, queries, "Siskel")
	_, err := queries.LogMovie(context.Background(), db.LogMovieParams{
		MovieID:  550,
		CinemaID: sql.NullInt64{Int64: c.ID, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, mux, http.MethodDelete, "/"+strconv.Itoa(int(c.ID)), "")
	if w.Code != http.StatusConflict {
		t.Errorf("got status %d, want 409: %s", w.Code, w.Body.String())
	}
}

func TestCinemasListReturns500OnDBError(t *testing.T) {
	discardLogs(t)
	mux, _, conn := newCinemasAPI(t)
	conn.Close()
	w := doJSON(t, mux, http.MethodGet, "/", "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want 500", w.Code)
	}
}
