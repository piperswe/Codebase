# Structured Memory

An independently deployable Go/PostgreSQL MCP service for typed facts, explicit
schema evolution, provenance, corrections and bounded graph reads. JavaScript
Code Mode runs in a separate Goja worker with an API-enforced read-only protocol.
This project does not modify ChatGPT's built-in memory.

## Build and test

Use the root Go module, Mise toolchain and Bazel workspace:

```sh
bazel build //projects/memory/cmd/memoryd:memoryd
bazel run //projects/memory/cmd/memoryd:memoryd -- version --json
bazel test //projects/memory/... //lib/go/cursor/... //lib/go/jsruntime/... //lib/go/unixsocket/... //lib/go/oauthas/...
bazel test --config=docker --test_tag_filters=-arm64 //projects/memory/...
bazel build --stamp //projects/memory/cmd/memoryd:memory_image
```

The default test configuration excludes Docker tests. Integration and image tests
require Docker and real PostgreSQL **17**, using a digest-pinned synthetic test
container. Native arm64 CI uses `--test_tag_filters=-amd64`. Cross-compilation
alone does not prove native arm64 execution.

The official MCP Go SDK supports Streamable HTTP, with `2026-07-28`, `2025-11-25`
and `2025-06-18` configured for negotiation. OAuth is mandatory; there is no
unauthenticated serving mode. The default space is initialized only by an explicit
operator bootstrap command, never by starting the API.

## Local operator setup

Read [operations](docs/operations.md) before creating a database. A DBA first runs
`memoryd migrate roles` using an administrative `MEMORY_MIGRATOR_DSN_FILE`. This
creates the restricted roles and removes PUBLIC database TEMP/CREATE privileges
**in that selected database**. Use a dedicated database. Subsequent
`memoryd migrate up` runs as `memory_migrator`, assuming the NOLOGIN owner role.
Neither the API nor the worker gets migrator credentials.

Supply separate application/reader DSNs and persisted cursor/signing secret files,
a bcrypt owner password hash, an HTTPS public origin and an existing private IPC
directory. Run `memoryd admin bootstrap personal`, then start `/memoryd worker`
and `/memoryd serve` in separate processes/containers with separate identities.
There is no domain schema or personal data in bootstrap.

Production host flakes, release pins, secrets and deployment workflows belong in
the private infrastructure repository. M7–M10 and production activation are outside
this implementation. This repository builds the application artifact and retains
its existing main-branch image publication, now gated on both native jobs and
security scans. It does not activate a home host.

## Contracts and evidence

- [API and numeric rules](docs/api.md)
- [Security boundary and dependency review](docs/security.md)
- [Operations, configuration and image contract](docs/operations.md)
- [Implementation and validation status](docs/implementation-status.md)
- [Accepted implementation plan](docs/implementation-plan.md)
- [Code Mode declarations](sdk/code-mode.d.ts) and executable examples
- [Typed worker protocol](proto/memory/worker/v1/worker.proto)

Archival/retraction retains provenance and history. It is **not erasure** from the
live database or backups. The service stores accepted assertions, not a guarantee
that those assertions remain true.
