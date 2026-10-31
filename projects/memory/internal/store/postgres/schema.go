package postgres

import (
	"context"
	"encoding/json"
	"slices"

	"codebase.bid/projects/memory/internal/model"
)

type Proposed struct {
	Revision       string      `json:"revision"`
	ID             string      `json:"id"`
	SchemaRevision string      `json:"schemaRevision"`
	Candidates     []model.Hit `json:"candidates"`
}

func validateDefinition(d model.Definition) error {
	if !model.ValidName(d.Ident) || model.Reserved(d.Ident) || !validText(d.Description, 4096) || d.Description == "" || len(d.Aliases) > 20 || len(d.AppliesTo) > 20 || len(d.ReferenceTypes) > 20 || len(d.Examples) > 20 || len(d.NotThis) > 20 || !validText(d.Unit, 200) || d.Protected {
		return model.Invalid("schema definition")
	}
	if d.Kind != "attribute" && d.Kind != "type" {
		return model.Invalid("schema kind")
	}
	for _, s := range append(append([]string{}, d.Examples...), d.NotThis...) {
		if !validText(s, 1000) {
			return model.Invalid("schema example")
		}
	}
	for _, n := range d.Aliases {
		if !model.ValidName(n) || model.Reserved(n) {
			return model.Invalid("alias")
		}
	}
	if d.Kind == "type" && (d.ValueType != "" || d.Cardinality != "" || d.Unit != "" || len(d.AppliesTo) > 0 || len(d.ReferenceTypes) > 0 || d.Replacement != "" || len(d.Examples) > 0 || len(d.NotThis) > 0 || d.Status == "deprecated") {
		return model.Invalid("type definition properties")
	}
	if d.Kind == "attribute" {
		if !slices.Contains([]model.ValueType{model.String, model.Boolean, model.Integer, model.Decimal, model.Timestamp, model.Ref}, d.ValueType) || !slices.Contains([]string{"one", "many"}, d.Cardinality) || (d.ValueType != model.Ref && len(d.ReferenceTypes) > 0) {
			return model.Invalid("attribute type/cardinality")
		}
	}
	if d.Status != "" && d.Status != "active" && d.Status != "deprecated" {
		return model.Invalid("status")
	}
	return nil
}
func (s *Store) Propose(ctx context.Context, p model.Principal, req model.Proposal) (Proposed, error) {
	var out Proposed
	if e := validateDefinition(req.Definition); e != nil {
		return out, e
	}
	w, e := s.begin(ctx, p, req.Space, "memory:write", "Propose explicit schema definition")
	if e != nil {
		return out, e
	}
	defer w.tx.Rollback(context.Background())
	out = Proposed{Revision: model.Revision(w.rev), ID: model.NewID(), SchemaRevision: model.Revision(w.schema), Candidates: []model.Hit{}}
	rows, e := w.tx.Query(ctx, `SELECT e.id::text,n.name,e.kind,left(coalesce(a.description,t.description,''),256) FROM memory.entities e JOIN memory.entity_names n ON n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical LEFT JOIN memory.attribute_defs a ON a.space_id=e.space_id AND a.id=e.id LEFT JOIN memory.type_defs t ON t.space_id=e.space_id AND t.id=e.id WHERE e.space_id=$1 AND e.kind IN ('attribute','type') AND (n.name=$2 OR to_tsvector('simple',n.name||' '||coalesce(a.description,t.description,'')) @@ plainto_tsquery('simple',$3)) ORDER BY (n.name=$2) DESC,e.id LIMIT 20`, w.space, req.Definition.Ident, req.Definition.Description)
	if e != nil {
		return out, safe(e)
	}
	for rows.Next() {
		var h model.Hit
		if e = rows.Scan(&h.ID, &h.Ident, &h.Kind, &h.Snippet); e != nil {
			rows.Close()
			return out, safe(e)
		}
		out.Candidates = append(out.Candidates, h)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, safe(e)
	}
	b, _ := json.Marshal(req.Definition)
	c, _ := json.Marshal(out.Candidates)
	_, e = w.tx.Exec(ctx, `INSERT INTO memory.schema_proposals(space_id,id,actor,definition,schema_revision,candidates) VALUES($1,$2,$3,$4,$5,$6)`, w.space, out.ID, p.Actor, b, w.schema, c)
	if e != nil {
		return out, safe(e)
	}
	return out, w.finish("", "", out)
}
func (s *Store) Apply(ctx context.Context, p model.Principal, req model.Apply) (model.Commit, error) {
	var out model.Commit
	w, e := s.begin(ctx, p, req.Space, "memory:schema:write", req.Rationale)
	if e != nil {
		return out, e
	}
	defer w.tx.Rollback(context.Background())
	if req.ExpectedSchemaRevision != model.Revision(w.schema) {
		return out, model.E("SCHEMA_CONFLICT", "Registry changed; review a new proposal")
	}
	var b []byte
	var rev int64
	var status string
	e = w.tx.QueryRow(ctx, `SELECT definition,schema_revision,status FROM memory.schema_proposals WHERE space_id=$1 AND id::text=$2`, w.space, req.Proposal).Scan(&b, &rev, &status)
	if e != nil || status != "pending" || rev != w.schema {
		return out, model.E("SCHEMA_CONFLICT", "Proposal is absent, stale, or already reviewed")
	}
	var d model.Definition
	if e = model.Decode(b, &d); e != nil {
		return out, e
	}
	if e = validateDefinition(d); e != nil {
		return out, e
	}
	id := ""
	newStatus := "applied"
	if req.Action == "reject" {
		newStatus = "rejected"
	} else {
		w.schema++
		switch req.Action {
		case "register":
			if d.ID != "" {
				return out, model.Invalid("new definition ID is server allocated")
			}
			id = model.NewID()
			_, e = w.tx.Exec(ctx, `INSERT INTO memory.entities(space_id,id,kind,created_revision,revision) VALUES($1,$2,$3,$4,$4)`, w.space, id, d.Kind, w.rev)
			if e != nil {
				return out, safe(e)
			}
			if e = w.name(id, d.Ident, true); e != nil {
				return out, e
			}
			if d.Kind == "type" {
				_, e = w.tx.Exec(ctx, `INSERT INTO memory.type_defs(space_id,id,description,schema_revision) VALUES($1,$2,$3,$4)`, w.space, id, d.Description, w.schema)
			} else {
				status := d.Status
				if status == "" {
					status = "active"
				}
				var replacement any
				if d.Replacement != "" {
					replacement, e = resolve(ctx, w.tx, w.space, d.Replacement)
					if e != nil {
						return out, e
					}
				}
				examples, _ := json.Marshal(nonNil(d.Examples))
				notThis, _ := json.Marshal(nonNil(d.NotThis))
				_, e = w.tx.Exec(ctx, `INSERT INTO memory.attribute_defs(space_id,id,description,value_type,cardinality,unit,examples,not_this,schema_revision,status,replacement) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, w.space, id, d.Description, d.ValueType, d.Cardinality, d.Unit, examples, notThis, w.schema, status, replacement)
				if e == nil {
					for _, x := range []struct {
						table string
						names []string
					}{{"attribute_domains", d.AppliesTo}, {"attribute_ranges", d.ReferenceTypes}} {
						for _, name := range x.names {
							typeID, err := resolve(ctx, w.tx, w.space, name)
							if err != nil {
								return out, err
							}
							_, e = w.tx.Exec(ctx, `INSERT INTO memory.`+x.table+`(space_id,attribute_id,type_id) VALUES($1,$2,$3)`, w.space, id, typeID)
							if e != nil {
								return out, safe(e)
							}
						}
					}
				}
			}
			if e != nil {
				return out, safe(e)
			}
		case "alias", "update", "deprecate":
			id, e = resolve(ctx, w.tx, w.space, req.Target)
			if e != nil {
				return out, e
			}
			var targetKind, targetName string
			if e = w.tx.QueryRow(ctx, `SELECT e.kind,n.name FROM memory.entities e JOIN memory.entity_names n ON n.space_id=e.space_id AND n.entity_id=e.id AND n.is_canonical WHERE e.space_id=$1 AND e.id=$2`, w.space, id).Scan(&targetKind, &targetName); e != nil {
				return out, safe(e)
			}
			if (d.ID != "" && d.ID != id) || targetKind != d.Kind || model.Reserved(targetName) {
				return out, model.E("PERMISSION_DENIED", "Target kind mismatch or protected definition")
			}
			if targetKind == "type" {
				if req.Action == "deprecate" {
					return out, model.E("SCHEMA_CONFLICT", "V1 type deprecation is unsupported; preserve membership definitions")
				}
				if req.Action == "alias" {
					e = w.name(id, d.Ident, false)
				} else {
					if d.Ident != targetName {
						e = w.name(id, d.Ident, true)
					}
					if e == nil {
						_, e = w.tx.Exec(ctx, `UPDATE memory.type_defs SET description=$3,schema_revision=$4 WHERE space_id=$1 AND id=$2`, w.space, id, d.Description, w.schema)
					}
				}
				if e != nil {
					return out, e
				}
				break
			}
			old, err := definition(ctx, w.tx, w.space, id)
			if err != nil {
				return out, err
			}
			if old.Protected || model.Reserved(old.Ident) {
				return out, model.E("PERMISSION_DENIED", "Core definitions cannot be changed")
			}
			if req.Action == "alias" && (d.ValueType != old.ValueType || d.Cardinality != old.Cardinality || d.Unit != old.Unit) {
				return out, model.E("SCHEMA_CONFLICT", "Alias definition conflicts with its target")
			}
			if req.Action == "alias" {
				e = w.name(id, d.Ident, false)
			} else {
				domains := append([]string{}, d.AppliesTo...)
				ranges := append([]string{}, d.ReferenceTypes...)
				for i := range domains {
					domains[i], e = resolve(ctx, w.tx, w.space, domains[i])
					if e != nil {
						return out, e
					}
				}
				for i := range ranges {
					ranges[i], e = resolve(ctx, w.tx, w.space, ranges[i])
					if e != nil {
						return out, e
					}
				}
				slices.Sort(domains)
				slices.Sort(ranges)
				if old.ValueType != d.ValueType || old.Cardinality != d.Cardinality || old.Unit != d.Unit || !slices.Equal(domains, old.AppliesTo) || !slices.Equal(ranges, old.ReferenceTypes) {
					return out, model.E("SCHEMA_CONFLICT", "Type, cardinality, unit, domain and range are immutable")
				}
				if d.Ident != old.Ident {
					if e = w.name(id, d.Ident, true); e != nil {
						return out, e
					}
				}
				replacement := any(nil)
				if d.Replacement != "" {
					replacement, e = resolve(ctx, w.tx, w.space, d.Replacement)
					if e != nil {
						return out, e
					}
					if replacement == id {
						return out, model.Invalid("replacement")
					}
				}
				status := "active"
				if req.Action == "deprecate" || d.Status == "deprecated" {
					status = "deprecated"
				}
				ex, _ := json.Marshal(nonNil(d.Examples))
				nt, _ := json.Marshal(nonNil(d.NotThis))
				_, e = w.tx.Exec(ctx, `UPDATE memory.attribute_defs SET description=$3,status=$4,replacement=$5,examples=$6,not_this=$7,schema_revision=$8 WHERE space_id=$1 AND id=$2`, w.space, id, d.Description, status, replacement, ex, nt, w.schema)
			}
			if e != nil {
				return out, safe(e)
			}
		default:
			return out, model.Invalid("schema action")
		}
		for _, alias := range d.Aliases {
			if e = w.name(id, alias, false); e != nil {
				return out, e
			}
		}
		d.ID = id
		d.SchemaRevision = model.Revision(w.schema)
		if d.Kind == "attribute" {
			if _, e = w.tx.Exec(ctx, `UPDATE memory.attribute_defs SET schema_revision=$3 WHERE space_id=$1 AND id=$2`, w.space, id, w.schema); e != nil {
				return out, safe(e)
			}
			d, e = definition(ctx, w.tx, w.space, id)
			if e != nil {
				return out, e
			}
		} else {
			if _, e = w.tx.Exec(ctx, `UPDATE memory.type_defs SET schema_revision=$3 WHERE space_id=$1 AND id=$2`, w.space, id, w.schema); e != nil {
				return out, safe(e)
			}
			if e = w.tx.QueryRow(ctx, `SELECT n.name,t.description,ARRAY(SELECT name FROM memory.entity_names a WHERE a.space_id=t.space_id AND a.entity_id=t.id AND NOT is_canonical ORDER BY name) FROM memory.type_defs t JOIN memory.entity_names n ON n.space_id=t.space_id AND n.entity_id=t.id AND n.is_canonical WHERE t.space_id=$1 AND t.id=$2`, w.space, id).Scan(&d.Ident, &d.Description, &d.Aliases); e != nil {
				return out, safe(e)
			}
			d.Status = "active"
		}
		if e = w.event("schema_events", req.Action, id, d); e != nil {
			return out, e
		}
	}
	_, e = w.tx.Exec(ctx, `UPDATE memory.schema_proposals SET status=$3,rationale=$4 WHERE space_id=$1 AND id::text=$2`, w.space, req.Proposal, newStatus, req.Rationale)
	if e != nil {
		return out, safe(e)
	}
	out = model.Commit{Revision: model.Revision(w.rev), SchemaRevision: model.Revision(w.schema), TempIDs: map[string]string{"schema": id}, Applied: 1, Warnings: []string{}}
	return out, w.finish("", "", out)
}
func nonNil[T any](x []T) []T {
	if x == nil {
		return []T{}
	}
	return x
}
