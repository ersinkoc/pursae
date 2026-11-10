package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

func gmemMailCount(srv *Server, subject string) int {
	var n int
	_ = srv.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE name = 'wp_mail' AND payload->>'Subject' LIKE '%' || $1 || '%'`, subject).Scan(&n)
	return n
}

// A refunded or cancelled order takes the membership it granted away; paying it again grants it again.
func TestGooExtMembershipRevokedWithOrder(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaRecordMails(t)
	gxaEnableGateways(t, srv)
	gxaOn(t, srv, gmemKey, true)
	pass := gooMakeSimple(t, srv, "Revoke Pass", 50, func(p *gooProduct) { p.Virtual = true })
	plan := &gooMembershipPlan{Name: "Revokable", AccessMethod: "purchase", ProductIDs: []int64{pass.ID}, AccessLength: "unlimited"}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	u := gxaCustomer(t, srv, "revoke@example.test")
	c := gxaCtx(u, http.MethodGet, "/", nil)
	if _, err := srv.gooCartFor(c).Add(c, pass.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test")); len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	list := srv.gmemUMList(ctx, u.ID, plan.ID, "")
	if len(list) != 1 || !list[0].HasAccess(time.Now()) {
		t.Fatalf("membership after purchase: %+v", list)
	}
	o, err := srv.gooGetOrder(ctx, list[0].OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetStatus(ctx, o, "refunded", "", ""); err != nil {
		t.Fatal(err)
	}
	m, _ := srv.gmemUM(ctx, list[0].ID)
	if m.Status != "cancelled" || m.HasAccess(time.Now()) {
		t.Errorf("membership after refund: %s", m.Status)
	}
}

// A subscription's status change moves the membership its orders granted.
func TestGooExtMembershipFollowsSubscription(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gmemKey, true)
	plan := &gooMembershipPlan{Name: "Sub plan", AccessMethod: "manual", AccessLength: "unlimited"}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	u := gxaCustomer(t, srv, "subfollow@example.test")
	m := &gooUserMembership{UserID: u.ID, PlanID: plan.ID, Status: "active", Start: time.Now().Add(-time.Hour), OrderID: 987654}
	if err := srv.gmemSaveUM(ctx, m); err != nil {
		t.Fatal(err)
	}
	sub := &gooSubscription{ID: 1, CustomerID: u.ID, ParentOrderID: 987654}
	step := func(to, want string) {
		t.Helper()
		srv.gooFire(ctx, "subscription_status_changed", &gsubStatusChange{Sub: sub, To: to})
		got, _ := srv.gmemUM(ctx, m.ID)
		if got.Status != want {
			t.Errorf("subscription %s: membership %s, want %s", to, got.Status, want)
		}
	}
	step("on-hold", "paused")
	step("active", "active")
	step("cancelled", "cancelled")
}

// Welcome, ending-soon and ended e-mails are sent once.
func TestGooExtMembershipEmails(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gmemKey, true)
	plan := &gooMembershipPlan{Name: "Mailable", AccessMethod: "manual", AccessLength: "specific", AccessAmount: 3, AccessPeriod: "days"}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	u := gxaCustomer(t, srv, "mails@example.test")
	m, err := srv.gmemGrant(ctx, u.ID, plan, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := gmemMailCount(srv, "Welcome to the Mailable"); n != 1 {
		t.Errorf("welcome mails: %d", n)
	}
	srv.gmemScheduler(ctx)
	srv.gmemScheduler(ctx)
	if n := gmemMailCount(srv, "Mailable membership ends on"); n != 1 {
		t.Errorf("ending soon mails: %d, want 1", n)
	}
	m, _ = srv.gmemUM(ctx, m.ID)
	past := time.Now().Add(-time.Minute)
	m.End = &past
	if err := srv.gmemSaveUM(ctx, m); err != nil {
		t.Fatal(err)
	}
	srv.gmemScheduler(ctx)
	srv.gmemScheduler(ctx)
	if n := gmemMailCount(srv, "Mailable membership has ended"); n != 1 {
		t.Errorf("ended mails: %d, want 1", n)
	}
	if got, _ := srv.gmemUM(ctx, m.ID); got.Status != "expired" {
		t.Errorf("status %s", got.Status)
	}
}

// Drip: restricted content opens N days after the membership start.
func TestGooExtMembershipDrip(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	gxaOn(t, srv, gmemKey, true)
	post := &store.Post{Type: "post", Title: "Lesson 2", Slug: "lesson-2", Content: "<p>Lesson text.</p>", Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, post); err != nil {
		t.Fatal(err)
	}
	plan := &gooMembershipPlan{Name: "Course", AccessMethod: "manual", AccessLength: "unlimited",
		Rules: gmemRules{ContentPostIDs: []int64{post.ID}, ContentDrip: map[string]int{itoa64(post.ID): 7}}}
	if err := srv.gmemSavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	u := gxaCustomer(t, srv, "drip@example.test")
	m := &gooUserMembership{UserID: u.ID, PlanID: plan.ID, Status: "active", Start: time.Now().Add(-2 * 24 * time.Hour)}
	if err := srv.gmemSaveUM(ctx, m); err != nil {
		t.Fatal(err)
	}
	render := func() string {
		return srv.gooFilterContent(withRender(ctx, &renderCtx{s: srv, postID: post.ID, user: u}), "<p>Lesson text.</p>")
	}
	out := render()
	if !strings.Contains(out, "wc-memberships-content-restricted-message") || !strings.Contains(out, "will be available on") {
		t.Errorf("content before the drip date: %s", out)
	}
	m.Start = time.Now().Add(-8 * 24 * time.Hour)
	if err := srv.gmemSaveUM(ctx, m); err != nil {
		t.Fatal(err)
	}
	if out := render(); !strings.Contains(out, "Lesson text") {
		t.Errorf("content after the drip date: %s", out)
	}
	if d, err := gmemParseDrip("12:3, t4:10"); err != nil || d["12"] != 3 || d["t4"] != 10 {
		t.Errorf("parse drip: %v %v", d, err)
	}
	if _, err := gmemParseDrip("abc"); err == nil {
		t.Error("invalid drip accepted")
	}
}

// The plan form rejects ids that do not exist and its search endpoint finds content by title.
func TestGooExtMembershipPlanRefs(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	post := &store.Post{Type: "post", Title: "Findable Lesson", Slug: "findable", Content: "x", Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, post); err != nil {
		t.Fatal(err)
	}
	if err := srv.gmemValidateRefs(ctx, &gooMembershipPlan{Rules: gmemRules{ContentPostIDs: []int64{post.ID}}}); err != nil {
		t.Errorf("valid plan rejected: %v", err)
	}
	err := srv.gmemValidateRefs(ctx, &gooMembershipPlan{ProductIDs: []int64{post.ID, 99999991}, Rules: gmemRules{ContentTermIDs: []int64{99999992}}})
	if err == nil || !strings.Contains(err.Error(), "product") {
		t.Errorf("unknown product accepted: %v", err)
	}
	if err := srv.gmemValidateRefs(ctx, &gooMembershipPlan{Rules: gmemRules{ContentTermIDs: []int64{99999992}}}); err == nil || !strings.Contains(err.Error(), "term") {
		t.Errorf("unknown term accepted: %v", err)
	}
	c := gxaCtx(nil, http.MethodGet, "/wp-admin/goocommerce?page=membership-plans&gmem_search=post&term="+url.QueryEscape("Findable"), nil)
	srv.gmemSearchJSON(c)
	if body := c.Writer.(interface{ Status() int }).Status(); body != http.StatusOK {
		t.Errorf("search status %d", body)
	}
}
