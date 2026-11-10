package server

import (
	"context"
	"errors"
	"sync"
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

func TestEmptyTrashDays(t *testing.T) {
	for v, want := range map[string]int{"": 30, "7": 7, "0": 0, "-1": 30, "x": 30, " 14 ": 14} {
		t.Setenv("WPGO_EMPTY_TRASH_DAYS", v)
		if got := emptyTrashDays(); got != want {
			t.Errorf("WPGO_EMPTY_TRASH_DAYS=%q: %d, want %d", v, got, want)
		}
	}
}

// TestRecurringJobNeverFails: a recurring job that keeps failing is retried with backoff and then
// waits for its next regular run instead of being marked "failed" for good (a Hub outage used to
// stop wpgo_hub_check forever); ensureEvents puts rows an older version failed back on the queue.
func TestRecurringJobNeverFails(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	pool := srv.store.Pool
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence) VALUES ('wpgo_test_flaky', now(), 'hourly')`); err != nil {
		t.Fatal(err)
	}
	boom := map[string]jobHandler{"wpgo_test_flaky": func(context.Context, map[string]any) error { return errors.New("hub down") }}
	for i := 0; i < jobMaxRetries+2; i++ {
		if _, err := pool.Exec(ctx, `UPDATE jobs SET run_at = now() WHERE name = 'wpgo_test_flaky'`); err != nil {
			t.Fatal(err)
		}
		srv.runDueJobs(ctx, "test", boom)
	}
	var status string
	var attempts int
	var inSecs float64
	if err := pool.QueryRow(ctx, `SELECT status, attempts, extract(epoch FROM run_at - now()) FROM jobs WHERE name = 'wpgo_test_flaky'`).Scan(&status, &attempts, &inSecs); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || attempts != jobMaxRetries+2 {
		t.Fatalf("status %q attempts %d, want queued after %d failures", status, attempts, jobMaxRetries+2)
	}
	if inSecs < 3500 || inSecs > 3700 {
		t.Fatalf("next run in %.0fs, want the next hourly run", inSecs)
	}
	// It recovers: success resets the counter.
	_, _ = pool.Exec(ctx, `UPDATE jobs SET run_at = now() WHERE name = 'wpgo_test_flaky'`)
	srv.runDueJobs(ctx, "test", map[string]jobHandler{"wpgo_test_flaky": func(context.Context, map[string]any) error { return nil }})
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM jobs WHERE name = 'wpgo_test_flaky'`).Scan(&status, &attempts); err != nil || status != "queued" || attempts != 0 {
		t.Fatalf("after success: %q %d (%v)", status, attempts, err)
	}

	// A recurring row an older version failed for good is repaired at start-up.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status = 'failed', attempts = 5 WHERE name = 'wpgo_hub_check'`); err != nil {
		t.Fatal(err)
	}
	srv.ensureEvents(ctx)
	if err := pool.QueryRow(ctx, `SELECT status, attempts FROM jobs WHERE name = 'wpgo_hub_check'`).Scan(&status, &attempts); err != nil || status != "queued" || attempts != 0 {
		t.Fatalf("failed recurring row not repaired: %q %d (%v)", status, attempts, err)
	}
	// One-off jobs still fail for good.
	_ = srv.queueJob(ctx, "wpgo_test_once", nil, time.Time{})
	once := map[string]jobHandler{"wpgo_test_once": func(context.Context, map[string]any) error { return errors.New("nope") }}
	for i := 0; i < jobMaxRetries; i++ {
		_, _ = pool.Exec(ctx, `UPDATE jobs SET run_at = now() WHERE name = 'wpgo_test_once'`)
		srv.runDueJobs(ctx, "test", once)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM jobs WHERE name = 'wpgo_test_once'`).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("one-off job: %q (%v), want failed", status, err)
	}
}

// TestJobHeartbeatAndShutdown: a long job keeps its lock fresh, so it is not re-queued while it
// still runs; a job interrupted by shutdown goes back to the queue without counting an attempt,
// and so do the claimed jobs it had not started.
func TestJobHeartbeatAndShutdown(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	pool := srv.store.Pool
	old := jobHeartbeatEvery
	jobHeartbeatEvery = 20 * time.Millisecond
	t.Cleanup(func() { jobHeartbeatEvery = old })

	_ = srv.queueJob(ctx, "wpgo_test_long", nil, time.Time{})
	var fresh bool
	srv.runDueJobs(ctx, "w1", map[string]jobHandler{"wpgo_test_long": func(ctx context.Context, _ map[string]any) error {
		// Pretend the job has run for 20 minutes; the heartbeat must refresh the lock.
		_, _ = pool.Exec(ctx, `UPDATE jobs SET locked_at = now() - interval '20 minutes' WHERE name = 'wpgo_test_long'`)
		time.Sleep(200 * time.Millisecond)
		_ = pool.QueryRow(ctx, `SELECT locked_at > now() - interval '1 minute' FROM jobs WHERE name = 'wpgo_test_long'`).Scan(&fresh)
		return nil
	}})
	if !fresh {
		t.Fatal("locked_at of a running job was not refreshed")
	}

	_ = srv.queueJob(ctx, "wpgo_test_cut", nil, time.Time{})
	_ = srv.queueJob(ctx, "wpgo_test_cut", nil, time.Time{})
	jctx, cancel := context.WithCancel(ctx)
	srv.runDueJobs(jctx, "w2", map[string]jobHandler{"wpgo_test_cut": func(ctx context.Context, _ map[string]any) error {
		cancel() // SIGTERM arrives mid-job
		<-ctx.Done()
		return ctx.Err()
	}})
	rows, err := pool.Query(ctx, `SELECT status, attempts, coalesce(locked_by, '') FROM jobs WHERE name = 'wpgo_test_cut'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var status, by string
		var attempts int
		if err := rows.Scan(&status, &attempts, &by); err != nil {
			t.Fatal(err)
		}
		n++
		if status != "queued" || attempts != 0 || by != "" {
			t.Errorf("interrupted job: status %q attempts %d locked_by %q, want queued, 0, none", status, attempts, by)
		}
	}
	if n != 2 {
		t.Fatalf("%d rows", n)
	}
}

// A recurring job that missed runs (the site was down) runs once and then waits for the next slot of
// its schedule; it does not run again right away.
func TestRecurringJobCatchUpRunsOnce(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	pool := srv.store.Pool
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence) VALUES ('wpgo_test_missed', now() - interval '3 hours 10 minutes', 'hourly')`); err != nil {
		t.Fatal(err)
	}
	runs := 0
	h := map[string]jobHandler{"wpgo_test_missed": func(context.Context, map[string]any) error { runs++; return nil }}
	srv.runDueJobs(ctx, "test", h)
	srv.runDueJobs(ctx, "test", h)
	if runs != 1 {
		t.Fatalf("the missed job ran %d times in two ticks, want 1", runs)
	}
	var inSecs float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM run_at - now()) FROM jobs WHERE name = 'wpgo_test_missed'`).Scan(&inSecs); err != nil {
		t.Fatal(err)
	}
	if inSecs < 49*60 || inSecs > 51*60 {
		t.Fatalf("next run in %.0fs, want the next hourly slot (~50 minutes)", inSecs)
	}
}

// Two instances starting together schedule each core event once (migration 0079).
func TestEnsureEventsOnce(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	pool := srv.store.Pool
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); srv.ensureEvents(ctx) }()
	}
	wg.Wait()
	var dups int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT name FROM jobs WHERE recurrence <> '' GROUP BY name HAVING count(*) > 1) d`).Scan(&dups); err != nil {
		t.Fatal(err)
	}
	if dups != 0 {
		t.Fatalf("%d recurring events are scheduled more than once", dups)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence) VALUES ('wp_session_gc', now(), 'hourly')`); err == nil {
		t.Fatal("a second wp_session_gc row was accepted")
	}
}
