//go:build integration

package db_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/eduardosirangelo/medication-api/internal/platform/db"
	"github.com/eduardosirangelo/medication-api/internal/platform/dbtest"
	"github.com/eduardosirangelo/medication-api/internal/platform/health"
	"github.com/eduardosirangelo/medication-api/migrations"
)

var pg *dbtest.Postgres

func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	pg, err = dbtest.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = pg.Terminate()
	os.Exit(code)
}

var quiet = slog.New(slog.DiscardHandler)

func TestOpen_AppliesSessionSettings(t *testing.T) {
	ctx := t.Context()
	cfg := pg.Config()
	cfg.StatementTimeout = 1500 * time.Millisecond

	pool, err := db.Open(ctx, cfg, quiet)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pool.Close()

	var stmt, idle, app string
	err = pool.QueryRow(ctx, `SELECT current_setting('statement_timeout'),
	                                 current_setting('idle_in_transaction_session_timeout'),
	                                 current_setting('application_name')`).Scan(&stmt, &idle, &app)
	if err != nil {
		t.Fatal(err)
	}
	if stmt != "1500ms" || idle != "3s" || app != "medication-api" {
		t.Errorf("got statement_timeout=%s idle=%s app=%s", stmt, idle, app)
	}

	// The server, not the client, cancels runaway queries.
	_, err = pool.Exec(ctx, "SELECT pg_sleep(3)")
	if err == nil {
		t.Fatal("expected statement_timeout to cancel pg_sleep(3)")
	}
}

func TestOpen_FailsFastOnBadDSN(t *testing.T) {
	cfg := pg.Config()
	cfg.URL = "postgres://nobody:wrong@127.0.0.1:1/none?connect_timeout=1"
	cfg.ConnectTimeout = time.Second
	if _, err := db.Open(t.Context(), cfg, quiet); err == nil {
		t.Fatal("expected error for unreachable database")
	}

	cfg.URL = "::not a dsn::"
	_, err := db.Open(t.Context(), cfg, quiet)
	if err == nil || err.Error() != "parse DATABASE_URL: invalid connection string" {
		t.Fatalf("parse error must be generic (no DSN echo), got %v", err)
	}
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	ctx := t.Context()
	pool, err := db.OpenForMigrations(ctx, pg.Config(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	for i := range 2 { // second run must be a no-op
		if err := db.Migrate(ctx, pool, migrations.FS, quiet); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}

	var ext string
	if err := pool.QueryRow(ctx, `SELECT extname FROM pg_extension WHERE extname = 'pg_trgm'`).Scan(&ext); err != nil {
		t.Fatalf("pg_trgm not installed: %v", err)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version`).Scan(&version); err != nil || version < 1 {
		t.Fatalf("goose version = %d, err = %v", version, err)
	}
	var stmt string
	_ = pool.QueryRow(ctx, `SELECT current_setting('statement_timeout')`).Scan(&stmt)
	if stmt != "0" {
		t.Errorf("migration pool statement_timeout = %s, want 0 (disabled)", stmt)
	}
}

// Several replicas starting at once must not apply the same migration twice.
// The migrations use plain CREATE TABLE (no IF NOT EXISTS), so any double
// application would fail with "relation already exists".
func TestMigrate_ConcurrentRunsAreSerialised(t *testing.T) {
	ctx := t.Context()
	// Fresh schema version table so these migrations are actually pending.
	fsys := fstest.MapFS{
		"00001_a.sql": {Data: []byte("-- +goose Up\nCREATE TABLE concurrent_a (id int);\n-- +goose Down\nDROP TABLE concurrent_a;\n")},
		"00002_b.sql": {Data: []byte("-- +goose Up\nSELECT pg_sleep(0.3);\nCREATE TABLE concurrent_b (id int);\n-- +goose Down\nDROP TABLE concurrent_b;\n")},
	}
	cfg := pg.Config()
	cfg.URL += "&options=-csearch_path%3Dconcurrent"
	setup, err := db.Open(ctx, pg.Config(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS concurrent"); err != nil {
		t.Fatal(err)
	}
	setup.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			pool, err := db.OpenForMigrations(ctx, cfg, quiet)
			if err != nil {
				errs <- err
				return
			}
			defer pool.Close()
			errs <- db.Migrate(ctx, pool, fsys, quiet)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
}

func TestReadiness_RealDatabase(t *testing.T) {
	pool, err := db.Open(t.Context(), pg.Config(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	r := health.NewReadiness(pool, time.Second, quiet)

	probe := func() int {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
		return rec.Code
	}
	if code := probe(); code != http.StatusOK {
		t.Fatalf("ready with live DB: got %d", code)
	}
	pool.Close() // simulate losing the database
	if code := probe(); code != http.StatusServiceUnavailable {
		t.Fatalf("closed pool: got %d, want 503", code)
	}
}
