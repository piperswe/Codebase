package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/davtest"
	"codebase.bid/projects/memode/internal/services"
)

// TestEndToEndServicesOverMCP is the full stack without real upstream
// accounts: config -> services.Enabled -> registry -> OAuth consent and
// token -> MCP over HTTP -> Lua -> live DAV and search servers. It also
// verifies that scope gating holds at the outermost layer.
func TestEndToEndServicesOverMCP(t *testing.T) {
	ctx := context.Background()

	calServer := davtest.CalServer()
	cardServer := davtest.CardServer()
	t.Cleanup(calServer.Close)
	t.Cleanup(cardServer.Close)

	var searchCalls int
	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"search": []map[string]string{
				{"url": "https://example.com/citrus", "title": "Citrus season", "snippet": "Kumquats are in season."},
			}},
		})
	}))
	t.Cleanup(searchServer.Close)

	// Configure the three services; mail needs an IMAP account, so disable it.
	t.Setenv("MEMODE_CALDAV_ENDPOINT", calServer.URL+"/")
	t.Setenv("MEMODE_CARDDAV_ENDPOINT", cardServer.URL+"/")
	t.Setenv("MEMODE_KAGI_ENDPOINT", searchServer.URL)
	cfg := config.Config{
		ICloudUser:     "owner@example.com",
		ICloudPassword: "app-password",
		KagiAPIKey:     "kagi-key",
		Disabled:       map[string]bool{"mail": true},
	}

	// Real service wiring: the same code paths main.go uses.
	reg := &codemode.Registry{}
	env := newOAuthEnvWith(t, cfg, reg)
	svcs := services.Enabled(cfg, nil) // mail is disabled, so no database is needed
	if len(svcs) != 3 {
		t.Fatalf("expected calendar, contacts and search services, got %d", len(svcs))
	}
	services.SetupAll(ctx, svcs, reg, env.srv.Health.SetReady)

	// Consent to calendar:write, contacts:write and search:read.
	token := env.runOAuth(t, map[string]string{"calendar": "write", "contacts": "write", "search": "read"})
	cs := env.mcpClient(t, token)

	// memode.write: create an event, add a contact, search the web - one script.
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memode.write",
		Arguments: map[string]any{"script": `
			local ev = calendar.upsert_event({summary = "Kumquat tasting", start = "2026-11-03T09:00:00Z", end_time = "2026-11-03T11:00:00Z"})
			local ct = contacts.upsert({full_name = "Alice Appleseed", email = "alice@example.com"})
			local hits = kagi.search({query = "kumquat season"})
			return {event = ev.path, contact = ct.path, hits = #hits}
		`},
	})
	if err != nil {
		t.Fatalf("memode.write: %v", err)
	}
	if res.IsError {
		t.Fatalf("memode.write failed: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	t.Logf("write result: %s", text)
	if searchCalls != 1 {
		t.Fatalf("expected one upstream search call, got %d", searchCalls)
	}

	// memode.read sees both writes.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memode.read",
		Arguments: map[string]any{"script": `
			local evs = calendar.events({from = "2026-11-03T00:00:00Z", to = "2026-11-04T00:00:00Z"})
			local cts = contacts.list({})
			return {events = #evs, contacts = #cts, first_event = evs[1].summary, first_contact = cts[1].full_name}
		`},
	})
	if err != nil {
		t.Fatalf("memode.read: %v", err)
	}
	if res.IsError {
		t.Fatalf("memode.read failed: %s", res.Content[0].(*mcp.TextContent).Text)
	}
	text = res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{`"events":1`, `"contacts":1`, "Kumquat tasting", "Alice Appleseed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("read result missing %q: %s", want, text)
		}
	}

	// The read tool cannot mutate, even with write grants.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "memode.read",
		Arguments: map[string]any{"script": `return calendar.delete_event({path = "/me/calendars/home/x.ics"})`},
	})
	if err != nil {
		t.Fatalf("memode.read: %v", err)
	}
	if !res.IsError {
		t.Fatal("memode.read must not expose write functions")
	}

	// A second client without the contacts grant cannot use contacts at all.
	token = env.runOAuth(t, map[string]string{"calendar": "read"})
	cs2 := env.mcpClient(t, token)
	res, err = cs2.CallTool(ctx, &mcp.CallToolParams{
		Name:      "memode.write",
		Arguments: map[string]any{"script": `return contacts.list({})`},
	})
	if err != nil {
		t.Fatalf("memode.write: %v", err)
	}
	if !res.IsError {
		t.Fatal("contacts must be unavailable without the grant")
	}
}
