package db

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestQueries(t *testing.T) *Queries {
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

func logMovieOn(t *testing.T, q *Queries, movieID int64, date int64) MovieLog {
	t.Helper()
	l, err := q.LogMovie(context.Background(), LogMovieParams{
		MovieID: movieID,
		Date:    sql.NullInt64{Int64: date, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestGetRecentMovieLogsOrdersByDateDescending(t *testing.T) {
	q := openTestQueries(t)
	logMovieOn(t, q, 1, 20240101)
	logMovieOn(t, q, 2, 20260702)
	logMovieOn(t, q, 3, 20250315)

	logs, err := q.GetRecentMovieLogs(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("got %d logs, want 3", len(logs))
	}
	wantOrder := []int64{2, 3, 1}
	for i, want := range wantOrder {
		if logs[i].MovieID != want {
			t.Errorf("logs[%d].MovieID = %d, want %d", i, logs[i].MovieID, want)
		}
	}
}

func TestCreateCinemaReturnsCinemaWithID(t *testing.T) {
	q := openTestQueries(t)

	c, err := q.CreateCinema(context.Background(), "Music Box Theatre")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 {
		t.Error("got zero ID, want assigned ID")
	}
	if c.Name != "Music Box Theatre" {
		t.Errorf("got name %q, want %q", c.Name, "Music Box Theatre")
	}

	got, err := q.GetCinema(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Errorf("got %+v from GetCinema, want %+v", got, c)
	}
}

func TestListCinemasReturnsAllOrderedByName(t *testing.T) {
	q := openTestQueries(t)
	for _, name := range []string{"Siskel", "Alamo", "Music Box"} {
		if _, err := q.CreateCinema(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}

	cinemas, err := q.ListCinemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cinemas) != 3 {
		t.Fatalf("got %d cinemas, want 3", len(cinemas))
	}
	wantOrder := []string{"Alamo", "Music Box", "Siskel"}
	for i, want := range wantOrder {
		if cinemas[i].Name != want {
			t.Errorf("cinemas[%d].Name = %q, want %q", i, cinemas[i].Name, want)
		}
	}
}

func TestListMovieLogsReturnsAllOrderedByDateDescending(t *testing.T) {
	q := openTestQueries(t)
	logMovieOn(t, q, 1, 20240101)
	logMovieOn(t, q, 2, 20260702)
	logMovieOn(t, q, 3, 20250315)

	logs, err := q.ListMovieLogs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("got %d logs, want 3", len(logs))
	}
	wantOrder := []int64{2, 3, 1}
	for i, want := range wantOrder {
		if logs[i].MovieID != want {
			t.Errorf("logs[%d].MovieID = %d, want %d", i, logs[i].MovieID, want)
		}
	}
}

func TestGetRecentMovieLogsAppliesLimit(t *testing.T) {
	q := openTestQueries(t)
	for i := int64(1); i <= 5; i++ {
		logMovieOn(t, q, i, 20260700+i)
	}

	logs, err := q.GetRecentMovieLogs(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("got %d logs, want 2", len(logs))
	}
	if logs[0].MovieID != 5 || logs[1].MovieID != 4 {
		t.Errorf("got movie IDs %d, %d; want 5, 4", logs[0].MovieID, logs[1].MovieID)
	}
}
