// Package oauthas implements the primitives of an OAuth 2.1 authorization
// server: PKCE authorization-code verification, JWT access-token issuance and
// validation, JWKS, AS metadata and refresh-token rotation. Hosts supply the
// client/code/token persistence (via interfaces), the consent UI and the HTTP
// wiring.
package oauthas

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Client is one registered OAuth client. A client with an empty Secret is
// public and must use PKCE (all clients must, per OAuth 2.1).
type Client struct {
	ID           string
	Secret       string
	Name         string
	RedirectURIs []string
}

func (c *Client) Public() bool { return c.Secret == "" }

// RedirectAllowed reports whether uri exactly matches a registered redirect.
func (c *Client) RedirectAllowed(uri string) bool {
	for _, r := range c.RedirectURIs {
		if r == uri {
			return true
		}
	}
	return false
}

// ClientStore resolves registered clients.
type ClientStore interface {
	GetClient(ctx context.Context, id string) (*Client, error)
}

// Code is a short-lived authorization code binding one pending approval.
type Code struct {
	Code        string
	ClientID    string
	RedirectURI string
	Challenge   string // PKCE S256 only
	Scopes      []string
	ExpiresAt   time.Time
}

// CodeStore issues and single-use consumes authorization codes. TakeCode must
// delete the code even if returning an error, per RFC 6749 4.1.2 (replay).
type CodeStore interface {
	PutCode(ctx context.Context, c Code) error
	TakeCode(ctx context.Context, code string) (*Code, error)
}

// Refresh is a stored refresh token.
type Refresh struct {
	Token     string
	ClientID  string
	Scopes    []string
	ExpiresAt time.Time
}

// TokenStore persists refresh tokens.
type TokenStore interface {
	PutRefresh(ctx context.Context, r Refresh) error
	TakeRefresh(ctx context.Context, token string) (*Refresh, error) // consumed: delete on read
}

// Server issues and validates tokens for one issuer.
type Server struct {
	Issuer  string
	Key     *rsa.PrivateKey
	Clients ClientStore
	Codes   CodeStore
	Tokens  TokenStore

	AccessTTL  time.Duration // default 15 min
	RefreshTTL time.Duration // default 30 days
	CodeTTL    time.Duration // default 60 s
}

func (s *Server) accessTTL() time.Duration  { return orDefault(s.AccessTTL, 15*time.Minute) }
func (s *Server) refreshTTL() time.Duration { return orDefault(s.RefreshTTL, 30*24*time.Hour) }
func (s *Server) codeTTL() time.Duration    { return orDefault(s.CodeTTL, 60*time.Second) }

func orDefault(t, def time.Duration) time.Duration {
	if t <= 0 {
		return def
	}
	return t
}

// OAuthError is an RFC 6749 section 5.2 error, surfaced as JSON on /token.
type OAuthError struct {
	Code string
	Desc string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Desc }

func oerr(code, format string, args ...any) *OAuthError {
	return &OAuthError{Code: code, Desc: fmt.Sprintf(format, args...)}
}

// AuthorizeRequest is a parsed and validated /authorize query.
type AuthorizeRequest struct {
	ClientID    string
	RedirectURI string
	Scopes      []string
	State       string
	Challenge   string
}

// ParseAuthorize validates the /authorize query per OAuth 2.1: exact redirect
// match, response_type=code, S256 PKCE. It returns the sanitized request, or
// an error safe to show the resource owner (never a redirect: client identity
// could not be established).
func (s *Server) ParseAuthorize(ctx context.Context, q url.Values) (*AuthorizeRequest, error) {
	clientID := q.Get("client_id")
	if clientID == "" {
		return nil, oerr("invalid_request", "missing client_id")
	}
	client, err := s.Clients.GetClient(ctx, clientID)
	if err != nil || client == nil {
		return nil, oerr("invalid_request", "unknown client")
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" || !client.RedirectAllowed(redirectURI) {
		return nil, oerr("invalid_request", "redirect_uri is not registered for this client")
	}
	if q.Get("response_type") != "code" {
		return nil, oerr("unsupported_response_type", "only response_type=code is supported")
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		return nil, oerr("invalid_request", "PKCE with code_challenge_method=S256 is required")
	}
	return &AuthorizeRequest{
		ClientID:    clientID,
		RedirectURI: redirectURI,
		Scopes:      splitScopes(q.Get("scope")),
		State:       q.Get("state"),
		Challenge:   q.Get("code_challenge"),
	}, nil
}

func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

// IssueCode stores a fresh authorization code for an approved request and
// returns the redirect URL the client should be sent to.
func (s *Server) IssueCode(ctx context.Context, req *AuthorizeRequest, grantedScopes []string) (string, error) {
	code, err := RandomToken()
	if err != nil {
		return "", err
	}
	if err := s.Codes.PutCode(ctx, Code{
		Code:        code,
		ClientID:    req.ClientID,
		RedirectURI: req.RedirectURI,
		Challenge:   req.Challenge,
		Scopes:      grantedScopes,
		ExpiresAt:   time.Now().Add(s.codeTTL()),
	}); err != nil {
		return "", err
	}
	u, err := url.Parse(req.RedirectURI)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("code", code)
	if req.State != "" {
		q.Set("state", req.State)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// TokenResponse is the /token success body.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

// TokenRequest is a parsed /token form body.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	ClientSecret string
	Verifier     string
	RefreshToken string
}

// ParseTokenForm parses the application/x-www-form-urlencoded /token body.
// Client credentials may come from the form or Basic auth (RFC 6749 2.3.1).
func ParseTokenForm(r *http.Request) TokenRequest {
	q := r.PostForm
	req := TokenRequest{
		GrantType:    q.Get("grant_type"),
		Code:         q.Get("code"),
		RedirectURI:  q.Get("redirect_uri"),
		ClientID:     q.Get("client_id"),
		ClientSecret: q.Get("client_secret"),
		Verifier:     q.Get("code_verifier"),
		RefreshToken: q.Get("refresh_token"),
	}
	if id, secret, ok := r.BasicAuth(); ok {
		if req.ClientID == "" {
			req.ClientID = id
		}
		if req.ClientSecret == "" {
			req.ClientSecret = secret
		}
	}
	return req
}

// Exchange runs the token endpoint logic: authorization_code with PKCE, or
// refresh_token with rotation. Returned refresh tokens replace the presented
// one; the old one is consumed.
func (s *Server) Exchange(ctx context.Context, req TokenRequest) (*TokenResponse, *OAuthError) {
	clientID := req.ClientID
	client, err := s.Clients.GetClient(ctx, clientID)
	if err != nil || client == nil {
		return nil, oerr("invalid_client", "unknown client")
	}
	if !client.Public() {
		if req.ClientSecret == "" || req.ClientSecret != client.Secret {
			return nil, oerr("invalid_client", "client authentication failed")
		}
	}
	switch req.GrantType {
	case "authorization_code":
		code, err := s.Codes.TakeCode(ctx, req.Code)
		if err != nil || code == nil {
			return nil, oerr("invalid_grant", "unknown or expired code")
		}
		if code.ClientID != clientID {
			return nil, oerr("invalid_grant", "code was issued to another client")
		}
		if time.Now().After(code.ExpiresAt) {
			return nil, oerr("invalid_grant", "code expired")
		}
		if code.RedirectURI != req.RedirectURI {
			return nil, oerr("invalid_grant", "redirect_uri mismatch")
		}
		if !VerifyPKCE(code.Challenge, req.Verifier) {
			return nil, oerr("invalid_grant", "PKCE verification failed")
		}
		return s.issue(ctx, clientID, code.Scopes)
	case "refresh_token":
		refresh, err := s.Tokens.TakeRefresh(ctx, req.RefreshToken)
		if err != nil || refresh == nil {
			return nil, oerr("invalid_grant", "unknown refresh token")
		}
		if refresh.ClientID != clientID {
			return nil, oerr("invalid_grant", "refresh token was issued to another client")
		}
		if time.Now().After(refresh.ExpiresAt) {
			return nil, oerr("invalid_grant", "refresh token expired")
		}
		return s.issue(ctx, clientID, refresh.Scopes)
	default:
		return nil, oerr("unsupported_grant_type", "supported: authorization_code, refresh_token")
	}
}

func (s *Server) issue(ctx context.Context, clientID string, scopes []string) (*TokenResponse, *OAuthError) {
	access, err := s.AccessToken(clientID, scopes, s.accessTTL())
	if err != nil {
		return nil, oerr("server_error", "%v", err)
	}
	resp := &TokenResponse{
		AccessToken: access,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.accessTTL().Seconds()),
		Scope:       strings.Join(scopes, " "),
	}
	refresh, err := RandomToken()
	if err != nil {
		return nil, oerr("server_error", "%v", err)
	}
	if err := s.Tokens.PutRefresh(ctx, Refresh{
		Token:     refresh,
		ClientID:  clientID,
		Scopes:    scopes,
		ExpiresAt: time.Now().Add(s.refreshTTL()),
	}); err != nil {
		return nil, oerr("server_error", "%v", err)
	}
	resp.RefreshToken = refresh
	return resp, nil
}

// Claims are the memode-agnostic JWT access-token claims.
type Claims struct {
	ClientID string
	Scopes   []string
}

// AccessToken issues a signed JWT for clientID with the given scopes.
func (s *Server) AccessToken(clientID string, scopes []string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    s.Issuer,
		Subject:   clientID,
		Audience:  jwt.ClaimStrings{s.Issuer},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		ID:        mustRandom(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["scope"] = strings.Join(scopes, " ")
	return token.SignedString(s.Key)
}

// ValidateAccessToken verifies signature, issuer and expiry, returning the
// embedded client and scopes.
func (s *Server) ValidateAccessToken(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwt.RegisteredClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return &s.Key.PublicKey, nil
	}, jwt.WithIssuer(s.Issuer), jwt.WithAudience(s.Issuer), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid access token")
	}
	rc := token.Claims.(*jwt.RegisteredClaims)
	scope, _ := token.Header["scope"].(string)
	return &Claims{ClientID: rc.Subject, Scopes: splitScopes(scope)}, nil
}

// JWKS returns the server's public key set in RFC 7517 form.
func (s *Server) JWKS() map[string]any {
	return map[string]any{
		"keys": []any{map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": "default",
			"n":   base64.RawURLEncoding.EncodeToString(s.Key.PublicKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(bigE(s.Key.PublicKey.E)),
		}},
	}
}

// Metadata is the RFC 8414 authorization-server metadata document.
// scopes is the host's ordered list of supported scopes.
func (s *Server) Metadata(scopes []string) map[string]any {
	iss := strings.TrimRight(s.Issuer, "/")
	return map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/authorize",
		"token_endpoint":                        iss + "/token",
		"registration_endpoint":                 iss + "/register",
		"jwks_uri":                              iss + "/keys",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                      append([]string{"offline_access"}, scopes...),
	}
}

// VerifyPKCE checks an S256 challenge against a verifier.
func VerifyPKCE(challenge, verifier string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

// RandomToken returns a 256-bit URL-safe random token.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func mustRandom() string {
	t, _ := RandomToken()
	return t
}

// ErrorWriter writes an OAuthError as the RFC 6749 token-endpoint JSON body.
func ErrorWriter(w http.ResponseWriter, e *OAuthError) {
	status := http.StatusBadRequest
	if e.Code == "invalid_client" {
		status = http.StatusUnauthorized
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q,"error_description":%q}`, e.Code, e.Desc)
}

// bigE encodes the RSA public exponent for JWKS.
func bigE(e int) []byte {
	return big.NewInt(int64(e)).Bytes()
}
