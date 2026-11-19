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

// Serve runs srv on ln until ctx is cancelled, then shuts down gracefully:
// it stops accepting new connections and waits up to shutdownTimeout for
// in-flight requests to finish before forcing connections closed.
//
// It returns nil after a clean shutdown, or the error that stopped the server.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration, log *slog.Logger) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Info("http server listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		// Serve only returns before shutdown on a real failure.
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	// TODO(slice-3): flip readiness to failing and wait a drain delay first, so
	// load balancers stop routing here before we stop accepting connections.
	log.Info("shutdown started", slog.String("timeout", shutdownTimeout.String()))

	// ctx is already cancelled; the shutdown deadline must be independent of it.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
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
