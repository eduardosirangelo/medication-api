package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

// One bad request must never take the process down or leak a stack trace.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := wrap(w)
			defer func() {
				if v := recover(); v != nil {
					handlePanic(v, sw, r, log)
				}
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

func handlePanic(v any, sw *statusWriter, r *http.Request, log *slog.Logger) {
	// net/http's sentinel for "abort the response silently": let the server
	// handle it as designed.
	if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
		panic(v)
	}
	log.ErrorContext(r.Context(), "panic recovered",
		slog.String("panic", fmt.Sprint(v)),
		slog.String("stack", string(debug.Stack())),
	)
	if sw.wroteHeader {
		// Too late for a clean 500: the client already has a status and maybe
		// part of a body. Aborting the connection stops a truncated response
		// from being mistaken for a complete one.
		panic(http.ErrAbortHandler)
	}
	httpx.WriteProblem(sw, r, httpx.NewProblem(http.StatusInternalServerError, ""))
}
