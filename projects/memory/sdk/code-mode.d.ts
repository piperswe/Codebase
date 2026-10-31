/** Goja JavaScript, not TypeScript execution, Node, a browser or ES modules.
 * All methods are synchronous, read-only, execution/space/snapshot bound.
 * Archival and retraction preserve history and backups; they are not erasure. */
type Scalar = string | boolean | bigint;
interface PageOptions { limit?: number; cursor?: string }
interface Page<T> { items: T[]; complete: boolean; nextCursor: string | null }
interface Value { type: 'string'|'boolean'|'integer'|'decimal'|'timestamp'|'ref'; value: string|boolean }
interface Evidence { source: string; confidence: number|null; method?: string; observedAt?: string|null }
interface Fact { id: string; entity: string; attribute: string; value: Value; revision: string; virtual: boolean; evidence: Evidence[] }
interface Definition { id: string; ident: string; kind: 'attribute'|'type'; description: string; valueType?: Value['type']; cardinality?: 'one'|'many'; appliesTo: string[]; referenceTypes: string[]; unit?: string; status: 'active'|'deprecated'; replacement?: string; examples: string[]; notThis: string[]; aliases: string[]; schemaRevision: string; protected: boolean }
interface Entity {
 readonly id: string; readonly ident: string|null; readonly revision: string; readonly archived: boolean;
 get(attribute: string): Scalar|null; // one only; ref returns UUID, decimal/timestamp exact strings
 values(attribute: string, options?: PageOptions): Page<Scalar>; // many scalar only
 ref(attribute: string): Entity|null;
 refs(attribute: string, options?: PageOptions): Page<Entity>;
 incoming(attribute: string, options?: PageOptions): Page<Entity>;
 pull(pattern: PullPattern): PullResult;
 facts(attributes?: string[]): Page<Fact>;
}
interface SearchQuery extends PageOptions { query?: string; kind?: 'ordinary'|'attribute'|'type'|'source'; types?: string[] }
interface Predicate { all?: Predicate[]; any?: Predicate[]; attribute?: string; op?: 'exists'|'eq'|'lt'|'lte'|'gt'|'gte'|'in'; value?: Value; values?: Value[]; path?: { attribute: string; direction: 'out'|'in' }[] }
interface FindQuery extends PageOptions { where: Predicate; select?: string[]; types?: string[]; query?: string }
interface PullPattern { select: Record<string,true|PullPattern>; incoming?: { attribute: string; select: PullPattern['select']; limit?: number }[]; limit?: number }
interface PullResult { entity: {id:string;ident:string|null;kind:string;revision:string;archived:boolean;facts:Fact[]}; edges: Record<string,Page<PullResult>>; stub: boolean }
interface HistoryQuery extends PageOptions { entity?: string }
interface HistoryEvent { revision: string; ordinal: number; kind: string; entity: string; payload: unknown; actor: string; client: string; reason: string; committedAt: string }
interface Graph {
 entity(idOrIdent: string): Entity;
 entities(idsOrIdents: string[]): Entity[];
 search(query: SearchQuery): Page<{id:string;ident:string|null;kind:string;snippet:string}>;
 find(query: FindQuery): Page<Entity>;
 pull(idOrIdent: string, pattern: PullPattern): PullResult;
 history(query: HistoryQuery): Page<HistoryEvent>;
 schema: { search(query: {query?:string}&PageOptions): Page<Definition>; describe(idOrIdent:string):Definition; list(query?:PageOptions):Page<Definition>; types(query?:PageOptions):Page<Definition> };
 help(topic?: string): object;
 toSafeNumber(value: bigint): number;
}
declare const db: Graph;
declare const console: { log(...primitives: Scalar[]): void };
/** Return JSON values, entity stubs, or signed-int64 BigInts (encoded as tagged
 * integers). Cycles, accessors, undefined, promises and arbitrary prototypes fail. */
declare function main(input: unknown): unknown;
