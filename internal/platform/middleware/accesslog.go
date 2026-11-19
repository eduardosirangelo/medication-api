package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// Probes hit every few seconds; at info level they would drown real traffic.
var quietPaths = map[string]bool{"/healthz": true, "/readyz": true}

// Bodies, query strings and headers are never logged: they may carry user
// input or credentials.
func AccessLog(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := wrap(w)
			// Deferred so the line is still written when a panic propagates
			// (e.g. the http.ErrAbortHandler re-panic from Recover).
			defer logRequest(log, r, sw, start)
			next.ServeHTTP(sw, r)
		})
	}
}

func logRequest(log *slog.Logger, r *http.Request, sw *statusWriter, start time.Time) {
	status := sw.Status()
	log.LogAttrs(r.Context(), requestLogLevel(r.URL.Path, status), "http request",
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", status),
		slog.Int64("bytes", sw.bytes),
		slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
	)
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
