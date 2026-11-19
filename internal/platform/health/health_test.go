package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveness(t *testing.T) {
	rec := httptest.NewRecorder()
	Liveness(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if body := rec.Body.String(); body != `{"status":"ok"}` {
		t.Errorf("body = %q", body)
	}
}
