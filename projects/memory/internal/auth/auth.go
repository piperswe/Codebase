// Package auth implements Memory's single-owner OAuth adapter. Cryptographic
// primitives come from oauthas, jwt/v5 and the maintained bcrypt password verifier.
package auth

import (
	"context"

	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"codebase.bid/lib/go/oauthas"
	"codebase.bid/projects/memory/internal/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

var Scopes = []string{"memory:read", "memory:execute", "memory:write", "memory:schema:write"}

type Config struct {
	Issuer              string
	Key                 []byte
	PreviousKey         []byte
	PreviousUntil       time.Time
	OwnerHash           string
	DynamicRegistration bool
}
type Server struct {
	DB     *pgxpool.Pool
	Config Config
	parsed *url.URL
}

func New(db *pgxpool.Pool, c Config) (*Server, error) {
	u, e := url.Parse(c.Issuer)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || len(c.Key) < 32 || !validHash(c.OwnerHash) {
		return nil, fmt.Errorf("HTTPS origin, persisted signing key and bcrypt owner hash are required")
	}
	if len(c.PreviousKey) > 0 && (len(c.PreviousKey) < 32 || c.PreviousUntil.IsZero() || c.PreviousUntil.After(time.Now().Add(15*time.Minute))) {
		return nil, fmt.Errorf("previous key overlap must be at most 15 minutes")
	}
	return &Server{DB: db, Config: c, parsed: u}, nil
}
func hash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
func token() string {
	s, e := oauthas.RandomToken()
	if e != nil {
		panic("randomness unavailable")
	}
	return s
}
func kid(key []byte) string        { return base64.RawURLEncoding.EncodeToString(hash(string(key))[:8]) }
func (s *Server) Resource() string { return s.Config.Issuer + "/mcp" }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func oauthError(w http.ResponseWriter, code string, status int) {
	writeJSON(w, status, map[string]string{"error": code})
}
func (s *Server) Client(ctx context.Context, id string) (*oauthas.Client, error) {
	var c oauthas.Client
	var redirects []byte
	var expires *time.Time
	e := s.DB.QueryRow(ctx, `SELECT id,name,redirects,metadata_expires FROM memory_auth.clients WHERE id=$1`, id).Scan(&c.ID, &c.Name, &redirects, &expires)
	if e == nil && (expires == nil || expires.After(time.Now())) {
		e = json.Unmarshal(redirects, &c.RedirectURIs)
		return &c, e
	}
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	var count int
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM memory_auth.clients WHERE metadata_expires IS NOT NULL`).Scan(&count); e != nil || count >= 100 {
		return nil, fmt.Errorf("client registration limit reached")
	}
	m, e := oauthas.FetchClientMetadata(ctx, id)
	if e != nil {
		return nil, e
	}
	if e = s.Register(ctx, m.ClientID, m.ClientName, m.RedirectURIs, time.Now().Add(time.Hour)); e != nil {
		return nil, e
	}
	return &oauthas.Client{ID: m.ClientID, Name: m.ClientName, RedirectURIs: m.RedirectURIs}, nil
}
func (s *Server) GetClient(ctx context.Context, id string) (*oauthas.Client, error) {
	return s.Client(ctx, id)
}
func (s *Server) Register(ctx context.Context, id, name string, redirects []string, expires time.Time) error {
	if len(id) == 0 || len(id) > 2048 || len(name) > 200 || len(redirects) == 0 || len(redirects) > 10 {
		return model.Invalid("client")
	}
	for _, r := range redirects {
		if !oauthas.ValidRedirect(r) {
			return model.Invalid("redirect")
		}
	}
	b, _ := json.Marshal(redirects)
	var ex any
	if !expires.IsZero() {
		ex = expires
	}
	_, e := s.DB.Exec(ctx, `INSERT INTO memory_auth.clients(id,name,redirects,metadata_expires) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET name=excluded.name,redirects=excluded.redirects,metadata_expires=excluded.metadata_expires`, id, name, b, ex)
	return e
}

type claims struct {
	Grant string `json:"grant"`
	jwt.RegisteredClaims
}

func (s *Server) access(grant, client string) (string, error) {
	now := time.Now()
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{Grant: grant, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.Config.Issuer, Subject: client, Audience: jwt.ClaimStrings{s.Resource()}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)), ID: token()}})
	t.Header["kid"] = kid(s.Config.Key)
	return t.SignedString(s.Config.Key)
}
func (s *Server) Validate(ctx context.Context, raw string) (model.Principal, error) {
	c := &claims{}
	t, e := jwt.ParseWithClaims(raw, c, func(t *jwt.Token) (any, error) {
		k, _ := t.Header["kid"].(string)
		if k == kid(s.Config.Key) {
			return s.Config.Key, nil
		}
		if len(s.Config.PreviousKey) > 0 && time.Now().Before(s.Config.PreviousUntil) && k == kid(s.Config.PreviousKey) {
			return s.Config.PreviousKey, nil
		}
		return nil, fmt.Errorf("unknown key")
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(s.Config.Issuer), jwt.WithAudience(s.Resource()), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if e != nil || !t.Valid {
		return model.Principal{}, fmt.Errorf("invalid access token")
	}
	p := model.Principal{Actor: "owner", Client: c.Subject}
	e = s.DB.QueryRow(ctx, `SELECT scopes,spaces FROM memory_auth.grants WHERE id::text=$1 AND client_id=$2 AND NOT revoked AND resource=$3`, c.Grant, c.Subject, s.Resource()).Scan(&p.Scopes, &p.Spaces)
	if e != nil {
		return model.Principal{}, fmt.Errorf("invalid grant")
	}
	return p, nil
}
func (s *Server) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if raw == r.Header.Get("Authorization") || raw == "" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+s.Config.Issuer+`/.well-known/oauth-protected-resource/mcp"`)
			oauthError(w, "invalid_token", 401)
			return
		}
		p, e := s.Validate(r.Context(), raw)
		if e != nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			oauthError(w, "invalid_token", 401)
			return
		}
		next.ServeHTTP(w, r.WithContext(model.WithPrincipal(r.Context(), p)))
	})
}
func (s *Server) Routes(mux *http.ServeMux) {
	metadata := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"resource": s.Resource(), "authorization_servers": []string{s.Config.Issuer}, "scopes_supported": Scopes, "bearer_methods_supported": []string{"header"}})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", metadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", metadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		m := map[string]any{"issuer": s.Config.Issuer, "authorization_endpoint": s.Config.Issuer + "/authorize", "token_endpoint": s.Config.Issuer + "/token", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": Scopes, "client_id_metadata_document_supported": true}
		if s.Config.DynamicRegistration {
			m["registration_endpoint"] = s.Config.Issuer + "/register"
		}
		writeJSON(w, 200, m)
	})
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /authorize", s.authorizeForm)
	mux.HandleFunc("POST /authorize", s.authorize)
	mux.HandleFunc("POST /token", s.exchange)
	if s.Config.DynamicRegistration {
		mux.HandleFunc("POST /register", s.register)
	}
}

// Guard validates Host/Origin independently of forwarded headers and bounds all
// public bodies. Ingress must preserve Host and exclude the separate admin port.
func (s *Server) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.parsed.Host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.Config.Issuer) {
			http.Error(w, "untrusted origin", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) parseAuthorize(ctx context.Context, q url.Values) (*oauthas.AuthorizeRequest, error) {
	if q.Get("resource") != s.Resource() {
		return nil, fmt.Errorf("invalid resource")
	}
	req, e := (&oauthas.Server{Clients: s}).ParseAuthorize(ctx, q)
	if e != nil {
		return nil, e
	}
	b, e := base64.RawURLEncoding.DecodeString(req.Challenge)
	if e != nil || len(b) != 32 {
		return nil, fmt.Errorf("invalid PKCE challenge")
	}
	for _, scope := range req.Scopes {
		if !slices.Contains(Scopes, scope) {
			return nil, fmt.Errorf("invalid scope")
		}
	}
	return req, nil
}
func (s *Server) Revoke(ctx context.Context, id string) error {
	_, e := s.DB.Exec(ctx, `UPDATE memory_auth.grants SET revoked=true WHERE id::text=$1 OR client_id=$1`, id)
	return e
}
func (s *Server) Cleanup(ctx context.Context) error {
	_, e := s.DB.Exec(ctx, `DELETE FROM memory_auth.sessions WHERE expires_at<now(); DELETE FROM memory_auth.codes WHERE expires_at<now(); DELETE FROM memory_auth.refresh WHERE expires_at<now(); DELETE FROM memory_auth.login_limits WHERE window_start<now()-interval '1 day'; DELETE FROM memory_auth.clients c WHERE metadata_expires<now()-interval '1 day' AND NOT EXISTS(SELECT FROM memory_auth.grants g WHERE g.client_id=c.id)`)
	return e
}
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var m oauthas.ClientMetadata
	if json.NewDecoder(r.Body).Decode(&m) != nil {
		oauthError(w, "invalid_client_metadata", 400)
		return
	}
	var n int
	if s.DB.QueryRow(r.Context(), `SELECT count(*) FROM memory_auth.clients WHERE metadata_expires IS NOT NULL`).Scan(&n) != nil || n >= 100 {
		oauthError(w, "temporarily_unavailable", 429)
		return
	}
	m.ClientID = token()
	if s.Register(r.Context(), m.ClientID, m.ClientName, m.RedirectURIs, time.Now().Add(24*time.Hour)) != nil {
		oauthError(w, "invalid_client_metadata", 400)
		return
	}
	m.TokenEndpointAuthMethod = "none"
	writeJSON(w, 201, m)
}

func validHash(s string) bool {
	cost, e := bcrypt.Cost([]byte(s))
	return e == nil && cost >= 12 && cost <= 16
}
