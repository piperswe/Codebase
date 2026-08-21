package views

import (
	"context"
	"strings"
	"testing"

	"github.com/piperswe/Codebase/projects/datasite/internal/version"
)

func renderHome(t *testing.T, vm HomeViewModel) string {
	t.Helper()
	var sb strings.Builder
	if err := Home(vm).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func TestHomeRenders(t *testing.T) {
	body := renderHome(t, HomeViewModel{})
	if !strings.Contains(body, "Datasite") {
		t.Errorf("body does not contain Datasite: %q", body)
	}
	if !strings.Contains(body, "Recent movie logs") {
		t.Errorf("body does not contain movie logs heading: %q", body)
	}
}

func TestHomeRendersMovieLogs(t *testing.T) {
	body := renderHome(t, HomeViewModel{MovieLogs: []MovieLogViewModel{
		{URL: "https://example.com/logs/2", MovieName: "The Third Man", Year: 2026, Month: 7, Day: 2},
		{URL: "https://example.com/logs/1", MovieName: "Metropolis", Year: 2024, Month: 11, Day: 23},
	}})
	if !strings.Contains(body, "The Third Man") {
		t.Errorf("body does not contain first log entry: %s", body)
	}
	if !strings.Contains(body, "2026-07-02") {
		t.Errorf("body does not contain first log date: %s", body)
	}
	if !strings.Contains(body, "Metropolis") {
		t.Errorf("body does not contain second log entry: %s", body)
	}
	if !strings.Contains(body, "2024-11-23") {
		t.Errorf("body does not contain second log date: %s", body)
	}
	if !strings.Contains(body, `href="https://example.com/logs/2"`) {
		t.Errorf("body does not link to log URL: %s", body)
	}
}

func TestHomeZeroPadsLogDates(t *testing.T) {
	body := renderHome(t, HomeViewModel{MovieLogs: []MovieLogViewModel{
		{URL: "https://example.com", MovieName: "Movie", Year: 2026, Month: 1, Day: 5},
	}})
	if !strings.Contains(body, "2026-01-05") {
		t.Errorf("body does not contain zero-padded date: %s", body)
	}
}

func TestHomeEscapesMovieName(t *testing.T) {
	body := renderHome(t, HomeViewModel{MovieLogs: []MovieLogViewModel{
		{URL: "https://example.com", MovieName: `<script>alert("xss")</script>`, Year: 2026, Month: 7, Day: 2},
	}})
	if strings.Contains(body, "<script>") {
		t.Errorf("body contains unescaped movie name: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("body does not contain escaped movie name: %s", body)
	}
}

func TestHomeSanitizesUnsafeLogURL(t *testing.T) {
	body := renderHome(t, HomeViewModel{MovieLogs: []MovieLogViewModel{
		{URL: "javascript:alert(1)", MovieName: "Movie", Year: 2026, Month: 7, Day: 2},
	}})
	if strings.Contains(body, "javascript:") {
		t.Errorf("body contains unsanitized javascript: URL: %s", body)
	}
}

func TestHomeRendersPageChrome(t *testing.T) {
	body := renderHome(t, HomeViewModel{
		ServerSrc: "https://example.org/src",
	})
	if !strings.HasPrefix(body, "<!doctype html>") {
		t.Errorf("body does not start with doctype: %.40s", body)
	}
	if !strings.Contains(body, `href="https://example.org/src"`) {
		t.Errorf("body does not link to server source: %s", body)
	}
	if !strings.Contains(body, "running version "+version.Version) {
		t.Errorf("body does not mention version: %s", body)
	}
	if !strings.Contains(body, `href="/static/css/bootstrap.min.css"`) {
		t.Errorf("body does not reference stylesheet: %s", body)
	}
}

func TestHomeSanitizesUnsafeServerSrcURL(t *testing.T) {
	body := renderHome(t, HomeViewModel{
		ServerSrc: "javascript:alert(1)",
	})
	if strings.Contains(body, "javascript:") {
		t.Errorf("body contains unsanitized javascript: URL: %s", body)
	}
}

func TestHomeRendersRatingAsStars(t *testing.T) {
	body := renderHome(t, HomeViewModel{MovieLogs: []MovieLogViewModel{
		{URL: "https://example.com", MovieName: "Movie", Year: 2026, Month: 7, Day: 2, Rating: "4.5"},
	}})
	if !strings.Contains(body, "star-display") {
		t.Errorf("body does not contain star display: %s", body)
	}
	if !strings.Contains(body, "★★★★★") {
		t.Errorf("body does not contain star glyphs: %s", body)
	}
	if strings.Contains(body, "/5<") || strings.Contains(body, "4.5/5</") {
		t.Errorf("body still contains raw numeric rating text: %s", body)
	}
}

func TestHomeOmitsStarsWhenRatingEmpty(t *testing.T) {
	body := renderHome(t, HomeViewModel{MovieLogs: []MovieLogViewModel{
		{URL: "https://example.com", MovieName: "Movie", Year: 2026, Month: 7, Day: 2},
	}})
	if strings.Contains(body, "star-display") {
		t.Errorf("body contains star display for empty rating: %s", body)
	}
}
