// Package db owns the PostgreSQL connection pool and schema migrations.
package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/eduardosirangelo/medication-api/internal/platform/config"
	"github.com/eduardosirangelo/medication-api/internal/platform/logging"
)

// migrationLockTimeout bounds how long a DDL statement may wait for a table
// lock. While it waits, every later query on that table queues behind it, so
// an unbounded wait during a deploy can stall live traffic.
const migrationLockTimeout = 10 * time.Second

// The startup ping makes a wrong DSN or unreachable database fail the process
// immediately. statement_timeout makes the server, not just the client,
// abandon runaway queries.
func Open(ctx context.Context, cfg config.DB, log *slog.Logger) (*pgxpool.Pool, error) {
	params := map[string]string{
		"statement_timeout":                   ms(cfg.StatementTimeout),
		"idle_in_transaction_session_timeout": ms(2 * cfg.StatementTimeout),
	}
	return open(ctx, cfg, params, log)
}

// Migrations need no statement_timeout (index builds can be slow) but do need
// a lock_timeout, so they get their own small, short-lived pool.
func OpenForMigrations(ctx context.Context, cfg config.DB, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg.MaxConns = 3 // goose holds one for the advisory lock, uses another to migrate
	cfg.MinConns = 0
	params := map[string]string{
		"statement_timeout": "0", // disabled
		"lock_timeout":      ms(migrationLockTimeout),
	}
	return open(ctx, cfg, params, log)
}

func open(ctx context.Context, cfg config.DB, params map[string]string, log *slog.Logger) (*pgxpool.Pool, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.URL.Reveal())
	if err != nil {
		// pgx's parse error can echo the DSN (and its password); don't wrap it.
		return nil, errors.New("parse DATABASE_URL: invalid connection string")
	}

	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	pcfg.MaxConnLifetimeJitter = cfg.MaxConnLifetime / 10 // avoid reconnect stampedes
	pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	// Sent in the startup packet, so they apply to every pooled connection.
	rp := pcfg.ConnConfig.RuntimeParams
	rp["application_name"] = logging.ServiceName // visible in pg_stat_activity
	for k, v := range params {
		rp[k] = v
	}

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.InfoContext(ctx, "database connected",
		slog.String("host", pcfg.ConnConfig.Host),
		slog.String("database", pcfg.ConnConfig.Database),
		slog.Int("max_conns", int(pcfg.MaxConns)),
	)
	return pool, nil
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }
