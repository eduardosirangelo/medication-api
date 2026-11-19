package middleware

import (
	"net/http"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/requestid"
)

// RequestID reuses a well-formed inbound X-Request-ID (so IDs propagate from
// gateways and callers) or generates one, stores it in the request context and
// echoes it in the response header.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestid.Header)
			if !requestid.Valid(id) {
				id = requestid.New()
			}
			w.Header().Set(requestid.Header, id)
			next.ServeHTTP(w, r.WithContext(requestid.With(r.Context(), id)))
		})
	}
}
