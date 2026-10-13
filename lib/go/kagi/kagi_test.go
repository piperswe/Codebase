package kagi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(r *http.Request, status int, media string, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}
}

func TestConcurrentSearchCacheAndResultOwnership(t *testing.T) {
	calls := 0
	client := New(Config{APIKey: "test", MaxSearches: 1, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://kagi.com/api/v1/search" || r.Header.Get("Authorization") != "Bearer test" {
			t.Errorf("unexpected default endpoint or authentication: %s", r.URL)
		}
		return response(r, 200, "application/json", []byte(`{"data":{"search":[{"url":"https://example.com/","title":"Original"}]}}`)), nil
	})}})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			results, err := client.Search(context.Background(), "restaurant menu")
			if err != nil {
				t.Error(err)
				return
			}
			if len(results) != 1 || results[0].Title != "Original" {
				t.Errorf("cache was modified: %v", results)
				return
			}
			results[0].Title = "Caller mutation"
		})
	}
	wg.Wait()
	if calls != 1 {
		t.Errorf("repeated query made %d requests", calls)
	}
	if _, err := client.Search(context.Background(), "another restaurant"); err == nil {
		t.Fatal("configured search limit ignored")
	}
}

func TestKagiFailuresAndBudget(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{401, `{}`}, {200, `not json`}, {200, `{"error":{"message":"bad"}}`}, {200, `{"data":null}`}} {
		k := New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return response(r, tc.status, "application/json", []byte(tc.body)), nil
		})}, Endpoint: "https://kagi.example/search"})
		if _, err := k.Search(context.Background(), "food"); err == nil {
			t.Errorf("accepted %d %s", tc.status, tc.body)
		}
	}
	calls := 0
	k := New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, 200, "application/json", []byte(`{"data":{"search":[]}}`)), nil
	})}, Endpoint: "https://kagi.example/search"})
	for i := range 6 {
		query := fmt.Sprint(i)
		if _, err := k.Search(context.Background(), query); err != nil {
			t.Fatal(err)
		}
		if _, err := k.Search(context.Background(), query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := k.Search(context.Background(), "seventh"); err == nil {
		t.Fatal("budget ignored")
	}
	if calls != 6 {
		t.Errorf("cache ignored: %d", calls)
	}
}
