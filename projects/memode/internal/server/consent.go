package server

import (
	"net/http"
	"net/url"

	"codebase.bid/projects/memode/internal/oauthstore"
)

// consentForm shows the owner which services the requesting client wants and
// at what access level, then approves or denies.
func (s *Server) consentForm(w http.ResponseWriter, r *http.Request) {
	s.serveConsent(w, r, url.Values(r.URL.Query()), "")
}

func (s *Server) serveConsent(w http.ResponseWriter, r *http.Request, q url.Values, formErr string) {
	req, err := s.oauth.ParseAuthorize(r.Context(), q)
	if err != nil {
		http.Error(w, "authorization request invalid: "+err.Error(), http.StatusBadRequest)
		return
	}
	client, _ := s.oauthStore.GetClient(r.Context(), req.ClientID)
	name := req.ClientID
	if client != nil && client.Name != "" {
		name = client.Name
	}

	var services []consentService
	labels := map[string]string{"mail": "Mail", "calendar": "Calendar", "contacts": "Contacts", "telegram": "Telegram", "search": "Kagi Search"}
	for _, key := range []string{"mail", "calendar", "contacts", "telegram", "search"} {
		if s.cfg.ServiceConfigured()[key] {
			services = append(services, consentService{Key: key, Label: labels[key]})
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = consentPage(consentViewModel{
		Client:      name,
		Services:    services,
		Error:       formErr,
		ClientID:    req.ClientID,
		RedirectURI: req.RedirectURI,
		State:       req.State,
		Challenge:   req.Challenge,
	}).Render(r.Context(), w)
}

func (s *Server) consentSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	q := url.Values{
		"client_id":             {r.PostForm.Get("client_id")},
		"redirect_uri":          {r.PostForm.Get("redirect_uri")},
		"response_type":         {"code"},
		"state":                 {r.PostForm.Get("state")},
		"code_challenge":        {r.PostForm.Get("code_challenge")},
		"code_challenge_method": {"S256"},
	}
	req, err := s.oauth.ParseAuthorize(r.Context(), q)
	if err != nil {
		http.Error(w, "authorization request invalid: "+err.Error(), http.StatusBadRequest)
		return
	}

	if r.PostForm.Get("deny") != "" {
		http.Redirect(w, r, denyRedirect(req.RedirectURI, req.State), http.StatusFound)
		return
	}

	grants := oauthstore.Grants{}
	for _, svc := range []string{"mail", "calendar", "contacts", "telegram", "search"} {
		switch r.PostForm.Get("svc_" + svc) {
		case "read", "write":
			if !s.cfg.ServiceConfigured()[svc] {
				continue
			}
			if svc == "search" && r.PostForm.Get("svc_"+svc) == "write" {
				continue // search is read-only
			}
			grants[svc] = r.PostForm.Get("svc_" + svc)
		}
	}
	if len(grants) == 0 {
		s.serveConsent(w, r, q, "select at least one service, or deny")
		return
	}
	if err := s.oauthStore.SaveGrant(r.Context(), req.ClientID, grants); err != nil {
		http.Error(w, "save grant: "+err.Error(), http.StatusInternalServerError)
		return
	}
	redirect, err := s.oauth.IssueCode(r.Context(), req, grants.Scopes())
	if err != nil {
		http.Error(w, "issue code: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirect, http.StatusFound)
}

func denyRedirect(redirectURI, state string) string {
	u, _ := url.Parse(redirectURI)
	q := u.Query()
	q.Set("error", "access_denied")
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
