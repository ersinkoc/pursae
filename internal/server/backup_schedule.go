package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Scheduled backups: a recurring row of the jobs table (daily or weekly, like wp_schedule_event())
// whose action, bmgrScheduleJob, the scheduler dispatches through the core hook registry. Each run
// backs up the chosen parts with every user into a "scheduled" archive in the backups folder, then
// deletes the oldest scheduled archives beyond the retention count; manual backups and safety
// snapshots are never pruned. Remote storage (S3, FTP) is not part of this.

const (
	bmgrScheduleJob  = "wpgo_backup_scheduled"
	bmgrOptSchedule  = "backup_manager_schedule"      // off | daily | weekly
	bmgrOptKeep      = "backup_manager_schedule_keep" // scheduled archives kept
	bmgrOptCats      = "backup_manager_schedule_cats" // comma-separated category keys
	bmgrOptLast      = "backup_manager_schedule_last" // {"time", "file", "error"} of the last run
	bmgrKeepDefault  = 7
	bmgrKeepMax      = 100
	bmgrScheduleKind = "scheduled"
)

// bmgrScheduleSettings is the schedule as the screen edits it.
type bmgrScheduleSettings struct {
	Freq string
	Keep int
	Cats []string
}

func (s *Server) bmgrSchedule() bmgrScheduleSettings {
	o := bmgrScheduleSettings{Freq: s.settings.String(bmgrOptSchedule), Keep: s.settings.Int(bmgrOptKeep)}
	if o.Freq != "daily" && o.Freq != "weekly" {
		o.Freq = "off"
	}
	if o.Keep < 1 || o.Keep > bmgrKeepMax {
		o.Keep = bmgrKeepDefault
	}
	raw, set := s.settings.Get(bmgrOptCats).(string)
	for _, k := range strings.Split(raw, ",") {
		if bmgrCategoryByKey(strings.TrimSpace(k)) != nil {
			o.Cats = append(o.Cats, strings.TrimSpace(k))
		}
	}
	if !set {
		for _, c := range bmgrCategories {
			o.Cats = append(o.Cats, c.Key)
		}
	}
	return o
}

// bmgrRegisterSchedule hooks the scheduled-backup action into the core hook registry the scheduler
// dispatches unknown job names to. Called once at start-up, after the plugin runtime exists.
func (s *Server) bmgrRegisterSchedule() {
	if s.plugins == nil || s.plugins.hooks == nil {
		return
	}
	s.plugins.hooks.AddAction(bmgrScheduleJob, 10, func(ctx context.Context, _ ...any) {
		if err := s.bmgrScheduledRun(ctx); err != nil {
			slog.Error("scheduled backup", "err", err)
		}
	})
}

// bmgrSyncSchedule makes the jobs table match the settings: one recurring row while the plugin is
// on and a frequency is chosen, none otherwise. An unchanged frequency keeps its next run.
func (s *Server) bmgrSyncSchedule(ctx context.Context) error {
	freq := s.bmgrSchedule().Freq
	if !s.bmgrOn() || freq == "off" {
		_, err := s.store.Pool.Exec(ctx, `DELETE FROM jobs WHERE name = $1`, bmgrScheduleJob)
		return err
	}
	tag, err := s.store.Pool.Exec(ctx, `UPDATE jobs SET recurrence = $2, run_at = now() + $3::interval
		WHERE name = $1 AND recurrence <> '' AND recurrence <> $2`, bmgrScheduleJob, freq, bmgrFirstRun)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = s.store.Pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence)
		SELECT $1, now() + $3::interval, $2 WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE name = $1 AND recurrence <> '')`,
		bmgrScheduleJob, freq, bmgrFirstRun)
	return err
}

// bmgrFirstRun is the delay before the first run of a new schedule: soon, so the first scheduled
// archive exists the same day, then every day or week from there.
const bmgrFirstRun = "10 minutes"

// bmgrNextRun is when the scheduled backup runs next (zero: not scheduled).
func (s *Server) bmgrNextRun(ctx context.Context) time.Time {
	var t time.Time
	_ = s.store.Pool.QueryRow(ctx, `SELECT run_at FROM jobs WHERE name = $1 AND recurrence <> '' ORDER BY run_at LIMIT 1`, bmgrScheduleJob).Scan(&t)
	return t
}

// bmgrScheduledRun is one scheduled backup: run, record the result, prune old scheduled archives.
func (s *Server) bmgrScheduledRun(ctx context.Context) error {
	o := s.bmgrSchedule()
	if !s.bmgrOn() || o.Freq == "off" {
		return nil
	}
	// The action is reachable through the hook registry, so a second call within the hour is ignored.
	if last, ok := s.settings.Get(bmgrOptLast).(map[string]any); ok {
		if t, err := time.Parse(time.RFC3339, strAny(last["time"])); err == nil && time.Since(t) < time.Hour {
			return nil
		}
	}
	if len(o.Cats) == 0 {
		return s.bmgrRecordRun(ctx, "", errors.New("no parts are selected for scheduled backups"))
	}
	j, err := s.bmgrStart("backup")
	if err != nil {
		return s.bmgrRecordRun(ctx, "", err)
	}
	name, err := s.bmgrRunBackup(ctx, j, bmgrSelection{Categories: o.Cats, UserMode: "all", Kind: bmgrScheduleKind,
		Note: "Scheduled " + o.Freq + " backup"})
	s.bmgrUpdate(j, func(j *bmgrJob) { j.File = name })
	s.bmgrFinish(j, err)
	if rerr := s.bmgrRecordRun(ctx, name, err); rerr != nil || err != nil {
		return errors.Join(err, rerr)
	}
	s.bmgrPrune(o.Keep)
	return nil
}

func (s *Server) bmgrRecordRun(ctx context.Context, file string, runErr error) error {
	last := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "file": file, "error": ""}
	if runErr != nil {
		last["error"] = runErr.Error()
	}
	if err := s.settings.Set(ctx, map[string]any{bmgrOptLast: last}); err != nil {
		return err
	}
	return runErr
}

// bmgrPrune deletes the oldest scheduled archives so that keep of them remain.
func (s *Server) bmgrPrune(keep int) []string {
	var sched []bmgrEntry
	for _, e := range s.bmgrList() { // newest first
		if e.M != nil && e.M.Kind == bmgrScheduleKind && strings.HasPrefix(e.Name, bmgrScheduleKind+"-") {
			sched = append(sched, e)
		}
	}
	sort.SliceStable(sched, func(i, j int) bool { return sched[i].M.Created.After(sched[j].M.Created) })
	var removed []string
	for i := keep; i < len(sched); i++ {
		if err := os.Remove(filepath.Join(s.bmgrDir(), sched[i].Name)); err == nil {
			removed = append(removed, sched[i].Name)
		}
	}
	return removed
}

func strAny(v any) string {
	s, _ := v.(string)
	return s
}

// --- the Schedule tab ---------------------------------------------------------------------------

type bmgrScheduleView struct {
	Freqs             []choice
	Keep              int
	Cats              []choice
	Next              string
	LastWhen, LastErr string
	LastFile          string
}

func (s *Server) bmgrScheduleView(c *gin.Context) *bmgrScheduleView {
	o := s.bmgrSchedule()
	v := &bmgrScheduleView{Keep: o.Keep}
	for _, f := range [][2]string{{"off", "Off"}, {"daily", "Daily"}, {"weekly", "Weekly"}} {
		v.Freqs = append(v.Freqs, choice{Value: f[0], Label: f[1], Checked: f[0] == o.Freq})
	}
	for _, cat := range bmgrCategories {
		v.Cats = append(v.Cats, choice{Value: cat.Key, Label: cat.Label, Checked: contains(o.Cats, cat.Key)})
	}
	if t := s.bmgrNextRun(c.Request.Context()); !t.IsZero() && o.Freq != "off" {
		v.Next = t.In(s.loc()).Format("2006-01-02 15:04")
	}
	if last, ok := s.settings.Get(bmgrOptLast).(map[string]any); ok {
		if t, err := time.Parse(time.RFC3339, strAny(last["time"])); err == nil {
			v.LastWhen = t.In(s.loc()).Format("2006-01-02 15:04")
		}
		v.LastErr, v.LastFile = strAny(last["error"]), strAny(last["file"])
	}
	return v
}

// bmgrPostSchedule saves the Schedule tab.
func (s *Server) bmgrPostSchedule(c *gin.Context) {
	freq := c.PostForm("schedule")
	if freq != "daily" && freq != "weekly" {
		freq = "off"
	}
	keep, err := strconv.Atoi(strings.TrimSpace(c.PostForm("keep")))
	if err != nil || keep < 1 || keep > bmgrKeepMax {
		c.Redirect(http.StatusFound, s.bmgrBack("&tab=schedule&bmgr-error="+url.QueryEscape("Keep between 1 and "+strconv.Itoa(bmgrKeepMax)+" scheduled backups.")))
		return
	}
	var cats []string
	for _, k := range c.PostFormArray("cat") {
		if bmgrCategoryByKey(k) != nil && !contains(cats, k) {
			cats = append(cats, k)
		}
	}
	if freq != "off" && len(cats) == 0 {
		c.Redirect(http.StatusFound, s.bmgrBack("&tab=schedule&bmgr-error="+url.QueryEscape("Select at least one part to back up.")))
		return
	}
	ctx := c.Request.Context()
	if err := s.settings.Set(ctx, map[string]any{bmgrOptSchedule: freq, bmgrOptKeep: keep, bmgrOptCats: strings.Join(cats, ",")}); err != nil {
		c.Redirect(http.StatusFound, s.bmgrBack("&tab=schedule&bmgr-error="+url.QueryEscape(err.Error())))
		return
	}
	if err := s.bmgrSyncSchedule(ctx); err != nil {
		c.Redirect(http.StatusFound, s.bmgrBack("&tab=schedule&bmgr-error="+url.QueryEscape(err.Error())))
		return
	}
	c.Redirect(http.StatusFound, s.bmgrBack("&tab=schedule&bmgr-msg=scheduled"))
}
