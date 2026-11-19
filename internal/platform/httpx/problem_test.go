package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/requestid"
)

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != ProblemContentType {
		t.Fatalf("Content-Type = %q, want %q", ct, ProblemContentType)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v: %s", err, rec.Body.String())
	}
	return p
}

func TestWriteProblem_FillsInstanceAndRequestID(t *testing.T) {
	req := httptest.NewRequestWithContext(requestid.With(t.Context(), "rid-1"), http.MethodGet, "/v1/x", nil)
	rec := httptest.NewRecorder()

	p := NewProblem(http.StatusUnprocessableEntity, "Validation failed.")
	p.Errors = []FieldError{{Field: "name", Message: "is required"}}
	WriteProblem(rec, req, p)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
	got := decodeProblem(t, rec)
	if got.Type != "about:blank" || got.Title != "Unprocessable Entity" || got.Status != 422 ||
		got.Detail != "Validation failed." || got.Instance != "/v1/x" || got.RequestID != "rid-1" {
		t.Errorf("unexpected problem: %+v", got)
	}
	if len(got.Errors) != 1 || got.Errors[0].Field != "name" {
		t.Errorf("errors = %+v", got.Errors)
	}
}

func TestWriteProblem_OmitsEmptyOptionalMembers(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	WriteProblem(rec, req, NewProblem(http.StatusNotFound, ""))

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"detail", "request_id", "errors"} {
		if _, ok := raw[k]; ok {
			t.Errorf("%q should be omitted when empty: %v", k, raw)
		}
	}
}

func TestWriteJSON_UnencodableValueBecomes500(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	WriteJSON(rec, req, http.StatusOK, map[string]any{"bad": make(chan int)})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if p := decodeProblem(t, rec); p.Status != 500 {
		t.Errorf("problem = %+v", p)
	}
}

func TestWithProblemFallback(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("item " + r.PathValue("id")))
	})
	h := WithProblemFallback(mux)

	t.Run("matched route passes through with path values", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items/7", nil))
		if rec.Code != 200 || rec.Body.String() != "item 7" {
			t.Fatalf("got %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown path is 404 problem", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/nope", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		if p := decodeProblem(t, rec); p.Status != 404 || p.Instance != "/nope" {
			t.Errorf("problem = %+v", p)
		}
	})

	t.Run("wrong method is 405 problem with Allow", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/items/7", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d", rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
		}
		if p := decodeProblem(t, rec); p.Status != 405 {
			t.Errorf("problem = %+v", p)
		}
	})

	t.Run("non-canonical path still redirects", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/items/../items/7", nil))
		// The exact 3xx code is ServeMux's choice; we only require that the
		// redirect is not swallowed into a 404 problem.
		if rec.Code < 300 || rec.Code > 399 || rec.Header().Get("Location") != "/items/7" {
			t.Fatalf("got %d Location=%q, want redirect to /items/7", rec.Code, rec.Header().Get("Location"))
		}
	})
}
