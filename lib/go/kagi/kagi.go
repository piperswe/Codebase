// Package kagi provides a bounded, caching client for the Kagi Search API.
package kagi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codebase.bid/lib/go/fetchweb"
)

// SearchResult is a web result returned by Kagi.
type SearchResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Client caches searches for one bounded session. Construct one with New.
// Its methods are safe for concurrent use.
type Client struct {
	mu          sync.Mutex
	maxSearches int
	client      *http.Client
	endpoint    string
	apiKey      string
	queries     map[string][]SearchResult
	calls       int
}

// Config configures a search session. Empty Endpoint uses Kagi's v1 Search API.
// A nil HTTPClient uses a 30-second timeout and rejects redirects, keeping API
// credentials on the configured endpoint. Custom clients must enforce their own
// redirect and credential policy. MaxSearches defaults to six when nonpositive.
type Config struct {
	APIKey      string
	HTTPClient  *http.Client
	Endpoint    string
	MaxSearches int
}

// New creates a client without making network requests. APIKey is required by Kagi.
func New(config Config) *Client {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = "https://kagi.com/api/v1/search"
	}
	maxSearches := config.MaxSearches
	if maxSearches <= 0 {
		maxSearches = 6
	}
	return &Client{client: client, endpoint: endpoint, apiKey: config.APIKey, maxSearches: maxSearches, queries: map[string][]SearchResult{}}
}

// Search returns up to ten public web results. Repeated queries use the session
// cache without consuming the search budget. Results are independent copies.
func (k *Client) Search(ctx context.Context, query string) ([]SearchResult, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 1000 {
		return nil, fmt.Errorf("search query must contain 1-1000 bytes")
	}
	if results, ok := k.queries[query]; ok {
		return append([]SearchResult{}, results...), nil
	}
	if k.calls >= k.maxSearches {
		return nil, fmt.Errorf("search limit reached (%d)", k.maxSearches)
	}
	k.calls++
	body, _ := json.Marshal(map[string]any{"query": query, "workflow": "search", "format": "json", "limit": 10})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+k.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := fetchweb.DoWithRetry(ctx, k.client, req)
	if err != nil {
		return nil, fmt.Errorf("Kagi search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Kagi search returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 2<<20 {
		return nil, fmt.Errorf("Kagi response exceeds 2 MiB")
	}
	var envelope struct {
		Data *struct {
			Search []SearchResult `json:"search"`
		} `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("invalid Kagi JSON: %w", err)
	}
	if envelope.Data == nil || (len(envelope.Error) > 0 && string(envelope.Error) != "null") {
		return nil, fmt.Errorf("Kagi response contains an error or no data")
	}
	results := []SearchResult{}
	for _, result := range envelope.Data.Search {
		if _, err := fetchweb.ParseURL(result.URL); err != nil {
			continue
		}
		if len(results) == 10 {
			break
		}
		result.Title = clip(result.Title, 500)
		result.Snippet = clip(result.Snippet, 3000)
		results = append(results, result)
	}
	if k.queries == nil {
		k.queries = map[string][]SearchResult{}
	}
	k.queries[query] = results
	return append([]SearchResult{}, results...), nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
