# Structured Memory Service — V1 Integrated Implementation Plan

**Document version:** 1.2 (cross-repository GitOps, multi-IaC layout)
**Prepared:** 2026-10-31
**Audience:** A coding agent implementing `piperswe/Codebase` and a separate private `piperswe/infrastructure` GitOps repository
**Status:** Implementation specification; not a claim that the software or deployment already exists
**Reference deployment:** HP EliteDesk `infra-1.11r.io`, NixOS, rootless Podman containers, native PostgreSQL

## How to use this document

Implement the system described here without needing the earlier design conversation. Read Sections 1–4 before changing code, then work through the milestones in Section 21. The detailed contracts in the intervening sections are part of the acceptance criteria, not optional architectural suggestions.

Preserve the user's choices: **Go + PostgreSQL; Goja; attributes as entities; read-only Code Mode in V1; Bazel builds; NixOS application/host deployments; private cross-repository GitOps; GitHub Actions CD to `infra-1.11r.io`; infrastructure repository organized for multiple IaC systems, with Terraform reserved for a later Cloudflare migration.** Do not substitute another language, database, build system, or hosting platform.

This document resolves earlier sketches into one design. API names and data shapes below are contracts to implement, not claims about an existing SDK. Resource numbers are initial configurable limits, not benchmark results. Any example identifiers, domains, image digests, and fixtures are illustrative unless explicitly identified as repository observations.

Do not make live repository, DNS, tailnet, Cloudflare, host, or production-data changes merely because this plan describes them. Implement and test the code/configuration; use the operator's existing execution permissions for activation. Missing host credentials or hardware identifiers must not block independent implementation and VM tests. Report those remaining deployment prerequisites precisely.

## Contents

1. [Decisions and scope](#1-decisions-and-scope)
2. [Repository baseline and integration](#2-repository-baseline-and-integration)
3. [Product behavior and acceptance scenarios](#3-product-behavior-and-acceptance-scenarios)
4. [Architecture and trust boundaries](#4-architecture-and-trust-boundaries)
5. [Domain model and schema registry](#5-domain-model-and-schema-registry)
6. [PostgreSQL storage and integrity](#6-postgresql-storage-and-integrity)
7. [Transactions, provenance, and history](#7-transactions-provenance-and-history)
8. [Retrieval, search, and graph queries](#8-retrieval-search-and-graph-queries)
9. [MCP interface](#9-mcp-interface)
10. [JavaScript Code Mode](#10-javascript-code-mode)
11. [Worker protocol and execution lifecycle](#11-worker-protocol-and-execution-lifecycle)
12. [Authentication and authorization](#12-authentication-and-authorization)
13. [Go implementation and commands](#13-go-implementation-and-commands)
14. [Bazel build and OCI images](#14-bazel-build-and-oci-images)
15. [NixOS host and container deployment](#15-nixos-host-and-container-deployment)
16. [Continuous delivery](#16-continuous-delivery)
17. [Migrations, activation, and rollback](#17-migrations-activation-and-rollback)
18. [Backups and recovery](#18-backups-and-recovery)
19. [Observability and operational limits](#19-observability-and-operational-limits)
20. [Testing and quality gates](#20-testing-and-quality-gates)
21. [Implementation milestones](#21-implementation-milestones)
22. [Operator bootstrap and handoff](#22-operator-bootstrap-and-handoff)
23. [Definition of done](#23-definition-of-done)
24. [Decision records](#24-decision-records)
25. [References and verification notes](#25-references-and-verification-notes)

---

## 1. Decisions and scope

### 1.1 Fixed decisions

| Area | V1 decision |
|---|---|
| Application repository | Existing `piperswe/Codebase` monorepo (public) |
| GitOps repository | Separate proposed private `piperswe/infrastructure`; owns desired infrastructure state and deployment workflows |
| Application location | `projects/memory`, independently deployable from MeMode |
| Go module | Reuse root `codebase.bid`; no nested `go.mod` or new Bazel workspace |
| Language | Go, using the repository's pinned toolchain |
| Database | PostgreSQL; initial native host deployment explicitly pins major version 17 |
| Data model | Typed entity–attribute–value graph; stable entity IDs; attributes and types are entities |
| Code Mode | JavaScript using a pinned, security-reviewed Goja revision |
| V1 script permissions | Read-only, enforced by the API as well as the JavaScript bindings |
| V1 normal writes | Authorized conventional MCP transactions and explicit schema-management operations |
| V2 | Code Mode transaction proposals/writes; not implemented or secretly enabled in V1 |
| Isolation | Separate API and worker processes in separate rootless containers |
| Worker connectivity | No network; only an explicitly mounted Unix-domain IPC socket |
| IPC | Versioned, bidirectional gRPC/Protobuf stream over Unix socket |
| Application artifacts | One Bazel-built OCI image with `/memoryd` entrypoint and multiple subcommands |
| Host | HP EliteDesk with FQDN `infra-1.11r.io`, running NixOS; use `infra-1` as the flake host key |
| Container management | NixOS `virtualisation.oci-containers` with Podman, not hand-maintained Quadlets |
| PostgreSQL placement | Native NixOS service, not a production database container |
| Build/deploy boundary | Bazel builds application binaries, generated code, tests, and images; Nix configures/deploys them |
| Artifact registry | Existing GHCR publication mechanism; immutable digest in deployment manifest |
| CD | Codebase CI → GHCR → GitHub App promotion PR in `piperswe/infrastructure` → infrastructure CI → ephemeral Tailscale → exact-SHA NixOS activation on `infra-1.11r.io` |
| IaC organization | Active NixOS under `iac/nixos/`; reserve `iac/terraform/` as a future peer (not implemented in V1) |
| Cloudflare ownership | No Terraform resources/state/provider/workflows in V1; future Terraform manages Cloudflare API resources, never host-local NixOS services |
| Production runner | No persistent GitHub Actions runner on the EliteDesk |
| Primary user | One owner in V1; authorization and space boundaries still explicit |

Nix may build the NixOS system, ordinary host packages, and operational shell wrappers. It must **not** use `buildGoModule`, `dockerTools.buildImage`, `buildLayeredImage`, a Dockerfile, or an embedded Bazel invocation to build this application's release artifact. Fetching/loading an already-built Bazel image for a NixOS integration test is permitted; constructing it with Nix is not.

The NixOS OCI-container module replaces the previous Debian/Quadlet proposal. The older single-repository GitOps layout is also superseded: **Codebase must not contain the NixOS host flake, deployment release pin, secrets, or home-host deployment workflow**. Those belong in the private infrastructure repository. Keep standard OCI images and Unix/TCP abstractions so the service can later run on another Linux distribution or Kubernetes. Kubernetes deployment manifests and remote-worker operation are not V1 deliverables.

### 1.2 V1 includes

A usable end-to-end memory service: entity and schema discovery; precise typed facts; relationships; bounded graph retrieval; transactions; provenance; corrections and history; read-only programmatic exploration; MCP-compatible authentication; native PostgreSQL operations; tested containers; declarative NixOS deployment; automated image promotion and CD; backups; recovery documentation.

One owner can maintain multiple knowledge spaces. Ship a default `personal` space and test a second space for isolation. Do not pre-populate the owner's actual personal facts, infer records from this document, or mix personal and business information.

### 1.3 Explicit non-goals

- Full Datomic compatibility, Datalog, arbitrary SQL, Gremlin, or GraphQL.
- A general-purpose remotely executable shell or JavaScript/Node.js hosting platform.
- Script writes, web fetching from scripts, filesystem access, arbitrary imports, or network APIs in Code Mode.
- Automatic modification of ChatGPT's built-in memory or guaranteed retrieval on every conversation.
- An LLM provider dependency inside the service; semantic judgments remain with the calling assistant.
- Vector databases, embedding pipelines, or fuzzy automatic schema merges.
- Type inheritance, general ontology inference, cross-space graph edges, or per-fact ACLs in V1.
- Full valid-time/bitemporal querying or public `asOf` graph snapshots.
- High availability, distributed workers, message queues, Redis, or managed Kubernetes.
- A complete administration UI; a small owner login/consent interface is in scope.
- Implementing alternative interpreters or refactoring MeMode's Lua runtime as a prerequisite.
- Creating Terraform configuration, backends, state, Cloudflare imports, or Terraform CI/CD in V1. The GitOps layout must only leave space for them.

---

## 2. Repository baseline and integration

### 2.1 Observed baseline

Repository files were inspected at commit **`3baa49e1ed6f8a198e5bae5061dc779fd6af94cd`**. Re-inspect the actual checkout before implementation; never overwrite later work to match this snapshot.

Observed conventions:

- Root `go.mod` uses module `codebase.bid`.
- `MODULE.bazel` has `rules_go`, Gazelle, `rules_oci`, a pinned Go toolchain derived from root `go.mod`, and digest-pinned distroless bases.
- `platforms/binary.bzl` exposes `platform_binary`.
- `platforms/oci.bzl` exposes `binary_oci_image` and builds Linux amd64/arm64 images plus a multi-platform index.
- Root `BUILD.bazel` contains `_OCI_IMAGES`, per-image `oci_push` targets, and `//:push_images`.
- `.github/workflows/ci.yaml` builds/tests amd64 and arm64; the amd64 matrix job currently publishes images on `main`.
- `projects/memode` is a separate personal-services MCP server with OAuth-related code and PostgreSQL-independent storage.
- `lib/go/codemode` is Lua-specific. Inspect it for conventions, but do not reuse it as the Goja engine or silently change its behavior.

References: [R1]–[R5]. An immediate CI correction is required for this service: **publishing/promoting a deployable release must wait for both architecture jobs, not just success of the amd64 job.**

### 2.2 Initial reconnaissance checklist

Read any repository `AGENTS.md`, contribution rules, root Bazel files, `mise.toml`, `.bazelrc`, `sqlc.yaml`, existing Go observability/configuration libraries, and the MeMode auth implementation. Confirm actual target labels and dependency versions. Record findings in `projects/memory/docs/implementation-status.md`.

Reuse existing dependencies such as `pgx/v5`, MCP Go SDK, migration, logging, and telemetry libraries where suitable. Add Goja, gRPC, and Protobuf dependencies through the established root Go/Bzlmod/Gazelle workflow. Avoid unrelated dependency upgrades and lockfile churn.

If infrastructure/Nix code has appeared in Codebase since the snapshot, inspect it and preserve unrelated work, but do not place the new production host flake or release state there. The new production host state belongs to `piperswe/infrastructure/iac/nixos/`. Preserve unrelated image publication behavior while isolating this service's release gates.

### 2.3 Two-repository layout (mandatory)

**Public application repo — `piperswe/Codebase`** (existing):

```text
Codebase/
├── MODULE.bazel / MODULE.bazel.lock
├── BUILD.bazel / go.mod / go.sum
├── platforms/                           # Existing Bazel image helpers
├── projects/memory/
│   ├── README.md
│   ├── cmd/memoryd/                    # main.go and BUILD.bazel
│   ├── internal/
│   │   ├── auth/ / config/ / model/ / schema/
│   │   ├── store/postgres/ / transaction/ / graph/ / search/
│   │   ├── mcp/ / worker/ / runtime/goja/ / observability/
│   ├── proto/memory/worker/v1/
│   ├── migrations/                     # Embedded in Bazel-built image
│   ├── sdk/                            # code-mode.d.ts + JS examples
│   ├── testdata/ / integration/
│   └── docs/
│       ├── implementation-plan.md / implementation-status.md
│       ├── api.md / operations.md / security.md
│       └── adr/
└── .github/workflows/
    ├── ci.yaml                         # Existing CI, correct publication gate
    └── promote-memory.yaml             # Publish evidence, cross-repo PR
```

**Private GitOps repo — proposed `piperswe/infrastructure`** (must be created with operator authorization):

```text
infrastructure/
├── README.md                            # Overall repo purpose and workflows
├── docs/
│   ├── iac-ownership.md                # Resource ownership matrix / boundaries
│   ├── deployment-architecture.md
│   └── terraform-roadmap.md            # Documentation only: future Cloudflare import
├── iac/
│   └── nixos/                          # Only active IaC system in V1
│       ├── flake.nix
│       ├── flake.lock
│       ├── hosts/
│       │   └── infra-1/
│       │       ├── default.nix
│       │       ├── hardware-configuration.nix
│       │       └── storage.nix
│       ├── modules/
│       │   ├── structured-memory.nix
│       │   └── memory-deployment.nix
│       ├── releases/
│       │   └── memory.json
│       ├── secrets/                      # sops-encrypted; no plaintext
│       └── tests/
│           └── memory.nix
└── .github/workflows/
    ├── nixos-validate.yaml             # Only checks iac/nixos + shared policies
    └── nixos-deploy.yaml               # Only activates NixOS host state
```

**Future only (not created or bootstrapped in V1):** `iac/terraform/` as a sibling of `iac/nixos/`, with provider/product-specific roots, separate state backends and dedicated `terraform-plan`/`terraform-apply` workflows. Do **not** create `.tf` files, Terraform backends, state, import blocks, Cloudflare provider credentials, or Terraform workflows merely to reserve the name. A short roadmap and ownership document are enough. Keep one GitOps repository with multiple independently owned IaC roots, rather than one universal wrapper that tries to run both tools.

An illustrative **future** addition (do not create as V1 files) is:

```text
infrastructure/iac/terraform/
└── cloudflare/
    ├── account/                  # Account-scoped resources, own state
    └── zones/
        └── 11r.io/               # Zone-scoped resources, own state
```

At onboarding, select a remote locking-capable Terraform state backend, pin provider versions, and define an import inventory before the first Cloudflare apply. This structure is intentionally an example rather than a promise that all Cloudflare resources share one state or provider root.

There is deliberately **no root flake in the GitOps repo**: its Nix flake lives in `iac/nixos/`. Nix supports subdirectory flakes; commands must address that directory explicitly. The exact NixOS configuration selector is `#infra-1`, not the DNS FQDN (the NixOS config sets `networking.hostName = "infra-1"` and `networking.domain = "11r.io"`). The FQDN is the host identity; do not infer it is the public MCP URL or is reachable directly from public DNS/Tailscale without verification.

### 2.4 Cross-IaC ownership rules

1. A resource has one reconciler/owner. In V1 Nix owns the EliteDesk operating system, native PostgreSQL, container services, local ingress, Tailscale daemon and backup timers.
2. Future Terraform owns **Cloudflare control-plane resources** only (e.g., zone DNS, account/zone settings, API-managed Tunnel configuration if selected), not the NixOS host, Podman containers, PostgreSQL, or Bazel artifacts.
3. Where systems interact, define an explicit interface (e.g., Nix config uses an externally created Cloudflare hostname/route); never have both manage the same object. Terraform-managed Cloudflare tunnels/routes must not conflict with Nix-managed local tunnel agent/service configuration.
4. Each IaC root has separate validation, deployment permission, state/locks, and failure domain. A NixOS image pin update must never implicitly trigger Terraform apply; a future Cloudflare DNS change must not implicitly rebuild the host.
5. Cross-system rollouts are coordinated by documented dependencies/health checks or dedicated orchestrated change plans, not arbitrary order between independent GitHub workflows.
6. The infrastructure repo is *desired* state; actual deployment status is tracked in host journal and GitHub deployment statuses. Do not commit mutable runtime status or Terraform state into Git.

---

## 3. Product behavior and acceptance scenarios

### 3.1 Core interaction

An assistant searches for an entity and relevant registered attributes, reads enough schema to understand their meaning, submits an atomic fact transaction using those identities, and later retrieves the information with sources and explicit completeness indicators.

For a larger graph question, the assistant submits a short JavaScript function. The worker executes deterministic traversal/filtering against a single read snapshot and returns only the requested result. It never decides which ambiguous attribute is semantically correct by selecting the highest search score.

### 3.2 Required user-visible scenarios

1. **Discover and remember:** Search for a model-year attribute; create a fictional device/entity; attach correctly typed facts and a source in one transaction. Retrieve the same facts by ID and canonical identifier.
2. **Prevent naming drift:** A write with an unknown attribute fails and suggests candidates. An exact registered alias resolves to the existing attribute. No duplicate attribute is silently created.
3. **Navigate:** Follow outgoing and incoming references; retrieve a bounded nested projection; produce the equivalent result in Code Mode.
4. **Correct:** Replace a cardinality-one fact using an expected revision. The previous assertion and correction reason remain visible in history. A stale concurrent update fails.
5. **Preserve uncertainty:** Distinguish user-stated information, document-derived information, and model inference. Unknown confidence is null, not a fabricated numerical score.
6. **Explain:** Return fact IDs, source IDs, transaction revisions, and observation timestamps with retrieved values.
7. **Stay read-only:** Code Mode cannot register schema, add a source, change an entity, or transact, even with a forged worker frame or a write-authorized caller.
8. **Recover:** A worker timeout/OOM causes a bounded error; ordinary MCP reads remain available and a later script succeeds after worker recovery.
9. **Deploy:** A tested application change reaches GHCR, updates a reviewed/auto-merged release pin, and is deployed through Nix over private networking.
10. **Restore:** Restore a backup on an isolated test host and verify graph data, schema IDs, provenance, and history.

### 3.3 Performance targets

Use a synthetic benchmark of 10,000 entities and 100,000 current facts, with a separately reported history size. Target warm p95 exact-entity reads below 100 ms and indexed discovery below 250 ms on the reference hardware; report CPU/RAM/disk/concurrency and do not claim these measurements before testing. Bounded script timeout and reliable recovery are correctness requirements, not aspirational performance goals.

---

## 4. Architecture and trust boundaries

```text
MCP client
  │ HTTPS + OAuth access token
  ▼
Nix-managed ingress
  │ loopback HTTP
  ▼
memory-api container ─────────────► native PostgreSQL
  │ authentication, authorization,  Unix socket; scoped roles
  │ schema, queries, transactions
  │
  │ one bidirectional gRPC stream per execution
  │ Unix-domain socket; bounded, typed messages
  ▼
memory-worker container
  Goja runtime; one execution at a time
  no network; no DB socket; no DB/auth/deployment secrets

GitHub-hosted Actions → Tailscale SSH → Nix-managed deployment service
  Bazel artifacts       private link    exact Git revision + image digest
```

The API is the security authority. The worker is treated as potentially compromised: it may invent operations, IDs, limits, and result bodies. The API must authorize every graph callback from its own execution context. Never accept a worker-selected principal, space, SQL query, transaction mode, or capability upgrade.

The worker can access the authorized graph during its live execution. Read-only does not mean nonsensitive: the caller's permitted data may appear in its output. Restrict outbound transport and never mount host secrets. Containers share a kernel and are not VM-strength isolation.

Use separate rootless host identities for API and worker as a hardening refinement over the earlier same-user sketch. Otherwise a worker escape to the same host UID could weaken peer-authentication and filesystem isolation. The socket permission design and tests in Section 15 are mandatory.

No application behavior depends on a Podman API, Nix evaluation, Fly.io, or Tailscale. The Go service takes standard configuration, opens PostgreSQL and gRPC connections, and speaks HTTP MCP.

---

## 5. Domain model and schema registry

### 5.1 Entities, identities, and spaces

Use UUIDs for persistent entity IDs and explicit UUID fields on wire contracts; use random UUIDs for user-created entities. Bootstrap system entities may use deterministic UUIDv5 values derived from `(space UUID, reserved ident)`.

An entity has a space, ID, kind (`ordinary`, `attribute`, `type`, `source`), created revision, current entity revision, and optional archived revision. Kinds are immutable. Names are aliases for IDs, not the storage identity.

A **space** is the authorization and schema boundary. Every data query and foreign key includes `space_id`. No cross-space references in V1. Space IDs come from authenticated request context, never from an untrusted script callback. A supplied space slug is resolved against the principal's grants before use.

Use one `entity_names` relation for canonical identifiers and aliases. A name resolves to exactly one entity within a space regardless of whether it names an ordinary entity, attribute, or type. Old names remain aliases or reserved tombstones and are never reassigned to an unrelated entity.

Attribute/type identifiers use lowercase ASCII namespaces and kebab-case: `vehicle/model-year`, `device/manufacturer`, `type/vehicle`. Ordinary entity idents use the same unambiguous grammar, such as `device/example-receiver`. Display labels remain arbitrary Unicode text. Exact identifier resolution does not stem, case-fold, or guess; lexical search can normalize separately.

### 5.2 Attributes really are entities

Every attribute definition references a row in `entities` with kind `attribute`. Attribute entities can be retrieved by `memory_get`, used as reference values, described, searched, and annotated with application-defined attributes. Types and sources are similarly first-class.

Use a **relational schema projection** for enforcement instead of making bootstrapping depend on circular EAV validation:

- `attribute_defs` is authoritative for protected schema properties such as value type, cardinality, domain, range, documentation, and lifecycle.
- `entity_names` is authoritative for names.
- The entity API exposes those columns as reserved virtual facts (`schema/value-type`, `schema/cardinality`, etc.) with their own stable attribute identities.
- Generic graph reads, pull, find, and incoming traversal must see these virtual facts through a common read relation/adapter, not just a special describe endpoint.
- User-defined annotations on a schema entity are ordinary EAV facts.
- Generic fact transactions cannot write protected virtual attributes. An explicit schema operation updates authoritative rows and corresponding audit events atomically.
- Do not store a second independently editable copy of protected metadata in `current_facts`.

This preserves first-class graph behavior while making PostgreSQL constraints practical. It is a deliberate implementation choice, not a claim of Datomic storage compatibility. Datomic supplies the first-class-schema inspiration [R6].

### 5.3 Required definition fields

| Field | Semantics |
|---|---|
| `id`, `ident` | Stable entity ID and canonical name |
| `description` | Specific meaning; distinguish similar concepts |
| `valueType` | `string`, `boolean`, `integer`, `decimal`, `timestamp`, `ref` |
| `cardinality` | `one` or `many`; many is a set, not an ordered list |
| `appliesTo` | Set of type IDs; empty means unrestricted |
| `referenceTypes` | Allowed target types for `ref`; empty means unrestricted |
| `unit` | Optional canonical unit identifier; no implicit unit conversions |
| `status` | `active` or `deprecated` |
| `replacement` | Optional reference to a distinct replacement attribute |
| `examples`, `notThis` | Brief usage and disambiguation metadata |
| `schemaRevision` | Registry revision after the latest definition change |

V1 type/cardinality/unit/range/domain changes after registration are prohibited. Introduce a new attribute and an explicit migration instead. Descriptions, examples, names/aliases, and deprecation can change with audit history and revision checks. This deliberately avoids dangerous in-place retyping.

Types are first-class named entities; membership is an ordinary cardinality-many `entity/type` reference. No inheritance in V1. Enforce domain/range checks on writes and on type removal/archive so a type change cannot invalidate existing facts unnoticed.

### 5.4 Bootstrap

A versioned migration/bootstrap routine creates the default space and the minimal meta-schema idempotently. Reserve `sys/*`, `schema/*`, and core `entity/*` definitions. At minimum define virtual identity/kind/revision metadata, `entity/label`, `entity/type`, attribute-definition metadata, and source metadata. Keep a checked-in bootstrap manifest with stable IDs and tests for repeated initialization.

Do not seed car, audio, medical, finance, or business attributes as presumed user data. Domain examples belong in test fixtures or an explicitly invoked sample-schema command.

### 5.5 Registry discovery and deliberate evolution

Resolution order: exact UUID → exact canonical ident → exact alias. Only these are authoritative resolutions. Search then offers normalized name, alias, description, and usage matches; fuzzy scores are never permissions to substitute an attribute.

Provide an explicit proposal flow:

1. Search/describe candidates, including their units, cardinality, and `notThis` fields.
2. `memory_schema_propose` stores a proposed definition and returns duplicate/conflict candidates plus the registry revision.
3. `memory_schema_apply`, requiring `memory:schema:write`, accepts a specific proposal with `expectedSchemaRevision` and a rationale for distinctions or reuse.
4. Inside the same write lock as data transactions, recheck names, constraints, candidate state, and the proposal revision. A stale proposal must be reviewed again.
5. Register, add an alias to an existing attribute, or reject. Never silently merge concepts or rewrite old data.

Descriptions cannot prove semantic uniqueness. The guarantee is **no accidental undeclared attributes, unique names, and explicit reviewable schema changes**, not perfect ontology deduplication. The coding agent must not claim otherwise.

---

## 6. PostgreSQL storage and integrity

### 6.1 Relations

Use schema-qualified names under a dedicated PostgreSQL schema such as `memory`. Keep auth tables in a separate `memory_auth` schema. Implement versioned SQL migrations and normal typed SQL; JSONB is allowed for metadata, validated query/audit envelopes, and idempotent responses, not as the sole fact store.

| Relation | Required purpose |
|---|---|
| `spaces` | UUID, slug, current data revision, schema revision |
| `entities` | Composite `(space_id,id)` identity, kind, lifecycle, revision |
| `entity_names` | Unified canonical/alias/reserved-name map |
| `attribute_defs` | First-class attribute entity FK and immutable validation properties |
| `attribute_domains`, `attribute_ranges` | References to first-class type entities |
| `type_defs` | Registered type identity/description |
| `sources` | First-class source entity projection, URI/locator, origin kind, observation metadata |
| `current_facts` | Current typed EAV set, fact UUID, assertion revision |
| `fact_evidence` | Fact-to-source links, confidence and observation metadata |
| `transactions` | Actor/client, revision, timestamp, idempotency key/hash/result |
| `fact_events` | Immutable adds/retractions/evidence changes, transaction and event ordinal |
| `entity_events`, `schema_events` | Auditable lifecycle/name/schema changes |
| `schema_proposals` | Proposed definitions, review information, status |
| `entity_search` | Rebuildable text-search projection, updated with committed changes |
| `memory_auth.*` | Owner sessions, clients, authorization codes, grants, refresh-token hashes |

History events must retain the full typed value and evidence needed to explain a retracted assertion. An event's logical fact ID must not have a foreign key that requires its row to remain in `current_facts`; retraction removes current state, not history. Source records referenced by history cannot be casually deleted.

Use a read view/adapter unifying physical facts with protected metadata projections. Keep virtual fact IDs deterministic or provide an explicit virtual-fact key; never invent a persisted UUID per read. Export format distinguishes virtual metadata from stored assertions to avoid duplicate restoration.

### 6.2 Typed values

`current_facts` has exactly one non-null typed value column:

```text
value_text       text
value_bool       boolean
value_integer    bigint
value_decimal    numeric
value_timestamp  timestamptz
value_ref        uuid
```

Also store `value_type`, `cardinality`, and a canonical-value fingerprint. Enforce `num_nonnulls(...) = 1` and an explicit CHECK mapping each type to its corresponding column. The repeated type/cardinality fields are for constraints, not independent metadata: use a composite FK to a unique `(space_id,id,value_type,cardinality)` key in `attribute_defs`.

Reject SQL NULL as a fact value; absence is absence. A boolean `false`, integer zero, and empty string are present values.

Canonicalization rules:

- Integers: signed 64-bit range, base-10 canonical representation; no floating-point parsing.
- Decimals: exact base-10, normalize equivalent values (e.g. `1.00` equals `1`); V1 input limit 38 significant digits and scale at most 12. Reject excess precision instead of silently rounding. Do not use `float64` as an intermediate.
- Timestamps: require an explicit offset; normalize to UTC at PostgreSQL microsecond precision. Reject sub-microsecond precision rather than silently changing equality.
- References: UUID after name resolution, always same-space, extant and valid for the operation.
- Strings: preserve Unicode exactly; do not normalize user text as an identity-changing side effect. Limit one fact string to 16 KiB UTF-8 initially. Reject PostgreSQL-incompatible NUL characters.
- Units: require the registered canonical unit when a measurement unit is supplied. Reject mismatch. Automatic conversions are out of scope.

Enums are references to named entities, not an additional primitive type. Ordered lists are explicit member/position entities, not cardinality-many values whose order happens to match query output.

### 6.3 Required constraints

At minimum, enforce these invariants in SQL:

```sql
-- Illustrative constraint/index shapes; implement complete migrations.
UNIQUE (space_id, name)  -- entity_names, canonical and aliases together

CREATE UNIQUE INDEX entity_one_canonical_name
  ON memory.entity_names (space_id, entity_id)
  WHERE is_canonical;

CREATE UNIQUE INDEX fact_one_value
  ON memory.current_facts (space_id, entity_id, attribute_id)
  WHERE cardinality = 'one';

CREATE UNIQUE INDEX fact_set_membership
  ON memory.current_facts
    (space_id, entity_id, attribute_id, value_type, value_fingerprint);
```

Every entity, attribute, source, type, and reference FK includes `space_id`. Attribute definitions must point to entities of kind `attribute`; type definitions must point to kind `type`. Implement a composite-kind FK or a migration-tested constraint trigger, not just a convention.

For long values, use SHA-256 over a versioned canonical type/value encoding for indexed set membership. On a fingerprint collision, compare full canonical values. An unequal value must produce `VALUE_FINGERPRINT_COLLISION`, never be treated as a duplicate. This avoids unsupported very-large text B-tree keys while making collision handling explicit.

Canonical names are optional for ordinary entities but required for registered schema entities. Enforce the latter in the schema-transaction path and a deferred constraint trigger or equivalent SQL invariant.

Schema entity IDs and definitions are not immutable SQL table names: attribute IDs are values in parameterized queries, never interpolated SQL identifiers.

Core definitions and metadata cannot be altered through generic EAV paths. Mutable schema rules must be verified even when calling internal APIs rather than MCP handlers.

### 6.4 Indexes and search projection

Add `(space_id,entity_id,attribute_id)` for entity reads; `(space_id,attribute_id,value_ref,entity_id)` for reverse references; typed attribute/value indexes for numeric/timestamp/range matching; revision/entity indexes for event history; a GIN index on `entity_search.tsv`; and exact name indexes.

Index text from names, labels, schema descriptions, and attributes explicitly marked searchable by the fixed registry policy. Do not automatically stuff every large/sensitive field into search snippets. Use PostgreSQL full-text search with a predictable configuration; identifier matching remains exact and separate. `pg_trgm` is optional, not a V1 deployment prerequisite.

Do not claim an application row-return limit bounds PostgreSQL's scanned rows. Bound the query language, use appropriate indexes, statement timeouts, and execution plans; measure actual work with `EXPLAIN (ANALYZE, BUFFERS)` in benchmarks.

### 6.5 Database roles

Use distinct roles:

- `memory_owner`: NOLOGIN owner of application schema/objects.
- `memory_migrator`: administrative login allowed to assume ownership for versioned migrations; never available to API or worker.
- `memory_app`: runtime DML on required tables; no CREATE/DDL, superuser, or role-management privileges.
- `memory_reader`: LOGIN role with SELECT-only graph access, no membership in a writer/owner role, no auth-token tables, used by Code Mode read sessions. Revoke unnecessary database TEMP/CREATE privileges for this role.

The API owns an application pool and a small read-only pool; the worker owns neither. Explicitly set `search_path`, application name, statement/lock timeouts, and transaction settings. Migration SQL defines grants and default privileges for later tables.

Native production connections use Unix sockets. Peer authentication mappings are described in Section 15; TCP/SCRAM is allowed for development/test portability and a future remote database, with certificate verification for nonlocal production TCP.

---

## 7. Transactions, provenance, and history

### 7.1 One simple serialization boundary for V1

Serialize writes per space with a PostgreSQL row lock on `spaces`, acquired **before** reading mutable graph/schema state. This is an intentional simplification for one user's workload, not a proposed high-throughput multi-tenant design. Under the lock, use READ COMMITTED, validate preconditions against the latest state, and increment the space revision for every committed mutation transaction. All graph/schema write entrypoints use this path. PostgreSQL row locks and read snapshots supply the underlying behavior [R7], [R8].

A monotonically increasing per-space revision allocated under that lock is the ordering token. Do not use a global sequence or a timestamp as an assertion that independent transactions committed in that order.

### 7.2 Transaction contract

```json
{
  "space": "personal",
  "idempotencyKey": "example-client-request-0001",
  "expectedSchemaRevision": "7",
  "reason": "Record a fictional device supplied by the owner",
  "operations": [
    {
      "op": "createEntity",
      "tempId": "new-device",
      "ident": "device/example-receiver",
      "types": ["type/audio-device"]
    },
    {
      "op": "assert",
      "entity": {"tempId": "new-device"},
      "attribute": "device/model",
      "value": {"type": "string", "value": "Example 100"},
      "evidence": [{"source": "source/example-owner-message"}]
    }
  ]
}
```

The fixture source/type/attribute above must already exist or be created through an allowed operation; examples do not imply production bootstrap data.

V1 operations: `createEntity`, `createSource`, `assert`, `replace`, `retract`, `addEvidence`, `addAlias`, `renameEntity`, and `archiveEntity`. Type membership uses the registered `entity/type` attribute. Creating attribute/type definitions and changing their protected metadata uses the schema API, not `createEntity` with a forged kind.

`replace` and `retract` on existing values require an expected entity revision or exact expected fact ID/value. Provide bounded transaction-wide entity preconditions for batch writes. Creating a genuinely absent scalar can use `assert` with an absent precondition. `assert` is not a silent last-write-wins update: a different existing cardinality-one value returns a conflict.

Many-valued `assert` adds a set member; adding the exact same member is a no-op unless evidence changes. Adding a duplicate evidence link is also a no-op. `replace` applies to cardinality-one only; for many, use explicit retract/add operations.

### 7.3 Commit algorithm

1. Authenticate and resolve authorized space and scopes. Bound request bytes and operation count.
2. Parse strict typed input; reject unknown fields that affect semantics.
3. Begin SQL transaction; lock the space row.
4. Resolve `(principal,space,idempotencyKey)`. Same canonical request hash returns the previous committed response; a different hash returns `IDEMPOTENCY_CONFLICT`. Authorization is rechecked before replay.
5. Resolve schema names, aliases, entity references, temporary IDs, and registry revision.
6. Allocate all temp entity IDs, then validate ordered operations against a bounded proposed state. Temporary references may target entities created in this transaction.
7. Validate cardinality, kinds, domain/range, names, lifecycle, unit, provenance, and optimistic preconditions. Validate the final state for type removals and inbound references as well.
8. Increment space revision; write entity/fact/evidence/schema projections and append corresponding events atomically. Update touched entity revisions once per transaction.
9. Update search projections; store response and request hash in the transaction record.
10. Commit, then publish cache invalidation/metrics. Return canonical IDs, revision, applied/no-op operations, and warnings.

On failure, rollback everything. Retry only narrowly classified PostgreSQL deadlock/serialization failures with a bounded retry count; preserve the request's idempotency and preconditions. A lost network response after commit is safe to retry with the same key.

### 7.4 Provenance

Every added assertion needs at least one source. A source is a first-class entity containing origin category (`user`, `document`, `import`, `inference`, `system`), optional URI/document locator, a bounded description/excerpt, and optional observed time. Source contents are untrusted data and are never fetched automatically.

Server-derived transaction metadata includes authenticated actor, OAuth client ID, server receive/commit time, request ID, reason, and source revision. These fields cannot be overridden by the model. A client's claimed conversation ID is source metadata, not a verified identity.

Evidence can include `confidence: null | number` in [0,1], the method that produced it, and `observedAt`. Confidence is optional and uncalibrated unless a method explicitly establishes otherwise. Do not turn search ranking scores into fact confidence.

### 7.5 Corrections, temporal behavior, and archival

Current facts mean the latest explicitly accepted assertions, not automatically resolved objective truth. Competing claims can be represented as separate claim/observation entities; do not automatically choose the most recent source as truth.

Replacing a fact emits a retraction and assertion in one transaction, retaining the reason and evidence. An observation's real-world timestamp is separate from the server's transaction timestamp. V1 offers paginated event history, not a general historical graph query API. Keep observation timestamps and optional source validity metadata without pretending that overlap resolution/bitemporal semantics exist.

Archiving an ordinary entity hides it from default search and disallows new ordinary facts; retained references can render a minimal archived stub. Block archival of in-use schema/types except through supported deprecation behavior. Never cascade-delete history accidentally.

**Archival/retraction is not erasure.** Document this in operator help and MCP descriptions. Permanent forgetting requires explicit owner-admin data removal and backup-retention handling; do not advertise V1 as providing irreversible erasure from all backups.

---

## 8. Retrieval, search, and graph queries

### 8.1 Common response semantics

Return canonical IDs/names, space revision, schema revision, requested values, and sufficient provenance to explain answers. Include explicit `complete`, `nextCursor`, and any truncation reason. Never silently truncate and let a client infer that no other data exists.

An unknown attribute is an error, even during reads. A known cardinality-one attribute with no assertion is absent/null according to the documented projection shape; a known many-valued attribute has an empty collection. Never collapse `false`, zero, or an empty string into absence.

Search returns candidates, not a guessed entity. Exact ID lookup failures do not automatically become name search. Keep denied vs nonexistent IDs indistinguishable to unauthorized callers.

### 8.2 Search and schema discovery

`memory_search` supports `kind`, text query, registered type filters, limit, and cursor. Rank exact names, aliases, labels, then full-text matches with deterministic tie-breaking. Return short relevant snippets and IDs, not entire entities by default.

`memory_schema` supports `search`, `describe`, `list`, `types`, and `help`. A describe result includes complete definition/aliases/examples, usage count if cheap, and revision. Return all registered relevant attributes by pagination; search must not be the only way to enumerate schema.

Start with lexical/full-text search. No embedding infrastructure or external LLM calls are required.

### 8.3 Structured find

Implement a small validated AST, not a string language and not Datalog. Initial predicates:

- `all`/`any` with bounded depth and term counts.
- `exists` for a registered attribute.
- `eq`, `lt`, `lte`, `gt`, `gte`, and bounded `in`, with type checking.
- Explicit reference paths up to four hops, each step specifying `out` or `in`.
- Registered type filtering and bounded text query.

For V1, do not implement arbitrary negation, user functions, cross-products, unbounded recursion, or arbitrary order expressions. Sort by canonical entity ID; optional relevance sort is only for search. This keeps pagination and query planning tractable.

Example:

```json
{
  "space": "personal",
  "where": {
    "all": [
      {"attribute": "entity/type", "op": "eq", "value": {"type": "ref", "value": "type/audio-device"}},
      {"attribute": "device/has-optical-output", "op": "eq", "value": {"type": "boolean", "value": true}}
    ]
  },
  "select": ["entity/label", "device/model"],
  "limit": 50
}
```

Resolve references before compiling parameterized SQL. Whitelist operators and identifiers; all values are SQL parameters. Use typed columns and indexes rather than loading the graph into Go or JavaScript to filter it.

### 8.4 Pull and incoming references

The pull pattern is a JSON object mapping attribute identifiers to `true` for scalar/ref-ID selection or to a bounded nested projection for references:

```json
{
  "select": {
    "entity/label": true,
    "device/manufacturer": {"select": {"entity/label": true}},
    "device/connected-to": {
      "select": {"entity/label": true, "device/model": true},
      "limit": 20
    }
  },
  "incoming": [
    {
      "attribute": "device/connected-to",
      "select": {"entity/label": true},
      "limit": 20
    }
  ]
}
```

Do not implement wildcard expansion or implicit recursive `*` in V1. Detect cycles using `(entity,projection-path)` and enforce depth/edge/entity budgets independently. Mark cycle references as stubs instead of looping. Return per-edge continuation metadata where a nested collection is cut off; the caller can use an incoming/reference page operation to continue.

Batch entity/attribute reads and perform breadth-level pull expansion to avoid one SQL query per field. Keep query counts visible in execution stats. A synchronous JavaScript API does not justify hidden unbounded N+1 queries.

### 8.5 Snapshot and cursor consistency

Each direct compound read and each Code Mode execution gets one short `REPEATABLE READ READ ONLY` transaction. Acquire its snapshot with an initial revision read. All schema and data accessed during the operation use that snapshot, including virtual schema metadata [R8].

A cursor contains/version-binds the space, query hash, last sort key, schema/data revision, and expiry; sign it with a persisted runtime secret. On a new request, if the current revision differs, return `CURSOR_STALE` and instruct a restart rather than implying a persistent snapshot. Within one Code Mode execution, subsequent pages use the same transaction and revision. No exported PostgreSQL snapshots are held across client turns.

Cache schema by `(space_id,schema_revision)`, not just a global latest value. A global cached schema newer than an execution's read snapshot must never be used. Per-execution entity caches die with the execution.

---

## 9. MCP interface

Use the repository-compatible official Go MCP SDK and Streamable HTTP. Pin and document the negotiated protocol versions; the currently published specification reviewed for this plan is 2026-07-28. Do not implement a custom approximation of MCP JSON-RPC or depend on a newer SDK API without updating/testing its pinned dependency [R9], [R10].

### 9.1 Tools

| Tool | Purpose | Required scope |
|---|---|---|
| `memory_search` | Find entity/source/schema candidates | `memory:read` |
| `memory_get` | Retrieve specified entities/attributes and evidence | `memory:read` |
| `memory_find` | Bounded structured predicates and projection | `memory:read` |
| `memory_pull` | Bounded nested graph projection | `memory:read` |
| `memory_history` | Paginated fact/entity/schema events | `memory:read` |
| `memory_schema` | Search/list/describe schema, types, and Code Mode API help | `memory:read` |
| `memory_transact` | Atomic ordinary graph mutations | `memory:write` |
| `memory_schema_propose` | Store a schema proposal; never register implicitly | `memory:write` |
| `memory_schema_apply` | Explicitly register/alias/deprecate a reviewed definition | `memory:schema:write` |
| `memory_code` | Run bounded read-only JavaScript | `memory:read` + `memory:execute` |

Keep names stable. Tools must have strict input and output JSON schemas, `additionalProperties: false` where appropriate, descriptive parameter documentation, and accurate MCP annotations. Read/write annotations inform clients but never replace authorization checks. Do not expose a single unconstrained `operation: string` tool that can bypass scope checks.

`memory_get` supports a bounded entity-ID array so callers do not need one round trip per entity. `memory_schema` help returns the compact SDK contract, numeric representation rules, and copyable examples; it is also available as an MCP resource, but tool discovery must not depend on clients automatically reading resources.

### 9.2 Responses

Use structured content with a consistent envelope, and a short text representation for clients that do not display structured content. Example shape:

```json
{
  "ok": true,
  "data": {},
  "meta": {
    "requestId": "server-generated-uuid",
    "spaceRevision": "42",
    "schemaRevision": "7",
    "complete": true,
    "nextCursor": null
  }
}
```

Revisions and signed 64-bit wire integers are decimal strings. Do not emit JSON numbers that lose precision in JavaScript clients. Facts use a tagged `Value` union:

```json
{"type":"integer","value":"9223372036854775807"}
```

Decimal values similarly use exact strings; booleans remain JSON booleans, timestamps are UTC strings, and ref values are canonical UUID strings. Projection wrappers distinguish values, metadata, and continuation information unambiguously.

Errors have stable codes, a safe message, request ID, retryability, and optional structured details/candidates. Use MCP tool-error semantics for graph/execution errors, and HTTP authentication errors for absent/invalid credentials. Do not expose SQL, credentials, host paths, full stack traces, or private data through errors.

Required errors include:

```text
ENTITY_NOT_FOUND       ATTRIBUTE_NOT_FOUND     ATTRIBUTE_AMBIGUOUS
INVALID_VALUE_TYPE     CARDINALITY_VIOLATION   DOMAIN_VIOLATION
REFERENCE_TYPE_MISMATCH NAME_CONFLICT          SCHEMA_CONFLICT
VERSION_CONFLICT       IDEMPOTENCY_CONFLICT    CURSOR_STALE
PERMISSION_DENIED      EXECUTION_TIMEOUT       RESOURCE_LIMIT_EXCEEDED
QUERY_LIMIT_EXCEEDED   WORKER_UNAVAILABLE      WORKER_BUSY
SCRIPT_SYNTAX_ERROR    SCRIPT_RUNTIME_ERROR    UNSUPPORTED_RESULT
VALUE_FINGERPRINT_COLLISION                  INCOMPATIBLE_PROTOCOL
```

No silent success with a partial write or a truncated script result. Query pagination is intentional partial retrieval; execution output overflow is an explicit error.

### 9.3 HTTP behavior

Expose `/mcp`, authorization metadata/routes, `/healthz` (liveness), and private `/readyz` and `/metrics` endpoints. Use a separate admin listener/Unix socket or strict ingress exclusions for diagnostic endpoints. Public liveness reveals no graph or infrastructure details.

Validate Host/Origin against configured public origins, validate bearer tokens on every request, bound HTTP body sizes, support SDK cancellation, and do not treat MCP session IDs as authentication. Prefer request-independent application state: a restart can invalidate transport sessions without losing graph data or OAuth grants.

No unauthenticated remote deployment mode. A local test-only auth bypass must bind only to loopback/Unix sockets and be rejected by production configuration validation.

---

## 10. JavaScript Code Mode

### 10.1 Language and entrypoint

V1 accepts ordinary JavaScript source defining one synchronous function:

```javascript
function main(input) {
  const device = db.entity(input.entity);
  return {
    id: device.id,
    model: device.get("device/model")
  };
}
```

The worker compiles/runs the source, checks `main` is callable, calls it once with bounded structured input, and safely encodes the result. Document that this is Goja JavaScript, not Node.js, a browser, TypeScript, or an ES module environment. Provide `.d.ts` files as documentation/editor aids, not as executable TypeScript support.

No npm packages, `require`, `import`, `fetch`, `process`, `fs`, timers, subprocesses, sockets, or Go reflection bridges. Do not install `goja_nodejs`. All database functions are synchronous from the script's perspective. Async functions/promises are unsupported return values; do not quietly return an unfulfilled promise or add an event loop.

A fresh Goja runtime is created for every execution. Ordinary VM access stays on one goroutine; cancellation uses the supported interrupt mechanism. Goja documents that interruption does not interrupt native Go calls, so each host callback independently honors its Go context [R11].

### 10.2 Exposed read API

Implement this compact conceptual contract, with detailed types in `sdk/code-mode.d.ts`:

```typescript
interface Page<T> {
  items: T[];
  complete: boolean;
  nextCursor: string | null;
}

interface Entity {
  readonly id: string;
  readonly ident: string | null;
  readonly revision: string;
  get(attribute: string): string | boolean | bigint | null;
  values(attribute: string, options?: PageOptions): Page<Scalar>;
  ref(attribute: string): Entity | null;
  refs(attribute: string, options?: PageOptions): Page<Entity>;
  incoming(attribute: string, options?: PageOptions): Page<Entity>;
  pull(pattern: PullPattern): PullResult;
  facts(attributes?: string[]): FactPage;
}

interface Graph {
  entity(idOrIdent: string): Entity;
  entities(idsOrIdents: string[]): Entity[];
  search(query: SearchQuery): Page<SearchHit>;
  find(query: FindQuery): Page<Entity>;
  pull(idOrIdent: string, pattern: PullPattern): PullResult;
  history(query: HistoryQuery): Page<HistoryEvent>;
  schema: {
    search(query: AttributeSearch): Page<AttributeDefinition>;
    describe(idOrIdent: string): AttributeDefinition;
    list(query?: SchemaListQuery): Page<AttributeDefinition>;
    types(query?: SchemaListQuery): Page<TypeDefinition>;
  };
  help(topic?: string): object;
  toSafeNumber(value: bigint): number;
}
```

The complete types must distinguish timestamp/decimal/ref strings through attribute metadata and the typed `facts` representation. `get` supports cardinality-one only; `values` handles scalar many-valued attributes. `ref`/`refs` reject non-reference or mismatched-cardinality attributes instead of guessing.

`entity` resolves and verifies existence; it does not allocate. Entity handles are opaque host objects bound to the execution. Do not expose mutable Go structs, database pool objects, or exported Go methods automatically. Assigning a JavaScript property does not update the database.

`get` returns:

- string → JavaScript string;
- boolean → boolean;
- integer → `bigint` consistently, even for small values;
- decimal → canonical exact decimal string;
- timestamp → canonical UTC string;
- reference → UUID string; use `ref` for navigation;
- known-but-absent scalar → `null`.

`db.toSafeNumber` rejects integers outside JavaScript's safe-integer range. Do not silently cast int64s or exact decimals to `Number`. Register and test BigInt conversion on the pinned Goja revision. Programmatic exact decimal arithmetic is not V1 functionality.

### 10.3 Examples delivered with the SDK

Schema exploration should return candidates to the model for semantic judgment:

```javascript
function main(input) {
  return db.schema.search({query: input.concept, limit: 10});
}
```

A bounded graph navigation example:

```javascript
function main(input) {
  const device = db.entity(input.entity);
  const page = device.refs("device/connected-to", {limit: 20});
  return {
    device: {id: device.id, label: device.get("entity/label")},
    connections: page.items.map(e => ({
      id: e.id,
      label: e.get("entity/label")
    })),
    complete: page.complete,
    nextCursor: page.nextCursor
  };
}
```

Also ship examples for batched pull, incoming edges, exact integer handling, history with sources, cursor continuation, unknown-attribute recovery, and a deliberately rejected write. Each example is an executable test against synthetic data, not just documentation.

### 10.4 Result encoding

Use a bounded host-controlled encoder. Support null, booleans, finite numbers, strings, arrays, plain objects, BigInts, and recognized entity handles. BigInts become tagged `{"type":"integer","value":"..."}` objects; entity handles become ID/ident stubs, never implicit full graph expansion. Exact-decimal strings stay strings unless carried in a typed fact. Tagged integer results must fit the signed 64-bit wire contract; reject larger computed BigInts with `UNSUPPORTED_RESULT`, or let the script explicitly return their decimal string using `.toString()`.

Reject cycles, functions, symbols, promises, non-finite numbers, unknown host objects, unsupported prototypes, and excessive nesting with explicit errors. For undefined: reject a top-level undefined result and undefined array/object members instead of silently dropping data.

Do not blindly call an arbitrary object's `toJSON`, getters, `String`, or recursive `Export()` outside the execution budget. Enumeration/serialization can execute JavaScript too. Keep the watchdog active through serialization, count depth/nodes/bytes, and test hostile getters/proxies. If safely extracting a value would invoke unsupported behavior, reject it.

Bound console diagnostics separately from structured return data. `console.log` is captured, never sent directly to process stdout as an uncontrolled stream. User logs are not the authoritative result.

### 10.5 Budgets and failure handling

Initial defaults, configurable downward by a caller but upward only by trusted configuration:

| Limit | Default |
|---|---:|
| Script source | 64 KiB |
| Input JSON | 64 KiB |
| Soft execution deadline, including compilation/serialization | 5 seconds |
| Process watchdog deadline | 7 seconds |
| Simultaneous scripts | 1 |
| Waiting executions | 0; return `WORKER_BUSY` with retry guidance |
| Host graph operations | 100 |
| SQL statements per execution | 200 |
| Database statement timeout | 1 second or remaining execution time, whichever is lower |
| Decoded DB rows across one execution | 10,000 |
| Unique entities loaded | 1,000 |
| Traversed reference edges | 2,000 |
| Pull depth | 5 |
| Total host-to-worker graph payload | 4 MiB |
| Structured script result | 256 KiB |
| Diagnostic logs | 16 KiB |
| Result nesting | 32 levels |
| Worker cgroup memory | 256 MiB initially |
| Worker process count | 64 |

The API enforces its own copies of graph/SQL/payload/time limits; a worker cannot reset them. Cap gRPC message sizes before allocation and avoid constructing oversized results just to reject them afterwards.

Use a Goja interrupt for normal timeouts, context cancellation for RPC/SQL, and a worker-process watchdog that terminates the worker if cooperative cancellation does not finish. Systemd/Podman restarts a failed worker. This watchdog is recovery machinery, not proof against arbitrary native-code compromise. API deadlines must independently release connections and return a bounded error even if the worker never cooperates.

Use container/cgroup memory and CPU controls as the hard resource boundary. `GOMEMLIMIT` is useful headroom tuning, not a hard quota. Do not invent a Goja instruction-metering guarantee or treat JavaScript stack limits as a memory limit. Parent/API and PostgreSQL must survive the worker's tested OOM case.

### 10.6 Dependency security gate

Use a reviewed Goja commit containing fixes for disclosed vulnerabilities; record the selected revision and review in `docs/security.md`. A September 2026 primary research report documents a Goja sandbox escape through a TypedArray bug and recommends updating affected consumers [R12]. Do not reproduce exploit code in the project or assume “pure Go” establishes sandbox safety.

Run dependency/security scanning as a release gate and include harmless regression tests for relevant boundary cases. A clean vulnerability scan alone is not evidence that an interpreter is safe; the container and server-side capability boundary remain required.

---

## 11. Worker protocol and execution lifecycle

### 11.1 RPC contract

Create `memory.worker.v1.Worker` with `Execute(stream ApiFrame) returns (stream WorkerFrame)` and a small protocol/health handshake. Generate Go bindings through Bazel. Use typed Protobuf messages, not arbitrary Go gob values or unvalidated JSON operation names.

Logical frame types:

```text
API → worker:
  StartExecution {execution_id, protocol_version, source, input, effective_limits,
                  sdk_version, schema_revision, space_revision}
  GraphResponse  {call_id, typed_result | error}
  Cancel         {reason}

Worker → API:
  Started        {execution_id, protocol_version, worker_version}
  GraphRequest   {call_id, oneof read operation + typed arguments}
  Completed      {typed_value, diagnostics, execution_stats}
  Failed         {safe_error, diagnostics, execution_stats}
```

The read-operation `oneof` enumerates lookup, batch lookup, attribute reads, refs, incoming, find, pull, history, and schema discovery. There are **no write variants** in the V1 worker protocol. Reserve field numbers for evolution, but do not expose dormant write methods.

`input` and results may use bounded JSON bytes where the shape is intentionally user-defined. Core value/query/provenance types have explicit Protobuf definitions. Do not use `google.protobuf.Struct` doubles for exact integer/decimal fields.

One stream handles one execution and one outstanding synchronous callback at a time. Maintain a single sending owner/queue and a single receiving loop per direction. Validate monotonic unique call IDs, legal state transitions, deadlines, sizes, duplicate responses, and unexpected frames. A second Start or a GraphRequest after completion is a protocol error.

### 11.2 Lifecycle

1. API authenticates, checks scopes/space, reserves the execution slot, and validates input.
2. Verify worker readiness/protocol compatibility before consuming a scarce DB connection.
3. API opens a reader-role transaction, applies deadlines, and reads schema/data revisions to establish the snapshot.
4. API opens the stream and sends Start with effective limits. No bearer tokens, DB credentials, or general authority tokens are sent.
5. Worker creates the VM; synchronous host functions send a GraphRequest and wait for the matching response.
6. API dispatches only registered read methods through the existing graph layer and execution-owned transaction. Every read is scoped by the API's context, not a field supplied by the worker.
7. Worker returns Completed/Failed. API validates and bounds the returned value, closes the stream, rolls back/releases the read transaction, and returns the MCP result.
8. On client cancellation, timeout, protocol error, or disconnect, cancel SQL/RPC, release resources, and record the correct failure. Do not return pre-error output as a successful answer.

Use a fresh connection/stream after worker restart. Do not replay an interrupted script automatically: return a retryable infrastructure error and let the caller decide. Reads are safe to retry, but silent replay obscures failures and can duplicate diagnostic output.

### 11.3 Unix-socket and remote portability

The worker owns a socket under `/run/structured-memory/worker.sock`. Only remove an existing path after checking it is an owned socket, not a symlink or arbitrary file. Take a startup lock so two workers cannot replace each other's socket. Ensure the IPC parent directory is host-created, not chosen by a request.

Use filesystem/ACL controls for local authentication and verify peer credentials where the chosen gRPC dial/listen adapter supports it. Do not rely on the socket pathname being obscure. Network transport configuration must reject unauthenticated remote endpoints; future TCP support requires mTLS and the same execution authorization model.

The API should retry initial worker connection for a bounded startup period but keep normal MCP reads available if the worker is down. Code Mode readiness and core graph readiness are separate health dimensions.

---

## 12. Authentication and authorization

### 12.1 Production baseline

Ship a working single-owner OAuth flow, not a placeholder bearer-token check that prevents actual MCP client use. Reuse audited repository components where possible, particularly MeMode's existing auth experience, but do not copy permissive development defaults or silently share its tokens/database. A maintained OAuth implementation is preferable to handwritten cryptography; isolate the auth adapter so the graph service only sees a validated `Principal`.

The service acts as an MCP resource server with resource-bound tokens. A small co-hosted authorization-server component is acceptable for V1. Discovery, registration, resource indicators, and token validation must follow the supported MCP authorization specification [R10]. The reviewed 2026-07-28 specification prefers Client ID Metadata Documents/pre-registration and retains dynamic registration for backward compatibility; do not hard-code an earlier DCR-only model.

### 12.2 Owner and client behavior

- Exactly one configured owner account in V1; no public sign-up or password-reset service.
- Owner login credential supplied as a slow password hash through a secret file; never plaintext in Nix/Git. Use a maintained verifier, rate limiting, CSRF protection, secure HttpOnly cookies, and short-lived login sessions.
- Owner explicitly consents to client identity, allowed spaces, and scopes. Default consent is read + execute; write and schema administration require explicit selection.
- Authorization-code flow with S256 PKCE, exact redirect matching, single-use short-lived codes, issuer/resource binding, and no implicit grant/password grant.
- Short-lived access tokens (initially 15 minutes), signed with a persisted secret key; validate algorithm allowlist, signature, issuer, audience/resource, expiry, scope, and grant revocation.
- Rotating refresh tokens (initial maximum lifetime 30 days), store hashes only, detect replay and revoke the affected token family. Include cleanup jobs.
- Persist clients, consent, token families, and owner sessions in PostgreSQL; API restart must not reset trust or mint a new signing key.
- Support pre-registered clients. Implement metadata-document registration with bounded HTTPS retrieval and explicit hostname/IP safety checks; no arbitrary SSRF to loopback/LAN/link-local addresses, redirects to those addresses, or DNS rebinding.
- Legacy dynamic client registration is off by default and only enabled when a verified client requires it. Registering a client never grants graph access without owner consent; apply limits and expiry to unused registrations.
- Provide owner-admin commands to revoke a client/grant and rotate keys. Rotation preserves a bounded overlap for verification, not indefinite old-key acceptance.

### 12.3 Resource-server controls

Serve protected-resource and authorization-server discovery at the correct well-known paths for the configured public URL. Return proper 401 challenges. Check token audience; no pass-through acceptance of arbitrary provider access tokens. Bind MCP requests to the validated principal independently of session IDs.

Use a single authorization abstraction across direct MCP, schema operations, history, exports, and worker graph callbacks. `memory:execute` never implies write. `memory:write` never implies `memory:schema:write`. A principal's granted spaces limit every lookup, including error suggestions and search snippets.

Do not expose owner login through an unauthenticated administrative command runner. Local operator commands use a separate explicit administrative path/OS identity and are not callable from Code Mode.

### 12.4 Public ingress versus deployment networking

Tailscale CD connectivity does **not** make the MCP endpoint reachable by a cloud client. Configure a separate public HTTPS ingress and test it from outside the LAN.

Deliver a Nix-managed Caddy reverse-proxy adapter for a configured public hostname, forwarding only application/auth routes to API loopback. The operator may instead supply an existing outbound HTTPS tunnel to that loopback endpoint. Keep this a deployment setting; do not embed a tunnel SDK into the application. No public SSH or PostgreSQL port is needed. Do not open public HTTP/HTTPS ports unless the selected ingress mode requires them and the operator enables that mode.

Both ingress modes must preserve authorization headers and MCP streaming/cancellation behavior, exclude private metrics/diagnostics, and supply correct forwarded-header trust configuration. TLS and a secret URL alone are not authorization.

---

## 13. Go implementation and commands

### 13.1 Shared service layer

Use context-aware interfaces for schema resolution, graph reads, transactions, and sources. MCP handlers and worker callback dispatchers call the same implementations; no separate SQL shortcuts in the runtime bindings. Keep DB transaction lifetime explicit through a `ReadSession`/`WriteSession` abstraction.

Prefer typed request/value structs over widespread `map[string]any`. Use a tagged value union with validating constructors, structured errors, canonical JSON hashing, and exhaustive switches. Validate both at the transport boundary and at invariant-sensitive service methods.

Reuse repository logging/telemetry/config helpers when compatible. Generate Protobuf and, if useful within established repository conventions, sqlc code through Bazel. Do not make optional code generation tools a production runtime dependency.

### 13.2 Commands

The single `/memoryd` binary provides:

| Command | Purpose |
|---|---|
| `serve` | MCP, auth, graph, and admin health listeners |
| `worker` | Read-only Goja worker over Unix socket |
| `migrate status` / `migrate up` | Explicit, lock-protected schema migration |
| `healthcheck api` / `healthcheck worker` | Distroless-compatible health probes |
| `admin bootstrap` | Idempotent space/meta-schema/owner initialization |
| `admin schema ...` | Owner-controlled schema proposal/apply/review |
| `admin export` / `admin import` | Versioned graph export and empty-space restore |
| `admin auth ...` | Client/grant revocation and key-management support |
| `version --json` | Binary revision, SDK/RPC versions, schema compatibility range |
| `smoke` | Bounded synthetic/local integration check without real-user mutations |

Production migrations/admin operations run through Nix-defined administrative units, not arbitrary shell entry from an MCP tool. Export/import commands are operator-only in V1 and emit no auth secrets in graph exports.

### 13.3 Configuration

Expose documented `MEMORY_*` flags/environment/file settings. At minimum configure public base URL, HTTP/admin addresses, database socket/DSNs and pool sizes, worker endpoint, code limits, auth secret-file paths, trusted origins, logging level, and telemetry destination.

Prefer `*_FILE` for secrets. Reject conflicting flag/environment values where precedence could silently change security. Worker startup rejects any DB/owner/deployment credentials accidentally supplied. It must not load `.env` files from arbitrary working directories in production.

Graceful shutdown stops accepting new work, cancels/finishes bounded reads, drains HTTP, cancels worker streams, and releases DB transactions. Provide clear startup failure messages for schema incompatibility or missing secrets; no automatic DDL in `serve`.

---

## 14. Bazel build and OCI images

### 14.1 Build integration

Use root `rules_go`/Gazelle and `platform_binary` conventions. Generate the new Go library/binary targets, Protobuf bindings, tests, embedded migrations, SDK declarations/examples, and version metadata under `projects/memory`.

Build one image with entrypoint `/memoryd`, using `binary_oci_image` and the repository's digest-pinned distroless base. Select `serve`, `worker`, or an administrative subcommand at runtime. Preserve native amd64/arm64 testing and multi-platform publication [R2], [R3], [R13].

Intended target contracts (verify actual generated labels):

```text
//projects/memory/cmd/memoryd:memoryd
//projects/memory/cmd/memoryd:memoryd_linux_gnu_amd64
//projects/memory/cmd/memoryd:memoryd_linux_gnu_arm64
//projects/memory/cmd/memoryd:memory_image
//projects/memory/integration:integration_test
//projects/memory:code_mode_examples_test
```

Register `("memory", "//projects/memory/cmd/memoryd:memory_image")` in root `_OCI_IMAGES`. Use the resulting existing-style push target and `//:push_images` integration. Do not create a second Docker build workflow.

### 14.2 Image requirements

No shell, compiler, Go toolchain, Bazel, Nix, database credentials, or private fixtures are required in the image. Embed SQL migrations and SDK docs using Bazel/Go resources. Do not rely on `curl`/`sh` for container probes; use `memoryd healthcheck`.

Document the actual entrypoint path, container UID behavior, required CA certificates, timezone behavior, writable directories, and architectures. Nix controls the runtime nonroot UID; test `--userns=keep-id` rather than assuming a distroless image's default identity is compatible.

Include OCI source/revision/version annotations using existing stamping. Record the resulting multi-architecture index digest and the amd64 child manifest digest. Deploy the index digest, allowing Podman to choose the host platform; verify which child was executed during health reporting.

### 14.3 Reproducibility and release evidence

Pin dependency/toolchain/base versions through existing lock mechanisms. Test repeat builds of the same source with the same stamp inputs. Reproducibility means identical inputs produce identical artifacts, not that two distinct Git revisions necessarily yield identical digests.

Store a release record containing source commit, image repository/index digest, architecture digests, build workflow/run ID, test results, and DB compatibility range. Retain current and prior deployed images; a Git SHA tag alone is mutable and not the deployment identity.

Security scanning must inspect the produced image and Go dependencies. Image provenance/attestation is desirable; do not make an unimplemented signature-verification claim. The V1 minimum is authenticated trusted publication, immutable digest pinning, tested release gates, and restricted promotion.

---

## 15. NixOS host and container deployment

### 15.1 Nix module boundary in the multi-IaC GitOps repo

Implement the reusable `services.structured-memory` module at `iac/nixos/modules/structured-memory.nix` and the host definition at `iac/nixos/hosts/infra-1/`. Put `flake.nix` and `flake.lock` in `iac/nixos/`, **not** in the Codebase repo root or GitOps root. Use the pinned stable Nixpkgs input; NixOS 26.05 is the reviewed initial release [R14], not a requirement to stay on it indefinitely. Preserve `system.stateVersion` from the host installation.

Define `nixosConfigurations."infra-1"` as the flake output and configure host identity as `networking.hostName = "infra-1"; networking.domain = "11r.io";`. This gives OS FQDN `infra-1.11r.io`; do not automatically publish a DNS A/AAAA record or reuse that FQDN for the public MCP endpoint. Tailnet SSH routing must be verified independently.

Use `virtualisation.oci-containers` with `backend = "podman"` for the two rootless containers; set `podman.user` explicitly and test actual option behavior at the locked Nixpkgs revision [R15]. Production module custom options:

```nix
services.structured-memory = {
  enable = true;
  releaseFile = ../releases/memory.json;
  publicBaseUrl = "https://REPLACE_WITH_PUBLIC_MCP_HOST/mcp";
  api = { port = 8080; memoryLimit = "512m"; };
  worker = { memoryLimit = "256m"; timeoutMs = 5000; maxConcurrent = 1; };
  database.name = "memory";
  secrets = {
    ownerPasswordHashFile = "/run/secrets/memory-owner-password-hash";
    signingKeyFile = "/run/secrets/memory-signing-key";
    cursorKeyFile = "/run/secrets/memory-cursor-key";
  };
  ingress.mode = "caddy";
};
```

This is a design-time custom option interface, not a preexisting NixOS option. Resolve actual relative `releaseFile` paths within the module based on the final call site. Add production assertions for placeholders, invalid digests, absent secrets, unsafe binds, and disabled auth. Add a synthetic VM fixture independent of real hardware or credentials.

Bazel exclusively compiles the application/builds images; Nix may build host packages/VM tests/operational wrappers but may not use `buildGoModule`, Nix Docker image builders, or Bazel to recreate the application artifact. Do not introduce Terraform evaluation into `nix flake check`; Terraform is not present in V1.

Run validation from the private GitOps checkout:

```bash
nix flake check ./iac/nixos
nix build './iac/nixos#nixosConfigurations.infra-1.config.system.build.toplevel'
```

Include only V1 NixOS-owned paths in the `nixos-validate` workflow, plus changes to shared governance docs/workflows when appropriate. Expose independent checks if further IaC roots are added later.

### 15.2 Release manifest

`iac/nixos/releases/memory.json` in the **private infrastructure repo** is the only desired application artifact pin:

```json
{
  "formatVersion": 1,
  "repository": "ghcr.io/piperswe/codebase/memory",
  "indexDigest": "sha256:REPLACE_WITH_REAL_64_HEX_DIGEST",
  "sourceRevision": "REPLACE_WITH_TESTED_SOURCE_COMMIT",
  "schemaCompatibility": {"minimum": 1, "maximum": 1},
  "workerProtocolMajor": 1
}
```

The example must not be committed as an activatable production pin. Codebase CI proposes changes in the GitOps repository; it does not commit deployment state back to `piperswe/Codebase`. Real fields are generated from tested build output. Do not include a `deploymentRevision` in the same file that would need to contain its own commit hash. Git determines deployment revision externally; source revision and deployment revision can legitimately differ.

The module derives the image string `repository@indexDigest` for both API and worker. No production `latest`, branch tags, auto-updater, or independently changed worker image. The two processes check RPC compatibility even when their configured digest is the same.

### 15.3 Host accounts and state

Create dedicated system accounts with locked passwords/no interactive login, separate homes, nonoverlapping subuid/subgid ranges, and lingering as required for rootless Podman:

- `memory-api`: owns API container storage and can connect as runtime DB roles.
- `memory-worker`: owns worker storage and IPC socket; no database identity mapping.
- `memory-migrator`: administrative one-shot execution identity, not a normal login.
- `deploy`: Tailscale SSH entrypoint authorized only for the deployment wrapper; nevertheless security-equivalent to trusted host administration.

Allocate stable UIDs in the host configuration after checking collisions; do not assume the same numerical UID exists on arbitrary hardware. Use `--userns=keep-id` and verify what host UID PostgreSQL observes. Configure runtime directories with tmpfiles and persisted homes/state with explicit ownership/modes.

A root-owned top-level deployment-state directory contains last-successful revision, previous system path, current release digest, rollback journal, and locks. Neither container can write it.

### 15.4 Container confinement

Both containers: read-only root filesystem; all capabilities dropped; `no-new-privileges` inside the container; no privileged mode; no host PID namespace; no host runtime socket; no whole-home or whole-`/run` mount; constrained processes, memory, CPU, and writable tmpfs. Keep the runtime's tested seccomp profile; do not globally disable it to make tests pass [R16].

The worker has `--network=none`, no public/published ports, no PostgreSQL socket, no secrets, and only its IPC directory plus bounded scratch tmpfs. Set memory+swap policy explicitly so the memory budget does not silently become a large swap allowance. Start at one shared CPU's quota and 256 MiB memory; benchmark before adjusting.

The API publishes only `127.0.0.1:8080:8080`, with a separate local administrative endpoint if needed. Mount only the PostgreSQL socket directory read-only, the IPC directory read-only where feasible, and individual secret files read-only. Mounting a socket directory read-only prevents creating/deleting its entries; it does not make the protocol or DB session read-only.

Rootless container controls still need delegated cgroup v2 support. Verify effective limits on the generated service/cgroup, not merely the presence of command flags. Avoid host-systemd hardening that prevents rootless Podman/newuidmap from establishing user namespaces; apply restrictions at the correct layer and prove them in VM tests.

### 15.5 Shared socket permissions

The IPC directory is ephemeral under `/run/structured-memory`, owned by `memory-worker`. Grant `memory-api` only directory traversal/read and socket connect rights using explicit POSIX ACLs. Default ACLs on the directory allow the API UID to connect to the worker-created socket; socket mode must preserve the ACL mask (e.g. `0660`), not be reset to `0600`.

The API must not be able to unlink/rebind the worker socket. Other local unprivileged accounts must not connect. Configure ACL creation through Nix-managed tmpfiles/setup, and inspect effective permissions after reboot and worker restart. Avoid relying on supplementary groups surviving user-namespace mapping unless that exact mechanism is tested.

The key tests are behavioral: API can communicate; an unrelated host account cannot; worker cannot reach PostgreSQL; API cannot replace the socket. Do not replace a failed ACL test with mode `0777`.

### 15.6 Native PostgreSQL

Enable `services.postgresql`, pin `pkgs.postgresql_17`, keep data on the host's persistent filesystem, and disable TCP for the reference deployment. Nix creates roles/database; migrations create application schema/tables/grants. API never owns the DB schema.

Use first-match HBA rules and explicit peer maps, preserving local PostgreSQL administration. Intended mapping:

```text
OS memory-api       → DB memory_app
OS memory-api       → DB memory_reader
OS memory-migrator  → DB memory_migrator
```

No map for `memory-worker`. PostgreSQL peer authentication obtains the OS identity from the kernel; container identity translation must be verified, not assumed [R17]. Ensure broader default HBA entries cannot inadvertently bypass the intended roles. Production VM tests must show the worker's host identity cannot connect as an application or migration role.

Initial API pool limits: four runtime connections and two reader connections, with at most one Code Mode reader in use. Keep normal autovacuum, monitoring, and conservative DB memory settings. Database startup ordering and retry behavior are both required; `After=` alone is not readiness.

### 15.7 Secrets and host services

Use sops-nix/age or existing equivalent Nix-managed runtime secret provisioning; this plan's default is sops-nix [R18]. Store only encrypted material in Git; never place plaintext secrets in Nix strings, derivations, flake arguments, image layers, or logs. Mount secrets only to the component that needs them. Keep the age decryption key off GitHub runners.

Declare ingress, Tailscale, backups, monitoring, storage permissions, firewall, secret ownership, and deployment services in Nix. Standard package/OS updates are deliberate flake-lock changes through the same tested process. PostgreSQL major upgrades are a separate operator procedure, never a side effect of a Nixpkgs update.

Set restart/backoff/start-limit behavior so a faulty worker does not spin indefinitely consuming the host. API ordinary reads remain functional during worker restarts. A full-release readiness check requires both API and worker; a liveness check must not create endless coupled restart storms when PostgreSQL is briefly unavailable.

---

## 16. Continuous delivery and multi-IaC GitOps

### 16.1 Pipeline invariant and repository authority

**Codebase/Bazel builds → trusted Codebase CI verifies → GHCR stores immutable image → Codebase CI proposes a PR against private `piperswe/infrastructure` → infrastructure CI validates → bot merges only permitted release pin → infrastructure Actions deploys over Tailscale → Nix activates exact GitOps revision on `infra-1.11r.io`.**

No Codebase build workflow edits an application release pin or host configuration within Codebase. Only the private infrastructure repository represents *desired* infrastructure state; host journal and GitHub Deployment records track actual running state. NixOS activation never consists of an ad hoc `podman run`, image-tag update, or Docker Compose operation. Use GitHub-hosted runners, never untrusted public-repo PRs on a home-hosted runner [R19].

A future Terraform root is a separate IaC deployment pipeline, not part of V1 and not a prerequisite for deploying memory. Nix-only deployment applies to the memory service and NixOS host; Terraform will eventually deploy Cloudflare-managed infrastructure resources under its own approval/state discipline.

### 16.2 Two-repository CI job graph

```text
Public piperswe/Codebase
  PR or trusted push:
    ├─ Bazel amd64 tests
    ├─ Bazel arm64 tests
    └─ aggregate required checks
  trusted main release after ALL required checks:
    └─ build/push memory OCI image to GHCR
       └─ capture exact multiarch index digest + source SHA + compatibility evidence
          └─ scoped GitHub App creates/updates promotion PR in private infrastructure

Private piperswe/infrastructure
  on pull_request against protected main:
    ├─ changed-path policy / release schema / source evidence verification
    ├─ nix flake check ./iac/nixos + synthetic VM checks
    └─ required NixOS checks (no deploy privileges)
       └─ bot auto-merges release-only PR when checks pass
  on protected main push after trusted checks:
    └─ nixos-deploy (environment home-production)
       └─ ephemeral Tailscale identity + restricted SSH to infra-1.11r.io
          └─ host-local memory-deploy <exact-infrastructure-commit-sha>
             └─ staged NixOS activation + health + compatible rollback
```

`piperswe/Codebase` CI needs GHCR publication privileges and a narrowly scoped promotion App credential, **not** Tailscale access, host SSH, NixOS activation privileges, or production secrets. Only the infrastructure repository's protected deploy workflow receives tailnet/SSH authorization.

Correct the existing Codebase CI release publication gate: its current amd64 matrix job may push images before arm64 finishes. The memory image must publish/promote only after both architecture jobs and other required checks pass; leave unrelated project behavior intact. PR/fork jobs receive no release App key, deployment keys, or write-capable cache secrets.

### 16.3 Cross-repository promotion (no self-commits)

Use a dedicated GitHub App installed against `piperswe/infrastructure` with only the permissions needed for contents/PR operations; grant its private key only to trusted Codebase main release jobs. `GITHUB_TOKEN` is repo-scoped and generally suppresses subsequent workflow events; App installation tokens can operate across repos and allow ordinary PR workflow triggers [R20], [R25]. The bot never directly modifies Codebase, deployment workflows, NixOS modules, or Terraform-related paths.

The PR shall modify **only** `iac/nixos/releases/memory.json`. Validate this changed-path allowlist in infrastructure CI and enforce branch protection/rulesets. The release metadata must include:

- Immutable GHCR OCI **index** digest matching the tested multiarch image.
- Exact trusted Codebase source SHA and retained build/test evidence or attestation.
- DB schema min/max compatibility and worker protocol major version.
- A clear source link, release timestamp, and PR/CI audit trail (not secrets or private facts).

Set required checks before bot auto-merge. Do not blindly auto-merge changes to flake inputs, host modules, GitHub workflows, policies, access controls, or future Terraform roots—those require owner review. Run ordinary read-only checks on PRs without Tailscale or production secrets. Avoid stale release races: if a later verified release supersedes a pending PR, update/close the older PR; validate intended source ordering and latest trusted release before merge. A pin-only GitOps change must not rebuild/stamp a new Codebase image; the two repos have independent triggers.

Run promotion for release-affecting changes in `projects/memory`, shared Go dependencies, root Go/Bazel lockfiles, image macros/base images, and relevant tooling. If Bazel dependency reachability is uncertain, conservatively rebuild; however, unrelated infrastructure-only commits cannot trigger Codebase builds because they occur in the other repository.

### 16.4 GitOps validation and IaC boundary

The private infrastructure repo has `.github/workflows/nixos-validate.yaml` and `.github/workflows/nixos-deploy.yaml` in V1. Scope NixOS checks to `iac/nixos/**`, relevant `.github/workflows/nixos-*`, and cross-IaC ownership-policy changes. Required checks must cover image digest allowlist, release metadata, flake format/evaluation, and NixOS VM integration where supported. Don't assume a Github Actions path filter alone is sufficient for branch protection checks: design stable required check names that report success/no-op on unrelated changes.

**Future extensibility contract, not implemented yet:** when `iac/terraform/` is added, give each Terraform root/provider a distinct workflow, remote state backend with locking, provider-scoped credentials, scoped change detection, and an independently protected apply environment. A Terraform plan PR must not be allowed to apply Cloudflare changes as a side effect of a NixOS image pin PR. No repository-wide `apply-all-IaC` script. Keep state, state locks, plan artifacts with secrets, and provider credentials out of Git. Define owner review for applies that can delete/replace existing Cloudflare resources.

Cloudflare migration later must inventory and import existing resources before Terraform becomes authoritative; do not create duplicates or let Terraform and another system both manage the same Cloudflare object [R26]–[R28]. Keep the Nix-managed host-local tunnel daemon distinct from Cloudflare account/zone API-side resources. This task does not actually migrate Cloudflare or initialize Terraform.

### 16.5 Deployment trigger, serialization, and stale-run behavior

Deploy only a trusted, fully checked **infrastructure `main`** revision. Use `concurrency: { group: nixos-infra-1-production, cancel-in-progress: false }` on the deploy job and a host-side `flock`. Do not let parent CI cancel migrations. An untrusted PR has no deployment credentials. On deployment execution, verify requested GitOps SHA is still eligible and not superseded by newer approved desired state; skip stale requests, never silently deploy untested HEAD.

NixOS config-only changes also use this pathway without any Codebase rebuild/promotion. A future `iac/terraform/**`-only change must not reconfigure the EliteDesk. Terraform's later apply workflow uses its own concurrency, locking and protected environment, independent of `nixos-infra-1-production`.

Report source Codebase SHA, infrastructure GitOps SHA, OCI digest, host system path, database schema version, worker protocol, and health results to the infrastructure repository's GitHub Deployment environment/status. Do not write runtime status back to the GitOps repository. Preserve the source/deploy SHA distinction in operator dashboards and audit history.

### 16.6 Tailscale and SSH to `infra-1.11r.io`

Use ephemeral GitHub-hosted Tailscale nodes and federated OIDC enrollment in the private infrastructure deploy workflow [R21], [R22]. Pin reviewed action commit SHAs. No long-lived Tailscale auth key or SSH private key in Codebase. Tailnet tags:

- `tag:nixos-deploy`: protected infrastructure deployment job only.
- `tag:infra-1`: EliteDesk host only.

Grant only TCP 22 and SSH as the `deploy` account from deploy runner tag to host tag. Validate the actual Tailscale SSH destination is the intended `infra-1.11r.io` machine; the FQDN being configured in NixOS **does not itself** create public DNS or tailnet MagicDNS. Use verified tailnet device identity/address or working private DNS; do not fall back to public router port forwarding or `StrictHostKeyChecking=no`.

Federation policy must restrict `piperswe/infrastructure`, the dedicated deployment workflow, and protected environment `home-production` (including claim/branch semantics). Tailnet tags alone are not sufficient authentication. Start with owner approval for bootstrap, then permit unattended trusted deployments when configured. No `pull_request_target` checkout of untrusted code.

### 16.7 Host-side GitOps activation contract

The `deploy` account is allowed only to invoke a fixed Nix-managed wrapper, conceptually:

```text
sudo memory-deploy <exact-infrastructure-40-character-commit-sha>
```

This wrapper fetches a **configured, read-only** `piperswe/infrastructure` remote into `/var/lib/gitops/infrastructure`, verifies commit eligibility against the protected infrastructure main branch, acquires the host lock, and builds the candidate from **`/var/lib/gitops/infrastructure/iac/nixos#infra-1`** at the checked-out exact SHA. It accepts no arbitrary repo URL, flake selector, host target, image, script, or Terraform commands. It must not ever fetch privileged GitOps instructions from `piperswe/Codebase`.

The host's read-only GitOps deploy key is restricted to that repository. Registry access to prebuilt OCI artifacts is separate and read-only. Root access to activating NixOS host configuration is inherently powerful; rulesets and workflow identity are the core trust boundary. Do not misleadingly describe a wrapper allowlist as a full security boundary.

Prefer a host-side Nix-managed deployment service with durable execution IDs/journal, bounded timeout and a watchdog that remains running if SSH disconnects. The runner polls status rather than blindly retrying. Nix builds only the system configuration/host packages; no Go/Bazel build and no OCI image construction on the EliteDesk.

## 17. Migrations, activation, and rollback

### 17.1 Migration discipline

Embed numbered, checksummed SQL migrations in the Bazel image. Use a maintained migration mechanism compatible with existing repository conventions. Migration runs take a PostgreSQL advisory lock, verify checksums and current version, and run transactionally where supported.

`serve` checks compatibility and refuses incompatible schemas; it does not run DDL. `migrate up` requires the migrator identity. Do not rerun migrations implicitly on every container restart or every unrelated NixOS rebuild.

For automatic CD, only tested backward-compatible/expand migrations are allowed. A migration that removes columns, changes fact semantics, retypes attributes, or invalidates the previous binary requires an explicit operator-approved maintenance procedure. Treat domain-schema changes and SQL storage migrations as different layers.

Every release declares DB compatibility and migration requirements; tests execute both the new binary and the previous deployable binary against the migrated schema when automatic rollback is promised. A range written in a manifest without testing is not sufficient.

### 17.2 Staged deployment

Implement these phases with a persisted journal and clear failure outcomes:

1. **Lock and validate:** Authenticate deployment request, validate commit/release manifest, check current-main eligibility, acquire host lock, and capture the last successful system path/image/schema version.
2. **Build only:** Build the candidate NixOS configuration from the exact revision without switching. Fail without changing services if evaluation/build fails.
3. **Preflight:** Check free disk, DB availability, host/cgroup support, secret readability, image architecture/protocol, and migration compatibility. Prefetch the digest for each rootless account through Nix-defined helpers, so registry failure is caught before stopping services.
4. **Backup:** If a migration is needed, create a fresh verified database backup and ensure the configured off-site step succeeded before proceeding. Do not confuse successful file creation with a tested restore procedure.
5. **Prepare:** Run the candidate image's migration command through an explicit Nix-defined administrative helper/unit using `memory-migrator`. It may run before switch only when compatible with the currently running application. Otherwise stop and require the manual maintenance path.
6. **Recheck:** Verify the candidate is still eligible, or abandon it safely. Record whether a compatible migration already ran even if the application switch is skipped.
7. **Arm recovery:** Start a host-local rollback watchdog using the captured known-good system path and a bounded confirmation deadline. It must not depend on the SSH connection surviving.
8. **Activate:** Switch to the candidate using NixOS's supported activation path. Do not patch containers in place. Start/readiness order: PostgreSQL, IPC setup, worker, API, ingress.
9. **Verify:** Check local API readiness, reader-role DB query, worker handshake and a synthetic read-only script, expected binary revision/digest/protocol, authorized MCP read, and an external HTTPS/auth check from the runner where configured.
10. **Confirm:** Persist the successful deployment state, disarm the recovery watchdog, update GitHub deployment status, and release the lock.

Candidate-specific preparation scripts may be emitted by Nix and used from the built system closure before activation. Their contents and image arguments are part of the reviewed Nix configuration. CI must not improvise privileged migration commands or inject arbitrary SQL.

### 17.3 Failure semantics

- Build/prefetch failure: old deployment remains running; no migration.
- Transactional migration failure: rollback migration transaction; do not activate candidate; report exact migration status.
- Worker/API readiness failure after switch: reactivate the recorded known-good NixOS configuration **only if its DB compatibility is still valid**; verify it and mark candidate failed.
- Lost SSH/runner: host journal/service and watchdog determine outcome; subsequent job queries the execution ID instead of guessing or duplicating migration.
- Incompatible/destructive migration or unverified DB state: no automatic database downgrade/restore; fail closed into operator recovery.
- Host power loss during deployment: on reboot inspect the durable journal and actual active generation/DB version, then converge safely; never infer success from a stale “started” record.

A NixOS generation rollback restores configuration, not PostgreSQL contents [R23]. Keep additive schema changes compatible with the previous release; restoration from backup is a distinct operation that can discard newer writes and needs explicit operator approval.

### 17.4 Artifact retention

Retain at least the last five successful NixOS generations and matching OCI digests, plus the current and previous release manifests. Configure Podman/registry cleanup not to delete these images. Nix retaining a string containing an image digest does not retain the image bytes.

Keep enough disk reserve for the candidate, prior images, migration backup, and PostgreSQL growth. A full disk is a deployment blocker. Garbage collection policy must account for native PostgreSQL data and rootless image stores separately.

---

## 18. Backups and recovery

### 18.1 Backup design

Create a Nix-managed systemd timer for nightly native PostgreSQL custom-format dumps. Back up role/global metadata as required for restoration, encrypted auth signing/cursor keys, deployment manifest, and host-independent application configuration. Do not expose owner credentials in diagnostic output.

Use an encrypted off-site repository (default implementation: Restic to an operator-supplied supported backend). Retain an initial policy of seven daily, four weekly, and six monthly recovery points; make it configurable. Local staging files use restricted permissions and are removed according to policy after a successful upload.

Initial recovery objectives: up to 24 hours of data loss between nightly backups, plus an extra backup before migrations. These are intended objectives, not demonstrated guarantees. Measure restoration duration on synthetic/representative data and record it; do not advertise a recovery-time objective until it is tested.

A logical dump has a consistent database view; it is not continuous point-in-time recovery. WAL archiving/PITR can be added later, but is not necessary to declare V1 complete [R24].

### 18.2 Recovery procedure and proof

Deliver a script/runbook that provisions an isolated NixOS test machine, installs the pinned native PostgreSQL major, restores globals/schema/data with appropriate privileges, deploys the recorded image digest, and verifies:

- Entity and attribute identities and names.
- Current fact counts, typed values, relationships, and cross-space isolation.
- Provenance and event ordering.
- Schema proposals and registered definitions.
- OAuth grants/token revocation behavior after restore; document whether restored refresh tokens must be invalidated.
- Runtime-role permissions and absence of worker DB access.
- Read-only Code Mode and core MCP authentication.

Do not restore untrusted role dumps as superuser without inspection. Recovery tests use synthetic secrets/data and run without production network access. A real production restore is manual and explicitly authorized.

### 18.3 Export/import

Provide a versioned NDJSON or JSON export containing spaces, IDs, names, registered schema metadata, sources, current facts/evidence, and audit history. Do not duplicate virtual metadata as separately writable facts. Include a manifest/checksum and format version.

Import is administrative and initially supports a new/empty destination space only, with validation before commit and preserved identities. Do not promise arbitrary merge/import conflict resolution. OAuth secrets/tokens are excluded from graph export; operational backups handle them separately.

---

## 19. Observability and operational limits

### 19.1 Metrics and logs

Expose Prometheus-compatible metrics using repository conventions, without requiring a new monitoring stack:

- HTTP/MCP latency and failures by bounded tool/error categories.
- DB pool utilization, query duration/count, cancellation, lock wait, transaction conflicts.
- Schema-resolution failures and proposal outcomes.
- Code execution latency, concurrency rejection, timeouts, payload/output sizes, worker restarts/OOMs.
- Snapshot lifetime, search/pull completion and limit failures.
- Successful deployment revision/digest and backup last-success time.

Do not use entity IDs, user queries, source URLs, raw scripts, or tokens as high-cardinality metric labels. Structured logs contain correlation IDs and safe operation summaries; fact values/scripts are off by default. Debug logging is local, time-limited, and explicitly configured.

Health output reports core DB/schema readiness and worker readiness separately. The deployment readiness probe includes both; a public liveness endpoint does not reveal internal host details.

### 19.2 Request limits

In addition to Code Mode limits, start with 1 MiB maximum ordinary request bytes, 100 mutation operations per transaction, 100 requested entity IDs per batch, 50 default/200 maximum page size, and a bounded graph projection AST. Clamp caller limits to server maxima.

Use request cancellation, HTTP timeouts appropriate to bounded MCP work, DB statement/lock/idle-in-transaction timeouts, and rate limits on auth/schema mutations. Limits have structured errors and metrics. Avoid ambiguous partial success.

### 19.3 Caches and data freshness

V1 has no persistent entity cache. Use execution-local caching and revision-keyed schema caches. All cache keys include space and relevant schema/data revision. On write commit, invalidate affected entries; a cache miss is acceptable, a cross-space or cross-snapshot hit is not.

Document which data is current-state, observed at a past time, inferred, archived, or sourced from a possibly stale document. The service stores evidence; it does not guarantee that an assertion remains true forever.

---

## 20. Testing and quality gates

### 20.1 Unit and contract tests

Cover strict value parsing/canonicalization, int64 boundaries, exact decimals, timestamp precision, Unicode and NUL handling, typed unions, alias resolution, schema-name collisions, type/domain/range rules, protected metadata, proposal revision conflicts, and stable error encoding.

Test absent versus `false`/zero/empty string; duplicate set membership and evidence; unknown attributes; deterministic pagination; fingerprint-collision handling through an injected collision function; archived entities and retained references.

Generate tests from SDK examples and keep MCP JSON schemas, Protobuf types, Go models, and `.d.ts` declarations aligned. Add compatibility tests for old/new worker protocol versions and invalid frames.

### 20.2 Real PostgreSQL integration tests

Use ephemeral PostgreSQL 17 for tests through existing repository testcontainer/native harness patterns. Production remains native host PostgreSQL. Tests include:

- Empty-database migrations/bootstrap and repeated bootstrap.
- Direct SQL rejection of broken FK/type/cardinality/name constraints.
- Simultaneous conflicting scalar writes; expected one success/one conflict.
- Concurrent schema registration and alias/canonical collision.
- Atomic rollback after a late invalid operation.
- Idempotent retry after simulated lost commit response; differing-payload key conflict.
- Consistent multi-query read snapshot during a concurrent correction/schema edit.
- No reads/writes/events/suggestions across spaces, including forged IDs.
- Query plans/index use for representative entity, reverse-ref, numeric, and text searches.
- Schema projection visibility in get/find/pull/incoming/history.
- Runtime and reader-role privilege restrictions.
- Cancellation releases connection/locks and leaves no idle transaction leak.

The monorepo `bazel test --config=docker ...` path must remain usable on both architectures. Treat container-requiring tests as such; do not silently skip them and declare integration complete.

### 20.3 Runtime abuse and reliability tests

Test infinite loops, very deep recursion, large allocations, huge strings/arrays, repeated host calls, excessive traversal, cyclic graph data, oversized output/logs, getters/proxies during encoding, BigInt encoding, unknown host objects, parse errors, native host calls stalled until context cancellation, worker exit, RPC disconnect, and forbidden methods/imports.

A forged GraphRequest cannot select another space or mutate anything. Worker OOM/restart must not terminate the API or PostgreSQL. After each failure, a direct read and a new script succeed within a bounded recovery window. No leaked Go goroutines or DB connections after repeated cancellations.

Run destructive resource-limit tests in disposable containers/VMs, not directly in the developer's host process or production environment.

### 20.4 NixOS VM tests

Build the application image with Bazel first. Supply its exported archive or a fixed-digest published artifact to a NixOS test; Nix only loads/deploys that artifact. Do not accidentally use a Nix Go build or a mock container as proof of application integration.

Required VM test assertions:

1. Native PostgreSQL and both rootless containers start after boot without interactive login.
2. Actual host/container UID mappings allow API peer auth; worker identity cannot authenticate to DB.
3. API/worker Unix socket ACLs survive reboot/restart and deny unrelated users.
4. Worker has no usable network, DB socket, auth secrets, deployment files, host runtime socket, or excess writable paths.
5. Memory/CPU/PID limits are actually active; OOM affects the worker, not API/DB.
6. Production MCP auth cannot be bypassed; metrics/admin routes are not public.
7. A candidate rollout succeeds; a deliberately unhealthy candidate triggers compatible rollback.
8. Native data persists across container replacement and NixOS rebuild.
9. Failed migration prevents activation; schema checks and rollback incompatibility behave correctly.
10. Backup/restore passes the semantic verification suite.

If GitHub-hosted nested virtualization is unavailable, run an independent disposable remote VM test runner or document the explicit infrastructure blocker; never use the production EliteDesk as an untrusted PR runner. Nix evaluation checks alone are not substitutes for VM runtime tests.

### 20.5 Authentication and deployment tests

Test PKCE, code replay, audience/issuer errors, exact redirects, refresh replay/revocation, consent scope/space binding, expired tokens, malicious client metadata URLs, login rate limiting, and sanitized error/log behavior.

CI/CD tests must cover: an arm64 failure blocking promotion; GHCR digest to external GitOps PR; a release-only PR changing only `iac/nixos/releases/memory.json`; App token vs repo `GITHUB_TOKEN` workflow semantics; Nix-only and future Terraform-only path isolation; no in-repo Codebase pin commits; source vs deployment SHA distinction; malformed digest; untrusted PR/fork denied all deployment credentials; stale deployment skip; concurrent host-lock exclusion; SSH loss after activation; missing image; rollback compatibility failure; and no secrets in logs/artifacts/Nix store.

Perform a final real-client OAuth/MCP smoke test using the owner's configured endpoint after authorized deployment. Automated SDK client tests are required even before that manual interoperability check.

### 20.6 Evidence required at handoff

Record commands, exit statuses, test reports, benchmark hardware/data sizes, image digests, evaluated Nix revision, and any unexecuted environment-dependent tests in `implementation-status.md`. Distinguish implemented, statically checked, integration-tested, and production-verified. Do not equate “the file exists” with an operational deployment.

---

## 21. Implementation milestones

Implement in reviewable increments. Each milestone ends with a buildable/tested checkpoint and an update to `implementation-status.md`. Do not produce a large scaffold with TODOs in security, transaction correctness, or deployment recovery and label V1 complete.

### M0 — Reconcile repository and freeze contracts

**Dependencies:** None.

Read the repository baseline and local instructions. Write the ADRs in Section 24, settle actual package/target names, and add this plan under `projects/memory/docs`. Confirm reusable dependencies and security-review the selected Goja revision. Define the value union, errors, request/response types, schema projection rules, SDK signatures, and worker proto before parallel implementation.

**Deliverables:** Package skeleton with a buildable `memoryd version`; schema/SDK/protocol design files; source/dependency inventory; initial test plan; no production changes.

**Exit gate:** New Bazel target builds with the monorepo's actual toolchain, existing targets still pass their baseline checks, and all placeholder operational inputs are identified rather than fabricated.

### M1 — Typed storage, migrations, and bootstrap

**Dependencies:** M0.

Implement SQL migrations, roles/grants, typed fact constraints, composite-space FKs, unified name registry, per-space revision row, and protected schema projections. Implement deterministic idempotent bootstrap and a minimal sample schema only for tests. Add pgx pool/session handling and native/Unix/TCP test configuration.

**Deliverables:** `migrate`, storage package, bootstrap manifest, real-PostgreSQL invariant tests.

**Exit gate:** Empty database initializes twice safely; direct invalid SQL is rejected; reader/app/migrator permissions are verified; schema entities behave as graph entities.

### M2 — Transactions, schema changes, sources, and history

**Dependencies:** M1.

Implement the locked transaction algorithm, expected revisions, temp IDs, idempotency, source/evidence handling, immutable events, archival, and schema proposal/apply flows. Add search-projection updates inside transactions.

**Deliverables:** Transaction service, schema service, source APIs, event history, concurrent-write tests.

**Exit gate:** Atomicity/concurrency/idempotency tests pass; unknown names never create schema; aliases/canonical names cannot collide; corrections preserve evidence; cross-space writes fail.

### M3 — Query/read layer and pagination

**Dependencies:** M1–M2.

Implement exact lookup, batch get, schema discovery, typed find AST, text search, bounded pull, incoming references, evidence projections, revision-aware cursors, and snapshot-safe caches. Benchmark realistic query shapes and add missing indexes based on plans.

**Deliverables:** Shared read-service API, query compiler, pagination codec, fixture benchmark harness.

**Exit gate:** Direct/batched reads agree; pull handles cycles and limits; virtual metadata is queryable; cursor staleness is explicit; snapshot tests survive concurrent updates.

### M4 — MCP and owner authentication

**Dependencies:** M2–M3; auth work can begin earlier after M0.

Implement the tool suite, strict schemas, structured errors/results, read/write/scope annotations, HTTP lifecycle, auth metadata and owner-consent flow, scoped token validation, and private health/metrics endpoints. Integrate tested repository auth components without coupling the new service to MeMode's data or availability.

**Deliverables:** Authenticated conventional MCP service, OAuth persistence/revocation commands, SDK-client integration tests, API documentation.

**Exit gate:** A test MCP client can authorize, create/read/correct synthetic facts, and receive accurate denials for absent scopes/other spaces. No remote unauthenticated mode is activatable.

### M5 — Goja worker and Code Mode

**Dependencies:** M0 contracts, M3 read layer, M4 authorization.

Implement the typed bidirectional stream, fresh Goja runtime, host bindings, lossless value codec, execution watchdogs, budget accounting, cancellation, and separate worker readiness. Add all SDK examples and hostile-input tests. Validate read-only behavior against forged frames, not only absent JavaScript methods.

**Deliverables:** `worker`, `memory_code`, `.d.ts` and examples, RPC compatibility suite, runtime recovery tests.

**Exit gate:** Graph tasks work through scripts; script/worker failures are bounded; output is correctly typed; API releases snapshots; no worker operation can mutate or change authority.

### M6 — Release-quality Bazel image

**Dependencies:** M4–M5.

Complete embedded resources/version metadata, multi-architecture OCI target, image-load test artifacts, healthcheck subcommands, vulnerability scanning, and GHCR release-evidence output. Register the image with existing publication abstractions.

**Deliverables:** Tested amd64/arm64 image/index, build commands, registry digest capture, image contract docs.

**Exit gate:** Both native architectures execute smoke/integration tests. Repeated builds with identical inputs are checked. The image runs read-only/nonroot without a shell or runtime compiler.

### M7 — Private multi-IaC GitOps skeleton and NixOS module

**Dependencies:** M6.

Set up a separate private `piperswe/infrastructure` repository only with operator authorization. Add `docs/iac-ownership.md`, `docs/terraform-roadmap.md`, and the **active** `iac/nixos/` flake; document future `iac/terraform/` without creating Terraform configuration/state/backend/workflows. Implement reusable module, native PostgreSQL roles/services, separate rootless users, socket ACLs, secrets, ingress adapter, container controls, backups, and operational helpers. Build the real Bazel image first and deploy it into NixOS VM tests. Provide production host configuration hooks without inventing disks, mount UUIDs, or secrets.

**Deliverables:** Separate GitOps repo layout, cross-IaC ownership contract, NixOS flake in `iac/nixos/`, service/deployment modules, `infra-1` sample host inputs, VM checks, operations runbook.

**Exit gate:** The actual packaged image boots/communicates on NixOS; rootless UID/peer/ACL/cgroup properties are behaviorally proven; data survives service replacement; worker isolation tests pass.

### M8 — Cross-repository promotion and NixOS CD

**Dependencies:** M6–M7.

Refactor the Codebase release gate to wait for all architecture/required tests. Implement trusted-main publication to GHCR and a tightly scoped GitHub App promotion PR in `piperswe/infrastructure`, protected PR validation/auto-merge, infrastructure-only NixOS deploy workflow, IaC path isolation, release manifest validation, environment/OIDC configuration documentation, exact-SHA Tailscale deployment to `infra-1.11r.io`, serialized host orchestration, and deployment status reporting.

**Deliverables:** Codebase publication/promotion workflow; infrastructure NixOS validate/deploy workflows; release manifest schema; Nix-managed deployment wrapper/service; rollback journal/watchdog; cross-repository integration and race/failure tests.

**Exit gate:** A disposable host receives an automatically promoted release through the private GitOps repo and Nix; Codebase is never self-modified for promotion; release-only PRs are constrained to the image pin; NixOS and future Terraform workflow scopes are independent; stale jobs cannot overwrite newer desired state; untrusted PR jobs cannot obtain home-network access.

### M9 — Recovery, security, performance, and documentation

**Dependencies:** M1–M8.

Run backup/restore, previous-version migration/rollback, cancellation/OOM soak, auth attack-case, export/import, and representative performance tests. Review logging/privacy, Nix store content, mounted paths, runtime roles, and action permissions. Update documentation from observed behavior, not assumptions.

**Deliverables:** Test/evidence report, final README/API/operations/security docs, benchmark results, completed definition-of-done checklist, explicit remaining operator tasks.

**Exit gate:** All software acceptance checks pass, no known critical security/integrity TODO remains, and any hardware/credential-dependent unverified step is separately listed.

### M10 — Authorized home-host bootstrap and first production verification

**Dependencies:** M9 plus operator inputs/access.

Apply the host configuration under authorized operator control, provision secrets/tailnet/registry/public ingress, run the first migration/bootstrap, perform a real-client OAuth/MCP smoke test, and verify the next harmless release through automatic CD. Test recovery access before relying on unattended updates.

**Deliverables:** Actual deployment revision/digest record, backup success/restore-test reference, operating instructions, and production verification results.

**Exit gate:** End-to-end service and CD function on the EliteDesk. If the coding agent lacks host access, report M0–M9 completion and M10's exact missing prerequisites; do not claim production deployment occurred.

### Parallelization boundaries

After M0, storage/transactions, SDK/worker, and Nix/CD can be separate workstreams against agreed contracts. Auth should have its own security review. Assign ownership of shared Codebase root files (`go.mod`, `MODULE.bazel`, root `BUILD.bazel`, CI), and **separately** of the infrastructure `iac/nixos/flake.nix` and its workflows, to integration workstreams to avoid conflicting lockfile/workflow edits. No workstream may weaken invariants to unblock another.

---

## 22. Operator bootstrap and handoff

### 22.1 Inputs that must come from the operator/environment

These are deployment inputs, not unresolved application architecture:

| Input | Why required |
|---|---|
| EliteDesk hardware inventory, disk layout, filesystem UUIDs | Safe NixOS hardware configuration; never invent or repartition automatically |
| **`infra-1.11r.io`** host identity, recovery-console/admin access, SSH/tailnet resolution | Configure OS hostname `infra-1`, domain `11r.io`; prove Tailscale destination identity and recovery access |
| Private infrastructure repository creation/ownership | `piperswe/infrastructure` must exist under operator-authorized account access and protected branch/rules |
| GitOps read-only deploy key on EliteDesk | Fetch protected exact infrastructure SHA without deploy write permission |
| Stable available UIDs/subuid/subgid ranges | Rootless identity and socket/peer-auth configuration |
| Tailscale host enrollment and tailnet policy authority | Private CD access and restricted tags/SSH |
| Federated identity client ID/audience and claim policy | GitHub OIDC trust for deployment jobs |
| Protected `main`, CODEOWNERS, environment and required checks in **both repositories** | Distinguish trusted public Codebase releases from privileged infrastructure activation |
| Scoped GitHub App ID/installation/private key for promotion into `piperswe/infrastructure` | Cross-repo PR creation/auto-merge with normal checks; no host access |
| GHCR package visibility or read-only host credentials | Artifact availability for each rootless account |
| Public MCP hostname, DNS, and chosen HTTPS ingress path | Cloud client access, distinct from CD connectivity |
| Owner password hash, signing/cursor keys, age recipient/key | Runtime authentication and secret decryption |
| Off-site backup destination/credentials and retention | Recovery outside the same EliteDesk disk |
| Initial approved image digest | First real production release pin |

Prefer public GHCR images if acceptable, since code/artifacts contain no secrets; repository visibility alone does not prove package visibility. Private images require read-only credentials stored only on the host and mounted/available to the appropriate image-prefetch mechanism, not to the worker process.

Federated client IDs/audiences are identifiers, not secrets; the promotion App private key and runtime keys are secrets. Keep GitHub and host secret sets separate. Do not upload host age keys or database backups to Actions.

### 22.2 Bootstrap order

1. Create/protect the private `piperswe/infrastructure` GitOps repository, with `iac/nixos/` flake and multi-IaC ownership docs; **do not** add Terraform code yet. Produce tested Codebase application artifacts and a valid initial GitOps image manifest.
2. Collect hardware-specific configuration without destructive disk actions.
3. Provision NixOS, native PostgreSQL, dedicated identities, secret provisioning, recovery access, and Tailscale enrollment.
4. Verify rootless containers/cgroups/ACLs and database peer identity locally before public ingress.
5. Run explicit migration/bootstrap using the administrative role. Verify no real personal data was seeded.
6. Configure owner authentication and public HTTPS ingress; verify unauthorized requests are rejected.
7. Perform authenticated synthetic MCP and Code Mode tests; keep smoke data in a designated nonpersonal test space.
8. Enable off-site backup, restore-test procedure, and alerts/status monitoring.
9. Configure protected branches in both repos, cross-repo promotion GitHub App, private infrastructure `home-production` environment, federated tailnet trust, NixOS validation/deployment workflows, and host read-only repo deploy key.
10. Run one authorized deployment and rollback exercise, then enable unattended ordinary releases.

A blank production image pin or missing credentials should disable deployment with a clear bootstrap status, not fall back to `latest`, anonymous auth, a mock database, or application compilation on the host.

### 22.3 Required handoff files

`README.md` must explain purpose, architecture, build/test commands, local development, authentication, and MCP connection setup. `api.md` defines exact tools/query/pull/value shapes. `sdk/code-mode.d.ts` and examples are tested. `operations.md` covers deployment, inspection, backups, restore, rotation, schema approval, rollback, and upgrades. `security.md` records threat boundaries, selected dependency review, limits, and known residual risks.

`implementation-status.md` must list completed milestones, actual commands/tests run, produced digests, Codebase source SHA and infrastructure deployment SHA, deviations and their reasoning, and external steps not performed. The infrastructure repo must contain `docs/iac-ownership.md` and `docs/terraform-roadmap.md`, with Terraform explicitly deferred rather than simulated. Include an explicit notice that this service is external structured memory, not an automatic replacement for an assistant's built-in memory subsystem.

### 22.4 Expected developer commands

Document verified commands using the real labels/tool versions. The following are intended contracts, not instructions to run nonexistent targets before implementation:

```bash
# Focused Go tests and generated-code/documentation checks.
bazel test //projects/memory/...

# Repository-native integration and image checks.
bazel test --config=docker //projects/memory/...
bazel build //projects/memory/cmd/memoryd:memory_image

# Monorepo regression checks, respecting existing architecture filters.
bazel test --config=docker //...

# Nix validates/deploys prebuilt artifacts; it does not build the application.
# From the private infrastructure repository checkout:
nix flake check ./iac/nixos
nix build './iac/nixos#nixosConfigurations.infra-1.config.system.build.toplevel'

# Production activation is normally invoked by the protected CD workflow.
# Operator recovery uses the documented Nix-managed deployment/rollback commands.
```

Ensure default test targets do not require production credentials. Real external smoke tests must be explicitly tagged/gated and never silently skipped in a report claiming they passed.

---

## 23. Definition of done

### Product and data integrity

- [ ] First-class attributes/types/sources work through the same graph read surface.
- [ ] Exact aliases resolve to stable IDs; undeclared names never create attributes.
- [ ] Unified name constraints prevent canonical/alias collisions and unsafe reuse.
- [ ] Values are typed/lossless; missing, false, zero, and empty string are distinct.
- [ ] Atomic writes enforce cardinality, references, type/domain/range, and space boundaries.
- [ ] Stale writes conflict; idempotent retries cannot duplicate effects.
- [ ] Facts carry source evidence and server-derived actor/time metadata.
- [ ] Corrections/history/archival are explicit and auditable.
- [ ] Search/find/pull/incoming/history expose limits and completeness accurately.
- [ ] Snapshot/schema cache behavior is tested under concurrent change.

### MCP and Code Mode

- [ ] All documented tools and JSON schemas are implemented and tested.
- [ ] Owner OAuth/client consent, resource-bound tokens, scopes, and revocation work.
- [ ] No unauthenticated remote mode is available in production.
- [ ] JavaScript entity APIs, examples, and lossless encoding match their documentation.
- [ ] No Code Mode or worker-RPC write path exists in V1.
- [ ] Time/memory/query/output limits and cancellation are tested, including worker OOM.
- [ ] Ordinary reads survive worker failures and subsequent executions recover.
- [ ] Dependency review records a patched Goja version and residual isolation limitations.

### Build and deployment

- [ ] Application/codegen/tests/images are built by Bazel in the existing monorepo.
- [ ] Both architectures pass required tests before publication/promotion.
- [ ] Nix deploys the same immutable image digest to API and worker.
- [ ] Native PostgreSQL and separate rootless identities/ACLs work in NixOS VM tests.
- [ ] Worker cannot access network, DB socket, auth secrets, or deployment state.
- [ ] Codebase CI publishes to GHCR and proposes **only** a digest-pin PR in private `piperswe/infrastructure`; it never commits deployment state into Codebase.
- [ ] Infrastructure repo groups IaC roots under `iac/`; NixOS is implemented, Terraform is only documented/reserved.
- [ ] Future Terraform Cloudflare ownership is unambiguous, with no duplicate reconciliation of Cloudflare resources.
- [ ] CI publication and cross-repo promotion cannot loop or publish on infrastructure pin-only commits.
- [ ] GitHub-hosted infrastructure-repo CD uses scoped private networking to validated host `infra-1.11r.io` and an exact trusted infrastructure revision.
- [ ] Infrastructure workflow path/credential boundaries prevent unrelated future Terraform activity from triggering NixOS deployment and vice versa.
- [ ] Host locking, stale-run rejection, readiness checks, and compatible rollback are tested.
- [ ] No manual container mutation or application Nix build bypasses the chosen architecture.

### Operations and evidence

- [ ] Backup/restore and export/import verification passes.
- [ ] Migration compatibility is tested; no automatic destructive downgrade/restore.
- [ ] Previous Nix generations and corresponding OCI images are retained.
- [ ] Secrets/private data are absent from Git, image layers, logs, public artifacts, and plaintext Nix store entries.
- [ ] Metrics, health semantics, runbooks, and recovery access are documented.
- [ ] Implementation report distinguishes software-tested from production-verified work.
- [ ] Remaining operator inputs/permissions are listed explicitly, without blocking unrelated implementation.

---

## 24. Decision records

Create one ADR file per decision. The first five preserve the numbering established in the design conversation, and ADR-011 records the new IaC boundary; earlier draft lists that reused these numbers are superseded.

| ADR | Title | Decision |
|---|---|---|
| 001 | Embedded Code Mode runtime | Goja/JavaScript, familiar syntax, restricted host API, pinned patched dependency |
| 002 | Worker isolation and IPC | Separate rootless containers on one NixOS host; bidirectional gRPC over Unix socket; API owns DB/authority |
| 003 | Code Mode mutation policy | V1 read-only; V2 may propose validated transactions; conventional V1 MCP writes remain available |
| 004 | Build and deployment separation | Existing Codebase; Bazel-only application artifacts; Nix-only deployment; immutable GHCR digest |
| 005 | Cross-repository GitOps continuous delivery | Codebase builds/publishes; GitHub App proposes an image-pin PR in private infrastructure; infra Actions deploy exact NixOS revision to `infra-1.11r.io` over Tailscale |
| 006 | First-class schema with relational enforcement | Attribute/type entities; protected metadata projections; no dual editable authority |
| 007 | V1 write ordering and history | Per-space write lock/revisions, optimistic preconditions, idempotency, atomic events/current state |
| 008 | Query and scripting contracts | Bounded AST/pull, snapshot-local entity handles, explicit pagination and lossless numeric codec |
| 009 | Local identity and secrets | Separate rootless UIDs, socket ACLs, native PostgreSQL peer roles, runtime-only secrets |
| 010 | Release safety and recovery | Backward-compatible automatic migrations, staged activation, tested rollback, off-site backups |
| 011 | IaC repository organization and ownership | One private GitOps repo with `iac/nixos/` active and future sibling `iac/terraform/`; independent workflows and ownership; Terraform deferred for V1 |

Keep the distinction between fixed user choices and implementation refinements visible in each ADR. Do not reopen Goja/Bazel/Nix/PostgreSQL decisions merely because a different tool is easier for the coding agent.

V2 extension points are reserved interfaces, not preimplemented functionality: worker write proposals, richer temporal queries, semantic discovery, additional deployment topologies, and larger-scale concurrency. Keep V1 small enough to complete, while making its existing behavior correct and operable.

---

## 25. References and verification notes

These links support upstream behavior and repository observations; [R25]–[R29] support cross-repository GitOps, subdirectory Nix flakes, and future Cloudflare Terraform onboarding. Most of this document is a proposed implementation contract, not a summary of upstream products. References were checked during preparation on 2026-10-31. Recheck versions/security advisories and the actual locked dependencies when implementing; do not use floating upstream `main` as a production pin.

Repository references use the inspected commit where practical. The operator's checkout may contain newer changes; reconcile rather than overwrite them. No repository files or host settings were changed while preparing this document, and none of the proposed application code, SQL migrations, Nix module, or workflows has been executed as part of drafting it.

| Ref | Source | Relevance |
|---|---|---|
| [R1] | Codebase inspected tree | Monorepo structure and source baseline |
| [R2] | Codebase OCI macro | Existing multi-architecture image/stamping conventions |
| [R3] | Codebase root BUILD | Existing image inventory and publication targets |
| [R4] | Codebase CI workflow | Existing build matrix and publication placement |
| [R5] | MeMode README | Existing MCP/auth service to inspect for reusable patterns |
| [R6] | Datomic schema reference | Inspiration for attributes as schema entities |
| [R7] | PostgreSQL 17 explicit locking | Per-space row-lock write serialization |
| [R8] | PostgreSQL 17 transaction isolation | Repeatable-read snapshots and isolation limits |
| [R9] | Official MCP Go SDK | SDK selection and supported protocol behavior |
| [R10] | MCP authorization specification | Resource-server discovery, registration, and OAuth requirements |
| [R11] | Goja package documentation | Runtime embedding, BigInt, threading, and interruption behavior |
| [R12] | Searchlight Cyber primary research | Documented Goja sandbox escape; dependency/security review requirement |
| [R13] | rules_oci documentation | Image build/index/load/push primitives |
| [R14] | NixOS 26.05 announcement | Initial host release and support-window awareness |
| [R15] | NixOS 26.05 OCI-container module | Rootless Podman/systemd options and semantics |
| [R16] | Podman run manual | User namespaces, mounts, and resource/security controls |
| [R17] | PostgreSQL peer authentication | OS-identity mapping for Unix-socket connections |
| [R18] | sops-nix | Runtime secret provisioning without plaintext store secrets |
| [R19] | GitHub Actions secure-use guidance | Trust boundaries and public-repository runner risks |
| [R20] | GitHub token/event behavior | Automated promotion and downstream workflow triggering |
| [R21] | Tailscale GitHub Action | Ephemeral runner connection and authentication inputs |
| [R22] | Tailscale workload federation | OIDC claim binding and credential-less workload enrollment |
| [R23] | NixOS manual | Configuration activation, generations, and operations |
| [R24] | PostgreSQL backup and restore | Logical backups versus continuous recovery |
| [R25] | GitHub App Actions token docs | Cross-repository installation access and workflow triggers |
| [R26] | Cloudflare Terraform best practices | Resource ownership, account/zone/product grouping, and state isolation |
| [R27] | Cloudflare import guide | Future discovery/import of existing Cloudflare configuration |
| [R28] | Terraform resource import blocks | Existing resources should be imported, not recreated |
| [R29] | Nix flake directory syntax | `?dir=` / local subdirectory flake references |

[R1]: https://github.com/piperswe/Codebase/tree/3baa49e1ed6f8a198e5bae5061dc779fd6af94cd
[R2]: https://github.com/piperswe/Codebase/blob/3baa49e1ed6f8a198e5bae5061dc779fd6af94cd/platforms/oci.bzl
[R3]: https://github.com/piperswe/Codebase/blob/3baa49e1ed6f8a198e5bae5061dc779fd6af94cd/BUILD.bazel
[R4]: https://github.com/piperswe/Codebase/blob/3baa49e1ed6f8a198e5bae5061dc779fd6af94cd/.github/workflows/ci.yaml
[R5]: https://github.com/piperswe/Codebase/blob/3baa49e1ed6f8a198e5bae5061dc779fd6af94cd/projects/memode/README.md
[R6]: https://docs.datomic.com/schema/schema-reference.html
[R7]: https://www.postgresql.org/docs/17/explicit-locking.html
[R8]: https://www.postgresql.org/docs/17/transaction-iso.html
[R9]: https://github.com/modelcontextprotocol/go-sdk
[R10]: https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization
[R11]: https://pkg.go.dev/github.com/dop251/goja
[R12]: https://www.slcyber.io/research/out-of-bounds-out-of-sandbox-rce-in-go-javascript-engine
[R13]: https://alexeagle.github.io/doc.bzl/rules_oci/oci/defs
[R14]: https://nixos.org/blog/announcements/2026/nixos-2605/
[R15]: https://raw.githubusercontent.com/NixOS/nixpkgs/nixos-26.05/nixos/modules/virtualisation/oci-containers.nix
[R16]: https://docs.podman.io/en/latest/markdown/podman-run.1.html
[R17]: https://www.postgresql.org/docs/17/auth-peer.html
[R18]: https://github.com/Mic92/sops-nix
[R19]: https://docs.github.com/en/actions/reference/security/secure-use
[R20]: https://docs.github.com/en/actions/concepts/security/github_token
[R21]: https://tailscale.com/docs/integrations/github/github-action
[R22]: https://tailscale.com/docs/features/workload-identity-federation
[R23]: https://nixos.org/manual/nixos/stable/
[R24]: https://www.postgresql.org/docs/17/backup.html

[R25]: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/making-authenticated-api-requests-with-a-github-app-in-a-github-actions-workflow
[R26]: https://developers.cloudflare.com/terraform/advanced-topics/best-practices/
[R27]: https://developers.cloudflare.com/terraform/advanced-topics/import-cloudflare-resources/
[R28]: https://developer.hashicorp.com/terraform/language/block/import
[R29]: https://github.com/NixOS/nix/blob/master/src/nix/flake.md
