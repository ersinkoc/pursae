package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/auth"
	"wpgo/internal/mail"
)

// WP-Cron without visitors: a runner claims due rows of the jobs table with FOR UPDATE SKIP LOCKED,
// so several WordPress Go instances can share one database without running a job twice. Recurring
// events reschedule themselves (wp_schedule_event()); one-off jobs (e-mail) are queued with queueJob.

type jobHandler func(ctx context.Context, payload map[string]any) error

var recurrences = map[string]time.Duration{
	"hourly": time.Hour, "twicedaily": 12 * time.Hour, "daily": 24 * time.Hour, "weekly": 7 * 24 * time.Hour,
}

// coreEvents are the scheduled events a fresh WordPress install has, plus WordPress Go's own.
var coreEvents = []struct{ Name, Recurrence string }{
	{"wp_scheduled_delete", "daily"},            // empty trash older than EMPTY_TRASH_DAYS
	{"wp_scheduled_auto_draft_delete", "daily"}, // drop auto-drafts older than 7 days
	{"delete_expired_transients", "daily"},
	{"wp_session_gc", "hourly"},           // expired login sessions and old login attempts
	{"wpgo_cache_janitor", "hourly"},      // expired page-cache entries
	{"wpgo_cache_preload", "daily"},       // Pursae Cache: daily preload (when switched on)
	{"wpgo_hub_check", "daily"},           // ping the Hub and refresh the release / marketplace cache
	{"gontact_captcha_cleanup", "hourly"}, // expired Gontact Form CAPTCHA challenges
	{"seogo_404_trim", "hourly"},          // Pursae SEO: keep the 404 log at its limit (seogo_front.go)
	{"goo_import_cleanup", "daily"},       // Pursae Commerce: delete abandoned product-import uploads (goo_admin_catalog_csv_batch.go)
}

// recurrenceInterval maps a recurrence name to its interval: the WordPress names above, or
// "every:<seconds>" for plugin events (schedule_event). Unknown names fall back to a day.
func recurrenceInterval(rec string) time.Duration {
	if d, ok := recurrences[rec]; ok {
		return d
	}
	if n, err := strconv.ParseInt(strings.TrimPrefix(rec, "every:"), 10, 64); err == nil && n >= 60 {
		return time.Duration(n) * time.Second
	}
	return 24 * time.Hour
}

func (s *Server) jobHandlers() map[string]jobHandler {
	return map[string]jobHandler{
		"wp_scheduled_delete": func(ctx context.Context, _ map[string]any) error {
			days := emptyTrashDays()
			// One by one through DeletePost, so the children of a deleted page move up to its parent.
			var ids []int64
			rows, err := s.store.Pool.Query(ctx, `SELECT id FROM posts WHERE status = 'trash' AND updated_at < now() - make_interval(days => $1) ORDER BY id`, days)
			if err == nil {
				ids, err = pgx.CollectRows(rows, pgx.RowTo[int64])
			}
			for _, id := range ids {
				if err != nil {
					break
				}
				err = s.store.DeletePost(ctx, id)
			}
			if err == nil {
				_, err = s.store.Pool.Exec(ctx, `DELETE FROM comments WHERE status = 'trash' AND updated_at < now() - make_interval(days => $1)`, days)
			}
			return err
		},
		"wp_scheduled_auto_draft_delete": func(ctx context.Context, _ map[string]any) error {
			_, err := s.store.Pool.Exec(ctx, `DELETE FROM posts WHERE status = 'auto-draft' AND created_at < now() - interval '7 days'`)
			return err
		},
		"delete_expired_transients": func(ctx context.Context, _ map[string]any) error {
			_, err := s.store.Pool.Exec(ctx, `DELETE FROM transients WHERE expires_at IS NOT NULL AND expires_at < now()`)
			return err
		},
		"wp_session_gc": func(ctx context.Context, _ map[string]any) error {
			_, err := s.store.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
			if err == nil {
				_, err = s.store.Pool.Exec(ctx, `DELETE FROM login_attempts WHERE created_at < now() - interval '1 day'`)
			}
			return err
		},
		"wpgo_cache_janitor": func(context.Context, map[string]any) error {
			if c := s.currentCache(); c != nil {
				c.PurgeExpired()
			}
			return nil
		},
		"wpgo_cache_preload": func(context.Context, map[string]any) error {
			if s.settings.Bool("wpgo_cache_preload_daily") {
				s.preloadCache()
			}
			return nil
		},
		"wpgo_hub_check":          func(ctx context.Context, _ map[string]any) error { return s.hubPing(ctx) },
		"gontact_captcha_cleanup": func(ctx context.Context, _ map[string]any) error { return s.gontactCaptchaCleanup(ctx) },
		gontactWebhookJob:         func(ctx context.Context, p map[string]any) error { return s.gontactWebhookRun(ctx, p) },
		"seogo_404_trim":          func(ctx context.Context, _ map[string]any) error { return s.seogo404Trim(ctx) },
		"goo_import_cleanup":      func(ctx context.Context, _ map[string]any) error { return s.gooImportCleanup(ctx) },
		"wp_mail": func(ctx context.Context, p map[string]any) error {
			var msg mail.Message
			raw, _ := json.Marshal(p)
			if err := json.Unmarshal(raw, &msg); err != nil {
				return err
			}
			err := s.mailer().Send(ctx, msg)
			s.gontactJobDelivered(context.WithoutCancel(ctx), p, err)
			return err
		},
		"wp_mail_smtp_daily": func(ctx context.Context, _ map[string]any) error { return s.mailSMTPDaily(ctx) },
		gooikeScanJob:        func(ctx context.Context, _ map[string]any) error { return s.gooikeScanRun(ctx) },
		gooikePurgeJob:       func(ctx context.Context, _ map[string]any) error { _, err := s.gooikePurge(ctx); return err }, // Gooike consent log retention
		pixelPurgeJob:        func(ctx context.Context, _ map[string]any) error { _, err := s.pixelPurge(ctx); return err },  // Pursae Pixel delivery log retention
		"adminsecgo_daily":   func(ctx context.Context, _ map[string]any) error { return s.adminsecDaily(ctx) },
		gooDailyJob:          func(ctx context.Context, _ map[string]any) error { return s.gooDaily(ctx) },                     // Goocommerce
		gooWeeklyJob:         func(ctx context.Context, _ map[string]any) error { return s.gooWeeklySummary(ctx, time.Now()) }, // Goocommerce: weekly summary e-mail
		gooHourlyJob:         func(ctx context.Context, _ map[string]any) error { return s.gooHourly(ctx) },                    // Goocommerce: held stock
		hgTickJob:            func(ctx context.Context, _ map[string]any) error { return s.hgTick(ctx) },                       // HeadlessGo: schedules, webhook queue
	}
}

// emptyTrashDays is EMPTY_TRASH_DAYS: how long trashed posts and comments are kept before
// wp_scheduled_delete removes them, from WPGO_EMPTY_TRASH_DAYS (default 30). 0 empties the trash
// at every run of the daily job, as WordPress' cron does with EMPTY_TRASH_DAYS = 0.
func emptyTrashDays() int {
	if v := strings.TrimSpace(os.Getenv("WPGO_EMPTY_TRASH_DAYS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
		slog.Warn("WPGO_EMPTY_TRASH_DAYS is not a whole number of days; using 30", "value", v)
	}
	return 30
}

// mailer is wp_mail()'s transport with the site's From address (wordpress@<domain> by default).
func (s *Server) mailer() *mail.Mailer {
	from := s.cfg.MailFrom
	if from == "" {
		host := "localhost"
		if u := s.settings.String("home"); u != "" {
			if h := hostOf(u); h != "" {
				host = h
			}
		}
		// Quoted and encoded: a site title such as "Acme, Inc." or "News: Daily" is not a valid bare
		// display name, and an unparsable From breaks the envelope sender of every mailer.
		from = (&netmail.Address{Name: s.settings.String("blogname"), Address: "wordpress@" + host}).String()
	}
	dir := s.cfg.MailDir
	if dir == "" {
		dir = filepath.Join(filepath.Dir(filepath.Clean(s.cfg.UploadsDir)), "mail")
	}
	m := &mail.Mailer{Host: s.cfg.SMTPHost, Port: s.cfg.SMTPPort, User: s.cfg.SMTPUser, Pass: s.cfg.SMTPPass, From: from, Dir: dir}
	s.mailSMTPAttach(m) // WPGO Mail SMTP takes over delivery while it is active
	return m
}

// queueJob schedules a one-off job (runAt zero means now).
//
// "Now" is the database's clock, the one runDueJobs compares run_at with: a timestamp from this
// process would leave the job in the future whenever the app host's clock runs ahead of the
// database's (Docker VMs drift), so a mail queued "now" would wait for the next scheduler tick.
func (s *Server) queueJob(ctx context.Context, name string, payload any, runAt time.Time) error {
	raw, _ := json.Marshal(payload)
	var at *time.Time
	if !runAt.IsZero() {
		at = &runAt
	}
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO jobs(name, payload, run_at) VALUES ($1, $2, COALESCE($3, now()))`, name, raw, at)
	return err
}

// sendMail queues an e-mail; delivery happens in the job runner so requests never wait on SMTP.
func (s *Server) sendMail(ctx context.Context, msg mail.Message) {
	if err := s.queueJob(context.WithoutCancel(ctx), "wp_mail", msg, time.Time{}); err != nil {
		slog.Error("queue mail", "err", err)
	}
}

// ensureEvents schedules the recurring core events that are missing. Recurring rows a version
// before this one marked "failed" (after five errors in a row) are put back on the queue: a
// recurring event never fails for good.
func (s *Server) ensureEvents(ctx context.Context) {
	_, _ = s.store.Pool.Exec(ctx, `UPDATE jobs SET status = 'queued', attempts = 0, locked_by = NULL, run_at = now() + interval '1 minute'
		WHERE status = 'failed' AND recurrence <> ''`)
	for _, e := range coreEvents {
		// Two instances starting together both pass NOT EXISTS: the unique index jobs_recurring_name (0079)
		// turns the second insert into a no-op.
		_, _ = s.store.Pool.Exec(ctx, `INSERT INTO jobs(name, run_at, recurrence)
			SELECT $1, now() + interval '1 minute', $2 WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE name = $1 AND recurrence <> '')
			ON CONFLICT (name) WHERE recurrence <> '' AND NOT (payload ? 'plugin') DO NOTHING`, e.Name, e.Recurrence)
	}
	// A version the Manager has not heard of yet (the first start after a core update): ping it
	// in a minute instead of at the next daily check, so its Sites screen shows the new version.
	if s.hubBase() != "" && s.settings.String(hubReportedVersionOption) != Version {
		_, _ = s.store.Pool.Exec(ctx, `INSERT INTO jobs(name, run_at)
			SELECT 'wpgo_hub_check', now() + interval '1 minute' WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE name = 'wpgo_hub_check' AND recurrence = '' AND status = 'queued')`)
	}
}

// Scheduler timing. A running job's locked_at is refreshed every jobHeartbeatEvery; a row still
// "running" whose lock is older than jobStaleAfter belonged to an instance that died.
const (
	schedulerTick = 15 * time.Second
	jobStaleAfter = 10 * time.Minute
	jobMaxRetries = 5
)

var jobHeartbeatEvery = time.Minute // a variable for tests

// RunScheduler runs scheduled posts and due jobs every 15 seconds until ctx ends. It returns once
// the job in hand has finished (or, interrupted by ctx, was put back on the queue).
func (s *Server) RunScheduler(ctx context.Context) {
	s.ensureEvents(ctx)
	host, _ := os.Hostname()
	// The random part keeps two processes apart that share hostname and pid (a container's
	// pid 1 across a restart), so one never refreshes the other's locks.
	worker := fmt.Sprintf("%s:%d:%s", host, os.Getpid(), auth.RandomToken(6))
	handlers := s.jobHandlers()
	t := time.NewTicker(schedulerTick)
	defer t.Stop()
	for {
		if n, err := s.store.PublishDuePosts(ctx); err != nil {
			if ctx.Err() == nil {
				slog.Error("scheduler: publish_future_post", "err", err)
			}
		} else if n > 0 {
			slog.Info("scheduler: published scheduled posts", "count", n)
			s.purgeAll(ctx)
		}
		s.runDueJobs(ctx, worker, handlers)
		s.maintTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type dueJob struct {
	id        int64
	name, rec string
	payload   map[string]any
	attempts  int
}

func (s *Server) runDueJobs(ctx context.Context, worker string, handlers map[string]jobHandler) {
	if ctx.Err() != nil {
		return
	}
	// Jobs stuck in "running" (a crashed instance: a live one refreshes locked_at) go back to the queue.
	_, _ = s.store.Pool.Exec(ctx, `UPDATE jobs SET status = 'queued', locked_by = NULL WHERE status = 'running' AND locked_at < now() - make_interval(secs => $1)`,
		jobStaleAfter.Seconds())
	rows, err := s.store.Pool.Query(ctx, `UPDATE jobs SET status = 'running', locked_by = $1, locked_at = now()
		WHERE id IN (SELECT id FROM jobs WHERE status = 'queued' AND run_at <= now() ORDER BY run_at LIMIT 20 FOR UPDATE SKIP LOCKED)
		RETURNING id, name, payload, recurrence, attempts`, worker)
	if err != nil {
		return
	}
	var due []dueJob
	for rows.Next() {
		var j dueJob
		var raw []byte
		if rows.Scan(&j.id, &j.name, &raw, &j.rec, &j.attempts) == nil {
			_ = json.Unmarshal(raw, &j.payload)
			due = append(due, j)
		}
	}
	rows.Close()
	if len(due) == 0 {
		return
	}
	// Status writes must land even when ctx was cancelled by a shutdown mid-job.
	wctx := context.WithoutCancel(ctx)
	stopBeat := s.jobHeartbeat(wctx, worker)
	defer stopBeat()
	for i, j := range due {
		if ctx.Err() != nil {
			// Shutting down: the claimed jobs not started yet go back to the queue untouched.
			ids := make([]int64, 0, len(due)-i)
			for _, r := range due[i:] {
				ids = append(ids, r.id)
			}
			_, _ = s.store.Pool.Exec(wctx, `UPDATE jobs SET status = 'queued', locked_by = NULL WHERE id = ANY($1) AND locked_by = $2`, ids, worker)
			return
		}
		runErr := s.runJob(ctx, handlers, j)
		s.finishJob(wctx, ctx, worker, j, runErr)
	}
	// Keep the table small: finished one-off jobs are kept for a week for Site Health.
	_, _ = s.store.Pool.Exec(wctx, `DELETE FROM jobs WHERE status = 'done' AND recurrence = '' AND updated_at < now() - interval '7 days'`)
}

// runJob runs one job's handler (or the plugin action of that name); a panic is the job's error.
func (s *Server) runJob(ctx context.Context, handlers map[string]jobHandler, j dueJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	h := handlers[j.name]
	owner, _ := j.payload["plugin"].(string)
	switch {
	case h != nil:
		return h(ctx, j.payload)
	case owner != "":
		// A plugin's event (ScheduleEvent) goes to that plugin only; while the plugin is not
		// running (deactivated, disabled) the run is skipped rather than failing every time.
		if s.plugins != nil {
			s.plugins.hooks.DoActionOwner(ctx, owner, j.name, j.payload)
		}
		return nil
	case s.plugins != nil && s.plugins.hooks.HasAction(j.name):
		s.plugins.hooks.DoAction(ctx, j.name, j.payload)
		return nil
	}
	return fmt.Errorf("no handler for %q", j.name)
}

// finishJob records a job's outcome. Recurring jobs always go back to the queue: a failure is
// retried with backoff (1, 4, 9, 16 minutes, never later than the next regular run), and after
// jobMaxRetries failures in a row the job waits for its next regular run, keeping its error
// for Site Health. One-off jobs fail for good after jobMaxRetries attempts. A job interrupted by
// shutdown goes back to the queue without counting an attempt.
func (s *Server) finishJob(wctx, ctx context.Context, worker string, j dueJob, runErr error) {
	interval := recurrenceInterval(j.rec)
	secs := int(interval.Seconds())
	switch {
	case runErr != nil && ctx.Err() != nil:
		_, _ = s.store.Pool.Exec(wctx, `UPDATE jobs SET status = 'queued', locked_by = NULL WHERE id = $1 AND locked_by = $2`, j.id, worker)
	case runErr == nil && j.rec != "":
		// The next slot of the schedule after now (wp_reschedule_event()): runs missed while the site was
		// down are not caught up one by one, and the run that just caught up is not repeated at once.
		_, _ = s.store.Pool.Exec(wctx, `UPDATE jobs SET status = 'queued', attempts = 0, last_error = '', locked_by = NULL,
			run_at = run_at + make_interval(secs => ($2 * (floor(greatest(extract(epoch FROM now() - run_at), 0) / $2) + 1))::double precision)
			WHERE id = $1`, j.id, secs)
	case runErr == nil:
		_, _ = s.store.Pool.Exec(wctx, `UPDATE jobs SET status = 'done', locked_by = NULL WHERE id = $1`, j.id)
	case j.rec != "":
		retry := time.Duration(j.attempts+1) * time.Duration(j.attempts+1) * time.Minute
		if j.attempts+1 >= jobMaxRetries || retry > interval {
			retry = interval
			slog.Error("recurring job failed; next try at its next run", "job", j.name, "attempts", j.attempts+1, "err", runErr)
		}
		_, _ = s.store.Pool.Exec(wctx, `UPDATE jobs SET status = 'queued', attempts = attempts + 1, last_error = $2, locked_by = NULL,
			run_at = now() + make_interval(secs => $3) WHERE id = $1`, j.id, runErr.Error(), int(retry.Seconds()))
	case j.attempts+1 < jobMaxRetries:
		// Retry with backoff: 1, 4, 9, 16 minutes.
		_, _ = s.store.Pool.Exec(wctx, `UPDATE jobs SET status = 'queued', attempts = attempts + 1, last_error = $2, locked_by = NULL,
			run_at = now() + make_interval(mins => (attempts + 1) * (attempts + 1)) WHERE id = $1`, j.id, runErr.Error())
	default:
		_, _ = s.store.Pool.Exec(wctx, `UPDATE jobs SET status = 'failed', attempts = attempts + 1, last_error = $2, locked_by = NULL WHERE id = $1`, j.id, runErr.Error())
		slog.Error("job failed", "job", j.name, "err", runErr)
	}
}

// jobHeartbeat refreshes locked_at of this worker's running jobs until the returned stop is
// called, so a job that runs longer than jobStaleAfter is not handed to another instance.
func (s *Server) jobHeartbeat(ctx context.Context, worker string) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(jobHeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_, _ = s.store.Pool.Exec(ctx, `UPDATE jobs SET locked_at = now() WHERE status = 'running' AND locked_by = $1`, worker)
			}
		}
	}()
	return func() {
		close(done)
		wg.Wait()
	}
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return ""
}
