package oauthstore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"codebase.bid/lib/go/oauthas"
)

// Store persists OAuth clients, grants, codes and refresh tokens in SQLite.
type Store struct {
	db *sql.DB
}

// New wraps an open database (tables from migration 001).
func New(db *sql.DB) *Store { return &Store{db: db} }

var _ oauthas.ClientStore = (*Store)(nil)
var _ oauthas.CodeStore = (*Store)(nil)
var _ oauthas.TokenStore = (*Store)(nil)

// EnsureClients inserts or updates statically configured clients.
func (s *Store) EnsureClients(ctx context.Context, clients []ClientConfig) error {
	for _, c := range clients {
		redirects, err := json.Marshal(c.RedirectURIs)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_clients (client_id, secret, name, redirect_uris, created_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(client_id) DO UPDATE SET secret = excluded.secret, name = excluded.name, redirect_uris = excluded.redirect_uris`,
			c.ClientID, c.Secret, c.Name, string(redirects), time.Now().Unix())
		if err != nil {
			return fmt.Errorf("ensure client %s: %w", c.ClientID, err)
		}
	}
	return nil
}

// ClientConfig mirrors config.Client without the import cycle.
type ClientConfig struct {
	ClientID     string
	Secret       string
	Name         string
	RedirectURIs []string
}

// RegisterClient dynamically registers a client (RFC 7591).
func (s *Store) RegisterClient(ctx context.Context, name string, redirectURIs []string) (*oauthas.Client, error) {
	id, err := oauthas.RandomToken()
	if err != nil {
		return nil, err
	}
	secret, err := oauthas.RandomToken()
	if err != nil {
		return nil, err
	}
	redirects, err := json.Marshal(redirectURIs)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO oauth_clients (client_id, secret, name, redirect_uris, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, secret, name, string(redirects), time.Now().Unix()); err != nil {
		return nil, err
	}
	return &oauthas.Client{ID: id, Secret: secret, Name: name, RedirectURIs: redirectURIs}, nil
}

// GetClient implements oauthas.ClientStore.
func (s *Store) GetClient(ctx context.Context, id string) (*oauthas.Client, error) {
	row := s.db.QueryRowContext(ctx, `SELECT secret, name, redirect_uris FROM oauth_clients WHERE client_id = ?`, id)
	var secret, name, redirectsJSON string
	if err := row.Scan(&secret, &name, &redirectsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var redirects []string
	if err := json.Unmarshal([]byte(redirectsJSON), &redirects); err != nil {
		return nil, err
	}
	return &oauthas.Client{ID: id, Secret: secret, Name: name, RedirectURIs: redirects}, nil
}

// PutCode implements oauthas.CodeStore.
func (s *Store) PutCode(ctx context.Context, c oauthas.Code) error {
	scopes, err := json.Marshal(c.Scopes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_codes (code, client_id, redirect_uri, challenge, challenge_scope, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.Code, c.ClientID, c.RedirectURI, c.Challenge, string(scopes), c.ExpiresAt.Unix())
	return err
}

// TakeCode implements oauthas.CodeStore: the code is consumed in one read.
func (s *Store) TakeCode(ctx context.Context, code string) (*oauthas.Code, error) {
	rows, err := s.db.QueryContext(ctx, `DELETE FROM oauth_codes WHERE code = ? RETURNING client_id, redirect_uri, challenge, challenge_scope, expires_at`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var c oauthas.Code
	var scopesJSON string
	var expires int64
	if err := rows.Scan(&c.ClientID, &c.RedirectURI, &c.Challenge, &scopesJSON, &expires); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &c.Scopes); err != nil {
		return nil, err
	}
	c.ExpiresAt = time.Unix(expires, 0)
	return &c, nil
}

// PutRefresh implements oauthas.TokenStore.
func (s *Store) PutRefresh(ctx context.Context, r oauthas.Refresh) error {
	scopes, err := json.Marshal(r.Scopes)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_refresh_tokens (token, client_id, scopes, expires_at) VALUES (?, ?, ?, ?)`,
		r.Token, r.ClientID, string(scopes), r.ExpiresAt.Unix())
	return err
}

// TakeRefresh implements oauthas.TokenStore: single-use, consumed on read.
func (s *Store) TakeRefresh(ctx context.Context, token string) (*oauthas.Refresh, error) {
	rows, err := s.db.QueryContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE token = ? RETURNING client_id, scopes, expires_at`, token)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var r oauthas.Refresh
	var scopesJSON string
	var expires int64
	if err := rows.Scan(&r.ClientID, &scopesJSON, &expires); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &r.Scopes); err != nil {
		return nil, err
	}
	r.ExpiresAt = time.Unix(expires, 0)
	return &r, nil
}

// SaveGrant records the owner's consent for a client.
func (s *Store) SaveGrant(ctx context.Context, clientID string, g Grants) error {
	services, err := json.Marshal(g)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO oauth_grants (client_id, services, granted_at) VALUES (?, ?, ?)
		ON CONFLICT(client_id) DO UPDATE SET services = excluded.services, granted_at = excluded.granted_at`,
		clientID, string(services), time.Now().Unix())
	return err
}

// GetGrant returns the stored consent for a client, or nil.
func (s *Store) GetGrant(ctx context.Context, clientID string) (Grants, error) {
	row := s.db.QueryRowContext(ctx, `SELECT services FROM oauth_grants WHERE client_id = ?`, clientID)
	var servicesJSON string
	if err := row.Scan(&servicesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var g Grants
	if err := json.Unmarshal([]byte(servicesJSON), &g); err != nil {
		return nil, err
	}
	return g, nil
}

// SigningKey loads the RSA signing key from settings, generating and
// persisting one on first use.
func (s *Store) SigningKey() (*rsa.PrivateKey, error) {
	var pem []byte
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'jwt_key'`).Scan(&pem)
	if err == nil {
		return x509.ParsePKCS1PrivateKey(pem)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('jwt_key', ?)`, x509.MarshalPKCS1PrivateKey(key)); err != nil {
		return nil, err
	}
	return key, nil
}
