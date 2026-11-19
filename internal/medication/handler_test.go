package medication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
)

const paracetamolJSON = `{"name":"Paracetamol","form":"tablet","dosage":{"value":500,"unit":"mg"}}`

type testAPI struct {
	repo *fakeRepo
	mux  *http.ServeMux
	logs *bytes.Buffer
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	repo := newFakeRepo()
	var logs bytes.Buffer
	mux := http.NewServeMux()
	NewHandler(newTestService(repo), slog.New(slog.NewJSONHandler(&logs, nil))).Register(mux)
	return &testAPI{repo: repo, mux: mux, logs: &logs}
}

func (a *testAPI) do(t *testing.T, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequestWithContext(t.Context(), method, path, nil)
	} else {
		r = httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, r)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

func TestCreate_201(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications", paracetamolJSON)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/v1/medications/"+fixedID.String() {
		t.Errorf("Location = %q", loc)
	}
	if etag := rec.Header().Get("ETag"); etag != `"1"` {
		t.Errorf("ETag = %q", etag)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	// Exact wire format: numbers as JSON numbers, no per_* when absent, RFC 3339 UTC.
	want := `{"id":"` + fixedID.String() + `","name":"Paracetamol","dosage":{"value":500,"unit":"mg"},` +
		`"form":"tablet","version":1,"created_at":"2026-11-19T12:00:00Z","updated_at":"2026-11-19T12:00:00Z"}`
	if got := rec.Body.String(); got != want {
		t.Errorf("body:\n got  %s\n want %s", got, want)
	}
}

func TestCreate_ConcentrationAndNormalisation(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"  Amoxicillin ","form":"Liquid","dosage":{"value":250.00,"unit":"MG","per_value":5,"per_unit":"ml"}}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(),
		`"name":"Amoxicillin","dosage":{"value":250,"unit":"mg","per_value":5,"per_unit":"mL"},"form":"liquid"`) {
		t.Errorf("not normalised: %s", rec.Body.String())
	}
}

func TestCreate_ValidationIs422WithAllFieldErrors(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"","form":"syrup","dosage":{"value":0.123456,"unit":"mg","per_value":5}}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	p := decodeBody[httpx.Problem](t, rec)
	got := map[string]string{}
	for _, f := range p.Errors {
		got[f.Field] = f.Message
	}
	for _, field := range []string{"name", "form", "dosage.value", "dosage.per_unit"} {
		if got[field] == "" {
			t.Errorf("missing field error for %s in %+v", field, p.Errors)
		}
	}
	if api.repo.calls != 0 {
		t.Error("repository must not be called for invalid input")
	}
}

func TestCreate_DecodeProblems(t *testing.T) {
	tests := []struct {
		name, body, contentType string
		want                    int
	}{
		{"malformed JSON", `{"name":`, "application/json", 400},
		{"unknown field", `{"name":"X","form":"tablet","dosage":{"value":1,"unit":"mg"},"colour":"red"}`, "application/json", 400},
		{"value as bool", `{"name":"X","form":"tablet","dosage":{"value":true,"unit":"mg"}}`, "application/json", 400},
		{"wrong content type", paracetamolJSON, "text/plain", 415},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newTestAPI(t)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/medications", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			rec := httptest.NewRecorder()
			api.mux.ServeHTTP(rec, r)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if rec.Header().Get("Content-Type") != httpx.ProblemContentType {
				t.Error("error responses must be problem+json")
			}
		})
	}
}

func TestCreate_ExponentNumberIsRejectedAs422(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"X","form":"tablet","dosage":{"value":5e2,"unit":"mg"}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreate_Duplicate409(t *testing.T) {
	api := newTestAPI(t)
	if rec := api.do(t, http.MethodPost, "/v1/medications", paracetamolJSON); rec.Code != http.StatusCreated {
		t.Fatalf("first create: %d", rec.Code)
	}
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"PARACETAMOL","form":"Tablet","dosage":{"value":500.0,"unit":"MG"}}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if p := decodeBody[httpx.Problem](t, rec); p.Status != 409 || p.Detail == "" {
		t.Errorf("problem = %+v", p)
	}
}

func TestCreate_WithIdentifiers(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"Amoxicillin","form":"capsule","dosage":{"value":500,"unit":"mg"},
		  "ndc":"0093-3109-01","rxcui":"308191","route":"Oral"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ndc":"00093-3109-01","rxcui":"308191","route":"oral"`) {
		t.Errorf("identifiers not normalized: %s", rec.Body.String())
	}
}

func TestCreate_DuplicateNDCHasItsOwnDetail(t *testing.T) {
	api := newTestAPI(t)
	first := `{"name":"Amoxicillin","form":"capsule","dosage":{"value":500,"unit":"mg"},"ndc":"0093-3109-01"}`
	// Different identity (strength), same package code written in another layout.
	second := `{"name":"Amoxicillin","form":"capsule","dosage":{"value":250,"unit":"mg"},"ndc":"00093310901"}`

	if rec := api.do(t, http.MethodPost, "/v1/medications", first); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := api.do(t, http.MethodPost, "/v1/medications", second)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if p := decodeBody[httpx.Problem](t, rec); !strings.Contains(p.Detail, "NDC") {
		t.Errorf("detail should name the NDC conflict: %q", p.Detail)
	}
}

func TestCreate_IdentifierValidationIs422(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"X","form":"tablet","dosage":{"value":1,"unit":"mg"},"ndc":"1234567890","rxcui":"abc","route":"by mouth"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d", rec.Code)
	}
	p := decodeBody[httpx.Problem](t, rec)
	if len(p.Errors) != 3 {
		t.Errorf("want 3 field errors (ndc, rxcui, route), got %+v", p.Errors)
	}
}

func TestCreate_InternalErrorDoesNotLeak(t *testing.T) {
	api := newTestAPI(t)
	api.repo.err = errors.New(`pq: relation "medications" password=hunter2`)
	rec := api.do(t, http.MethodPost, "/v1/medications", paracetamolJSON)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "relation") {
		t.Errorf("internal error leaked: %s", rec.Body.String())
	}
	if !strings.Contains(api.logs.String(), "hunter2") {
		t.Error("internal error must be logged for operators")
	}
}

func TestCreate_ClientCancelledIsNotLoggedAsError(t *testing.T) {
	api := newTestAPI(t)
	api.repo.err = context.Canceled
	rec := api.do(t, http.MethodPost, "/v1/medications", paracetamolJSON)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(api.logs.String(), `"level":"ERROR"`) {
		t.Errorf("cancellation logged as error: %s", api.logs.String())
	}
}

func TestGet(t *testing.T) {
	api := newTestAPI(t)
	created := api.do(t, http.MethodPost, "/v1/medications", paracetamolJSON)
	path := created.Header().Get("Location")

	t.Run("200 with ETag and same body as create", func(t *testing.T) {
		rec := api.do(t, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if rec.Header().Get("ETag") != `"1"` || rec.Header().Get("Cache-Control") != "private, no-cache" {
			t.Errorf("headers = %v", rec.Header())
		}
		if rec.Body.String() != created.Body.String() {
			t.Errorf("GET body differs from POST body:\n%s\n%s", rec.Body.String(), created.Body.String())
		}
	})

	t.Run("304 when If-None-Match matches", func(t *testing.T) {
		rec := api.do(t, http.MethodGet, path, "", "If-None-Match", `W/"1"`)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("ETag") != `"1"` {
			t.Error("304 must repeat the ETag")
		}
	})

	t.Run("200 when If-None-Match is stale", func(t *testing.T) {
		if rec := api.do(t, http.MethodGet, path, "", "If-None-Match", `"0"`); rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("404 for unknown id", func(t *testing.T) {
		rec := api.do(t, http.MethodGet, "/v1/medications/019a9c3e-7a10-7000-8000-00000000ffff", "")
		if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != httpx.ProblemContentType {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("404 for malformed id, repository not called", func(t *testing.T) {
		before := api.repo.calls
		rec := api.do(t, http.MethodGet, "/v1/medications/not-a-uuid", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
		if api.repo.calls != before {
			t.Error("malformed id must not hit the repository")
		}
	})
}
