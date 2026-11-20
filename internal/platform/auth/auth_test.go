package auth

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/logging"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/middleware"
)

const (
	partnerKey = "partner-secret-0123456789"
	opsKey     = "ops-secret-9876543210abc"
)

var testKeys = []config.APIKey{
	{ID: "partner-a", Key: partnerKey},
	{ID: "ops", Key: opsKey},
}

func TestKeyring_Identify(t *testing.T) {
	ring := NewKeyring(testKeys)
	tests := map[string]struct {
		presented string
		wantID    string
		wantOK    bool
	}{
		"first key":           {partnerKey, "partner-a", true},
		"second key":          {opsKey, "ops", true},
		"wrong key":           {"partner-secret-0123456780", "", false},
		"prefix of a key":     {partnerKey[:10], "", false},
		"key with extra byte": {partnerKey + "x", "", false},
		"empty":               {"", "", false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			id, ok := ring.Identify(tt.presented)
			if id != tt.wantID || ok != tt.wantOK {
				t.Errorf("Identify = %q, %v", id, ok)
			}
		})
	}
}

func TestKeyring_EmptyDeniesEverything(t *testing.T) {
	if _, ok := NewKeyring(nil).Identify(""); ok {
		t.Fatal("empty keyring accepted a key")
	}
}

// serve runs the real AccessLog around Require so the test sees the same
// log line production writes.
func serve(t *testing.T, ring *Keyring, path, key string) (*httptest.ResponseRecorder, map[string]any, bool) {
	t.Helper()
	var buf bytes.Buffer
	reached := false
	h := middleware.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }),
		middleware.AccessLog(logging.New(&buf, slog.LevelDebug)),
		Require(ring, "/healthz", "/readyz", "/version"),
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set(Header, key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("log: %v (%s)", err, buf.String())
	}
	if strings.Contains(buf.String(), partnerKey) || strings.Contains(buf.String(), "wrong-key") {
		t.Errorf("a key reached the log: %s", buf.String())
	}
	return rec, line, reached
}

func TestRequire_ValidKeyPassesAndLogsItsID(t *testing.T) {
	rec, line, reached := serve(t, NewKeyring(testKeys), "/v1/medications", partnerKey)
	if !reached || rec.Code != http.StatusOK {
		t.Fatalf("reached = %v, status %d", reached, rec.Code)
	}
	if line["key_id"] != "partner-a" {
		t.Errorf("log = %v", line)
	}
}

func TestRequire_Rejects(t *testing.T) {
	tests := map[string]struct {
		ring       *Keyring
		path, key  string
		wantReason string
	}{
		"missing key":                  {NewKeyring(testKeys), "/v1/medications", "", "missing"},
		"wrong key":                    {NewKeyring(testKeys), "/v1/medications", "wrong-key-0123456789", "invalid"},
		"no keys configured":           {NewKeyring(nil), "/v1/medications", partnerKey, "invalid"},
		"unknown path is protected":    {NewKeyring(testKeys), "/admin", "", "missing"},
		"public path is exact match":   {NewKeyring(testKeys), "/healthz/extra", "", "missing"},
		"trailing slash is not public": {NewKeyring(testKeys), "/readyz/", "", "missing"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec, line, reached := serve(t, tt.ring, tt.path, tt.key)
			if reached {
				t.Fatal("handler reached without a valid key")
			}
			if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != httpx.ProblemContentType {
				t.Fatalf("status %d, %s", rec.Code, rec.Header().Get("Content-Type"))
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "ApiKey ") {
				t.Errorf("WWW-Authenticate = %q", got)
			}
			if line["auth_failure"] != tt.wantReason || line["key_id"] != nil {
				t.Errorf("log = %v", line)
			}
		})
	}
}

func TestRequire_PublicPathsNeedNoKey(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/version"} {
		if rec, _, reached := serve(t, NewKeyring(nil), path, ""); !reached || rec.Code != http.StatusOK {
			t.Errorf("%s: reached = %v, status %d", path, reached, rec.Code)
		}
	}
}
