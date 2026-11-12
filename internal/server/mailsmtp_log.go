package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gomail "wpgo/internal/mail"
)

// Email log, debug events, alerts, reports and the daily housekeeping of WPGO Mail SMTP.

func msHash(msg gomail.Message) string {
	h := sha256.Sum256([]byte(strings.Join(msg.To, ",") + "\x00" + msg.Subject + "\x00" + msg.Body + "\x00" + msg.HTML))
	return hex.EncodeToString(h[:])
}

var msSeenFailures sync.Map // hash -> time.Time, alert de-duplication when the log is off

// msSeenSwept is when msSeenFailures was last cleared of entries older than a day; without it
// every distinct failed message (contact form spam while SMTP is down) would stay forever.
var msSeenSwept atomic.Int64

func msSweepSeenFailures() {
	now := time.Now()
	last := msSeenSwept.Load()
	if now.UnixNano()-last < int64(time.Hour) || !msSeenSwept.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	msSeenFailures.Range(func(k, v any) bool {
		if t, ok := v.(time.Time); !ok || now.Sub(t) >= 24*time.Hour {
			msSeenFailures.Delete(k)
		}
		return true
	})
}

// msLogResult records a delivery in the email log. The job runner retries a failed e-mail; a retry
// updates the entry of the earlier attempt instead of adding a new one. It reports whether this
// is the first failure of the message (alerts fire once per message).
func (s *Server) msLogResult(ctx context.Context, o *msOptions, msg gomail.Message, res msResult, source string) bool {
	hash := msHash(msg)
	status, errText := "sent", ""
	if res.Err != nil {
		status, errText = "failed", res.Err.Error()
	}
	mailer, conn := "", ""
	if res.Conn != nil {
		mailer, conn = res.Conn.Mailer, res.Conn.Name
	}
	if !o.Logs.Enabled {
		if res.Err == nil {
			msSeenFailures.Delete(hash)
			return false
		}
		if t, ok := msSeenFailures.Load(hash); ok && time.Since(t.(time.Time)) < 24*time.Hour {
			return false
		}
		msSeenFailures.Store(hash, time.Now())
		msSweepSeenFailures()
		return true
	}
	var id int64
	err := s.store.Pool.QueryRow(ctx, `UPDATE wpmailsmtp_emails_log SET status = $2, error_text = $3, mailer = $4, connection = $5, message_id = $6,
		attempts = attempts + 1, date_sent = now(), track_id = CASE WHEN $7 <> '' THEN $7 ELSE track_id END WHERE id = (SELECT id FROM wpmailsmtp_emails_log WHERE hash = $1 AND status = 'failed'
		AND date_sent > now() - interval '1 day' ORDER BY id DESC LIMIT 1) RETURNING id`, hash, status, errText, mailer, conn, res.MessageID, res.TrackID).Scan(&id)
	if err == nil {
		return false
	}
	headers := fmt.Sprintf("From: %s\nTo: %s\n", res.From, strings.Join(msg.To, ", "))
	if len(msg.Cc) > 0 {
		headers += "Cc: " + strings.Join(msg.Cc, ", ") + "\n"
	}
	if len(msg.Bcc) > 0 {
		headers += "Bcc: " + strings.Join(msg.Bcc, ", ") + "\n"
	}
	if msg.ReplyTo != "" {
		headers += "Reply-To: " + msg.ReplyTo + "\n"
	}
	headers += "Subject: " + msg.Subject + "\nX-Mailer: Pursae SMTP\n"
	if res.Envelope != "" {
		headers += "Return-Path: <" + res.Envelope + ">\n"
	}
	attJSON := ""
	if len(msg.Attachments) > 0 {
		list := make([]msLogAttachment, 0, len(msg.Attachments))
		for _, at := range msg.Attachments {
			list = append(list, msLogAttachment{Name: at.Name, Size: len(at.Data)})
		}
		if b, err := json.Marshal(list); err == nil {
			attJSON = string(b)
		}
	}
	plain, html := "", ""
	if o.Logs.LogContent {
		plain, html = msg.Body, msg.HTML
	}
	to := msg.To
	if to == nil {
		to = []string{}
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO wpmailsmtp_emails_log (message_id, subject, from_addr, to_addrs, reply_to, headers, content_plain,
		content_html, has_content, status, error_text, mailer, connection, initiator, hash, attachments, track_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		res.MessageID, msg.Subject, res.From, to, msg.ReplyTo, headers, plain, html, o.Logs.LogContent, status, errText, mailer, conn, source, hash, attJSON, res.TrackID); err != nil {
		slog.Error("wp-mail-smtp: email log", "err", err)
	}
	return res.Err != nil
}

// msDebugEvent records an entry under Tools › Debug Events.
func (s *Server) msDebugEvent(ctx context.Context, _ *msOptions, typ, content, initiator string) {
	if _, err := s.store.Pool.Exec(context.WithoutCancel(ctx), `INSERT INTO wpmailsmtp_debug_events (event_type, content, initiator) VALUES ($1, $2, $3)`,
		typ, content, initiator); err != nil {
		slog.Error("wp-mail-smtp: debug event", "err", err)
	}
}

// --- alerts ----------------------------------------------------------------------------------

// msLastAlertKey is the setting holding the unix time of the last threshold alert (shared by every instance).
const msLastAlertKey = "wp_mail_smtp_last_alert"

// msRecentFailures counts the failed deliveries of the last 30 minutes: from the email log, or from
// the debug events (one "Mailer:" error per failed message) while the log is off.
func (s *Server) msRecentFailures(ctx context.Context, o *msOptions) int {
	var n int
	q := `SELECT count(*) FROM wpmailsmtp_emails_log WHERE status = 'failed' AND date_sent > now() - interval '30 minutes'`
	if !o.Logs.Enabled {
		q = `SELECT count(*) FROM wpmailsmtp_debug_events WHERE event_type = 'error' AND content LIKE 'Mailer: %' AND created_at > now() - interval '30 minutes'`
	}
	_ = s.store.Pool.QueryRow(ctx, q).Scan(&n)
	return n
}

// msHealthTest is the Pursae SMTP row of Site Health: is e-mail going out. It reports nothing while
// the plugin is off. Unlike the admin notice it cannot be hidden by "Hide Email Delivery Errors".
func (s *Server) msHealthTest(ctx context.Context) (healthTest, bool) {
	if !s.mailSMTPOn() {
		return healthTest{}, false
	}
	o := s.msLoad()
	q := `SELECT count(*) FROM wpmailsmtp_emails_log WHERE status = 'failed' AND date_sent > now() - interval '24 hours'`
	if !o.Logs.Enabled {
		q = `SELECT count(*) FROM wpmailsmtp_debug_events WHERE event_type = 'error' AND content LIKE 'Mailer: %' AND created_at > now() - interval '24 hours'`
	}
	var failed int
	_ = s.store.Pool.QueryRow(ctx, q).Scan(&failed)
	action := template.HTML(`<a href="/wp-admin/admin?page=wp-mail-smtp-tools&amp;tab=test">Send a test email</a>`)
	t := healthTest{Badge: "Performance", Action: action}
	switch st, hasErr := s.msGetLastError(); {
	case o.General.DoNotSend:
		t.Label, t.Status = "Pursae SMTP is set not to send any emails", "critical"
		t.Description = "Do Not Send is on, so no email leaves the site."
	case hasErr:
		t.Label, t.Status = "The last email your site tried to send failed", "critical"
		t.Description = fmt.Sprintf("%s reported: %s (%d failed in the last 24 hours).", msMailerTitle(st.Mailer), st.Err, failed)
	case failed > 0:
		t.Label, t.Status = fmt.Sprintf("%d email(s) failed to send in the last 24 hours", failed), "recommended"
		t.Description = "Later emails went out, but check the Email Log and Debug Events to find out why some did not."
	default:
		t.Label, t.Status = "Your site can send email", "good"
		t.Description = "No delivery failures were recorded in the last 24 hours."
	}
	return t, true
}

func (s *Server) msAlert(ctx context.Context, o *msOptions, msg gomail.Message, res msResult) {
	a := &o.Alerts
	if !a.Email.Enabled && !a.Slack.Enabled && !a.Discord.Enabled && !a.Teams.Enabled && !a.Webhook.Enabled && !a.Twilio.Enabled {
		return
	}
	if a.Threshold > 1 {
		if s.msRecentFailures(ctx, o) < a.Threshold {
			return
		}
		if t := int64(s.settings.Int(msLastAlertKey)); t > 0 && time.Since(time.Unix(t, 0)) < 30*time.Minute {
			return
		}
	}
	_ = s.settings.Set(ctx, map[string]any{msLastAlertKey: time.Now().Unix()})
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	home := strings.TrimRight(s.settings.String("home"), "/")
	site := s.settings.String("blogname")
	mailer := msMailerTitle(res.Conn.Mailer)
	text := fmt.Sprintf("Pursae SMTP: your site %s failed to send an email.\n\nWebsite: %s\nTo: %s\nSubject: %s\nMailer: %s\nError: %v\n\nEmail log: %s/wp-admin/admin?page=wp-mail-smtp-logs",
		site, home, strings.Join(msg.To, ", "), msg.Subject, mailer, res.Err, home)
	post := func(u string, body []byte, ctype string) {
		if err := msWebhookURL(u); err != nil {
			slog.Warn("wp-mail-smtp: alert", "url", u, "err", err)
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", ctype)
		resp, err := msHTTP.Do(req)
		if err != nil {
			slog.Warn("wp-mail-smtp: alert", "url", u, "err", err)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
	}
	j := func(v any) []byte { b, _ := json.Marshal(v); return b }
	if a.Email.Enabled && len(a.Email.URLs) > 0 {
		m := s.mailer()
		m.Transport = nil // the plugin's mailer just failed: alerts go out with WordPress Go's own transport
		if err := m.Send(ctx, gomail.Message{To: a.Email.URLs, Subject: "[" + site + "] Pursae SMTP Alert: An email failed to send", Body: text}); err != nil {
			slog.Warn("wp-mail-smtp: email alert", "err", err)
		}
	}
	if a.Slack.Enabled {
		for _, u := range a.Slack.URLs {
			post(u, j(map[string]string{"text": text}), "application/json")
		}
	}
	if a.Discord.Enabled {
		for _, u := range a.Discord.URLs {
			post(u, j(map[string]string{"content": text}), "application/json")
		}
	}
	if a.Teams.Enabled {
		for _, u := range a.Teams.URLs {
			post(u, j(map[string]any{"@type": "MessageCard", "@context": "https://schema.org/extensions", "themeColor": "D63638",
				"summary": "An email failed to send", "title": "Pursae SMTP: an email failed to send", "text": strings.ReplaceAll(text, "\n", "<br>")}), "application/json")
		}
	}
	if a.Webhook.Enabled {
		for _, u := range a.Webhook.URLs {
			post(u, j(map[string]any{"event": "email_failed", "site_url": home, "site_name": site, "to": msg.To, "subject": msg.Subject,
				"mailer": res.Conn.Mailer, "connection": res.Conn.Name, "error": res.Err.Error(), "date": time.Now().UTC().Format(time.RFC3339)}), "application/json")
		}
	}
	if t := a.Twilio; t.Enabled && t.SID != "" {
		base := "https://api.twilio.com"
		if msTestBase != "" {
			base = msTestBase
		}
		f := url.Values{"To": {t.To}, "From": {t.From}, "Body": {truncateRunes(fmt.Sprintf("Pursae SMTP: %s failed to send an email (%s): %v", home, msg.Subject, res.Err), 320)}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/2010-04-01/Accounts/"+url.PathEscape(t.SID)+"/Messages.json", strings.NewReader(f.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetBasicAuth(t.SID, s.msReveal(t.Token))
			if resp, err := msHTTP.Do(req); err == nil {
				resp.Body.Close()
			} else {
				slog.Warn("wp-mail-smtp: twilio alert", "err", err)
			}
		}
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// --- log entries -----------------------------------------------------------------------------

// msLogAttachment is the name and size of a file a logged e-mail carried (the bytes are not stored).
type msLogAttachment struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

type msLogEntry struct {
	ID                                   int64
	MessageID, Subject, From, ReplyTo    string
	To                                   []string
	Headers, Plain, HTML                 string
	HasContent                           bool
	Status, Error, Mailer, Conn, Source  string
	Attempts                             int
	ParentID                             *int64
	Attachments                          []msLogAttachment
	Date                                 time.Time
	OpenCount, ClickCount                int
	FirstOpened                          *time.Time
	Delivery, DeliveryDetail             string // "", delivered, bounced, complained
	DeliveryAt                           *time.Time
	Tracked                              bool
	DateStr, MailerTitle, ToStr, ViewURL string
}

const msLogCols = `id, message_id, subject, from_addr, to_addrs, reply_to, headers, content_plain, content_html, has_content, status, error_text,
	mailer, connection, initiator, attempts, parent_id, date_sent, attachments,
	open_count, click_count, first_opened_at, delivery_status, delivery_detail, delivery_at, track_id <> ''`

func (s *Server) msScanLog(rows interface{ Scan(...any) error }) (*msLogEntry, error) {
	e := &msLogEntry{}
	var att string
	err := rows.Scan(&e.ID, &e.MessageID, &e.Subject, &e.From, &e.To, &e.ReplyTo, &e.Headers, &e.Plain, &e.HTML, &e.HasContent, &e.Status, &e.Error,
		&e.Mailer, &e.Conn, &e.Source, &e.Attempts, &e.ParentID, &e.Date, &att,
		&e.OpenCount, &e.ClickCount, &e.FirstOpened, &e.Delivery, &e.DeliveryDetail, &e.DeliveryAt, &e.Tracked)
	if err != nil {
		return nil, err
	}
	if att != "" {
		_ = json.Unmarshal([]byte(att), &e.Attachments)
	}
	e.DateStr = e.Date.In(s.loc()).Format("Jan 2, 2006 3:04 pm")
	e.MailerTitle = msMailerTitle(e.Mailer)
	e.ToStr = strings.Join(e.To, ", ")
	e.ViewURL = "/wp-admin/admin?page=wp-mail-smtp-logs&mode=view&email_id=" + strconv.FormatInt(e.ID, 10)
	return e, nil
}

func (s *Server) msLogGet(ctx context.Context, id int64) (*msLogEntry, error) {
	return s.msScanLog(s.store.Pool.QueryRow(ctx, `SELECT `+msLogCols+` FROM wpmailsmtp_emails_log WHERE id = $1`, id))
}

type msLogFilter struct {
	Status, Search, From, To string // From/To are dates (YYYY-MM-DD)
	IDs                      []int64
}

func (f msLogFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.Status == "sent" || f.Status == "failed" {
		add("status = ?", f.Status)
	}
	if f.Search != "" {
		add("(subject ILIKE ? OR array_to_string(to_addrs, ',') ILIKE ? OR from_addr ILIKE ? OR content_plain ILIKE ? OR content_html ILIKE ?)", "%"+f.Search+"%")
	}
	if t, err := time.Parse("2006-01-02", f.From); err == nil {
		add("date_sent >= ?", t)
	}
	if t, err := time.Parse("2006-01-02", f.To); err == nil {
		add("date_sent < ?", t.Add(24*time.Hour))
	}
	if len(f.IDs) > 0 {
		add("id = ANY(?)", f.IDs)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Server) msLogList(ctx context.Context, f msLogFilter, limit, offset int) ([]*msLogEntry, int, error) {
	where, args := f.where()
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM wpmailsmtp_emails_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + msLogCols + ` FROM wpmailsmtp_emails_log` + where + ` ORDER BY date_sent DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}
	rows, err := s.store.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*msLogEntry
	for rows.Next() {
		e, err := s.msScanLog(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// msResend sends a logged e-mail again (to its recipients or to others) through the current
// settings; the new entry points at the original.
func (s *Server) msResend(ctx context.Context, e *msLogEntry, to []string) error {
	if !e.HasContent {
		return fmt.Errorf("the content of this email was not logged (enable \"Log Email Content\"), so it can't be resent")
	}
	msg := gomail.Message{To: to, Subject: e.Subject, Body: e.Plain, HTML: e.HTML, ReplyTo: e.ReplyTo}
	if len(to) == 0 { // to the original recipients: the carbon copies too
		msg.To = e.To
		msg.Cc, msg.Bcc = msLogHeaderList(e.Headers, "Cc"), msLogHeaderList(e.Headers, "Bcc")
	}
	err := s.mailer().Send(ctx, msg)
	_, _ = s.store.Pool.Exec(ctx, `UPDATE wpmailsmtp_emails_log SET parent_id = $1 WHERE id = (SELECT max(id) FROM wpmailsmtp_emails_log WHERE hash = $2) AND id <> $1`,
		e.ID, msHash(msg))
	return err
}

// msLogHeaderList reads an address list header ("Cc", "Bcc") from a log entry's recorded headers.
func msLogHeaderList(headers, name string) []string {
	for _, l := range strings.Split(headers, "\n") {
		if v, ok := strings.CutPrefix(l, name+": "); ok {
			var out []string
			for _, a := range parseAddrList(strings.Split(v, ",")) {
				out = append(out, a.String())
			}
			return out
		}
	}
	return nil
}

// msEML renders a logged e-mail as an .eml file.
func msEML(e *msLogEntry) []byte {
	m := &gomail.Mailer{From: e.From}
	raw, err := m.Build(gomail.Message{To: e.To, Subject: e.Subject, Body: e.Plain, HTML: e.HTML, ReplyTo: e.ReplyTo})
	if err != nil {
		return []byte(e.Headers + "\n" + e.Plain)
	}
	return raw
}

// --- reports ---------------------------------------------------------------------------------

type msDay struct {
	Label        string
	Sent, Failed int
}

type msSubjectStat struct {
	Subject             string
	Total, Sent, Failed int
	Tracked, Opened     int // e-mails that carried tracking / were opened at least once
	Clicked             int
	OpenRate, ClickRate string
	LastStr, LastMailer string
}

type msStats struct {
	Days                []msDay
	Total, Sent, Failed int
	Max                 int
	Subjects            []msSubjectStat
	// Engagement and delivery over the period. Opened/Clicked count e-mails (not hits) with at
	// least one open/click; the rates are relative to Tracked, the e-mails that carried tracking.
	Tracked, Opened, Clicked        int
	Opens, Clicks                   int // raw hit counters
	Delivered, Bounced, Complained  int
	OpenRate, ClickRate, BounceRate string
}

// msRate formats part/whole as a percentage ("—" when there is nothing to divide by).
func msRate(part, whole int) string {
	if whole <= 0 {
		return "—"
	}
	return strconv.FormatFloat(float64(part)*100/float64(whole), 'f', 1, 64) + "%"
}

// msReport counts the log for the last days (today included).
func (s *Server) msReport(ctx context.Context, days int) (*msStats, error) {
	loc := s.loc()
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	st := &msStats{}
	idx := map[string]int{}
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		idx[d.Format("2006-01-02")] = i
		st.Days = append(st.Days, msDay{Label: d.Format("Jan 2")})
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT date_sent, status FROM wpmailsmtp_emails_log WHERE date_sent >= $1`, start)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t time.Time
		var status string
		if rows.Scan(&t, &status) != nil {
			continue
		}
		i, ok := idx[t.In(loc).Format("2006-01-02")]
		if !ok {
			continue
		}
		if status == "sent" {
			st.Days[i].Sent++
			st.Sent++
		} else {
			st.Days[i].Failed++
			st.Failed++
		}
	}
	rows.Close()
	st.Total = st.Sent + st.Failed
	for _, d := range st.Days {
		st.Max = max(st.Max, d.Sent+d.Failed)
	}
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE track_id <> ''), count(*) FILTER (WHERE open_count > 0),
		count(*) FILTER (WHERE click_count > 0), COALESCE(sum(open_count), 0), COALESCE(sum(click_count), 0),
		count(*) FILTER (WHERE delivery_status = 'delivered'), count(*) FILTER (WHERE delivery_status = 'bounced'),
		count(*) FILTER (WHERE delivery_status = 'complained') FROM wpmailsmtp_emails_log WHERE date_sent >= $1`, start).
		Scan(&st.Tracked, &st.Opened, &st.Clicked, &st.Opens, &st.Clicks, &st.Delivered, &st.Bounced, &st.Complained)
	st.OpenRate, st.ClickRate, st.BounceRate = msRate(st.Opened, st.Tracked), msRate(st.Clicked, st.Tracked), msRate(st.Bounced, st.Sent)
	rows, err = s.store.Pool.Query(ctx, `SELECT subject, count(*), count(*) FILTER (WHERE status = 'sent'), count(*) FILTER (WHERE status = 'failed'),
		count(*) FILTER (WHERE track_id <> ''), count(*) FILTER (WHERE open_count > 0), count(*) FILTER (WHERE click_count > 0),
		max(date_sent), (array_agg(mailer ORDER BY date_sent DESC))[1] FROM wpmailsmtp_emails_log WHERE date_sent >= $1
		GROUP BY subject ORDER BY count(*) DESC, subject LIMIT 50`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r msSubjectStat
		var last time.Time
		if rows.Scan(&r.Subject, &r.Total, &r.Sent, &r.Failed, &r.Tracked, &r.Opened, &r.Clicked, &last, &r.LastMailer) == nil {
			r.OpenRate, r.ClickRate = msRate(r.Opened, r.Tracked), msRate(r.Clicked, r.Tracked)
			r.LastStr = last.In(loc).Format("Jan 2, 3:04 pm")
			r.LastMailer = msMailerTitle(r.LastMailer)
			st.Subjects = append(st.Subjects, r)
		}
	}
	return st, rows.Err()
}

// --- daily job -------------------------------------------------------------------------------

// mailSMTPDaily is the wp_mail_smtp_daily event: log and debug-event retention, and the weekly
// Email Summary.
func (s *Server) mailSMTPDaily(ctx context.Context) error {
	if !s.mailSMTPOn() {
		return nil
	}
	o := s.msLoad()
	if o.Logs.Retention > 0 {
		if _, err := s.store.Pool.Exec(ctx, `DELETE FROM wpmailsmtp_emails_log WHERE date_sent < now() - make_interval(days => $1)`, o.Logs.Retention); err != nil {
			return err
		}
	}
	if o.General.DebugRetention > 0 {
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM wpmailsmtp_debug_events WHERE created_at < now() - make_interval(days => $1)`, o.General.DebugRetention)
	}
	msSweepSeenFailures()
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM wpmailsmtp_rate WHERE sent_at < now() - interval '31 days'`)
	if o.General.SummaryDisabled {
		return nil
	}
	last := int64(s.settings.Int("wp_mail_smtp_summary_sent"))
	if last == 0 { // the first summary goes out a week after activation
		return s.settings.Set(ctx, map[string]any{"wp_mail_smtp_summary_sent": time.Now().Unix()})
	}
	if time.Since(time.Unix(last, 0)) < 7*24*time.Hour-time.Hour {
		return nil
	}
	if err := s.msSendSummary(ctx); err != nil {
		return err
	}
	return s.settings.Set(ctx, map[string]any{"wp_mail_smtp_summary_sent": time.Now().Unix()})
}

// msSendSummary sends the weekly Email Summary to the site admin.
func (s *Server) msSendSummary(ctx context.Context) error {
	st, err := s.msReport(ctx, 7)
	if err != nil {
		return err
	}
	site := s.settings.String("blogname")
	home := strings.TrimRight(s.settings.String("home"), "/")
	var b strings.Builder
	fmt.Fprintf(&b, "Hi there,\n\nLet's see how your emails performed in the last week on %s.\n\nTotal emails: %d\nSent: %d\nFailed: %d\n\n", home, st.Total, st.Sent, st.Failed)
	if st.Tracked > 0 {
		fmt.Fprintf(&b, "Open rate: %s (%d of %d tracked emails)\nClick rate: %s\n\n", st.OpenRate, st.Opened, st.Tracked, st.ClickRate)
	}
	if st.Bounced+st.Complained > 0 {
		fmt.Fprintf(&b, "Bounced: %d\nSpam complaints: %d\n\n", st.Bounced, st.Complained)
	}
	if len(st.Subjects) > 0 {
		b.WriteString("Top subjects:\n")
		for i, r := range st.Subjects {
			if i == 10 {
				break
			}
			fmt.Fprintf(&b, "- %s: %d sent, %d failed\n", r.Subject, r.Sent, r.Failed)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "View the email reports: %s/wp-admin/admin?page=wp-mail-smtp-reports\n\nThis email was auto-generated and sent from Pursae SMTP. You can turn it off under Pursae SMTP › Settings › Misc.\n", home)
	return s.mailer().Send(ctx, gomail.Message{To: []string{s.settings.String("admin_email")}, Subject: "[" + site + "] Your Weekly Pursae SMTP Email Summary", Body: b.String()})
}
