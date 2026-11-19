package httpx

import (
	"bytes"
	"net/http"
)

// WithProblemFallback wraps mux so that requests matching no route get an
// application/problem+json 404/405 instead of ServeMux's text/plain bodies.
// The Allow header ServeMux computes for 405 is preserved.
func WithProblemFallback(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An empty pattern means ServeMux would use its own 404/405 handler.
		// Registered routes (and canonical-path redirects) pass straight through.
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}

		// Run the mux's tiny built-in handler into a buffer to learn the
		// status and Allow header, then replace its body.
		cw := &captureWriter{header: http.Header{}, status: http.StatusOK}
		mux.ServeHTTP(cw, r)

		switch cw.status {
		case http.StatusNotFound:
			WriteProblem(w, r, NewProblem(http.StatusNotFound, "No resource matches the requested path."))
		case http.StatusMethodNotAllowed:
			w.Header().Set("Allow", cw.header.Get("Allow"))
			WriteProblem(w, r, NewProblem(http.StatusMethodNotAllowed, "The method is not allowed for this resource."))
		default: // not expected; replay verbatim
			for k, v := range cw.header {
				w.Header()[k] = v
			}
			w.WriteHeader(cw.status)
			_, _ = w.Write(cw.body.Bytes())
		}
	})
}

type captureWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *captureWriter) Header() http.Header         { return c.header }
func (c *captureWriter) WriteHeader(status int)      { c.status = status }
func (c *captureWriter) Write(b []byte) (int, error) { return c.body.Write(b) }
