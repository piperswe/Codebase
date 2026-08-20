CREATE TABLE IF NOT EXISTS cache_items (
    partition INTEGER NOT NULL,
    cache_key TEXT NOT NULL,
    cache_value BLOB,
    -- UNIX timestamp in seconds
    expires INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS cache_lookup ON cache_items(partition, cache_key);
CREATE INDEX IF NOT EXISTS cache_expiration ON cache_items(expires);
