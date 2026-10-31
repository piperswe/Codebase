# Memory V1 API

## Identity and authority

Persistent identities are UUIDs, scoped by a knowledge space. Exact UUID,
canonical identifier and registered alias resolve authoritatively; lexical
search returns candidates. Name lookup never silently becomes a fuzzy search.
Identifiers have lowercase ASCII namespaces and kebab-case components, for example
`device/example-receiver`. A name is reserved to its entity for that entity's
lifetime, including after rename/archive. Display labels preserve Unicode.

Every tool supplies a `space` slug. The authenticated principal must have that
space and the exact required scope. `memory:write`, `memory:schema:write` and
`memory:execute` are independent. Worker messages contain none of these authority
fields. Absent/invalid credentials produce HTTP 401, with resource discovery;
graph failures use MCP `isError` plus a structured error envelope.

`contracts.json` is the checked-in strict JSON Schema contract. Regenerate it with
`python3 projects/memory/tools/generate_contracts.py`. The server validates the
input schema and then invariant-sensitive service methods validate it again.
Unknown semantic fields are errors. Responses contain `ok`, `data` or `error`,
and `meta` with server request ID, decimal-string data/schema revisions,
`complete` and nullable `nextCursor`. Nested pull edges have their own completeness
and continuation metadata.

## Tools

| Tool | Scope | Behavior |
|---|---|---|
| `memory_search` | `memory:read` | Names, aliases, labels and fixed-policy lexical text |
| `memory_get` | `memory:read` | Batch exact entity reads with typed facts/evidence |
| `memory_find` | `memory:read` | Bounded typed predicates and reference paths |
| `memory_pull` | `memory:read` | Breadth-level graph expansion with cycle stubs |
| `memory_history` | `memory:read` | Retained assertion/evidence/entity/schema events |
| `memory_schema` | `memory:read` | `search`, `describe`, `list`, `types`, `help` |
| `memory_transact` | `memory:write` | Atomic ordinary graph mutations |
| `memory_schema_propose` | `memory:write` | Store a reviewed-later proposal and candidates |
| `memory_schema_apply` | `memory:schema:write` | Register/alias/update/deprecate/reject one proposal |
| `memory_code` | `memory:read` + `memory:execute` | Isolated synchronous JavaScript reads |

`memory://sdk/code-mode` also exposes declarations as an authenticated MCP resource.
Schema help is available as a tool, so clients need not automatically load resources.

## Values and schema

Facts use `{type,value}`. Integer and decimal payloads are **strings**. Integers
are signed int64; decimals have at most 38 significant digits and 12 fractional
places after canonicalization, with no float intermediate. Timestamps need an
explicit offset, normalize to UTC and reject sub-microsecond values. Booleans
remain JSON booleans. `false`, zero and empty text are present facts. Null is not
a fact value. Strings reject NUL and values over 16 KiB. References resolve within
the authenticated space. A supplied measurement `unit` must exactly match schema;
there are no automatic conversions.

Attributes/types/sources are entities. `attribute_defs`, `entity_names`, type and
source projections are the single authorities for protected metadata. The common
`memory.read_facts` view exposes these as virtual facts to get/find/pull/incoming.
Virtual IDs use `virtual:<entity UUID>:<attribute UUID>:<member key>` and remain
stable across reads; exports store authoritative rows, never duplicate virtual
assertions. Ordinary annotations on schema entities remain physical facts.

`entity/label` is a normal scalar string and `entity/type` is a normal many-valued
reference to registered types. Other bootstrap metadata is protected. Core names
cannot be registered or renamed by ordinary transactions. Attribute type,
cardinality, domain/range and unit are immutable after registration. Use a new
attribute for a migration. Description/examples, aliases, names and deprecation
use the explicit proposal/apply path and preserve audit events. Type descriptions
and names can be updated; type inheritance and type deprecation are unsupported.

## Transactions and history

A transaction requires `idempotencyKey`, `expectedSchemaRevision`, a reason and
1–100 operations. The space row is locked before mutable graph/schema reads.
Temporary IDs are allocated up front. Entity refs can be `{tempId:"new"}`;
reference/evidence strings can use `temp:new` within the same transaction.

Operations are `createEntity`, `createSource`, `assert`, `replace`, `retract`,
`addEvidence`, `addAlias`, `renameEntity`, `archiveEntity`. Attribute/type creation
is exclusive to the schema API. Every assertion requires source evidence. Explicit
types on entity creation receive a clearly labeled system source; no user testimony
is fabricated. Sources are never fetched. Optional confidence stays null when
unknown, and values in [0,1] are not asserted to be calibrated probabilities.

A different existing scalar cannot be overwritten by `assert`. `replace` is
scalar-only. Replace/retract need an expected entity revision, a matching
transaction-wide precondition, or an exact expected fact ID. For many-valued
retraction, specify the fact ID or value to select the member. Exact duplicate
members/evidence are no-ops; changed evidence metadata is audited. A final-state
check prevents type removal from invalidating existing domain/range facts.

Every accepted transaction has a per-space ordering revision. Corrections append
full retraction/assertion payloads with evidence; deleting a current fact never
deletes its history. Idempotency is keyed by principal, space and key. A canonical
request hash mismatch fails; matching retries replay the stored result after
fresh authorization. Database lock/statement failures are bounded, retryable
errors; callers preserve their key/preconditions when explicitly retrying.

## Queries and cursors

Find accepts all/any, exists, eq/in and numeric/timestamp comparisons. It has a
64-term/8-level expression bound and explicit in/out reference paths of at most
four hops. SQL identifiers/operators come from closed server lists and values
are parameters. No arbitrary SQL, negation, recursion or cross-products exist.
Pull has no wildcard expansion, a depth limit of five, independent entity/edge
budgets and cycle stubs. Reference continuation is available through Code Mode
`refs`/`incoming`; returned edge cursors bind the same entity/attribute/direction.

Each compound read/script owns a REPEATABLE READ READ ONLY reader-role
transaction. Caches live inside that snapshot/execution. Cursors are HMAC-signed,
expire after 15 minutes, and bind space, query, sort key and data/schema revisions.
A changed revision returns `CURSOR_STALE`; restart pagination. They do not retain
exported PostgreSQL snapshots across conversations. Oversized reads fail explicitly.

## Code Mode

Source defines synchronous `function main(input)`. It is JavaScript on the pinned
Goja runtime, with no Node, imports, fetch, filesystem, process, timers or async
support. `get` is scalar-only; `values` is a paginated many-scalar operation;
`ref`/`refs` enforce reference cardinality. JS find yields opaque entity handles;
its batch read loads the handle's facts rather than applying the direct MCP find
projection. Integer getters consistently return `bigint`; `db.toSafeNumber`
rejects unsafe conversions. Exact decimals/timestamps remain canonical strings.

Return values are bounded JSON values, entity stubs or tagged signed-int64
BigInts. Undefined (including members), accessors, cycles, promises, functions,
symbols, non-finite numbers, unknown prototypes and excessive nesting fail.
Diagnostics are separately bounded and never constitute the result. Script
errors/timeouts never return a partial success and are not silently replayed.
