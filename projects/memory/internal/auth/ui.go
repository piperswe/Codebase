package auth

import (
	"crypto/hmac"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"codebase.bid/projects/memory/internal/model"
	"golang.org/x/crypto/bcrypt"
)

var loginPage = template.Must(template.New("login").Parse(`<!doctype html><html><head><title>Memory owner login</title></head><body><h1>Memory owner login</h1><form method="post" action="/login"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="return" value="{{.Return}}"><label>Owner password <input type="password" name="password" autocomplete="current-password" required></label><button>Sign in</button></form></body></html>`))
var consentPage = template.Must(template.New("consent").Parse(`<!doctype html><html><head><title>Memory consent</title></head><body><h1>Allow {{.Name}} to access Memory?</h1><p>Client identity: {{.ID}}</p><form method="post" action="/authorize">{{range $key,$values:=.Params}}{{range $values}}<input type="hidden" name="{{$key}}" value="{{.}}">{{end}}{{end}}<input type="hidden" name="csrf" value="{{.CSRF}}"><fieldset><legend>Scopes</legend>{{range .Scopes}}<label><input type="checkbox" name="grant_scope" value="{{.}}" {{if or (eq . "memory:read") (eq . "memory:execute")}}checked{{end}}>{{.}}</label><br>{{end}}</fieldset><fieldset><legend>Spaces</legend>{{range .Spaces}}<label><input type="checkbox" name="grant_space" value="{{.}}" {{if eq . "personal"}}checked{{end}}>{{.}}</label><br>{{end}}</fieldset><button name="decision" value="allow">Allow selected access</button><button name="decision" value="deny">Deny</button></form><p>Archival and retraction retain history and backups.</p></body></html>`))

func cookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(ttl.Seconds())})
}
func returnPath(s string) string {
	if strings.HasPrefix(s, "/authorize?") && !strings.ContainsAny(s, "\r\n") {
		return s
	}
	return "/authorize"
}
func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	csrf := token()
	cookie(w, "__Host-memory-login", csrf, 5*time.Minute)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = loginPage.Execute(w, struct{ CSRF, Return string }{csrf, returnPath(r.URL.Query().Get("return"))})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		oauthError(w, "invalid_request", 400)
		return
	}
	c, e := r.Cookie("__Host-memory-login")
	if e != nil || !hmac.Equal([]byte(c.Value), []byte(r.PostForm.Get("csrf"))) || c.Value == "" {
		oauthError(w, "invalid_request", 403)
		return
	}
	var attempts int
	e = s.DB.QueryRow(r.Context(), `INSERT INTO memory_auth.login_limits(key,attempts,window_start) VALUES('owner-login',1,now()) ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN memory_auth.login_limits.window_start<now()-interval '1 minute' THEN 1 ELSE memory_auth.login_limits.attempts+1 END,window_start=CASE WHEN memory_auth.login_limits.window_start<now()-interval '1 minute' THEN now() ELSE memory_auth.login_limits.window_start END RETURNING attempts`).Scan(&attempts)
	if e != nil || attempts > 5 {
		oauthError(w, "temporarily_unavailable", 429)
		return
	}
	password := r.PostForm.Get("password")
	if len(password) > 1024 || bcrypt.CompareHashAndPassword([]byte(s.Config.OwnerHash), []byte(password)) != nil {
		oauthError(w, "access_denied", 401)
		return
	}
	session, csrf := token(), token()
	_, e = s.DB.Exec(r.Context(), `INSERT INTO memory_auth.sessions(hash,csrf,expires_at) VALUES($1,$2,now()+interval '1 hour')`, hash(session), csrf)
	if e != nil {
		oauthError(w, "server_error", 500)
		return
	}
	cookie(w, "__Host-memory-session", session, time.Hour)
	cookie(w, "__Host-memory-login", "", -time.Second)
	http.Redirect(w, r, returnPath(r.PostForm.Get("return")), 303)
}
func (s *Server) ownerSession(r *http.Request) (string, error) {
	c, e := r.Cookie("__Host-memory-session")
	if e != nil {
		return "", e
	}
	var csrf string
	e = s.DB.QueryRow(r.Context(), `SELECT csrf FROM memory_auth.sessions WHERE hash=$1 AND expires_at>now()`, hash(c.Value)).Scan(&csrf)
	return csrf, e
}
func (s *Server) authorizeForm(w http.ResponseWriter, r *http.Request) {
	csrf, e := s.ownerSession(r)
	if e != nil {
		http.Redirect(w, r, "/login?return="+url.QueryEscape(r.URL.RequestURI()), 303)
		return
	}
	req, e := s.parseAuthorize(r.Context(), r.URL.Query())
	if e != nil {
		oauthError(w, "invalid_request", 400)
		return
	}
	client, e := s.Client(r.Context(), req.ClientID)
	if e != nil {
		oauthError(w, "invalid_client", 400)
		return
	}
	rows, e := s.DB.Query(r.Context(), `SELECT slug FROM memory.spaces ORDER BY slug`)
	if e != nil {
		oauthError(w, "server_error", 500)
		return
	}
	spaces := []string{}
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) != nil {
			rows.Close()
			oauthError(w, "server_error", 500)
			return
		}
		spaces = append(spaces, slug)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		oauthError(w, "server_error", 500)
		return
	}
	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"memory:read", "memory:execute"}
	}
	params := url.Values{}
	for _, k := range []string{"client_id", "redirect_uri", "response_type", "state", "code_challenge", "code_challenge_method", "resource", "scope"} {
		params.Set(k, r.URL.Query().Get(k))
	}
	redirect, _ := url.Parse(req.RedirectURI)
	// Chromium applies form-action to the post-consent redirect as well as the
	// same-origin form target. The registered, validated origin is explicit.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self' "+redirect.Scheme+"://"+redirect.Host+"; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = consentPage.Execute(w, struct {
		Name, ID, CSRF string
		Params         url.Values
		Scopes, Spaces []string
	}{client.Name, client.ID, csrf, params, scopes, spaces})
}
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	csrf, e := s.ownerSession(r)
	if e != nil {
		oauthError(w, "access_denied", 401)
		return
	}
	if r.ParseForm() != nil || !hmac.Equal([]byte(csrf), []byte(r.PostForm.Get("csrf"))) {
		oauthError(w, "invalid_request", 403)
		return
	}
	req, e := s.parseAuthorize(r.Context(), r.PostForm)
	if e != nil {
		oauthError(w, "invalid_request", 400)
		return
	}
	u, _ := url.Parse(req.RedirectURI)
	q := u.Query()
	q.Set("state", req.State)
	if r.PostForm.Get("decision") != "allow" {
		q.Set("error", "access_denied")
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.String(), 303)
		return
	}
	scopes := r.PostForm["grant_scope"]
	spaces := r.PostForm["grant_space"]
	requested := req.Scopes
	if len(requested) == 0 {
		requested = []string{"memory:read", "memory:execute"}
	}
	if len(scopes) == 0 || len(scopes) > 4 || len(spaces) == 0 || len(spaces) > 100 {
		oauthError(w, "invalid_scope", 400)
		return
	}
	for _, scope := range scopes {
		if !slices.Contains(requested, scope) || !slices.Contains(Scopes, scope) {
			oauthError(w, "invalid_scope", 400)
			return
		}
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		oauthError(w, "server_error", 500)
		return
	}
	defer tx.Rollback(r.Context())
	for _, slug := range spaces {
		var exists bool
		if tx.QueryRow(r.Context(), `SELECT EXISTS(SELECT FROM memory.spaces WHERE slug=$1)`, slug).Scan(&exists) != nil || !exists {
			oauthError(w, "invalid_request", 400)
			return
		}
	}
	grant := model.NewID()
	_, e = tx.Exec(r.Context(), `INSERT INTO memory_auth.grants(id,client_id,scopes,spaces,resource) VALUES($1,$2,$3,$4,$5)`, grant, req.ClientID, scopes, spaces, s.Resource())
	if e != nil {
		oauthError(w, "server_error", 500)
		return
	}
	code := token()
	_, e = tx.Exec(r.Context(), `INSERT INTO memory_auth.codes(hash,grant_id,redirect,challenge,expires_at) VALUES($1,$2,$3,$4,now()+interval '60 seconds')`, hash(code), grant, req.RedirectURI, req.Challenge)
	if e != nil || tx.Commit(r.Context()) != nil {
		oauthError(w, "server_error", 500)
		return
	}
	q.Set("code", code)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), 303)
}
