package server

import (
	"context"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Marketing › Coupons: the coupon list (page=coupons, WooCommerce's edit.php?post_type=shop_coupon) and
// the coupon editor (action=edit&id= / action=new) with its Coupon data tabs.

type gooCouponRow struct {
	ID          int64
	Code        string
	Type        string
	Amount      string
	Description string
	ProductIDs  string
	Usage       string
	Expiry      string
	Status      string
	EditURL     string
}

type gooCouponsData struct {
	Rows      []gooCouponRow
	Views     []gooView
	Status    string
	Search    string
	Pager     gooPager
	Nonce     string
	IsTrash   bool
	Bulk      []gooOpt
	Enabled   bool
	ExportURL string
}

func (s *Server) gooAdminCoupons(c *gin.Context) {
	switch c.Query("action") {
	case "edit", "new":
		s.gooAdminCouponEdit(c)
		return
	case "export_csv":
		s.gooExportCouponsCSV(c)
		return
	case "trash", "untrash", "delete":
		if !s.checkNonce(c, gooStoreNonce) {
			s.gooDeny(c)
			return
		}
		id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
		n := s.gooCouponBulk(c.Request.Context(), c.Query("action"), []int64{id})
		gooRedirect(c, gooAdminURL("coupons", "post_status", c.Query("post_status")), gooCouponBulkMsg(c.Query("action"), n), "")
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, gooStoreNonce) {
			s.gooDeny(c)
			return
		}
		action := c.PostForm("action")
		if action == "" || action == "-1" {
			action = c.PostForm("action2")
		}
		var ids []int64
		for _, raw := range c.PostFormArray("post[]") {
			if id, _ := strconv.ParseInt(raw, 10, 64); id > 0 {
				ids = append(ids, id)
			}
		}
		n := s.gooCouponBulk(ctx, action, ids)
		gooRedirect(c, gooAdminURL("coupons", "post_status", c.PostForm("post_status")), gooCouponBulkMsg(action, n), "")
		return
	}
	status := c.Query("post_status")
	d := &gooCouponsData{Status: status, Search: strings.TrimSpace(c.Query("s")), Nonce: s.nonce(c, gooStoreNonce), IsTrash: status == "trash",
		Enabled: s.gooCouponsEnabled()}
	d.ExportURL = gooAdminURL("coupons", "action", "export_csv", "post_status", status, "s", d.Search, "_wpnonce", d.Nonce)
	page := gooPaged(c)
	coupons, total, err := s.gooQueryCoupons(ctx, status, d.Search, page, 20)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	types := map[string]string{}
	for _, t := range gooCouponTypes {
		types[t.Value] = t.Label
	}
	for _, cp := range coupons {
		r := gooCouponRow{ID: cp.ID, Code: cp.Code, Type: types[cp.DiscountType], Amount: s.gooFmtDec(cp.Amount), Description: cp.Description,
			Status: cp.Status, EditURL: gooAdminURL("coupons", "action", "edit", "id", itoa64(cp.ID))}
		var ids []string
		for _, id := range cp.ProductIDs {
			ids = append(ids, itoa64(id))
		}
		r.ProductIDs = strings.Join(ids, ", ")
		if r.ProductIDs == "" {
			r.ProductIDs = "&ndash;"
		}
		limit := "&infin;"
		if cp.UsageLimit != nil && *cp.UsageLimit > 0 {
			limit = strconv.Itoa(*cp.UsageLimit)
		}
		r.Usage = strconv.Itoa(cp.UsageCount) + " / " + limit
		r.Expiry = "&ndash;"
		if cp.DateExpires != nil {
			r.Expiry = cp.DateExpires.In(s.loc()).Format("January 2, 2006")
		}
		d.Rows = append(d.Rows, r)
	}
	d.Pager = newGooPager(gooAdminURL("coupons", "post_status", status, "s", d.Search), page, 20, total)
	counts := map[string]int{}
	rows, err := s.store.Pool.Query(ctx, `SELECT status, count(*) FROM goo_coupons GROUP BY status`)
	if err == nil {
		for rows.Next() {
			var st string
			var n int
			if rows.Scan(&st, &n) == nil {
				counts[st] = n
			}
		}
		rows.Close()
	}
	d.Views = append(d.Views, gooView{Label: "All", URL: gooAdminURL("coupons"), Count: counts["publish"] + counts["draft"], Current: status == ""})
	for _, v := range []gooOpt{{"publish", "Published"}, {"draft", "Drafts"}, {"trash", "Trash"}} {
		if counts[v.Value] > 0 {
			d.Views = append(d.Views, gooView{Label: v.Label, URL: gooAdminURL("coupons", "post_status", v.Value), Count: counts[v.Value], Current: status == v.Value})
		}
	}
	if d.IsTrash {
		d.Bulk = []gooOpt{{"untrash", "Restore"}, {"delete", "Delete permanently"}}
	} else {
		d.Bulk = []gooOpt{{"trash", "Move to Trash"}}
	}
	s.gooStoreRender(c, "goocommerce?page=coupons", "wc-coupons", "Coupons", "admin/goo-coupons", d)
}

func gooCouponBulkMsg(action string, n int) string {
	switch action {
	case "trash":
		return plural(n, "1 coupon moved to the Trash.", "%d coupons moved to the Trash.")
	case "untrash":
		return plural(n, "1 coupon restored from the Trash.", "%d coupons restored from the Trash.")
	case "delete":
		return plural(n, "1 coupon permanently deleted.", "%d coupons permanently deleted.")
	}
	return ""
}

func (s *Server) gooCouponBulk(ctx context.Context, action string, ids []int64) int {
	n := 0
	for _, id := range ids {
		cp, err := s.gooCouponByID(ctx, id)
		if err != nil {
			continue
		}
		switch action {
		case "trash":
			cp.Meta["_wp_trash_meta_status"] = cp.Status
			cp.Status = "trash"
			if s.gooSaveCoupon(ctx, cp) == nil {
				n++
				s.gooWebhookFire(ctx, "coupon.deleted", cp.ID, map[string]any{"id": cp.ID})
			}
		case "untrash":
			prev, _ := cp.Meta["_wp_trash_meta_status"].(string)
			cp.Status = defaultStr(prev, "draft")
			delete(cp.Meta, "_wp_trash_meta_status")
			if s.gooSaveCoupon(ctx, cp) == nil {
				n++
				s.gooWebhookCoupon(ctx, "coupon.restored", cp.ID)
			}
		case "delete":
			if _, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_coupons WHERE id = $1`, id); err == nil {
				n++
				if cp.Status != "trash" {
					s.gooWebhookFire(ctx, "coupon.deleted", id, map[string]any{"id": id})
				}
			}
		}
	}
	return n
}

// Editor ------------------------------------------------------------------------------------------------------

type gooCouponEditData struct {
	C           *gooCoupon
	New         bool
	Nonce       string
	Action      string
	Types       []gooOpt
	Amount      string
	Expiry      string
	Min, Max    string
	Limit       string
	LimitItems  string
	LimitUser   string
	Emails      string
	Products    []gooOpt
	Categories  []gooOpt
	Sel         map[string]map[string]bool
	StatusLabel string
	Created     string
}

func (s *Server) gooAdminCouponEdit(c *gin.Context) {
	ctx := c.Request.Context()
	cp := &gooCoupon{DiscountType: "fixed_cart", Status: "draft", Meta: map[string]any{}}
	isNew := c.Query("action") == "new"
	if !isNew {
		id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
		var err error
		if cp, err = s.gooCouponByID(ctx, id); err != nil {
			c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
			return
		}
	}
	var notices []notice
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, gooStoreNonce) {
			s.gooDeny(c)
			return
		}
		s.gooCouponFromForm(c, cp)
		wasNew := cp.ID == 0
		if err := s.gooSaveCoupon(ctx, cp); err != nil {
			notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(err.Error()))})
		} else {
			if wasNew {
				s.gooWebhookCoupon(ctx, "coupon.created", cp.ID)
			} else {
				s.gooWebhookCoupon(ctx, "coupon.updated", cp.ID)
			}
			msg := "Coupon updated."
			if wasNew && cp.Status == "publish" {
				msg = "Coupon published."
			} else if cp.Status == "draft" {
				msg = "Coupon draft updated."
			}
			gooRedirect(c, gooAdminURL("coupons", "action", "edit", "id", itoa64(cp.ID)), msg, "")
			return
		}
	}
	d := s.gooCouponEditView(c, cp, isNew)
	title := "Edit coupon"
	if isNew {
		title = "Add coupon"
	}
	s.gooStoreRender(c, "goocommerce?page=coupons", "wc-coupons", title, "admin/goo-coupon-edit", d, notices...)
}

func (s *Server) gooCouponFromForm(c *gin.Context, cp *gooCoupon) {
	cp.Code = strings.TrimSpace(c.PostForm("post_title"))
	cp.Description = strings.TrimSpace(c.PostForm("excerpt"))
	cp.DiscountType = c.PostForm("discount_type")
	cp.Amount = s.gooFloat(c.PostForm("coupon_amount"))
	cp.FreeShipping = c.PostForm("free_shipping") != ""
	cp.DateExpires = nil
	if v := strings.TrimSpace(c.PostForm("expiry_date")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, s.loc()); err == nil {
			t = t.Add(24*time.Hour - time.Second) // the end of that day, like WooCommerce
			cp.DateExpires = &t
		}
	}
	cp.MinimumAmount = s.gooParseAmount(c.PostForm("minimum_amount"))
	cp.MaximumAmount = s.gooParseAmount(c.PostForm("maximum_amount"))
	cp.IndividualUse = c.PostForm("individual_use") != ""
	cp.ExcludeSaleItems = c.PostForm("exclude_sale_items") != ""
	ids := func(name string) []int64 {
		var out []int64
		for _, raw := range c.PostFormArray(name) {
			for _, part := range strings.Split(raw, ",") {
				if id, _ := strconv.ParseInt(strings.TrimSpace(part), 10, 64); id > 0 {
					out = append(out, id)
				}
			}
		}
		return out
	}
	cp.ProductIDs = ids("product_ids[]")
	cp.ExcludedProductIDs = ids("exclude_product_ids[]")
	cp.ProductCategories = ids("product_categories[]")
	cp.ExcludedProductCategories = ids("exclude_product_categories[]")
	cp.EmailRestrictions = nil
	for _, e := range strings.Split(c.PostForm("customer_email"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			cp.EmailRestrictions = append(cp.EmailRestrictions, e)
		}
	}
	intp := func(name string) *int {
		v := strings.TrimSpace(c.PostForm(name))
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return &n
		}
		return nil
	}
	cp.UsageLimit = intp("usage_limit")
	cp.LimitUsageToXItems = intp("limit_usage_to_x_items")
	cp.UsageLimitPerUser = intp("usage_limit_per_user")
	if c.PostForm("save") != "" {
		cp.Status = "draft"
	} else {
		cp.Status = "publish"
	}
}

func (s *Server) gooCouponEditView(c *gin.Context, cp *gooCoupon, isNew bool) *gooCouponEditData {
	ctx := c.Request.Context()
	d := &gooCouponEditData{C: cp, New: isNew, Nonce: s.nonce(c, gooStoreNonce), Types: gooCouponTypes, Amount: s.gooFmtDec(cp.Amount),
		Emails: strings.Join(cp.EmailRestrictions, ", "), Sel: map[string]map[string]bool{}}
	d.Action = gooAdminURL("coupons", "action", "edit", "id", itoa64(cp.ID))
	if isNew || cp.ID == 0 {
		d.Action = gooAdminURL("coupons", "action", "new")
	}
	if cp.Amount == 0 {
		d.Amount = "0"
	}
	if cp.DateExpires != nil {
		d.Expiry = cp.DateExpires.In(s.loc()).Format("2006-01-02")
	}
	fp := func(v *float64) string {
		if v == nil {
			return ""
		}
		return s.gooFmtDec(*v)
	}
	ip := func(v *int) string {
		if v == nil {
			return ""
		}
		return strconv.Itoa(*v)
	}
	d.Min, d.Max = fp(cp.MinimumAmount), fp(cp.MaximumAmount)
	d.Limit, d.LimitItems, d.LimitUser = ip(cp.UsageLimit), ip(cp.LimitUsageToXItems), ip(cp.UsageLimitPerUser)
	set := func(key string, ids []int64) {
		d.Sel[key] = map[string]bool{}
		for _, id := range ids {
			d.Sel[key][itoa64(id)] = true
		}
	}
	set("product_ids", cp.ProductIDs)
	set("exclude_product_ids", cp.ExcludedProductIDs)
	set("product_categories", cp.ProductCategories)
	set("exclude_product_categories", cp.ExcludedProductCategories)
	rows, err := s.store.Pool.Query(ctx, `SELECT po.id, po.title, coalesce(p.sku,'') FROM posts po LEFT JOIN goo_products p ON p.post_id = po.id
		WHERE po.type IN ('product','product_variation') AND po.status NOT IN ('trash','auto-draft') ORDER BY po.title LIMIT 1000`)
	if err == nil {
		for rows.Next() {
			var id int64
			var title, sku string
			if rows.Scan(&id, &title, &sku) == nil {
				lbl := title
				if sku != "" {
					lbl += " (" + sku + ")"
				}
				d.Products = append(d.Products, gooOpt{itoa64(id), lbl + " (#" + itoa64(id) + ")"})
			}
		}
		rows.Close()
	}
	if terms, err := s.store.HierarchicalTerms(ctx, "product_cat", false); err == nil {
		for _, t := range terms {
			d.Categories = append(d.Categories, gooOpt{itoa64(t.ID), strings.Repeat("— ", t.Depth) + t.Name})
		}
	}
	d.StatusLabel = map[string]string{"publish": "Published", "draft": "Draft", "trash": "Trash"}[cp.Status]
	if !cp.DateCreated.IsZero() {
		d.Created = cp.DateCreated.In(s.loc()).Format("Jan 2, 2006 @ 15:04")
	}
	return d
}

// Selected reports whether an id is selected in a restriction list (template helper).
func (d *gooCouponEditData) Selected(key, id string) bool { return d.Sel[key][id] }
