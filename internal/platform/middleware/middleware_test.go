package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/logging"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/requestid"
)

func newReq(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), method, target, body)
}

func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatalf("bad log line %s: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) httpx.Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != httpx.ProblemContentType {
		t.Fatalf("Content-Type = %q", ct)
	}
	var p httpx.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return p
}

func TestChain_Order(t *testing.T) {
	var order []string
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "h") }),
		mk("A"), mk("B"))
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))

	if got := strings.Join(order, ","); got != "A,B,h" {
		t.Fatalf("order = %s, want A,B,h", got)
	}
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = requestid.From(r.Context())
	}))

	tests := []struct {
		name     string
		inbound  string
		wantSame bool
	}{
		{"generated when absent", "", false},
		{"valid inbound reused", "abc-123", true},
		{"invalid inbound replaced", "bad id\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newReq(t, http.MethodGet, "/", nil)
			if tt.inbound != "" {
				req.Header.Set(requestid.Header, tt.inbound)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(requestid.Header)
			if !requestid.Valid(got) {
				t.Fatalf("response ID %q invalid", got)
			}
			if seen != got {
				t.Errorf("context ID %q != header ID %q", seen, got)
			}
			if (got == tt.inbound) != tt.wantSame {
				t.Errorf("got %q, inbound %q, wantSame=%v", got, tt.inbound, tt.wantSame)
			}
		})
	}
}

func TestRecover_PanicBeforeWrite_Returns500Problem(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelInfo)
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		RequestID(), Recover(log))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq(t, http.MethodGet, "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	p := problemOf(t, rec)
	if p.RequestID == "" || p.RequestID != rec.Header().Get(requestid.Header) {
		t.Errorf("problem request_id %q mismatch", p.RequestID)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Error("panic value leaked to client")
	}
	lines := logLines(t, &buf)
	if len(lines) != 1 || lines[0]["panic"] != "boom" || lines[0]["stack"] == "" || lines[0]["request_id"] != p.RequestID {
		t.Errorf("panic log = %v", lines)
	}
}

func TestRecover_PanicAfterWrite_AbortsConnection(t *testing.T) {
	h := Recover(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		panic("late boom")
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // identity check
			t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
	t.Fatal("expected re-panic")
}

func TestRecover_ErrAbortHandlerPassesThrough(t *testing.T) {
	h := Recover(slog.New(slog.DiscardHandler))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // identity check
			t.Fatalf("recovered %v, want http.ErrAbortHandler", v)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/", nil))
}

func TestAccessLog(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		status    int
		body      string
		wantLevel string
		wantLog   bool
	}{
		{"ok request at info", "/v1/x", http.StatusCreated, "hello", "INFO", true},
		{"server error at error", "/v1/x", http.StatusServiceUnavailable, "", "ERROR", true},
		{"client error at info", "/v1/x", http.StatusNotFound, "", "INFO", true},
		{"probe hidden at info level", "/healthz", http.StatusOK, "ok", "", false},
		{"probe 503 (draining) not an error", "/readyz", http.StatusServiceUnavailable, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := logging.New(&buf, slog.LevelInfo)
			h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}), RequestID(), AccessLog(log))

			req := newReq(t, http.MethodPost, tt.path+"?name=secret", nil)
			h.ServeHTTP(httptest.NewRecorder(), req)

			lines := logLines(t, &buf)
			if !tt.wantLog {
				if len(lines) != 0 {
					t.Fatalf("expected no log, got %v", lines)
				}
				return
			}
			if len(lines) != 1 {
				t.Fatalf("want 1 line, got %v", lines)
			}
			l := lines[0]
			if l["level"] != tt.wantLevel || l["method"] != "POST" || l["path"] != tt.path ||
				l["status"] != float64(tt.status) || l["bytes"] != float64(len(tt.body)) ||
				l["request_id"] == nil || l["duration_ms"] == nil {
				t.Errorf("log line = %v", l)
			}
			if strings.Contains(buf.String(), "secret") {
				t.Error("query string must not be logged")
			}
		})
	}
}

func TestAccessLog_DefaultsTo200WhenNothingWritten(t *testing.T) {
	var buf bytes.Buffer
	h := AccessLog(logging.New(&buf, slog.LevelInfo))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/v1/x", nil))
	if l := logLines(t, &buf); len(l) != 1 || l[0]["status"] != float64(200) {
		t.Fatalf("log = %v", l)
	}
}

func TestAccessLog_LogsRecoveredPanicAs500(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelInfo)
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("x") }),
		AccessLog(log), Recover(log))
	h.ServeHTTP(httptest.NewRecorder(), newReq(t, http.MethodGet, "/v1/x", nil))

	lines := logLines(t, &buf)
	last := lines[len(lines)-1]
	if last["msg"] != "http request" || last["status"] != float64(500) {
		t.Fatalf("access log = %v", last)
	}
}

func TestMaxBodyBytes(t *testing.T) {
	const limit = 10
	readAll := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	h := MaxBodyBytes(limit)(readAll)

	t.Run("within limit", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newReq(t, http.MethodPost, "/", strings.NewReader("0123456789")))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("declared length over limit rejected before handler", func(t *testing.T) {
		called := false
		h := MaxBodyBytes(limit)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newReq(t, http.MethodPost, "/", strings.NewReader("01234567890")))
		if rec.Code != http.StatusRequestEntityTooLarge || called {
			t.Fatalf("status = %d, handler called = %v", rec.Code, called)
		}
		if p := problemOf(t, rec); p.Status != 413 {
			t.Errorf("problem = %+v", p)
		}
	})

	t.Run("unknown length over limit fails on read", func(t *testing.T) {
		req := newReq(t, http.MethodPost, "/", io.NopCloser(strings.NewReader(strings.Repeat("x", 50))))
		req.ContentLength = -1 // chunked / unknown
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d", rec.Code)
		}
	})

	t.Run("no body is fine", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newReq(t, http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
	})
}
