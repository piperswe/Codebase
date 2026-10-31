package postgres

import (
	"context"
	"encoding/json"

	"fmt"
	"sort"
	"strconv"
	"strings"

	"codebase.bid/lib/go/cursor"
	"codebase.bid/projects/memory/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func factsFor(ctx context.Context, tx pgx.Tx, space string, ids, attrs []string) ([]model.Fact, error) {
	return factsPage(ctx, tx, space, ids, attrs, "", 10001)
}
func factsPage(ctx context.Context, tx pgx.Tx, space string, ids, attrs []string, last string, limit int) ([]model.Fact, error) {
	rows, e := tx.Query(ctx, `SELECT f.id,f.entity_id::text,f.attribute_id::text,f.value_type,
 coalesce(f.value_text,f.value_integer::text,f.value_decimal::text,to_char(f.value_timestamp AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),f.value_ref::text,''),coalesce(f.value_bool,false),f.assertion_revision::text,f.virtual,
 coalesce((SELECT jsonb_agg(jsonb_build_object('source',v.source_id::text,'confidence',v.confidence,'method',v.method,'observedAt',CASE WHEN v.observed_at IS NULL THEN NULL ELSE to_char(v.observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') END) ORDER BY v.source_id) FROM memory.fact_evidence v WHERE NOT f.virtual AND v.space_id=f.space_id AND v.fact_id=CASE WHEN f.virtual THEN NULL::uuid ELSE f.id::uuid END),'[]')
 FROM memory.read_facts f WHERE f.space_id=$1 AND f.entity_id=ANY($2::uuid[]) AND (cardinality($3::uuid[])=0 OR f.attribute_id=ANY($3::uuid[])) AND f.id>$4 ORDER BY f.id LIMIT $5`, space, ids, nonNil(attrs), last, limit)
	if e != nil {
		return nil, safe(e)
	}
	defer rows.Close()
	out := []model.Fact{}
	totalBytes := 0
	for rows.Next() {
		var f model.Fact
		var ev []byte
		if e = rows.Scan(&f.ID, &f.Entity, &f.Attribute, &f.Value.Type, &f.Value.Text, &f.Value.Bool, &f.Revision, &f.Virtual, &ev); e != nil {
			return nil, safe(e)
		}
		v, err := f.Value.Canonical()
		if err != nil {
			return nil, err
		}
		f.Value = v
		if err = json.Unmarshal(ev, &f.Evidence); err != nil {
			return nil, err
		}
		decoded := len(f.Value.Text) + len(ev) + 512
		if tracked, ok := tx.(*readTx); ok {
			if e = tracked.decoded(decoded); e != nil {
				return nil, e
			}
		}
		totalBytes += decoded
		if totalBytes > 4<<20 {
			return nil, model.Limit("Decoded fact payload exceeds 4 MiB")
		}
		out = append(out, f)
		if len(out) > 10000 {
			return nil, model.Limit("Too many facts; request fewer attributes/entities")
		}
	}
	return out, safe(rows.Err())
}
func (r *Session) Get(names, attributes []string) ([]model.Entity, error) {
	if len(names) == 0 || len(names) > 100 || len(attributes) > 100 {
		return nil, model.Limit("Batch size must be 1..100")
	}
	if e := r.budget(); e != nil {
		return nil, e
	}
	normalized := append([]string(nil), names...)
	uuidIDs := []string{}
	for i, name := range normalized {
		if id, e := uuid.Parse(name); e == nil {
			normalized[i] = id.String()
			uuidIDs = append(uuidIDs, id.String())
		}
	}
	resolved, e := r.tx.Query(r.ctx, `SELECT e.id::text,e.id::text FROM memory.entities e WHERE e.space_id=$1 AND e.id=ANY($3::uuid[]) UNION ALL SELECT n.name,n.entity_id::text FROM memory.entity_names n WHERE n.space_id=$1 AND n.name=ANY($2::text[])`, r.Space, normalized, uuidIDs)
	if e != nil {
		return nil, safe(e)
	}
	matched := map[string]string{}
	for resolved.Next() {
		var name, id string
		if e = resolved.Scan(&name, &id); e != nil {
			resolved.Close()
			return nil, safe(e)
		}
		matched[name] = id
		r.nameCache[name] = id
	}
	e = resolved.Err()
	resolved.Close()
	if e != nil {
		return nil, safe(e)
	}
	ids := []string{}
	for _, name := range normalized {
		id := matched[name]
		if id == "" {
			return nil, model.E("ENTITY_NOT_FOUND", "Entity does not exist in this space")
		}
		ids = append(ids, id)
	}
	attrs := []string{}
	for _, n := range attributes {
		d, e := r.Definition(n)
		if e != nil {
			return nil, e
		}
		attrs = append(attrs, d.ID)
	}
	if e := r.budget(); e != nil {
		return nil, e
	}
	rows, e := r.tx.Query(r.ctx, `SELECT e.id::text,n.name,e.kind,e.revision::text,e.archived_revision IS NOT NULL FROM memory.entities e LEFT JOIN memory.entity_names n ON n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical WHERE e.space_id=$1 AND e.id=ANY($2::uuid[]) ORDER BY e.id`, r.Space, ids)
	if e != nil {
		return nil, safe(e)
	}
	byID := map[string]*model.Entity{}
	for rows.Next() {
		var v model.Entity
		v.Facts = []model.Fact{}
		if e = rows.Scan(&v.ID, &v.Ident, &v.Kind, &v.Revision, &v.Archived); e != nil {
			rows.Close()
			return nil, safe(e)
		}
		byID[v.ID] = &v
		r.loaded[v.ID] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, safe(e)
	}
	if e = r.budget(); e != nil {
		return nil, e
	}
	facts, e := factsFor(r.ctx, r.tx, r.Space, ids, attrs)
	if e != nil {
		return nil, e
	}
	for _, f := range facts {
		byID[f.Entity].Facts = append(byID[f.Entity].Facts, f)
	}
	out := []model.Entity{}
	for _, id := range ids {
		out = append(out, *byID[id])
	}
	if e = r.budget(); e != nil {
		return nil, e
	}
	return out, nil
}
func pageSize(n int) (int, error) {
	if n == 0 {
		return 50, nil
	}
	if n < 1 || n > 100 {
		return 0, model.Limit("Page limit must be 1..100")
	}
	return n, nil
}
func (r *Session) after(options model.PageOptions, q any) (string, int, error) {
	n, e := pageSize(options.Limit)
	if e != nil {
		return "", 0, e
	}
	if options.Cursor == "" {
		return "", n, nil
	}
	c, e := cursor.Decode(r.key, options.Cursor)
	if e != nil || c.Space != r.Space || c.Query != model.Hash(q) {
		return "", 0, model.E("INVALID_CURSOR", "Cursor does not match this query")
	}
	if c.Data != r.Revision || c.Schema != r.SchemaRevision {
		return "", 0, model.E("CURSOR_STALE", "Data or schema changed; restart pagination")
	}
	return c.Last, n, nil
}
func (r *Session) next(last string, q any) *string {
	s, _ := cursor.Encode(r.key, cursor.State{Space: r.Space, Query: model.Hash(q), Last: last, Data: r.Revision, Schema: r.SchemaRevision})
	return &s
}
func (r *Session) Search(req model.Search) (model.Page[model.Hit], error) {
	out := model.Page[model.Hit]{Items: []model.Hit{}, Complete: true}
	q := req
	q.Cursor = ""
	q.Limit = 0
	last, n, e := r.after(req.PageOptions, q)
	if e != nil {
		return out, e
	}
	if len(req.Query) > 500 || len(req.Types) > 20 {
		return out, model.Limit("Search is too large")
	}
	if req.Kind != "" && req.Kind != "ordinary" && req.Kind != "attribute" && req.Kind != "type" && req.Kind != "source" {
		return out, model.Invalid("kind")
	}
	types := []string{}
	for _, name := range req.Types {
		id, e := r.TypeID(name)
		if e != nil {
			return out, e
		}
		types = append(types, id)
	}
	rank := -1
	lastID := "00000000-0000-0000-0000-000000000000"
	if last != "" {
		p := strings.SplitN(last, ":", 2)
		if len(p) != 2 {
			return out, model.Invalid("cursor")
		}
		rank, e = strconv.Atoi(p[0])
		if e != nil {
			return out, model.Invalid("cursor")
		}
		lastID = p[1]
	}
	if e = r.budget(); e != nil {
		return out, e
	}
	rows, e := r.tx.Query(r.ctx, `WITH hits AS(SELECT e.id,n.name,e.kind,left(s.text,256) snippet,
 CASE WHEN n.name=$2 THEN 0 WHEN EXISTS(SELECT 1 FROM memory.entity_names a WHERE a.space_id=e.space_id AND a.entity_id=e.id AND a.name=$2) THEN 1 ELSE 2 END rank
 FROM memory.entities e JOIN memory.entity_search s ON s.space_id=e.space_id AND s.entity_id=e.id LEFT JOIN memory.entity_names n ON n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical
 WHERE e.space_id=$1 AND e.archived_revision IS NULL AND ($3='' OR e.kind=$3) AND ($2='' OR s.tsv @@ plainto_tsquery('simple',$2) OR EXISTS(SELECT 1 FROM memory.entity_names a WHERE a.space_id=e.space_id AND a.entity_id=e.id AND a.name=$2))
 AND (cardinality($4::uuid[])=0 OR EXISTS(SELECT 1 FROM memory.current_facts f JOIN memory.entity_names an ON an.space_id=f.space_id AND an.entity_id=f.attribute_id AND an.name='entity/type' WHERE f.space_id=e.space_id AND f.entity_id=e.id AND f.value_ref=ANY($4::uuid[]))))
 SELECT id::text,name,kind,snippet,rank FROM hits WHERE (rank,id)>($5,$6::uuid) ORDER BY rank,id LIMIT $7`, r.Space, req.Query, req.Kind, types, rank, lastID, n+1)
	if e != nil {
		return out, safe(e)
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var h model.Hit
		var rank int
		if e = rows.Scan(&h.ID, &h.Ident, &h.Kind, &h.Snippet, &rank); e != nil {
			return out, safe(e)
		}
		r.loaded[h.ID] = true
		out.Items = append(out.Items, h)
		keys = append(keys, fmt.Sprintf("%d:%s", rank, h.ID))
	}
	if e = rows.Err(); e != nil {
		return out, safe(e)
	}
	if len(out.Items) > n {
		out.Items = out.Items[:n]
		out.Complete = false
		out.NextCursor = r.next(keys[n-1], q)
	}
	return out, nil
}
func (r *Session) Schema(req model.SchemaQuery) (any, error) {
	switch req.Action {
	case "help":
		return map[string]any{"sdkVersion": model.SDKVersion, "entrypoint": "function main(input)", "integers": "bigint; wire integers and decimals are exact strings", "writes": "Conventional MCP only; Code Mode is read-only", "archival": "Archival/retraction preserves history and backups; it is not erasure"}, nil
	case "describe":
		d, e := r.Definition(req.Entity)
		if e == nil {
			return d, nil
		}
		t, err := r.typeDefinition(req.Entity)
		if err == nil {
			return t, nil
		}
		return nil, e
	case "list", "search", "types":
		q := req
		q.Cursor = ""
		q.Limit = 0
		last, n, e := r.after(req.PageOptions, q)
		if e != nil {
			return nil, e
		}
		if last == "" {
			last = "00000000-0000-0000-0000-000000000000"
		}
		kind := "attribute"
		if req.Action == "types" {
			kind = "type"
		}
		if len(req.Query) > 500 {
			return nil, model.Limit("Query is too long")
		}
		if e = r.budget(); e != nil {
			return nil, e
		}
		rows, e := r.tx.Query(r.ctx, `SELECT e.id::text,n.name,coalesce(a.description,t.description),coalesce(a.schema_revision,t.schema_revision)::text,ARRAY(SELECT alias.name FROM memory.entity_names alias WHERE alias.space_id=e.space_id AND alias.entity_id=e.id AND NOT alias.is_canonical ORDER BY alias.name) FROM memory.entities e JOIN memory.entity_names n ON n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical LEFT JOIN memory.attribute_defs a ON a.space_id=e.space_id AND a.id=e.id LEFT JOIN memory.type_defs t ON t.space_id=e.space_id AND t.id=e.id JOIN memory.entity_search s ON s.space_id=e.space_id AND s.entity_id=e.id WHERE e.space_id=$1 AND e.kind=$2 AND e.id>$3::uuid AND ($4='' OR s.tsv @@ plainto_tsquery('simple',$4) OR n.name=$4) ORDER BY e.id LIMIT $5`, r.Space, kind, last, req.Query, n+1)
		if e != nil {
			return nil, safe(e)
		}
		ds := []model.Definition{}
		for rows.Next() {
			var d model.Definition
			d.Kind = kind
			d.Status = "active"
			d.SchemaRevision = model.Revision(r.SchemaRevision)
			if e = rows.Scan(&d.ID, &d.Ident, &d.Description, &d.SchemaRevision, &d.Aliases); e != nil {
				rows.Close()
				return nil, safe(e)
			}
			r.loaded[d.ID] = true
			ds = append(ds, d)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, safe(e)
		}
		out := model.Page[model.Definition]{Items: ds, Complete: true}
		if len(ds) > n {
			out.Items = ds[:n]
			out.Complete = false
			out.NextCursor = r.next(ds[n-1].ID, q)
		}
		if kind == "attribute" {
			for i := range out.Items {
				out.Items[i], e = r.Definition(out.Items[i].ID)
				if e != nil {
					return nil, e
				}
			}
		}
		return out, nil
	default:
		return nil, model.Invalid("schema action")
	}
}
func (r *Session) History(req model.History) (model.Page[model.Event], error) {
	out := model.Page[model.Event]{Items: []model.Event{}, Complete: true}
	q := req
	q.Cursor = ""
	q.Limit = 0
	last, n, e := r.after(req.PageOptions, q)
	if e != nil {
		return out, e
	}
	entity := ""
	if req.Entity != "" {
		entity, e = r.Resolve(req.Entity)
		if e != nil {
			return out, e
		}
	}
	var rev int64
	ord := 0
	if last != "" {
		p := strings.Split(last, ":")
		if len(p) != 2 {
			return out, model.Invalid("cursor")
		}
		rev, e = strconv.ParseInt(p[0], 10, 64)
		if e != nil {
			return out, model.Invalid("cursor")
		}
		ord, e = strconv.Atoi(p[1])
		if e != nil {
			return out, model.Invalid("cursor")
		}
	}
	if e = r.budget(); e != nil {
		return out, e
	}
	rows, e := r.tx.Query(r.ctx, `WITH events AS(SELECT * FROM memory.fact_events UNION ALL SELECT * FROM memory.entity_events UNION ALL SELECT * FROM memory.schema_events)
 SELECT e.revision::text,e.ordinal,e.kind,e.entity_id::text,e.payload,t.actor,t.client,t.reason,to_char(t.committed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') FROM events e JOIN memory.transaction_history t ON t.space_id=e.space_id AND t.revision=e.revision WHERE e.space_id=$1 AND ($2='' OR e.entity_id=nullif($2,'')::uuid) AND (e.revision,e.ordinal)>($3,$4) ORDER BY e.revision,e.ordinal LIMIT $5`, r.Space, entity, rev, ord, n+1)
	if e != nil {
		return out, safe(e)
	}
	defer rows.Close()
	for rows.Next() {
		var v model.Event
		if e = rows.Scan(&v.Revision, &v.Ordinal, &v.Kind, &v.Entity, &v.Payload, &v.Actor, &v.Client, &v.Reason, &v.CommittedAt); e != nil {
			return out, safe(e)
		}
		r.loaded[v.Entity] = true
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return out, safe(e)
	}
	if len(out.Items) > n {
		out.Items = out.Items[:n]
		out.Complete = false
		v := out.Items[n-1]
		out.NextCursor = r.next(v.Revision+":"+strconv.Itoa(v.Ordinal), q)
	}
	return out, nil
}

// References is also the continuation operation for a truncated pull edge.
func (r *Session) References(entity, attribute, direction string, opt model.PageOptions) (model.Page[model.Entity], error) {
	out := model.Page[model.Entity]{Items: []model.Entity{}, Complete: true}
	id, e := r.Resolve(entity)
	if e != nil {
		return out, e
	}
	d, e := r.Definition(attribute)
	if e != nil {
		return out, e
	}
	if d.ValueType != model.Ref {
		return out, model.E("INVALID_VALUE_TYPE", "Reference operation requires a reference attribute")
	}
	if direction != "in" && direction != "out" {
		return out, model.Invalid("direction")
	}
	q := []string{"refs", id, d.ID, direction}
	last, n, e := r.after(opt, q)
	if e != nil {
		return out, e
	}
	if last == "" {
		last = "00000000-0000-0000-0000-000000000000"
	}
	if e = r.budget(); e != nil {
		return out, e
	}
	column, predicate := "value_ref", "entity_id"
	if direction == "in" {
		column, predicate = "entity_id", "value_ref"
	}
	rows, e := r.tx.Query(r.ctx, `SELECT DISTINCT `+column+`::text FROM memory.read_facts WHERE space_id=$1 AND attribute_id=$2 AND `+predicate+`=$3 AND `+column+`>$4::uuid ORDER BY `+column+`::text LIMIT $5`, r.Space, d.ID, id, last, n+1)
	if e != nil {
		return out, safe(e)
	}
	ids := []string{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return out, safe(e)
		}
		ids = append(ids, v)
		r.Edges++
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, safe(e)
	}
	if len(ids) > n {
		ids = ids[:n]
		out.Complete = false
		out.NextCursor = r.next(ids[n-1], q)
	}
	if len(ids) > 0 {
		out.Items, e = r.Get(ids, nil)
	}
	return out, e
}

// stableIDs makes breadth-level reads deterministic without implicit ordering of sets.
func stableIDs(ids []string) []string {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	out := []string{}
	for id := range m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Values pages the typed fact set by stable fact identity in the same snapshot.
func (r *Session) Values(entity, attribute string, opt model.PageOptions) (model.Page[model.Fact], error) {
	out := model.Page[model.Fact]{Items: []model.Fact{}, Complete: true}
	id, e := r.Resolve(entity)
	if e != nil {
		return out, e
	}
	d, e := r.Definition(attribute)
	if e != nil {
		return out, e
	}
	q := []string{"values", id, d.ID}
	last, n, e := r.after(opt, q)
	if e != nil {
		return out, e
	}
	if e = r.budget(); e != nil {
		return out, e
	}
	out.Items, e = factsPage(r.ctx, r.tx, r.Space, []string{id}, []string{d.ID}, last, n+1)
	if e != nil {
		return out, e
	}
	if len(out.Items) > n {
		out.Items = out.Items[:n]
		out.Complete = false
		out.NextCursor = r.next(out.Items[n-1].ID, q)
	}
	return out, nil
}

func (r *Session) typeDefinition(name string) (model.Definition, error) {
	var d model.Definition
	d.Kind = "type"
	d.Status = "active"
	id, e := r.Resolve(name)
	if e != nil {
		return d, e
	}
	e = r.tx.QueryRow(r.ctx, `SELECT t.id::text,n.name,t.description,t.schema_revision::text,ARRAY(SELECT alias.name FROM memory.entity_names alias WHERE alias.space_id=t.space_id AND alias.entity_id=t.id AND NOT alias.is_canonical ORDER BY alias.name) FROM memory.type_defs t JOIN memory.entity_names n ON n.space_id=t.space_id AND n.entity_id=t.id AND n.is_canonical WHERE t.space_id=$1 AND t.id=$2`, r.Space, id).Scan(&d.ID, &d.Ident, &d.Description, &d.SchemaRevision, &d.Aliases)
	return d, safe(e)
}
