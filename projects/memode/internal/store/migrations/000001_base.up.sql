-- 001_base: settings key/value store, OAuth client registrations, grants,
-- authorization codes and refresh tokens.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value BLOB NOT NULL
);

-- RFC 7591 registrations plus statically configured clients.
CREATE TABLE oauth_clients (
    client_id     TEXT PRIMARY KEY,
    secret        TEXT NOT NULL,          -- empty for public clients
    name          TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,          -- JSON array
    created_at    INTEGER NOT NULL
);

-- The owner's consent: which services this client may use, read and/or write.
-- services is a JSON object: {"mail":"read"|"write", ...} (absent = no access).
CREATE TABLE oauth_grants (
    client_id  TEXT PRIMARY KEY REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    services   TEXT NOT NULL,
    granted_at INTEGER NOT NULL
);

CREATE TABLE oauth_codes (
    code            TEXT PRIMARY KEY,
    client_id       TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    redirect_uri    TEXT NOT NULL,
    challenge       TEXT NOT NULL,         -- PKCE S256
    challenge_scope TEXT NOT NULL,
    expires_at      INTEGER NOT NULL
);

CREATE TABLE oauth_refresh_tokens (
    token      TEXT PRIMARY KEY,
    client_id  TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    scopes     TEXT NOT NULL,              -- JSON array
    expires_at INTEGER NOT NULL
);
