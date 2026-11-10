package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSiteHealthTestsShareDashboardCount(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	tests := srv.healthTests(ctx)
	h := srv.siteHealthSummary(ctx)
	if h.Good+h.Recommended+h.Critical != len(tests) {
		t.Fatalf("dashboard counts %d tests, screen has %d", h.Good+h.Recommended+h.Critical, len(tests))
	}
	// No SMTP configured: a critical test (mail only lands in .eml files).
	found := false
	for _, x := range tests {
		if strings.Contains(x.Label, "Email delivery is not configured") && x.Status == "critical" {
			found = true
		}
	}
	if !found {
		t.Error("missing the unconfigured e-mail test")
	}
	// A newer core release announced by the Manager turns the version test into a recommendation.
	_ = srv.settings.Set(ctx, map[string]any{coreLatestOption: "99.0.0"})
	if srv.hubBase() != "" {
		for _, x := range srv.healthTests(ctx) {
			if strings.Contains(x.Label, "new version of Pursae") && x.Status != "recommended" {
				t.Errorf("update test status %q", x.Status)
			}
		}
	}
	// A failed job is reported.
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO jobs(name, status, run_at) VALUES ('x_failed', 'failed', now())`)
	ok := false
	for _, x := range srv.healthTests(ctx) {
		if x.Label == "Background jobs have failed" {
			ok = true
		}
	}
	if !ok {
		t.Error("failed job not reported")
	}
	if got := srv.builtinPluginSummary(); !strings.Contains(got, "Pursae Cache") || !strings.Contains(got, "(") {
		t.Errorf("built-in summary %q", got)
	}
}

func TestPrivacyDeleteOldExports(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	dir := srv.privacyExportsDir()
	_ = os.MkdirAll(dir, 0o755)
	oldF, newF := filepath.Join(dir, "old.zip"), filepath.Join(dir, "new.zip")
	_ = os.WriteFile(oldF, []byte("x"), 0o644)
	_ = os.WriteFile(newF, []byte("x"), 0o644)
	past := time.Now().Add(-4 * 24 * time.Hour)
	_ = os.Chtimes(oldF, past, past)
	if err := srv.privacyDeleteOldExports(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldF); err == nil {
		t.Error("old export kept")
	}
	if _, err := os.Stat(newF); err != nil {
		t.Error("fresh export removed")
	}
	if _, ok := srv.jobHandlers()["wp_privacy_delete_old_export_files"]; !ok {
		t.Error("no job handler")
	}
}

func TestPersonalDataRegistryCommerceAndMailLog(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	_ = srv.settings.Set(ctx, map[string]any{gooSetting: true})
	email := "Buyer@Example.test"
	_, err := srv.store.Pool.Exec(ctx, `INSERT INTO goo_orders(status, billing_email, billing, customer_ip) VALUES
		('completed', 'buyer@example.test', '{"first_name":"Ada"}', '1.2.3.4'), ('processing', 'buyer@example.test', '{"first_name":"Ada"}', '1.2.3.4')`)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO wpmailsmtp_emails_log(subject, to_addrs) VALUES ('Hi', ARRAY['buyer@example.test'])`)

	groups := srv.registeredPersonalData(ctx, email)
	ids := map[string]int{}
	for _, g := range groups {
		ids[g.ID] = len(g.Items)
	}
	if ids["woocommerce-customer-orders"] != 2 || ids["email-log"] != 1 {
		t.Fatalf("export groups %v", ids)
	}

	// Default settings: orders are retained and the request says so.
	res := srv.erasePersonalDataRegistered(ctx, email)
	if res.Retained != 2 || len(res.Messages) == 0 {
		t.Fatalf("default erase: %+v", res)
	}
	_ = srv.settings.Set(ctx, map[string]any{gooOptPrefix + "erasure_request_removes_order_data": true})
	res = srv.erasePersonalDataRegistered(ctx, email)
	var anon, left int
	srv.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE billing_email = 'deleted@site.invalid' AND billing = '{}'), count(*) FILTER (WHERE billing_email = 'buyer@example.test') FROM goo_orders`).Scan(&anon, &left)
	if anon != 1 || left != 1 || res.Retained != 1 {
		t.Fatalf("anonymized %d, kept %d, %+v", anon, left, res)
	}
	var logs int
	srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM wpmailsmtp_emails_log`).Scan(&logs)
	if logs != 0 {
		t.Errorf("mail log rows left: %d", logs)
	}
}

func TestPrivacyRequestsConcurrentCreate(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gc, _ := gin.CreateTestContext(httptest.NewRecorder())
			gc.Request = httptest.NewRequest("GET", "/", nil)
			_, _ = srv.createUserRequest(gc, "export_personal_data", "u"+string(rune('a'+i))+"@example.test", "request-pending")
		}(i)
	}
	wg.Wait()
	if n := len(srv.loadPersonalRequests()); n != 8 {
		t.Fatalf("%d requests survived, want 8", n)
	}
}
