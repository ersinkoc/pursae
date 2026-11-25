package settings

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/config"
	"wpgo/internal/db"
)

// testPool recreates PURSAE_TEST_DB and migrates it; skipped without the test databases.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	adminURL, testURL := config.Env("TEST_ADMIN_DB"), config.Env("TEST_DB")
	if adminURL == "" || testURL == "" {
		t.Skip("PURSAE_TEST_ADMIN_DB / PURSAE_TEST_DB not set")
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

type changingJSONValue int

func (v *changingJSONValue) MarshalJSON() ([]byte, error) {
	*v = *v + 1
	return []byte(strconv.Itoa(int(*v))), nil
}

func TestSetCachesTheCommittedJSON(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := New(pool)
	value := changingJSONValue(0)
	if err := s.Set(ctx, map[string]any{"custom": &value}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT value #>> '{}' FROM settings WHERE key = 'custom'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if got := s.String("custom"); stored != "1" || got != stored {
		t.Fatalf("cache = %q, database = %q, want 1 for both", got, stored)
	}
}

// TestRevisionFollowsEveryChange: Set and Load (the path Watch takes on NOTIFY) each move Revision,
// so values memoized under it are rebuilt.
func TestRevisionFollowsEveryChange(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := New(pool)
	r0 := s.Revision()
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	r1 := s.Revision()
	if r1 == r0 {
		t.Fatal("Load kept the revision")
	}
	if err := s.Set(ctx, map[string]any{"blogname": "Revision"}); err != nil {
		t.Fatal(err)
	}
	if s.Revision() == r1 {
		t.Fatal("Set kept the revision")
	}
}

func TestSetRespectsAutoload(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := New(pool)
	if err := s.Set(ctx, map[string]any{"cache_probe": "cached"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE settings SET autoload = false WHERE key = 'cache_probe'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, map[string]any{"cache_probe": "saved"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("cache_probe"); got != nil {
		t.Fatalf("non-autoloaded value remains cached: %v", got)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT value #>> '{}' FROM settings WHERE key = 'cache_probe'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "saved" {
		t.Fatalf("database = %q, want saved", stored)
	}
}

type settingsReloadGate struct {
	loaded  chan struct{}
	release chan struct{}
}

func (g settingsReloadGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if data.SQL == `SELECT key, value FROM settings WHERE autoload` {
		return context.WithValue(ctx, g, true)
	}
	return ctx
}
func (g settingsReloadGate) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if ctx.Value(g) == nil {
		return
	}
	select {
	case g.loaded <- struct{}{}:
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	default:
	}
}
func TestLoadPreservesConcurrentCommit(t *testing.T) {
	base := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gate := settingsReloadGate{make(chan struct{}, 1), make(chan struct{})}
	poolCfg := base.Config()
	poolCfg.ConnConfig.Tracer = gate
	poolCfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool)
	if err := s.Set(ctx, map[string]any{"audit_reload": "old"}); err != nil {
		t.Fatal(err)
	}
	if got := s.String("audit_reload"); got != "old" {
		t.Fatalf("control = %q", got)
	}
	done := make(chan error, 1)
	go func() { done <- s.Load(ctx) }()
	select {
	case <-gate.loaded:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := s.Set(ctx, map[string]any{"audit_reload": "new"}); err != nil {
		t.Fatal(err)
	}
	if got := s.String("audit_reload"); got != "new" {
		t.Fatalf("before reload resumes: %q", got)
	}
	close(gate.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := s.String("audit_reload"); got != "new" {
		t.Fatalf("cache = %q after concurrent reload, want new", got)
	}
}
