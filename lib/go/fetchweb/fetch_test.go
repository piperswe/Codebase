package fetchweb

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func response(r *http.Request, status int, media string, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}
}

func TestConcurrentFetchAndDocumentLookup(t *testing.T) {
	calls := 0
	f := New(Config{MaxFetches: 1, UserAgent: "test-agent", HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Error("configured user agent ignored")
		}
		return response(r, 200, "text/html", []byte("<h1>Menu</h1>")), nil
	})}})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			d, err := f.Fetch(context.Background(), "https://example.com/menu")
			if err != nil {
				t.Error(err)
				return
			}
			if f.Document(d.Source.ID) != d {
				t.Error("document lookup did not return cached source")
			}
		})
	}
	wg.Wait()
	if calls != 1 {
		t.Errorf("repeated URL made %d requests", calls)
	}
	if f.Document("unknown") != nil {
		t.Error("unknown source returned a document")
	}
	if _, err := f.Fetch(context.Background(), "https://example.com/other"); err == nil {
		t.Fatal("configured fetch limit ignored")
	}
}

func TestRetryRejectsNonReplayableBody(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Error("non-replayable request was sent")
		return response(r, 200, "text/plain", nil), nil
	})}
	req, err := http.NewRequest("POST", "https://example.com/", io.NopCloser(strings.NewReader("body")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DoWithRetry(context.Background(), client, req); err == nil {
		t.Fatal("expected GetBody error")
	}
}

func pngFixture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.White)
		}
	}
	img.Set(8, 8, color.Black)
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestHTMLDiscoveryAndCache(t *testing.T) {
	var calls int
	data := base64.StdEncoding.EncodeToString(pngFixture(t))
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, 200, "text/html", []byte(`<base href="https://cdn.example/menus/"><h1>Dinner</h1><table><tr><td>Soup</td><td>8.50</td></tr></table><script>ignore_me()</script><script type="application/ld+json">{"@type":"Menu","name":"Lunch"}</script><a href="dinner.pdf">Dinner PDF</a><object data="lunch.pdf"></object><embed src="dessert.pdf"><iframe src="drinks.pdf"></iframe><img data-src="menu.png" srcset="small.png 1x, large.png 2x"><img src="data:image/png;base64,`+data+`">`)), nil
	})}
	f := New(Config{HTTPClient: client})
	d, err := f.Fetch(context.Background(), "https://restaurant.example/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Text, "Soup") || !strings.Contains(d.Text, "JSON-LD") || strings.Contains(d.Text, "ignore_me") {
		t.Fatalf("HTML text: %s", d.Text)
	}
	if len(d.Links) != 8 {
		t.Fatalf("links: %+v", d.Links)
	}
	for _, link := range d.Links {
		if link.Kind != "image" && !strings.HasPrefix(link.URL, "https://cdn.example/menus/") {
			t.Errorf("relative URL not resolved: %s", link.URL)
		}
	}
	if _, err := f.Fetch(context.Background(), "https://restaurant.example/#fragment"); err != nil {
		t.Fatal(err)
	}
	embedded, err := f.Fetch(context.Background(), d.Links[len(d.Links)-1].URL)
	if err != nil {
		t.Fatal(err)
	}
	if embedded.Source.MediaType != "image/png" || calls != 1 {
		t.Errorf("embedded/cache: %s, %d fetches", embedded.Source.MediaType, calls)
	}
}

func TestFetchFailuresAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		media  string
		body   string
	}{
		{"http", 403, "text/plain", "denied"}, {"type", 200, "application/zip", "PK archive"}, {"size", 200, "text/html", strings.Repeat("x", (2<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			f := New(Config{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return response(r, tc.status, tc.media, []byte(tc.body)), nil
			})}})
			for range 2 {
				if _, err := f.Fetch(context.Background(), "https://restaurant.example/menu"); err == nil {
					t.Fatal("expected failure")
				}
			}
			if calls != 1 {
				t.Errorf("failed URL fetched %d times", calls)
			}
			f.maxFetches = 1
			if _, err := f.Fetch(context.Background(), "https://restaurant.example/other"); err == nil {
				t.Fatal("fetch limit ignored")
			}
		})
	}
}

func TestPublicURLPolicy(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://127.0.0.1/", "http://[::1]/", "http://169.254.169.254/", "http://10.0.0.1/", "https://user:password@example.com/", "https://example.com:22/"} {
		if _, err := ParseURL(raw); err == nil {
			t.Errorf("allowed %s", raw)
		}
	}
	for _, ip := range []string{"127.0.0.1", "::ffff:127.0.0.1", "169.254.169.254", "100.64.0.1", "192.168.1.1", "fc00::1", "64:ff9b::7f00:1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("allowed IP %s", ip)
		}
	}
	if !publicAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IP blocked")
	}
	client := publicHTTPClient()
	req, _ := http.NewRequest("GET", "http://127.0.0.1/", nil)
	if err := client.CheckRedirect(req, []*http.Request{{}}); err == nil {
		t.Fatal("private redirect allowed")
	}
}

func TestRetryCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		cancel()
		return response(r, 429, "text/plain", nil), nil
	})}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.com", nil)
	if _, err := DoWithRetry(ctx, client, req); err == nil {
		t.Fatal("cancellation ignored")
	}
	if calls != 1 {
		t.Fatal("retried after cancellation")
	}
}
