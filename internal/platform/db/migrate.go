package db

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// A PostgreSQL session-level advisory lock serialises concurrent runs, so it
// is safe if several replicas (or a job and a replica) start at once: the
// first applies the migrations, the rest wait and then find nothing to do.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, log *slog.Logger) error {
	// Retry every 1s (default 5s) for up to 5 minutes, so waiting replicas
	// proceed promptly once the first one finishes.
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 300))
	if err != nil {
		return fmt.Errorf("create migration locker: %w", err)
	}

	// goose speaks database/sql; this adapter borrows connections from the pool.
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys,
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	results, err := provider.Up(ctx)
	for _, r := range results {
		log.InfoContext(ctx, "migration applied",
			slog.Int64("version", r.Source.Version),
			slog.String("file", r.Source.Path),
			slog.String("duration", r.Duration.String()),
		)
	}
	if err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	log.InfoContext(ctx, "schema up to date", slog.Int64("version", version), slog.Int("applied", len(results)))
	return nil
}
