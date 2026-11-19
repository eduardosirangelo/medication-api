package middleware

import (
	"fmt"
	"net/http"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

// MaxBodyBytes caps request bodies at limit bytes.
//
//   - A declared Content-Length over the limit is rejected up front with 413,
//     before any of the body is read.
//   - Otherwise (including chunked bodies of unknown length) the body is wrapped
//     in http.MaxBytesReader; reads past the limit fail with *http.MaxBytesError,
//     which the JSON decoder maps to 413 (slice 5).
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
