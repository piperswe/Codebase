package kagi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
)

func TestSearchThroughLua(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization header: %q", got)
		}
		var req struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"search": []map[string]string{
				{"url": "https://example.com/1", "title": "T1 " + req.Query, "snippet": "S1"},
				{"url": "https://example.com/2", "title": "T2", "snippet": "S2"},
			}},
		})
	}))
	defer srv.Close()
	t.Setenv("MEMODE_KAGI_ENDPOINT", srv.URL)

	cfg := config.Config{KagiAPIKey: "test-key"}
	svc := New(cfg) // picks up MEMODE_KAGI_ENDPOINT

	ready := false
	svc.Setup(context.Background(), func(ok bool) { ready = ok })
	if !ready {
		t.Fatal("service should be ready with an API key")
	}

	reg := &codemode.Registry{}
	svc.Register(reg)

	res, err := codemode.Run(context.Background(), reg, codemode.AllowAll, `
		local r = kagi.search({query = "kumquat"})
		return #r .. "|" .. r[1].title .. "|" .. r[1].url
	`, codemode.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := res.Items[0]; got != "2|T1 kumquat|https://example.com/1" {
		t.Fatalf("unexpected result: %q", got)
	}

	// Limit is honored.
	res, err = codemode.Run(context.Background(), reg, codemode.AllowAll, `return #kagi.search({query = "kumquat", limit = 1})`, codemode.RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Items[0] != "1" {
		t.Fatalf("limit ignored: %s", res.Items[0])
	}
	if hits != 1 {
		t.Fatalf("expected one upstream call (second search was cached), got %d", hits)
	}

	// A blank query is rejected.
	if _, err := codemode.Run(context.Background(), reg, codemode.AllowAll, `kagi.search({query = "   "})`, codemode.RunOptions{}); err == nil {
		t.Fatal("blank query should error")
	}
}

func TestSearchIsReadOnly(t *testing.T) {
	reg := &codemode.Registry{}
	New(config.Config{KagiAPIKey: "k"}).Register(reg)
	res, err := codemode.Run(context.Background(), reg, codemode.AllowReadOnly, `return type(kagi.search)`, codemode.RunOptions{})
	if err != nil || res.Items[0] != "function" {
		t.Fatalf("search must be available read-only: %v %v", res.Items, err)
	}
	fns := reg.Functions()
	if len(fns) != 1 || fns[0].Write {
		t.Fatalf("kagi must expose exactly one non-write function: %#v", fns)
	}
}
