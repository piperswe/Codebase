// Package kagi exposes Kagi web search to scripts by wrapping lib/go/kagi.
package kagi

import (
	"context"
	"fmt"
	"strings"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/lib/go/kagi"
	"codebase.bid/projects/memode/internal/config"
)

// serviceSearches is the per-process search budget. lib/go/kagi bounds searches
// per client to keep a runaway agent from draining the quota; memode is a
// long-lived server, so the bound is set generously and the client's own
// result cache still absorbs repeated queries.
const serviceSearches = 500

// Service is the Kagi search service.
type Service struct {
	cfg    config.Config
	client *kagi.Client
}

// New creates the Kagi service. No network call happens here.
func New(cfg config.Config) *Service {
	return &Service{
		cfg: cfg,
		client: kagi.New(kagi.Config{
			APIKey:      cfg.KagiAPIKey,
			Endpoint:    cfg.KagiEndpoint(),
			MaxSearches: serviceSearches,
		}),
	}
}

// Setup reports readiness; the API key is validated by the first search.
func (s *Service) Setup(_ context.Context, ready func(bool)) {
	ready(s.cfg.KagiAPIKey != "")
}

// Register adds the Lua API.
func (s *Service) Register(reg *codemode.Registry) {
	reg.Register(codemode.Function{
		Namespace: "kagi", Name: "search",
		Desc: "search the public web. args: {query, limit?}. returns up to ten results with url, title, snippet",
		Fn:   s.fnSearch,
	})
}

func (s *Service) fnSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	results, err := s.client.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	if limit, ok := args["limit"].(float64); ok && int(limit) > 0 && int(limit) < len(results) {
		results = results[:int(limit)]
	}
	if results == nil {
		results = []kagi.SearchResult{}
	}
	return results, nil
}
