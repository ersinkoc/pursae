package auth

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/db"
)

// attemptsManager returns a Manager on a scratch schema of the test database (skipped without
// WPGO_TEST_DB), so these tests never touch another test's data.
func attemptsManager(t *testing.T) *Manager {
	t.Helper()
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
	t.Cleanup(admin.Close)
	schema := fmt.Sprintf("authtest_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`) })
	pool, err := db.ConnectSchema(ctx, base, schema, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return NewManager(pool, "test-secret")
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
