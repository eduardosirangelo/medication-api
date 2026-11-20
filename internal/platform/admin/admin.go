// Package admin serves operational endpoints on their own listener, off by
// default. It has no authentication: protect it by binding to loopback or a
// private network, never by exposing it next to the API.
package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

// CPU profiles and traces stream for ?seconds=N (30 by default), so the write
// timeout must leave room for that, unlike the API's.
const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 2 * time.Minute
	shutdownTimeout   = 5 * time.Second
)

// Handler registers pprof on a private mux. Importing net/http/pprof also
// registers it on http.DefaultServeMux, which nothing in this service serves.
func Handler() http.Handler {
	mux := http.NewServeMux()
	// No method in the patterns: symbol takes GET and POST, and ServeMux
	// rejects mixing a GET prefix with a method-less path under it.
	mux.HandleFunc("/debug/pprof/", pprof.Index) // also serves heap, goroutine, allocs, ...
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return mux
}

// Serve blocks until ctx is done, then shuts down. There is no drain delay:
// no load balancer routes to this listener.
func Serve(ctx context.Context, ln net.Listener, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	log.Info("admin server listening", slog.String("addr", ln.Addr().String()))

	select {
	case err := <-serveErr:
		return fmt.Errorf("admin server: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("admin server shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("admin server: %w", err)
	}
	return nil
}
