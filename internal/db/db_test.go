package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var migrationName = regexp.MustCompile(`^migrations/\d{4}_[a-z0-9_]+\.sql$`)

// Several branches may add a migration with the same number (0016_goo_*, 0020_*); they run in name
// order, so the names must be well formed and unique, and no file may be empty.
func TestMigrationFilesAreWellFormed(t *testing.T) {
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil || len(names) == 0 {
		t.Fatalf("no migrations found: %v", err)
	}
	if !sort.StringsAreSorted(names) {
		t.Fatal("fs.Glob must return names in sorted order")
	}
	seen := map[string]bool{}
	for _, n := range names {
		if !migrationName.MatchString(n) {
			t.Errorf("%s: want migrations/NNNN_name.sql in lower case", n)
		}
		if seen[n] {
			t.Errorf("%s listed twice", n)
		}
		seen[n] = true
		b, err := migrationFS.ReadFile(n)
		if err != nil || len(b) == 0 {
			t.Errorf("%s: empty or unreadable (%v)", n, err)
		}
	}
}

// TestMigrateIsIdempotent runs every migration in a fresh database and then runs Migrate again, which must apply
// nothing. Skipped without WPGO_TEST_ADMIN_DB.
func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_dbtest"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	first, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := fs.Glob(migrationFS, "migrations/*.sql")
	if len(first) != len(files) {
		t.Errorf("first run applied %d migrations, want %d", len(first), len(files))
	}
	second, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("second run applied %v, want none", second)
	}
}

func TestLoadMigrationSettingsFromEnv(t *testing.T) {
	t.Setenv("WPGO_MIGRATE_LOCK_TIMEOUT", "")
	t.Setenv("WPGO_MIGRATE_STATEMENT_TIMEOUT", "")
	s := loadMigrationSettings()
	if s.lockTimeout != defaultMigrationLockTimeout || s.statementTimeout != 0 || s.attempts != defaultMigrationAttempts {
		t.Errorf("defaults = %+v", s)
	}
	t.Setenv("WPGO_MIGRATE_LOCK_TIMEOUT", "250ms")
	t.Setenv("WPGO_MIGRATE_STATEMENT_TIMEOUT", "45m")
	if s := loadMigrationSettings(); s.lockTimeout != 250*time.Millisecond || s.statementTimeout != 45*time.Minute {
		t.Errorf("from env = %+v", s)
	}
	t.Setenv("WPGO_MIGRATE_LOCK_TIMEOUT", "soon")
	t.Setenv("WPGO_MIGRATE_STATEMENT_TIMEOUT", "-1s")
	if s := loadMigrationSettings(); s.lockTimeout != defaultMigrationLockTimeout || s.statementTimeout != 0 {
		t.Errorf("invalid values must fall back to defaults, got %+v", s)
	}
}

// TestApplyMigrationLockTimeout holds a lock that conflicts with an ALTER TABLE from another
// connection: the migration must give up with lock_not_available instead of waiting (and queueing
// live traffic behind it) forever, and leave no schema_migrations row. Once the lock is gone the same
// migration applies. Skipped without WPGO_TEST_ADMIN_DB.
func TestApplyMigrationLockTimeout(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_locktimeout"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
		CREATE TABLE locked_t (id int)`); err != nil {
		t.Fatal(err)
	}

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE locked_t IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	const version, sql = "9999_alter_locked", `ALTER TABLE locked_t ADD COLUMN extra text`
	s := migrationSettings{lockTimeout: 200 * time.Millisecond, attempts: 1}

	start := time.Now()
	err = applyMigration(ctx, conn, version, sql, s)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("applyMigration waited %v, want it bounded by lock_timeout", elapsed)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("err = %v, want SQLSTATE 55P03 (lock_not_available)", err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version=$1`, version).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a failed migration was recorded in schema_migrations")
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(ctx, conn, version, sql, s); err != nil {
		t.Fatalf("after the lock is released: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version=$1`, version).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("schema_migrations rows for %s = %d, want 1", version, rows)
	}
}

// TestApplyMigrationRetriesLockTimeout releases the conflicting lock while applyMigration backs off
// between attempts: a later attempt must succeed. Skipped without WPGO_TEST_ADMIN_DB.
func TestApplyMigrationRetriesLockTimeout(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_lockretry"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
		CREATE TABLE locked_t (id int)`); err != nil {
		t.Fatal(err)
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `LOCK TABLE locked_t IN ACCESS SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, func() { _ = holder.Rollback(context.Background()) })

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	s := migrationSettings{lockTimeout: 100 * time.Millisecond, attempts: 5}
	if err := applyMigration(ctx, conn, "9999_alter_locked", `ALTER TABLE locked_t ADD COLUMN extra text`, s); err != nil {
		t.Fatalf("want a retry to succeed once the lock is released: %v", err)
	}
}

// tempDatabase creates an empty database next to the admin database (WPGO_TEST_ADMIN_DB, an existing database
// such as "wpgo") and drops it after the test, so the test needs no other database to exist. Skipped without it.
func tempDatabase(t *testing.T, prefix string) string {
	t.Helper()
	adminURL := os.Getenv("WPGO_TEST_ADMIN_DB")
	if adminURL == "" {
		t.Skip("WPGO_TEST_ADMIN_DB not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
		admin.Close(context.Background())
	})
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}
