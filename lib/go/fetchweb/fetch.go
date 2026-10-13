// Package fetchweb fetches and inspects public web documents without executing JavaScript.
package fetchweb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const maxText = 100_000

// Source records the origin and retrieval time of a document.
type Source struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	MediaType   string `json:"media_type"`
	RetrievedAt string `json:"retrieved_at"`
}

// Link identifies an asset or page discovered in an HTML document.
type Link struct {
	URL   string `json:"url"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// Document contains source metadata, readable text, discovered links, and raw bytes.
// Raw bytes are excluded from JSON so media is passed explicitly to consumers.
type Document struct {
	Source   Source   `json:"source"`
	Text     string   `json:"text"`
	Links    []Link   `json:"links"`
	Warnings []string `json:"warnings"`
	Data     []byte   `json:"-"`
}

// Fetcher caches documents and failures for one bounded fetching session.
// Construct one with New. Its methods are safe for concurrent use; returned
// documents are shared, read-only values and must not be modified by callers.
type Fetcher struct {
	mu         sync.Mutex
	userAgent  string
	client     *http.Client
	documents  map[string]*Document
	byURL      map[string]*Document
	failures   map[string]error
	attempts   int
	maxFetches int
}

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96", "64:ff9b:1::/48", "2002::/16"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}

// ParseURL validates the URL syntax, web port, and literal IP, and strips fragments.
// DNS addresses must still be checked when dialing, as the default fetcher does.
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u == nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("expected a public HTTP(S) URL")
	}
	if port := u.Port(); port != "" && port != "80" && port != "443" {
		return nil, fmt.Errorf("only web ports 80 and 443 are allowed")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicAddress(ip) {
		return nil, fmt.Errorf("non-public address is not allowed")
	}
	u.Fragment = ""
	return u, nil
}

func publicHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A proxy could bypass the destination checks in DialContext.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("host has no addresses")
		}
		for _, ip := range addresses {
			if !publicAddress(ip) {
				return nil, fmt.Errorf("host resolves to a non-public address")
			}
		}
		var last error
		for _, ip := range addresses {
			conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		_, err := ParseURL(req.URL.String())
		return err
	}}
}

// Config configures a fetching session. Zero limits use the documented defaults.
// HTTPClient, when supplied, replaces the public-network client entirely; the
// caller is responsible for its DNS, redirect, timeout, and credential policy.
type Config struct {
	HTTPClient *http.Client
	MaxFetches int
	UserAgent  string
}

// New creates a fetching session with a default limit of 30 URL fetch attempts.
// It uses a public-network-only HTTP client unless Config.HTTPClient is supplied.
func New(config Config) *Fetcher {
	client := config.HTTPClient
	if client == nil {
		client = publicHTTPClient()
	}
	maxFetches := config.MaxFetches
	if maxFetches <= 0 {
		maxFetches = 30
	}
	userAgent := config.UserAgent
	if userAgent == "" {
		userAgent = "FetchWeb/1.0"
	}
	return &Fetcher{client: client, userAgent: userAgent, documents: map[string]*Document{}, byURL: map[string]*Document{}, failures: map[string]error{}, maxFetches: maxFetches}
}

// Document returns a previously fetched document by its source ID, or nil.
// The returned document belongs to the fetcher and must be treated as read-only.
func (f *Fetcher) Document(id string) *Document {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.documents[id]
}

// Fetch downloads and inspects a public URL, reusing cached documents and failures.
// The returned document belongs to the fetcher and must be treated as read-only.
func (f *Fetcher) Fetch(ctx context.Context, raw string) (*Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Embedded data images have synthetic, parent-relative source URLs.
	if d := f.byURL[raw]; d != nil {
		return d, nil
	}
	u, err := ParseURL(raw)
	if err != nil {
		return nil, err
	}
	key := u.String()
	if d := f.byURL[key]; d != nil {
		return d, nil
	}
	if err := f.failures[key]; err != nil {
		return nil, err
	}
	if f.attempts >= f.maxFetches {
		return nil, fmt.Errorf("fetch limit reached (%d)", f.maxFetches)
	}
	if len(f.documents) >= 60 {
		return nil, fmt.Errorf("document limit reached (60)")
	}
	f.attempts++
	d, err := f.download(ctx, u)
	if err != nil {
		f.failures[key] = err
		return nil, err
	}
	if cached := f.byURL[d.Source.URL]; cached != nil {
		f.byURL[key] = cached
		return cached, nil
	}
	d.Source.ID = fmt.Sprintf("s%d", len(f.documents)+1)
	f.documents[d.Source.ID] = d
	f.byURL[key], f.byURL[d.Source.URL] = d, d
	if d.Source.MediaType == "text/html" {
		if err := f.parseHTML(d); err != nil {
			d.Warnings = append(d.Warnings, "HTML parsing failed: "+err.Error())
		}
	}
	return d, nil
}

func (f *Fetcher) download(ctx context.Context, u *url.URL) (*Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/pdf,image/png,image/jpeg,image/webp,image/gif;q=0.9,*/*;q=0.1")
	resp, err := DoWithRetry(ctx, f.client, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	prefix := make([]byte, 512)
	n, err := io.ReadFull(resp.Body, prefix)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	prefix = prefix[:n]
	declared, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	detected := http.DetectContentType(prefix)
	mediaType := declared
	if strings.HasPrefix(detected, "image/") || detected == "application/pdf" {
		mediaType = detected
	}
	if mediaType == "" || mediaType == "application/octet-stream" {
		mediaType, _, _ = mime.ParseMediaType(detected)
	}
	var limit int64
	switch mediaType {
	case "text/html", "application/xhtml+xml":
		mediaType = "text/html"
		limit = 2 << 20
	case "text/plain":
		limit = 2 << 20
	case "application/pdf":
		limit = 20 << 20
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		limit = 10 << 20
	default:
		return nil, fmt.Errorf("unsupported content type %q", mediaType)
	}
	data, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(prefix), resp.Body), limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d byte limit", mediaType, limit)
	}
	d := &Document{Source: Source{URL: resp.Request.URL.String(), MediaType: mediaType, RetrievedAt: time.Now().UTC().Format(time.RFC3339)}, Data: data, Links: []Link{}, Warnings: []string{}}
	if mediaType == "text/html" || mediaType == "text/plain" {
		reader, err := charset.NewReader(bytes.NewReader(data), resp.Header.Get("Content-Type"))
		if err != nil {
			return nil, err
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, 4<<20))
		if err != nil {
			return nil, err
		}
		d.Data = decoded
		if mediaType == "text/plain" {
			d.Text = clipped(string(decoded), maxText, &d.Warnings)
		}
	}
	return d, nil
}

func clipped(s string, n int, warnings *[]string) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	*warnings = append(*warnings, "Source text or links were truncated; extraction may be incomplete.")
	return s[:n]
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func (f *Fetcher) parseHTML(d *Document) error {
	root, err := html.Parse(bytes.NewReader(d.Data))
	if err != nil {
		return err
	}
	base, _ := url.Parse(d.Source.URL)
	var findBase func(*html.Node)
	foundBase := false
	findBase = func(n *html.Node) {
		if !foundBase && n.Type == html.ElementNode && n.Data == "base" {
			if ref, err := url.Parse(attr(n, "href")); err == nil {
				if u, err := ParseURL(base.ResolveReference(ref).String()); err == nil {
					base = u
					foundBase = true
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBase(c)
		}
	}
	findBase(root)
	seen := map[string]bool{}
	linksTruncated := false
	add := func(raw, kind, label string) {
		if raw == "" {
			return
		}
		var resolved string
		if strings.HasPrefix(raw, "data:image/") {
			parts := strings.SplitN(raw, ",", 2)
			if len(parts) != 2 || !strings.HasSuffix(parts[0], ";base64") || len(parts[1]) > 3<<20 {
				return
			}
			data, err := base64.StdEncoding.DecodeString(parts[1])
			if err != nil {
				return
			}
			mt := http.DetectContentType(data)
			if mt != "image/png" && mt != "image/jpeg" && mt != "image/webp" && mt != "image/gif" {
				return
			}
			hash := sha256.Sum256(data)
			resolved = fmt.Sprintf("%s#embedded-%x", d.Source.URL, hash[:8])
			if f.byURL[resolved] == nil {
				if len(f.documents) >= 60 {
					return
				}
				embedded := &Document{Source: Source{ID: fmt.Sprintf("s%d", len(f.documents)+1), URL: resolved, MediaType: mt, RetrievedAt: d.Source.RetrievedAt}, Data: data, Links: []Link{}, Warnings: []string{}}
				f.documents[embedded.Source.ID], f.byURL[resolved] = embedded, embedded
			}
		} else {
			ref, err := url.Parse(strings.TrimSpace(raw))
			if err != nil {
				return
			}
			u, err := ParseURL(base.ResolveReference(ref).String())
			if err != nil {
				return
			}
			resolved = u.String()
		}
		if seen[resolved] {
			return
		}
		seen[resolved] = true
		if len(d.Links) >= 200 {
			if !linksTruncated {
				d.Warnings = append(d.Warnings, "More than 200 source links; additional links omitted.")
				linksTruncated = true
			}
			return
		}
		if len(label) > 200 {
			label = string([]rune(label)[:min(100, len([]rune(label)))])
		}
		d.Links = append(d.Links, Link{URL: resolved, Kind: kind, Label: label})
	}
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script":
				if attr(n, "type") == "application/ld+json" {
					text.WriteString("\nJSON-LD: " + nodeText(n) + "\n")
				}
				return
			case "style":
				return
			case "a":
				add(attr(n, "href"), "link", nodeText(n))
			case "iframe", "embed":
				add(attr(n, "src"), "embed", attr(n, "title"))
			case "object":
				add(attr(n, "data"), "embed", attr(n, "title"))
			case "img", "source":
				for _, key := range []string{"src", "data-src", "data-original", "data-lazy-src"} {
					add(attr(n, key), "image", attr(n, "alt"))
				}
				for _, key := range []string{"srcset", "data-srcset"} {
					for _, candidate := range strings.Split(attr(n, key), ",") {
						fields := strings.Fields(candidate)
						if len(fields) > 0 {
							add(fields[0], "image", attr(n, "alt"))
						}
					}
				}
			}
			if strings.Contains("|p|div|section|article|li|tr|h1|h2|h3|h4|br|", "|"+n.Data+"|") {
				text.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			text.WriteString(strings.Join(strings.Fields(n.Data), " "))
			text.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && n.Data == "td" {
			text.WriteString(" | ")
		}
	}
	walk(root)
	d.Text = clipped(text.String(), maxText, &d.Warnings)
	return nil
}
