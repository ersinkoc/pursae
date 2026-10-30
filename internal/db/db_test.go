package db

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"testing"
	"time"

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
	}
}

// TestMigrateIsIdempotent runs every migration in a scratch schema of the test database and then
// runs Migrate again, which must apply nothing. Skipped without WPGO_TEST_DB.
func TestMigrateIsIdempotent(t *testing.T) {
	url := os.Getenv("WPGO_TEST_DB")
	if url == "" {
		t.Skip("WPGO_TEST_DB not set")
	}
	ctx := context.Background()
	base, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	schema := fmt.Sprintf("dbtest_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)

	pool, err := ConnectSchema(ctx, base, schema, 2)
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
