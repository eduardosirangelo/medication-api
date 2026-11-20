package buildinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	got := fromBuildInfo(&debug.BuildInfo{
		GoVersion: "go1.27.4",
		Main:      debug.Module{Version: "v0.0.0-20261120150000-abcdef123456+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abcdef1234567890"},
			{Key: "vcs.time", Value: "2026-11-20T15:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
			{Key: "GOOS", Value: "linux"},
		},
	})
	want := Info{
		Version: "v0.0.0-20261120150000-abcdef123456+dirty", Commit: "abcdef1234567890",
		CommitTime: "2026-11-20T15:00:00Z", Modified: true, GoVersion: "go1.27.4",
	}
	if got != want {
		t.Fatalf("got %+v", got)
	}
}

func TestRead_InTestBinary(t *testing.T) {
	if info := Read(); info.GoVersion == "" {
		t.Fatalf("test binaries carry build info: %+v", info)
	}
}

func TestHandler(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(Info{Version: "v1.2.3", GoVersion: "go1.27.4"}).ServeHTTP(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/version", nil))

	var got Info
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Version != "v1.2.3" {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}
