package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

// Recover turns a handler panic into a logged error and a 500 problem response,
// so one bad request never takes the process down or leaks a stack trace.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := wrap(w)
			defer func() {
				v := recover()
				if v == nil {
					return
				}
				// http.ErrAbortHandler is net/http's sentinel for "abort the
				// response silently"; let the server handle it as designed.
				if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
					panic(v)
				}
				log.ErrorContext(r.Context(), "panic recovered",
					slog.String("panic", fmt.Sprint(v)),
					slog.String("stack", string(debug.Stack())),
				)
				if sw.wroteHeader {
					// Too late for a clean 500: the client already has a status
					// and maybe part of a body. Abort the connection so the
					// truncated response cannot be mistaken for a complete one.
					panic(http.ErrAbortHandler)
				}
				httpx.WriteProblem(sw, r, httpx.NewProblem(http.StatusInternalServerError, ""))
			}()
			next.ServeHTTP(sw, r)
		})
	}
}
