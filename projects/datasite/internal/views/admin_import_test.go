package views

import (
	"context"
	"strings"
	"testing"
)

func renderAdminImportReport(t *testing.T, vm AdminImportReportViewModel) string {
	t.Helper()
	var sb strings.Builder
	if err := AdminImportReport(vm).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func TestAdminImportReportRendersRatingAsStars(t *testing.T) {
	body := renderAdminImportReport(t, AdminImportReportViewModel{
		Results: []ImportResult{
			{Source: "diary.csv", MovieName: "The Third Man", Watched: "2026-07-02", Rating: "4.5", Success: true},
		},
	})
	if !strings.Contains(body, "star-display") {
		t.Errorf("body does not contain star display: %s", body)
	}
	if !strings.Contains(body, "★★★★★") {
		t.Errorf("body does not contain star glyphs: %s", body)
	}
}
