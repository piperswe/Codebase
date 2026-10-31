package postgres

import (
	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/migrations"
	"context"
	"encoding/json"
	"github.com/google/uuid"
)

// Bootstrap is operator-only. It never runs from serve or a worker callback.
func (s *Store) Bootstrap(ctx context.Context, slug string) error {
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("codebase.bid/memory/spaces/"+slug)).String()
	tx, e := s.App.Begin(ctx)
	if e != nil {
		return safe(e)
	}
	defer tx.Rollback(context.Background())
	_, e = tx.Exec(ctx, `INSERT INTO memory.spaces(id,slug) VALUES($1,$2) ON CONFLICT(slug) DO NOTHING`, id, slug)
	if e != nil {
		return safe(e)
	}
	var rev int64
	if e = tx.QueryRow(ctx, `SELECT id::text,schema_revision FROM memory.spaces WHERE slug=$1 FOR UPDATE`, slug).Scan(&id, &rev); e != nil {
		return safe(e)
	}
	if rev > 0 {
		return tx.Commit(ctx)
	}
	var defs []model.Definition
	b, _ := migrations.Files.ReadFile("bootstrap.json")
	if e = json.Unmarshal(b, &defs); e != nil {
		return e
	}
	for i, d := range defs {
		eid := model.StableID(id, d.Ident)
		_, e = tx.Exec(ctx, `INSERT INTO memory.entities(space_id,id,kind,created_revision,revision) VALUES($1,$2,'attribute',1,1);`, id, eid)
		if e != nil {
			return safe(e)
		}
		_, e = tx.Exec(ctx, `INSERT INTO memory.entity_names(space_id,entity_id,name,is_canonical) VALUES($1,$2,$3,true)`, id, eid, d.Ident)
		if e != nil {
			return safe(e)
		}
		_, e = tx.Exec(ctx, `INSERT INTO memory.attribute_defs(space_id,id,description,value_type,cardinality,protected,schema_revision) VALUES($1,$2,$3,$4,$5,$6,1)`, id, eid, d.Description, d.ValueType, d.Cardinality, d.Protected)
		if e != nil {
			return safe(e)
		}
		payload, _ := json.Marshal(d)
		_, e = tx.Exec(ctx, `INSERT INTO memory.schema_events(space_id,revision,ordinal,entity_id,kind,payload) VALUES($1,1,$2,$3,'bootstrap',$4)`, id, i, eid, payload)
		if e != nil {
			return safe(e)
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO memory.transactions(space_id,revision,actor,client,request_id,reason) VALUES($1,1,'system','bootstrap',$2,'Initialize reserved meta-schema')`, id, model.NewID())
	if e != nil {
		return safe(e)
	}
	_, e = tx.Exec(ctx, `UPDATE memory.spaces SET revision=1,schema_revision=1 WHERE id=$1`, id)
	if e != nil {
		return safe(e)
	}
	if e = refreshSearch(ctx, tx, id, nil); e != nil {
		return safe(e)
	}
	return safe(tx.Commit(ctx))
}
