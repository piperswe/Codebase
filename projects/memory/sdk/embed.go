// Package sdk embeds the public SDK contract and executable synthetic examples.
package sdk

import "embed"

//go:embed contracts.json code-mode.d.ts examples/*.js
var Files embed.FS
