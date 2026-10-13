// Package oauthstore persists OAuth clients, grants, codes and tokens, and
// defines the per-service grant matrix that gates tool calls.
package oauthstore

import "context"

// Grants maps a service name to the access level the client holds:
// "" (none), "read" or "write".
type Grants map[string]string

type grantsKey struct{}

// GrantsFromContext returns the grants set by the OAuth middleware, or nil
// when no middleware ran (local trusted mode).
func GrantsFromContext(ctx context.Context) Grants {
	g, _ := ctx.Value(grantsKey{}).(Grants)
	return g
}

// WithGrants returns ctx carrying grants for handlers further down.
func WithGrants(ctx context.Context, g Grants) context.Context {
	return context.WithValue(ctx, grantsKey{}, g)
}

// Scopes renders grants as OAuth scope strings: {"mail:read","mail:write",...}.
func (g Grants) Scopes() []string {
	var out []string
	for svc, level := range g {
		if level == "" {
			continue
		}
		out = append(out, svc+":read")
		if level == "write" {
			out = append(out, svc+":write")
		}
	}
	return out
}

// GrantsFromScopes is the inverse of Scopes.
func GrantsFromScopes(scopes []string) Grants {
	g := Grants{}
	for _, s := range scopes {
		var svc, level string
		if n, _ := fmtSscanf(s, &svc, &level); n != 2 {
			continue
		}
		switch level {
		case "write":
			g[svc] = "write"
		case "read":
			if g[svc] == "" {
				g[svc] = "read"
			}
		}
	}
	return g
}

// fmtSscanf splits "service:level"; kept separate for testability.
func fmtSscanf(s string, svc, level *string) (int, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' && i > 0 && i < len(s)-1 {
			*svc, *level = s[:i], s[i+1:]
			return 2, nil
		}
	}
	return 0, nil
}
