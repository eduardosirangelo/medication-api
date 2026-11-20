// Command api runs the Medication API.
//
//	api [serve]   run the HTTP server (default)
//	api migrate   apply pending database migrations and exit
//	api seed      insert the sample catalog (development and demos only)
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
	"github.com/eduardo-sirangelo/medication-api/internal/seed"
	"github.com/eduardo-sirangelo/medication-api/migrations"
)

const usage = "usage: api [serve|migrate|seed]"

// Must stay below the orchestrator's probe timeout (Kubernetes defaults to
// 1s); a pooled ping normally takes well under 10ms.
const readinessTimeout = 800 * time.Millisecond

// os.Exit skips deferred calls, so it lives here and nowhere else.
func main() {
	if err := run(context.Background(), os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookup config.LookupFunc, stdout io.Writer) error {
	cmd, err := parseCommand(args)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Restoring default signal handling after the first signal lets a second
	// Ctrl+C or SIGTERM kill the process instead of waiting out the drain.
	go func() { <-ctx.Done(); stop() }()

	cfg, err := config.Load(lookup)
	if err != nil {
		return err
	}
	log := logging.New(stdout, cfg.LogLevel)
	slog.SetDefault(log)

	switch cmd {
	case "migrate":
		return migrate(ctx, cfg.DB, log)
	case "seed":
		return seedCatalog(ctx, cfg.DB, log)
	default:
		return serve(ctx, cfg, log)
	}
}

func parseCommand(args []string) (string, error) {
	switch {
	case len(args) == 0:
		return "serve", nil
	case len(args) == 1 && (args[0] == "serve" || args[0] == "migrate" || args[0] == "seed"):
		return args[0], nil
	default:
		return "", errors.New(usage)
	}
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if cfg.DB.MigrateOnStart {
		if err := migrate(ctx, cfg.DB, log); err != nil {
			return err
		}
	}

	pool, err := db.Open(ctx, cfg.DB, log)
	if err != nil {
		return err
	}
	defer pool.Close() // after Serve returns, i.e. once in-flight requests are done

	readiness := health.NewReadiness(pool, readinessTimeout, log)
	handler := newHandler(cfg, log, readiness, medication.NewPostgresRepository(pool))

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTP.Addr, err)
	}
	return httpx.Serve(ctx, httpx.NewServer(cfg.HTTP, handler, log), ln, httpx.Shutdown{
		Timeout:    cfg.HTTP.ShutdownTimeout,
		DrainDelay: cfg.HTTP.DrainDelay,
		OnDrain:    readiness.SetDraining,
	}, log)
}

func newHandler(cfg config.Config, log *slog.Logger, readiness *health.Readiness, repo medication.Repository) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Liveness)
	mux.Handle("GET /readyz", readiness)
	medication.NewHandler(medication.NewService(repo), log).Register(mux)

	// RequestID first so every log line and problem body carries the ID;
	// AccessLog outside Recover so recovered panics are logged as 500s.
	return middleware.Chain(httpx.WithProblemFallback(mux),
		middleware.RequestID(),
		middleware.AccessLog(log),
		middleware.Recover(log),
		// TODO(slice-9): API key authentication.
		middleware.MaxBodyBytes(cfg.HTTP.MaxBodyBytes),
	)
}

// Migrations get their own short-lived pool: see db.OpenForMigrations.
func migrate(ctx context.Context, cfg config.DB, log *slog.Logger) error {
	pool, err := db.OpenForMigrations(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	return db.Migrate(ctx, pool, migrations.FS, log)
}

// Seeding is never wired into serve or MIGRATE_ON_START: sample rows must
// only reach a database when someone asks for them by name.
func seedCatalog(ctx context.Context, cfg config.DB, log *slog.Logger) error {
	pool, err := db.Open(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	res, err := seed.Run(ctx, medication.NewService(medication.NewPostgresRepository(pool)), seed.Catalog, log)
	log.InfoContext(ctx, "seed finished", "inserted", res.Inserted, "skipped", res.Skipped)
	return err
}
