package server

import (
	"context"
	"testing"
	"time"
)

// TestQueueJobUsesDatabaseClock: a job queued for "now" is stamped with the database's clock, the
// one runDueJobs compares run_at with, so it is due at once even when the app host's clock runs
// ahead of the database's (the TestMailSMTPPlugin flake: a queued mail was not yet due).
func TestQueueJobUsesDatabaseClock(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	pool := srv.store.Pool
	var before, after, runAt time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := srv.queueJob(ctx, "wpgo_test_now", map[string]any{"x": 1}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	var due bool
	if err := pool.QueryRow(ctx, `SELECT run_at, run_at <= now() FROM jobs WHERE name = 'wpgo_test_now'`).Scan(&runAt, &due); err != nil {
		t.Fatal(err)
	}
	if runAt.Before(before) || runAt.After(after) || !due {
		t.Fatalf("run_at %v not stamped by the database clock (between %v and %v), due %v", runAt, before, after, due)
	}
	ran := false
	srv.runDueJobs(ctx, "test", map[string]jobHandler{"wpgo_test_now": func(context.Context, map[string]any) error { ran = true; return nil }})
	if !ran {
		t.Fatal("job queued for now did not run at once")
	}

	// An explicit time is kept as given.
	later := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	if err := srv.queueJob(ctx, "wpgo_test_later", nil, later); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT run_at FROM jobs WHERE name = 'wpgo_test_later'`).Scan(&runAt); err != nil || !runAt.Equal(later) {
		t.Fatalf("explicit run_at %v, want %v (%v)", runAt, later, err)
	}
}
