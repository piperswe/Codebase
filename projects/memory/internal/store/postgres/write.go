package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"codebase.bid/projects/memory/internal/model"
	"github.com/jackc/pgx/v5"
)

type write struct {
	request     model.RequestInfo
	ctx         context.Context
	tx          pgx.Tx
	space       string
	rev, schema int64
	ordinal     int
	actor       model.Principal
	reason      string
	touched     map[string]bool
}

func (s *Store) begin(ctx context.Context, p model.Principal, slug, scope, reason string) (*write, error) {
	if e := writeScope(p, slug, scope); e != nil {
		return nil, e
	}
	if reason == "" || !validText(reason, 4096) {
		return nil, model.Invalid("reason")
	}
	ctx, request := model.EnsureRequest(ctx)
	tx, e := s.App.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if e != nil {
		return nil, safe(e)
	}
	w := &write{request: request, ctx: ctx, tx: tx, actor: p, reason: reason, touched: map[string]bool{}}
	e = tx.QueryRow(ctx, `SELECT id::text,revision,schema_revision FROM memory.spaces WHERE slug=$1 FOR UPDATE`, slug).Scan(&w.space, &w.rev, &w.schema)
	if e != nil {
		tx.Rollback(context.Background())
		return nil, model.E("PERMISSION_DENIED", "Space or scope is not granted")
	}
	w.rev++
	return w, nil
}
func (w *write) event(table, kind, entity string, payload any) error {
	// table is selected only by internal exhaustive call sites.
	if table != "fact_events" && table != "entity_events" && table != "schema_events" {
		panic("invalid event table")
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	w.ordinal++
	_, e = w.tx.Exec(w.ctx, `INSERT INTO memory.`+table+`(space_id,revision,ordinal,entity_id,kind,payload) VALUES($1,$2,$3,$4,$5,$6)`, w.space, w.rev, w.ordinal, entity, kind, b)
	w.touched[entity] = true
	return safe(e)
}
func (w *write) finish(key, hash string, result any) error {
	for id := range w.touched {
		if _, e := w.tx.Exec(w.ctx, `UPDATE memory.entities SET revision=$3 WHERE space_id=$1 AND id=$2`, w.space, id, w.rev); e != nil {
			return safe(e)
		}
	}
	_, e := w.tx.Exec(w.ctx, `UPDATE memory.spaces SET revision=$2,schema_revision=$3 WHERE id=$1`, w.space, w.rev, w.schema)
	if e != nil {
		return safe(e)
	}
	ids := []string{}
	for id := range w.touched {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		if e = refreshSearch(w.ctx, w.tx, w.space, ids); e != nil {
			return safe(e)
		}
	}
	b, _ := json.Marshal(result)
	_, e = w.tx.Exec(w.ctx, `INSERT INTO memory.transactions(space_id,revision,actor,client,request_id,reason,idempotency_key,request_hash,result,received_at) VALUES($1,$2,$3,$4,$5,$6,nullif($7,''),nullif($8,''),$9,$10)`, w.space, w.rev, w.actor.Actor, w.actor.Client, w.request.ID, w.reason, key, hash, b, w.request.ReceivedAt)
	if e != nil {
		return safe(e)
	}
	return safe(w.tx.Commit(w.ctx))
}
func refreshSearch(ctx context.Context, tx pgx.Tx, space string, ids []string) error {
	_, e := tx.Exec(ctx, `INSERT INTO memory.entity_search(space_id,entity_id,text,tsv)
 SELECT e.space_id,e.id,left(concat_ws(' ',n.names,l.labels,a.description,t.description),8192),to_tsvector('simple',left(concat_ws(' ',n.names,l.labels,a.description,t.description),8192))
 FROM memory.entities e
 LEFT JOIN LATERAL(SELECT string_agg(name,' ' ORDER BY name) names FROM memory.entity_names WHERE space_id=e.space_id AND entity_id=e.id) n ON true
 LEFT JOIN LATERAL(SELECT string_agg(f.value_text,' ' ORDER BY f.id) labels FROM memory.current_facts f JOIN memory.entity_names an ON an.space_id=f.space_id AND an.entity_id=f.attribute_id AND an.name='entity/label' WHERE f.space_id=e.space_id AND f.entity_id=e.id) l ON true
 LEFT JOIN memory.attribute_defs a ON a.space_id=e.space_id AND a.id=e.id LEFT JOIN memory.type_defs t ON t.space_id=e.space_id AND t.id=e.id
 WHERE e.space_id=$1 AND (cardinality($2::uuid[])=0 OR e.id=ANY($2::uuid[])) ON CONFLICT(space_id,entity_id) DO UPDATE SET text=excluded.text,tsv=excluded.tsv`, space, nonNil(ids))
	return e
}
func (w *write) resolve(name string, temps map[string]string) (string, error) {
	if strings.HasPrefix(name, "temp:") {
		id, ok := temps[strings.TrimPrefix(name, "temp:")]
		if !ok {
			return "", model.Invalid("temporary reference")
		}
		return id, nil
	}
	return resolve(w.ctx, w.tx, w.space, name)
}
func (w *write) entityRef(ref model.EntityRef, temps map[string]string) (string, error) {
	if ref.TempID != "" {
		return w.resolve("temp:"+ref.TempID, temps)
	}
	return w.resolve(ref.ID, temps)
}
func (w *write) precondition(id, revision string) error {
	if _, e := model.ParseRevision(revision); e != nil {
		return e
	}
	var got string
	if e := w.tx.QueryRow(w.ctx, `SELECT revision::text FROM memory.entities WHERE space_id=$1 AND id=$2`, w.space, id).Scan(&got); e != nil {
		return safe(e)
	}
	if got != revision {
		return model.E("VERSION_CONFLICT", "Entity revision changed")
	}
	return nil
}
func (w *write) name(id, name string, canonical bool) error {
	if !model.ValidName(name) || model.Reserved(name) {
		return model.Invalid("entity name")
	}
	if canonical {
		if _, e := w.tx.Exec(w.ctx, `UPDATE memory.entity_names SET is_canonical=false WHERE space_id=$1 AND entity_id=$2`, w.space, id); e != nil {
			return safe(e)
		}
	}
	_, e := w.tx.Exec(w.ctx, `INSERT INTO memory.entity_names(space_id,entity_id,name,is_canonical) VALUES($1,$2,$3,$4) ON CONFLICT(space_id,name) DO UPDATE SET is_canonical=excluded.is_canonical WHERE memory.entity_names.entity_id=excluded.entity_id`, w.space, id, name, canonical)
	if e != nil {
		return safe(e)
	}
	var owner string
	if e = w.tx.QueryRow(w.ctx, `SELECT entity_id::text FROM memory.entity_names WHERE space_id=$1 AND name=$2`, w.space, name).Scan(&owner); e != nil {
		return safe(e)
	}
	if owner != id {
		return model.E("NAME_CONFLICT", "Name belongs to another entity")
	}
	return w.event("entity_events", "name", id, struct {
		Name      string `json:"name"`
		Canonical bool   `json:"canonical"`
	}{name, canonical})
}
func (s *Store) Transact(ctx context.Context, p model.Principal, req model.Transaction) (model.Commit, error) {
	var out model.Commit
	if len(req.Operations) == 0 || len(req.Operations) > 100 || len(req.Preconditions) > 100 || len(req.IdempotencyKey) < 8 || len(req.IdempotencyKey) > 200 {
		return out, model.Invalid("transaction limits or idempotency key")
	}
	w, e := s.begin(ctx, p, req.Space, "memory:write", req.Reason)
	if e != nil {
		return out, e
	}
	defer w.tx.Rollback(context.Background())
	for _, op := range req.Operations {
		if e := validateOperation(op); e != nil {
			return out, e
		}
	}
	hash := model.Hash(req)
	var previousHash string
	var result []byte
	e = w.tx.QueryRow(ctx, `SELECT request_hash,result FROM memory.transactions WHERE space_id=$1 AND actor=$2 AND idempotency_key=$3`, w.space, p.Actor, req.IdempotencyKey).Scan(&previousHash, &result)
	if e == nil {
		if hash != previousHash {
			return out, model.E("IDEMPOTENCY_CONFLICT", "Key was used with a different request")
		}
		e = json.Unmarshal(result, &out)
		return out, e
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return out, safe(e)
	}
	if req.ExpectedSchemaRevision != model.Revision(w.schema) {
		return out, model.E("SCHEMA_CONFLICT", "Registry revision changed")
	}
	for name, rev := range req.Preconditions {
		id, e := w.resolve(name, nil)
		if e != nil {
			return out, e
		}
		if e = w.precondition(id, rev); e != nil {
			return out, e
		}
	}
	out = model.Commit{Revision: model.Revision(w.rev), SchemaRevision: model.Revision(w.schema), TempIDs: map[string]string{}, Warnings: []string{}}
	// Allocate all entities first, permitting forward temporary references. Ordinary
	// transactions cannot create attribute/type entities.
	for _, op := range req.Operations {
		if op.Op != "createEntity" && op.Op != "createSource" {
			continue
		}
		if op.TempID == "" || len(op.TempID) > 100 || out.TempIDs[op.TempID] != "" {
			return out, model.Invalid("tempId")
		}
		id := model.NewID()
		out.TempIDs[op.TempID] = id
		kind := "ordinary"
		if op.Op == "createSource" {
			kind = "source"
		}
		_, e = w.tx.Exec(ctx, `INSERT INTO memory.entities(space_id,id,kind,created_revision,revision) VALUES($1,$2,$3,$4,$4)`, w.space, id, kind, w.rev)
		if e != nil {
			return out, safe(e)
		}
		if e = w.event("entity_events", "create", id, struct {
			Kind string `json:"kind"`
		}{kind}); e != nil {
			return out, e
		}
		if op.Ident != "" {
			if e = w.name(id, op.Ident, true); e != nil {
				return out, e
			}
		}
		if kind == "source" {
			if op.Source == nil {
				return out, model.Invalid("source")
			}
			src := op.Source
			if !validText(src.Description, 16384) || !validText(src.URI, 4096) || !validText(src.Locator, 4096) {
				return out, model.Invalid("source length")
			}
			observed, e := parseTime(src.ObservedAt)
			if e != nil {
				return out, e
			}
			_, e = w.tx.Exec(ctx, `INSERT INTO memory.sources(space_id,id,origin,uri,locator,description,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, w.space, id, src.Origin, src.URI, src.Locator, src.Description, observed)
			if e != nil {
				return out, safe(e)
			}
			if e = w.event("entity_events", "source", id, src); e != nil {
				return out, e
			}
		} else if op.Source != nil {
			return out, model.Invalid("source on ordinary entity")
		}
	}
	for _, op := range req.Operations {
		if op.Op == "createEntity" || op.Op == "createSource" {
			id := out.TempIDs[op.TempID]
			for _, typ := range op.Types {
				typeID, e := w.resolve(typ, out.TempIDs)
				if e != nil {
					return out, e
				}
				v := model.Value{Type: model.Ref, Text: typeID}
				d, e := definition(ctx, w.tx, w.space, "entity/type")
				if e != nil {
					return out, e
				}
				// Type membership on creation is server-derived and sourced to a synthetic
				// system source, rather than fabricated user evidence.
				source, e := w.systemSource()
				if e != nil {
					return out, e
				}
				_, e = w.assert(id, d, v, []model.Evidence{{Source: source}}, s.Fingerprint)
				if e != nil {
					return out, e
				}
			}
			out.Applied++
			continue
		}
		id, e := w.entityRef(op.Entity, out.TempIDs)
		if e != nil {
			return out, e
		}
		var kind string
		var archived bool
		if e = w.tx.QueryRow(ctx, `SELECT kind,archived_revision IS NOT NULL FROM memory.entities WHERE space_id=$1 AND id=$2`, w.space, id).Scan(&kind, &archived); e != nil {
			return out, safe(e)
		}
		if op.ExpectedRevision != "" {
			if e = w.precondition(id, op.ExpectedRevision); e != nil {
				return out, e
			}
		}
		changed := true
		switch op.Op {
		case "addAlias", "renameEntity":
			if kind == "attribute" || kind == "type" {
				return out, model.E("PERMISSION_DENIED", "Use the explicit schema API to change schema names")
			}
			e = w.name(id, op.Name, op.Op == "renameEntity")
		case "archiveEntity":
			if kind != "ordinary" {
				return out, model.E("PERMISSION_DENIED", "Only ordinary entities may be archived")
			}
			if op.ExpectedRevision == "" {
				return out, model.Invalid("expectedRevision")
			}
			if archived {
				changed = false
				break
			}
			_, e = w.tx.Exec(ctx, `UPDATE memory.entities SET archived_revision=$3 WHERE space_id=$1 AND id=$2`, w.space, id, w.rev)
			if e == nil {
				e = w.event("entity_events", "archive", id, struct{}{})
			}
		case "assert", "replace", "retract", "addEvidence":
			d, err := definition(ctx, w.tx, w.space, op.Attribute)
			if err != nil {
				return out, err
			}
			if op.Unit != "" && op.Unit != d.Unit {
				return out, model.E("INVALID_VALUE_TYPE", "Measurement unit does not match the registered canonical unit")
			}
			if d.Protected {
				return out, model.E("PERMISSION_DENIED", "Protected metadata requires a schema operation")
			}
			if (op.Op == "assert" || op.Op == "replace") && (archived || d.Status != "active") {
				return out, model.E("PERMISSION_DENIED", "Entity is archived or attribute is deprecated")
			}
			if (op.Op == "replace" || op.Op == "retract") && op.ExpectedRevision == "" && op.ExpectedFact == "" && req.Preconditions[id] == "" && req.Preconditions[op.Entity.ID] == "" {
				return out, model.Invalid("optimistic precondition")
			}
			facts, err := factsFor(ctx, w.tx, w.space, []string{id}, []string{d.ID})
			if err != nil {
				return out, err
			}
			var old *model.Fact
			for i := range facts {
				if op.ExpectedFact != "" && facts[i].ID == op.ExpectedFact {
					old = &facts[i]
				}
				if d.Cardinality == "one" && op.ExpectedFact == "" {
					old = &facts[i]
				}
			}
			if d.Cardinality == "many" && op.Op == "retract" && op.ExpectedFact == "" && op.Value != nil {
				wanted := *op.Value
				if wanted.Type == model.Ref {
					wanted.Text, e = w.resolve(wanted.Text, out.TempIDs)
					if e != nil {
						return out, e
					}
				}
				wanted, e = wanted.Canonical()
				if e != nil {
					return out, e
				}
				for i := range facts {
					if facts[i].Value.Equal(wanted) {
						old = &facts[i]
					}
				}
			}
			if op.ExpectedFact != "" && old == nil {
				return out, model.E("VERSION_CONFLICT", "Expected fact changed")
			}
			if op.Op == "retract" || op.Op == "replace" {
				if op.Op == "replace" && d.Cardinality != "one" {
					return out, model.E("CARDINALITY_VIOLATION", "Replace requires cardinality one")
				}
				if old == nil {
					return out, model.E("VERSION_CONFLICT", "Expected fact is absent")
				}
				if e = w.event("fact_events", "retract", id, old); e != nil {
					return out, e
				}
				_, e = w.tx.Exec(ctx, `DELETE FROM memory.current_facts WHERE space_id=$1 AND id=$2`, w.space, old.ID)
				if e != nil {
					return out, safe(e)
				}
				if op.Op == "retract" {
					break
				}
			}
			if op.Op == "addEvidence" {
				if old == nil || len(op.Evidence) == 0 {
					return out, model.Invalid("fact and evidence")
				}
				changed, e = w.evidence(*old, op.Evidence, out.TempIDs)
				break
			}
			if op.Value == nil {
				return out, model.Invalid("value")
			}
			v := *op.Value
			if v.Type == model.Ref {
				v.Text, e = w.resolve(v.Text, out.TempIDs)
				if e != nil {
					return out, e
				}
			}
			if v.Type != d.ValueType {
				return out, model.E("INVALID_VALUE_TYPE", "Value does not match registered attribute")
			}
			v, e = v.Canonical()
			if e != nil {
				return out, e
			}
			if op.Absent && len(facts) > 0 {
				return out, model.E("VERSION_CONFLICT", "Attribute is already present")
			}
			if len(op.Evidence) == 0 {
				return out, model.Invalid("assertion evidence")
			}
			evidence := append([]model.Evidence(nil), op.Evidence...)
			for i := range evidence {
				evidence[i].Source, e = w.resolve(evidence[i].Source, out.TempIDs)
				if e != nil {
					return out, e
				}
			}
			changed, e = w.assert(id, d, v, evidence, s.Fingerprint)
		default:
			return out, model.Invalid("operation")
		}
		if e != nil {
			return out, safe(e)
		}
		if changed {
			out.Applied++
		} else {
			out.Noop++
		}
	}
	if e = w.validateFinal(); e != nil {
		return out, e
	}
	if e = w.finish(req.IdempotencyKey, hash, out); e != nil {
		return model.Commit{}, e
	}
	return out, nil
}
func parseTime(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	v, e := (model.Value{Type: model.Timestamp, Text: *s}).Canonical()
	if e != nil {
		return nil, e
	}
	t, _ := time.Parse(time.RFC3339Nano, v.Text)
	return &t, nil
}
func (w *write) systemSource() (string, error) {
	id := model.StableID(w.space, "sys/transaction-source")
	tag, e := w.tx.Exec(w.ctx, `INSERT INTO memory.entities(space_id,id,kind,created_revision,revision) VALUES($1,$2,'source',$3,$3) ON CONFLICT DO NOTHING`, w.space, id, w.rev)
	if e != nil {
		return "", safe(e)
	}
	source := model.Source{Origin: "system", Description: "Explicit type membership from createEntity"}
	if _, e = w.tx.Exec(w.ctx, `INSERT INTO memory.sources(space_id,id,origin,description) VALUES($1,$2,'system',$3) ON CONFLICT DO NOTHING`, w.space, id, source.Description); e != nil {
		return "", safe(e)
	}
	if tag.RowsAffected() > 0 {
		e = w.event("entity_events", "createSource", id, source)
	}
	return id, e
}

func (w *write) assert(id string, d model.Definition, v model.Value, evidence []model.Evidence, fp func(model.Value) []byte) (bool, error) {
	if v.Type == model.Ref {
		var archived bool
		if e := w.tx.QueryRow(w.ctx, `SELECT archived_revision IS NOT NULL FROM memory.entities WHERE space_id=$1 AND id=$2`, w.space, v.Text).Scan(&archived); e != nil {
			return false, model.E("ENTITY_NOT_FOUND", "Reference target is not in this space")
		}
		if archived {
			return false, model.E("PERMISSION_DENIED", "Cannot add a reference to an archived entity")
		}
	}
	existing, e := factsFor(w.ctx, w.tx, w.space, []string{id}, []string{d.ID})
	if e != nil {
		return false, e
	}
	for _, f := range existing {
		if f.Value.Equal(v) {
			return w.evidence(f, evidence, nil)
		}
		if string(fp(f.Value)) == string(fp(v)) {
			return false, model.E("VALUE_FINGERPRINT_COLLISION", "Unequal values have the same fingerprint")
		}
		if d.Cardinality == "one" {
			return false, model.E("CARDINALITY_VIOLATION", "Scalar already has a different value; use replace")
		}
	}
	fid := model.NewID()
	vals := make([]any, 6)
	switch v.Type {
	case model.String:
		vals[0] = v.Text
	case model.Boolean:
		vals[1] = v.Bool
	case model.Integer:
		vals[2] = v.Text
	case model.Decimal:
		vals[3] = v.Text
	case model.Timestamp:
		t, _ := time.Parse(time.RFC3339Nano, v.Text)
		vals[4] = t
	case model.Ref:
		vals[5] = v.Text
	}
	_, e = w.tx.Exec(w.ctx, `INSERT INTO memory.current_facts(space_id,id,entity_id,attribute_id,value_type,cardinality,value_text,value_bool,value_integer,value_decimal,value_timestamp,value_ref,value_fingerprint,assertion_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, w.space, fid, id, d.ID, v.Type, d.Cardinality, vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], fp(v), w.rev)
	if e != nil {
		return false, safe(e)
	}
	f := model.Fact{ID: fid, Entity: id, Attribute: d.ID, Value: v, Revision: model.Revision(w.rev), Evidence: []model.Evidence{}}
	if _, e = w.evidence(f, evidence, nil); e != nil {
		return false, e
	}
	f.Evidence = evidence
	return true, w.event("fact_events", "assert", id, f)
}
func (w *write) evidence(f model.Fact, ev []model.Evidence, temps map[string]string) (bool, error) {
	if len(ev) > 20 {
		return false, model.Limit("Too many sources")
	}
	changed := false
	for _, v := range ev {
		var e error
		v.Source, e = w.resolve(v.Source, temps)
		if e != nil {
			return false, e
		}
		if v.Confidence != nil && (math.IsNaN(*v.Confidence) || math.IsInf(*v.Confidence, 0) || *v.Confidence < 0 || *v.Confidence > 1) {
			return false, model.Invalid("confidence")
		}
		if !validText(v.Method, 1000) {
			return false, model.Invalid("evidence method")
		}
		t, e := parseTime(v.ObservedAt)
		if e != nil {
			return false, e
		}
		tag, e := w.tx.Exec(w.ctx, `INSERT INTO memory.fact_evidence(space_id,fact_id,source_id,confidence,method,observed_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(space_id,fact_id,source_id) DO UPDATE SET confidence=excluded.confidence,method=excluded.method,observed_at=excluded.observed_at WHERE (memory.fact_evidence.confidence,memory.fact_evidence.method,memory.fact_evidence.observed_at) IS DISTINCT FROM (excluded.confidence,excluded.method,excluded.observed_at)`, w.space, f.ID, v.Source, v.Confidence, v.Method, t)
		if e != nil {
			return false, safe(e)
		}
		if tag.RowsAffected() > 0 {
			changed = true
			if e = w.event("fact_events", "evidence", f.Entity, struct {
				Fact     string         `json:"fact"`
				Evidence model.Evidence `json:"evidence"`
			}{f.ID, v}); e != nil {
				return false, e
			}
		}
	}
	return changed, nil
}
func (w *write) validateFinal() error {
	// Recheck the final graph after type removal, assertions and all temp-ID work.
	var bad bool
	e := w.tx.QueryRow(w.ctx, `SELECT EXISTS(SELECT 1 FROM memory.current_facts f JOIN memory.entity_names n ON n.space_id=f.space_id AND n.entity_id=f.attribute_id AND n.name='entity/type' WHERE f.space_id=$1 AND NOT EXISTS(SELECT 1 FROM memory.type_defs t WHERE t.space_id=f.space_id AND t.id=f.value_ref))`, w.space).Scan(&bad)
	if e != nil {
		return safe(e)
	}
	if bad {
		return model.E("REFERENCE_TYPE_MISMATCH", "entity/type requires a registered type")
	}
	for _, x := range []struct{ table, entity, code string }{{"attribute_domains", "f.entity_id", "DOMAIN_VIOLATION"}, {"attribute_ranges", "f.value_ref", "REFERENCE_TYPE_MISMATCH"}} {
		e = w.tx.QueryRow(w.ctx, `SELECT EXISTS(SELECT 1 FROM memory.current_facts f WHERE f.space_id=$1 AND EXISTS(SELECT 1 FROM memory.`+x.table+` d WHERE d.space_id=f.space_id AND d.attribute_id=f.attribute_id) AND NOT EXISTS(SELECT 1 FROM memory.`+x.table+` d JOIN memory.current_facts m ON m.space_id=d.space_id AND m.entity_id=`+x.entity+` AND m.value_ref=d.type_id JOIN memory.entity_names n ON n.space_id=m.space_id AND n.entity_id=m.attribute_id AND n.name='entity/type' WHERE d.space_id=f.space_id AND d.attribute_id=f.attribute_id))`, w.space).Scan(&bad)
		if e != nil {
			return safe(e)
		}
		if bad {
			return model.E(x.code, "Final type membership would invalidate facts")
		}
	}
	return nil
}

func validateOperation(op model.Operation) error {
	if op.Entity.ID != "" && op.Entity.TempID != "" {
		return model.Invalid("entity reference union")
	}
	b, _ := json.Marshal(op)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	allowed := map[string]bool{"op": true}
	add := func(names ...string) {
		for _, n := range names {
			allowed[n] = true
		}
	}
	switch op.Op {
	case "createEntity":
		add("tempId", "ident", "types")
	case "createSource":
		add("tempId", "ident", "types", "source")
	case "assert", "replace":
		add("entity", "attribute", "value", "evidence", "expectedRevision", "expectedFact", "absent", "unit")
	case "retract":
		add("entity", "attribute", "value", "expectedRevision", "expectedFact")
	case "addEvidence":
		add("entity", "attribute", "evidence", "expectedRevision", "expectedFact")
	case "renameEntity", "addAlias":
		add("entity", "name", "expectedRevision")
	case "archiveEntity":
		add("entity", "expectedRevision")
	default:
		return model.Invalid("operation")
	}
	for n := range fields {
		if !allowed[n] {
			return model.Invalid("field " + n + " for " + op.Op)
		}
	}
	return nil
}

// Reindex is an operator repair operation for the rebuildable search projection.
func (s *Store) Reindex(ctx context.Context, p model.Principal, space string) error {
	w, e := s.begin(ctx, p, space, "memory:write", "Rebuild lexical search projection")
	if e != nil {
		return e
	}
	defer w.tx.Rollback(context.Background())
	if _, e = w.tx.Exec(ctx, `SET LOCAL statement_timeout='30s'`); e != nil {
		return safe(e)
	}
	if e = refreshSearch(ctx, w.tx, w.space, nil); e != nil {
		return safe(e)
	}
	return w.finish("", "", struct{}{})
}
