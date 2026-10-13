// Package server hosts the MeMode MCP server: two code-mode tools over
// streamable HTTP, later gated by OAuth grants.
package server

import (
	"context"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/lib/go/oauthas"
	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/oauthstore"
	"codebase.bid/projects/memode/internal/version"
)

// Server owns the MCP server, the codemode registry and service health.
type Server struct {
	cfg        config.Config
	reg        *codemode.Registry
	mcp        *mcp.Server
	oauth      *oauthas.Server
	oauthStore *oauthstore.Store
	*Health
}

// Health tracks per-service readiness, updated by service Setup.
type Health struct {
	mu    sync.Mutex
	ready map[string]bool
}

func (h *Health) SetReady(service string, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready[service] = ok
}

func (h *Health) Snapshot() map[string]bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]bool, len(h.ready))
	for k, v := range h.ready {
		out[k] = v
	}
	return out
}

// New builds the MCP server and registers the two code-mode tools plus the
// status function. reg must already contain all service functions. store
// enables OAuth (authorization server + token-gated MCP); nil keeps local
// trusted mode.
func New(cfg config.Config, reg *codemode.Registry, store *oauthstore.Store) *Server {
	s := &Server{
		cfg:    cfg,
		reg:    reg,
		Health: &Health{ready: map[string]bool{}},
	}

	reg.Register(codemode.Function{
		Name:  "status",
		Desc:  "report memode version and per-service configured/ready state",
		Write: false,
		Fn: func(_ context.Context, _ map[string]any) (any, error) {
			configured := s.cfg.ServiceConfigured()
			ready := s.Health.Snapshot()
			services := map[string]any{}
			for name, isConfigured := range configured {
				services[name] = map[string]any{
					"configured": isConfigured,
					"ready":      isConfigured && ready[name],
				}
			}
			return map[string]any{"version": version.Version, "services": services}, nil
		},
	})

	if store != nil {
		if err := s.initOAuth(store); err != nil {
			panic(err) // key generation from store: a broken DB is unrecoverable at startup
		}
	}

	ms := mcp.NewServer(&mcp.Implementation{Name: "memode", Version: version.Version}, nil)
	s.addTool(ms, false) // memode.read
	s.addTool(ms, true)  // memode.write
	s.mcp = ms
	return s
}

// MCPHandler returns the streamable-HTTP handler serving the MCP protocol.
func (s *Server) MCPHandler() http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, nil)
}
