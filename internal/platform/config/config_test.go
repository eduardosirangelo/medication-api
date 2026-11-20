package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testDSN = "postgres://u:s3cret@localhost:5432/db"

// lookupFrom builds a LookupFunc; DATABASE_URL is set unless overridden.
func lookupFrom(env map[string]string) LookupFunc {
	merged := map[string]string{"DATABASE_URL": testDSN}
	for k, v := range env {
		merged[k] = v
	}
	return func(k string) (string, bool) {
		v, ok := merged[k]
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
			DrainDelay:        5 * time.Second,
			MaxBodyBytes:      64 << 10,
		},
		DB: DB{
			URL:              Secret(testDSN),
			MaxConns:         10,
			MinConns:         0,
			MaxConnLifetime:  30 * time.Minute,
			MaxConnIdleTime:  5 * time.Minute,
			ConnectTimeout:   5 * time.Second,
			StatementTimeout: 5 * time.Second,
			MigrateOnStart:   false,
		},
		Pricing:  Pricing{Budget: 300 * time.Millisecond},
		LogLevel: slog.LevelInfo,
	}
	if !reflect.DeepEqual(cfg, want) {
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
		"SHUTDOWN_DRAIN_DELAY":     "0s",
		"HTTP_MAX_BODY_BYTES":      "2048",
		"DB_MAX_CONNS":             "20",
		"DB_MIN_CONNS":             "2",
		"DB_MAX_CONN_LIFETIME":     "1h",
		"DB_MAX_CONN_IDLE_TIME":    "1m",
		"DB_CONNECT_TIMEOUT":       "2s",
		"DB_STATEMENT_TIMEOUT":     "500ms",
		"MIGRATE_ON_START":         "true",
		"PRICING_BUDGET":           "150ms",
		"LOG_LEVEL":                "debug",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	h, d := cfg.HTTP, cfg.DB
	if h.Addr != "127.0.0.1:9000" || h.ReadHeaderTimeout != 2*time.Second || h.ReadTimeout != 3*time.Second ||
		h.WriteTimeout != 4*time.Second || h.IdleTimeout != time.Minute || h.ShutdownTimeout != 30*time.Second ||
		h.DrainDelay != 0 || h.MaxBodyBytes != 2048 {
		t.Errorf("HTTP overrides not applied: %+v", h)
	}
	if d.MaxConns != 20 || d.MinConns != 2 || d.MaxConnLifetime != time.Hour || d.MaxConnIdleTime != time.Minute ||
		d.ConnectTimeout != 2*time.Second || d.StatementTimeout != 500*time.Millisecond || !d.MigrateOnStart {
		t.Errorf("DB overrides not applied: %+v", d)
	}
	if cfg.Pricing.Budget != 150*time.Millisecond {
		t.Errorf("Pricing.Budget = %v", cfg.Pricing.Budget)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
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
		{"missing database url", map[string]string{"DATABASE_URL": ""}, []string{"DATABASE_URL: is required"}},
		{"bad addr", map[string]string{"HTTP_ADDR": "8080"}, []string{"HTTP_ADDR"}},
		{"bad duration", map[string]string{"HTTP_READ_TIMEOUT": "ten"}, []string{"HTTP_READ_TIMEOUT"}},
		{"zero duration", map[string]string{"SHUTDOWN_TIMEOUT": "0s"}, []string{"SHUTDOWN_TIMEOUT"}},
		{"negative duration", map[string]string{"HTTP_IDLE_TIMEOUT": "-1s"}, []string{"HTTP_IDLE_TIMEOUT"}},
		{"negative drain delay", map[string]string{"SHUTDOWN_DRAIN_DELAY": "-1s"}, []string{"SHUTDOWN_DRAIN_DELAY"}},
		{"bad body limit", map[string]string{"HTTP_MAX_BODY_BYTES": "1MB"}, []string{"HTTP_MAX_BODY_BYTES"}},
		{"zero body limit", map[string]string{"HTTP_MAX_BODY_BYTES": "0"}, []string{"HTTP_MAX_BODY_BYTES"}},
		{"zero max conns", map[string]string{"DB_MAX_CONNS": "0"}, []string{"DB_MAX_CONNS"}},
		{"overflow max conns", map[string]string{"DB_MAX_CONNS": "99999999999"}, []string{"DB_MAX_CONNS"}},
		{"negative min conns", map[string]string{"DB_MIN_CONNS": "-1"}, []string{"DB_MIN_CONNS"}},
		{"min above max", map[string]string{"DB_MIN_CONNS": "5", "DB_MAX_CONNS": "2"}, []string{"must not exceed"}},
		{"bad bool", map[string]string{"MIGRATE_ON_START": "yes please"}, []string{"MIGRATE_ON_START"}},
		{"zero pricing budget", map[string]string{"PRICING_BUDGET": "0s"}, []string{"PRICING_BUDGET"}},
		{"budget not below write timeout", map[string]string{"PRICING_BUDGET": "2s", "HTTP_WRITE_TIMEOUT": "2s"}, []string{"must be below HTTP_WRITE_TIMEOUT"}},
		{"api key without id", map[string]string{"API_KEYS": "0123456789abcdef"}, []string{"API_KEYS: entry 1"}},
		{"api key id with spaces", map[string]string{"API_KEYS": "my app:0123456789abcdef"}, []string{"API_KEYS: entry 1"}},
		{"short api key", map[string]string{"API_KEYS": "app:short"}, []string{`key "app": must be at least 16`}},
		{"duplicate api key id", map[string]string{"API_KEYS": "app:0123456789abcdef,app:fedcba9876543210"}, []string{"duplicate id"}},
		{"reused api key secret", map[string]string{"API_KEYS": "a:0123456789abcdef,b:0123456789abcdef"}, []string{`key "b": reuses`}},
		{"bad log level", map[string]string{"LOG_LEVEL": "verbose"}, []string{"LOG_LEVEL"}},
		{
			"all errors reported together",
			map[string]string{"HTTP_ADDR": "nope", "LOG_LEVEL": "loud", "HTTP_WRITE_TIMEOUT": "x", "DATABASE_URL": ""},
			[]string{"HTTP_ADDR", "LOG_LEVEL", "HTTP_WRITE_TIMEOUT", "DATABASE_URL"},
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

func TestLoad_APIKeys(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{
		"API_KEYS": " partner-a:0123456789abcdef , ops_cli:with:colons:inside-ok ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := []APIKey{
		{ID: "partner-a", Key: "0123456789abcdef"},
		{ID: "ops_cli", Key: "with:colons:inside-ok"}, // split on the first colon only
	}
	if !reflect.DeepEqual(cfg.Auth.Keys, want) {
		t.Fatalf("keys = %#v", cfg.Auth.Keys)
	}
}

func TestLoad_APIKeyErrorsNeverEchoTheKey(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{"API_KEYS": "a:topsecret-short,:topsecret-0123456789,b:topsecret-0123456789,c:topsecret-0123456789"}))
	if err == nil || strings.Contains(err.Error(), "topsecret") {
		t.Fatalf("err = %v", err)
	}
}

// The DSN and the API keys are secrets: they must not leak through any printing path.
func TestSecret_NeverPrinted(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{"API_KEYS": "app:s3cret-0123456789"}))
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("cfg", "db_url", cfg.DB.URL, "cfg", cfg)
	jsonOut, _ := json.Marshal(cfg)

	outputs := map[string]string{
		"%v":   fmt.Sprintf("%v", cfg),
		"%+v":  fmt.Sprintf("%+v", cfg),
		"%#v":  fmt.Sprintf("%#v", cfg),
		"Str":  cfg.DB.URL.String(),
		"slog": logBuf.String(),
		"json": string(jsonOut),
	}
	for name, out := range outputs {
		if strings.Contains(out, "s3cret") {
			t.Errorf("%s leaked the secret: %s", name, out)
		}
	}
	if cfg.DB.URL.Reveal() != testDSN {
		t.Error("Reveal must return the real value")
	}
}
