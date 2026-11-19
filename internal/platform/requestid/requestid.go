// Package requestid generates, validates and carries per-request correlation
// IDs. It has no dependencies so logging, httpx and middleware can all use it.
package requestid

import (
	"context"
	"crypto/rand"
)

const Header = "X-Request-ID"

const maxLen = 128

type ctxKey struct{}

// 130 bits of entropy: collisions are not a practical concern.
func New() string { return rand.Text() }

// Valid reports whether an inbound ID is safe to reuse: bounded length and a
// conservative charset, so a client cannot inject log-breaking or huge values.
func Valid(id string) bool {
	if id == "" || len(id) > maxLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':':
		default:
			return false
		}
	}
	return true
}

func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func From(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
