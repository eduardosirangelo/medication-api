// Command api runs the Medication API.
//
// Usage:
//
//	api [serve]   run the HTTP server (default)
//	api migrate   apply pending database migrations and exit
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/medication"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/db"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/health"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/logging"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/middleware"
	"github.com/eduardo-sirangelo/medication-api/migrations"
)

const usage = "usage: api [serve|migrate]"

// readinessTimeout must stay below the orchestrator's probe timeout
// (Kubernetes defaults to 1s); a pooled ping normally takes well under 10ms.
const readinessTimeout = 800 * time.Millisecond

func main() {
	if err := run(context.Background(), os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

// run parses the subcommand, loads config and dispatches. Dependencies are
// passed in so it stays testable; os.Exit lives only in main.
func run(ctx context.Context, args []string, lookup config.LookupFunc, stdout io.Writer) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	if len(args) > 1 || (cmd != "serve" && cmd != "migrate") {
		return errors.New(usage)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// After the first signal, restore default handling so a second Ctrl+C or
	// SIGTERM kills the process immediately instead of waiting for the drain.
	go func() { <-ctx.Done(); stop() }()

	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}
	log := logging.New(stdout, cfg.LogLevel)
	slog.SetDefault(log) // for code paths without an injected logger

	if cmd == "migrate" {
		return migrate(ctx, cfg.DB, log)
	}
	if cfg.DB.MigrateOnStart {
		if err := migrate(ctx, cfg.DB, log); err != nil {
			return err
		}
	}

	pool, err := db.Open(ctx, cfg.DB, log)
	if err != nil {
		return err
	}
	defer pool.Close() // runs after Serve returns, i.e. after in-flight requests finish

	readiness := health.NewReadiness(pool, readinessTimeout, log)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Liveness)
	mux.Handle("GET /readyz", readiness)
	medication.NewHandler(
		medication.NewService(medication.NewPostgresRepository(pool)), log,
	).Register(mux)

	// Outermost first. RequestID runs before everything so logs and problem
	// bodies carry the ID; AccessLog wraps Recover so recovered panics are
	// logged with their final 500 status.
	handler := middleware.Chain(httpx.WithProblemFallback(mux),
		middleware.RequestID(),
		middleware.AccessLog(log),
		middleware.Recover(log),
		// TODO(slice-9): API key authentication.
		middleware.MaxBodyBytes(cfg.HTTP.MaxBodyBytes),
	)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTP.Addr, err)
	}

	srv := httpx.NewServer(cfg.HTTP, handler, log)
	return httpx.Serve(ctx, srv, ln, httpx.Shutdown{
		Timeout:    cfg.HTTP.ShutdownTimeout,
		DrainDelay: cfg.HTTP.DrainDelay,
		OnDrain:    readiness.SetDraining,
	}, log)
}

// migrate applies pending migrations using a dedicated short-lived pool.
func migrate(ctx context.Context, cfg config.DB, log *slog.Logger) error {
	pool, err := db.OpenForMigrations(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool, migrations.FS, log)
}
