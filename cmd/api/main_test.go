package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/auth"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/health"
)

func TestParseCommand(t *testing.T) {
	tests := map[string]struct {
		args    []string
		want    string
		wantErr bool
	}{
		"default":    {nil, "serve", false},
		"serve":      {[]string{"serve"}, "serve", false},
		"migrate":    {[]string{"migrate"}, "migrate", false},
		"seed":       {[]string{"seed"}, "seed", false},
		"unknown":    {[]string{"drop"}, "", true},
		"extra args": {[]string{"seed", "now"}, "", true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseCommand(tt.args)
			if got != tt.want || (err != nil) != tt.wantErr {
				t.Errorf("parseCommand(%v) = %q, %v", tt.args, got, err)
			}
		})
	}
}

type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

func TestNewHandler_AuthWiring(t *testing.T) {
	const key = "wiring-test-key-0123456789"
	cfg := config.Config{
		HTTP: config.HTTP{MaxBodyBytes: 1 << 10},
		Auth: config.Auth{Keys: []config.APIKey{{ID: "test", Key: key}}},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The repository is never reached: every protected request below stops
	// at auth or at ID parsing.
	h := newHandler(cfg, log, health.NewReadiness(okPinger{}, time.Second, log), nil)

	tests := map[string]struct {
		path, key string
		want      int
	}{
		"probe without key":            {"/healthz", "", http.StatusOK},
		"readiness without key":        {"/readyz", "", http.StatusOK},
		"API without key":              {"/v1/medications", "", http.StatusUnauthorized},
		"API with wrong key":           {"/v1/medications", "nope-nope-nope-nope", http.StatusUnauthorized},
		"API with key reaches handler": {"/v1/medications/not-a-uuid", key, http.StatusNotFound},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			if tt.key != "" {
				req.Header.Set(auth.Header, tt.key)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.want, rec.Body)
			}
			if tt.want == http.StatusUnauthorized && !strings.Contains(rec.Body.String(), `"request_id"`) {
				t.Errorf("401 without request_id: auth must run inside RequestID: %s", rec.Body)
			}
		})
	}
}
