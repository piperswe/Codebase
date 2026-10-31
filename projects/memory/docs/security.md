# Security model and dependency review

## Boundaries

The authenticated API owns authority, all database connections, schema validation
and the read transaction. The worker is treated as compromised. Its typed protobuf
oneof contains only read operations and no space, principal, SQL, transaction mode
or capability upgrade. Unknown fields, invalid call sequences, oversized frames
and illegal lifecycle transitions terminate the execution. API counters/deadlines
are independent of worker statistics. Reader-role sessions cannot read auth token
tables, write facts, create objects or create temporary tables. The migrator role
and its credentials are never available to serve/worker.

The worker creates one fresh VM per execution, runs one script at a time and has
no waiting queue. Goja's interrupt handles cooperative timeouts, SQL/RPC honor
context cancellation, and a dedicated process watchdog exits a stuck worker after
the effective deadline plus two seconds. API deadlines release the read session
independently. Process death requires supervisor restart and an explicit client
retry. Go's memory limit is not a hard quota: deploy separate rootless identities,
network-disabled worker containers, cgroup memory/CPU/PID limits, read-only roots,
dropped capabilities and a restricted writable IPC mount. These are deployment
requirements, not claims established by an in-process unit test.

The Unix listener holds a startup flock, refuses symlinks/arbitrary files and
foreign-owned sockets, and checks Linux peer UID when configured. IPC directories
must be created by the operator/supervisor with controlled ACLs; the API should
have connect/traverse access without permission to replace the worker socket.
The application supports local Unix IPC only; remote unauthenticated endpoints
cannot be selected. Containers share a kernel and are not VM-strength isolation.

The reusable `lib/go/jsruntime` does not install Go reflection bridges. Every
public graph object/method is explicitly constructed. Serialization uses captured
intrinsics, property descriptors, cycle/node/depth/byte checks and a live watchdog;
it does not blindly export arbitrary objects or invoke their getters/toJSON.
Result and log limits are independent. Native callbacks must honor their context.

## Goja review

Pinned module: `github.com/dop251/goja`
`v0.0.0-20261031103838-cf2ff0ec0b31` (commit `cf2ff0ec0b31`).
The upstream history was inspected at that commit. It includes nonzero-offset
TypedArray corrections `c9b2ea77db59c0e127916c8e92f066e95c812e91`, `0b76000`,
`c65cf2f`, and subsequent BigInt conversion/parser fixes. A harmless regression
test checks that a filtered subarray preserves its byte offset; exact BigInt
conversion, output bounds and hostile serialization are also exercised. No exploit
payload is included. The research link in the input plan is motivation for the
review; this implementation does not claim independent reproduction of its exploit.

The initial official Go advisory scan found reachable gRPC issues GO-2026-6443
and GO-2026-6348, and standard-library GO-2026-6809. The implementation therefore
patches gRPC from 1.83.0 to **1.83.2** and the root Go toolchain to **1.27.3**.
These are security fixes needed for the new boundary, not unrelated dependency
upgrades. No reachable Goja advisory was reported by that scan. A clean scan is
not a sandbox proof.

In this execution environment `vuln.go.dev` is blocked. A local database was built
using the official `golang/vulndb` generator from upstream commit
`8f7e390a0ff7345d74ecd7a056a1073214466db9`, then scanned with pinned govulncheck
1.9.0. CI requires the normal advisory service and checks that the source scanner and
Bazel binary use the same Go version;
unavailable advisory data fails the gate. Final image scanning uses digest-pinned
Trivy 0.76.1. See implementation-status for executed results and limitations.

## OAuth

The owner password is a persisted bcrypt hash (cost 12–16) supplied through a
private file. There is no signup or password-reset endpoint. Login attempts are
rate-limited in PostgreSQL, sessions are short-lived, cookies are Secure/HttpOnly
with SameSite, and both login and consent check CSRF tokens. Consent shows client
identity and explicitly selected scopes/spaces; writes and schema administration
are never selected by default.

Authorization codes are short-lived, single-use and S256-PKCE-bound, with exact
redirect and resource binding. Access tokens allow only HS256 with a persisted
key, validate issuer/audience/expiry/key ID and recheck the persisted grant on every
request. Refresh tokens store only SHA-256 hashes, rotate atomically, retain a
30-day family expiry and revoke the grant on consumed-token replay. Codes and
sessions also store hashes. Key rotation has a maximum 15-minute previous-key
verification overlap; remove the previous file/config after that expiry.

Pre-registration and HTTPS client ID metadata documents are supported. Metadata
retrieval caps bytes, deadlines and redirects, rejects nonpublic DNS answers and
pins dialing to a checked address to prevent rebinding. It forwards no owner or
bearer credentials. Client registrations are bounded and never convey consent.
Legacy DCR is disabled by default. Host/Origin are checked against the configured
HTTPS origin; forwarded headers are not trusted as authority. Private readiness
and metrics run on a separate loopback/Unix listener.

Schema descriptions, source text, documents and diagnostics are untrusted data.
They do not become instructions, verified identities or automatically fetched URLs.

The stripped-binary govulncheck mode falls back to all packages in each module
when symbols are unavailable. It reported x/net/html and deprecated x/crypto
OpenPGP packages that are absent from Memory's import graph. CI therefore uses
precise source call-graph scanning with a binary/toolchain version check, plus
Trivy scanning of the actual image and embedded Go dependency metadata. This is
not an ignore list for reachable findings. The image scan also found fixable
OpenSSL issues in the original shared base; Memory uses its own updated pinned
distroless base so other application image pins stay unchanged.
