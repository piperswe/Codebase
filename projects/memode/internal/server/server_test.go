package server

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/oauthstore"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	reg := &codemode.Registry{}
	reg.Register(codemode.Function{Namespace: "mail", Name: "search", Desc: "search mail", Fn: func(context.Context, map[string]any) (any, error) {
		return []any{"hit1", "hit2"}, nil
	}})
	reg.Register(codemode.Function{Namespace: "mail", Name: "send", Desc: "send mail", Write: true, Fn: func(_ context.Context, args map[string]any) (any, error) {
		return "sent:" + args["to"].(string), nil
	}})
	cfg := config.Config{}
	cfg.ICloudUser, cfg.ICloudPassword = "me@example.com", "app-password" // mail configured
	return New(cfg, reg, nil)
}

func callTool(t *testing.T, s *Server, ctx context.Context, name, script string) (*mcp.CallToolResult, error) {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := s.mcp.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: map[string]any{"script": script},
	})
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("expected one content block, got %d", len(res.Content))
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func TestReadWriteTools(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()

	res, err := callTool(t, s, ctx, "memode.read", `return mail.search("hello")`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := resultText(t, res); got != `["hit1","hit2"]` {
		t.Fatalf("unexpected result: %q", got)
	}

	// The write tool can read and write.
	res, err = callTool(t, s, ctx, "memode.write", `return mail.send({to = "x@y.z"})`)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := resultText(t, res); got != "sent:x@y.z" {
		t.Fatalf("unexpected result: %q", got)
	}

	// The read tool must not expose write functions at all.
	res, err = callTool(t, s, ctx, "memode.read", `return mail.send({to = "x@y.z"})`)
	if err != nil {
		t.Fatalf("read call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error calling write fn through memode.read")
	}

	// Status works in both tools.
	for _, tool := range []string{"memode.read", "memode.write"} {
		res, err := callTool(t, s, ctx, tool, `return status()`)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if got := resultText(t, res); !strings.Contains(got, `"mail"`) || !strings.Contains(got, "configured") {
			t.Fatalf("status output unexpected: %s", got)
		}
	}
}

func TestGrantGating(t *testing.T) {
	s := testServer(t)
	// Client granted only mail:read.
	ctx := oauthstore.WithGrants(context.Background(), oauthstore.Grants{"mail": "read"})

	res, err := callTool(t, s, ctx, "memode.read", `return mail.search("hi")`)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resultText(t, res) != `["hit1","hit2"]` {
		t.Fatalf("read fn should be allowed")
	}

	// Even memode.write cannot call write fns without a write grant.
	res, err = callTool(t, s, ctx, "memode.write", `return mail.send({to = "x@y.z"})`)
	if err != nil {
		t.Fatalf("write call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error: write requires mail:write grant")
	}

	// A service with no grant is invisible entirely.
	ctx = oauthstore.WithGrants(context.Background(), oauthstore.Grants{"calendar": "write"})
	res, err = callTool(t, s, ctx, "memode.write", `return mail.search("hi")`)
	if err != nil {
		t.Fatalf("write call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error: mail has no grant")
	}
}

func TestScopesRoundTrip(t *testing.T) {
	g := oauthstore.Grants{"mail": "write", "contacts": "read"}
	got := oauthstore.GrantsFromScopes(g.Scopes())
	if got["mail"] != "write" || got["contacts"] != "read" || got["calendar"] != "" {
		t.Fatalf("round trip mismatch: %#v", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || index(s, sub) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
