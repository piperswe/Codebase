package cache

import (
	"context"
	"encoding/json"
	"time"
)

const PARTITION_MOVIE_DETAILS = 1

func Lookup[T any](ctx context.Context, q *Queries, partition int64, cacheKey string) (*T, error) {
	data, err := q.Lookup(ctx, LookupParams{
		partition,
		cacheKey,
	})
	if err != nil {
		return nil, err
	}
	if len(data) > 0 {
		data := data[0]
		var result T
		err := json.Unmarshal(data.CacheValue, &result)
		if err != nil {
			return nil, err
		}
		return &result, nil
	}
	return nil, nil
}

func Write[T any](ctx context.Context, q *Queries, partition int64, cacheKey string, value T, expires time.Time) error {
	serialized, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return q.Write(ctx, WriteParams{
		Partition:  partition,
		CacheKey:   cacheKey,
		CacheValue: serialized,
		Expires:    expires.Unix(),
	})
}
