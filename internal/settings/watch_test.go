package settings

import (
	"context"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/db"
)

// testPool recreates WPGO_TEST_DB and migrates it; skipped without the test databases.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL, testURL := os.Getenv("WPGO_TEST_ADMIN_DB"), os.Getenv("WPGO_TEST_DB")
	if adminURL == "" || testURL == "" {
		t.Skip("WPGO_TEST_ADMIN_DB / WPGO_TEST_DB not set")
	}
	ctx := context.Background()
	u, err := url.Parse(testURL)
	if err != nil {
		t.Fatal(err)
	}
	name := pgx.Identifier{strings.TrimPrefix(u.Path, "/")}.Sanitize()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`, `CREATE DATABASE ` + name} {
		if _, err := admin.Exec(ctx, q); err != nil {
			admin.Close(ctx)
			t.Fatal(err)
		}
	}
	admin.Close(ctx)
	pool, err := db.Connect(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// TestWatchSeesOtherInstance: a value saved through one Store reaches another Store on the same
// database through LISTEN/NOTIFY, without a restart.
func TestWatchSeesOtherInstance(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	a, b := New(pool), New(pool)
	for _, s := range []*Store{a, b} {
		if err := s.Load(ctx); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); b.Watch(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for b.String("blogname") != want {
			if time.Now().After(deadline) {
				t.Fatalf("instance b still has %q, want %q", b.String("blogname"), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	// Watch reloads after LISTEN; give it a moment to subscribe before the first change.
	time.Sleep(200 * time.Millisecond)
	if err := a.Set(ctx, map[string]any{"blogname": "From A"}); err != nil {
		t.Fatal(err)
	}
	wait("From A")
	if _, err := pool.Exec(ctx, `UPDATE settings SET value = '"Raw SQL"' WHERE key = 'blogname'`); err != nil {
		t.Fatal(err)
	}
	wait("Raw SQL")
}

// TestConcurrentSetsDoNotDeadlock: two saves of the same keys in parallel both succeed, and the
// cache agrees with the database afterwards.
func TestConcurrentSetsDoNotDeadlock(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := New(pool)
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	vals := func(tag string) map[string]any {
		m := map[string]any{}
		for i := range 30 {
			m["k"+string(rune('a'+i%26))+string(rune('a'+i/26))] = tag
		}
		return m
	}
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Set(ctx, vals(string(rune('A'+i%2)))); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT value #>> '{}' FROM settings WHERE key = 'kaa'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if got := s.String("kaa"); got != stored {
		t.Errorf("cache %q, database %q", got, stored)
	}
}
