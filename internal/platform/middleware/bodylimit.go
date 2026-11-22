package middleware

import (
	"fmt"
	"net/http"

	"github.com/eduardosirangelo/medication-api/internal/platform/httpx"
)

// A declared oversized Content-Length is rejected without reading anything;
// chunked bodies have no declared length, so MaxBytesReader cuts them off
// while reading and httpx.DecodeJSON turns that into 413.
func MaxBodyBytes(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				httpx.WriteProblem(w, r, httpx.NewProblem(http.StatusRequestEntityTooLarge,
					fmt.Sprintf("Request body must not exceed %d bytes.", limit)))
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
