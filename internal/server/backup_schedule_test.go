package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBmgrScheduledBackups(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	t.Setenv("WPGO_BACKUPS_DIR", t.TempDir())
	if err := srv.bmgrPluginAction(ctx, "activate"); err != nil {
		t.Fatal(err)
	}
	jobRow := func() (rec string, runAt time.Time, ok bool) {
		t.Helper()
		err := srv.store.Pool.QueryRow(ctx, `SELECT recurrence, run_at FROM jobs WHERE name = $1`, bmgrScheduleJob).Scan(&rec, &runAt)
		return rec, runAt, err == nil
	}
	if _, _, ok := jobRow(); ok {
		t.Fatal("a scheduled backup exists before one was set up")
	}
	if !srv.plugins.hooks.HasAction(bmgrScheduleJob) {
		t.Fatal("the scheduled-backup action is not registered")
	}

	// The Schedule tab renders and saves.
	code, body, _ := get(t, c, ts.URL+"/wp-admin/admin?page=backup-manager&tab=schedule")
	if code != http.StatusOK || !strings.Contains(body, `name="schedule"`) || !strings.Contains(body, `class="form-table"`) {
		t.Fatalf("Schedule tab: %d", code)
	}
	nonce := formNonce(t, body)
	save := func(form url.Values) string {
		t.Helper()
		form.Set("_wpnonce", nonce)
		form.Set("do", "schedule")
		code, _, loc := post(t, c, ts.URL+"/wp-admin/admin?page=backup-manager", form)
		if code != http.StatusFound {
			t.Fatalf("save schedule: %d", code)
		}
		return loc
	}
	if loc := save(url.Values{"schedule": {"daily"}, "keep": {"0"}, "cat": {"content"}}); !strings.Contains(loc, "bmgr-error") {
		t.Errorf("keep 0 accepted: %s", loc)
	}
	if loc := save(url.Values{"schedule": {"daily"}, "keep": {"2"}}); !strings.Contains(loc, "bmgr-error") {
		t.Errorf("a schedule without parts accepted: %s", loc)
	}
	if loc := save(url.Values{"schedule": {"daily"}, "keep": {"2"}, "cat": {"content", "settings", "bogus"}}); !strings.Contains(loc, "bmgr-msg=scheduled") {
		t.Fatalf("save: %s", loc)
	}
	if o := srv.bmgrSchedule(); o.Freq != "daily" || o.Keep != 2 || strings.Join(o.Cats, ",") != "content,settings" {
		t.Fatalf("saved schedule: %+v", o)
	}
	rec, first, ok := jobRow()
	if !ok || rec != "daily" {
		t.Fatalf("job row: %q %v", rec, ok)
	}
	// Saving again with the same frequency keeps the next run; switching to weekly reschedules.
	save(url.Values{"schedule": {"daily"}, "keep": {"2"}, "cat": {"content", "settings"}})
	if _, again, _ := jobRow(); !again.Equal(first) {
		t.Errorf("an unchanged schedule moved its next run: %v → %v", first, again)
	}
	save(url.Values{"schedule": {"weekly"}, "keep": {"2"}, "cat": {"content", "settings"}})
	if rec, _, _ := jobRow(); rec != "weekly" {
		t.Errorf("recurrence after switching to weekly: %q", rec)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = $1`, bmgrScheduleJob).Scan(&n)
	if n != 1 {
		t.Errorf("%d job rows, want 1", n)
	}

	// A manual backup is never pruned.
	var manual string
	bmgrRun(t, srv, "backup", func(j *bmgrJob) (err error) {
		manual, err = srv.bmgrRunBackup(ctx, j, bmgrSelection{Categories: []string{"settings"}, UserMode: "all"})
		return err
	})

	// Three runs through the scheduler (the job is due, dispatched through the hook registry):
	// only the newest two scheduled archives remain.
	var made []string
	for i := 0; i < 3; i++ {
		_ = srv.settings.Set(ctx, map[string]any{bmgrOptLast: map[string]any{"time": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)}})
		if _, err := srv.store.Pool.Exec(ctx, `UPDATE jobs SET run_at = now() - interval '1 second', status = 'queued' WHERE name = $1`, bmgrScheduleJob); err != nil {
			t.Fatal(err)
		}
		srv.runDueJobs(ctx, "test", srv.jobHandlers())
		last, _ := srv.settings.Get(bmgrOptLast).(map[string]any)
		if e := strAny(last["error"]); e != "" {
			t.Fatalf("scheduled run %d: %s", i, e)
		}
		f := strAny(last["file"])
		if !strings.HasPrefix(f, "scheduled-") {
			t.Fatalf("scheduled run %d made %q", i, f)
		}
		made = append(made, f)
		m, err := bmgrReadManifest(filepath.Join(srv.bmgrDir(), f))
		if err != nil || m.Kind != bmgrScheduleKind || strings.Join(m.Categories, ",") != "content,settings" {
			t.Fatalf("manifest of %s: %+v %v", f, m, err)
		}
		time.Sleep(1100 * time.Millisecond) // distinct archive times
	}
	if _, err := os.Stat(filepath.Join(srv.bmgrDir(), made[0])); !os.IsNotExist(err) {
		t.Errorf("the oldest scheduled backup %s was kept", made[0])
	}
	for _, f := range append(made[1:], manual) {
		if _, err := os.Stat(filepath.Join(srv.bmgrDir(), f)); err != nil {
			t.Errorf("%s is gone: %v", f, err)
		}
	}
	if rec, runAt, ok := jobRow(); !ok || rec != "weekly" || runAt.Before(time.Now().Add(6*24*time.Hour)) {
		t.Errorf("the job was not rescheduled a week ahead: %q %v %v", rec, runAt, ok)
	}
	// A second call within the hour (the action is reachable through the hooks) does nothing.
	if err := srv.bmgrScheduledRun(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(srv.bmgrList()); got != 3 {
		t.Errorf("%d archives after an early repeat, want 3", got)
	}
	_, body, _ = get(t, c, ts.URL+"/wp-admin/admin?page=backup-manager&tab=backups")
	if !strings.Contains(body, "Scheduled backup") {
		t.Error("the Backups tab does not mark scheduled backups")
	}

	// Turning the schedule off, or the plugin, removes the job.
	save(url.Values{"schedule": {"off"}, "keep": {"2"}})
	if _, _, ok := jobRow(); ok {
		t.Error("the job survived turning the schedule off")
	}
	save(url.Values{"schedule": {"daily"}, "keep": {"2"}, "cat": {"content"}})
	if err := srv.bmgrPluginAction(ctx, "deactivate"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := jobRow(); ok {
		t.Error("the job survived deactivating the plugin")
	}
	if err := srv.bmgrPluginAction(ctx, "activate"); err != nil {
		t.Fatal(err)
	}
	if rec, _, ok := jobRow(); !ok || rec != "daily" {
		t.Error("reactivating the plugin did not restore the schedule")
	}
}
