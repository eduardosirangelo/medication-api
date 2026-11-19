package httpx

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
)

var discard = slog.New(slog.DiscardHandler)

func testHTTPConfig() config.HTTP {
	return config.HTTP{
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       time.Second,
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func get(ctx context.Context, t *testing.T, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	return http.DefaultClient.Do(req)
}

// An in-flight request must complete even though shutdown starts mid-request.
func TestServe_GracefulShutdownWaitsForInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "done")
	})

	ln := listen(t)
	srv := NewServer(testHTTPConfig(), h, discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, ln, 5*time.Second, discard) }()

	type result struct {
		body string
		err  error
	}
	respCh := make(chan result, 1)
	go func() {
		resp, err := get(context.Background(), t, "http://"+ln.Addr().String())
		if err != nil {
			respCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		respCh <- result{string(b), err}
	}()

	<-started
	cancel() // begin shutdown while the request is still running

	// New connections must be refused once shutdown has begun.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(context.Background(), "tcp", ln.Addr().String())
		if err != nil {
			break
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatal("listener still accepting after shutdown started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(release)
	r := <-respCh
	if r.err != nil || r.body != "done" {
		t.Fatalf("in-flight request: body=%q err=%v", r.body, r.err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve returned %v, want nil", err)
	}
}

// If handlers outlive the shutdown timeout, Serve reports it instead of hanging.
func TestServe_ShutdownTimeout(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	h := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(started)
		<-block
	})

	ln := listen(t)
	srv := NewServer(testHTTPConfig(), h, discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, srv, ln, 50*time.Millisecond, discard) }()

	reqCtx, reqCancel := context.WithCancel(context.Background())
	t.Cleanup(reqCancel)
	go func() {
		if resp, err := get(reqCtx, t, "http://"+ln.Addr().String()); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Serve returned %v, want deadline exceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after shutdown timeout")
	}
}

// A listener failure (not caused by shutdown) is returned as an error.
func TestServe_ListenerFailure(t *testing.T) {
	ln := listen(t)
	_ = ln.Close()
	srv := NewServer(testHTTPConfig(), http.NotFoundHandler(), discard)

	err := Serve(context.Background(), srv, ln, time.Second, discard)
	if err == nil {
		t.Fatal("expected error from closed listener")
	}
}

func TestNewServer_SetsTimeouts(t *testing.T) {
	cfg := testHTTPConfig()
	cfg.Addr = ":1234"
	srv := NewServer(cfg, http.NotFoundHandler(), discard)
	if srv.Addr != ":1234" || srv.ReadHeaderTimeout != cfg.ReadHeaderTimeout ||
		srv.ReadTimeout != cfg.ReadTimeout || srv.WriteTimeout != cfg.WriteTimeout ||
		srv.IdleTimeout != cfg.IdleTimeout || srv.MaxHeaderBytes == 0 || srv.ErrorLog == nil {
		t.Fatalf("server not fully configured: %+v", srv)
	}
}
