package oauthas

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type memClients struct{ m map[string]*Client }

func (c *memClients) GetClient(_ context.Context, id string) (*Client, error) {
	cl, ok := c.m[id]
	if !ok {
		return nil, nil
	}
	return cl, nil
}

type memCodes struct {
	mu sync.Mutex
	m  map[string]*Code
}

func (c *memCodes) PutCode(_ context.Context, code Code) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	sc := code
	c.m[code.Code] = &sc
	return nil
}

func (c *memCodes) TakeCode(_ context.Context, id string) (*Code, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	code, ok := c.m[id]
	delete(c.m, id)
	if !ok {
		return nil, nil
	}
	return code, nil
}

type memTokens struct {
	mu sync.Mutex
	m  map[string]*Refresh
}

func (t *memTokens) PutRefresh(_ context.Context, r Refresh) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	rr := r
	t.m[r.Token] = &rr
	return nil
}

func (t *memTokens) TakeRefresh(_ context.Context, tok string) (*Refresh, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.m[tok]
	delete(t.m, tok)
	if !ok {
		return nil, nil
	}
	return r, nil
}

func testServer(t *testing.T) *Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		Issuer: "https://memode.test",
		Key:    key,
		Clients: &memClients{m: map[string]*Client{
			"pub":  {ID: "pub", Name: "public client", RedirectURIs: []string{"http://127.0.0.1:1/cb"}},
			"conf": {ID: "conf", Secret: "s3cret", Name: "confidential", RedirectURIs: []string{"http://127.0.0.1:2/cb"}},
		}},
		Codes:  &memCodes{m: map[string]*Code{}},
		Tokens: &memTokens{m: map[string]*Refresh{}},
	}
}

func pkce(t *testing.T) (challenge, verifier string) {
	t.Helper()
	verifier = "a-verifier-string-that-is-long-enough-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]), verifier
}

func codeFromRedirect(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("code")
}

func runAuthCodeFlow(t *testing.T, s *Server, clientID, secret string, scopes []string) *TokenResponse {
	t.Helper()
	ctx := context.Background()
	challenge, verifier := pkce(t)
	redirectURI := "http://127.0.0.1:1/cb"
	if clientID == "conf" {
		redirectURI = "http://127.0.0.1:2/cb"
	}
	q := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {join(scopes)},
		"state":                 {"st"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	req, err := s.ParseAuthorize(ctx, q)
	if err != nil {
		t.Fatalf("parse authorize: %v", err)
	}
	redirect, err := s.IssueCode(ctx, req, scopes)
	if err != nil {
		t.Fatalf("issue code: %v", err)
	}
	code := codeFromRedirect(t, redirect)
	if code == "" {
		t.Fatalf("bad redirect: %s", redirect)
	}
	resp, oerr := s.Exchange(ctx, TokenRequest{
		GrantType:    "authorization_code",
		Code:         code,
		RedirectURI:  redirectURI,
		ClientID:     clientID,
		ClientSecret: secret,
		Verifier:     verifier,
	})
	if oerr != nil {
		t.Fatalf("exchange: %v", oerr)
	}
	if resp.Scope != join(scopes) {
		t.Fatalf("scope mismatch: %q", resp.Scope)
	}
	return resp
}

func TestAuthorizationCodeFlow(t *testing.T) {
	s := testServer(t)
	resp := runAuthCodeFlow(t, s, "pub", "", []string{"mail:read"})
	claims, err := s.ValidateAccessToken(resp.AccessToken)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if claims.ClientID != "pub" || len(claims.Scopes) != 1 || claims.Scopes[0] != "mail:read" {
		t.Fatalf("claims: %#v", claims)
	}
}

func TestConfidentialClientRequiresSecret(t *testing.T) {
	s := testServer(t)
	if _, oerr := runAuthCodeFlowChecked(t, s, "conf", "", []string{"mail:read"}); oerr == nil {
		t.Fatal("expected invalid_client without secret")
	}
	runAuthCodeFlow(t, s, "conf", "s3cret", []string{"mail:read"})
}

func TestRefreshRotation(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	resp := runAuthCodeFlow(t, s, "pub", "", []string{"mail:read", "mail:write"})

	// Refresh grants a new pair; the old refresh token is consumed.
	resp2, oerr := s.Exchange(ctx, TokenRequest{
		GrantType:    "refresh_token",
		RefreshToken: resp.RefreshToken,
		ClientID:     "pub",
	})
	if oerr != nil {
		t.Fatalf("refresh: %v", oerr)
	}
	if resp2.RefreshToken == "" || resp2.RefreshToken == resp.RefreshToken {
		t.Fatalf("refresh token not rotated")
	}
	if _, oerr := s.Exchange(ctx, TokenRequest{GrantType: "refresh_token", RefreshToken: resp.RefreshToken, ClientID: "pub"}); oerr == nil {
		t.Fatal("old refresh token must be single-use")
	}
	claims, err := s.ValidateAccessToken(resp2.AccessToken)
	if err != nil || len(claims.Scopes) != 2 {
		t.Fatalf("refreshed claims: %v %#v", err, claims)
	}
}

func TestCodeIsSingleUseAndExpiring(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	challenge, verifier := pkce(t)
	q := url.Values{"client_id": {"pub"}, "redirect_uri": {"http://127.0.0.1:1/cb"}, "response_type": {"code"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	req, _ := s.ParseAuthorize(ctx, q)
	redirect, _ := s.IssueCode(ctx, req, nil)
	code := codeFromRedirect(t, redirect)

	tr := TokenRequest{GrantType: "authorization_code", Code: code, RedirectURI: "http://127.0.0.1:1/cb", ClientID: "pub", Verifier: verifier}
	if _, oerr := s.Exchange(ctx, tr); oerr != nil {
		t.Fatalf("first use: %v", oerr)
	}
	if _, oerr := s.Exchange(ctx, tr); oerr == nil {
		t.Fatal("code must be single-use")
	}

	// Expired code is rejected.
	s.CodeTTL = time.Millisecond
	req2, _ := s.ParseAuthorize(ctx, q)
	redirect2, _ := s.IssueCode(ctx, req2, nil)
	code2 := codeFromRedirect(t, redirect2)
	time.Sleep(5 * time.Millisecond)
	if _, oerr := s.Exchange(ctx, TokenRequest{GrantType: "authorization_code", Code: code2, RedirectURI: "http://127.0.0.1:1/cb", ClientID: "pub", Verifier: verifier}); oerr == nil {
		t.Fatal("expired code must be rejected")
	}
}

func TestPKCEVerifierRequired(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	challenge, verifier := pkce(t)
	q := url.Values{"client_id": {"pub"}, "redirect_uri": {"http://127.0.0.1:1/cb"}, "response_type": {"code"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	req, _ := s.ParseAuthorize(ctx, q)
	redirect, _ := s.IssueCode(ctx, req, nil)
	code := codeFromRedirect(t, redirect)
	if _, oerr := s.Exchange(ctx, TokenRequest{GrantType: "authorization_code", Code: code, RedirectURI: "http://127.0.0.1:1/cb", ClientID: "pub", Verifier: verifier + "x"}); oerr == nil {
		t.Fatal("wrong verifier must fail")
	}
}

func TestTamperedTokenRejected(t *testing.T) {
	s := testServer(t)
	resp := runAuthCodeFlow(t, s, "pub", "", []string{"mail:read"})
	if _, err := s.ValidateAccessToken(resp.AccessToken + "x"); err == nil {
		t.Fatal("tampered token must fail")
	}
	// A token from another issuer's key fails too.
	s2 := testServer(t)
	s2.Issuer = s.Issuer
	if _, err := s2.ValidateAccessToken(resp.AccessToken); err == nil {
		t.Fatal("wrong key must fail")
	}
}

func TestAuthorizeValidation(t *testing.T) {
	s := testServer(t)
	ctx := context.Background()
	base := url.Values{"client_id": {"pub"}, "redirect_uri": {"http://127.0.0.1:1/cb"}, "response_type": {"code"}, "code_challenge": {"x"}, "code_challenge_method": {"S256"}}
	if _, err := s.ParseAuthorize(ctx, url.Values{"client_id": {"nope"}, "redirect_uri": {"http://x/cb"}, "response_type": {"code"}}); err == nil {
		t.Fatal("unknown client must fail")
	}
	bad := cloneVals(base)
	bad.Set("redirect_uri", "http://evil/cb")
	if _, err := s.ParseAuthorize(ctx, bad); err == nil {
		t.Fatal("unregistered redirect must fail")
	}
	bad = cloneVals(base)
	bad.Set("response_type", "token")
	if _, err := s.ParseAuthorize(ctx, bad); err == nil {
		t.Fatal("implicit flow must be rejected (OAuth 2.1)")
	}
	bad = cloneVals(base)
	bad.Set("code_challenge_method", "plain")
	if _, err := s.ParseAuthorize(ctx, bad); err == nil {
		t.Fatal("plain PKCE must be rejected")
	}
	if _, err := s.ParseAuthorize(ctx, base); err != nil {
		t.Fatalf("valid request must pass: %v", err)
	}
}

// runAuthCodeFlowChecked is runAuthCodeFlow that returns the OAuth error
// instead of failing the test.
func runAuthCodeFlowChecked(t *testing.T, s *Server, clientID, secret string, scopes []string) (*TokenResponse, *OAuthError) {
	t.Helper()
	ctx := context.Background()
	challenge, verifier := pkce(t)
	redirectURI := "http://127.0.0.1:1/cb"
	if clientID == "conf" {
		redirectURI = "http://127.0.0.1:2/cb"
	}
	q := url.Values{"client_id": {clientID}, "redirect_uri": {redirectURI}, "response_type": {"code"}, "scope": {join(scopes)}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	req, err := s.ParseAuthorize(ctx, q)
	if err != nil {
		return nil, &OAuthError{Code: "invalid_request", Desc: err.Error()}
	}
	redirect, err := s.IssueCode(ctx, req, scopes)
	if err != nil {
		return nil, &OAuthError{Code: "server_error", Desc: err.Error()}
	}
	code := codeFromRedirect(t, redirect)
	return s.Exchange(ctx, TokenRequest{GrantType: "authorization_code", Code: code, RedirectURI: redirectURI, ClientID: clientID, ClientSecret: secret, Verifier: verifier})
}

func cloneVals(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = vs
	}
	return out
}

func join(ss []string) string { return strings.Join(ss, " ") }
