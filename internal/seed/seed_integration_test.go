//go:build integration

package seed

import (
	"context"
	"testing"

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
	svc := medication.NewService(medication.NewPostgresRepository(pool))

	first, err := Run(t.Context(), svc, Catalog, discard)
	if err != nil || first != (Result{Inserted: len(Catalog)}) {
		t.Fatalf("first run: %+v, %v", first, err)
	}
	second, err := Run(t.Context(), svc, Catalog, discard)
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
}
