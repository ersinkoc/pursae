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

func TestGooExtMemberships(t *testing.T) {
	base, cl, srv := gooSite(t)
	ctx := context.Background()
	gxaRecordMails(t)
	gxaEnableGateways(t, srv)

	pass := gooMakeSimple(t, srv, "Gold Pass", 50, func(p *gooProduct) { p.Virtual = true })
	widget := gooMakeSimple(t, srv, "Widget", 20, func(p *gooProduct) { p.Virtual = true })
	secret := gooMakeSimple(t, srv, "Secret Blend", 15, func(p *gooProduct) { p.Virtual = true })
	news := &store.Post{Type: "post", Title: "Members news", Slug: "members-news", Content: "<p>The secret recipe.</p>", Excerpt: "A teaser.", Status: "publish", AuthorID: 1, Meta: map[string]any{}}
	if err := srv.store.InsertPost(ctx, news); err != nil {
		t.Fatal(err)
	}
	gold := &gooMembershipPlan{Name: "Gold", AccessMethod: "purchase", ProductIDs: []int64{pass.ID}, AccessLength: "specific", AccessAmount: 1, AccessPeriod: "years",
		Rules: gmemRules{ContentPostIDs: []int64{news.ID}, ProductIDs: []int64{secret.ID}, ProductRestrict: "purchase",
			Discounts: []gmemDiscount{{Target: "product", ID: widget.ID, Type: "percent", Amount: 10}}}}
	if err := srv.gmemSavePlan(ctx, gold); err != nil {
		t.Fatal(err)
	}
	if gold.Slug != "gold" {
		t.Errorf("plan slug %q", gold.Slug)
	}
	render := func(u *store.User, content string) string {
		rc := &renderCtx{s: srv, postID: news.ID, user: u}
		return srv.gooFilterContent(withRender(ctx, rc), content)
	}
	u := gxaCustomer(t, srv, "member@example.test")
	c := gxaCtx(u, http.MethodGet, "/", nil)

	// Off: nothing is restricted or discounted.
	if out := render(nil, "<p>The secret recipe.</p>"); !strings.Contains(out, "secret recipe") {
		t.Errorf("content restricted while off: %s", out)
	}
	if _, err := srv.gooCartFor(c).Add(c, secret.ID, 1, 0, nil, nil, nil); err != nil {
		t.Errorf("member-only product while off: %v", err)
	}
	srv.gooCartFor(c).Empty()

	gxaOn(t, srv, gmemKey, true)
	// Non-members: the restriction message (with the excerpt) instead of the content.
	out := render(u, "<p>The secret recipe.</p>")
	if strings.Contains(out, "secret recipe") || !strings.Contains(out, "wc-memberships-content-restricted-message") || !strings.Contains(out, "Gold Pass") || !strings.Contains(out, "A teaser.") {
		t.Errorf("restricted content: %s", out)
	}
	if out := render(nil, "x"); !strings.Contains(out, "log in") {
		t.Errorf("visitor message lacks the login link: %s", out)
	}
	if _, err := srv.gooCartFor(c).Add(c, secret.ID, 1, 0, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "can only be purchased by members") {
		t.Errorf("non-member bought a member-only product: %v", err)
	}
	if h := string(srv.gooHTML(c, "single_after_add_to_cart", secret)); !strings.Contains(h, "wc-memberships-product-purchasing-restricted-message") {
		t.Errorf("product notice: %s", h)
	}
	sc := "[goocommerce_members_only plans=\"gold\"]VIP[/goocommerce_members_only][goocommerce_non_members]Join us[/goocommerce_non_members]"
	if out := srv.gooFilterContent(withRender(ctx, &renderCtx{s: srv, user: u}), sc); out != "Join us" {
		t.Errorf("non-member shortcodes: %q", out)
	}

	// Buying the granting product makes the customer a member for a year.
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(c, pass.ID, 1, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, errs := srv.gooProcessCheckout(c, gxaCheckoutForm(u.Email, "goo_test")); len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	list := srv.gmemUMList(ctx, u.ID, gold.ID, "")
	if len(list) != 1 || list[0].Status != "active" || list[0].End == nil || list[0].OrderID == 0 {
		t.Fatalf("membership after purchase: %+v", list)
	}
	if d := list[0].End.Sub(time.Now()); d < 364*24*time.Hour || d > 367*24*time.Hour {
		t.Errorf("membership length %v", d)
	}

	// Members: the content, the member-only product and the discount.
	if out := render(u, "<p>The secret recipe.</p>"); !strings.Contains(out, "secret recipe") {
		t.Errorf("member content: %s", out)
	}
	if out := srv.gooFilterContent(withRender(ctx, &renderCtx{s: srv, user: u}), sc+"[goocommerce_member_discount]"); !strings.HasPrefix(out, "VIP") || strings.Contains(out, "Join us") || !strings.Contains(out, "Widget") {
		t.Errorf("member shortcodes: %q", out)
	}
	c2 := gxaCtx(u, http.MethodGet, "/", nil)
	cart2 := srv.gooCartFor(c2)
	if _, err := cart2.Add(c2, secret.ID, 1, 0, nil, nil, nil); err != nil {
		t.Errorf("member can not buy the member-only product: %v", err)
	}
	if _, err := cart2.Add(c2, widget.ID, 2, 0, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "member cart", cart2.Subtotal, 15+2*18)
	gooAlmost(t, "member catalog price", srv.gooDisplayPrice(c2.Request.Context(), widget), 18)
	gooAlmost(t, "visitor catalog price", srv.gooDisplayPrice(ctx, widget), 20)
	if h := string(srv.gooFilterPriceHTML(c2.Request.Context(), widget, srv.gooPrice(20))); !strings.Contains(h, "<del") || !strings.Contains(h, "18.00") {
		t.Errorf("member price html: %s", h)
	}
	// Sale items keep their price unless discounts apply to them.
	widget.SalePrice = gooFloatPtr(19)
	_ = srv.gooSaveProduct(ctx, widget, 1)
	gooAlmost(t, "sale item", srv.gooDisplayPrice(c2.Request.Context(), widget), 19)
	gxaSetExt(t, srv, gmemKey, map[string]any{"apply_discounts_to_sale_items": true})
	gooAlmost(t, "sale item with inherited discount", srv.gooDisplayPrice(c2.Request.Context(), widget), 17.1)

	// Members area.
	if h := string(gmemAccountRender(srv, c2, "")); !strings.Contains(h, "Gold") || !strings.Contains(h, "my_account_memberships") {
		t.Errorf("members area list: %.300s", h)
	}
	if h := string(gmemAccountRender(srv, c2, itoa64(list[0].ID))); !strings.Contains(h, "My Content") || !strings.Contains(h, "Members news") || !strings.Contains(h, "Secret Blend") {
		t.Errorf("members area: %.400s", h)
	}

	// Sign-up plans are granted to new accounts.
	free := &gooMembershipPlan{Name: "Community", AccessMethod: "signup"}
	if err := srv.gmemSavePlan(ctx, free); err != nil {
		t.Fatal(err)
	}
	nu := gxaCustomer(t, srv, "newbie@example.test")
	if l := srv.gmemUMList(ctx, nu.ID, free.ID, ""); len(l) != 1 || l[0].Status != "active" || l[0].End != nil {
		t.Errorf("signup membership: %+v", l)
	}

	// Expiry.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE goo_user_memberships SET end_date = now() - interval '1 hour' WHERE id = $1`, list[0].ID)
	srv.gooFire(ctx, "hourly", nil)
	if m, _ := srv.gmemUM(ctx, list[0].ID); m.Status != "expired" {
		t.Errorf("expired membership: %s", m.Status)
	}
	if out := render(u, "<p>The secret recipe.</p>"); strings.Contains(out, "secret recipe") {
		t.Error("an expired member sees the content")
	}

	// Admin: plans and members.
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=membership-plans", "Gold", "Community")
	body := gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=membership-plans&action=new", "Restrict Content", "Purchasing Discounts")
	code, _, loc := post(t, cl, base+"/wp-admin/goocommerce?page=membership-plans", url.Values{"_wpnonce": {formNonce(t, body)}, "id": {"0"},
		"post_title": {"Silver"}, "_access_method": {"manual"}, "_access_length": {"specific"}, "_access_length_amount": {"3"}, "_access_length_period": {"months"},
		"_content_post_ids": {itoa64(news.ID)}, "discount_target[]": {"product", "product"}, "discount_id[]": {itoa64(widget.ID), ""},
		"discount_type[]": {"fixed", "percent"}, "discount_amount[]": {"2", ""}})
	if code != http.StatusFound || !strings.Contains(loc, "goo-msg") {
		t.Fatalf("save plan: %d %s", code, loc)
	}
	silver, err := srv.gmemPlanBySlug(ctx, "silver")
	if err != nil || silver.AccessLength != "specific" || silver.AccessAmount != 3 || len(silver.Rules.Discounts) != 1 || silver.Rules.Discounts[0].Type != "fixed" {
		t.Fatalf("saved plan: %+v %v", silver, err)
	}
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=membership-plans&action=edit&id="+itoa64(silver.ID), "Silver")
	body = gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=memberships&action=new", "Add Member")
	code, _, loc = post(t, cl, base+"/wp-admin/goocommerce?page=memberships", url.Values{"_wpnonce": {formNonce(t, body)}, "member_action": {"add"},
		"member": {u.Email}, "plan_id": {itoa64(silver.ID)}, "status": {"complimentary"}})
	if code != http.StatusFound || !strings.Contains(loc, "action=edit") {
		t.Fatalf("add member: %d %s", code, loc)
	}
	ms := srv.gmemUMList(ctx, u.ID, silver.ID, "")
	if len(ms) != 1 || ms[0].Status != "complimentary" || ms[0].End == nil {
		t.Fatalf("manual member: %+v", ms)
	}
	if out := render(u, "<p>The secret recipe.</p>"); !strings.Contains(out, "secret recipe") {
		t.Error("a complimentary member of another plan restricting the post can not see it")
	}
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=memberships", "Silver", "Complimentary", "Expired")
	body = gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=memberships&action=edit&id="+itoa64(ms[0].ID), "Membership notes")
	_, _, _ = post(t, cl, base+"/wp-admin/goocommerce?page=memberships", url.Values{"_wpnonce": {formNonce(t, body)}, "member_action": {"cancel"}, "id": {itoa64(ms[0].ID)}})
	if m, _ := srv.gmemUM(ctx, ms[0].ID); m.Status != "cancelled" {
		t.Errorf("cancelled member: %s", m.Status)
	}
	gxaAdminOK(t, cl, base+"/wp-admin/goocommerce?page=memberships&status=cancelled&plan="+itoa64(silver.ID), "Cancelled")
}
