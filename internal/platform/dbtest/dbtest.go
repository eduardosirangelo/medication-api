//go:build integration

// Package dbtest starts a throwaway PostgreSQL container for integration
// tests. Only compiled with -tags=integration; requires Docker.
package dbtest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
	"github.com/eduardo-sirangelo/medication-api/internal/platform/db"
	"github.com/eduardo-sirangelo/medication-api/migrations"
)

// Keep in sync with compose.yaml so tests run against what developers use.
const Image = "postgres:18-alpine"

type Postgres struct {
	DSN       string
	container *tcpostgres.PostgresContainer
}

// One container per test package (from TestMain): starting one per test is slow.
func Start(ctx context.Context) (*Postgres, error) {
	c, err := tcpostgres.Run(ctx, Image,
		tcpostgres.WithDatabase("medication_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, fmt.Errorf("start postgres container: %w", err)
	}
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return nil, fmt.Errorf("connection string: %w", err)
	}
	return &Postgres{DSN: dsn, container: c}, nil
}

func (p *Postgres) Terminate() error { return testcontainers.TerminateContainer(p.container) }

func (p *Postgres) Config() config.DB {
	return config.DB{
		URL:              config.Secret(p.DSN),
		MaxConns:         5,
		MaxConnLifetime:  time.Hour,
		MaxConnIdleTime:  time.Minute,
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 5 * time.Second,
	}
}

// Uses the real migrations and pool setup so tests exercise what production runs.
func (p *Postgres) MigratedPool(ctx context.Context) (*pgxpool.Pool, error) {
	quiet := slog.New(slog.DiscardHandler)
	mpool, err := db.OpenForMigrations(ctx, p.Config(), quiet)
	if err != nil {
		return nil, err
	}
	err = db.Migrate(ctx, mpool, migrations.FS, quiet)
	mpool.Close()
	if err != nil {
		return nil, err
	}
	return db.Open(ctx, p.Config(), quiet)
}
