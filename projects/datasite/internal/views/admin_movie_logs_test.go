package views

import (
	"context"
	"strings"
	"testing"
)

func renderAdminMovieLogs(t *testing.T, vm AdminMovieLogsListViewModel) string {
	t.Helper()
	var sb strings.Builder
	if err := AdminMovieLogs(vm).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func TestAdminMovieLogsRendersRatingAsStars(t *testing.T) {
	body := renderAdminMovieLogs(t, AdminMovieLogsListViewModel{
		Logs: []AdminMovieLogsLogViewModel{
			{URL: "/admin/movie_logs/1", MovieName: "The Third Man", Date: "2026-07-02", Rating: "4.5"},
		},
	})
	if !strings.Contains(body, "star-display") {
		t.Errorf("body does not contain star display: %s", body)
	}
	if !strings.Contains(body, "★★★★★") {
		t.Errorf("body does not contain star glyphs: %s", body)
	}
}

func TestAdminMovieLogsRendersMarkdownPreview(t *testing.T) {
	body := renderAdminMovieLog(t, AdminMovieLogViewModel{
		ID:        1,
		MovieID:   550,
		MovieName: "The Third Man",
		Review:    "**Bold** review",
	})
	if !strings.Contains(body, "<strong>Bold</strong>") {
		t.Errorf("body does not contain rendered markdown preview: %s", body)
	}
}

func renderAdminMovieLog(t *testing.T, vm AdminMovieLogViewModel) string {
	t.Helper()
	var sb strings.Builder
	if err := AdminMovieLog(vm).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}
