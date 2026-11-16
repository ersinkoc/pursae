package server

import (
	"context"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/cli"
	"wpgo/internal/coreupdate"
	"wpgo/internal/db"
	"wpgo/internal/wp"
)

// registerOpsCLI registers the operations commands. See cli.go for the environment (cliEnv) and conventions.
//
// Nouns: cron (+ cron event), db, core, backup, language (+ language core), mail, site.
// This file holds cron, db and core; cli_ops_site.go holds backup, language, mail and site.
func registerOpsCLI(r *cli.Registry, e *cliEnv) {
	registerOpsCronCLI(r, e)
	registerOpsDBCLI(r, e)
	registerOpsCoreCLI(r, e)
	registerCoreInstallCLI(r, e) // cli_core_install.go: core install, core is-installed
	registerOpsBackupCLI(r, e)
	registerOpsLanguageCLI(r, e)
	registerOpsMailCLI(r, e)
	registerOpsSiteCLI(r, e)
}

// opsLoc is the site time zone (Server.loc) from the loaded settings, without building a Server.
func opsLoc(c *cli.Ctx, e *cliEnv) *time.Location {
	set, err := e.Settings(c.Context)
	if err != nil {
		return time.UTC
	}
	return wp.Location(set.String("timezone_string"), set.Float("gmt_offset"))
}

// --- cron ---------------------------------------------------------------------------

// jobStatuses are the states of a jobs row.
var jobStatuses = []string{"queued", "running", "done", "failed"}

// cronEventFields are the default columns of `cron event list`.
var cronEventFields = []string{"ID", "hook", "status", "recurrence", "attempts", "next_run", "last_error"}

// cronSchedulerIdle is how long without any job pickup makes the CLI suspect no server is running
// (the core schedule has hourly events, so a running scheduler picks one up at least that often).
const cronSchedulerIdle = 2 * time.Hour

// cronEventRef is a `cron event run` argument: a job ID or a hook (job name).
type cronEventRef struct {
	ID   int64
	Hook string
}

func parseCronEventRef(s string) cronEventRef {
	s = strings.TrimSpace(s)
	if id, err := strconv.ParseInt(s, 10, 64); err == nil && id > 0 {
		return cronEventRef{ID: id}
	}
	return cronEventRef{Hook: s}
}

// coreEventName reports whether hook is one of the core recurring events ensureEvents recreates.
func coreEventName(hook string) bool {
	for _, e := range coreEvents {
		if e.Name == hook {
			return true
		}
	}
	return hook == bmgrScheduleJob
}

func registerOpsCronCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("cron", "Inspect and manage the job queue (scheduled events, queued mail, plugin events).")
	r.Noun("cron event", "List, run, retry and delete jobs of the queue.")

	r.Add(cli.Command{
		Path:  "cron event list",
		Short: "List jobs of the queue (Tools › Scheduled Actions).",
		Long: "Lists the jobs table: recurring events (hourly, daily, every:<seconds> …), one-off jobs such as\n" +
			"queued e-mail, and plugin events. Failed jobs come first, then by next run, newest first.\n" +
			"next_run is in the site time zone.",
		Flags: []cli.Flag{
			{Name: "status", Usage: "Only jobs in this state.", Values: jobStatuses},
			{Name: "hook", Usage: "Only jobs with this hook (job name)."},
			{Name: "recurring", Bool: true, Usage: "Only recurring events."},
			{Name: "limit", Usage: "At most this many jobs (0: all).", Default: "200"},
		},
		Examples: []string{
			"cron event list",
			"cron event list --status=failed --fields=ID,hook,last_error",
			"cron event list --recurring --format=json",
		},
		Run: func(c *cli.Ctx) error {
			limit, err := c.Int("limit", 200)
			if err != nil {
				return err
			}
			if limit < 0 {
				return cli.Usagef("--limit must be 0 or more")
			}
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			var lim any
			if limit > 0 {
				lim = limit
			}
			rows, err := pool.Query(c.Context, `SELECT id, name, status, recurrence, attempts, last_error, run_at, payload FROM jobs
				WHERE ($1 = '' OR status = $1) AND ($2 = '' OR name = $2) AND (NOT $3 OR recurrence <> '')
				ORDER BY (status = 'failed') DESC, run_at DESC, id DESC LIMIT $4`, c.Flag("status"), c.Flag("hook"), c.Bool("recurring"), lim)
			if err != nil {
				return err
			}
			defer rows.Close()
			loc := opsLoc(c, e)
			var out []cli.Row
			for rows.Next() {
				var id int64
				var name, status, rec, lastErr string
				var attempts int
				var at time.Time
				var payload map[string]any
				if err := rows.Scan(&id, &name, &status, &rec, &attempts, &lastErr, &at, &payload); err != nil {
					return err
				}
				out = append(out, cli.Row{"ID": id, "hook": name, "status": status, "recurrence": rec, "attempts": attempts,
					"next_run": at.In(loc), "last_error": lastErr, "payload": payload})
			}
			if err := rows.Err(); err != nil {
				return err
			}
			return c.Print(out, cli.View{Fields: cronEventFields, ID: "ID"})
		},
	})

	r.Add(cli.Command{
		Path:  "cron event run",
		Short: "Make jobs due now, so the running server's scheduler runs them.",
		Long: "Sets run_at to now for each queued or failed job (a failed one also gets a fresh attempt count).\n" +
			"The handlers are NOT run in this process: they need the full server (plugin runtime, page cache,\n" +
			"mailer). The scheduler of a running server picks the jobs up within 15 seconds. With --wait the\n" +
			"command waits until they have run and reports the outcome.\n\n" +
			"An argument is a job ID or a hook (job name); a hook makes every queued or failed job of that\n" +
			"name due.",
		Args: []cli.Arg{{Name: "event", Usage: "Job ID or hook name.", Repeat: true}},
		Flags: []cli.Flag{
			{Name: "wait", Bool: true, Usage: "Wait until the jobs have run."},
			{Name: "timeout", Usage: "Seconds to wait with --wait.", Default: "120"},
		},
		Examples: []string{
			"cron event run wp_scheduled_delete",
			"cron event run 1234 --wait",
		},
		Run: func(c *cli.Ctx) error {
			timeout, err := c.Int("timeout", 120)
			if err != nil {
				return err
			}
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			type due struct {
				id       int64
				hook     string
				mark     time.Time
				attempts int
			}
			var jobs []due
			for _, a := range c.Args {
				ref := parseCronEventRef(a)
				if ref.ID == 0 && ref.Hook == "" {
					return cli.Usagef("empty event")
				}
				rows, err := pool.Query(c.Context, `UPDATE jobs SET run_at = now(), status = 'queued', locked_by = NULL,
					attempts = CASE WHEN status = 'failed' THEN 0 ELSE attempts END,
					last_error = CASE WHEN status = 'failed' THEN '' ELSE last_error END
					WHERE (id = $1 OR ($1 = 0 AND name = $2)) AND status IN ('queued', 'failed')
					RETURNING id, name, run_at, attempts`, ref.ID, ref.Hook)
				if err != nil {
					return err
				}
				n := 0
				for rows.Next() {
					var d due
					if err := rows.Scan(&d.id, &d.hook, &d.mark, &d.attempts); err != nil {
						rows.Close()
						return err
					}
					jobs = append(jobs, d)
					n++
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					return err
				}
				if n == 0 {
					return cli.Errorf("no queued or failed job matches '%s' (a running job cannot be re-run, a finished one-off job is done)", a)
				}
			}
			for _, d := range jobs {
				c.Log("Scheduled '%s' (ID %d) to run now.", d.hook, d.id)
			}
			if last := cronLastPickup(c.Context, pool); last.IsZero() || time.Since(last) > cronSchedulerIdle {
				c.Warning("no job has been picked up recently: the jobs run only while a server (%s serve) is running.", c.Prog)
			}
			if !c.Bool("wait") {
				c.Success("%d job(s) are due; a running server's scheduler runs them within 15 seconds.", len(jobs))
				return nil
			}
			deadline := time.Now().Add(time.Duration(timeout) * time.Second)
			failed := 0
			for _, d := range jobs {
				for {
					var status, lastErr string
					var at time.Time
					err := pool.QueryRow(c.Context, `SELECT status, last_error, run_at FROM jobs WHERE id = $1`, d.id).Scan(&status, &lastErr, &at)
					if errors.Is(err, pgx.ErrNoRows) {
						c.Log("'%s' (ID %d) ran and was removed.", d.hook, d.id)
						break
					}
					if err != nil {
						return err
					}
					if finished, ok := cronRunFinished(status, at, d.mark); finished {
						if ok && lastErr == "" {
							c.Log("'%s' (ID %d) ran successfully.", d.hook, d.id)
						} else {
							failed++
							c.Warning("'%s' (ID %d) failed: %s", d.hook, d.id, defaultStr(lastErr, status))
						}
						break
					}
					if time.Now().After(deadline) {
						return cli.Errorf("'%s' (ID %d) has not run after %d seconds; is a server running?", d.hook, d.id, timeout)
					}
					select {
					case <-c.Context.Done():
						return c.Context.Err()
					case <-time.After(time.Second):
					}
				}
			}
			if failed > 0 {
				return cli.Errorf("%d of %d job(s) failed", failed, len(jobs))
			}
			c.Success("%d job(s) ran.", len(jobs))
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "cron event retry",
		Short: "Put failed jobs back on the queue with a fresh attempt count.",
		Args:  []cli.Arg{{Name: "id", Usage: "Job ID.", Optional: true, Repeat: true}},
		Flags: []cli.Flag{{Name: "all-failed", Bool: true, Usage: "Retry every failed job."}},
		Examples: []string{
			"cron event retry 1234",
			"cron event retry --all-failed",
		},
		Run: func(c *cli.Ctx) error {
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			all := c.Bool("all-failed")
			if len(ids) == 0 && !all {
				return cli.Usagef("give a job ID or --all-failed")
			}
			if len(ids) > 0 && all {
				return cli.Usagef("give job IDs or --all-failed, not both")
			}
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			// Tools › Scheduled Actions "Retry" (admin_scheduled_actions.go).
			tag, err := pool.Exec(c.Context, `UPDATE jobs SET status = 'queued', attempts = 0, last_error = '', run_at = now(), locked_by = NULL
				WHERE status = 'failed' AND ($1 OR id = ANY($2))`, all, ids)
			if err != nil {
				return err
			}
			n := tag.RowsAffected()
			if len(ids) > 0 && int(n) < len(ids) {
				c.Warning("%d of %d job(s) were not failed (or do not exist) and were left alone.", len(ids)-int(n), len(ids))
			}
			c.Success("%d job(s) queued again.", n)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "cron event delete",
		Short: "Delete jobs from the queue (never a running one).",
		Long: "Deletes the given jobs, or every job in a state with --status. Core recurring events\n" +
			"(wp_scheduled_delete, wpgo_cache_janitor …) are created again at the next server start, and the\n" +
			"scheduled backup event follows the Backup Manager schedule.",
		Args:  []cli.Arg{{Name: "id", Usage: "Job ID.", Optional: true, Repeat: true}},
		Flags: []cli.Flag{{Name: "status", Usage: "Delete every job in this state.", Values: []string{"failed", "done"}}},
		Examples: []string{
			"cron event delete 1234 1235 --yes",
			"cron event delete --status=failed --yes",
		},
		Run: func(c *cli.Ctx) error {
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			status := c.Flag("status")
			if len(ids) == 0 && status == "" {
				return cli.Usagef("give a job ID or --status")
			}
			if len(ids) > 0 && status != "" {
				return cli.Usagef("give job IDs or --status, not both")
			}
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			if status != "" {
				var n int
				if err := pool.QueryRow(c.Context, `SELECT count(*) FROM jobs WHERE status = $1`, status).Scan(&n); err != nil {
					return err
				}
				if n == 0 {
					c.Success("No %s jobs.", status)
					return nil
				}
				if err := c.Confirm("Delete %d %s job(s)?", n, status); err != nil {
					return err
				}
			} else if err := c.Confirm("Delete %d job(s)?", len(ids)); err != nil {
				return err
			}
			rows, err := pool.Query(c.Context, `DELETE FROM jobs WHERE status <> 'running' AND (($1 <> '' AND status = $1) OR ($1 = '' AND id = ANY($2)))
				RETURNING name, recurrence`, status, ids)
			if err != nil {
				return err
			}
			n := 0
			recreated := map[string]bool{}
			for rows.Next() {
				var name, rec string
				if err := rows.Scan(&name, &rec); err != nil {
					rows.Close()
					return err
				}
				n++
				if rec != "" && coreEventName(name) {
					recreated[name] = true
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if len(ids) > 0 && n < len(ids) {
				c.Warning("%d job(s) were running or do not exist and were left alone.", len(ids)-n)
			}
			for name := range recreated {
				c.Warning("'%s' is a core recurring event: the server schedules it again at its next start.", name)
			}
			c.Success("Deleted %d job(s).", n)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "cron status",
		Short: "Show the queue counts and the recurring events with their next run.",
		Long: "Counts the jobs by state, shows when a scheduler last picked up a job (no pickup for hours\n" +
			"means no server is running) and lists the recurring events like Site Health › Info.",
		Examples: []string{"cron status", "cron status --format=json"},
		Run: func(c *cli.Ctx) error {
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			pool := s.store.Pool
			item := cli.Row{"queued": 0, "running": 0, "done": 0, "failed": 0}
			total := 0
			rows, err := pool.Query(c.Context, `SELECT status, count(*) FROM jobs GROUP BY status`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var st string
				var n int
				if err := rows.Scan(&st, &n); err != nil {
					rows.Close()
					return err
				}
				item[st] = n
				total += n
			}
			rows.Close()
			item["total"] = total
			last := cronLastPickup(c.Context, pool)
			item["last_pickup"] = last.In(s.loc())
			if last.IsZero() {
				item["last_pickup"] = nil
			}
			item["scheduler"] = "active"
			if last.IsZero() || time.Since(last) > cronSchedulerIdle {
				item["scheduler"] = "idle (no server running?)"
			}
			var events []cli.Row
			for _, ev := range s.healthEvents(c.Context) {
				events = append(events, cli.Row{"event": ev[0], "schedule": ev[1]})
			}
			fields := []string{"queued", "running", "done", "failed", "total", "last_pickup", "scheduler"}
			switch c.Format {
			case "json", "yaml":
				item["events"] = events
				return c.PrintItem(item, append(fields, "events"))
			}
			if err := c.PrintItem(item, fields); err != nil {
				return err
			}
			if c.Field != "" || len(c.Fields) > 0 {
				return nil
			}
			c.Println()
			return cli.Write(c.Stdout(), c.Format, events, []string{"event", "schedule"})
		},
	})
}

// cronLastPickup is when a scheduler last claimed a job (zero: never). locked_at stays on the row
// after the job finished, so the newest one is the last pickup of any instance.
func cronLastPickup(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) time.Time {
	var at *time.Time
	if err := q.QueryRow(ctx, `SELECT max(locked_at) FROM jobs`).Scan(&at); err != nil || at == nil {
		return time.Time{}
	}
	return *at
}

// cronRunFinished reports whether a job made due at mark has run since: a one-off job ends done
// or failed, a recurring one (or a one-off waiting for its retry) is queued again for later.
// ok is false when the run failed.
func cronRunFinished(status string, runAt, mark time.Time) (finished, ok bool) {
	switch status {
	case "done":
		return true, true
	case "failed":
		return true, false
	case "queued":
		if runAt.After(mark) {
			return true, true // the caller checks last_error for a failed run waiting for its retry
		}
	}
	return false, false
}

// --- db -----------------------------------------------------------------------------

func registerOpsDBCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("db", "Inspect the PostgreSQL database and apply migrations.")

	r.Add(cli.Command{
		Path:  "db migrate",
		Short: "Apply pending database migrations, or show their state.",
		Long: "Applies the migrations embedded in this binary, like `" + e.prog + " migrate`. It takes an advisory\n" +
			"lock, so it is safe while a server is running. With --status it only lists every migration and\n" +
			"whether it is applied.",
		Flags: []cli.Flag{
			{Name: "status", Bool: true, Usage: "List the migrations and their state instead of applying them."},
			{Name: "pending", Bool: true, Usage: "With --status: only the pending ones."},
		},
		Examples: []string{"db migrate", "db migrate --status --pending"},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			if !c.Bool("status") {
				applied, err := db.Migrate(c.Context, pool)
				if err != nil {
					return err
				}
				for _, v := range applied {
					c.Log("Applied %s", v)
				}
				if len(applied) == 0 {
					c.Success("The database is up to date.")
				} else {
					c.Success("Applied %d migration(s).", len(applied))
				}
				return nil
			}
			versions, err := db.Versions()
			if err != nil {
				return err
			}
			appliedAt := map[string]time.Time{}
			rows, err := pool.Query(c.Context, `SELECT version, applied_at FROM schema_migrations`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var v string
				var at time.Time
				if err := rows.Scan(&v, &at); err != nil {
					rows.Close()
					return err
				}
				appliedAt[v] = at
			}
			rows.Close()
			var out []cli.Row
			for _, row := range migrationStatusRows(versions, appliedAt) {
				if c.Bool("pending") && row["status"] != "pending" {
					continue
				}
				out = append(out, row)
			}
			return c.Print(out, cli.View{Fields: []string{"version", "status", "applied_at"}, ID: "version"})
		},
	})

	r.Add(cli.Command{
		Path:  "db size",
		Short: "Show the size of the database, or of each table.",
		Flags: []cli.Flag{
			{Name: "tables", Bool: true, Usage: "List every table with its size, estimated rows and backup category."},
			{Name: "size_format", Usage: "Print sizes as a number in this unit instead of human-readable.", Values: []string{"b", "kb", "mb", "gb", "tb"}},
		},
		Examples: []string{"db size", "db size --tables --format=csv", "db size --size_format=mb --field=Size"},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			unit := c.Flag("size_format")
			if !c.Bool("tables") {
				var name string
				var bytes int64
				if err := pool.QueryRow(c.Context, `SELECT current_database(), pg_database_size(current_database())`).Scan(&name, &bytes); err != nil {
					return err
				}
				return c.Print([]cli.Row{{"Name": name, "Size": formatDBSize(bytes, unit), "Bytes": bytes}}, cli.View{Fields: []string{"Name", "Size"}, ID: "Name"})
			}
			rows, err := pool.Query(c.Context, `SELECT c.relname, pg_total_relation_size(c.oid), greatest(c.reltuples, 0)::bigint
				FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
				WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY 2 DESC, 1`)
			if err != nil {
				return err
			}
			defer rows.Close()
			var out []cli.Row
			for rows.Next() {
				var name string
				var bytes, est int64
				if err := rows.Scan(&name, &bytes, &est); err != nil {
					return err
				}
				out = append(out, cli.Row{"Name": name, "Size": formatDBSize(bytes, unit), "Bytes": bytes, "Rows": est, "Category": bmgrCategoryOf(name)})
			}
			if err := rows.Err(); err != nil {
				return err
			}
			return c.Print(out, cli.View{Fields: []string{"Name", "Size", "Rows", "Category"}, ID: "Name"})
		},
	})

	r.Add(cli.Command{
		Path:          "db tables",
		Short:         "List the database tables.",
		Long:          "Lists the tables of the public schema, one per line (--format=list), or with the Backup Manager\ncategory each belongs to.",
		Flags:         []cli.Flag{{Name: "category", Usage: "Only tables of this backup category (content, media, users, settings, …)."}},
		Formats:       []string{"list"},
		DefaultFormat: "list",
		Examples:      []string{"db tables", "db tables --category=commerce", "db tables --format=table"},
		Run: func(c *cli.Ctx) error {
			cat := c.Flag("category")
			if cat != "" && bmgrCategoryByKey(cat) == nil {
				return cli.Usagef("unknown category '%s' (one of %s)", cat, strings.Join(bmgrCategoryKeys(), ", "))
			}
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			sc, err := bmgrLoadSchema(c.Context, pool)
			if err != nil {
				return err
			}
			var out []cli.Row
			for _, t := range sc.Tables {
				if cat != "" && bmgrCategoryOf(t) != cat {
					continue
				}
				out = append(out, cli.Row{"Name": t, "Category": bmgrCategoryOf(t)})
			}
			if c.Format == "list" && c.Field == "" {
				for _, r := range out {
					c.Println(r["Name"])
				}
				return nil
			}
			return c.Print(out, cli.View{Fields: []string{"Name", "Category"}, ID: "Name"})
		},
	})

	r.Add(cli.Command{
		Path:  "db check",
		Short: "Check the database connection and that every migration is applied.",
		Long:  "Connects, reports the PostgreSQL version and the pending migrations (like /readyz), and exits\nwith 1 when migrations are pending.",
		Examples: []string{
			"db check",
			"db check --format=json",
		},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			if err := pool.Ping(c.Context); err != nil {
				return cli.Errorf("database: %v", err)
			}
			var name, version, latest string
			if err := pool.QueryRow(c.Context, `SELECT current_database(), current_setting('server_version')`).Scan(&name, &version); err != nil {
				return err
			}
			// A database that was never migrated has no schema_migrations: latest stays empty.
			if err := pool.QueryRow(c.Context, `SELECT COALESCE(max(version), '') FROM schema_migrations`).Scan(&latest); err != nil && !db.IsUndefinedTable(err) {
				return err
			}
			pending, err := db.Pending(c.Context, pool)
			if err != nil {
				return err
			}
			if err := c.PrintItem(cli.Row{"database": name, "server_version": version, "latest_migration": latest, "pending_migrations": pending},
				[]string{"database", "server_version", "latest_migration", "pending_migrations"}); err != nil {
				return err
			}
			if pending > 0 {
				return cli.Errorf("%d migration(s) pending; run '%s db migrate'", pending, c.Prog)
			}
			if c.Format == "table" && c.Field == "" {
				c.Success("Database OK.")
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "db query",
		Short: "Run one read-only SQL statement and print its rows.",
		Long: "Runs a single statement inside a READ ONLY transaction with a statement timeout and rolls it back,\n" +
			"so it cannot change the database. Without <sql> (or with -) the statement is read from stdin.\n" +
			"There is no write mode: use the other commands, or psql, to change data.",
		Args: []cli.Arg{{Name: "sql", Usage: "The SQL statement.", Optional: true}},
		Flags: []cli.Flag{
			{Name: "timeout", Usage: "Statement timeout in seconds.", Default: "30"},
		},
		Examples: []string{
			`db query "SELECT id, login, email FROM users ORDER BY id"`,
			`db query "SELECT status, count(*) FROM jobs GROUP BY 1" --format=json`,
			`echo "SELECT key FROM settings" | db query --format=csv`,
		},
		Run: func(c *cli.Ctx) error {
			timeout, err := c.Int("timeout", 30)
			if err != nil {
				return err
			}
			if timeout <= 0 {
				return cli.Usagef("--timeout must be at least 1 second")
			}
			q := c.Arg(0)
			if q == "" || q == "-" {
				b, err := c.ReadInput("-", os.ReadFile)
				if err != nil {
					return err
				}
				q = string(b)
			}
			if err := dbQueryCheck(q); err != nil {
				return err
			}
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			tx, err := pool.BeginTx(c.Context, pgx.TxOptions{AccessMode: pgx.ReadOnly})
			if err != nil {
				return err
			}
			defer tx.Rollback(context.WithoutCancel(c.Context)) //nolint:errcheck // read-only: nothing to keep
			if _, err := tx.Exec(c.Context, `SELECT set_config('statement_timeout', $1, true)`, strconv.Itoa(timeout*1000)); err != nil {
				return err
			}
			rows, err := tx.Query(c.Context, q)
			if err != nil {
				return cli.Errorf("query: %v", err)
			}
			defer rows.Close()
			var names []string
			for _, fd := range rows.FieldDescriptions() {
				names = append(names, fd.Name)
			}
			names = uniqueColumnNames(names)
			var out []cli.Row
			for rows.Next() {
				vals, err := rows.Values()
				if err != nil {
					return err
				}
				row := cli.Row{}
				for i, v := range vals {
					if i < len(names) {
						row[names[i]] = sqlCLIValue(v)
					}
				}
				out = append(out, row)
			}
			if err := rows.Err(); err != nil {
				return cli.Errorf("query: %v", err)
			}
			return c.Print(out, cli.View{Fields: names})
		},
	})
}

// migrationStatusRows lists every embedded migration with its state.
func migrationStatusRows(versions []string, appliedAt map[string]time.Time) []cli.Row {
	out := make([]cli.Row, 0, len(versions))
	for _, v := range versions {
		row := cli.Row{"version": v, "status": "pending", "applied_at": nil}
		if at, ok := appliedAt[v]; ok {
			row["status"], row["applied_at"] = "applied", at
		}
		out = append(out, row)
	}
	return out
}

// formatDBSize prints a byte count human-readable (unit "") or as a number in unit (b, kb, mb, gb, tb).
func formatDBSize(bytes int64, unit string) string {
	div := map[string]float64{"b": 1, "kb": 1 << 10, "mb": 1 << 20, "gb": 1 << 30, "tb": 1 << 40}[unit]
	if div == 0 {
		return bmgrHuman(bytes)
	}
	if unit == "b" {
		return strconv.FormatInt(bytes, 10)
	}
	return strconv.FormatFloat(float64(bytes)/div, 'f', 2, 64)
}

func bmgrCategoryKeys() []string {
	out := make([]string, 0, len(bmgrCategories))
	for _, c := range bmgrCategories {
		out = append(out, c.Key)
	}
	return out
}

// dbQueryCheck refuses what `db query` cannot run: an empty statement, or transaction control,
// which would end the read-only transaction the statement runs in.
func dbQueryCheck(q string) error {
	w := strings.ToLower(firstSQLWord(q))
	switch w {
	case "":
		return cli.Usagef("no SQL statement given")
	case "begin", "start", "commit", "end", "rollback", "abort", "savepoint", "release", "prepare", "set", "reset", "discard", "listen", "notify":
		return cli.Usagef("'%s' cannot run in a read-only query", strings.ToUpper(w))
	}
	return nil
}

// firstSQLWord is the first keyword of a statement, after whitespace, comments and "(".
func firstSQLWord(q string) string {
	for {
		q = strings.TrimLeft(q, " \t\r\n(")
		switch {
		case strings.HasPrefix(q, "--"):
			i := strings.IndexByte(q, '\n')
			if i < 0 {
				return ""
			}
			q = q[i+1:]
		case strings.HasPrefix(q, "/*"):
			i := strings.Index(q, "*/")
			if i < 0 {
				return ""
			}
			q = q[i+2:]
		default:
			end := strings.IndexFunc(q, func(r rune) bool {
				return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_')
			})
			if end < 0 {
				return q
			}
			return q[:end]
		}
	}
}

// uniqueColumnNames makes repeated result column names unique (count, count_2 …): rows are maps.
func uniqueColumnNames(names []string) []string {
	seen := map[string]int{}
	out := make([]string, len(names))
	for i, n := range names {
		if n == "" || n == "?column?" {
			n = "column"
		}
		seen[n]++
		if seen[n] > 1 {
			base := n
			for {
				n = base + "_" + strconv.Itoa(seen[base])
				if seen[n] == 0 {
					break
				}
				seen[base]++
			}
			seen[n] = 1
		}
		out[i] = n
	}
	return out
}

// sqlCLIValue turns a pgx result value into something the formatters print well: bytea as \x…
// hex like psql, UUIDs as text, numeric and interval through their SQL text.
func sqlCLIValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return `\x` + hex.EncodeToString(x)
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16])
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = sqlCLIValue(e)
		}
		return out
	case driver.Valuer:
		if val, err := x.Value(); err == nil {
			return val
		}
	}
	return v
}

// --- core ---------------------------------------------------------------------------

func registerOpsCoreCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("core", "Install Pursae, and check its version and updates.")

	r.Add(cli.Command{
		Path:  "core version",
		Short: "Print the Pursae version of this binary.",
		Long: "Prints the version compiled into this binary (a running server may run another one). --extra\n" +
			"adds the Go version, the platform, the update state kept next to the binary and the newest\n" +
			"release the site knows of.",
		Flags:    []cli.Flag{{Name: "extra", Bool: true, Usage: "Show more details."}},
		Examples: []string{"core version", "core version --extra", "core version --extra --format=json"},
		Run: func(c *cli.Ctx) error {
			if !c.Bool("extra") && c.Format == "table" && c.Field == "" && len(c.Fields) == 0 {
				c.Println(Version)
				return nil
			}
			item := cli.Row{"version": Version, "go": runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
				"binary": "", "update_status": "", "previous_version": "", "previous_kept_until": nil, "latest_release": ""}
			if exe, err := coreExecutable(); err == nil {
				item["binary"] = exe
				if st := coreupdate.Read(exe); st != nil {
					item["update_status"] = st.Status
				}
				if from, until := coreupdate.Previous(exe, Version); from != "" {
					item["previous_version"] = from
					if !until.IsZero() {
						item["previous_kept_until"] = until
					}
				}
			}
			if set, err := e.Settings(c.Context); err == nil {
				item["latest_release"] = set.String(coreLatestOption)
			} else {
				c.Warning("%v", err)
			}
			return c.PrintItem(item, []string{"version", "go", "platform", "binary", "update_status", "previous_version", "previous_kept_until", "latest_release"})
		},
	})

	r.Add(cli.Command{
		Path:  "core check-update",
		Short: "Ask Pursae Manager for a newer release.",
		Long: "Reads the release index from Pursae Manager (network), stores the newest release for the admin's\n" +
			"update notice (running servers follow it live) and lists it when it is newer than this binary.\n\n" +
			"There is no `core update` command: update from Dashboard › Updates, or pull the new image /\n" +
			"replace the binary and restart the server.",
		Examples: []string{"core check-update", "core check-update --field=version"},
		Run: func(c *cli.Ctx) error {
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			if s.hubBase() == "" {
				return cli.Errorf("%v", errHubOff)
			}
			idx, err := s.hubFetchIndex(c.Context)
			if err != nil {
				return cli.Errorf("Pursae Manager: %v", err)
			}
			if idx.Core == nil || idx.Core.Latest == "" || !versionLess(Version, idx.Core.Latest) {
				if c.Format == "table" && c.Field == "" {
					c.Success("Pursae is at the latest version (%s).", Version)
					return nil
				}
				return c.Print(nil, cli.View{Fields: []string{"version", "update_type", "channel", "released", "package_url", "notes_url"}, ID: "version"})
			}
			row := cli.Row{"version": idx.Core.Latest, "update_type": coreUpdateType(Version, idx.Core.Latest), "channel": idx.Core.Channel,
				"released": idx.Core.Released, "package_url": idx.Core.DownloadURL, "notes_url": idx.Core.NotesURL}
			if err := c.Print([]cli.Row{row}, cli.View{Fields: []string{"version", "update_type", "channel", "released", "package_url"}, ID: "version"}); err != nil {
				return err
			}
			if c.Format == "table" && c.Field == "" {
				c.Log("To update: Dashboard › Updates in the admin, or pull the new image / replace the binary and restart the server.")
			}
			return nil
		},
	})
}

// coreUpdateType is wp-cli's update_type: major, minor or patch, by the first number that differs.
func coreUpdateType(from, to string) string {
	num := func(v string) []string {
		v = strings.TrimPrefix(strings.TrimSpace(v), "v")
		v, _, _ = strings.Cut(v, "-")
		v, _, _ = strings.Cut(v, "+")
		return strings.Split(v, ".")
	}
	a, b := num(from), num(to)
	names := []string{"major", "minor", "patch"}
	for i := range min(len(b), 3) {
		if i >= len(a) || a[i] != b[i] {
			return names[i]
		}
	}
	return "patch"
}

// opsDirOf is filepath.Dir of a cleaned path (the data directory next to the uploads).
func opsDirOf(p string) string { return filepath.Dir(filepath.Clean(p)) }
