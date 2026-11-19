// Package health exposes liveness (and, later, readiness) probes.
package health

import "net/http"

// Liveness reports that the process is up and serving HTTP. It deliberately
// checks no dependencies: a failing database must not get pods restarted, it
// should only take them out of rotation (that is /readyz's job).
func Liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// TODO(slice-3): Readiness handler (DB ping with timeout, fails while draining).
