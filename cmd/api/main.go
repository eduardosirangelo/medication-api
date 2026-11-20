// Command api runs the Medication API.
//
//	api [serve]   run the HTTP server (default)
//	api migrate   apply pending database migrations and exit
//	api seed      insert the sample catalog (development and demos only)
//	api healthcheck  exit 0 if this host's API reports ready (container probes)
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
	"slices"
	"syscall"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/medication"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/admin"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/auth"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/buildinfo"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/db"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/health"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/httpx"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/logging"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/middleware"
	"github.com/eduardo-sirangelo/medication-api/internal/pricing"
	"github.com/eduardo-sirangelo/medication-api/internal/seed"
	"github.com/eduardo-sirangelo/medication-api/migrations"
)

const usage = "usage: api [serve|migrate|seed|healthcheck]"

// Must stay below the orchestrator's probe timeout (Kubernetes defaults to
// 1s); a pooled ping normally takes well under 10ms.
const readinessTimeout = 800 * time.Millisecond

// Probes run without credentials; /version carries no data worth protecting
// and helps confirm a deploy.
var publicPaths = []string{"/healthz", "/readyz", "/version"}

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
	// Before config.Load: a probe must not depend on settings it never uses.
	if cmd == "healthcheck" {
		return healthcheck(ctx, lookup)
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
	case len(args) == 1 && slices.Contains([]string{"serve", "migrate", "seed", "healthcheck"}, args[0]):
		return args[0], nil
	default:
		return "", errors.New(usage)
	}
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	info := buildinfo.Read()
	log.Info("starting", slog.Any("build", info))

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
	handler := newHandler(cfg, log, readiness, medication.NewPostgresRepository(pool), info)

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTP.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.HTTP.Addr, err)
	}
	stopAdmin, err := startAdmin(ctx, cfg.Admin, log)
	if err != nil {
		return err
	}
	defer stopAdmin()

	return httpx.Serve(ctx, httpx.NewServer(cfg.HTTP, handler, log), ln, httpx.Shutdown{
		Timeout:    cfg.HTTP.ShutdownTimeout,
		DrainDelay: cfg.HTTP.DrainDelay,
		OnDrain:    readiness.SetDraining,
	}, log)
}

// startAdmin listens before returning, so a taken port fails startup instead
// of surfacing later in a goroutine. The returned func stops the listener and
// waits for it, so it never outlives the API server.
func startAdmin(ctx context.Context, cfg config.Admin, log *slog.Logger) (stop func(), err error) {
	if cfg.Addr == "" {
		return func() {}, nil
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s (ADMIN_ADDR): %w", cfg.Addr, err)
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := admin.Serve(ctx, ln, log); err != nil {
			log.Error("admin server stopped", slog.Any("error", err))
		}
	}()
	return func() { cancel(); <-done }, nil
}

// healthcheck asks this host's API for readiness. Distroless images have no
// curl, so the container probe runs the binary itself.
func healthcheck(ctx context.Context, lookup config.LookupFunc) error {
	addr, ok := lookup("HTTP_ADDR")
	if !ok || addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR=%q: %w", addr, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort("127.0.0.1", port)+"/readyz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("readyz returned %d", resp.StatusCode)
	}
	return nil
}

func newHandler(cfg config.Config, log *slog.Logger, readiness *health.Readiness, repo medication.Repository, info buildinfo.Info) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health.Liveness)
	mux.Handle("GET /readyz", readiness)
	mux.Handle("GET /version", buildinfo.Handler(info))
	meds := medication.NewService(repo)
	medication.NewHandler(meds, log).Register(mux)
	comparer := pricing.NewComparer(pricing.DefaultNetworks(), cfg.Pricing.Budget)
	pricing.NewHandler(meds, comparer, log).Register(mux)

	keys := auth.NewKeyring(cfg.Auth.Keys)
	if keys.Len() == 0 {
		log.Warn("API_KEYS is empty: every request outside the public paths will get 401")
	}

	// RequestID first so every log line and problem body carries the ID;
	// AccessLog outside Recover so recovered panics are logged as 500s;
	// auth before the body limit so unauthenticated callers get 401, not 413.
	return middleware.Chain(httpx.WithProblemFallback(mux),
		middleware.RequestID(),
		middleware.AccessLog(log),
		middleware.Recover(log),
		auth.Require(keys, publicPaths...),
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

	res, err := seed.Run(ctx, medication.NewPostgresRepository(pool), seed.Catalog, log)
	log.InfoContext(ctx, "seed finished", "inserted", res.Inserted, "skipped", res.Skipped)
	return err
}
