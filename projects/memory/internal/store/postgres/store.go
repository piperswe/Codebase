// Package postgres owns graph sessions. Every caller supplies its authenticated
// authority; worker callbacks never choose a principal or space.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"codebase.bid/projects/memory/internal/model"
	"codebase.bid/projects/memory/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	App, Reader *pgxpool.Pool
	CursorKey   []byte
	Fingerprint func(model.Value) []byte
}

func Open(ctx context.Context, appDSN, readerDSN string, key []byte) (*Store, error) {
	return OpenWithPools(ctx, appDSN, readerDSN, key, 8, 2)
}
func OpenWithPools(ctx context.Context, appDSN, readerDSN string, key []byte, appMax, readerMax int32) (*Store, error) {
	if appMax < 1 || appMax > 64 || readerMax < 1 || readerMax > 16 {
		return nil, model.Invalid("database pool limits")
	}
	if len(key) < 32 {
		return nil, model.Invalid("cursor signing key")
	}
	open := func(dsn, name string, max int32) (*pgxpool.Pool, error) {
		c, e := pgxpool.ParseConfig(dsn)
		if e != nil {
			return nil, e
		}
		c.MaxConns = max
		c.ConnConfig.RuntimeParams["application_name"] = name
		c.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,memory"
		c.ConnConfig.RuntimeParams["statement_timeout"] = "1000"
		c.ConnConfig.RuntimeParams["lock_timeout"] = "1000"
		c.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "10000"
		return pgxpool.NewWithConfig(ctx, c)
	}
	app, e := open(appDSN, "memory-api", appMax)
	if e != nil {
		return nil, e
	}
	reader, e := open(readerDSN, "memory-reader", readerMax)
	if e != nil {
		app.Close()
		return nil, e
	}
	s := &Store{App: app, Reader: reader, CursorKey: append([]byte(nil), key...), Fingerprint: func(v model.Value) []byte { return v.Fingerprint() }}
	if e = s.Check(ctx); e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() { s.App.Close(); s.Reader.Close() }
func (s *Store) Check(ctx context.Context) error {
	for _, p := range []struct {
		pool *pgxpool.Pool
		role string
	}{{s.App, "memory_app"}, {s.Reader, "memory_reader"}} {
		var role string
		var elevated bool
		e := p.pool.QueryRow(ctx, `SELECT current_user,rolsuper OR rolcreaterole OR rolcreatedb OR pg_has_role(current_user,'memory_owner','MEMBER') FROM pg_roles WHERE rolname=current_user`).Scan(&role, &elevated)
		if e != nil {
			return e
		}
		if role != p.role || elevated {
			return fmt.Errorf("runtime requires restricted %s role", p.role)
		}
	}
	var version int
	if e := s.App.QueryRow(ctx, `SELECT max(version) FROM memory.schema_migrations`).Scan(&version); e != nil || version != 1 {
		return fmt.Errorf("incompatible database schema; run migrate up separately")
	}
	return nil
}
func Migrate(ctx context.Context, dsn, command string) (int, error) {
	c, e := pgx.Connect(ctx, dsn)
	if e != nil {
		return 0, e
	}
	defer c.Close(context.Background())
	if command == "roles" {
		b, _ := migrations.Files.ReadFile("roles.sql")
		_, e = c.Exec(ctx, string(b))
		return 0, e
	}
	if _, e = c.Exec(ctx, `SELECT pg_advisory_lock(6713820991)`); e != nil {
		return 0, e
	}
	defer c.Exec(context.Background(), `SELECT pg_advisory_unlock(6713820991)`)
	var exists bool
	e = c.QueryRow(ctx, `SELECT to_regclass('memory.schema_migrations') IS NOT NULL`).Scan(&exists)
	if e != nil {
		return 0, e
	}
	if exists {
		var n int
		e = c.QueryRow(ctx, `SELECT coalesce(max(version),0) FROM memory.schema_migrations`).Scan(&n)
		if e == nil && command == "up" && n != 1 {
			return n, fmt.Errorf("unsupported database schema version")
		}
		return n, e
	}
	if command == "status" {
		return 0, nil
	}
	if command != "up" {
		return 0, model.Invalid("migration command")
	}
	tx, e := c.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SET LOCAL ROLE memory_owner`); e != nil {
		return 0, e
	}
	b, _ := migrations.Files.ReadFile("000001_base.sql")
	if _, e = tx.Exec(ctx, string(b)); e != nil {
		return 0, e
	}
	b, _ = migrations.Files.ReadFile("grants.sql")
	if _, e = tx.Exec(ctx, string(b)); e != nil {
		return 0, e
	}
	_, e = tx.Exec(ctx, `CREATE TABLE memory.schema_migrations(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO memory.schema_migrations(version) VALUES(1); REVOKE ALL ON memory.schema_migrations FROM memory_app; GRANT SELECT ON memory.schema_migrations TO memory_app`)
	if e != nil {
		return 0, e
	}
	return 1, tx.Commit(ctx)
}
func safe(err error) error {
	if err == nil {
		return nil
	}
	var m *model.Error
	if errors.As(err, &m) {
		return m
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &model.Error{Code: "EXECUTION_TIMEOUT", Message: "Operation timed out", Retryable: true}
	}
	if errors.Is(err, context.Canceled) {
		return model.E("CANCELLED", "Operation cancelled")
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "23505":
			return model.E("NAME_CONFLICT", "A name or set member already exists")
		case "23503", "23514", "22P02":
			return model.E("INVALID_INPUT", "Data violates a graph invariant")
		case "57014", "55P03", "40P01", "40001":
			return &model.Error{Code: "RESOURCE_LIMIT_EXCEEDED", Message: "Database operation exceeded its budget; retry", Retryable: true}
		}
	}
	return model.E("INTERNAL_ERROR", "Storage operation failed")
}

type Session struct {
	DecodedBytes                 int
	tx                           pgx.Tx
	ctx                          context.Context
	Space, Slug                  string
	Revision, SchemaRevision     int64
	key                          []byte
	Statements, Rows, Ops, Edges int
	loaded                       map[string]bool
	Payload                      int
	schemaCache                  map[string]model.Definition
	nameCache                    map[string]string
}

func (s *Store) Read(ctx context.Context, p model.Principal, space string) (*Session, error) {
	if !p.Allow(space, "memory:read") {
		return nil, model.E("PERMISSION_DENIED", "Space or scope is not granted")
	}
	tx, e := s.Reader.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, safe(e)
	}
	r := &Session{tx: tx, ctx: ctx, Slug: space, key: s.CursorKey, loaded: map[string]bool{}, schemaCache: map[string]model.Definition{}, nameCache: map[string]string{}}
	e = tx.QueryRow(ctx, `SELECT id::text,revision,schema_revision FROM memory.spaces WHERE slug=$1`, space).Scan(&r.Space, &r.Revision, &r.SchemaRevision)
	if e != nil {
		tx.Rollback(context.Background())
		return nil, model.E("PERMISSION_DENIED", "Space or scope is not granted")
	}
	r.Statements = 1
	r.Rows = 1
	r.tx = &readTx{Tx: tx, s: r}
	return r, nil
}
func (r *Session) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = r.tx.Rollback(ctx)
}
func (r *Session) budget() error {
	if r.Statements > 200 || r.Rows > 10000 || len(r.loaded) > 1000 || r.Edges > 2000 || r.Payload > 4<<20 {
		return model.Limit("Read budget exhausted")
	}
	return r.ctx.Err()
}
func (r *Session) Charge() error {
	r.Ops++
	if r.Ops > 100 {
		return model.Limit("Graph operation budget exhausted")
	}
	return r.budget()
}
func resolve(ctx context.Context, tx pgx.Tx, space, name string) (string, error) {
	var id string
	var e error
	if parsed, err := uuid.Parse(name); err == nil {
		e = tx.QueryRow(ctx, `SELECT id::text FROM memory.entities WHERE space_id=$1 AND id=$2`, space, parsed.String()).Scan(&id)
	} else {
		e = tx.QueryRow(ctx, `SELECT entity_id::text FROM memory.entity_names WHERE space_id=$1 AND name=$2`, space, name).Scan(&id)
	}
	if errors.Is(e, pgx.ErrNoRows) {
		return "", model.E("ENTITY_NOT_FOUND", "Entity does not exist in this space")
	}
	return id, safe(e)
}
func (r *Session) Resolve(name string) (string, error) {
	if id := r.nameCache[name]; id != "" {
		return id, nil
	}
	if e := r.budget(); e != nil {
		return "", e
	}
	id, e := resolve(r.ctx, r.tx, r.Space, name)
	if e == nil {
		r.nameCache[name] = id
		r.loaded[id] = true
	}
	return id, e
}

func definition(ctx context.Context, tx pgx.Tx, space, name string) (model.Definition, error) {
	var d model.Definition
	id, e := resolve(ctx, tx, space, name)
	if e != nil {
		return d, unknownAttribute(ctx, tx, space, name)
	}
	var examples, notThis []byte
	e = tx.QueryRow(ctx, `SELECT a.id::text,n.name,a.description,a.value_type,a.cardinality,a.unit,a.status,coalesce(a.replacement::text,''),a.examples,a.not_this,a.schema_revision::text,a.protected,
 ARRAY(SELECT type_id::text FROM memory.attribute_domains WHERE space_id=a.space_id AND attribute_id=a.id ORDER BY type_id),
 ARRAY(SELECT type_id::text FROM memory.attribute_ranges WHERE space_id=a.space_id AND attribute_id=a.id ORDER BY type_id),
 ARRAY(SELECT name FROM memory.entity_names WHERE space_id=a.space_id AND entity_id=a.id AND NOT is_canonical ORDER BY name)
 FROM memory.attribute_defs a JOIN memory.entity_names n ON n.space_id=a.space_id AND n.entity_id=a.id AND n.is_canonical WHERE a.space_id=$1 AND a.id=$2`, space, id).Scan(&d.ID, &d.Ident, &d.Description, &d.ValueType, &d.Cardinality, &d.Unit, &d.Status, &d.Replacement, &examples, &notThis, &d.SchemaRevision, &d.Protected, &d.AppliesTo, &d.ReferenceTypes, &d.Aliases)
	if errors.Is(e, pgx.ErrNoRows) {
		return d, model.E("ATTRIBUTE_NOT_FOUND", "Entity is not a registered attribute")
	}
	if e != nil {
		return d, safe(e)
	}
	d.Kind = "attribute"
	_ = json.Unmarshal(examples, &d.Examples)
	_ = json.Unmarshal(notThis, &d.NotThis)
	return d, nil
}
func (r *Session) Definition(name string) (model.Definition, error) {
	if d, ok := r.schemaCache[name]; ok {
		return d, nil
	}
	if e := r.budget(); e != nil {
		return model.Definition{}, e
	}
	d, e := definition(r.ctx, r.tx, r.Space, name)
	if e == nil {
		r.loaded[d.ID] = true
		r.schemaCache[name] = d
		r.schemaCache[d.ID] = d
		r.schemaCache[d.Ident] = d
		for _, alias := range d.Aliases {
			r.schemaCache[alias] = d
		}
	}
	return d, e
}

func writeScope(p model.Principal, space, scope string) error {
	if !p.Allow(space, scope) {
		return model.E("PERMISSION_DENIED", "Space or scope is not granted")
	}
	return nil
}
func validText(s string, max int) bool { return len(s) <= max && !strings.ContainsRune(s, 0) }

func unknownAttribute(ctx context.Context, tx pgx.Tx, space, name string) error {
	err := model.E("ATTRIBUTE_NOT_FOUND", "Unknown registered attribute; review schema candidates before writing")
	rows, e := tx.Query(ctx, `SELECT a.id::text,n.name,'attribute',left(a.description,256) FROM memory.attribute_defs a JOIN memory.entity_names n ON n.space_id=a.space_id AND n.entity_id=a.id AND n.is_canonical WHERE a.space_id=$1 ORDER BY (n.name=$2) DESC,(split_part(n.name,'/',1)=split_part($2,'/',1)) DESC,n.name LIMIT 5`, space, name)
	if e != nil {
		return err
	}
	defer rows.Close()
	hits := []model.Hit{}
	for rows.Next() {
		var h model.Hit
		if rows.Scan(&h.ID, &h.Ident, &h.Kind, &h.Snippet) != nil {
			return err
		}
		hits = append(hits, h)
	}
	err.Details = struct {
		Candidates []model.Hit `json:"candidates"`
	}{hits}
	return err
}

// CheckBudget includes decoded/loaded objects charged after the last SQL row.
func (r *Session) CheckBudget() error { return r.budget() }

func (r *Session) TypeID(name string) (string, error) {
	id, e := r.Resolve(name)
	if e != nil {
		return "", e
	}
	var exists bool
	if e = r.tx.QueryRow(r.ctx, `SELECT EXISTS(SELECT FROM memory.type_defs WHERE space_id=$1 AND id=$2)`, r.Space, id).Scan(&exists); e != nil {
		return "", safe(e)
	}
	if !exists {
		return "", model.E("REFERENCE_TYPE_MISMATCH", "Type filters require a registered type")
	}
	return id, nil
}
