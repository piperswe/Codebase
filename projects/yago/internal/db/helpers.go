package db

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/oklog/ulid/v2"
)

func NewFromEnvironment(ctx context.Context) (*Queries, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, fmt.Errorf("missing DATABASE_URL")
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Queries{db: conn}, nil
}

func (q *Queries) Close(ctx context.Context) error {
	db, ok := q.db.(*pgx.Conn)
	if ok {
		return db.Close(ctx)
	}
	return nil
}

func (q *Queries) Migrate(ctx context.Context) error {
	db, ok := q.db.(*pgx.Conn)
	if !ok {
		return fmt.Errorf("invalid database connection")
	}
	return Migrator.Migrate(ctx, db)
}

func (q *Queries) CreateUser(ctx context.Context, arg CreateUserWithUlidParams) (User, error) {
	if arg.Ulid == "" {
		arg.Ulid = ulid.Make().String()
	}
	return q.CreateUserWithUlid(ctx, arg)
}

func (q *Queries) CreateProfile(ctx context.Context, arg CreateProfileWithUlidParams) (Profile, error) {
	if arg.Ulid == "" {
		arg.Ulid = ulid.Make().String()
	}
	return q.CreateProfileWithUlid(ctx, arg)
}

func (q *Queries) TxVal[T any](ctx context.Context, fn func(*Queries) (T, error)) (T, error) {
	var zero T
	db, ok := q.db.(*pgx.Conn)
	if !ok {
		return zero, fmt.Errorf("invalid database connection")
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	txQueries := &Queries{db: tx}
	result, err := fn(txQueries)
	if err != nil {
		_ = tx.Rollback(ctx)
		return zero, err
	}
	return result, tx.Commit(ctx)
}

func (q *Queries) Tx(ctx context.Context, fn func(*Queries) error) error {
	_, err := q.TxVal(ctx, func(txQueries *Queries) (struct{}, error) {
		err := fn(txQueries)
		return struct{}{}, err
	})
	return err
}
