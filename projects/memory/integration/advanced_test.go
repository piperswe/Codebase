package integration

import (
	"bytes"

	"encoding/json"
	"strings"
	"testing"

	"codebase.bid/projects/memory/internal/model"
)

func TestStorageAdvanced(t *testing.T) {
	s, admin := database(t)
	ctx := t.Context()
	a := definition(t, s, model.Definition{Ident: "type/alpha", Kind: "type", Description: "Synthetic alpha"})
	b := definition(t, s, model.Definition{Ident: "type/beta", Kind: "type", Description: "Synthetic beta"})
	attrs := []model.Definition{{Ident: "test/flag", ValueType: model.Boolean, Cardinality: "one"}, {Ident: "test/text", ValueType: model.String, Cardinality: "one"}, {Ident: "test/numbers", ValueType: model.Integer, Cardinality: "many"}, {Ident: "test/measure", ValueType: model.Decimal, Cardinality: "one", Unit: "m"}, {Ident: "test/time", ValueType: model.Timestamp, Cardinality: "one"}, {Ident: "test/link", ValueType: model.Ref, Cardinality: "many", AppliesTo: []string{a}, ReferenceTypes: []string{b}}}
	ids := map[string]string{}
	for _, d := range attrs {
		d.Kind = "attribute"
		d.Description = "Synthetic " + d.Ident
		ids[d.Ident] = definition(t, s, d)
	}
	r := current(t, s)
	schema := model.Revision(r.SchemaRevision)
	r.Close()
	ops := []model.Operation{{Op: "createSource", TempID: "source", Source: &model.Source{Origin: "document", Description: "Fictional test excerpt; never fetch this URL", URI: "https://example.invalid/fixture"}}, {Op: "createEntity", TempID: "alpha", Types: []string{a}}, {Op: "createEntity", TempID: "beta", Types: []string{b}}}
	values := map[string]model.Value{"test/flag": {Type: model.Boolean, Bool: false}, "test/text": {Type: model.String, Text: ""}, "test/numbers": {Type: model.Integer, Text: "0"}, "test/measure": {Type: model.Decimal, Text: "1.00"}, "test/time": {Type: model.Timestamp, Text: "2026-01-01T01:00:00+01:00"}, "test/link": {Type: model.Ref, Text: "temp:beta"}}
	for attr, value := range values {
		value := value
		op := model.Operation{Op: "assert", Entity: model.EntityRef{TempID: "alpha"}, Attribute: attr, Value: &value, Evidence: []model.Evidence{{Source: "temp:source"}}}
		if attr == "test/measure" {
			op.Unit = "m"
		}
		ops = append(ops, op)
	}
	// Exercise the wire decoder, including forward temporary reference values.
	var req model.Transaction
	wire, _ := json.Marshal(model.Transaction{Space: "personal", IdempotencyKey: "advanced-0001", ExpectedSchemaRevision: schema, Reason: "Synthetic advanced fixture", Operations: ops})
	if e := model.Decode(wire, &req); e != nil {
		t.Fatal(e)
	}
	commit, e := s.Transact(ctx, owner(), req)
	if e != nil {
		t.Fatal(e)
	}
	entity, source := commit.TempIDs["alpha"], commit.TempIDs["source"]
	r = current(t, s)
	got, e := r.Get([]string{entity}, nil)
	if e != nil {
		t.Fatal(e)
	}
	facts := map[string]model.Fact{}
	for _, f := range got[0].Facts {
		facts[f.Attribute] = f
	}
	for name, id := range ids {
		f, ok := facts[id]
		if !ok {
			t.Fatal("absent/false/zero/empty conflated", name)
		}
		if len(f.Evidence) != 1 || f.Evidence[0].Confidence != nil {
			t.Fatal("unknown confidence fabricated")
		}
	}
	r.Close()
	transact := func(key string, operations []model.Operation) (model.Commit, error) {
		return s.Transact(ctx, owner(), model.Transaction{Space: "personal", IdempotencyKey: key, ExpectedSchemaRevision: schema, Reason: "Synthetic invariant test", Operations: operations})
	}
	t.Run("set-and-evidence-noop", func(t *testing.T) {
		v := model.Value{Type: model.Integer, Text: "0"}
		out, e := transact("advanced-duplicate", []model.Operation{{Op: "assert", Entity: model.EntityRef{ID: entity}, Attribute: "test/numbers", Value: &v, Evidence: []model.Evidence{{Source: source}}}})
		if e != nil || out.Noop != 1 {
			t.Fatal(out, e)
		}
		zero := 0.0
		op := model.Operation{Op: "addEvidence", Entity: model.EntityRef{ID: entity}, Attribute: "test/numbers", ExpectedFact: facts[ids["test/numbers"]].ID, Evidence: []model.Evidence{{Source: source, Confidence: &zero, Method: "synthetic"}}}
		out, e = transact("advanced-evidence", []model.Operation{op})
		if e != nil || out.Applied != 1 {
			t.Fatal(out, e)
		}
		out, e = transact("advanced-evidence-again", []model.Operation{op})
		if e != nil || out.Noop != 1 {
			t.Fatal(out, e)
		}
	})
	t.Run("unit-and-range", func(t *testing.T) {
		v := model.Value{Type: model.Decimal, Text: "2"}
		_, e := transact("advanced-unit", []model.Operation{{Op: "replace", Entity: model.EntityRef{ID: entity}, Attribute: "test/measure", ExpectedFact: facts[ids["test/measure"]].ID, Value: &v, Unit: "feet", Evidence: []model.Evidence{{Source: source}}}})
		code(t, e, "INVALID_VALUE_TYPE")
		ref := model.Value{Type: model.Ref, Text: entity}
		_, e = transact("advanced-range", []model.Operation{{Op: "assert", Entity: model.EntityRef{ID: entity}, Attribute: "test/link", Value: &ref, Evidence: []model.Evidence{{Source: source}}}})
		code(t, e, "REFERENCE_TYPE_MISMATCH")
	})
	t.Run("type-removal-final-state", func(t *testing.T) {
		r := current(t, s)
		v, e := r.Get([]string{entity}, []string{"entity/type"})
		if e != nil {
			t.Fatal(e)
		}
		r.Close()
		_, e = transact("advanced-remove-type", []model.Operation{{Op: "retract", Entity: model.EntityRef{ID: entity}, Attribute: "entity/type", ExpectedFact: v[0].Facts[0].ID}})
		code(t, e, "DOMAIN_VIOLATION")
	})
	t.Run("protected-and-unknown-schema", func(t *testing.T) {
		v := model.Value{Type: model.String, Text: "forged"}
		_, e := transact("advanced-protected", []model.Operation{{Op: "assert", Entity: model.EntityRef{ID: entity}, Attribute: "schema/value-type", Value: &v, Evidence: []model.Evidence{{Source: source}}}})
		code(t, e, "PERMISSION_DENIED")
		_, e = transact("advanced-unknown", []model.Operation{{Op: "assert", Entity: model.EntityRef{ID: entity}, Attribute: "test/undeclared", Value: &v, Evidence: []model.Evidence{{Source: source}}}})
		code(t, e, "ATTRIBUTE_NOT_FOUND")
		if !strings.Contains(e.Error(), "schema candidates") {
			t.Fatal(e)
		}
	})
	t.Run("fingerprint-collision", func(t *testing.T) {
		original := s.Fingerprint
		s.Fingerprint = func(model.Value) []byte { return bytes.Repeat([]byte{1}, 32) }
		defer func() { s.Fingerprint = original }()
		v := model.Value{Type: model.Integer, Text: "11"}
		_, e := transact("advanced-collision-first", []model.Operation{{Op: "assert", Entity: model.EntityRef{ID: entity}, Attribute: "test/numbers", Value: &v, Evidence: []model.Evidence{{Source: source}}}})
		code(t, e, "VALUE_FINGERPRINT_COLLISION")
	})
	t.Run("scalar-pages-and-schema-edges", func(t *testing.T) {
		for _, n := range []string{"1", "2", "3"} {
			v := model.Value{Type: model.Integer, Text: n}
			if _, e := transact("advanced-number-"+n, []model.Operation{{Op: "assert", Entity: model.EntityRef{ID: entity}, Attribute: "test/numbers", Value: &v, Evidence: []model.Evidence{{Source: source}}}}); e != nil {
				t.Fatal(e)
			}
		}
		r := current(t, s)
		first, e := r.Values(entity, "test/numbers", model.PageOptions{Limit: 2})
		if e != nil || first.Complete || first.NextCursor == nil {
			t.Fatal(first, e)
		}
		second, e := r.Values(entity, "test/numbers", model.PageOptions{Limit: 2, Cursor: *first.NextCursor})
		if e != nil || !second.Complete || len(second.Items) != 2 {
			t.Fatal(second, e)
		}
		incoming, e := r.References(a, "schema/applies-to", "in", model.PageOptions{})
		if e != nil || len(incoming.Items) != 1 || incoming.Items[0].ID != ids["test/link"] {
			t.Fatal(incoming, e)
		}
	})
	t.Run("direct-sql-invariants", func(t *testing.T) {
		var space string
		admin.QueryRow(ctx, `SELECT id::text FROM memory.spaces WHERE slug='personal'`).Scan(&space)
		_, e := admin.Exec(ctx, `INSERT INTO memory.type_defs(space_id,id,description,schema_revision) VALUES($1,$2,'wrong kind',1)`, space, entity)
		if e == nil {
			t.Fatal("kind FK absent")
		}
		_, e = admin.Exec(ctx, `UPDATE memory.attribute_defs SET value_type='string' WHERE space_id=$1 AND id=$2`, space, ids["test/numbers"])
		if e == nil {
			t.Fatal("immutable validation type changed")
		}
		_, e = admin.Exec(ctx, `UPDATE memory.entities SET kind='source' WHERE space_id=$1 AND id=$2`, space, entity)
		if e == nil {
			t.Fatal("kind changed")
		}
		tx, e := admin.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		sid := model.NewID()
		_, e = tx.Exec(ctx, `INSERT INTO memory.entities(space_id,id,kind,created_revision,revision) VALUES($1,$2,'attribute',1,1)`, space, sid)
		if e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e == nil {
			t.Fatal("unnamed schema entity committed")
		}
	})
}
