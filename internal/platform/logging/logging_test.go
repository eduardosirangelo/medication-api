package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNew_JSONWithServiceAndLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)

	log.Debug("hidden")
	log.Info("hello", "k", "v")

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 line (debug filtered), got %d: %s", len(lines), buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal(lines[0], &rec); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for k, want := range map[string]string{"msg": "hello", "level": "INFO", "service": ServiceName, "k": "v"} {
		if rec[k] != want {
			t.Errorf("%s = %v, want %q", k, rec[k], want)
		}
	}
}
