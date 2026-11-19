// Package health exposes the liveness and readiness probes.
//
//	/healthz (liveness)  — is the process alive? Failing it gets the pod restarted.
//	/readyz  (readiness) — should it receive traffic? Failing it only removes the
//	                       pod from the load balancer.
package health

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Liveness reports that the process is up and serving HTTP. It deliberately
// checks no dependencies: a failing database must not get pods restarted, it
// should only take them out of rotation (that is /readyz's job).
func Liveness(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, `{"status":"ok"}`)
}

// Pinger is satisfied by *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Readiness reports whether this instance should receive traffic: it fails
// while draining for shutdown or when the database does not answer in time.
type Readiness struct {
	db       Pinger
	timeout  time.Duration
	log      *slog.Logger
	draining atomic.Bool
}

// NewReadiness returns a readiness probe that pings db with the given timeout.
func NewReadiness(db Pinger, timeout time.Duration, log *slog.Logger) *Readiness {
	return &Readiness{db: db, timeout: timeout, log: log}
}

// SetDraining makes every subsequent probe fail. It is called when shutdown
// starts and is never reverted. Safe for concurrent use.
func (r *Readiness) SetDraining() { r.draining.Store(true) }

func (r *Readiness) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.draining.Load() {
		writeStatus(w, http.StatusServiceUnavailable, `{"status":"draining"}`)
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), r.timeout)
	defer cancel()
	if err := r.db.Ping(ctx); err != nil {
		r.log.WarnContext(ctx, "readiness check failed", slog.String("check", "database"), slog.Any("error", err))
		writeStatus(w, http.StatusServiceUnavailable, `{"status":"unavailable","checks":{"database":"down"}}`)
		return
	}
	writeStatus(w, http.StatusOK, `{"status":"ok","checks":{"database":"up"}}`)
}

func writeStatus(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
