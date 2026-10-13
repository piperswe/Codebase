package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"codebase.bid/lib/go/codemode"
	"codebase.bid/projects/memode/internal/config"
	"codebase.bid/projects/memode/internal/oauthstore"
	"codebase.bid/projects/memode/internal/store"
)

// oauthTestEnv spins up the full HTTP server: OAuth endpoints plus MCP.
type oauthTestEnv struct {
	ts    *httptest.Server
	base  string
	reg   *codemode.Registry
	db    *oauthstore.Store
	srv   *Server
	close func()
}

func newOAuthTestEnv(t *testing.T) *oauthTestEnv {
	t.Helper()
	reg := &codemode.Registry{}
	reg.Register(codemode.Function{Namespace: "mail", Name: "search", Desc: "search mail", Fn: func(context.Context, map[string]any) (any, error) {
		return []any{"hit1"}, nil
	}})
	reg.Register(codemode.Function{Namespace: "mail", Name: "send", Desc: "send mail", Write: true, Fn: func(_ context.Context, a map[string]any) (any, error) {
		return "sent:" + a["to"].(string), nil
	}})
	reg.Register(codemode.Function{Namespace: "kagi", Name: "search", Desc: "web search", Fn: func(context.Context, map[string]any) (any, error) {
		return []any{"kagi-hit"}, nil
	}})

	cfg := config.Config{}
	cfg.ICloudUser, cfg.ICloudPassword = "me@example.com", "pw" // mail configured
	cfg.KagiAPIKey = "kagi-key"                                 // search configured
	return newOAuthEnvWith(t, cfg, reg)
}

// newOAuthEnvWith starts the full HTTP server (OAuth + MCP) for the given
// configuration and registry, with one statically registered client.
func newOAuthEnvWith(t *testing.T, cfg config.Config, reg *codemode.Registry) *oauthTestEnv {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ostore := oauthstore.New(db)
	if err := ostore.EnsureClients(ctx, []oauthstore.ClientConfig{{
		ClientID: "harness", Secret: "hsecret", Name: "Test harness",
		RedirectURIs: []string{"http://127.0.0.1:9/cb"},
	}}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewUnstartedServer(nil)
	cfg.BaseURL = "http://" + ts.Listener.Addr().String()
	srv := New(cfg, reg, ostore)
	ts.Config = &http.Server{Handler: srv.HTTPHandler()}
	ts.Start()
	t.Cleanup(func() {
		ts.Close()
		db.Close()
	})
	return &oauthTestEnv{ts: ts, base: ts.URL, reg: reg, db: ostore, srv: srv}
}

// runOAuth performs the full dance with a scripted client and returns the
// access token.
func (e *oauthTestEnv) runOAuth(t *testing.T, services map[string]string) string {
	t.Helper()
	verifier := "test-verifier-that-is-long-enough-abcdef1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	q := url.Values{
		"client_id":             {"harness"},
		"redirect_uri":          {"http://127.0.0.1:9/cb"},
		"response_type":         {"code"},
		"scope":                 {"mail:read mail:write search:read"},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	resp, err := client.Get(e.base + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("authorize GET: %d %s", resp.StatusCode, body)
	}

	form := url.Values{
		"client_id":             {"harness"},
		"redirect_uri":          {"http://127.0.0.1:9/cb"},
		"state":                 {"xyz"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	for svc, level := range services {
		form.Set("svc_"+svc, level)
	}
	resp, err = client.PostForm(e.base+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("authorize POST: %d %s", resp.StatusCode, body)
	}
	location, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect: %s", resp.Header.Get("Location"))
	}
	if location.Query().Get("state") != "xyz" {
		t.Fatalf("state mismatch")
	}

	form = url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://127.0.0.1:9/cb"},
		"client_id":     {"harness"},
		"client_secret": {"hsecret"},
		"code_verifier": {verifier},
	}
	resp, err = http.PostForm(e.base+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("token: %d %s", resp.StatusCode, body)
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := jsonDecode(resp.Body, &tokenResp); err != nil {
		t.Fatal(err)
	}
	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" || tokenResp.ExpiresIn == 0 {
		t.Fatalf("incomplete token response: %+v", tokenResp)
	}
	return tokenResp.AccessToken
}

func jsonDecode(r io.Reader, v any) error {
	return json.NewDecoder(r).Decode(v)
}

// mcpClient connects an MCP client with a bearer token.
func (e *oauthTestEnv) mcpClient(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	httpClient := &http.Client{Transport: bearerRoundTripper{token}}
	transport := &mcp.StreamableClientTransport{Endpoint: e.base, HTTPClient: httpClient}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type bearerRoundTripper struct{ token string }

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(clone)
}

func TestFullStackOAuthAndMCP(t *testing.T) {
	e := newOAuthTestEnv(t)
	ctx := context.Background()

	// MCP without a token is rejected.
	resp, err := http.Post(e.base, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Full write grant: memode.write can send mail; memode.read can search.
	token := e.runOAuth(t, map[string]string{"mail": "write"})
	cs := e.mcpClient(t, token)

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memode.write", Arguments: map[string]any{"script": `return mail.send({to = "x@y.z"})`},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if res.IsError || res.Content[0].(*mcp.TextContent).Text != "sent:x@y.z" {
		t.Fatalf("write result: %+v", res)
	}

	// Read tool under a write grant can still only read.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memode.read", Arguments: map[string]any{"script": `return mail.search("hi")`},
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if res.IsError || res.Content[0].(*mcp.TextContent).Text != `["hit1"]` {
		t.Fatalf("read result: %+v", res)
	}

	// Denied service: kagi.search is not registered in this client's grant.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "memode.read", Arguments: map[string]any{"script": `return kagi.search("hi")`},
	})
	if err != nil {
		t.Fatalf("kagi call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error: kagi has no grant")
	}

	// Read-only grant: write denied even through memode.write.
	token = e.runOAuth(t, map[string]string{"mail": "read"})
	cs2 := e.mcpClient(t, token)
	res, err = cs2.CallTool(ctx, &mcp.CallToolParams{
		Name: "memode.write", Arguments: map[string]any{"script": `return mail.send({to = "x@y.z"})`},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error: mail:write grant missing")
	}
}

func TestDenyAndRegistration(t *testing.T) {
	e := newOAuthTestEnv(t)

	// Denying redirects with error=access_denied.
	verifier := "test-verifier-that-is-long-enough-abcdef1234567890"
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {"harness"},
		"redirect_uri":          {"http://127.0.0.1:9/cb"},
		"response_type":         {"code"},
		"state":                 {"s1"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.PostForm(e.base+"/authorize", url.Values{
		"client_id": {"harness"}, "redirect_uri": {"http://127.0.0.1:9/cb"}, "state": {"s1"},
		"code_challenge": {q.Get("code_challenge")}, "code_challenge_method": {"S256"}, "deny": {"1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "error=access_denied") {
		t.Fatalf("deny: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// RFC 7591 dynamic registration works and returns credentials.
	resp, err = http.Post(e.base+"/register", "application/json",
		strings.NewReader(`{"client_name":"later","redirect_uris":["http://127.0.0.1:9/cb2"]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d", resp.StatusCode)
	}
	var reg struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := jsonDecode(resp.Body, &reg); err != nil || reg.ClientID == "" || reg.ClientSecret == "" {
		t.Fatalf("registration response: %+v err=%v", reg, err)
	}

	// Metadata endpoints are served.
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/keys"} {
		resp, err := http.Get(e.base + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			t.Fatalf("metadata %s: %d", path, resp.StatusCode)
		}
	}
}
