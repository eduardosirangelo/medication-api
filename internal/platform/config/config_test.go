package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func lookupFrom(env map[string]string) LookupFunc {
	return func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(lookupFrom(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{
		HTTP: HTTP{
			Addr:              ":8080",
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownTimeout:   20 * time.Second,
			MaxBodyBytes:      64 << 10,
		},
		LogLevel: slog.LevelInfo,
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{
		"HTTP_ADDR":                "127.0.0.1:9000",
		"HTTP_READ_HEADER_TIMEOUT": "2s",
		"HTTP_READ_TIMEOUT":        "3s",
		"HTTP_WRITE_TIMEOUT":       "4s",
		"HTTP_IDLE_TIMEOUT":        "1m",
		"SHUTDOWN_TIMEOUT":         "30s",
		"HTTP_MAX_BODY_BYTES":      "2048",
		"LOG_LEVEL":                "debug",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != "127.0.0.1:9000" ||
		cfg.HTTP.ReadHeaderTimeout != 2*time.Second ||
		cfg.HTTP.ReadTimeout != 3*time.Second ||
		cfg.HTTP.WriteTimeout != 4*time.Second ||
		cfg.HTTP.IdleTimeout != time.Minute ||
		cfg.HTTP.ShutdownTimeout != 30*time.Second ||
		cfg.HTTP.MaxBodyBytes != 2048 ||
		cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestLoad_EmptyValueUsesDefault(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{"HTTP_ADDR": "", "LOG_LEVEL": ""}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != ":8080" || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("empty values should fall back to defaults: %+v", cfg)
	}
}

func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string // substrings that must all appear in the error
	}{
		{"bad addr", map[string]string{"HTTP_ADDR": "8080"}, []string{"HTTP_ADDR"}},
		{"bad duration", map[string]string{"HTTP_READ_TIMEOUT": "ten"}, []string{"HTTP_READ_TIMEOUT"}},
		{"zero duration", map[string]string{"SHUTDOWN_TIMEOUT": "0s"}, []string{"SHUTDOWN_TIMEOUT"}},
		{"negative duration", map[string]string{"HTTP_IDLE_TIMEOUT": "-1s"}, []string{"HTTP_IDLE_TIMEOUT"}},
		{"bad body limit", map[string]string{"HTTP_MAX_BODY_BYTES": "1MB"}, []string{"HTTP_MAX_BODY_BYTES"}},
		{"zero body limit", map[string]string{"HTTP_MAX_BODY_BYTES": "0"}, []string{"HTTP_MAX_BODY_BYTES"}},
		{"bad log level", map[string]string{"LOG_LEVEL": "verbose"}, []string{"LOG_LEVEL"}},
		{
			"all errors reported together",
			map[string]string{"HTTP_ADDR": "nope", "LOG_LEVEL": "loud", "HTTP_WRITE_TIMEOUT": "x"},
			[]string{"HTTP_ADDR", "LOG_LEVEL", "HTTP_WRITE_TIMEOUT"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(lookupFrom(tt.env))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			for _, s := range tt.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q does not mention %s", err, s)
				}
			}
		})
	}
}
