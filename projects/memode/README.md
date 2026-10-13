# memode

MeMode is a unified MCP server that gives a personal-assistant agent access to
the owner's own services:

| Service | Protocol | Read | Write |
|---|---|---|---|
| iCloud Mail | IMAP (local full-text index, continuous sync) + SMTP | ✅ | ✅ send, flags, move, delete |
| iCloud Calendar | CalDAV | ✅ | ✅ create/update/delete events |
| iCloud Contacts | CardDAV | ✅ | ✅ create/update/delete contacts |
| Telegram | MTProto user session (`gotd/td`) | ✅ | ✅ send, edit, delete, mark read |
| Kagi Search | HTTPS API | ✅ | — |

It is **Code Mode** oriented: instead of dozens of MCP tools, it exposes **two**
tools that each run a Lua script.

- `memode.read` — read-only: only non-mutating functions are registered.
- `memode.write` — read and write.

Both take `{ script, max_output_bytes?, timeout_ms? }` and return the script's
`print()` output and return value. Scripts filter and aggregate inside the
sandbox, so only what matters reaches the model.

Access is OAuth-gated: memode is its own OAuth 2.1 authorization server, and
the consent screen lets the owner pick **which services** an agent may use and
**how far** (read vs. read & write) per service.

## Quick start

```sh
cp .env.example .env       # fill in what you want to enable
go run ./projects/memode/cmd/memode        # or: bazel run //projects/memode/cmd/memode
```

The server listens on `MEMODE_LISTEN` (default `127.0.0.1:8300`) and serves:

| Path | Purpose |
|---|---|
| `/` | MCP over streamable HTTP |
| `/.well-known/oauth-protected-resource` | RFC 9728 metadata |
| `/.well-known/oauth-authorization-server` | RFC 8414 metadata |
| `/authorize` | consent screen (owner picks services + access level) |
| `/token` | authorization-code (PKCE S256) and refresh-token grants |
| `/register` | RFC 7591 dynamic client registration |
| `/keys` | JWKS for access-token verification |

Point your agent harness at the MCP endpoint; on first connect it will be
challenged with `401` and a `WWW-Authenticate` header pointing at the
protected-resource metadata, then run the standard OAuth dance. The consent
page is protected with basic auth (`memode` / `MEMODE_OWNER_PASSWORD`) whenever
that variable is set — set it as soon as the port is reachable beyond
localhost.

## Linux deployment

Bazel builds multi-architecture OCI images and Debian/RPM packages:

```sh
bazel build //projects/memode/cmd/memode:memode_image
bazel build //projects/memode/cmd/memode:memode_linux_packages
```

The package installs `/usr/bin/memode`, `/etc/memode/memode.env`, and
`memode.service`. Review the environment file, set `MEMODE_BASE_URL` to the
public URL and `MEMODE_OWNER_PASSWORD`, then enable with
`systemctl enable --now memode`. The service runs as the unprivileged `memode`
user; its SQLite database and Telegram session persist in `/var/lib/memode`.

The OCI image is stateless by default. Set `MEMODE_LISTEN`, `MEMODE_BASE_URL`,
`MEMODE_DB` (for example `/data/memode.db`), secrets and optional client
configuration in the runtime environment, and mount persistent writable storage
at the database/session path. Do not enable it publicly without an owner
password and TLS-terminating reverse proxy.

## Configuration

All configuration is environment based (`MEMODE_*`), optionally via `.env`.

| Variable | Meaning |
|---|---|
| `MEMODE_LISTEN` | listen address, default `127.0.0.1:8300` |
| `MEMODE_BASE_URL` | external URL used in OAuth metadata/redirects, default `http://<listen>` |
| `MEMODE_DB` | SQLite path, default `~/.local/share/memode/memode.db` |
| `MEMODE_OWNER_PASSWORD` | basic-auth password for `/authorize` (owner consent) |
| `MEMODE_ALLOW_UNAUTHENTICATED` | `1` disables token verification on the MCP endpoint (local development only) |
| `MEMODE_DISABLE_SERVICES` | comma list of services to keep off even when configured, e.g. `mail,telegram` |
| `MEMODE_ICLOUD_USER` / `MEMODE_ICLOUD_APP_PASSWORD` | Apple ID + **app-specific password** (Apple does not offer OAuth for third-party Mail/Calendar/Contacts access) |
| `MEMODE_KAGI_API_KEY` | Kagi Search API key |
| `MEMODE_TELEGRAM_API_ID` / `_API_HASH` / `_PHONE` / `_PASSWORD` | Telegram app credentials and account; a service with any of these set requires all of them |
| `MEMODE_CLIENTS` | path to a JSON file of static OAuth clients (see below) |
| `MEMODE_IMAP_ADDR` / `MEMODE_SMTP_ADDR` | override mail endpoints (default `imap.mail.me.com:993`, `smtp.mail.me.com:587`) |
| `MEMODE_CALDAV_ENDPOINT` / `MEMODE_CARDDAV_ENDPOINT` | override DAV roots (default `https://caldav.icloud.com/`, `https://carddav.icloud.com/`) |
| `MEMODE_KAGI_ENDPOINT` | override the Kagi API URL |
| `MEMODE_IMAP_INSECURE_TLS` | `1` skips IMAPS certificate verification (test containers only) |

A service is enabled only when its credentials are configured (and it is not in
`MEMODE_DISABLE_SERVICES`). `MEMODE_TELEGRAM_TESTS=1` enables the Telegram
smoke test.

### Static OAuth clients

```json
[
  {
    "client_id": "my-harness",
    "client_secret": "a-long-random-string",
    "name": "My agent harness",
    "redirect_uris": ["http://127.0.0.1:8765/callback"]
  }
]
```

Without `MEMODE_CLIENTS`, a permissive `memode-local` client (redirect
`http://127.0.0.1/callback`) is registered. Public clients (no secret) are
allowed and must use PKCE — as must confidential ones.

Scopes are `<service>:read` and `<service>:write` for `mail`, `calendar`,
`contacts` and `telegram`, plus `search:read`.

## First run: Telegram login

Telegram uses your **user account** (MTProto), so the first start asks for the
login code Telegram sends to your other sessions (and your 2FA password, unless
`MEMODE_TELEGRAM_PASSWORD` is set) on stdin:

```
memode telegram: login code sent to your Telegram app:
```

The authorized session is cached in `<MEMODE_DB>.telegram.session`, so later
starts are non-interactive. Everything else (iCloud app password, Kagi key)
is configured up front.

## Writing scripts

Call `help()` to list the available functions with their descriptions, then
call them as namespaced functions. A single script can chain calls — the whole
point of Code Mode:

```lua
-- memode.read
local hits = kagi.search({query = "kumquat season"})
for _, h in ipairs(hits) do print(h.title, h.url) end

local events = calendar.events({from = "2026-11-01T00:00:00Z", to = "2026-12-01T00:00:00Z"})
return {search = #hits, events = #events}
```

```lua
-- memode.write
local ev = calendar.upsert_event({
  summary = "Kumquat tasting",
  start = "2026-11-03T09:00:00Z",
  end_time = "2026-11-03T11:00:00Z",     -- "end" is a Lua keyword: use end_time
  location = "Orchard",
})
local ct = contacts.upsert({full_name = "Alice Appleseed", email = "alice@example.com"})
telegram.send({chat = "@alice", text = "Tasting confirmed for Tuesday."})
mail.send({to = "alice@example.com", subject = "Kumquat tasting", body = "See you Tuesday."})
return {event = ev.path, contact = ct.path}
```

### API surface

| Function | Kind | Notes |
|---|---|---|
| `mail.mailboxes()` | read | mailboxes with message counts |
| `mail.search({query, mailbox?, limit?, offset?})` | read | FTS5 query — supports FTS syntax (`"exact phrase"`, `AND`, `OR`, `NOT`, `prefix*`) |
| `mail.recent({mailbox?, limit?})` | read | newest messages |
| `mail.get({id})` | read | full message including body text |
| `mail.send({to, cc?, subject, body})` | write | sends, then syncs INBOX/Sent before returning |
| `mail.set_flags({id, seen?, flagged?})` | write | |
| `mail.move({id, to})` / `mail.delete({id})` | write | |
| `mail.sync({mailboxes?})` | write | force a synchronous re-sync |
| `calendar.calendars()` | read | |
| `calendar.events({calendar?, from, to})` | read | server-side time-range query |
| `calendar.get({path})` | read | |
| `calendar.upsert_event({calendar?, uid?, summary, start, end_time, location?, description?, all_day?})` | write | pass `uid` to update |
| `calendar.delete_event({path})` | write | |
| `contacts.groups()` / `contacts.list({group?})` | read | |
| `contacts.search({query, group?})` | read | substring over name/email/phone/org |
| `contacts.get({path})` | read | |
| `contacts.upsert({group?, uid?, full_name, email?, phone?, org?, note?})` | write | pass `uid` to update |
| `contacts.delete({path})` | write | |
| `telegram.dialogs({limit?})` | read | chat keys, titles, unread counts |
| `telegram.history({chat, limit?, before_id?})` | read | |
| `telegram.search({query, chat?, limit?})` | read | |
| `telegram.send({chat, text, reply_to?})` | write | |
| `telegram.edit({chat, message_id, text})` | write | |
| `telegram.delete({chat, message_ids, for_everyone?})` | write | |
| `telegram.mark_read({chat})` | write | |
| `kagi.search({query, limit?})` | read | up to ten web results |
| `status()` | both | version, per-service configured/ready state |

`chat` accepts a numeric id, an `@username`, or a title substring when it is
unambiguous.

### Script environment limits

- Lua 5.2 (Shopify/go-lua) — **no `string.match`, `string.gsub` or
  `string.gmatch`**, and `string.find` does plain substring search only
  (patterns like `"%.ics$"` never match). Use `string.sub`, plain `find` and
  `ipairs`/`pairs` instead.
- Tables, `string`, `math` and the base library are available; there is no
  `io`, `os`, `require`, no filesystem and no network beyond the registered
  functions.
- `end` is a Lua keyword, so event end times are `end_time`, and any key named
  `end` must be written as `["end"]`.
- Output is capped (default 1 MiB, `max_output_bytes`) and truncated output is
  flagged; scripts are killed at the deadline (default 30 s, `timeout_ms`) by a
  VM instruction hook, and `sleep(ms)` is capped at 10 s per call.

## Architecture

```
projects/memode/
  cmd/memode/                 server entry point: config, store, services, HTTP
  internal/config/            env configuration
  internal/store/             SQLite open/migrate (settings, OAuth, mail index)
  internal/oauthstore/        OAuth clients, grants, codes, refresh tokens
  internal/server/            MCP server, the two code-mode tools, OAuth endpoints, consent UI
  internal/services/          service wiring + mail, calendar, contacts, telegram, kagi
  internal/davtest/           in-memory CalDAV/CardDAV servers for tests
lib/go/codemode/              generic go-lua code-mode host (sandbox, bridge, registry)
lib/go/oauthas/               generic OAuth 2.1 authorization-server primitives
lib/go/dav/                   CalDAV/CardDAV client wrappers (discovery, typed events/contacts)
lib/go/kagi/                  Kagi Search client (reused as-is)
```

Adding a service is one package plus one line in
`internal/services/registry.go`: implement `Register(*codemode.Registry)` and
`Setup(ctx, ready func(bool))`, then add it to `Enabled`.

Mail keeps a local SQLite index of every mailbox (schema: `mail_messages` +
`mail_fts`), synced continuously by a worker that IDLEs on INBOX and re-syncs
all mailboxes every ten minutes. Writes go to IMAP/SMTP first and then re-sync
the affected mailboxes synchronously, so the next read in the same script
already sees the change. The `mail_messages.embedding` column is reserved for
the future embedding-based search release; at personal-mail scale a brute-force
cosine scan in Go will be enough, and `ncruces/go-sqlite3` + `sqlite-vec` is the
escape hatch if it ever is not.

### Known limits / deliberate shortcuts

- **Only INBOX is watched by IDLE**; other mailboxes refresh on the 10-minute
  cycle and after writes.
- **No OAuth to Apple.** Apple offers no third-party OAuth for Mail, Calendar
  or Contacts; an app-specific password is the supported route. memode's own
  OAuth server is what scopes the agent.
- **Telegram session is a file** (`<MEMODE_DB>.telegram.session`) rather than a
  SQLite table: one less schema, and gotd's own storage handles it.
- **SMTP uses `net/smtp`** (which upgrades with STARTTLS when the server offers
  it) instead of `emersion/go-smtp`: fewer moving parts for plain text sending.
- **Telegram sends plain text only** (no markdown parsing, attachments or
  replies to messages you do not own).
- HTML-only mail bodies are indexed after crude tag stripping.
- Search and DAV calls are executed serially per script; `codemode` runs one
  script per call.

## Tests

```sh
go test ./projects/memode/... ./lib/go/codemode/... ./lib/go/oauthas/... ./lib/go/dav/...
bazel test //projects/memode/...     # repo-standard build and test entry point
```

- `lib/go/codemode` — sandbox behaviour: read-only environments, timeouts,
  output caps, JSON/Lua bridging, empty-collection handling.
- `lib/go/oauthas` — PKCE exchange, single-use codes and refresh tokens,
  rotation, tampered-token rejection, authorize-request validation.
- `lib/go/dav` — client wrappers against go-webdav's own CalDAV/CardDAV server.
- `internal/store`, `internal/services/mail` — SQLite schema, FTS search, flags,
  moves, deletes.
- `internal/services/mail` integration (testcontainers, skipped with `-short`)
  — GreenMail IMAP/SMTP container: send → sync → search → flags → move → delete.
- `internal/services` integration — calendar/contacts services driven through
  the real Lua surface against live DAV servers.
- `internal/server` — OAuth dance over HTTP with a scripted client, MCP
  transport, scope gating, and a full end-to-end test (config → services →
  OAuth consent → token → MCP → Lua → DAV/search).
- `internal/services/telegram` — helpers, cache-based chat resolution and the
  read/write split; a credentialed smoke test runs with
  `MEMODE_TELEGRAM_TESTS=1`.

The container-backed DAV test uses go-webdav's server over `httptest` rather
than a Baïkal container: Baïkal has no unattended installer (it needs its web
setup wizard), which would make the test fragile for no extra protocol
coverage.

### Manual verification against real accounts

The automated tests cover every layer with fake upstreams. To verify against
real accounts, set the credentials above, start the server, complete the
consent screen in a browser (or drive `/authorize` with your harness), then
through `memode.write`:

1. `mail.send({to = "<your other address>", subject = "memode test", body = "hello"})`
   and confirm the message appears in `mail.search({query = "memode test"})`
   immediately after the same call returns.
2. `calendar.upsert_event({summary = "memode test", start = ..., end_time = ...})`,
   check the event on your phone, then `calendar.delete_event`.
3. `contacts.upsert({full_name = "memode test"})`, check it on your phone, then
   `contacts.delete`.
4. `telegram.dialogs({})`, then `telegram.send({chat = "<a chat>", text = "memode test"})`.
5. `kagi.search({query = "kumquat"})`.
