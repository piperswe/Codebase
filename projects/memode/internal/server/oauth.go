package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"codebase.bid/lib/go/oauthas"
	"github.com/go-chi/chi/v5"

	"codebase.bid/projects/memode/internal/oauthstore"
)

// initOAuth builds the authorization server. Call only when store != nil.
func (s *Server) initOAuth(store *oauthstore.Store) error {
	key, err := store.SigningKey()
	if err != nil {
		return err
	}
	s.oauthStore = store
	s.oauth = &oauthas.Server{Issuer: s.cfg.BaseURL, Key: key, Clients: store, Codes: store, Tokens: store}
	return nil
}

// scopeVocabulary lists the OAuth scopes for configured services.
func (s *Server) scopeVocabulary() []string {
	var out []string
	for _, svc := range []string{"mail", "calendar", "contacts", "telegram"} {
		if s.cfg.ServiceConfigured()[svc] {
			out = append(out, svc+":read", svc+":write")
		}
	}
	if s.cfg.ServiceConfigured()["search"] {
		out = append(out, "search:read")
	}
	return out
}

// HTTPHandler mounts everything: OAuth endpoints and the MCP server.
func (s *Server) HTTPHandler() http.Handler {
	r := chi.NewRouter()

	if s.oauth != nil {
		r.Get("/.well-known/oauth-protected-resource", s.protectedResourceMetadata)
		r.Get("/.well-known/oauth-authorization-server", s.asMetadata)
		r.Get("/keys", s.jwks)
		r.Post("/token", s.tokenEndpoint)
		r.Post("/register", s.registerEndpoint)
		s.ownerProtected(r, "/authorize")
		r.Get("/authorize", s.consentForm)
		r.Post("/authorize", s.consentSubmit)
	}

	r.Handle("/", s.authMiddleware(s.MCPHandler()))
	return r
}

// ownerProtected requires the owner password (basic auth, user "memode") on
// routes the resource owner uses directly.
func (s *Server) ownerProtected(r chi.Router, pattern string) {
	if s.cfg.OwnerPass == "" {
		return // local trusted mode: consent pages unprotected on localhost
	}
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user, pass, ok := req.BasicAuth()
			if !ok || user != "memode" || pass != s.cfg.OwnerPass {
				w.Header().Set("WWW-Authenticate", `Basic realm="memode"`)
				http.Error(w, "owner authentication required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
}

// authMiddleware validates Bearer access tokens and injects the grant matrix.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AllowUnauthenticated || s.oauth == nil {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="memode", resource_metadata="`+s.cfg.BaseURL+`/.well-known/oauth-protected-resource"`)
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		claims, err := s.oauth.ValidateAccessToken(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			http.Error(w, "invalid access token", http.StatusUnauthorized)
			return
		}
		ctx := oauthstore.WithGrants(r.Context(), oauthstore.GrantsFromScopes(claims.Scopes))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) protectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              s.cfg.BaseURL,
		"authorization_servers": []string{s.cfg.BaseURL},
	})
}

func (s *Server) asMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.oauth.Metadata(s.scopeVocabulary()))
}

func (s *Server) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.oauth.JWKS())
}

func (s *Server) tokenEndpoint(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	resp, oerr := s.oauth.Exchange(r.Context(), oauthas.ParseTokenForm(r))
	if oerr != nil {
		oauthas.ErrorWriter(w, oerr)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) registerEndpoint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.RedirectURIs) == 0 {
		http.Error(w, `{"error":"invalid_client_metadata"}`, http.StatusBadRequest)
		return
	}
	name := body.ClientName
	if name == "" {
		name = "registered client"
	}
	client, err := s.oauthStore.RegisterClient(r.Context(), name, body.RedirectURIs)
	if err != nil {
		http.Error(w, `{"error":"invalid_client_metadata"}`, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ID,
		"client_secret":              client.Secret,
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"token_endpoint_auth_method": "client_secret_post",
	})
}
