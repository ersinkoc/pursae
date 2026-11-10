package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/mail"
)

// Pursae Contact Form entries (the part of Flamingo that CF7 users expect): every accepted
// submission is stored before its mail is queued, so a message survives a failed delivery.
// Forms opt out with `do_not_store: true`; demo forms never store.

const (
	gontactRetentionOption = "gontact_entries_retention_days" // 0 = keep until deleted
	gontactEntriesPage     = "wpcf7-entries"
	gontactFeedbackPerMin  = 30 // submissions per IP and minute
	gontactRefillPerMin    = 30 // refills (CAPTCHA images) per IP and minute
)

// gontactMinFillSeconds is the quickest a human fills a form; faster posts with a valid signed
// timestamp are spam. A variable for tests.
var gontactMinFillSeconds = 3

// gontactTimestampField is the signed render time of a form ("unix.hmac").
const gontactTimestampField = "_wpcf7_ts"

// gontactHoneypotField is a text input humans never see; bots fill it.
const gontactHoneypotField = "_wpcf7_website"

func (s *Server) gontactSignTimestamp(unix int64) string {
	m := hmac.New(sha256.New, []byte(s.settings.String(gontactSecretKey)+"|ts"))
	m.Write([]byte(strconv.FormatInt(unix, 10)))
	return strconv.FormatInt(unix, 10) + "." + hex.EncodeToString(m.Sum(nil))[:24]
}

// gontactCheckTimestamp reports whether a timestamp was posted, whether its signature is valid and its age.
func (s *Server) gontactCheckTimestamp(v string, now time.Time) (present, valid bool, age int64) {
	if v == "" {
		return false, false, 0
	}
	unix, _, ok := strings.Cut(v, ".")
	n, err := strconv.ParseInt(unix, 10, 64)
	if !ok || err != nil || !hmac.Equal([]byte(s.gontactSignTimestamp(n)), []byte(v)) {
		return true, false, 0
	}
	return true, true, now.Unix() - n
}

// gontactRateOK counts one request of kind for the client's address.
func gontactRateOK(c *gin.Context, kind string, limit int) bool {
	return gooikeAllow("gontact-"+kind+":"+gooikeTruncateIP(clientIP(c)), limit)
}

// storeEntry writes the submission and remembers its id; nothing is stored for demo forms or do_not_store.
func (sub *gfSubmission) storeEntry() {
	if sub.form.demoMode() || sub.form.isTrue("do_not_store") {
		return
	}
	posted, _ := json.Marshal(sub.flatPosted())
	mailStatus := "queued"
	if sub.skipMail {
		mailStatus = "skipped"
	}
	err := sub.s.store.Pool.QueryRow(sub.ctx, `INSERT INTO gontact_entries (form_id, form_title, posted, ip, user_agent, page_url, status, mail_status)
		VALUES ($1, $2, $3, $4, $5, $6, 'received', $7) RETURNING id`,
		sub.form.ID, sub.form.Title, posted, sub.remoteIP, truncate(sub.userAgent, 500), truncate(sub.pageURL, 2000), mailStatus).Scan(&sub.entryID)
	if err != nil {
		slog.Error("gontact form: store entry", "form", sub.form.ID, "err", err)
		sub.entryID = 0
	}
}

func truncate(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

func (s *Server) gontactEntryMailStatus(ctx context.Context, id int64, status string) {
	if id <= 0 {
		return
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE gontact_entries SET mail_status = $2 WHERE id = $1`, id, status); err != nil {
		slog.Warn("gontact form: entry mail status", "entry", id, "err", err)
	}
}

// gontactMailJob is the queued wp_mail payload of a stored entry: the message plus the entry the
// job reports its result to.
type gontactMailJob struct {
	mail.Message
	Entry int64 `json:"gontact_entry,omitempty"`
}

// gontactJobDelivered is called by the wp_mail handler with the job's result.
func (s *Server) gontactJobDelivered(ctx context.Context, p map[string]any, err error) {
	id, _ := p["gontact_entry"].(float64)
	if id <= 0 {
		return
	}
	if err != nil {
		slog.Warn("gontact form: mail delivery failed", "entry", int64(id), "err", err)
		s.gontactEntryMailStatus(ctx, int64(id), "failed")
		return
	}
	s.gontactEntryMailStatus(ctx, int64(id), "sent")
}

// gontactEntriesCleanup applies the retention setting.
func (s *Server) gontactEntriesCleanup(ctx context.Context) error {
	// Finished mails do not keep their (base64) attachments in the jobs table.
	if _, err := s.store.Pool.Exec(ctx, `UPDATE jobs SET payload = payload - 'Attachments' WHERE name = 'wp_mail' AND status = 'done' AND payload ? 'Attachments'`); err != nil {
		return err
	}
	days := s.settings.Int(gontactRetentionOption)
	if days <= 0 {
		return nil
	}
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM gontact_entries WHERE created_at < now() - make_interval(days => $1)`, days)
	return err
}

// gontactFailedMails counts entries whose mail failed in the last 7 days.
func (s *Server) gontactFailedMails(ctx context.Context) int {
	var n int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gontact_entries WHERE mail_status = 'failed' AND created_at > now() - interval '7 days'`).Scan(&n)
	return n
}

// Admin: Contact > Entries ----------------------------------------------------------

type gfEntryRow struct {
	ID                                                int64
	Form, Summary, Date, ISODate, IP, MailStatus, URL string
	ViewURL                                           string
}

type gfEntriesData struct {
	Rows                 []gfEntryRow
	Pagination           pagination
	Search, MailFilter   string
	FormFilter           int64
	Forms                []gfEntryFormOpt
	BulkNonce, ExportURL string
	Total, Failed        int
	Detail               *gfEntryDetail
	RetentionDays        int
	RetentionNonce       string
	CanManage            bool
	BackURL              string
	ColCount             int
}

type gfEntryFormOpt struct {
	ID       int64
	Title    string
	Selected bool
}

type gfEntryDetail struct {
	Form, Date, IP, UserAgent, PageURL, MailStatus string
	Fields                                         [][2]string
}

func (s *Server) gontactEntriesScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("publish_pages", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if c.Request.Method == http.MethodPost {
		s.gontactEntriesPost(c)
		return
	}
	formID, _ := strconv.ParseInt(c.Query("form"), 10, 64)
	mailFilter := c.Query("mail_status")
	search := strings.TrimSpace(c.Query("s"))
	where, args := gontactEntryWhere(formID, mailFilter, search)
	if c.Query("action") == "export" {
		if !s.checkNonce(c, "wpcf7-entries-export") {
			s.denyNonce(c)
			return
		}
		s.gontactEntriesCSV(c, where, args)
		return
	}
	d := gfEntriesData{Search: search, MailFilter: mailFilter, FormFilter: formID, CanManage: u.Can("manage_options", nil), ColCount: 6,
		BulkNonce: s.nonce(c, "wpcf7-entries-bulk"), RetentionNonce: s.nonce(c, "wpcf7-entries-retention"), RetentionDays: s.settings.Int(gontactRetentionOption),
		Failed: s.gontactFailedMails(c)}
	d.ExportURL = gontactAdminURL(gontactEntriesPage, url.Values{"action": {"export"}, "form": {c.Query("form")}, "mail_status": {mailFilter}, "s": {search},
		"_wpnonce": {s.nonce(c, "wpcf7-entries-export")}})
	if id, _ := strconv.ParseInt(c.Query("entry"), 10, 64); id > 0 {
		d.Detail = s.gontactEntryDetail(c, id)
		d.BackURL = gontactAdminURL(gontactEntriesPage, nil)
		if d.Detail == nil {
			c.String(http.StatusNotFound, "This entry does not exist.")
			return
		}
		s.adminPage(c, "admin?page="+gontactEntriesPage, "Entry", "contact_page_wpcf7-entries", "admin/gontact-entries", d)
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("paged", "1"))
	if page < 1 {
		page = 1
	}
	const perPage = 20
	_ = s.store.Pool.QueryRow(c, `SELECT count(*) FROM gontact_entries `+where, args...).Scan(&d.Total)
	base := url.URL{Path: "/wp-admin/admin", RawQuery: url.Values{"page": {gontactEntriesPage}, "form": {c.Query("form")}, "mail_status": {mailFilter}, "s": {search}}.Encode()}
	d.Pagination = newPagination(d.Total, perPage, page, base)
	q := fmt.Sprintf(`SELECT id, form_title, posted, ip, mail_status, created_at FROM gontact_entries %s ORDER BY created_at DESC, id DESC LIMIT %d OFFSET %d`, where, perPage, (page-1)*perPage)
	if rows, err := s.store.Pool.Query(c, q, args...); err == nil {
		for rows.Next() {
			var r gfEntryRow
			var raw []byte
			var at time.Time
			if rows.Scan(&r.ID, &r.Form, &raw, &r.IP, &r.MailStatus, &at) != nil {
				continue
			}
			var m map[string]string
			_ = json.Unmarshal(raw, &m)
			r.Summary = gontactEntrySummary(m)
			r.Date = at.In(s.loc()).Format("2006/01/02") + " at " + at.In(s.loc()).Format("3:04 pm")
			r.ISODate = at.Format("2006-01-02T15:04:05Z07:00")
			r.ViewURL = gontactAdminURL(gontactEntriesPage, url.Values{"entry": {itoa64(r.ID)}})
			d.Rows = append(d.Rows, r)
		}
		rows.Close()
	}
	forms, _, _ := s.gontactForms(c, "", "title", "asc", 1, -1)
	for _, f := range forms {
		d.Forms = append(d.Forms, gfEntryFormOpt{ID: f.ID, Title: f.Title, Selected: f.ID == formID})
	}
	var notes []notice
	switch c.Query("message") {
	case "deleted":
		notes = append(notes, notice{Type: "success", Message: "Entries deleted.", Dismissible: true})
	case "retention":
		notes = append(notes, notice{Type: "success", Message: "Settings saved.", Dismissible: true})
	}
	if d.Failed > 0 {
		notes = append(notes, notice{Type: "warning", Message: template.HTML(fmt.Sprintf("%d form submission(s) in the last 7 days could not be delivered by e-mail. Their data is kept below.", d.Failed))})
	}
	s.adminPage(c, "admin?page="+gontactEntriesPage, "Entries", "contact_page_wpcf7-entries", "admin/gontact-entries", d, notes...)
}

func gontactEntryWhere(formID int64, mailStatus, search string) (string, []any) {
	var conds []string
	var args []any
	if formID > 0 {
		args = append(args, formID)
		conds = append(conds, "form_id = $"+strconv.Itoa(len(args)))
	}
	if mailStatus == "queued" || mailStatus == "sent" || mailStatus == "failed" || mailStatus == "skipped" {
		args = append(args, mailStatus)
		conds = append(conds, "mail_status = $"+strconv.Itoa(len(args)))
	}
	if search != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(search)+"%")
		n := strconv.Itoa(len(args))
		conds = append(conds, "(posted::text ILIKE $"+n+" OR ip ILIKE $"+n+")")
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

func gfSortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func gontactEntrySummary(m map[string]string) string {
	var parts []string
	for _, k := range gfSortedKeys(m) {
		if m[k] != "" {
			parts = append(parts, k+": "+truncate(m[k], 60))
		}
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, " | ")
}

func (s *Server) gontactEntryDetail(c *gin.Context, id int64) *gfEntryDetail {
	var raw []byte
	var at time.Time
	d := &gfEntryDetail{}
	err := s.store.Pool.QueryRow(c, `SELECT form_title, posted, ip, user_agent, page_url, mail_status, created_at FROM gontact_entries WHERE id = $1`, id).
		Scan(&d.Form, &raw, &d.IP, &d.UserAgent, &d.PageURL, &d.MailStatus, &at)
	if err != nil {
		return nil
	}
	d.Date = at.In(s.loc()).Format("2006/01/02 3:04 pm")
	var m map[string]string
	_ = json.Unmarshal(raw, &m)
	for _, k := range gfSortedKeys(m) {
		d.Fields = append(d.Fields, [2]string{k, m[k]})
	}
	return d
}

func (s *Server) gontactEntriesPost(c *gin.Context) {
	switch c.PostForm("action") {
	case "retention":
		if !currentUser(c).Can("manage_options", nil) || !s.checkNonce(c, "wpcf7-entries-retention") {
			s.denyNonce(c)
			return
		}
		days, _ := strconv.Atoi(c.PostForm("retention_days"))
		if days < 0 {
			days = 0
		}
		_ = s.settings.Set(c, map[string]any{gontactRetentionOption: days})
		c.Redirect(http.StatusFound, gontactAdminURL(gontactEntriesPage, url.Values{"message": {"retention"}}))
	case "delete":
		if !s.checkNonce(c, "wpcf7-entries-bulk") {
			s.denyNonce(c)
			return
		}
		var ids []int64
		for _, v := range c.PostFormArray("entry[]") {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				ids = append(ids, n)
			}
		}
		if len(ids) > 0 {
			_, _ = s.store.Pool.Exec(c, `DELETE FROM gontact_entries WHERE id = ANY($1)`, ids)
		}
		c.Redirect(http.StatusFound, gontactAdminURL(gontactEntriesPage, url.Values{"message": {"deleted"}}))
	default:
		c.Redirect(http.StatusFound, gontactAdminURL(gontactEntriesPage, nil))
	}
}

// gontactEntriesCSV streams the filtered entries; cells starting with = + - @ are prefixed so a
// spreadsheet does not run a visitor's text as a formula.
func (s *Server) gontactEntriesCSV(c *gin.Context, where string, args []any) {
	rows, err := s.store.Pool.Query(c, `SELECT id, form_title, posted, ip, mail_status, created_at FROM gontact_entries `+where+` ORDER BY created_at, id LIMIT 50000`, args...)
	if err != nil {
		c.String(http.StatusInternalServerError, "Could not read the entries.")
		return
	}
	defer rows.Close()
	type rec struct {
		id         int64
		form, ip   string
		mailStatus string
		at         time.Time
		posted     map[string]string
	}
	var all []rec
	cols := map[string]string{}
	for rows.Next() {
		var r rec
		var raw []byte
		if rows.Scan(&r.id, &r.form, &raw, &r.ip, &r.mailStatus, &r.at) != nil {
			continue
		}
		_ = json.Unmarshal(raw, &r.posted)
		for k := range r.posted {
			cols[k] = ""
		}
		all = append(all, r)
	}
	names := gfSortedKeys(cols)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="contact-form-entries.csv"`)
	w := csv.NewWriter(c.Writer)
	safe := func(v string) string {
		if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
			return "'" + v
		}
		return v
	}
	_ = w.Write(append([]string{"ID", "Form", "Date", "IP", "Mail status"}, names...))
	for _, r := range all {
		line := []string{itoa64(r.id), safe(r.form), r.at.UTC().Format(time.RFC3339), r.ip, r.mailStatus}
		for _, n := range names {
			line = append(line, safe(r.posted[n]))
		}
		_ = w.Write(line)
	}
	w.Flush()
}
