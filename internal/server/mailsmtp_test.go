package server

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	gomail "wpgo/internal/mail"
	"wpgo/internal/mail/smtptest"
)

// msPluginLink finds the Activate/Deactivate link of WPGO Mail SMTP on the Plugins screen.
func msPluginLink(t *testing.T, c *http.Client, base, action string) string {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=wp-mail-smtp[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for Pursae SMTP", action)
	}
	return html.UnescapeString(m[1])
}

func msPage(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	code, body, _ := get(t, c, u)
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d\n%s", u, code, body)
	}
	if strings.Contains(body, "Template error") {
		t.Fatalf("GET %s: template error %v", u, lastRenderError.Load())
	}
	return body
}

// msPost submits a form of the page at u (its first _wpnonce) to action.
func msPost(t *testing.T, c *http.Client, base, page, action string, form url.Values) (int, string, string) {
	t.Helper()
	form.Set("_wpnonce", formNonce(t, msPage(t, c, base+page)))
	return post(t, c, base+action, form)
}

type hookRecorder struct {
	mu   sync.Mutex
	hits []string
}

func (h *hookRecorder) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.hits = append(h.hits, r.URL.Path+" "+string(b))
		h.mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/v3/mail/send"): // SendGrid
			w.Header().Set("X-Message-Id", "sg-id")
			w.WriteHeader(http.StatusAccepted)
		case strings.HasPrefix(r.URL.Path, "/emails"): // Resend
			_, _ = w.Write([]byte(`{"id":"resend-id"}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	})
}

func (h *hookRecorder) find(sub string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range h.hits {
		if strings.Contains(x, sub) {
			return x
		}
	}
	return ""
}

func TestMailSMTPPlugin(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()

	smtp := &smtptest.Server{User: "smtp-user", Pass: "s3cret-Pa55"}
	if err := smtp.Start(); err != nil {
		t.Fatal(err)
	}
	defer smtp.Close()
	rec := &hookRecorder{}
	api := httptest.NewServer(rec.handler())
	defer api.Close()
	msTestBase = api.URL
	defer func() { msTestBase = "" }()

	// Inactive: listed, but no menu, no screens, and wp_mail keeps writing .eml files.
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(body, "Pursae SMTP") {
		t.Fatal("plugin not listed")
	}
	if strings.Contains(msPage(t, c, base+"/wp-admin/"), "toplevel_page_wp-mail-smtp") {
		t.Fatal("menu shown while inactive")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/admin?page=wp-mail-smtp"); code != http.StatusForbidden {
		t.Fatalf("settings while inactive: %d", code)
	}
	if srv.mailer().Transport != nil {
		t.Fatal("transport attached while inactive")
	}
	if err := srv.mailer().Send(ctx, gomail.Message{To: []string{"x@example.test"}, Subject: "env", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(filepath.Join(srv.mailer().Dir, "*.eml")); len(files) != 1 {
		t.Fatalf("eml files: %d", len(files))
	}

	// Activation opens the Setup Wizard the first time.
	code, _, loc := get(t, c, base+msPluginLink(t, c, base, "activate"))
	if code != http.StatusFound || !strings.Contains(loc, "wp-mail-smtp-setup-wizard") {
		t.Fatalf("activate: %d %s", code, loc)
	}
	dash := msPage(t, c, base+"/wp-admin/")
	if !strings.Contains(dash, "toplevel_page_wp-mail-smtp") || !strings.Contains(dash, "wp_mail_smtp_reports_widget_lite") {
		t.Fatal("menu or dashboard widget missing while active")
	}

	t.Run("wizard", func(t *testing.T) {
		for _, step := range []string{"welcome", "mailer", "configure", "features", "check"} {
			msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step="+step)
		}
		_, _, loc := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=mailer", "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=mailer",
			url.Values{"mailer": {"smtp"}})
		if !strings.Contains(loc, "step=configure") {
			t.Fatalf("mailer step: %s", loc)
		}
		_, _, loc = msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=configure", "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=configure",
			url.Values{"from_email": {"news@example.test"}, "from_name": {"News"}, "smtp__host": {smtp.Host()}, "smtp__port": {smtp.Port()},
				"smtp__encryption": {"none"}, "smtp__auth": {"1"}, "smtp__auth__present": {"1"}, "smtp__autotls__present": {"1"},
				"smtp__user": {"smtp-user"}, "smtp__pass": {"s3cret-Pa55"}})
		if !strings.Contains(loc, "step=features") {
			t.Fatalf("configure step: %s", loc)
		}
		msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=features", "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=features",
			url.Values{"email_log": {"1"}, "email_log_content": {"1"}, "summary_report_email": {"1"}})
		_, body, _ := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=check", "/wp-admin/admin?page=wp-mail-smtp-setup-wizard&step=check",
			url.Values{"test_email": {"admin@example.test"}})
		if !strings.Contains(body, "successfully set up") {
			t.Fatalf("check step failed:\n%s", body)
		}
		if !srv.msLoad().WizardDone {
			t.Fatal("wizard not marked done")
		}
		msgs := smtp.Messages()
		if len(msgs) != 1 || msgs[0].User != "smtp-user" || msgs[0].From != "news@example.test" || !strings.Contains(msgs[0].Data, "From: \"News\" <news@example.test>") {
			t.Fatalf("smtp got %+v", msgs)
		}
	})

	t.Run("secrets are encrypted and never echoed", func(t *testing.T) {
		raw, _ := json.Marshal(srv.settings.Get(msOption))
		if strings.Contains(string(raw), "s3cret-Pa55") || !strings.Contains(string(raw), msSealPrefix) {
			t.Fatalf("stored option: %s", raw)
		}
		var stored string
		if err := getPool(t).QueryRow(ctx, `SELECT value::text FROM settings WHERE key = 'wp_mail_smtp'`).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stored, "s3cret-Pa55") {
			t.Fatal("password stored in plain text")
		}
		page := msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp")
		if strings.Contains(page, "s3cret-Pa55") || strings.Contains(page, msSealPrefix) {
			t.Fatal("password printed into the settings form")
		}
		if !strings.Contains(page, "(saved)") {
			t.Fatal("saved-secret placeholder missing")
		}
		// Saving the form with an empty password keeps it.
		msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp", "/wp-admin/admin?page=wp-mail-smtp&tab=general", url.Values{
			"from_email": {"news@example.test"}, "from_name": {"News"}, "return_path": {"1"}, "mailer": {"smtp"},
			"smtp__host": {smtp.Host()}, "smtp__port": {smtp.Port()}, "smtp__encryption": {"none"}, "smtp__auth": {"1"}, "smtp__auth__present": {"1"},
			"smtp__autotls__present": {"1"}, "smtp__user": {"smtp-user"}, "smtp__pass": {""}})
		if got := srv.msReveal(srv.msLoad().Mailers["smtp"]["pass"]); got != "s3cret-Pa55" {
			t.Fatalf("password after save: %q", got)
		}
	})

	t.Run("wp_mail job goes through the plugin and is logged", func(t *testing.T) {
		before := len(smtp.Messages())
		srv.sendMail(ctx, gomail.Message{To: []string{"reader@example.test"}, Subject: "[Toggle Site] Password Reset", Body: "reset link"})
		srv.runDueJobs(ctx, "test", srv.jobHandlers())
		if len(smtp.Messages()) != before+1 {
			t.Fatal("queued mail not delivered through SMTP")
		}
		logs, _, err := srv.msLogList(ctx, msLogFilter{Search: "Password Reset"}, 10, 0)
		if err != nil || len(logs) != 1 || logs[0].Status != "sent" || logs[0].Mailer != "smtp" || logs[0].Plain != "reset link" || !strings.HasPrefix(logs[0].Source, "Pursae: Reset") {
			t.Fatalf("log %+v %v", logs, err)
		}
		page := msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp-logs")
		if !strings.Contains(page, "[Toggle Site] Password Reset") {
			t.Fatal("log list misses the e-mail")
		}
		view := msPage(t, c, base+logs[0].ViewURL)
		if !strings.Contains(view, "reset link") || !strings.Contains(view, "Other SMTP") {
			t.Fatal("log view incomplete")
		}
	})

	t.Run("Cc and Bcc are delivered and kept on resend", func(t *testing.T) {
		before := len(smtp.Messages())
		if err := srv.mailer().Send(ctx, gomail.Message{To: []string{"to@example.test"}, Cc: []string{"cc@example.test"}, Bcc: []string{"bcc@example.test"},
			Subject: "carbon copies", Body: "x"}); err != nil {
			t.Fatal(err)
		}
		msgs := smtp.Messages()
		if len(msgs) != before+1 {
			t.Fatal("not delivered")
		}
		last := msgs[len(msgs)-1]
		if strings.Join(last.To, ",") != "to@example.test,cc@example.test,bcc@example.test" || strings.Contains(last.Data, "bcc@example.test") {
			t.Fatalf("RCPT %v\n%s", last.To, last.Data)
		}
		logs, _, err := srv.msLogList(ctx, msLogFilter{Search: "carbon copies"}, 10, 0)
		if err != nil || len(logs) != 1 {
			t.Fatalf("log %+v %v", logs, err)
		}
		if err := srv.msResend(ctx, logs[0], nil); err != nil {
			t.Fatal(err)
		}
		msgs = smtp.Messages()
		if last := msgs[len(msgs)-1]; strings.Join(last.To, ",") != "to@example.test,cc@example.test,bcc@example.test" {
			t.Fatalf("resend RCPT %v", last.To)
		}
	})

	t.Run("form From is kept unless forced", func(t *testing.T) {
		m := srv.mailer()
		m.From = "Visitor Form <form@example.test>"
		if err := m.Send(ctx, gomail.Message{To: []string{"a@example.test"}, Subject: "form", Body: "x"}); err != nil {
			t.Fatal(err)
		}
		last := smtp.Messages()[len(smtp.Messages())-1]
		if !strings.Contains(last.Data, "From: \"Visitor Form\" <form@example.test>") || last.From != "form@example.test" {
			t.Fatalf("not forced: from %q\n%s", last.From, last.Data)
		}
		raw := srv.msLoadRaw()
		raw.Mail.FromEmailForce, raw.Mail.FromNameForce = true, true
		_ = srv.msSave(ctx, raw)
		if err := m.Send(ctx, gomail.Message{To: []string{"a@example.test"}, Subject: "form2", Body: "x"}); err != nil {
			t.Fatal(err)
		}
		last = smtp.Messages()[len(smtp.Messages())-1]
		if !strings.Contains(last.Data, "From: \"News\" <news@example.test>") {
			t.Fatalf("forced:\n%s", last.Data)
		}
	})

	t.Run("failure: alerts, notice, retry updates the entry, backup connection", func(t *testing.T) {
		smtp.RejectTo = "bounce@example.test"
		raw := srv.msLoadRaw()
		raw.Alerts.Slack = msHook{Enabled: true, URLs: []string{api.URL + "/slack"}}
		raw.Alerts.Webhook = msHook{Enabled: true, URLs: []string{api.URL + "/hook"}}
		raw.Alerts.Twilio.Enabled, raw.Alerts.Twilio.SID, raw.Alerts.Twilio.Token = true, "AC1", srv.msSeal("tw-token")
		_ = srv.msSave(ctx, raw)
		msg := gomail.Message{To: []string{"bounce@example.test"}, Subject: "Will fail", Body: "x"}
		if err := srv.mailer().Send(ctx, msg); err == nil {
			t.Fatal("expected a failure")
		}
		if rec.find("/slack") == "" || !strings.Contains(rec.find("/hook"), `"event":"email_failed"`) || rec.find("/2010-04-01/Accounts/AC1/Messages.json") == "" {
			t.Fatalf("alerts not sent: %v", rec.hits)
		}
		if !strings.Contains(msPage(t, c, base+"/wp-admin/"), "your last email failed to send") {
			t.Fatal("delivery error notice missing")
		}
		_ = srv.mailer().Send(ctx, msg) // a retry of the same message
		logs, _, _ := srv.msLogList(ctx, msLogFilter{Search: "Will fail"}, 10, 0)
		if len(logs) != 1 || logs[0].Attempts != 2 || logs[0].Status != "failed" {
			t.Fatalf("retry log %+v", logs)
		}
		var events int
		_ = getPool(t).QueryRow(ctx, `SELECT count(*) FROM wpmailsmtp_debug_events WHERE event_type = 'error'`).Scan(&events)
		if events == 0 {
			t.Fatal("no debug event for the failure")
		}
		// Backup: an additional SendGrid connection takes over.
		_, _, loc := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp&tab=connections&edit=new", "/wp-admin/admin?page=wp-mail-smtp&tab=connections", url.Values{
			"conn_id": {"new"}, "conn_name": {"SendGrid backup"}, "from_email": {"backup@example.test"}, "mailer": {"sendgrid"}, "sendgrid__api_key": {"SG.KEY"}})
		if !strings.Contains(loc, "settings-updated=true") {
			t.Fatalf("save connection: %s", loc)
		}
		raw = srv.msLoadRaw()
		if len(raw.Connections) != 1 || raw.Connections[0].Opts["api_key"] == "SG.KEY" {
			t.Fatalf("connection %+v", raw.Connections)
		}
		raw.Backup = raw.Connections[0].ID
		_ = srv.msSave(ctx, raw)
		if err := srv.mailer().Send(ctx, msg); err != nil {
			t.Fatalf("backup did not take over: %v", err)
		}
		if hit := rec.find("/v3/mail/send"); !strings.Contains(hit, "bounce@example.test") {
			t.Fatalf("sendgrid not called: %q", hit)
		}
		logs, _, _ = srv.msLogList(ctx, msLogFilter{Search: "Will fail"}, 10, 0)
		if len(logs) != 1 || logs[0].Status != "sent" || logs[0].Mailer != "sendgrid" || logs[0].MessageID != "sg-id" {
			t.Fatalf("backup log %+v", logs[0])
		}
		if strings.Contains(msPage(t, c, base+"/wp-admin/"), "your last email failed to send") {
			t.Fatal("error notice still shown after a successful send")
		}
		smtp.RejectTo = ""
	})

	t.Run("smart routing", func(t *testing.T) {
		raw := srv.msLoadRaw()
		id := raw.Connections[0].ID
		_, _, loc := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp&tab=routing", "/wp-admin/admin?page=wp-mail-smtp&tab=routing", url.Values{
			"smart_routing_enabled": {"1"}, "routes[0][connection]": {id}, "routes[0][enabled]": {"1"},
			"routes[0][g][0][0][property]": {"subject"}, "routes[0][g][0][0][operator]": {"contains"}, "routes[0][g][0][0][value]": {"invoice"},
			"routes[0][g][0][1][property]": {"to"}, "routes[0][g][0][1][operator]": {"ends"}, "routes[0][g][0][1][value]": {"@example.test"}})
		if !strings.Contains(loc, "settings-updated") {
			t.Fatal(loc)
		}
		o := srv.msLoad()
		if !o.SmartRouting.Enabled || len(o.SmartRouting.Routes) != 1 || len(o.SmartRouting.Routes[0].Groups[0]) != 2 {
			t.Fatalf("routes %+v", o.SmartRouting)
		}
		before := len(smtp.Messages())
		_ = srv.mailer().Send(ctx, gomail.Message{To: []string{"c@example.test"}, Subject: "Your Invoice #1", Body: "x"})
		if len(smtp.Messages()) != before || !strings.Contains(rec.find("Your Invoice #1"), "/v3/mail/send") {
			t.Fatal("invoice not routed to SendGrid")
		}
		_ = srv.mailer().Send(ctx, gomail.Message{To: []string{"c@example.test"}, Subject: "Hello", Body: "x"})
		if len(smtp.Messages()) != before+1 {
			t.Fatal("other mail not on the primary connection")
		}
		msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp&tab=routing")
	})

	t.Run("email controls and do not send", func(t *testing.T) {
		form := url.Values{}
		for _, ec := range msEmailControls {
			if ec.Key != "password_reset" {
				form.Set("control_"+ec.Key, "1")
			}
		}
		msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp&tab=control", "/wp-admin/admin?page=wp-mail-smtp&tab=control", form)
		before := len(smtp.Messages())
		_ = srv.mailer().Send(ctx, gomail.Message{To: []string{"r@example.test"}, Subject: "[Toggle Site] Password Reset", Body: "x"})
		_ = srv.mailer().Send(ctx, gomail.Message{To: []string{"r@example.test"}, Subject: "[Toggle Site] Login Details", Body: "x"})
		if len(smtp.Messages()) != before+1 {
			t.Fatal("email control not applied")
		}
		msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp&tab=misc", "/wp-admin/admin?page=wp-mail-smtp&tab=misc", url.Values{"do_not_send": {"1"}})
		_ = srv.mailer().Send(ctx, gomail.Message{To: []string{"r@example.test"}, Subject: "blocked", Body: "x"})
		if len(smtp.Messages()) != before+1 {
			t.Fatal("Do Not Send did not block")
		}
		if !strings.Contains(msPage(t, c, base+"/wp-admin/"), "Do Not Send") {
			t.Fatal("Do Not Send notice missing")
		}
		msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp&tab=misc", "/wp-admin/admin?page=wp-mail-smtp&tab=misc", url.Values{})
	})

	t.Run("rate limiting queues the overflow", func(t *testing.T) {
		raw := srv.msLoadRaw()
		raw.RateLimit.Enabled, raw.RateLimit.Minute = true, 1
		_ = srv.msSave(ctx, raw)
		_, _ = getPool(t).Exec(ctx, `DELETE FROM wpmailsmtp_rate`)
		before := len(smtp.Messages())
		_ = srv.mailer().Send(ctx, gomail.Message{To: []string{"r@example.test"}, Subject: "first", Body: "x"})
		_ = srv.mailer().Send(ctx, gomail.Message{To: []string{"r@example.test"}, Subject: "second", Body: "x"})
		if len(smtp.Messages()) != before+1 {
			t.Fatal("rate limit not applied")
		}
		var runAt time.Time
		if err := getPool(t).QueryRow(ctx, `SELECT run_at FROM jobs WHERE name = 'wp_mail' AND payload->>'Subject' = 'second'`).Scan(&runAt); err != nil || time.Until(runAt) < 30*time.Second {
			t.Fatalf("overflow not queued for later: %v %v", runAt, err)
		}
		raw.RateLimit.Enabled = false
		_ = srv.msSave(ctx, raw)
	})

	t.Run("email test tool with debug output", func(t *testing.T) {
		_, body, _ := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-tools", "/wp-admin/admin?page=wp-mail-smtp-tools&tab=test",
			url.Values{"wp-mail-smtp[test_email]": {"t@example.test"}, "wp-mail-smtp[test_email_html]": {"1"}, "wp-mail-smtp[test_connection]": {"primary"}})
		if !strings.Contains(body, "Test HTML email was sent successfully") || !strings.Contains(body, "C: MAIL FROM:") || strings.Contains(body, "s3cret-Pa55") {
			t.Fatalf("test tool:\n%s", body)
		}
		smtp.RejectTo = "t@example.test"
		_, body, _ = msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-tools", "/wp-admin/admin?page=wp-mail-smtp-tools&tab=test",
			url.Values{"wp-mail-smtp[test_email]": {"t@example.test"}, "wp-mail-smtp[test_connection]": {"primary"}})
		smtp.RejectTo = ""
		if !strings.Contains(body, "There was a problem while sending the test email") || !strings.Contains(body, "550") {
			t.Fatalf("failed test not explained:\n%s", body)
		}
	})

	t.Run("resend, export, delete", func(t *testing.T) {
		logs, _, _ := srv.msLogList(ctx, msLogFilter{Search: "Will fail", Status: "sent"}, 10, 0)
		if len(logs) == 0 {
			t.Fatal("no entry to resend")
		}
		before := len(smtp.Messages())
		_, _, loc := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-logs", "/wp-admin/admin?page=wp-mail-smtp-logs",
			url.Values{"action": {"resend"}, "email_id": {itoa64(logs[0].ID)}, "recipients": {"other@example.test"}})
		if !strings.Contains(loc, "resent+successfully") || len(smtp.Messages()) != before+1 || smtp.Messages()[before].To[0] != "other@example.test" {
			t.Fatalf("resend: %s", loc)
		}
		form := url.Values{"format": {"csv"}, "include_content": {"1"}, "s": {"Password Reset"}}
		resent, _, _ := srv.msLogList(ctx, msLogFilter{Search: "other@example.test"}, 10, 0)
		if len(resent) != 1 || resent[0].ParentID == nil || *resent[0].ParentID != logs[0].ID {
			t.Fatalf("resent entry %+v", resent)
		}
		code, csvBody, _ := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-tools&tab=export", "/wp-admin/admin?page=wp-mail-smtp-tools&tab=export", form)
		if code != http.StatusOK || !strings.HasPrefix(csvBody, "ID,Date,Status,Subject") || !strings.Contains(csvBody, "reset link") {
			t.Fatalf("csv export %d:\n%s", code, csvBody)
		}
		form.Set("format", "eml")
		code, zipBody, _ := msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-tools&tab=export", "/wp-admin/admin?page=wp-mail-smtp-tools&tab=export", form)
		if code != http.StatusOK || !strings.HasPrefix(zipBody, "PK") {
			t.Fatalf("eml export %d", code)
		}
		_, _, loc = msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp-logs", "/wp-admin/admin?page=wp-mail-smtp-logs",
			url.Values{"action": {"delete"}, "email_id[]": {itoa64(logs[0].ID)}})
		if !strings.Contains(loc, "1+email+log+entries+deleted") {
			t.Fatalf("delete: %s", loc)
		}
	})

	t.Run("screens render", func(t *testing.T) {
		for _, p := range []string{"wp-mail-smtp", "wp-mail-smtp&tab=logs", "wp-mail-smtp&tab=alerts", "wp-mail-smtp&tab=connections",
			"wp-mail-smtp&tab=connections&edit=new", "wp-mail-smtp&tab=control", "wp-mail-smtp&tab=misc", "wp-mail-smtp-logs&status=failed",
			"wp-mail-smtp-reports", "wp-mail-smtp-reports&days=30", "wp-mail-smtp-tools&tab=export", "wp-mail-smtp-tools&tab=debug-events"} {
			msPage(t, c, base+"/wp-admin/admin?page="+p)
		}
		rep := msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp-reports")
		if !strings.Contains(rep, "<svg class=\"wpms-chart\"") || !strings.Contains(rep, "Will fail") {
			t.Fatal("reports incomplete")
		}
	})

	t.Run("environment constants lock fields", func(t *testing.T) {
		t.Setenv("WPMS_ON", "true")
		t.Setenv("WPMS_SMTP_HOST", "env-host.example.test")
		t.Setenv("WPMS_MAIL_FROM_NAME", "Env Name")
		o := srv.msLoad()
		if o.Mailers["smtp"]["host"] != "env-host.example.test" || o.Mail.FromName != "Env Name" || o.locked["smtp.host"] != "WPMS_SMTP_HOST" {
			t.Fatalf("env not applied: %+v", o.Mail)
		}
		page := msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp")
		if !strings.Contains(page, "set using a constant (WPMS_SMTP_HOST)") {
			t.Fatal("locked field not marked")
		}
		if srv.msLoadRaw().Mailers["smtp"]["host"] == "env-host.example.test" {
			t.Fatal("env value leaked into the stored options")
		}
	})

	t.Run("oauth connect flow", func(t *testing.T) {
		var mux http.ServeMux
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"ACCESS","refresh_token":"REFRESH","expires_in":3600}`))
		})
		mux.HandleFunc("/gmail/v1/users/me/profile", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"emailAddress":"owner@gmail.test"}`))
		})
		mux.HandleFunc("/gmail/v1/users/me/messages/send", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ACCESS" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":"gmail-id"}`))
		})
		g := httptest.NewServer(&mux)
		defer g.Close()
		msTestBase = g.URL
		defer func() { msTestBase = api.URL }()
		msPost(t, c, base, "/wp-admin/admin?page=wp-mail-smtp", "/wp-admin/admin?page=wp-mail-smtp&tab=general", url.Values{
			"from_email": {"news@example.test"}, "mailer": {"gmail"}, "gmail__client_id": {"CID"}, "gmail__client_secret": {"CSECRET"}})
		page := msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp")
		m := regexp.MustCompile(`href="(/wp-admin/admin\?page=wp-mail-smtp&amp;action=oauth_connect[^"]+)"`).FindStringSubmatch(page)
		if m == nil {
			t.Fatal("no connect button")
		}
		_, _, loc := get(t, c, base+html.UnescapeString(m[1]))
		u, _ := url.Parse(loc)
		if u.Query().Get("client_id") != "CID" || !strings.HasSuffix(u.Query().Get("redirect_uri"), "/wp-admin/admin?page=wp-mail-smtp&tab=auth") {
			t.Fatalf("authorize redirect %s", loc)
		}
		_, _, loc = get(t, c, base+"/wp-admin/admin?page=wp-mail-smtp&tab=auth&code=CODE&state="+url.QueryEscape(u.Query().Get("state")))
		if !strings.Contains(loc, "oauth=connected") {
			t.Fatalf("callback: %s", loc)
		}
		if !strings.Contains(msPage(t, c, base+"/wp-admin/admin?page=wp-mail-smtp"), "owner@gmail.test") {
			t.Fatal("connected account not shown")
		}
		if err := srv.mailer().Send(ctx, gomail.Message{To: []string{"g@example.test"}, Subject: "via gmail", Body: "x"}); err != nil {
			t.Fatal(err)
		}
		logs, _, _ := srv.msLogList(ctx, msLogFilter{Search: "via gmail"}, 1, 0)
		if len(logs) != 1 || logs[0].MessageID != "gmail-id" {
			t.Fatalf("gmail log %+v", logs)
		}
	})

	t.Run("daily job and deactivation", func(t *testing.T) {
		_, _ = getPool(t).Exec(ctx, `UPDATE wpmailsmtp_emails_log SET date_sent = now() - interval '40 days' WHERE subject = 'Hello'`)
		raw := srv.msLoadRaw()
		raw.Logs.Retention = 30
		_ = srv.msSave(ctx, raw)
		_ = srv.settings.Set(ctx, map[string]any{"wp_mail_smtp_summary_sent": time.Now().Add(-8 * 24 * time.Hour).Unix()})
		if err := srv.mailSMTPDaily(ctx); err != nil {
			t.Fatal(err)
		}
		if logs, _, _ := srv.msLogList(ctx, msLogFilter{Search: "Hello"}, 10, 0); len(logs) != 0 {
			t.Fatal("retention did not delete old entries")
		}
		if logs, _, _ := srv.msLogList(ctx, msLogFilter{Search: "Weekly Pursae SMTP Email Summary"}, 10, 0); len(logs) != 1 {
			t.Fatal("weekly summary not sent")
		}
		code, _, loc := get(t, c, base+msPluginLink(t, c, base, "deactivate"))
		if code != http.StatusFound || !strings.Contains(loc, "deactivate=true") {
			t.Fatalf("deactivate: %d %s", code, loc)
		}
		if srv.mailer().Transport != nil || strings.Contains(msPage(t, c, base+"/wp-admin/"), "toplevel_page_wp-mail-smtp") {
			t.Fatal("plugin still active")
		}
		var jobs int
		_ = getPool(t).QueryRow(ctx, `SELECT count(*) FROM jobs WHERE name = 'wp_mail_smtp_daily'`).Scan(&jobs)
		if jobs != 0 {
			t.Fatal("daily job left behind")
		}
		if srv.settings.Get(msOption) == nil {
			t.Fatal("settings removed without Uninstall")
		}
	})
}

func TestMailSMTPUnit(t *testing.T) {
	s := &Server{}
	s.cfg.Secret = "unit-secret"
	sealed := s.msSeal("api-key-1")
	if !strings.HasPrefix(sealed, msSealPrefix) || strings.Contains(sealed, "api-key-1") || s.msReveal(sealed) != "api-key-1" {
		t.Fatalf("seal round trip: %q", sealed)
	}
	if s.msSeal(sealed) != sealed || s.msReveal("plain") != "plain" {
		t.Fatal("seal is not idempotent / plain values must pass through")
	}
	other := &Server{}
	other.cfg.Secret = "other"
	if other.msReveal(sealed) != "" {
		t.Fatal("decrypted with a different key")
	}
	for subj, want := range map[string]string{"[Site] Please moderate: \"Hi\"": "comment_moderation", "[Site] Comment: \"Hi\"": "comment_published",
		"[My [odd] Site] Password Reset": "password_reset", "Hello": ""} {
		if got, _ := msClassify(subj); got != want {
			t.Errorf("classify %q = %q, want %q", subj, got, want)
		}
	}
	for _, tc := range []struct {
		op, val, in string
		want        bool
	}{{"contains", "inv", "Your Invoice", true}, {"not_contains", "inv", "Hello", true}, {"is", "a@b.c", "A@B.C", true}, {"is_not", "x", "x", false},
		{"starts", "[site]", "[Site] x", true}, {"ends", ".test", "a@b.test", true}, {"regex", `^\d+$`, "123", true}, {"not_regex", `^\d+$`, "12a", true}} {
		if got := msRuleMatch(msRule{Operator: tc.op, Value: tc.val}, tc.in); got != tc.want {
			t.Errorf("%s %q on %q = %v", tc.op, tc.val, tc.in, got)
		}
	}
	if msConstName("smtp", "encryption") != "WPMS_SSL" || msConstName("postmark", "server_api_token") != "WPMS_POSTMARK_SERVER_API_TOKEN" {
		t.Fatal("constant names")
	}
	if csvSafe("=cmd()") != "'=cmd()" || csvSafe("ok") != "ok" {
		t.Fatal("csvSafe")
	}
}
