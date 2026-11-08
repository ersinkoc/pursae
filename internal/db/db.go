// Package db owns the PostgreSQL pool and schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Connect opens a pool and waits for the database to accept connections.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := PoolConfig(url)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = pool.Ping(ctx)
		if err == nil {
			return pool, nil
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("database not reachable: %w", err)
		}
		time.Sleep(time.Second)
	}
}

// PoolConfig parses url and applies the pool defaults below to every setting the URL does not
// carry itself (pool_max_conns, pool_min_conns, pool_max_conn_lifetime, pool_max_conn_idle_time,
// pool_health_check_period — pgx's own query parameters).
//
// pgx alone would allow max(4, NumCPU) connections, too few for a site whose requests, scheduler
// and background writers share one pool. The cap of 40 stays well inside PostgreSQL's default
// max_connections (100) so a second instance or a backup still gets a connection.
func PoolConfig(url string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	set := func(param string) bool { return strings.Contains(url, param+"=") }
	if !set("pool_max_conns") {
		cfg.MaxConns = defaultMaxConns(runtime.NumCPU())
	}
	if !set("pool_min_conns") {
		cfg.MinConns = 2
	}
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	if !set("pool_max_conn_lifetime") {
		cfg.MaxConnLifetime = time.Hour
	}
	if !set("pool_max_conn_lifetime_jitter") {
		cfg.MaxConnLifetimeJitter = 5 * time.Minute // instances started together do not reconnect together
	}
	if !set("pool_max_conn_idle_time") {
		cfg.MaxConnIdleTime = 15 * time.Minute
	}
	if !set("pool_health_check_period") {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	return cfg, nil
}

// defaultMaxConns is 4 per CPU, at least 10 and at most 40.
func defaultMaxConns(cpus int) int32 {
	return int32(min(max(10, 4*cpus), 40))
}

// Migrate applies every embedded migration that has not run yet, each in its own transaction.
// A session-level advisory lock keeps concurrent instances from migrating twice.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	const lockID = 7_356_001
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return nil, err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockID)

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}

	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	var applied []string
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			return applied, err
		}
		if exists {
			continue
		}
		sql, err := migrationFS.ReadFile(name)
		if err != nil {
			return applied, err
		}
		err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, version)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", version, err)
		}
		applied = append(applied, version)
	}
	return applied, nil
}

// ConnectSchema opens a pool on the database of base whose connections see only the given schema
// (search_path): a second, separate site in the same database, migrated with Migrate like the
// first (theme previews). The schema must exist.
func ConnectSchema(ctx context.Context, base *pgxpool.Config, schema string, maxConns int32) (*pgxpool.Pool, error) {
	cfg := base.Copy()
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize()
	cfg.MaxConns, cfg.MinConns = maxConns, 0
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Pending counts the embedded migrations the database has not applied yet (readiness checks).
func Pending(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return 0, err
	}
	want := make([]string, 0, len(names))
	for _, name := range names {
		want = append(want, strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql"))
	}
	var applied int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ANY($1)`, want).Scan(&applied)
	if err != nil {
		return 0, err
	}
	return len(want) - applied, nil
}
