package server

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestScheduledActionsScreenRetryDelete(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	var failedID, runningID int64
	if err := srv.store.Pool.QueryRow(ctx, `INSERT INTO jobs(name, run_at, status, attempts, last_error) VALUES ('sa_test_failed', now(), 'failed', 5, 'smtp: boom') RETURNING id`).Scan(&failedID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.Pool.QueryRow(ctx, `INSERT INTO jobs(name, run_at, status) VALUES ('sa_test_running', now(), 'running') RETURNING id`).Scan(&runningID); err != nil {
		t.Fatal(err)
	}
	code, body, _ := get(t, c, ts.URL+"/wp-admin/scheduled-actions?status=failed")
	if code != 200 || !strings.Contains(body, "sa_test_failed") || !strings.Contains(body, "smtp: boom") || strings.Contains(body, "sa_test_running") {
		t.Fatalf("failed list: %d %s", code, body)
	}
	nonce := formNonce(t, body)
	// A running job cannot be deleted.
	post(t, c, ts.URL+"/wp-admin/scheduled-actions", url.Values{"_wpnonce": {nonce}, "do": {"delete"}, "job": {itoa64(runningID)}})
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE id = $1`, runningID).Scan(&n)
	if n != 1 {
		t.Error("a running job was deleted")
	}
	// Retry puts the failed job back on the queue with a clean slate.
	if code, _, _ := post(t, c, ts.URL+"/wp-admin/scheduled-actions", url.Values{"_wpnonce": {nonce}, "do": {"retry"}, "job": {itoa64(failedID)}}); code != 303 {
		t.Fatalf("retry: %d", code)
	}
	var status, lastErr string
	var attempts int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT status, attempts, last_error FROM jobs WHERE id = $1`, failedID).Scan(&status, &attempts, &lastErr)
	if status != "queued" || attempts != 0 || lastErr != "" {
		t.Errorf("after retry: %s %d %q", status, attempts, lastErr)
	}
	// Without a valid nonce nothing happens.
	post(t, c, ts.URL+"/wp-admin/scheduled-actions", url.Values{"_wpnonce": {"bad"}, "do": {"delete"}, "job": {itoa64(failedID)}})
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE id = $1`, failedID).Scan(&n)
	if n != 1 {
		t.Error("delete went through with a bad nonce")
	}
	post(t, c, ts.URL+"/wp-admin/scheduled-actions", url.Values{"_wpnonce": {nonce}, "do": {"delete"}, "job": {itoa64(failedID)}})
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE id = $1`, failedID).Scan(&n)
	if n != 0 {
		t.Error("delete did not remove the job")
	}
}

func TestDashboardNewsBoxFromHubIndex(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/")
	if strings.Contains(body, "WordPress Events and News") || strings.Contains(body, "News feeds are not loaded yet") || strings.Contains(body, "make.wordpress.org/community") {
		t.Error("the WordPress news placeholder is still on the Dashboard")
	}
	if !strings.Contains(body, "Pursae News") {
		t.Error("no Pursae News box")
	}
	srv.hubCacheSet(context.Background(), srv.hubCacheKey("wpgo_hub_index"), hubIndex{API: 1, Core: &hubCore{Latest: "99.0.0", Released: "2026-11-01", Notes: "Faster imports and a Scheduled Actions screen."}}, 3600e9)
	_, body, _ = get(t, c, ts.URL+"/wp-admin/")
	if !strings.Contains(body, "Pursae 99.0.0") || !strings.Contains(body, "Faster imports") {
		t.Errorf("release news missing: %s", body)
	}
}
