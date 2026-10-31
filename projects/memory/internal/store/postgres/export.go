package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"codebase.bid/projects/memory/internal/model"
	"github.com/jackc/pgx/v5"
)

var exportTables = []string{"entities", "entity_names", "type_defs", "attribute_defs", "attribute_domains", "attribute_ranges", "sources", "transactions", "current_facts", "fact_evidence", "fact_events", "entity_events", "schema_events", "schema_proposals", "entity_search"}

type graphExport struct {
	Version int                        `json:"version"`
	Space   json.RawMessage            `json:"space"`
	Tables  map[string]json.RawMessage `json:"tables"`
}

// Export includes protected projections only as authoritative registry rows. The
// derived read_facts view and all memory_auth state are deliberately excluded.
func (s *Store) Export(ctx context.Context, p model.Principal, slug string, w io.Writer) error {
	r, e := s.Read(ctx, p, slug)
	if e != nil {
		return e
	}
	defer r.Close()
	// The operator's export uses an application-role read-only snapshot because
	// it must preserve idempotency records that reader-role scripts cannot see.
	tx, e := s.App.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return safe(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='30s'`); e != nil {
		return safe(e)
	}
	out := graphExport{Version: 1, Tables: map[string]json.RawMessage{}}
	if e = tx.QueryRow(ctx, `SELECT row_to_json(s) FROM memory.spaces s WHERE id=$1`, r.Space).Scan(&out.Space); e != nil {
		return safe(e)
	}
	for _, table := range exportTables {
		var raw []byte
		e = tx.QueryRow(ctx, `SELECT coalesce(json_agg(t),'[]') FROM memory.`+table+` t WHERE space_id=$1`, r.Space).Scan(&raw)
		if e != nil {
			return safe(e)
		}
		out.Tables[table] = raw
	}
	return json.NewEncoder(w).Encode(out)
}
func (s *Store) Import(ctx context.Context, slug string, in io.Reader) error {
	b, e := io.ReadAll(io.LimitReader(in, 256<<20+1))
	if e != nil {
		return e
	}
	if len(b) > 256<<20 {
		return model.Limit("Import exceeds 256 MiB")
	}
	var data graphExport
	if e = model.Decode(b, &data); e != nil {
		return e
	}
	if data.Version != 1 || len(data.Tables) != len(exportTables) {
		return model.Invalid("export version/tables")
	}
	for name := range data.Tables {
		found := false
		for _, table := range exportTables {
			found = found || name == table
		}
		if !found {
			return model.Invalid("export table")
		}
	}
	var space struct {
		ID             string `json:"id"`
		Slug           string `json:"slug"`
		Revision       int64  `json:"revision"`
		SchemaRevision int64  `json:"schema_revision"`
	}
	if e = model.Decode(data.Space, &space); e != nil {
		return e
	}
	if space.Slug != slug {
		return model.Invalid("export space slug")
	}
	tx, e := s.App.Begin(ctx)
	if e != nil {
		return safe(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='30s'`); e != nil {
		return safe(e)
	}
	_, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,611))`, slug)
	if e != nil {
		return safe(e)
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM memory.spaces WHERE slug=$1 OR id::text=$2)`, slug, space.ID).Scan(&exists); e != nil {
		return safe(e)
	}
	if exists {
		return model.E("VERSION_CONFLICT", "Restore requires an empty target space; existing spaces are never overwritten")
	}
	if _, e = tx.Exec(ctx, `SET CONSTRAINTS ALL DEFERRED`); e != nil {
		return safe(e)
	}
	_, e = tx.Exec(ctx, `INSERT INTO memory.spaces SELECT * FROM json_populate_record(NULL::memory.spaces,$1::json)`, []byte(data.Space))
	if e != nil {
		return safe(e)
	}
	for _, table := range exportTables {
		var wrong bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT FROM json_array_elements($1::json) v WHERE v->>'space_id' IS DISTINCT FROM $2)`, []byte(data.Tables[table]), space.ID).Scan(&wrong)
		if e != nil || wrong {
			return model.Invalid("export space boundary")
		}
		_, e = tx.Exec(ctx, `INSERT INTO memory.`+table+` SELECT * FROM json_populate_recordset(NULL::memory.`+table+`,$1::json)`, []byte(data.Tables[table]))
		if e != nil {
			return fmt.Errorf("restore %s: %w", table, safe(e))
		}
	}
	// Stored protected attributes cannot appear in an export. The insertion
	// trigger enforces that, and final validation rechecks membership integrity.
	w := &write{ctx: ctx, tx: tx, space: space.ID}
	if e = w.validateFinal(); e != nil {
		return e
	}
	return safe(tx.Commit(ctx))
}
