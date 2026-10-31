// Package migrations embeds immutable, forward-only schema migrations.
package migrations

import "embed"

//go:embed *.sql bootstrap.json
var Files embed.FS
