// Package httpx contains HTTP server plumbing shared by all handlers.
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

// NewServer builds an http.Server with explicit timeouts. Go's zero-value
// server has none, which leaves it open to slowloris-style resource exhaustion.
func NewServer(cfg config.HTTP, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    1 << 20, // 1 MiB
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

// Shutdown configures how Serve stops.
type Shutdown struct {
	// Timeout bounds how long in-flight requests get to finish.
	Timeout time.Duration
	// DrainDelay is waited after OnDrain and before refusing connections, while
	// the server keeps serving normally. It gives load balancers time to notice
	// the failing readiness probe and stop routing new requests here.
	DrainDelay time.Duration
	// OnDrain is called once shutdown begins (typically Readiness.SetDraining).
	OnDrain func()
}

// Serve runs srv on ln until ctx is cancelled, then shuts down gracefully:
//
//  1. OnDrain (readiness starts failing) and wait DrainDelay, still serving;
//  2. stop accepting connections and wait up to Timeout for in-flight requests;
//  3. if they don't finish in time, force-close the remaining connections.
//
// It returns nil after a clean shutdown, or the error that stopped the server.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, sd Shutdown, log *slog.Logger) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Info("http server listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		// Serve only returns before shutdown on a real failure.
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	if sd.OnDrain != nil {
		sd.OnDrain()
	}
	if sd.DrainDelay > 0 {
		log.Info("draining: readiness failing, still serving", slog.String("delay", sd.DrainDelay.String()))
		select {
		case <-time.After(sd.DrainDelay):
		case err := <-serveErr:
			return fmt.Errorf("http server: %w", err)
		}
	}

	log.Info("shutdown started", slog.String("timeout", sd.Timeout.String()))

	// ctx is already cancelled; the shutdown deadline must be independent of it.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sd.Timeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close() // deadline exceeded: drop remaining connections
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	log.Info("shutdown complete")
	return nil
}
