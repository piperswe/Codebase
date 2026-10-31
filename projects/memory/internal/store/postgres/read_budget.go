package postgres

import (
	"context"

	"codebase.bid/projects/memory/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// readTx counts at the SQL boundary, including queries inside shared schema/fact
// helpers. Worker-selected counters can never reset this execution-owned state.
type readTx struct {
	pgx.Tx
	s *Session
}

func (t *readTx) charge() error { t.s.Statements++; return t.s.budget() }
func (t *readTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if e := t.charge(); e != nil {
		return nil, e
	}
	rows, e := t.Tx.Query(ctx, sql, args...)
	if e != nil {
		return nil, e
	}
	return &readRows{Rows: rows, s: t.s}, nil
}
func (t *readTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if e := t.charge(); e != nil {
		return failedRow{e}
	}
	return readRow{Row: t.Tx.QueryRow(ctx, sql, args...), s: t.s}
}
func (t *readTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if e := t.charge(); e != nil {
		return pgconn.CommandTag{}, e
	}
	return t.Tx.Exec(ctx, sql, args...)
}

type failedRow struct{ err error }

func (r failedRow) Scan(...any) error { return r.err }

type readRow struct {
	pgx.Row
	s *Session
}

func (r readRow) Scan(dest ...any) error {
	if e := r.Row.Scan(dest...); e != nil {
		return e
	}
	r.s.Rows++
	return r.s.budget()
}

type readRows struct {
	pgx.Rows
	s *Session
}

func (r *readRows) Scan(dest ...any) error {
	if e := r.Rows.Scan(dest...); e != nil {
		return e
	}
	r.s.Rows++
	return r.s.budget()
}
func (t *readTx) decoded(n int) error {
	t.s.DecodedBytes += n
	if t.s.DecodedBytes > 4<<20 {
		return model.Limit("Decoded graph payload exceeds 4 MiB")
	}
	return nil
}
