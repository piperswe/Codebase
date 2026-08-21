-- name: Lookup :many
SELECT * FROM cache_items WHERE partition = ? AND cache_key = ? AND expires > unixepoch('now');

-- name: Write :exec
INSERT INTO cache_items(partition, cache_key, cache_value, expires) VALUES (?, ?, ?, ?)
    ON CONFLICT(partition, cache_key) DO UPDATE SET cache_value = excluded.cache_value, expires = excluded.expires;

-- name: DeleteExpired :exec
DELETE FROM cache_items WHERE expires <= unixepoch('now');
