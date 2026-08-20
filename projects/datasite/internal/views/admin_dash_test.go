package views

import (
	"context"
	"strings"
	"testing"
)

func renderAdminDash(t *testing.T, vm AdminDashViewModel) string {
	t.Helper()
	var sb strings.Builder
	if err := AdminDash(vm).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func TestAdminDashRendersForm(t *testing.T) {
	body := renderAdminDash(t, AdminDashViewModel{
		ServerSrc: "https://example.org/src",
	})
	if !strings.Contains(body, "SQL Query Executor") {
		t.Errorf("body does not contain SQL Query Executor heading: %s", body)
	}
	if !strings.Contains(body, "name=\"sql\"") {
		t.Errorf("body does not contain sql textarea: %s", body)
	}
	if !strings.Contains(body, "name=\"page\"") {
		t.Errorf("body does not contain page hidden input: %s", body)
	}
	if !strings.Contains(body, "name=\"page_size\"") {
		t.Errorf("body does not contain page_size input: %s", body)
	}
	if strings.Contains(body, "Error:") {
		t.Errorf("body should not contain error when no SQL submitted: %s", body)
	}
}

func TestAdminDashRendersError(t *testing.T) {
	body := renderAdminDash(t, AdminDashViewModel{
		ServerSrc: "https://example.org/src",
		SQL:       "SELECT invalid",
		Error:     "query failed: no such column: invalid",
	})
	if !strings.Contains(body, "Error:") {
		t.Errorf("body does not contain Error label: %s", body)
	}
	if !strings.Contains(body, "no such column") {
		t.Errorf("body does not contain error message: %s", body)
	}
}

func TestAdminDashRendersResults(t *testing.T) {
	body := renderAdminDash(t, AdminDashViewModel{
		ServerSrc:  "https://example.org/src",
		SQL:        "SELECT * FROM cinemas",
		Columns:    []string{"id", "name"},
		Rows:       [][]string{{"1", "Music Box"}, {"2", "Gene Siskel Film Center"}},
		Page:       1,
		PageSize:   50,
		TotalRows:  2,
		TotalPages: 1,
	})
	if !strings.Contains(body, "Results") {
		t.Errorf("body does not contain Results heading: %s", body)
	}
	if !strings.Contains(body, "<th>id</th>") {
		t.Errorf("body does not contain id column header: %s", body)
	}
	if !strings.Contains(body, "<th>name</th>") {
		t.Errorf("body does not contain name column header: %s", body)
	}
	if !strings.Contains(body, "Music Box") {
		t.Errorf("body does not contain Music Box row value: %s", body)
	}
	if !strings.Contains(body, "Gene Siskel Film Center") {
		t.Errorf("body does not contain row value: %s", body)
	}
	if !strings.Contains(body, "Page 1 of 1") {
		t.Errorf("body does not contain pagination info: %s", body)
	}
	if !strings.Contains(body, "2 total rows") {
		t.Errorf("body does not contain total row count: %s", body)
	}
}

func TestAdminDashRendersNoResults(t *testing.T) {
	body := renderAdminDash(t, AdminDashViewModel{
		ServerSrc:  "https://example.org/src",
		SQL:        "SELECT * FROM cinemas WHERE 1=0",
		Columns:    []string{"id", "name"},
		Rows:       [][]string{},
		Page:       1,
		PageSize:   50,
		TotalRows:  0,
		TotalPages: 0,
	})
	if !strings.Contains(body, "No results") {
		t.Errorf("body does not contain No results message: %s", body)
	}
}

func TestAdminDashPreFillsSQL(t *testing.T) {
	body := renderAdminDash(t, AdminDashViewModel{
		ServerSrc: "https://example.org/src",
		SQL:       "SELECT 1",
		Error:     "query failed",
	})
	if !strings.Contains(body, "SELECT 1") {
		t.Errorf("body does not contain pre-filled SQL: %s", body)
	}
}

func TestAdminDashPaginationLinks(t *testing.T) {
	body := renderAdminDash(t, AdminDashViewModel{
		ServerSrc:  "https://example.org/src",
		SQL:        "SELECT * FROM cinemas",
		Columns:    []string{"id", "name"},
		Rows:       [][]string{{"1", "Music Box"}},
		Page:       1,
		PageSize:   50,
		TotalRows:  500,
		TotalPages: 10,
	})
	if !strings.Contains(body, "aria-label=\"Page navigation\"") {
		t.Errorf("body does not contain pagination nav: %s", body)
	}
	if !strings.Contains(body, "page=1") {
		t.Errorf("body does not contain first page link: %s", body)
	}
	if !strings.Contains(body, "page=10") {
		t.Errorf("body does not contain last page link: %s", body)
	}
	if !strings.Contains(body, "page=2") {
		t.Errorf("body does not contain next page link: %s", body)
	}
}