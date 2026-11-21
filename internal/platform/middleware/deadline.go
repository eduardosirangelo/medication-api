package middleware

import (
	"context"
	"net/http"
	"time"
)

// Deadline bounds the work behind each request. Without it, a slow query
// runs until HTTP_WRITE_TIMEOUT and the client sees a dropped connection; with
// it, the handler sees context.DeadlineExceeded in time to answer 503.
// http.TimeoutHandler is not used: it buffers every response and writes its
// own non-problem+json body.
func Deadline(timeout time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
