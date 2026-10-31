package auth

import (
	"net/http"
	"regexp"
	"strings"
	"time"

	"codebase.bid/lib/go/oauthas"
	"codebase.bid/projects/memory/internal/model"
)

var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.ParseForm() != nil || r.PostForm.Get("resource") != s.Resource() {
		oauthError(w, "invalid_target", 400)
		return
	}
	clientID := r.PostForm.Get("client_id")
	client, e := s.Client(r.Context(), clientID)
	if e != nil || client == nil {
		oauthError(w, "invalid_client", 400)
		return
	}
	tx, e := s.DB.Begin(r.Context())
	if e != nil {
		oauthError(w, "server_error", 500)
		return
	}
	defer tx.Rollback(r.Context())
	var grant, grantClient, family string
	var scopes, spaces []string
	var expires time.Time
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		var redirect, challenge string
		e = tx.QueryRow(r.Context(), `DELETE FROM memory_auth.codes c USING memory_auth.grants g WHERE c.hash=$1 AND g.id=c.grant_id AND g.resource=$2 RETURNING g.id::text,g.client_id,g.scopes,g.spaces,c.redirect,c.challenge,c.expires_at`, hash(r.PostForm.Get("code")), s.Resource()).Scan(&grant, &grantClient, &scopes, &spaces, &redirect, &challenge, &expires)
		if e != nil || grantClient != clientID || redirect != r.PostForm.Get("redirect_uri") || time.Now().After(expires) || !verifierPattern.MatchString(r.PostForm.Get("code_verifier")) || !oauthas.VerifyPKCE(challenge, r.PostForm.Get("code_verifier")) {
			_ = tx.Commit(r.Context())
			oauthError(w, "invalid_grant", 400)
			return
		}
		family = model.NewID()
		expires = time.Now().Add(30 * 24 * time.Hour)
	case "refresh_token":
		var consumed bool
		e = tx.QueryRow(r.Context(), `SELECT f.grant_id::text,g.client_id,g.scopes,g.spaces,f.family::text,f.consumed,f.expires_at FROM memory_auth.refresh f JOIN memory_auth.grants g ON g.id=f.grant_id WHERE f.hash=$1 AND g.resource=$2 FOR UPDATE OF f`, hash(r.PostForm.Get("refresh_token")), s.Resource()).Scan(&grant, &grantClient, &scopes, &spaces, &family, &consumed, &expires)
		if e != nil {
			oauthError(w, "invalid_grant", 400)
			return
		}
		if consumed {
			_, e = tx.Exec(r.Context(), `UPDATE memory_auth.grants SET revoked=true WHERE id=$1`, grant)
			if e == nil {
				_ = tx.Commit(r.Context())
			}
			oauthError(w, "invalid_grant", 400)
			return
		}
		if grantClient != clientID || time.Now().After(expires) {
			oauthError(w, "invalid_grant", 400)
			return
		}
		_, e = tx.Exec(r.Context(), `UPDATE memory_auth.refresh SET consumed=true WHERE hash=$1`, hash(r.PostForm.Get("refresh_token")))
		if e != nil {
			oauthError(w, "server_error", 500)
			return
		}
	default:
		oauthError(w, "unsupported_grant_type", 400)
		return
	}
	var revoked bool
	if tx.QueryRow(r.Context(), `SELECT revoked FROM memory_auth.grants WHERE id=$1 FOR UPDATE`, grant).Scan(&revoked) != nil || revoked {
		oauthError(w, "invalid_grant", 400)
		return
	}
	access, e := s.access(grant, clientID)
	if e != nil {
		oauthError(w, "server_error", 500)
		return
	}
	refresh := token()
	_, e = tx.Exec(r.Context(), `INSERT INTO memory_auth.refresh(hash,grant_id,family,expires_at) VALUES($1,$2,$3,$4)`, hash(refresh), grant, family, expires)
	if e != nil || tx.Commit(r.Context()) != nil {
		oauthError(w, "server_error", 500)
		return
	}
	writeJSON(w, 200, oauthas.TokenResponse{AccessToken: access, TokenType: "Bearer", ExpiresIn: 900, RefreshToken: refresh, Scope: strings.Join(scopes, " ")})
}
