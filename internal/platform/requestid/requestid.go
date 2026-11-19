// Package requestid generates, validates and carries per-request correlation
// IDs. It has no dependencies so logging, httpx and middleware can all use it.
package requestid

import (
	"context"
	"crypto/rand"
)

// Header is the HTTP header used to accept and echo the request ID.
const Header = "X-Request-ID"

const maxLen = 128

type ctxKey struct{}

// New returns a fresh random ID (26 chars, base32, 130 bits of entropy).
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

// With returns a copy of ctx carrying id.
func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// From returns the request ID in ctx, or "" if none.
func From(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
