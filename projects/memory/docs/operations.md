# Memory operations and image contract

## Process roles and startup

One Bazel-built multi-architecture OCI image has entrypoint `/memoryd` and these
subcommands:

| Command | Operation |
|---|---|
| `version --json` | Build revision/version, SDK/RPC versions and DB compatibility |
| `migrate roles` | One-time DBA bootstrap of cluster roles/database privileges |
| `migrate status` / `migrate up` | Locked, explicit forward-only migrations |
| `admin bootstrap [SPACE]` | Idempotent empty space/meta-schema initialization |
| `admin schema propose SPACE` / `apply SPACE` | Strict JSON request on stdin |
| `admin auth register ID NAME REDIRECT` | Pre-register a public OAuth client |
| `admin auth revoke CLIENT_OR_GRANT` | Revoke a client/grant immediately |
| `admin auth cleanup` | Delete expired auth material/unused registrations |
| `admin hash-password` | Read password from stdin, emit bcrypt hash |
| `admin auth rotate-key ACTIVE PREVIOUS` | Atomic local signing-key rotation |
| `admin export SPACE` / `import SPACE` | Versioned graph stream on stdout/stdin |
| `admin reindex SPACE` | Rebuild the disposable lexical search projection |
| `serve` | Public MCP/auth HTTP and private diagnostics |
| `worker` | Network-independent Unix gRPC Goja worker |
| `healthcheck api` / `worker` | Probes implemented in the distroless binary |
| `smoke` | Read-only personal-space schema/readiness check |

`serve` never runs DDL/bootstrap. Worker startup rejects Memory DB, owner, signing,
cursor and common deployment/build credentials. Neither process loads .env files.
Administration requires local access to separately supplied credentials and is
not reachable from any MCP/JavaScript operation.

## Configuration

Secrets accept `NAME_FILE` or `NAME`, never conflicting simultaneous values. Use
private files in production; files must be regular, have no world permissions and
be at most 64 KiB. Do not put DSNs, hashes, keys or auth exports into Git, image
layers, command-line arguments, Nix derivations or logs.

| Setting | Default / requirement |
|---|---|
| `MEMORY_PUBLIC_URL` | Required HTTPS origin, without path/query/credentials |
| `MEMORY_HTTP_ADDR` | `127.0.0.1:8080`; put a streaming-capable HTTPS ingress in front |
| `MEMORY_ADMIN_ADDR` | `127.0.0.1:8081`; literal loopback or `unix:/absolute/socket` |
| `MEMORY_WORKER_SOCKET` | `/run/structured-memory/worker.sock`; absolute local path only |
| `MEMORY_PEER_UID` | Optional explicit peer UID in the process's user namespace |
| `MEMORY_DB_DSN_FILE` | Required application-role DSN, user `memory_app` |
| `MEMORY_READER_DSN_FILE` | Required SELECT-only DSN, user `memory_reader` |
| `MEMORY_MIGRATOR_DSN_FILE` | Explicit migration commands only; rejected by serve |
| `MEMORY_CURSOR_KEY_FILE` | Persisted random secret, at least 32 bytes |
| `MEMORY_SIGNING_KEY_FILE` | Persisted random secret, at least 32 bytes, serve only |
| `MEMORY_OWNER_HASH_FILE` | Persisted bcrypt hash, cost 12–16, serve only |
| `MEMORY_PREVIOUS_KEY_FILE` | Optional one previous key during rotation |
| `MEMORY_PREVIOUS_KEY_UNTIL` | Required RFC3339 expiry for previous key, at most 15 minutes |
| `MEMORY_DYNAMIC_REGISTRATION` | `false`; enable only for a client requiring legacy DCR |
| `MEMORY_CODE_TIMEOUT_MS` | 5000; trusted configuration may lower, never exceed default |
| `MEMORY_CODE_OPERATIONS` | 100; trusted configuration may lower |
| `MEMORY_CODE_RESULT_BYTES` | 262144; trusted configuration may lower |
| `MEMORY_CODE_LOG_BYTES` | 16384; trusted configuration may lower |

The API defaults to 8 application and 2 reader connections (configurable with
`MEMORY_APP_POOL_MAX` and `MEMORY_READER_POOL_MAX`), `search_path=pg_catalog,memory`,
1-second statement/lock timeouts and a 10-second idle-in-transaction timeout.
Reads/scripts have independent row/entity/edge/SQL/payload budgets. Operator
export/import/reindex use a 30-second statement timeout for bulk work. Nonlocal
TCP DB connections require certificate-verified TLS; local Unix sockets and
loopback TCP support native/test operation. Production should use native
PostgreSQL 17 Unix sockets with explicit peer mappings. Reader identity must have
no writer/owner membership. Startup verifies restricted runtime roles and schema
compatibility rather than silently assuming privileges are correct.

All HTTP request bodies are at most 1 MiB; ordinary transactions/batch IDs are at
most 100. Graph pages currently cap at 100, a conservative limit below the plan's
200 maximum. Source/input are 64 KiB each. Code Mode limits also include 200 SQL
statements, 10,000 decoded rows, 1,000 unique entities, 2,000 edges, depth five,
1-MiB RPC frames and 4-MiB total graph payload. Limits fail explicitly; no output
is silently shortened.

## Image and confinement

`memory_image` is a linux/amd64 + linux/arm64 OCI index, using the repository's
pinned distroless base and OCI source/revision/version annotations. Nix/supervisor
selects the runtime UID; tests run an explicit nonroot numeric UID. Do not assume
the base image's default identity supplies isolation. The image has CA trust and
uses UTC timestamps. No shell, compiler, Go, Bazel, Nix or SQL client is needed.
Migrations, bootstrap manifest, tool schemas, declarations and examples are embedded.

Use a read-only root, `no-new-privileges`, dropped capabilities and bounded PIDs,
CPU and memory. Only the worker's IPC directory is writable. Configure the worker
with no network and no PostgreSQL socket or DB/auth/deployment mounts. Give API
and worker separate rootless host identities. Start with a 256-MiB worker limit,
64 PIDs, and supervisor restart on failure. The parent directory is operator-owned
with appropriate per-user ACLs; do not use a world-writable socket directory.
Set and test peer UIDs in the actual container user namespaces. File permissions
remain the primary protection when user-namespace UID mappings collapse peers.

Load artifacts with `bazel run //projects/memory/cmd/memoryd:memory_load_amd64` or
build `memory_amd64_tar`/`memory_arm64_tar`. Image smoke tests check the actual
archive with a nonroot UID, read-only root, network disabled, bounded memory/PIDs,
a worker health probe, cooperative timeout, a real cgroup OOM kill and successful
execution after replacement when the test runner is nonroot. Rootless Podman's
`--userns=keep-id`, NixOS peer auth/ACL persistence and supervisor OOM recovery
require the infrastructure VM tests in later milestones; Docker smoke tests do
not substitute for those deployment proofs.

## Auth operation

Bootstrap only the configured owner; there is no public registration of owners.
Pre-register known client IDs/redirects or allow HTTPS client ID metadata documents.
Clients request the exact resource `<PUBLIC_URL>/mcp`. The owner must explicitly
approve scopes and spaces in the browser. Default consent selects read/execute;
write/schema administration are unchecked. DCR is off unless explicitly enabled.

Generate signing/cursor keys from an OS CSPRNG and store them in private files.
For rotation, run the local rotate-key command, restart serve with both key files
and the emitted `MEMORY_PREVIOUS_KEY_UNTIL`, then remove previous-key configuration
at expiry. Grants/refresh families survive restarts. Revocation takes effect on
the next request because token validation checks persisted grants. Periodic cleanup
also runs hourly during serve. Logs/metrics contain counters and safe errors, not
script source, fact values, bearer tokens or credential files.

## Restore and rebuild

Exports are graph-only version 1 streams. They retain canonical identities,
current typed assertions, sources/evidence, schema, revisions, history and
idempotency results; they exclude `memory_auth` and the derived virtual-fact view.
Keep exports private. Import requires an absent target space, exact space slug and
matching schema version. It never overwrites an existing space. Restore OAuth
state and secret files through separately protected database/secret backups.

Use PostgreSQL logical backups or the infrastructure backup system for full
recovery. Restore into an isolated database, run semantic graph/history checks,
then switch credentials/traffic only through the operator's deployment process.
`admin reindex` repairs lexical search without editing facts or schema. Archival
and retraction do not erase history or backups; permanent forgetting needs an
explicit owner-admin removal and backup-retention procedure outside this API.

## Build/release evidence

Existing Codebase publication runs only after both native test jobs. The security
job checks the Bazel/scanner toolchain match, scans the source call graph with
govulncheck 1.9.0 and scans the final image with
digest-pinned Trivy 0.76.1. No advisory-access error is treated as a clean scan.
The publication job preserves the existing registry mechanism and captures
source SHA, workflow run, index digest, architecture digests, test/scan results and
schema compatibility using `tools/release_evidence.py`. Digests, not mutable tags,
are deployment identities. Cross-repository promotion/activation is M8, not part
of this change. No deployment pin or production host state is stored here.

For repeat-build verification, use identical source and workspace-status inputs
and compare the OCI index descriptor digest after forcing action execution;
ordinary cache hits alone do not prove reproducibility. See implementation-status
for executed evidence, including any native-architecture or image-scanner limits.
