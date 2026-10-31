# M0–M6 implementation and validation

Status date: 2026-10-31. The application is implemented and locally validated.
Native arm64 execution is delegated to the existing native arm64 CI runner and
has not been claimed as a local result. There has been no production activation,
private infrastructure repository creation, DNS/tailnet change or real-user data
load. M7–M10 remain outside this PR.

## Repository reconciliation (M0)

The checkout and remote main were at
`3baa49e1ed6f8a198e5bae5061dc779fd6af94cd`, matching the supplied baseline, with a
clean initial tree. No repository AGENTS.md was present. Root module is
`codebase.bid`; there is no nested module/workspace. Existing Bazel/Gazelle,
`platform_binary`, `binary_oci_image`, OCI image inventory, migration/test helpers,
MeMode auth/config, root SQL/code-generation settings and shared observability
libraries were inspected.

Reusable code is in `lib/go/cursor` (signed pagination), `lib/go/jsruntime`
(fresh Goja VM/watchdog/encoder), `lib/go/unixsocket` (owned socket/peer checks)
and the additive safe client-metadata helpers in `lib/go/oauthas`. Memory uses
its own PostgreSQL auth/graph state and explicit bindings. MeMode's permissive
localhost/basic-password defaults and SQLite grants were not copied. Its Lua
runtime remains unchanged. The existing password helper's format/resource profile
was unsuitable for interactive login; Memory uses maintained bcrypt instead.

Actual application packages are `internal/model`, `store/postgres`, `auth`,
`config`, `mcp`, `worker` and `version`. The storage package owns shared schema,
transaction and read-session services. The SDK and worker protobuf freeze the
wire contracts; SQL and bootstrap/SDK resources are embedded. ADR-001–011 record
the fixed decisions and implementation refinements.

Security-required patch updates are Go **1.27.3**, gRPC **1.83.2** and a dedicated
Memory distroless base digest
`sha256:f5e95195e998742ff590b7569bae479accce1226e51ae6c3118954030121cdcb`.
Other applications retain their existing base pins. Goja is pinned at
`v0.0.0-20261031103838-cf2ff0ec0b31`. Dependency and security review details are in
security.md; no unrelated dependency versions were upgraded.

## Milestone evidence

| Milestone | Implemented behavior | Executed local evidence |
|---|---|---|
| M0 | Contracts, ADRs, inventory, SDK/proto, version command, Bazel targets | New binary and existing MeMode server baseline build/test; Gazelle consistency |
| M1 | Typed SQL storage, composite-space/kind FKs, canonical names, protected view, role grants, deterministic bootstrap | PostgreSQL 17 empty migration/bootstrap twice, direct invalid SQL, restricted reader/app roles |
| M2 | Space lock/revisions, temp IDs, preconditions, idempotency, sources/evidence, immutable events, proposals/apply, archive | Concurrent correction/registration, stale proposals, alias collisions, rollback, replay, preserved evidence, type-removal checks, collision injection |
| M3 | Batch get, lexical search, typed AST, bounded breadth-level pull/incoming, schema projections, signed cursors, snapshots, execution caches/budgets | False/zero/empty/absent, cycles, scalar continuation, virtual incoming/find, stale cursors, concurrent snapshot and 10k/100k query-plan benchmark |
| M4 | Official SDK tools/schemas, owner consent, persisted OAuth, PKCE, scoped resource tokens, refresh replay/revocation, private diagnostics | TLS test owner login/consent and official MCP client create/read/correct; scope/space, PKCE/redirect/CSRF/JWT denials; output-schema validation |
| M5 | Typed gRPC stream, explicit bindings, fresh read-only Goja, bounded encoding/callbacks, cancellation, worker readiness/recovery | All synthetic SDK examples, forbidden writes/forged fields, protocol/busy cases, hostile results/getters/proxies, loop recovery, disconnect/restart and race detector |
| M6 | Stamped amd64/arm64 OCI index, native archive tests/probes, patched image, security/reproducibility gates, release-evidence capture | Both images built/scanned; amd64 nonroot/read-only/no-network execution, real cgroup OOM/replacement, and forced repeat links/packaging with matching index |

The native arm64 image test and full native arm64 integration execution are CI
exit gates. Cross-compilation and static image scanning are explicitly not a
substitute. NixOS/rootless-Podman VM checks, actual host UID/ACL mappings,
`--userns=keep-id`, ingress interoperability, backup deployment and production
verification belong to later milestones and have not been marked complete here.

## Final local checks

Commands used the root Mise Go 1.27.3 and Bazel 9.2.0. The local remote cache
became unavailable, so final Bazel invocations used `--remote_cache=` with bounded
local resources. A GNU source download was supplied from Bazel's mirror with the
exact pinned checksum; no dependency checksum was relaxed. The stale Bazel daemon
was confirmed to be a zombie before removing only its daemon metadata; source and
build outputs were preserved.

1. **Exit 0, 12 Bazel tests passed:**

   ```sh
   bazel test --stamp --config=docker --test_tag_filters=-arm64 \
     //projects/memory/... //lib/go/cursor/... //lib/go/jsruntime/... \
     //lib/go/unixsocket/... //lib/go/oauthas/... \
     //projects/memode/internal/server:server_test //:gazelle_test
   ```

   Includes the actual amd64 archive under nonroot UID, read-only root, network
   disabled, dropped capabilities, 64 PIDs and 256 MiB. A runaway allocation
   produced a verified cgroup OOM kill; replacing that worker allowed a new
   script to succeed. Separate API tests prove ordinary reads remain available
   during worker disconnect and a subsequent script succeeds after restart.

2. **Exit 0, race detector:** integration suite plus cursor, Goja runtime,
   Unix-socket and OAuth helpers. PostgreSQL tests use a digest-pinned real
   PostgreSQL 17 container. They are tagged `requires-docker`; the ordinary
   non-Docker test invocation does not stand in for them.

3. **Exit 0, Go advisory call-graph scan:** pinned govulncheck 1.9.0, with the
   official Go vulnerability database generated from upstream commit
   `8f7e390a0ff7345d74ecd7a056a1073214466db9`. Result: **0 reachable vulnerabilities**.
   The local environment blocks `vuln.go.dev`; CI uses the normal advisory source
   and treats an unavailable source as a failure. A stripped-binary-only scan
   over-reported unimported module packages; it is not presented as a clean scan.
   CI checks the scanner/Bazel Go-version match and scans both target architectures.

4. **Exit 0, image scans:** digest-pinned Trivy 0.76.1, HIGH/CRITICAL policy.
   Both amd64 and arm64 image archives: **0 OS findings, 0 Go-binary findings** at
   the configured severity threshold. The initial base's OpenSSL findings were
   fixed by the Memory-specific base pin; no finding was hidden by an ignore list.

5. **Exit 0, repeat-build check:** two amd64/arm64 application links, layer tars,
   OCI images and index were actually executed in each run. Trusted execution
   logs confirmed non-cache runners; only validated generated Memory outputs were
   removed between runs. The OCI index descriptors matched. CI repeats the same
   check before publication. Source/stamp inputs were held fixed for the local
   working-tree check; CI release evidence records the final clean source SHA.

6. **Exit 0, graph export/empty-space restore:** graph, stable schema IDs,
   sources/evidence and history agree; auth and virtual facts are excluded, and
   importing over an existing space is rejected. This is an application semantic
   restore check, not an off-site backup/host recovery drill.

7. **Static workflow checks:** publication depends on both native matrix jobs and
   the security job; build jobs have package-read permission, publication alone
   has package-write permission, and publication remains restricted to main.
   Release evidence records immutable index/child digests and source/run identity.
   No registry publication, release promotion or host activation was run locally.

## Query benchmark

Executed with `MEMORY_RUN_BENCHMARK=1 go test -run '^TestQueryBenchmark$' -v
./projects/memory/integration -count=1` (exit 0). Synthetic data: **10,000 ordinary
entities, 100,000 current facts** (90k integers and 10k directed references),
100,000 evidence links and **100,000 fact-history events**, plus meta-schema/source
entities. Concurrency one; 20 warmups and 100 measured samples per shape.

Machine: amd64 Linux 6.18.57, AMD EPYC 9V74 host CPU, five available logical CPUs,
approximately 18 GiB reported memory, workspace overlay filesystem (underlying
physical disk unspecified). These are development-machine observations, not
EliteDesk production measurements.

| Shape | Warm p50 | Warm p95 |
|---|---:|---:|
| Exact entity, facts and provenance | 3.76 ms | 5.13 ms |
| Indexed discovery | 11.69 ms | 18.02 ms |

`EXPLAIN (ANALYZE, BUFFERS)` confirms entity and reverse-reference indexes and the
text GIN index. PostgreSQL chose a short sequential scan for a low-selectivity
numeric predicate with LIMIT 50; a row-return limit is not claimed to bound
scanned work. Profiling exposed a cast on the evidence UUID index column; moving
the cast to the known fact UUID restored indexed evidence lookup. Writes refresh
only touched search rows; an explicit operator reindex handles full rebuilds.

## Remaining operator/CI inputs

Before deployment, supply the actual HTTPS MCP origin/ingress, private secret
files, owner hash, client registrations, space grants, restricted PostgreSQL
credentials/peer mappings, real container UID/ACL mappings and supervisor resource
settings. No values were inferred from the reference host FQDN. Hardware/storage,
private GitOps access/App promotion credentials, Tailscale/SSH, encrypted secrets
and backup destinations remain later infrastructure inputs. They did not block
independent application implementation or disposable tests.
