package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// quietPaths are probe endpoints hit every few seconds; logged at debug only.
var quietPaths = map[string]bool{"/healthz": true, "/readyz": true}

// AccessLog writes one structured line per request after it completes.
// It never logs bodies, query strings or headers (they may carry user input or
// credentials); request_id is added by the context-aware logger.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := wrap(w)
			// Deferred so the line is still written when a panic propagates
			// (e.g. the http.ErrAbortHandler re-panic from Recover).
			defer func() {
				status := sw.Status()
				level := slog.LevelInfo
				switch {
				case quietPaths[r.URL.Path]:
					// Probe 503s are expected while draining; real dependency
					// failures are already logged by the readiness handler.
					level = slog.LevelDebug
				case status >= 500:
					level = slog.LevelError
				}
				log.LogAttrs(r.Context(), level, "http request",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", status),
					slog.Int64("bytes", sw.bytes),
					slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				)
			}()
			next.ServeHTTP(sw, r)
		})
	}
}
