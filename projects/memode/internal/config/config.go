// Package config loads memode configuration from the environment, optionally
// seeded from a .env file in the working directory.
package config

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joho/godotenv"

	"codebase.bid/projects/memode/internal/oauthstore"
)

// Client is one OAuth client of memode: the owner's harness and any other
// agent frontend. Secret may be empty for public (PKCE-only) clients.
type Client struct {
	ClientID     string   `json:"client_id"`
	Secret       string   `json:"client_secret,omitempty"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// Config is the full server configuration.
type Config struct {
	ListenAddr           string // MEMODE_LISTEN, default "127.0.0.1:8300"
	BaseURL              string // MEMODE_BASE_URL, external URL for OAuth endpoints
	DBPath               string // MEMODE_DB, SQLite path
	OwnerPass            string // MEMODE_OWNER_PASSWORD, basic-auth on consent/authorize
	AllowUnauthenticated bool   // MEMODE_ALLOW_UNAUTHENTICATED, local dev escape hatch

	ICloudUser     string // MEMODE_ICLOUD_USER
	ICloudPassword string // MEMODE_ICLOUD_APP_PASSWORD (mail, CalDAV, CardDAV)

	KagiAPIKey string // MEMODE_KAGI_API_KEY

	// Disabled lists services that stay off even when configured.
	Disabled map[string]bool // MEMODE_DISABLE_SERVICES=mail,telegram

	TelegramAPIID    string // MEMODE_TELEGRAM_API_ID
	TelegramAPIHash  string // MEMODE_TELEGRAM_API_HASH
	TelegramPhone    string // MEMODE_TELEGRAM_PHONE
	TelegramPassword string // MEMODE_TELEGRAM_PASSWORD (2FA)

	Clients []Client // MEMODE_CLIENTS, JSON file of OAuth clients
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment after loading .env (if
// present), validates it and returns it.
func Load() (Config, error) {
	_ = godotenv.Load() // best effort; a missing .env is fine
	home, _ := os.UserHomeDir()
	c := Config{
		ListenAddr:           env("MEMODE_LISTEN", "127.0.0.1:8300"),
		BaseURL:              strings.TrimRight(env("MEMODE_BASE_URL", ""), "/"),
		DBPath:               env("MEMODE_DB", filepath.Join(home, ".local", "share", "memode", "memode.db")),
		OwnerPass:            env("MEMODE_OWNER_PASSWORD", ""),
		AllowUnauthenticated: env("MEMODE_ALLOW_UNAUTHENTICATED", "") == "1",

		ICloudUser:     env("MEMODE_ICLOUD_USER", ""),
		ICloudPassword: env("MEMODE_ICLOUD_APP_PASSWORD", ""),

		KagiAPIKey: env("MEMODE_KAGI_API_KEY", ""),
		Disabled:   map[string]bool{},

		TelegramAPIID:    env("MEMODE_TELEGRAM_API_ID", ""),
		TelegramAPIHash:  env("MEMODE_TELEGRAM_API_HASH", ""),
		TelegramPhone:    env("MEMODE_TELEGRAM_PHONE", ""),
		TelegramPassword: env("MEMODE_TELEGRAM_PASSWORD", ""),
	}
	for _, name := range strings.Split(env("MEMODE_DISABLE_SERVICES", ""), ",") {
		if name = strings.TrimSpace(name); name != "" {
			c.Disabled[name] = true
		}
	}
	if c.BaseURL == "" {
		c.BaseURL = "http://" + c.ListenAddr
	}
	if path := os.Getenv("MEMODE_CLIENTS"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return c, fmt.Errorf("read MEMODE_CLIENTS %s: %w", path, err)
		}
		if err := json.Unmarshal(data, &c.Clients); err != nil {
			return c, fmt.Errorf("parse %s: %w", path, err)
		}
	} else {
		c.Clients = []Client{{ClientID: "memode-local", Name: "Local harness", RedirectURIs: []string{"http://127.0.0.1/callback"}}}
	}
	return c, nil
}

// ServiceConfigured reports which services have their upstream credentials
// set and are not explicitly disabled; other services are not registered at
// all.
func (c Config) ServiceConfigured() map[string]bool {
	all := map[string]bool{
		"mail":     c.ICloudUser != "" && c.ICloudPassword != "",
		"calendar": c.ICloudUser != "" && c.ICloudPassword != "",
		"contacts": c.ICloudUser != "" && c.ICloudPassword != "",
		"telegram": c.TelegramAPIID != "" && c.TelegramAPIHash != "" && c.TelegramPhone != "",
		"search":   c.KagiAPIKey != "",
	}
	for name := range c.Disabled {
		all[name] = false
	}
	return all
}

// FullGrants returns the grant matrix giving write access to every configured
// service: the default in local trusted mode (no OAuth middleware).
func (c Config) FullGrants() oauthstore.Grants {
	g := oauthstore.Grants{}
	for svc, configured := range c.ServiceConfigured() {
		if configured {
			g[svc] = "write"
		}
	}
	return g
}

// IMAPAddr is the IMAPS address to connect to.
func (c Config) IMAPAddr() string {
	if v := os.Getenv("MEMODE_IMAP_ADDR"); v != "" {
		return v // tests and non-iCloud providers
	}
	return "imap.mail.me.com:993"
}

// SMTPAddr is the SMTP submission address.
func (c Config) SMTPAddr() string {
	if v := os.Getenv("MEMODE_SMTP_ADDR"); v != "" {
		return v
	}
	return "smtp.mail.me.com:587"
}

// KagiEndpoint overrides the Kagi Search API URL (used by tests).
func (c Config) KagiEndpoint() string {
	return os.Getenv("MEMODE_KAGI_ENDPOINT")
}

// CalDAVEndpoint is the CalDAV service root.
func (c Config) CalDAVEndpoint() string {
	if v := os.Getenv("MEMODE_CALDAV_ENDPOINT"); v != "" {
		return v
	}
	return "https://caldav.icloud.com/"
}

// CardDAVEndpoint is the CardDAV service root.
func (c Config) CardDAVEndpoint() string {
	if v := os.Getenv("MEMODE_CARDDAV_ENDPOINT"); v != "" {
		return v
	}
	return "https://carddav.icloud.com/"
}

// IMAPInsecureTLS skips IMAPS certificate verification. Tests with
// self-signed containers only; never enable against iCloud.
func (c Config) IMAPInsecureTLS() *tls.Config {
	if os.Getenv("MEMODE_IMAP_INSECURE_TLS") == "1" {
		return &tls.Config{InsecureSkipVerify: true}
	}
	return nil
}

// SMTPHost is the hostname part of SMTPAddr, for smtp.PlainAuth.
func (c Config) SMTPHost() string {
	addr := c.SMTPAddr()
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[:i]
	}
	return addr
}

// Validate returns an error describing every missing-but-required credential.
// No upstream credentials at all is legal (server still runs OAuth + status).
func (c Config) Validate() error {
	var missing []string
	if c.TelegramAPIID != "" || c.TelegramAPIHash != "" || c.TelegramPhone != "" {
		for k, v := range map[string]string{
			"MEMODE_TELEGRAM_API_ID":   c.TelegramAPIID,
			"MEMODE_TELEGRAM_API_HASH": c.TelegramAPIHash,
			"MEMODE_TELEGRAM_PHONE":    c.TelegramPhone,
		} {
			if v == "" {
				missing = append(missing, k)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return fmt.Errorf("incomplete telegram credentials: missing %s", strings.Join(missing, ", "))
	}
	return nil
}
