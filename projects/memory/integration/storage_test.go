package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func database(t *testing.T) (*postgres.Store, *pgx.Conn) {
	t.Helper()
	ctx := t.Context()
	c, e := pgcontainer.Run(ctx, "postgres:17@sha256:31ba9c94a03ada9988f2bb493a22a75eef3a185884533e5cdbcd498ea835b08f", pgcontainer.WithDatabase("memorytest"), pgcontainer.WithUsername("postgres"), pgcontainer.WithPassword("test-only"), testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(30*time.Second)))
	if e != nil {
		t.Fatal(e)
	}
	testcontainers.CleanupContainer(t, c)
	dsn, e := c.ConnectionString(ctx, "sslmode=disable")
	if e != nil {
		t.Fatal(e)
	}
	conn, e := pgx.Connect(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	if _, e = postgres.Migrate(ctx, dsn, "roles"); e != nil {
		t.Fatal("roles:", e)
	}
	if _, e = postgres.Migrate(ctx, dsn, "up"); e != nil {
		t.Fatal("migrate:", e)
	}
	if n, e := postgres.Migrate(ctx, dsn, "up"); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	_, e = conn.Exec(ctx, `ALTER ROLE memory_app PASSWORD 'test-only'; ALTER ROLE memory_reader PASSWORD 'test-only'`)
	if e != nil {
		t.Fatal(e)
	}
	roleDSN := func(role string) string {
		u, e := url.Parse(dsn)
		if e != nil {
			t.Fatal(e)
		}
		u.User = url.UserPassword(role, "test-only")
		return u.String()
	}
	s, e := postgres.Open(ctx, roleDSN("memory_app"), roleDSN("memory_reader"), []byte(strings.Repeat("k", 32)))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	for _, slug := range []string{"personal", "other"} {
		if e = s.Bootstrap(ctx, slug); e != nil {
			t.Fatal("bootstrap", e)
		}
		if e = s.Bootstrap(ctx, slug); e != nil {
			t.Fatal(e)
		}
	}
	return s, conn
}
func owner() model.Principal {
	return model.Principal{Actor: "owner", Client: "test", Scopes: []string{"memory:read", "memory:write", "memory:schema:write", "memory:execute"}, Spaces: []string{"personal", "other"}}
}
func definition(t *testing.T, s *postgres.Store, d model.Definition) string {
	t.Helper()
	p, e := s.Propose(t.Context(), owner(), model.Proposal{Space: "personal", Definition: d})
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.Apply(t.Context(), owner(), model.Apply{Space: "personal", Proposal: p.ID, ExpectedSchemaRevision: p.SchemaRevision, Action: "register", Rationale: "Synthetic test schema reviewed"})
	if e != nil {
		t.Fatal(e)
	}
	return out.TempIDs["schema"]
}
func current(t *testing.T, s *postgres.Store) *postgres.Session {
	t.Helper()
	r, e := s.Read(t.Context(), owner(), "personal")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(r.Close)
	return r
}
func code(t *testing.T, e error, want string) {
	t.Helper()
	var m *model.Error
	if !errors.As(e, &m) || m.Code != want {
		t.Fatalf("want %s, got %v", want, e)
	}
}
func TestStorage(t *testing.T) {
	s, admin := database(t)
	ctx := t.Context()
	typ := definition(t, s, model.Definition{Ident: "type/device", Kind: "type", Description: "Fictional test device"})
	attr := definition(t, s, model.Definition{Ident: "device/year", Kind: "attribute", Description: "Year of test device", ValueType: model.Integer, Cardinality: "one", AppliesTo: []string{typ}, Aliases: []string{"device/model-year"}})
	ref := definition(t, s, model.Definition{Ident: "device/link", Kind: "attribute", Description: "Synthetic connection", ValueType: model.Ref, Cardinality: "many", ReferenceTypes: []string{typ}})
	r := current(t, s)
	schema := model.Revision(r.SchemaRevision)
	r.Close()
	v := model.Value{Type: model.Integer, Text: "2024"}
	req := model.Transaction{Space: "personal", IdempotencyKey: "create-0001", ExpectedSchemaRevision: schema, Reason: "Create synthetic graph", Operations: []model.Operation{
		{Op: "createSource", TempID: "source", Ident: "source/test", Source: &model.Source{Origin: "user", Description: "Synthetic message"}},
		{Op: "createEntity", TempID: "device", Ident: "device/test", Types: []string{typ}},
		{Op: "createEntity", TempID: "second", Types: []string{typ}},
		{Op: "assert", Entity: model.EntityRef{TempID: "device"}, Attribute: "device/model-year", Value: &v, Evidence: []model.Evidence{{Source: "temp:source"}}},
	}}
	out, e := s.Transact(ctx, owner(), req)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Transact(ctx, owner(), req)
	if e != nil || model.Hash(again) != model.Hash(out) {
		t.Fatal("replay", again, e)
	}
	req.Reason = "different"
	_, e = s.Transact(ctx, owner(), req)
	code(t, e, "IDEMPOTENCY_CONFLICT")
	id := out.TempIDs["device"]
	source := out.TempIDs["source"]
	t.Run("typed-and-virtual-reads", func(t *testing.T) {
		r := current(t, s)
		got, e := r.Get([]string{id, "device/test", attr}, nil)
		if e != nil {
			t.Fatal(e)
		}
		if model.Hash(got[0]) != model.Hash(got[1]) {
			t.Fatal("ID and name differ")
		}
		found := false
		for _, f := range got[2].Facts {
			if f.Virtual && f.Value.Text == "integer" {
				found = true
			}
		}
		if !found {
			t.Fatal("schema metadata missing")
		}
		p, e := r.Find(model.Find{Where: model.Predicate{Attribute: "schema/value-type", Op: "eq", Value: &model.Value{Type: model.String, Text: "integer"}}})
		if e != nil || len(p.Items) < 1 {
			t.Fatal(p, e)
		}
		h, e := r.History(model.History{Entity: id})
		if e != nil || len(h.Items) == 0 {
			t.Fatal(h, e)
		}
	})
	t.Run("atomic-late-error", func(t *testing.T) {
		_, e := s.Transact(ctx, owner(), model.Transaction{Space: "personal", IdempotencyKey: "rollback-001", ExpectedSchemaRevision: schema, Reason: "Must roll back", Operations: []model.Operation{{Op: "createEntity", TempID: "new", Ident: "device/rollback"}, {Op: "assert", Entity: model.EntityRef{TempID: "new"}, Attribute: "unknown/name", Value: &v, Evidence: []model.Evidence{{Source: source}}}}})
		code(t, e, "ATTRIBUTE_NOT_FOUND")
		r := current(t, s)
		_, e = r.Get([]string{"device/rollback"}, nil)
		code(t, e, "ENTITY_NOT_FOUND")
	})
	t.Run("concurrent-correction-snapshot", func(t *testing.T) {
		snapshot := current(t, s)
		before, e := snapshot.Get([]string{id}, nil)
		if e != nil {
			t.Fatal(e)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				v := model.Value{Type: model.Integer, Text: []string{"2025", "2026"}[i]}
				_, e := s.Transact(ctx, owner(), model.Transaction{Space: "personal", IdempotencyKey: []string{"correct-001", "correct-002"}[i], ExpectedSchemaRevision: schema, Reason: "Correct test fact", Operations: []model.Operation{{Op: "replace", Entity: model.EntityRef{ID: id}, ExpectedRevision: out.Revision, Attribute: attr, Value: &v, Evidence: []model.Evidence{{Source: source}}}}})
				errs <- e
			}(i)
		}
		wg.Wait()
		close(errs)
		success, conflict := 0, 0
		for e := range errs {
			if e == nil {
				success++
			} else {
				code(t, e, "VERSION_CONFLICT")
				conflict++
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal(success, conflict)
		}
		after, e := snapshot.Get([]string{id}, nil)
		if e != nil || model.Hash(before) != model.Hash(after) {
			t.Fatal("snapshot changed", e)
		}
		snapshot.Close()
		r := current(t, s)
		h, e := r.History(model.History{Entity: id})
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, v := range h.Items {
			if v.Kind == "retract" {
				var f model.Fact
				if json.Unmarshal(v.Payload, &f) != nil || len(f.Evidence) == 0 || f.Value.Text != "2024" {
					t.Fatal("history lost evidence")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("missing correction event")
		}
	})
	t.Run("cross-space", func(t *testing.T) {
		p := owner()
		p.Spaces = []string{"other"}
		r, e := s.Read(ctx, p, "other")
		if e != nil {
			t.Fatal(e)
		}
		defer r.Close()
		_, e = r.Get([]string{id}, nil)
		code(t, e, "ENTITY_NOT_FOUND")
		_, e = s.Read(ctx, p, "personal")
		code(t, e, "PERMISSION_DENIED")
	})
	t.Run("cursor-staleness", func(t *testing.T) {
		r := current(t, s)
		page, e := r.Search(model.Search{PageOptions: model.PageOptions{Limit: 1}})
		if e != nil || page.NextCursor == nil {
			t.Fatal(page, e)
		}
		r.Close()
		_, e = s.Transact(ctx, owner(), model.Transaction{Space: "personal", IdempotencyKey: "cursor-change", ExpectedSchemaRevision: schema, Reason: "Change revision", Operations: []model.Operation{{Op: "createEntity", TempID: "new"}}})
		if e != nil {
			t.Fatal(e)
		}
		r = current(t, s)
		_, e = r.Search(model.Search{PageOptions: model.PageOptions{Limit: 1, Cursor: *page.NextCursor}})
		code(t, e, "CURSOR_STALE")
	})
	t.Run("pull-cycle-incoming", func(t *testing.T) {
		a := out.TempIDs["device"]
		b := out.TempIDs["second"]
		ops := []model.Operation{}
		for _, pair := range [][2]string{{a, b}, {b, a}} {
			v := model.Value{Type: model.Ref, Text: pair[1]}
			ops = append(ops, model.Operation{Op: "assert", Entity: model.EntityRef{ID: pair[0]}, Attribute: ref, Value: &v, Evidence: []model.Evidence{{Source: source}}})
		}
		_, e := s.Transact(ctx, owner(), model.Transaction{Space: "personal", IdempotencyKey: "cycle-00001", ExpectedSchemaRevision: schema, Reason: "Test cyclic graph", Operations: ops})
		if e != nil {
			t.Fatal(e)
		}
		r := current(t, s)
		raw := json.RawMessage(`{"select":{"device/link":{"select":{"entity/kind":true}}}}`)
		pull, e := r.Pull(a, model.Pattern{Select: map[string]json.RawMessage{"device/link": raw}, Incoming: []model.Incoming{{Attribute: ref, Select: map[string]json.RawMessage{"entity/kind": json.RawMessage(`true`)}}}})
		if e != nil {
			t.Fatal(e)
		}
		if !pull.Edges[ref].Items[0].Edges[ref].Items[0].Stub {
			t.Fatal("cycle did not become stub")
		}
		if len(pull.Edges["incoming:"+ref].Items) != 1 {
			t.Fatal("incoming missing")
		}
	})
	t.Run("database-privileges", func(t *testing.T) {
		_, e := s.Reader.Exec(ctx, `CREATE TABLE memory.illegal(id int)`)
		if e == nil {
			t.Fatal("reader DDL allowed")
		}
		_, e = s.Reader.Exec(ctx, `SELECT * FROM memory_auth.refresh`)
		if e == nil {
			t.Fatal("reader auth access")
		}
		_, e = s.App.Exec(ctx, `DELETE FROM memory.fact_events`)
		if e == nil {
			t.Fatal("history deletion allowed")
		}
		_, e = s.App.Exec(ctx, `CREATE TABLE memory.illegal(id int)`)
		if e == nil {
			t.Fatal("app DDL allowed")
		}
		// A wrong typed column and cross-space target must fail even via direct SQL.
		var space string
		if e = admin.QueryRow(ctx, `SELECT id::text FROM memory.spaces WHERE slug='personal'`).Scan(&space); e != nil {
			t.Fatal(e)
		}
		_, e = admin.Exec(ctx, `INSERT INTO memory.current_facts(space_id,id,entity_id,attribute_id,value_type,cardinality,value_text,value_fingerprint,assertion_revision) VALUES($1,$2,$3,$4,'integer','one','bad',decode(repeat('00',32),'hex'),1)`, space, model.NewID(), out.TempIDs["second"], attr)
		if e == nil {
			t.Fatal("typed column constraint missing")
		}
	})
}
