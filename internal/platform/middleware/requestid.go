package middleware

import (
	"net/http"

	"github.com/eduardosirangelo/medication-api/internal/platform/requestid"
)

// A well-formed inbound ID is reused so one ID follows the request across
// gateways and services; anything else is replaced.
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
