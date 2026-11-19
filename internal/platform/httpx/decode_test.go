package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type target struct {
	Name  string `json:"name"`
	Inner struct {
		N int `json:"n"`
	} `json:"inner"`
}

func jsonReq(t *testing.T, contentType, body string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func TestDecodeJSON_OK(t *testing.T) {
	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON; charset=UTF-8"} {
		var dst target
		if p := DecodeJSON(jsonReq(t, ct, `{"name":"x","inner":{"n":2}}`+"\n"), &dst); p != nil {
			t.Fatalf("%s: unexpected problem %+v", ct, p)
		}
		if dst.Name != "x" || dst.Inner.N != 2 {
			t.Fatalf("decoded %+v", dst)
		}
	}
}

func TestDecodeJSON_Problems(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantDetail  string // substring
	}{
		{"missing content type", "", `{}`, 415, "application/json"},
		{"wrong content type", "text/plain", `{}`, 415, "application/json"},
		{"form content type", "application/x-www-form-urlencoded", `name=x`, 415, ""},
		{"non-utf8 charset", "application/json; charset=latin1", `{}`, 415, ""},
		{"empty body", "application/json", ``, 400, "must not be empty"},
		{"syntax error", "application/json", `{"name": }`, 400, "Malformed JSON at byte offset"},
		{"truncated", "application/json", `{"name": "x"`, 400, "unexpected end"},
		{"array instead of object", "application/json", `[1,2]`, 400, "must be a JSON object"},
		{"wrong field type", "application/json", `{"name": 5}`, 400, `Field "name" has the wrong JSON type (got number)`},
		{"wrong nested type", "application/json", `{"inner": {"n": "two"}}`, 400, `Field "inner.n"`},
		{"unknown field", "application/json", `{"colour": "x"}`, 400, `Unknown field "colour"`},
		{"trailing object", "application/json", `{"name":"a"}{"name":"b"}`, 400, "single JSON object"},
		{"trailing garbage", "application/json", `{"name":"a"} xyz`, 400, "single JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dst target
			p := DecodeJSON(jsonReq(t, tt.contentType, tt.body), &dst)
			if p == nil {
				t.Fatal("expected a problem")
			}
			if p.Status != tt.wantStatus || !strings.Contains(p.Detail, tt.wantDetail) {
				t.Errorf("got %d %q, want %d containing %q", p.Status, p.Detail, tt.wantStatus, tt.wantDetail)
			}
		})
	}
}

func TestDecodeJSON_WrongTypeDoesNotEchoValue(t *testing.T) {
	var dst target
	p := DecodeJSON(jsonReq(t, "application/json", `{"name": 123456789}`), &dst)
	if p == nil || strings.Contains(p.Detail, "123456789") {
		t.Fatalf("client value must not be echoed: %+v", p)
	}
}

func TestDecodeJSON_BodyTooLarge(t *testing.T) {
	big := `{"name":"` + strings.Repeat("x", 100) + `"}`
	for _, tc := range []struct{ name, body string }{
		{"while decoding", big},
		{"in trailing data", `{"name":"a"}` + strings.Repeat(" ", 100) + "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := jsonReq(t, "application/json", tc.body)
			r.Body = http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(strings.NewReader(tc.body)), 20)
			var dst target
			p := DecodeJSON(r, &dst)
			if p == nil || p.Status != http.StatusRequestEntityTooLarge || !strings.Contains(p.Detail, "20 bytes") {
				t.Fatalf("got %+v, want 413", p)
			}
		})
	}
}
