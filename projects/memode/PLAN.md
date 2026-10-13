# MeMode — Unified Personal-Assistant MCP Server

## Context

Build `projects/memode/`: a Go MCP server ("MeMode") giving a personal-assistant
agent unified access to the owner's personal services:

- **iCloud Mail** (read & write incl. send) — local index with continuous IMAP
  sync of everything; writes go to IMAP/SMTP directly, then trigger a
  synchronous re-sync so the local view stays consistent.
- **iCloud Calendar** (read & write, CalDAV)
- **iCloud Contacts** (read & write, CardDAV)
- **Telegram** (read & write, user account via MTProto)
- **Kagi search**

Design decisions (confirmed with user):

1. **Code Mode**: follow the Cloudflare/pi codemode pattern but with Lua — the
   server exposes two MCP tools, each taking a Lua script (Shopify/go-lua) that
   calls registered service functions; only the script's output returns to the
   model. Read/write enforcement is at the host boundary: write functions are
   simply not registered in the read-only tool's Lua environment.
2. **MeMode is an OAuth provider.** Upstream service credentials (iCloud
   app-specific password, Kagi API key, Telegram API id/hash) are configured
   globally by the owner. Agent clients authorize against MeMode via OAuth 2.1;
   the consent screen lets the owner pick which services the agent gets, and
   read vs. read/write per service. Scopes: `mail:read`, `mail:write`,
   `calendar:read`, `calendar:write`, `contacts:read`, `contacts:write`,
   `telegram:read`, `telegram:write`, `search:read` (search is read-only).
3. **Telegram**: user account via MTProto (`gotd/td`).
4. **Transport**: HTTP MCP server (streamable HTTP). Primary client is a custom
   harness, not pi.
5. **Mail**: sync everything, full-text-indexed; architecture prepared for
   future embedding-based search (no embeddings in v1).

## Approach

### Dependencies (add to go.mod / MODULE.bazel)

| Purpose | Package |
|---|---|
| MCP server + HTTP transport | `github.com/modelcontextprotocol/go-sdk` (official SDK; `mcp` + `auth` + `oauthex` packages) |
| Lua engine | `github.com/Shopify/go-lua` (note: lightly maintained; acceptable, pure Go) |
| IMAP | `github.com/emersion/go-imap/v2` (IDLE built in; restart IDLE every ~10 min per upstream guidance) |
| SMTP | `github.com/emersion/go-smtp` (client) for sending mail via smtp.mail.me.com:587 STARTTLS |
| MIME parse/build | `github.com/emersion/go-message` |
| CalDAV / CardDAV | `github.com/emersion/go-webdav` (`/caldav`, `/carddav`) + `emersion/go-ical`; vCards parsed with `go-vcard` (`github.com/emersion/go-vcard`) |
| Telegram | `github.com/gotd/td` (MTProto; session stored in SQLite) |
| SQLite | `modernc.org/sqlite` (already in go.mod; FTS5 available for mail index) |
| FTS5 helpers | `github.com/tetratelabs/…` not needed; use raw FTS5 SQL |
| Router/config | repo conventions: chi, godotenv (already in go.mod) |

### Layout

```
projects/memode/
  cmd/memode/main.go            # flags/env config, start HTTP server + sync workers
  internal/
    config/config.go            # env/file config (upstream creds, listen addr, base URL, signing key)
    server/
      server.go                 # mcp.Server setup, streamable HTTP transport
      codemode.go               # the two MCP tools: memode.read, memode.write
      oauth.go                  # OAuth 2.1 authorization-server endpoints (authorize, token, JWKS,
                                #   RFC 9728 protected-resource metadata, RFC 8414 AS metadata)
      consent.go                # consent UI (server-rendered HTML page with per-service read/write
                                #   checkboxes); approves/denies per client registration
    services/
      registry.go               # Service { Name, Scopes, Register(reg) } interface + wiring
      mail/
        sync.go                 # background IDLE + periodic full-sync worker (all mailboxes)
        store.go                # SQLite schema: messages, FTS5 index, sync state (UIDVALIDITY/UIDNEXT)
        index.go                # search over FTS5
        write.go                # send (SMTP), mark read/flagged, move, delete → then targeted sync
      calendar/calendar.go      # CalDAV client: list calendars, events; create/update/delete
      contacts/contacts.go      # CardDAV client: list contacts; create/update/delete (vCard)
      telegram/telegram.go      # gotd client: dialogs, history, send message/reply; session in SQLite
      kagi/kagi.go              # wraps lib/go/kagi
    store/store.go              # shared SQLite open/migrate (mail index, oauth tokens, tg session)
    oauthstore/oauthstore.go    # registered clients, auth codes, tokens, grants (service+rw matrix)
```

### Reusable packages → `lib/go`

Generic, memode-independent code lives in `lib/go` (imported by memode, and
reusable by other services in this repo):

- `lib/go/codemode` — the go-lua sandbox host: VM-per-call, sandboxing,
  JSON↔Lua bridge, function registry, per-scope environment building, output
  collection + caps. (This is `luaenv/` above, promoted to lib.)
- `lib/go/oauthas` — OAuth 2.1 authorization-server primitives: PKCE,
  authorize/token/JWKS/metadata endpoints, JWT issuance (golang-jwt), consent
  grant store interfaces, token middleware. MeMode wires its consent UI and
  scope vocabulary to it.
- `lib/go/dav` — thin CalDAV/CardDAV convenience wrappers over
  `emersion/go-webdav` (discovery against iCloud, typed event/vCard helpers),
  plus iTen/vCard parse helpers. MeMode uses these; other projects can too.

MeMode-specific glue stays in `projects/memode`: config, service wiring,
mail sync worker + SQLite index schema, consent page, MCP tool definitions.

### Key design

**Code Mode tools.** Two MCP tools on one server:

- `memode.read` — input `{ script: string, max_output_bytes?, timeout_ms? }`.
  Registers only read functions. `memode.write` — same input, registers
  read+write functions for services whose scope set includes write. The Lua
  surface mirrors pi codemode: functions grouped in service namespaces
  (`mail.search(query, opts)`, `mail.get(id)`, `mail.send(msg)`,
  `calendar.list_events(range)`, `calendar.upsert_event(e)`,
  `contacts.search(q)`, `contacts.upsert(c)`, `telegram.dialogs()`,
  `telegram.history(chat_id, ...)`, `telegram.send(chat_id, text)`,
  `kagi.search(q)`), plus globals: `print(text)` collects output items,
  `sleep(ms)` (bounded, context-aware), no io/os/network access.
- Output: script result (last value or `return`) + print items, serialized to
  text/JSON; hard cap on output size (default 1 MiB) with truncation flag.
- Scope enforcement is layered: (1) functions not registered per tool,
  (2) OAuth token scopes per client checked at tool-call time — a write call
  requires both the tool to be `memode.write` *and* the client's grant to
  include the service's write scope.

**Mail sync.** Worker opens IMAP (imap.mail.me.com:993, app-specific password),
lists all mailboxes, does initial full fetch (headers + body text, chunked,
resumable via per-mailbox UID cursor) into SQLite, then IDLE per connection;
on any notification or every 10 min, re-sync changed mailboxes. Writes
(`mail.send` via smtp.mail.me.com:587 STARTTLS; flag/move/delete via IMAP)
execute the upstream mutation, then synchronously re-sync the affected mailbox
before the Lua call returns. FTS5 index over subject/from/to/body. For the future embedding release: plain
SQLite has no native vector search. `modernc.org/sqlite` is pure-Go and cannot
load the `sqlite-vec` C extension, so the plan is to store embeddings as a
`BLOB` column and do brute-force cosine similarity in Go — at personal-mail
scale (tens of thousands of messages) that's milliseconds-fast and needs no
extra index. If it ever gets too slow, the escape hatch is swapping the driver
to `ncruces/go-sqlite3` (already an indirect dep) which loads `sqlite-vec`.

**OAuth.** MeMode is both authorization server and resource server on one HTTP
listener: `/` MCP (streamable HTTP), `/.well-known/oauth-protected-resource`,
`/.well-known/oauth-authorization-server`, `/authorize`, `/token`, `/keys`,
`/consent`. PKCE required (OAuth 2.1). Client registration: static config for
the owner's harness plus RFC 7591 dynamic registration endpoint. Tokens: JWT
access tokens (RS256 via `golang.org/x/crypto` + `go-jose`… simplest:
`github.com/golang-jwt/jwt/v5`) embedding the granted scopes; refresh tokens in
SQLite. Consent page lists each service with "Read" and "Read & Write"
checkboxes; grant stored as per-service `read|write` matrix. Token middleware
validates signature + scopes; the scope set decides which Lua functions are
registered for that session's tool calls.

**Service registry.** `services.Service` interface: `Name() string`,
`Setup(ctx) error` (connect/verify creds), `Register(reg *luaenv.Registry)`.
Adding a service = one package + one line in the wiring list. Health of each
service reported via MCP tools/list annotations and a `memode.status` read fn
free in both tools.

## Files to modify / create

- New: entire `projects/memode/**` tree (above layout)
- `go.mod` / `go.sum`: add dependencies listed above
- `MODULE.bazel` / `BUILD.bazel` (root, gazelle-generated rules under `projects/memode`)
- `projects/memode/README.md`: config reference (env vars), OAuth client setup
  for the harness, example Lua scripts
- Note: plan file convention — after approval, copy this plan to
  `~/Documents/70-79 AI Workspace/71.01 Plans/memode/memode-mcp-server/PLAN.md`

## Reuse

- `lib/go/kagi` — Kagi Search client (bounded, caching) — wrap, don't re-implement
- `lib/go/o11y` — logging/tracing/metrics conventions used across repo services
- `lib/go/fetchweb` — HTTP fetch helpers/conventions if useful for CalDAV transport
- Repo conventions: chi router, godotenv config, modernc.org/sqlite, Bazel+gazelle builds, o11y

## Steps

- [x] 1. Scaffold `projects/memode` (cmd, internal packages, BUILD files, README), add deps to go.mod; `go mod tidy`; gazelle
- [x] 2. `store` + `config`: SQLite open/migrate (FTS5 enabled), env config
- [x] 3. `lib/go/codemode`: go-lua host — VM-per-call sandbox, JSON↔Lua bridge, fn registry, per-scope env building, output collection + caps; unit tests with a fake service
- [x] 4. `server.codemode`: `memode.read` / `memode.write` MCP tools wired to `luaenv`; `memode.status`; MCP server over streamable HTTP (no auth yet); integration test over `mcp` client SDK
- [x] 5. `lib/go/oauthas` AS endpoints (authorize/token/keys/metadata, PKCE), JWT issuance, token middleware; `projects/memode` consent UI + scope vocabulary, scope→Lua-registration mapping; tests with a scripted OAuth client
- [x] 6. `services/mail`: sync worker (initial + IDLE), SQLite index + FTS5, read fns (search/get/list mailboxes), write fns (send/flag/move/delete) with synchronous post-write re-sync; tests against `greenmail`/docker IMAP? — use testcontainers (already in repo) with a dovecot/greenmail image, or unit-test store logic and keep IMAP paths behind small interfaces
- [x] 7. `lib/go/dav` CalDAV/CardDAV wrappers, then `services/calendar` + `services/contacts`: read fns (list, get, expand recurrences server-side by CalDAV time-range queries), write fns (create/update/delete events, vCards); thin unit tests with httptest serving canned DAV responses
- [x] 8. `services/telegram`: gotd/td user client (code+2FA login on first run, session persisted), read fns (dialogs, history, read inbox state), write fns (send, reply, mark read, edit/delete); smoke test gated behind env flag
- [x] 9. `services/kagi`: wrap `lib/go/kagi`, expose `kagi.search`
- [x] 10. End-to-end: launch server, run OAuth dance with a test client, run Lua scripts through `memode.read`/`memode.write` against real iCloud creds in a manual pass; README

## Verification

- `bazel build //projects/memode/...` and `go test ./projects/memode/... ./lib/go/codemode/... ./lib/go/oauthas/... ./lib/go/dav/...`
- Unit tests: codemode sandbox (write fn absent in read env, timeout kills VM,
  output cap), OAuth flow (PKCE, consent matrix → token scopes → tool gating),
  mail store/search, CalDAV/CardDAV fns against httptest, kagi wrapper.
- Integration tests (testcontainers, already in repo):
  - IMAP/SMTP round-trip against a `greenmail` (or dovecot) container: real
    send → sync worker picks it up → appears in FTS search; flag/move/delete
    reflected in the index.
  - Full-stack MCP-over-HTTP: launch server, run OAuth dance (PKCE + consent),
    then call `memode.read`/`memode.write` via the MCP Go SDK client, asserting
    scope gating end-to-end (write denied with read-only grant).
  - DAV round-trip against a Baïkal container (CalDAV + CardDAV in one image):
    create calendar event + contact via `memode.write` Lua fns, verify via
    raw DAV PROPFIND/GET, update them, delete them, and confirm reads via the
    Lua read fns match after each mutation.
  - Telegram smoke test gated behind `MEMODE_TELEGRAM_TESTS=1` with real creds
    (skipped in CI otherwise).
- Manual: configure real upstream creds, run server, complete consent with a
  test harness client, exercise each service read & write (send a test email,
  create a test event & contact, send a Telegram message), confirm mail write
  ops reflect in the index immediately.
