package views

import (
	"context"
	"strings"
	"testing"
)

func renderPublicMovieLog(t *testing.T, vm PublicMovieLogViewModel) string {
	t.Helper()
	var sb strings.Builder
	if err := PublicMovieLog(vm).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func TestPublicMovieLogRendersRatingAsStars(t *testing.T) {
	body := renderPublicMovieLog(t, PublicMovieLogViewModel{
		MovieName: "The Third Man",
		Rating:    "4.5",
	})
	if !strings.Contains(body, "star-display") {
		t.Errorf("body does not contain star display: %s", body)
	}
	if !strings.Contains(body, "★★★★★") {
		t.Errorf("body does not contain star glyphs: %s", body)
	}
	if strings.Contains(body, "4.5/5<") {
		t.Errorf("body still contains raw numeric rating text: %s", body)
	}
}

func TestPublicMovieLogOmitsStarsWhenRatingEmpty(t *testing.T) {
	body := renderPublicMovieLog(t, PublicMovieLogViewModel{
		MovieName: "The Third Man",
	})
	if strings.Contains(body, "star-display") {
		t.Errorf("body contains star display for empty rating: %s", body)
	}
}

func TestPublicMovieLogRendersMarkdownReview(t *testing.T) {
	body := renderPublicMovieLog(t, PublicMovieLogViewModel{
		MovieName: "The Third Man",
		Review:    "**Bold** text with [link](https://example.com)",
	})
	if !strings.Contains(body, "<strong>Bold</strong>") {
		t.Errorf("body does not contain rendered bold markdown: %s", body)
	}
	if !strings.Contains(body, `href="https://example.com"`) {
		t.Errorf("body does not contain rendered markdown link: %s", body)
	}
}
