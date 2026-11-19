package httpx

import (
	"bytes"
	"net/http"
)

// WithProblemFallback replaces ServeMux's text/plain 404/405 bodies with
// problem+json, keeping the Allow header ServeMux computes for 405.
func WithProblemFallback(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux documents that an empty pattern means it would use its own
		// 404/405 handler. ServeHTTP (not the returned handler) must serve
		// matched routes, because it is what populates r.PathValue.
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		cw := &captureWriter{header: http.Header{}, status: http.StatusOK}
		mux.ServeHTTP(cw, r)
		writeUnmatched(w, r, cw)
	})
}

func writeUnmatched(w http.ResponseWriter, r *http.Request, cw *captureWriter) {
	switch cw.status {
	case http.StatusNotFound:
		WriteProblem(w, r, NewProblem(http.StatusNotFound, "No resource matches the requested path."))
	case http.StatusMethodNotAllowed:
		w.Header().Set("Allow", cw.header.Get("Allow"))
		WriteProblem(w, r, NewProblem(http.StatusMethodNotAllowed, "The method is not allowed for this resource."))
	default:
		cw.replay(w)
	}
}

type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header         { return c.header }
func (c *captureWriter) WriteHeader(status int)      { c.status = status }
func (c *captureWriter) Write(b []byte) (int, error) { return c.body.Write(b) }

func (c *captureWriter) replay(w http.ResponseWriter) {
	for k, v := range c.header {
		w.Header()[k] = v
	}
	w.WriteHeader(c.status)
	_, _ = w.Write(c.body.Bytes())
}
