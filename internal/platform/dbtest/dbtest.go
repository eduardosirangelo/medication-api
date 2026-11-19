//go:build integration

// Package dbtest starts a throwaway PostgreSQL container for integration
// tests. Only compiled with -tags=integration; requires Docker.
package dbtest

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/eduardo-sirangelo/medication-api/internal/platform/config"
)

// Image is the PostgreSQL version used in tests; keep in sync with compose.yaml.
const Image = "postgres:18-alpine"

// Postgres is a running test database.
type Postgres struct {
	DSN       string
	container *tcpostgres.PostgresContainer
}

// Start launches a container and returns once it accepts connections.
// Call Terminate when done (typically from TestMain).
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

// Terminate stops and removes the container.
func (p *Postgres) Terminate() error { return testcontainers.TerminateContainer(p.container) }

// Config returns a DB config pointing at the container with test-friendly values.
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
