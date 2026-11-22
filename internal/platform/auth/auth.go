// Package auth authenticates callers with static API keys sent in the
// X-API-Key header. Every path is protected unless explicitly listed as public.
//
// TODO: OAuth 2.0 client credentials or mTLS for partner integrations;
// key rotation without a restart (overlapping old/new keys from a secret
// store); per-key rate limits.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"

	"github.com/eduardosirangelo/medication-api/internal/platform/config"
	"github.com/eduardosirangelo/medication-api/internal/platform/httpx"
	"github.com/eduardosirangelo/medication-api/internal/platform/middleware"
)

const Header = "X-API-Key"

// Not Bearer: that scheme promises OAuth 2.0 tokens (RFC 6750), which a
// static key is not, and Bearer stays free for the OAuth TODO.
const challenge = `ApiKey realm="medication-api", header="` + Header + `"`

// Keyring holds digests, not keys: hashing gives every comparison the same
// length, so neither the key's length nor its content shows in the timing.
type Keyring struct {
	entries []entry
}

type entry struct {
	id     string
	digest [sha256.Size]byte
}

func NewKeyring(keys []config.APIKey) *Keyring {
	k := &Keyring{entries: make([]entry, len(keys))}
	for i, key := range keys {
		k.entries[i] = entry{id: key.ID, digest: sha256.Sum256([]byte(key.Key.Reveal()))}
	}
	return k
}

func (k *Keyring) Len() int { return len(k.entries) }

// Identify returns the ID of the key that matches. It checks every entry
// without stopping early, so the time taken does not reveal which one matched.
func (k *Keyring) Identify(presented string) (id string, ok bool) {
	digest := sha256.Sum256([]byte(presented))
	matched := 0
	for _, e := range k.entries {
		if subtle.ConstantTimeCompare(digest[:], e.digest[:]) == 1 {
			id, matched = e.id, 1
		}
	}
	return id, matched == 1
}

// Require rejects requests without a valid key with 401, except for the
// exact public paths given (probes and build info). Unknown paths are
// protected too, so unauthenticated callers cannot map the routes.
func Require(keys *Keyring, publicPaths ...string) middleware.Middleware {
	public := make(map[string]bool, len(publicPaths))
	for _, p := range publicPaths {
		public[p] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if public[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			presented := r.Header.Get(Header)
			if presented == "" {
				unauthorized(w, r, "missing", "Missing API key; send it in the "+Header+" header.")
				return
			}
			id, ok := keys.Identify(presented)
			if !ok {
				unauthorized(w, r, "invalid", "Invalid API key.")
				return
			}
			// The ID, never the key, so logs can say who called without
			// becoming a place where keys leak.
			middleware.Annotate(r.Context(), slog.String("key_id", id))
			next.ServeHTTP(w, r)
		})
	}
}

func unauthorized(w http.ResponseWriter, r *http.Request, reason, detail string) {
	middleware.Annotate(r.Context(), slog.String("auth_failure", reason))
	w.Header().Set("WWW-Authenticate", challenge)
	httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusUnauthorized, detail))
}
