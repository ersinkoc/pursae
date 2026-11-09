package auth

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/db"
)

// attemptsManager returns a Manager on a fresh, migrated database that is dropped after the test.
func attemptsManager(t *testing.T) *Manager {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Connect(ctx, tempDatabase(t, "wpgo_authtest"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return NewManager(pool, "test-secret")
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

// A successful login must clear only that login's failures. Otherwise an attacker with one valid
// account alternates four guesses at a victim with one login of their own and is never throttled.
func TestSuccessfulLoginKeepsOtherAccountsFailures(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	const ip = "198.51.100.7"
	for round := 0; round < 3; round++ {
		for i := 0; i < MaxFailedLogins-1; i++ {
			m.RecordAttempt(ctx, ip, "victim", false)
		}
		m.RecordAttempt(ctx, ip, "attacker", true)
	}
	throttled, err := m.Throttled(ctx, ip)
	if err != nil {
		t.Fatal(err)
	}
	if !throttled {
		t.Fatal("12 failed guesses at one account in a row were not throttled because of logins to another account")
	}
}

func TestSuccessfulLoginClearsOwnFailures(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	const ip = "198.51.100.8"
	for i := 0; i < MaxFailedLogins-1; i++ {
		m.RecordAttempt(ctx, ip, "Alice", false)
	}
	m.RecordAttempt(ctx, ip, "alice", true) // same account, different case
	m.RecordAttempt(ctx, ip, "alice", false)
	if throttled, _ := m.Throttled(ctx, ip); throttled {
		t.Fatal("a user who typos a few times and then logs in should start from zero")
	}
}

// Parallel attempts must not all pass the throttle: each is recorded before its password is checked, so
// at most MaxFailedLogins of them get through, however many arrive at once.
func TestBeginAttemptIsAtomic(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	const ip, n = "198.51.100.20", 30
	var wg sync.WaitGroup
	var passed atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, throttled, err := m.BeginAttempt(ctx, ip, "victim"); err == nil && !throttled {
				passed.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := passed.Load(); got > MaxFailedLogins {
		t.Fatalf("%d of %d parallel attempts passed the throttle, want at most %d", got, n, MaxFailedLogins)
	}
	// Blocked attempts are not stored: they do not extend the block.
	var stored int
	_ = m.pool.QueryRow(ctx, `SELECT count(*) FROM login_attempts WHERE ip = $1`, ip).Scan(&stored)
	if stored > MaxFailedLogins {
		t.Errorf("%d attempts stored, want at most %d", stored, MaxFailedLogins)
	}
}

// Guesses at one user name spread over many addresses are bounded too.
func TestBeginAttemptPerLogin(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	for i := 0; i < MaxFailedLoginsPerLogin; i++ {
		if _, throttled, _ := m.BeginAttempt(ctx, fmt.Sprintf("203.0.113.%d", i), "Admin"); throttled {
			t.Fatalf("attempt %d throttled too early", i)
		}
	}
	if _, throttled, _ := m.BeginAttempt(ctx, "192.0.2.1", "admin"); !throttled {
		t.Fatal("a fresh address guessing a user name with 20 recent failures was not throttled")
	}
	if _, throttled, _ := m.BeginAttempt(ctx, "192.0.2.1", "someone-else"); throttled {
		t.Fatal("another user name from a fresh address was throttled")
	}
	// A success turns the attempt into a success and clears that login's failures from the address.
	id, _, _ := m.BeginAttempt(ctx, "192.0.2.9", "carol")
	m.AttemptSucceeded(ctx, id, "192.0.2.9", "carol")
	var ok bool
	if err := m.pool.QueryRow(ctx, `SELECT success FROM login_attempts WHERE id = $1`, id).Scan(&ok); err != nil || !ok {
		t.Errorf("attempt not marked as success: %v %v", ok, err)
	}
}

// An attempt still being checked is pending: it counts against parallel requests (BeginAttempt) but is no
// completed failure (Throttled) until AttemptFailed marks it so.
func TestPendingAttemptIsNoFailure(t *testing.T) {
	m := attemptsManager(t)
	ctx := context.Background()
	const ip = "198.51.100.30"
	var ids []int64
	for i := 0; i < MaxFailedLogins; i++ {
		id, throttled, err := m.BeginAttempt(ctx, ip, "dave")
		if err != nil || throttled {
			t.Fatalf("attempt %d: throttled %v, %v", i, throttled, err)
		}
		ids = append(ids, id)
	}
	if _, throttled, _ := m.BeginAttempt(ctx, ip, "dave"); !throttled {
		t.Error("in-flight attempts did not count against a parallel one")
	}
	if throttled, _ := m.Throttled(ctx, ip); throttled {
		t.Error("pending attempts counted as completed failures")
	}
	for _, id := range ids {
		m.AttemptFailed(ctx, id)
	}
	if throttled, _ := m.Throttled(ctx, ip); !throttled {
		t.Error("failed attempts not counted after AttemptFailed")
	}
}
