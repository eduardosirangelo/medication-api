//go:build integration

package seed

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eduardo-sirangelo/medication-api/internal/medication"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/dbtest"
)

func TestRun_IsIdempotentAgainstPostgres(t *testing.T) {
	// Not t.Context(): the container must outlive it until Cleanup terminates it.
	ctx := context.Background()
	pg, err := dbtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate() })
	pool, err := pg.MigratedPool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := medication.NewPostgresRepository(pool)

	first, err := Run(t.Context(), repo, Catalog, discard)
	if err != nil || first != (Result{Inserted: len(Catalog)}) {
		t.Fatalf("first run: %+v, %v", first, err)
	}
	second, err := Run(t.Context(), repo, Catalog, discard)
	if err != nil || second != (Result{Skipped: len(Catalog)}) {
		t.Fatalf("second run: %+v, %v", second, err)
	}

	var rows int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM medications").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != len(Catalog) {
		t.Errorf("rows = %d, want %d", rows, len(Catalog))
	}

	// The point of CreateIfAbsent: skipping must not make PostgreSQL log
	// unique-violation ERRORs. One deliberate duplicate through the API path
	// (Create) acts as a marker: once its line shows up, the log is complete,
	// and it must be the only one.
	m, err := newMedication(Catalog[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(t.Context(), m); !errors.Is(err, medication.ErrDuplicate) {
		t.Fatalf("API path must still report the duplicate: %v", err)
	}
	if n := waitForDuplicateErrors(t, pg, 1); n != 1 {
		t.Errorf("PostgreSQL logged %d duplicate-key ERRORs, want only the marker", n)
	}
}

func waitForDuplicateErrors(t *testing.T, pg *dbtest.Postgres, atLeast int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		logs, err := pg.Logs(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		n := strings.Count(logs, "duplicate key value violates unique constraint")
		if n >= atLeast || time.Now().After(deadline) {
			return n
		}
		time.Sleep(100 * time.Millisecond)
	}
}
