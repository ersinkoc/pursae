package auth

import (
	"context"
	"fmt"
	"net/url"
	"os"
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
