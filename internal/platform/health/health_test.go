package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLiveness(t *testing.T) {
	rec := httptest.NewRecorder()
	Liveness(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if body := rec.Body.String(); body != `{"status":"ok"}` {
		t.Errorf("body = %q", body)
	}
}

type pingerFunc func(ctx context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReadiness(t *testing.T) {
	blockUntilCancelled := pingerFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	tests := []struct {
		name     string
		pinger   Pinger
		draining bool
		wantCode int
		wantBody string
	}{
		{"db up", pingerFunc(func(context.Context) error { return nil }), false,
			http.StatusOK, `{"status":"ok","checks":{"database":"up"}}`},
		{"db down", pingerFunc(func(context.Context) error { return errors.New("conn refused") }), false,
			http.StatusServiceUnavailable, `{"status":"unavailable","checks":{"database":"down"}}`},
		{"db hangs past timeout", blockUntilCancelled, false,
			http.StatusServiceUnavailable, `{"status":"unavailable","checks":{"database":"down"}}`},
		{"draining wins even if db up", pingerFunc(func(context.Context) error { return nil }), true,
			http.StatusServiceUnavailable, `{"status":"draining"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReadiness(tt.pinger, 20*time.Millisecond, slog.New(slog.DiscardHandler))
			if tt.draining {
				r.SetDraining()
			}
			rec := httptest.NewRecorder()
			start := time.Now()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))

			if time.Since(start) > time.Second {
				t.Error("probe must respect its timeout")
			}
			if rec.Code != tt.wantCode || rec.Body.String() != tt.wantBody {
				t.Errorf("got %d %s, want %d %s", rec.Code, rec.Body.String(), tt.wantCode, tt.wantBody)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Error("missing Cache-Control: no-store")
			}
		})
	}
}
