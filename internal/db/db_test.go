package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"wpgo/internal/config"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
		if isNoTransaction(string(b)) {
			if err := checkNoTxMigration(string(b)); err != nil {
				t.Errorf("%s: %v", n, err)
			}
		}
	}
}

// checkNoTxMigration enforces the no-transaction rules Migrate documents: one statement, idempotent.
func checkNoTxMigration(sql string) error {
	stmt := strings.TrimSpace(stripLineComments(sql))
	if strings.Contains(strings.TrimSuffix(stmt, ";"), ";") {
		return fmt.Errorf("a no-transaction migration must hold exactly one statement")
	}
	if !regexp.MustCompile(`(?i)\bIF\s+(NOT\s+)?EXISTS\b`).MatchString(stmt) {
		return fmt.Errorf("a no-transaction migration must be idempotent (IF [NOT] EXISTS)")
	}
	return nil
}

func TestCheckNoTxMigration(t *testing.T) {
	ok := noTransactionMarker + "\n-- why\nCREATE INDEX CONCURRENTLY IF NOT EXISTS posts_x ON posts (post_date);\n"
	if !isNoTransaction(ok) || checkNoTxMigration(ok) != nil {
		t.Errorf("valid no-transaction migration rejected")
	}
	if m := concurrentIndexName.FindStringSubmatch(stripLineComments(ok)); m == nil || m[1] != "posts_x" {
		t.Errorf("index name = %v, want posts_x", m)
	}
	if checkNoTxMigration(noTransactionMarker+"\nCREATE INDEX CONCURRENTLY a ON t (x);") == nil {
		t.Error("non-idempotent statement accepted")
	}
	if checkNoTxMigration(noTransactionMarker+"\nCREATE INDEX CONCURRENTLY IF NOT EXISTS a ON t (x);\nSELECT 1;") == nil {
		t.Error("two statements accepted")
	}
	if isNoTransaction("CREATE TABLE t (x int);\n" + noTransactionMarker) {
		t.Error("marker counts only on the first line")
	}
}

// useMigrations swaps the migration source for a fixture for the rest of the test. Tests that call
// it must not run in parallel.
func useMigrations(t *testing.T, files map[string]string) {
	t.Helper()
	m := fstest.MapFS{}
	for name, sql := range files {
		m["migrations/"+name] = &fstest.MapFile{Data: []byte(sql)}
	}
	prev := migrationSource
	migrationSource = m
	t.Cleanup(func() { migrationSource = prev })
}

func indexValid(t *testing.T, pool *pgxpool.Pool, name string) (exists, valid bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `SELECT count(*) > 0, coalesce(bool_and(indisvalid), false) FROM pg_index WHERE indexrelid = to_regclass($1)`, name).Scan(&exists, &valid)
	if err != nil {
		t.Fatal(err)
	}
	return exists, valid
}

// TestMigrateNoTransactionConcurrentIndex applies a "-- wpgo:no-transaction" migration with
// CREATE INDEX CONCURRENTLY, which a transactional migration cannot run; a second run applies
// nothing. Skipped without PURSAE_TEST_ADMIN_DB.
func TestMigrateNoTransactionConcurrentIndex(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_notx"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	useMigrations(t, map[string]string{
		"0001_t.sql":   `CREATE TABLE big_t (id int, v text); INSERT INTO big_t SELECT g, g::text FROM generate_series(1, 1000) g;`,
		"0002_idx.sql": noTransactionMarker + "\nCREATE INDEX CONCURRENTLY IF NOT EXISTS big_t_v ON big_t (v);\n",
	})
	first, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(first, ",") != "0001_t,0002_idx" {
		t.Errorf("first run applied %v", first)
	}
	if exists, valid := indexValid(t, pool, "big_t_v"); !exists || !valid {
		t.Errorf("big_t_v exists=%v valid=%v, want a valid index", exists, valid)
	}
	if n, err := Pending(ctx, pool); err != nil || n != 0 {
		t.Errorf("Pending = %d, %v; want 0", n, err)
	}
	second, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("second run applied %v, want none", second)
	}
	var lt string
	if err := pool.QueryRow(ctx, `SHOW lock_timeout`).Scan(&lt); err != nil || lt != "0" {
		t.Errorf("pooled connection kept lock_timeout=%q (%v), want it reset", lt, err)
	}
}

// TestMigrateRebuildsInvalidIndex leaves an INVALID index behind (a failed concurrent unique build)
// under the name a no-transaction migration builds: IF NOT EXISTS alone would keep the broken index.
// Skipped without PURSAE_TEST_ADMIN_DB.
func TestMigrateRebuildsInvalidIndex(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_invalidx"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE dup_t (v int); INSERT INTO dup_t VALUES (1), (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE UNIQUE INDEX CONCURRENTLY dup_t_v ON dup_t (v)`); err == nil {
		t.Fatal("unique build on duplicates succeeded")
	}
	if exists, valid := indexValid(t, pool, "dup_t_v"); !exists || valid {
		t.Fatalf("setup: dup_t_v exists=%v valid=%v, want an invalid index", exists, valid)
	}
	useMigrations(t, map[string]string{
		"0001_idx.sql": noTransactionMarker + "\nCREATE INDEX CONCURRENTLY IF NOT EXISTS dup_t_v ON dup_t (v);\n",
	})
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if exists, valid := indexValid(t, pool, "dup_t_v"); !exists || !valid {
		t.Errorf("dup_t_v exists=%v valid=%v, want it rebuilt valid", exists, valid)
	}
}

// TestMigrateLockTimeoutDoesNotHang runs a transactional ALTER while another connection holds a
// conflicting lock: Migrate must fail with lock_not_available after its retries, not wait forever.
// Skipped without PURSAE_TEST_ADMIN_DB.
func TestMigrateLockTimeoutDoesNotHang(t *testing.T) {
	ctx := context.Background()
	pool, err := Connect(ctx, tempDatabase(t, "wpgo_migratelock"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `CREATE TABLE locked_t (id int)`); err != nil {
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
	t.Setenv("PURSAE_MIGRATE_LOCK_TIMEOUT", "100ms")
	prev := migrationRetryBackoff
	migrationRetryBackoff = 10 * time.Millisecond
	t.Cleanup(func() { migrationRetryBackoff = prev })
	useMigrations(t, map[string]string{"0001_alter.sql": `ALTER TABLE locked_t ADD COLUMN extra text;`})

	start := time.Now()
	_, err = Migrate(ctx, pool)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Migrate waited %v", elapsed)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("err = %v, want SQLSTATE 55P03", err)
	}
	if !strings.Contains(err.Error(), "0001_alter") || !strings.Contains(err.Error(), "after 5 attempts") {
		t.Errorf("error %q should name the migration and the attempts", err)
	}
}

// TestConcurrentMigrateWithConcurrentIndex starts two instances' Migrate at once while one of them
// builds an index concurrently: the waiting instance must not hold a snapshot the build waits for,
// which PostgreSQL would report as a deadlock. Skipped without PURSAE_TEST_ADMIN_DB.
func TestConcurrentMigrateWithConcurrentIndex(t *testing.T) {
	ctx := context.Background()
	dbURL := tempDatabase(t, "wpgo_migraterace")
	pools := make([]*pgxpool.Pool, 2)
	for i := range pools {
		p, err := Connect(ctx, dbURL)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		pools[i] = p
	}
	prev := migrateLockPoll
	migrateLockPoll = 20 * time.Millisecond
	t.Cleanup(func() { migrateLockPoll = prev })
	useMigrations(t, map[string]string{
		"0001_t.sql":   `CREATE TABLE big_t (id int, v text); INSERT INTO big_t SELECT g, md5(g::text) FROM generate_series(1, 200000) g;`,
		"0002_idx.sql": noTransactionMarker + "\nCREATE INDEX CONCURRENTLY IF NOT EXISTS big_t_v ON big_t (v);\n",
	})
	type result struct {
		applied []string
		err     error
	}
	results := make(chan result, 2)
	for _, p := range pools {
		go func() {
			applied, err := Migrate(ctx, p)
			results <- result{applied, err}
		}()
	}
	var all []string
	for range pools {
		r := <-results
		if r.err != nil {
			t.Errorf("Migrate: %v", r.err)
		}
		all = append(all, r.applied...)
	}
	sort.Strings(all)
	if strings.Join(all, ",") != "0001_t,0002_idx" {
		t.Errorf("applied across both instances = %v, want each migration once", all)
	}
	if exists, valid := indexValid(t, pools[0], "big_t_v"); !exists || !valid {
		t.Errorf("big_t_v exists=%v valid=%v", exists, valid)
	}
}

// TestMigrateIsIdempotent runs every migration in a fresh database and then runs Migrate again, which must apply
// nothing. Skipped without PURSAE_TEST_ADMIN_DB.
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
	if floor, err := MinBinary(ctx, pool); err != nil || floor != MinCompatibleBinary {
		t.Errorf("min_binary_version = %q (%v), want %s", floor, err, MinCompatibleBinary)
	}
	second, err := Migrate(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Errorf("second run applied %v, want none", second)
	}
}

// TestConnectHonorsCancel needs no database: with nothing listening, Connect must stop retrying as
// soon as ctx is cancelled rather than waiting out its 30-second deadline.
func TestConnectHonorsCancel(t *testing.T) {
	const unreachable = "postgres://u:p@127.0.0.1:1/x?connect_timeout=1"
	cases := map[string]func() (context.Context, context.CancelFunc){
		"already cancelled": func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		},
		"cancelled while retrying": func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(100*time.Millisecond, cancel)
			return ctx, cancel
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := mk()
			defer cancel()
			start := time.Now()
			pool, err := Connect(ctx, unreachable)
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("Connect returned after %v, want it to stop on cancel", elapsed)
			}
			if pool != nil {
				pool.Close()
				t.Error("want a nil pool")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", err)
			}
		})
	}
}

func TestLoadMigrationSettingsFromEnv(t *testing.T) {
	t.Setenv("PURSAE_MIGRATE_LOCK_TIMEOUT", "")
	t.Setenv("PURSAE_MIGRATE_STATEMENT_TIMEOUT", "")
	s := loadMigrationSettings()
	if s.lockTimeout != defaultMigrationLockTimeout || s.statementTimeout != 0 || s.attempts != defaultMigrationAttempts {
		t.Errorf("defaults = %+v", s)
	}
	t.Setenv("PURSAE_MIGRATE_LOCK_TIMEOUT", "250ms")
	t.Setenv("PURSAE_MIGRATE_STATEMENT_TIMEOUT", "45m")
	if s := loadMigrationSettings(); s.lockTimeout != 250*time.Millisecond || s.statementTimeout != 45*time.Minute {
		t.Errorf("from env = %+v", s)
	}
	t.Setenv("PURSAE_MIGRATE_LOCK_TIMEOUT", "soon")
	t.Setenv("PURSAE_MIGRATE_STATEMENT_TIMEOUT", "-1s")
	if s := loadMigrationSettings(); s.lockTimeout != defaultMigrationLockTimeout || s.statementTimeout != 0 {
		t.Errorf("invalid values must fall back to defaults, got %+v", s)
	}
}

// TestApplyMigrationLockTimeout holds a lock that conflicts with an ALTER TABLE from another
// connection: the migration must give up with lock_not_available instead of waiting (and queueing
// live traffic behind it) forever, and leave no schema_migrations row. Once the lock is gone the same
// migration applies. Skipped without PURSAE_TEST_ADMIN_DB.
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
// between attempts: a later attempt must succeed. Skipped without PURSAE_TEST_ADMIN_DB.
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

// tempDatabase creates an empty database next to the admin database (PURSAE_TEST_ADMIN_DB, an existing database
// such as "wpgo") and drops it after the test, so the test needs no other database to exist. Skipped without it.
func tempDatabase(t *testing.T, prefix string) string {
	t.Helper()
	adminURL := config.Env("TEST_ADMIN_DB")
	if adminURL == "" {
		t.Skip("PURSAE_TEST_ADMIN_DB not set")
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
