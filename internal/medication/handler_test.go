package medication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/eduardosirangelo/medication-api/internal/platform/httpx"
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

// Listing needs distinct, time-ordered IDs, so this variant keeps the real
// UUIDv7 generator instead of the fixed ID.
func newRandomIDAPI(t *testing.T) *testAPI {
	t.Helper()
	repo := newFakeRepo()
	var logs bytes.Buffer
	mux := http.NewServeMux()
	NewHandler(NewService(repo), slog.New(slog.NewJSONHandler(&logs, nil))).Register(mux)
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
		{"per_value as object", `{"name":"X","form":"tablet","dosage":{"value":1,"unit":"mg","per_value":{},"per_unit":"mL"}}`, "application/json", 400},
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

func TestQuotedNumbersAre422(t *testing.T) {
	tests := map[string]struct {
		dosage string
		name   string
		want   []string // "field: message"
	}{
		"quoted value": {`{"value":"500","unit":"mg"}`, "X", []string{
			"dosage.value: must be a JSON number, not a string"}},
		"quoted per_value": {`{"value":250,"unit":"mg","per_value":"5","per_unit":"mL"}`, "X", []string{
			"dosage.per_value: must be a JSON number, not a string"}},
		"still reports every other invalid field": {`{"value":"500","unit":"mg"}`, "", []string{
			"dosage.value: must be a JSON number, not a string", "name: is required"}},
		"a quoted field is reported once": {`{"value":"abc","unit":"mg"}`, "X", []string{
			"dosage.value: must be a JSON number, not a string"}},
		"null is absent": {`{"value":null,"unit":"mg"}`, "X", []string{
			"dosage.value: is required"}},
	}
	for name, tt := range tests {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(name+"/"+method, func(t *testing.T) {
				api := newTestAPI(t)
				path, headers := "/v1/medications", []string(nil)
				if method == http.MethodPut {
					created := decodeBody[medicationResponse](t, api.do(t, http.MethodPost, path, paracetamolJSON))
					path, headers = path+"/"+created.ID, []string{"If-Match", `"1"`}
				}
				body := fmt.Sprintf(`{"name":%q,"form":"tablet","dosage":%s}`, tt.name, tt.dosage)
				rec := api.do(t, method, path, body, headers...)

				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d: %s", rec.Code, rec.Body)
				}
				var got []string
				for _, f := range decodeBody[httpx.Problem](t, rec).Errors {
					got = append(got, f.Field+": "+f.Message)
				}
				if !slices.Equal(got, tt.want) {
					t.Errorf("errors = %q, want %q", got, tt.want)
				}
				if api.repo.calls > 1 || (method == http.MethodPost && api.repo.calls != 0) {
					t.Errorf("repository called %d times for invalid input", api.repo.calls)
				}
			})
		}
	}
}

// Documented behavior, not an endorsement: encoding/json keeps the last of
// duplicate keys. The test exists so a decoder change can't alter it silently.
func TestCreate_WrongJSONTypeNamesTheField(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications", `{"name":"X","form":"tablet","dosage":{"value":[1],"unit":"mg"}}`)
	want := `Field "dosage.value" has the wrong JSON type (got array).`
	if rec.Code != http.StatusBadRequest || decodeBody[httpx.Problem](t, rec).Detail != want {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestCreate_DuplicateKeysLastOneWins(t *testing.T) {
	api := newTestAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"First","name":"Second","form":"tablet","dosage":{"value":5,"unit":"mg"}}`)
	if rec.Code != http.StatusCreated || decodeBody[medicationResponse](t, rec).Name != "Second" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
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

func TestTimeoutIs503WithRetryAfter(t *testing.T) {
	const id = "/v1/medications/019a9c3e-7a10-7000-8000-000000000001"
	routes := []struct{ method, path, body, ifMatch string }{
		{http.MethodPost, "/v1/medications", paracetamolJSON, ""},
		{http.MethodGet, "/v1/medications", "", ""},
		{http.MethodGet, id, "", ""},
		{http.MethodPut, id, paracetamolJSON, `"1"`},
		{http.MethodDelete, id, "", ""},
	}
	causes := map[string]error{
		"statement timeout": fmt.Errorf("select medication: %w: %w", ErrTimeout, errors.New("ERROR: canceling statement (SQLSTATE 57014)")),
		"request deadline":  fmt.Errorf("select medication: %w", context.DeadlineExceeded),
	}
	for name, cause := range causes {
		for _, rt := range routes {
			t.Run(name+"/"+rt.method+" "+rt.path, func(t *testing.T) {
				api := newTestAPI(t)
				api.repo.err = cause
				var headers []string
				if rt.ifMatch != "" {
					headers = []string{"If-Match", rt.ifMatch}
				}
				rec := api.do(t, rt.method, rt.path, rt.body, headers...)

				if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" ||
					rec.Header().Get("Content-Type") != httpx.ProblemContentType {
					t.Fatalf("status %d, Retry-After %q, Content-Type %q",
						rec.Code, rec.Header().Get("Retry-After"), rec.Header().Get("Content-Type"))
				}
				if logs := api.logs.String(); !strings.Contains(logs, `"level":"WARN"`) || strings.Contains(logs, `"level":"ERROR"`) {
					t.Errorf("want one WARN and no ERROR, got %s", logs)
				}
			})
		}
	}
}

// pgx can report a query cancelled because the client left as a plain
// PostgreSQL error; the request context still says what happened.
func TestClientGoneWithDriverErrorIsNotAnError(t *testing.T) {
	api := newTestAPI(t)
	api.repo.err = errors.New("ERROR: canceling statement due to user request (SQLSTATE 57014)")
	ctx, disconnect := context.WithCancel(t.Context())
	disconnect()
	rec := httptest.NewRecorder()
	api.mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/medications", nil))

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("status %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if strings.Contains(api.logs.String(), `"level":"ERROR"`) {
		t.Errorf("client disconnect logged as error: %s", api.logs.String())
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

type listBody struct {
	Data       []medicationResponse `json:"data"`
	NextCursor *string              `json:"next_cursor"`
}

func seed(t *testing.T, api *testAPI, n int, form string) {
	t.Helper()
	for i := range n {
		body := `{"name":"Med ` + string(rune('A'+i)) + `","form":"` + form + `","dosage":{"value":` + strconv.Itoa(i+1) + `,"unit":"mg"}}`
		if rec := api.do(t, http.MethodPost, "/v1/medications", body); rec.Code != http.StatusCreated {
			t.Fatalf("seed %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestList_EmptyIsArrayAndNullCursor(t *testing.T) {
	api := newRandomIDAPI(t)
	rec := api.do(t, http.MethodGet, "/v1/medications", "")
	if rec.Code != http.StatusOK || rec.Body.String() != `{"data":[],"next_cursor":null}` {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Link") != "" {
		t.Error("no Link header on the last page")
	}
}

func TestList_WalkAllPagesViaLinkHeader(t *testing.T) {
	api := newRandomIDAPI(t)
	seed(t, api, 5, "tablet")
	seed(t, api, 2, "capsule")

	var names []string
	next := "/v1/medications?form=tablet&limit=2"
	for pages := 0; next != ""; pages++ {
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		rec := api.do(t, http.MethodGet, next, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", next, rec.Code, rec.Body.String())
		}
		body := decodeBody[listBody](t, rec)
		for _, m := range body.Data {
			names = append(names, m.Name)
		}
		next = linkTarget(t, rec.Header().Get("Link"))
		if (body.NextCursor == nil) != (next == "") {
			t.Fatalf("next_cursor and Link disagree: %v / %q", body.NextCursor, next)
		}
	}
	// Newest first, every tablet exactly once, no capsules.
	want := []string{"Med E", "Med D", "Med C", "Med B", "Med A"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func linkTarget(t *testing.T, header string) string {
	t.Helper()
	if header == "" {
		return ""
	}
	target, rel, ok := strings.Cut(header, ">; ")
	if !ok || rel != `rel="next"` || !strings.HasPrefix(target, "<") {
		t.Fatalf("malformed Link header %q", header)
	}
	return strings.TrimPrefix(target, "<")
}

func TestList_NameFilterIsCaseInsensitiveSubstring(t *testing.T) {
	api := newRandomIDAPI(t)
	for _, body := range []string{
		`{"name":"Amoxicillin","form":"capsule","dosage":{"value":500,"unit":"mg"}}`,
		`{"name":"Co-Amoxiclav","form":"tablet","dosage":{"value":625,"unit":"mg"}}`,
		`{"name":"Ibuprofen","form":"tablet","dosage":{"value":200,"unit":"mg"}}`,
	} {
		api.do(t, http.MethodPost, "/v1/medications", body)
	}
	body := decodeBody[listBody](t, api.do(t, http.MethodGet, "/v1/medications?name=AMOX", ""))
	if len(body.Data) != 2 {
		t.Fatalf("want 2 matches, got %+v", body.Data)
	}
}

func TestList_BadQueryIs400WithFieldErrors(t *testing.T) {
	api := newRandomIDAPI(t)
	rec := api.do(t, http.MethodGet, "/v1/medications?limit=1000&colour=red", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if p := decodeBody[httpx.Problem](t, rec); len(p.Errors) != 2 {
		t.Errorf("want limit and colour errors, got %+v", p.Errors)
	}
}

func TestList_CursorFromOtherFiltersIs400(t *testing.T) {
	api := newRandomIDAPI(t)
	seed(t, api, 3, "tablet")
	first := decodeBody[listBody](t, api.do(t, http.MethodGet, "/v1/medications?limit=1", ""))
	if first.NextCursor == nil {
		t.Fatal("expected a next cursor")
	}
	rec := api.do(t, http.MethodGet, "/v1/medications?limit=1&form=capsule&cursor="+*first.NextCursor, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

const ibuprofenJSON = `{"name":"Ibuprofen","form":"tablet","dosage":{"value":400,"unit":"mg"},"route":"oral"}`

func createForReplace(t *testing.T) (*testAPI, string) {
	t.Helper()
	api := newRandomIDAPI(t)
	rec := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"Paracetamol","form":"tablet","dosage":{"value":500,"unit":"mg"},"ndc":"0071-0155-23"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed: %d", rec.Code)
	}
	return api, rec.Header().Get("Location")
}

func TestReplace_200BumpsVersionAndClearsOmittedFields(t *testing.T) {
	api, path := createForReplace(t)
	rec := api.do(t, http.MethodPut, path, ibuprofenJSON, "If-Match", `"1"`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("ETag") != `"2"` {
		t.Errorf("ETag = %q", rec.Header().Get("ETag"))
	}
	body := decodeBody[medicationResponse](t, rec)
	if body.Name != "Ibuprofen" || body.Version != 2 || body.Route != "oral" || body.NDC != "" {
		t.Errorf("PUT is a full replacement (ndc omitted → cleared): %+v", body)
	}
	if !body.UpdatedAt.After(body.CreatedAt) {
		t.Error("updated_at must advance")
	}
	if got := api.do(t, http.MethodGet, path, ""); got.Body.String() != rec.Body.String() {
		t.Errorf("GET after PUT differs:\n%s\n%s", got.Body.String(), rec.Body.String())
	}
}

func TestReplace_Preconditions(t *testing.T) {
	tests := []struct {
		name    string
		ifMatch string
		want    int
	}{
		{"missing If-Match", "", http.StatusPreconditionRequired},
		{"wildcard refused", "*", http.StatusPreconditionRequired},
		{"stale version", `"7"`, http.StatusPreconditionFailed},
		{"weak tag never matches", `W/"1"`, http.StatusPreconditionFailed},
		{"garbage tag", `abc`, http.StatusPreconditionFailed},
		{"list containing current", `"9", "1"`, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, path := createForReplace(t)
			var headers []string
			if tt.ifMatch != "" {
				headers = []string{"If-Match", tt.ifMatch}
			}
			rec := api.do(t, http.MethodPut, path, ibuprofenJSON, headers...)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestReplace_SecondWriterWithSameETagGets412(t *testing.T) {
	api, path := createForReplace(t)
	if rec := api.do(t, http.MethodPut, path, ibuprofenJSON, "If-Match", `"1"`); rec.Code != http.StatusOK {
		t.Fatalf("first writer: %d", rec.Code)
	}
	rec := api.do(t, http.MethodPut, path, paracetamolJSON, "If-Match", `"1"`)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("second writer: %d", rec.Code)
	}
	if p := decodeBody[httpx.Problem](t, rec); !strings.Contains(p.Detail, "fetch it again") {
		t.Errorf("detail should tell the client how to recover: %q", p.Detail)
	}
}

func TestReplace_Errors(t *testing.T) {
	t.Run("404 unknown id", func(t *testing.T) {
		api, _ := createForReplace(t)
		rec := api.do(t, http.MethodPut, "/v1/medications/019a9c3e-7a10-7000-8000-00000000ffff", ibuprofenJSON, "If-Match", `"1"`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
	})
	t.Run("404 malformed id", func(t *testing.T) {
		api, _ := createForReplace(t)
		if rec := api.do(t, http.MethodPut, "/v1/medications/nope", ibuprofenJSON, "If-Match", `"1"`); rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d", rec.Code)
		}
	})
	t.Run("422 invalid body", func(t *testing.T) {
		api, path := createForReplace(t)
		if rec := api.do(t, http.MethodPut, path, `{"name":""}`, "If-Match", `"1"`); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d", rec.Code)
		}
	})
	t.Run("409 when taking another entry's identity", func(t *testing.T) {
		api, path := createForReplace(t)
		api.do(t, http.MethodPost, "/v1/medications", ibuprofenJSON)
		if rec := api.do(t, http.MethodPut, path, ibuprofenJSON, "If-Match", `"1"`); rec.Code != http.StatusConflict {
			t.Fatalf("status = %d", rec.Code)
		}
	})
	t.Run("keeping its own identity and NDC is not a conflict", func(t *testing.T) {
		api, path := createForReplace(t)
		same := `{"name":"PARACETAMOL","form":"tablet","dosage":{"value":500,"unit":"mg"},"ndc":"00071015523"}`
		if rec := api.do(t, http.MethodPut, path, same, "If-Match", `"1"`); rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name    string
		ifMatch string
		want    int
	}{
		{"no If-Match deletes", "", http.StatusNoContent},
		{"wildcard deletes", "*", http.StatusNoContent},
		{"current version deletes", `"1"`, http.StatusNoContent},
		{"stale version refused", `"2"`, http.StatusPreconditionFailed},
		{"weak tag refused", `W/"1"`, http.StatusPreconditionFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, path := createForReplace(t)
			var headers []string
			if tt.ifMatch != "" {
				headers = []string{"If-Match", tt.ifMatch}
			}
			rec := api.do(t, http.MethodDelete, path, "", headers...)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			stillThere := api.do(t, http.MethodGet, path, "").Code == http.StatusOK
			if deleted := tt.want == http.StatusNoContent; deleted == stillThere {
				t.Errorf("deleted=%v but GET still finds it=%v", deleted, stillThere)
			}
			if tt.want == http.StatusNoContent && rec.Body.Len() != 0 {
				t.Errorf("204 must have no body: %q", rec.Body.String())
			}
		})
	}
}

func TestDelete_GoneEverywhereAndCanBeRecreated(t *testing.T) {
	api, path := createForReplace(t)
	if rec := api.do(t, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}

	checks := []struct {
		name string
		rec  *httptest.ResponseRecorder
		want int
	}{
		{"second DELETE", api.do(t, http.MethodDelete, path, ""), http.StatusNotFound},
		{"GET", api.do(t, http.MethodGet, path, ""), http.StatusNotFound},
		{"PUT", api.do(t, http.MethodPut, path, ibuprofenJSON, "If-Match", `"1"`), http.StatusNotFound},
		{"malformed id", api.do(t, http.MethodDelete, "/v1/medications/nope", ""), http.StatusNotFound},
	}
	for _, c := range checks {
		if c.rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, c.rec.Code, c.want)
		}
	}
	if list := decodeBody[listBody](t, api.do(t, http.MethodGet, "/v1/medications", "")); len(list.Data) != 0 {
		t.Errorf("deleted medication still listed: %+v", list.Data)
	}

	again := api.do(t, http.MethodPost, "/v1/medications",
		`{"name":"Paracetamol","form":"tablet","dosage":{"value":500,"unit":"mg"},"ndc":"0071-0155-23"}`)
	if again.Code != http.StatusCreated || again.Header().Get("Location") == path {
		t.Fatalf("re-create: %d, Location %q (must be a new id)", again.Code, again.Header().Get("Location"))
	}
}
