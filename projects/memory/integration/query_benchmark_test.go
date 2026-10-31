package integration

import (
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"codebase.bid/projects/memory/internal/model"
)

// Run explicitly with MEMORY_RUN_BENCHMARK=1. These are measurements on the
// selected execution machine, never a claim about the EliteDesk deployment.
func TestQueryBenchmark(t *testing.T) {
	if os.Getenv("MEMORY_RUN_BENCHMARK") != "1" {
		t.Skip("opt-in 10,000 entity / 100,000 fact performance fixture")
	}
	store, db := database(t)
	ctx := t.Context()
	attr := definition(t, store, model.Definition{Ident: "bench/value", Kind: "attribute", Description: "Synthetic benchmark exact number", ValueType: model.Integer, Cardinality: "many"})
	ref := definition(t, store, model.Definition{Ident: "bench/link", Kind: "attribute", Description: "Synthetic benchmark directed link", ValueType: model.Ref, Cardinality: "many"})
	source := model.NewID()
	var space string
	var rev int64
	if e := db.QueryRow(ctx, `SELECT id::text,revision+1 FROM memory.spaces WHERE slug='personal'`).Scan(&space, &rev); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `INSERT INTO memory.transactions(space_id,revision,actor,client,request_id,reason) VALUES($1,$2,'benchmark','benchmark',$3,'Synthetic benchmark fixture')`, space, rev, model.NewID()); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memory.entities(space_id,id,kind,created_revision,revision) SELECT $1,gen_random_uuid(),'ordinary',$2,$2 FROM generate_series(1,10000)`, space, rev); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memory.entity_names(space_id,entity_id,name,is_canonical) SELECT space_id,id,'bench/entity-'||row_number() OVER(ORDER BY id),true FROM memory.entities WHERE space_id=$1 AND kind='ordinary'`, space); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memory.current_facts(space_id,id,entity_id,attribute_id,value_type,cardinality,value_integer,value_fingerprint,assertion_revision) SELECT $1,gen_random_uuid(),e.id,$2,'integer','many',v,sha256(convert_to('memory-value-v1','UTF8')||decode('00','hex')||convert_to('{"type":"integer","value":"'||v||'"}','UTF8')),$3 FROM memory.entities e CROSS JOIN generate_series(1,9) v WHERE e.space_id=$1 AND e.kind='ordinary'`, space, attr, rev); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memory.entities(space_id,id,kind,created_revision,revision) VALUES($1,$2,'source',$3,$3)`, space, source, rev); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memory.sources(space_id,id,origin,description) VALUES($1,$2,'system','Synthetic benchmark generator')`, space, source); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `WITH targets AS(SELECT id,coalesce(lead(id) OVER(ORDER BY id),first_value(id) OVER(ORDER BY id)) target FROM memory.entities WHERE space_id=$1 AND kind='ordinary') INSERT INTO memory.current_facts(space_id,id,entity_id,attribute_id,value_type,cardinality,value_ref,value_fingerprint,assertion_revision) SELECT $1,gen_random_uuid(),id,$2,'ref','many',target,sha256(convert_to('memory-value-v1','UTF8')||decode('00','hex')||convert_to('{"type":"ref","value":"'||target||'"}','UTF8')),$3 FROM targets`, space, ref, rev); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memory.fact_evidence(space_id,fact_id,source_id) SELECT space_id,id,$2 FROM memory.current_facts WHERE space_id=$1`, space, source); e != nil {
		t.Fatal(e)
	}
	// History size is independent of current facts and reported explicitly.
	if _, e = tx.Exec(ctx, `INSERT INTO memory.fact_events(space_id,revision,ordinal,entity_id,kind,payload) SELECT space_id,$2,row_number() OVER(ORDER BY id),entity_id,'assert',jsonb_build_object('id',id,'attribute',attribute_id,'value',jsonb_build_object('type',value_type,'value',coalesce(value_integer::text,value_ref::text)),'evidence',jsonb_build_array(jsonb_build_object('source',$3::text,'confidence',NULL))) FROM memory.current_facts WHERE space_id=$1`, space, rev, source); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE memory.spaces SET revision=$2 WHERE id=$1`, space, rev); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e = store.Reindex(ctx, owner(), "personal"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, `ANALYZE memory.current_facts; ANALYZE memory.entities; ANALYZE memory.entity_names; ANALYZE memory.entity_search`); e != nil {
		t.Fatal(e)
	}
	var first string
	var history int64
	if e = db.QueryRow(ctx, `SELECT entity_id::text FROM memory.entity_names WHERE space_id=$1 AND name='bench/entity-5000'`, space).Scan(&first); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(ctx, `SELECT count(*) FROM memory.fact_events WHERE space_id=$1`, space).Scan(&history); e != nil {
		t.Fatal(e)
	}
	measure := func(name string, fn func() error) {
		times := []time.Duration{}
		for i := 0; i < 120; i++ {
			start := time.Now()
			if e := fn(); e != nil {
				t.Fatal(name, e)
			}
			if i >= 20 {
				times = append(times, time.Since(start))
			}
		}
		slices.Sort(times)
		t.Logf("%s warm p50=%s p95=%s samples=%d", name, times[len(times)/2], times[len(times)*95/100], len(times))
	}
	measure("exact entity", func() error {
		r, e := store.Read(ctx, owner(), "personal")
		if e != nil {
			return e
		}
		defer r.Close()
		_, e = r.Get([]string{first}, nil)
		return e
	})
	measure("indexed discovery", func() error {
		r, e := store.Read(ctx, owner(), "personal")
		if e != nil {
			return e
		}
		defer r.Close()
		_, e = r.Search(model.Search{Query: "bench/entity-5000"})
		return e
	})
	for name, sql := range map[string]string{
		"entity":  `SELECT * FROM memory.current_facts WHERE space_id=$1 AND entity_id=$2`,
		"reverse": `SELECT entity_id FROM memory.current_facts WHERE space_id=$1 AND attribute_id=$3 AND value_ref=$2`,
		"numeric": `SELECT entity_id FROM memory.current_facts WHERE space_id=$1 AND attribute_id=$2 AND value_integer>=9 LIMIT 50`,
		"text":    `SELECT entity_id FROM memory.entity_search WHERE space_id=$1 AND tsv @@ plainto_tsquery('simple','bench/entity-5000')`,
	} {
		args := []any{space, first, ref}
		if name == "numeric" {
			args = []any{space, attr}
		}
		if name == "entity" {
			args = args[:2]
		}
		if name == "text" {
			args = args[:1]
		}
		rows, e := db.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+sql, args...)
		if e != nil {
			t.Fatal(e)
		}
		plan := []string{}
		for rows.Next() {
			var line string
			if e = rows.Scan(&line); e != nil {
				t.Fatal(e)
			}
			plan = append(plan, line)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
		t.Log(name + " plan:\n" + strings.Join(plan, "\n"))
	}
	t.Logf("fixture: entities=10000 currentFacts=100000 factHistory=%d concurrency=1 os=%s arch=%s logicalCPUs=%d", history, runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}
