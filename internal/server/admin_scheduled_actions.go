package server

import (
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// Tools › Scheduled Actions lists the core job queue (the jobs table: queued mail, cron events,
// importer runs, plugin events) with the last error of each job and Retry / Delete for the ones
// that failed for good. Pursae Commerce keeps its own list on its Status screen.

type scheduledAction struct {
	ID         int64
	Name       string
	Status     string
	Recurrence string
	Attempts   int
	LastError  string
	RunAt      string
	CanRetry   bool
	CanDelete  bool
}

var scheduledActionStatuses = []string{"failed", "queued", "running", "done"}

func (s *Server) scheduledActionsScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to manage scheduled actions on this site.")
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "scheduled-actions") {
			s.denyNonce(c)
			return
		}
		id, _ := strconv.ParseInt(c.PostForm("job"), 10, 64)
		var n int64
		switch c.PostForm("do") {
		case "retry":
			// Only a job that failed for good goes back on the queue, with a fresh attempt count.
			tag, _ := s.store.Pool.Exec(c, `UPDATE jobs SET status = 'queued', attempts = 0, last_error = '', run_at = now(), locked_by = NULL WHERE id = $1 AND status = 'failed'`, id)
			n = tag.RowsAffected()
		case "delete":
			tag, _ := s.store.Pool.Exec(c, `DELETE FROM jobs WHERE id = $1 AND status <> 'running'`, id)
			n = tag.RowsAffected()
		case "delete-failed":
			tag, _ := s.store.Pool.Exec(c, `DELETE FROM jobs WHERE status = 'failed'`)
			n = tag.RowsAffected()
		}
		q := url.Values{"done": {strconv.FormatInt(n, 10)}}
		if st := c.PostForm("status"); st != "" {
			q.Set("status", st)
		}
		c.Redirect(http.StatusSeeOther, "/wp-admin/scheduled-actions?"+q.Encode())
		return
	}
	status := c.Query("status")
	if status != "" && status != "failed" && status != "queued" && status != "running" && status != "done" {
		status = ""
	}
	counts := map[string]int{}
	total := 0
	if rows, err := s.store.Pool.Query(c, `SELECT status, count(*) FROM jobs GROUP BY status`); err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				counts[st] = n
				total += n
			}
		}
		rows.Close()
	}
	var list []scheduledAction
	rows, err := s.store.Pool.Query(c, `SELECT id, name, status, recurrence, attempts, last_error, run_at FROM jobs
		WHERE $1 = '' OR status = $1 ORDER BY (status = 'failed') DESC, run_at DESC, id DESC LIMIT 200`, status)
	if err == nil {
		defer rows.Close()
		loc := s.loc()
		for rows.Next() {
			var a scheduledAction
			var at time.Time
			if rows.Scan(&a.ID, &a.Name, &a.Status, &a.Recurrence, &a.Attempts, &a.LastError, &at) == nil {
				a.RunAt = at.In(loc).Format("2006-01-02 15:04:05")
				a.CanRetry = a.Status == "failed"
				a.CanDelete = a.Status != "running"
				list = append(list, a)
			}
		}
	}
	d := map[string]any{"Nonce": s.nonce(c, "scheduled-actions"), "Actions": list, "Status": status, "Total": total, "Counts": counts, "Statuses": scheduledActionStatuses}
	var notices []notice
	if v := c.Query("done"); v != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML("Done. " + template.HTMLEscapeString(v) + " scheduled action(s) changed."), Dismissible: true})
	}
	s.adminPage(c, "scheduled-actions", "Scheduled Actions", "tools_page_scheduled-actions", "admin/scheduled-actions", d, notices...)
}
