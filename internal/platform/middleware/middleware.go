// Package middleware holds the cross-cutting HTTP handler wrappers.
package middleware

import (
	"net/http"
)

// Middleware wraps a handler with extra behaviour.
type Middleware func(http.Handler) http.Handler

// Chain applies mws to h so that the first middleware is the outermost:
// Chain(h, A, B) handles a request as A → B → h.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// statusWriter records the status code and bytes written. Middlewares share a
// single instance per request (see wrap) so they agree on what was sent.
type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func wrap(w http.ResponseWriter) *statusWriter {
	if sw, ok := w.(*statusWriter); ok {
		return sw
	}
	return &statusWriter{ResponseWriter: w}
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Status returns the status sent, defaulting to 200 when the handler wrote
// nothing (net/http sends 200 in that case).
func (w *statusWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

// Unwrap lets http.ResponseController reach Flush/Hijack/deadlines on the
// underlying writer without this type re-implementing every optional interface.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
