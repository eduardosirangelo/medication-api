package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/requestid"
)

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("not JSON: %v: %s", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func TestNew_JSONWithServiceAndLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)

	log.Debug("hidden")
	log.Info("hello", "k", "v")

	recs := decodeLines(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("want exactly 1 line (debug filtered), got %d: %s", len(recs), buf.String())
	}
	for k, want := range map[string]string{"msg": "hello", "level": "INFO", "service": ServiceName, "k": "v"} {
		if recs[0][k] != want {
			t.Errorf("%s = %v, want %q", k, recs[0][k], want)
		}
	}
	if _, ok := recs[0]["request_id"]; ok {
		t.Error("request_id must be absent when not in context")
	}
}

func TestNew_AddsRequestIDFromContext_EvenAfterWith(t *testing.T) {
	var buf bytes.Buffer
	// .With() must keep the context enrichment (the classic wrapper bug).
	log := New(&buf, slog.LevelInfo).With("component", "test")
	ctx := requestid.With(t.Context(), "req-42")

	log.InfoContext(ctx, "with id")
	log.WithGroup("g").InfoContext(ctx, "grouped")

	recs := decodeLines(t, &buf)
	if len(recs) != 2 {
		t.Fatalf("want 2 lines, got %d", len(recs))
	}
	if recs[0]["request_id"] != "req-42" || recs[0]["component"] != "test" {
		t.Errorf("first record missing attrs: %v", recs[0])
	}
	g, _ := recs[1]["g"].(map[string]any)
	if g == nil || g["request_id"] != "req-42" {
		t.Errorf("grouped record lost request_id: %v", recs[1])
	}
}
