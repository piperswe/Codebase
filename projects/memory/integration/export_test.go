package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/internal/store/postgres"
	"github.com/jackc/pgx/v5"
)

func TestGraphExportRestore(t *testing.T) {
	s, admin := database(t)
	ctx := t.Context()
	d := definition(t, s, model.Definition{Ident: "test/value", Kind: "attribute", Description: "Synthetic exported value", ValueType: model.String, Cardinality: "one"})
	r := current(t, s)
	schema := model.Revision(r.SchemaRevision)
	r.Close()
	out, e := s.Transact(ctx, owner(), model.Transaction{Space: "personal", IdempotencyKey: "export-0001", ExpectedSchemaRevision: schema, Reason: "Export synthetic history", Operations: []model.Operation{{Op: "createSource", TempID: "source", Source: &model.Source{Origin: "user", Description: "Synthetic source"}}, {Op: "createEntity", TempID: "entity", Ident: "test/exported"}, {Op: "assert", Entity: model.EntityRef{TempID: "entity"}, Attribute: d, Value: &model.Value{Type: model.String, Text: "preserve"}, Evidence: []model.Evidence{{Source: "temp:source"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	var exported bytes.Buffer
	if e = s.Export(ctx, owner(), "personal", &exported); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(exported.String(), "memory_auth") || strings.Contains(exported.String(), "virtual:") {
		t.Fatal("auth/virtual metadata duplicated in export")
	}
	if e = s.Import(ctx, "personal", bytes.NewReader(exported.Bytes())); e == nil {
		t.Fatal("restore overwrote space")
	}
	if _, e = admin.Exec(ctx, `CREATE DATABASE memory_restore`); e != nil {
		t.Fatal(e)
	}
	cfg := admin.Config().Copy()
	cfg.Database = "memory_restore"
	restoreAdmin, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer restoreAdmin.Close(context.Background())
	// ConnString retains original input; build a keyword DSN with a copied config
	// through ParseConfig only after replacing the database URL component.
	dsn := admin.Config().ConnString()
	pos := strings.Index(dsn, "/memorytest")
	if pos < 0 {
		t.Fatal("unexpected fixture DSN")
	}
	dsn = dsn[:pos] + strings.Replace(dsn[pos:], "/memorytest", "/memory_restore", 1)
	if _, e = postgres.Migrate(ctx, dsn, "roles"); e != nil {
		t.Fatal(e)
	}
	if _, e = postgres.Migrate(ctx, dsn, "up"); e != nil {
		t.Fatal(e)
	}
	// Open uses separately authenticated runtime identities, never the DBA.
	app := strings.Replace(dsn, "postgres:test-only@", "memory_app:test-only@", 1)
	reader := strings.Replace(dsn, "postgres:test-only@", "memory_reader:test-only@", 1)
	restored, e := postgres.Open(ctx, app, reader, []byte(strings.Repeat("k", 32)))
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if e = restored.Import(ctx, "personal", bytes.NewReader(exported.Bytes())); e != nil {
		t.Fatal(e)
	}
	before, e := s.Read(ctx, owner(), "personal")
	if e != nil {
		t.Fatal(e)
	}
	defer before.Close()
	after, e := restored.Read(ctx, owner(), "personal")
	if e != nil {
		t.Fatal(e)
	}
	defer after.Close()
	a, e := before.Get([]string{out.TempIDs["entity"], d}, nil)
	if e != nil {
		t.Fatal(e)
	}
	b, e := after.Get([]string{out.TempIDs["entity"], d}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if model.Hash(a) != model.Hash(b) {
		t.Fatal("restored graph differs")
	}
	ha, e := before.History(model.History{})
	if e != nil {
		t.Fatal(e)
	}
	hb, e := after.History(model.History{})
	if e != nil {
		t.Fatal(e)
	}
	aj, _ := json.Marshal(ha)
	bj, _ := json.Marshal(hb)
	if !bytes.Equal(aj, bj) {
		t.Fatal("restored history differs")
	}
}
