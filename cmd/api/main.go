// Command api runs the Medication API HTTP server.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/health"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/logging"
)

func main() {
	if err := run(context.Background(), os.LookupEnv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// run wires the application and blocks until SIGINT/SIGTERM triggers a
// graceful shutdown. Dependencies are passed in so it stays testable.
func run(ctx context.Context, lookup config.LookupFunc, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}
	log := logging.New(stdout, cfg.LogLevel)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Liveness)
	// TODO(slice-2): middleware chain (request ID, recovery, access log, body limit).
	// TODO(slice-3): GET /readyz, database pool, migrations.

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTP.Addr, err)
	}

	srv := httpx.NewServer(cfg.HTTP, mux, log)
	return httpx.Serve(ctx, srv, ln, cfg.HTTP.ShutdownTimeout, log)
}
