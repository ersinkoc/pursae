package db

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
