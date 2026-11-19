// Package httpx contains HTTP plumbing shared by all handlers: server
// lifecycle, problem+json errors, strict JSON decoding and ETags.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
)

const maxHeaderBytes = 1 << 20

// Go's zero-value server has no timeouts, which leaves it open to
// slowloris-style connection exhaustion.
func NewServer(cfg config.HTTP, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Shutdown configures how Serve stops: first OnDrain and a DrainDelay while
// still serving (so load balancers notice the failing readiness probe), then
// up to Timeout for in-flight requests before connections are forced closed.
type Shutdown struct {
	Timeout    time.Duration
	DrainDelay time.Duration
	OnDrain    func()
}

// Serve blocks until ctx is cancelled and the server has shut down. It
// returns nil after a clean shutdown.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, sd Shutdown, log *slog.Logger) error {
	serveErr := make(chan error, 1) // buffered: the goroutine must never block if nobody reads
	go func() { serveErr <- srv.Serve(ln) }()
	log.Info("http server listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	if err := drain(sd, serveErr, log); err != nil {
		return err
	}
	return shutdown(ctx, srv, sd.Timeout, serveErr, log)
}

func drain(sd Shutdown, serveErr <-chan error, log *slog.Logger) error {
	if sd.OnDrain != nil {
		sd.OnDrain()
	}
	if sd.DrainDelay <= 0 {
		return nil
	}
	log.Info("draining: readiness failing, still serving", slog.String("delay", sd.DrainDelay.String()))
	select {
	case <-time.After(sd.DrainDelay):
		return nil
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	}
}

func shutdown(ctx context.Context, srv *http.Server, timeout time.Duration, serveErr <-chan error, log *slog.Logger) error {
	log.Info("shutdown started", slog.String("timeout", timeout.String()))

	// ctx is already cancelled; deriving the deadline from it unchanged would
	// make Shutdown give up immediately.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	log.Info("shutdown complete")
	return nil
}
