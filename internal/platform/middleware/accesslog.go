package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// StatusClientClosedRequest is nginx's convention for "the client went away
// before the response": not a server failure, so it must not page anyone.
const StatusClientClosedRequest = 499

// Probes hit every few seconds; at info level they would drown real traffic.
var quietPaths = map[string]bool{"/healthz": true, "/readyz": true}

// Bodies, query strings and headers are never logged: they may carry user
// input or credentials.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := wrap(w)
			notes := &annotations{}
			r = r.WithContext(context.WithValue(r.Context(), annotationsKey{}, notes))
			// Deferred so the line is still written when a panic propagates
			// (e.g. the http.ErrAbortHandler re-panic from Recover).
			defer logRequest(log, r, sw, start, notes)
			next.ServeHTTP(sw, r)
		})
	}
}

type annotationsKey struct{}

type annotations struct{ attrs []slog.Attr }

// Annotate adds attributes to this request's access log line. Inner
// middleware cannot change the context AccessLog logs with, so this is how
// facts learned later (such as the caller's key ID) reach that one line.
// Call it from the request's goroutine only.
func Annotate(ctx context.Context, attrs ...slog.Attr) {
	if notes, ok := ctx.Value(annotationsKey{}).(*annotations); ok {
		notes.attrs = append(notes.attrs, attrs...)
	}
}

func logRequest(log *slog.Logger, r *http.Request, sw *statusWriter, start time.Time, notes *annotations) {
	status := loggedStatus(r, sw.Status())
	attrs := append([]slog.Attr{
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Int64("bytes", sw.bytes),
		slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
	}, notes.attrs...)
	log.LogAttrs(r.Context(), requestLogLevel(r.URL.Path, status), "http request", attrs...)
}

// The context AccessLog sees is cancelled only when the client disconnects
// (the request deadline is set further in). A 5xx written after that is the
// handler abandoning work nobody will read, so it is recorded as 499.
func loggedStatus(r *http.Request, written int) int {
	if written >= 500 && errors.Is(r.Context().Err(), context.Canceled) {
		return StatusClientClosedRequest
	}
	return written
}

func requestLogLevel(path string, status int) slog.Level {
	switch {
	case quietPaths[path]:
		// Probe 503s are expected while draining; real dependency failures
		// are already logged by the readiness handler.
		return slog.LevelDebug
	case status >= 500:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
