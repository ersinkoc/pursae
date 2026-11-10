package server

import (
	"context"
	"html"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

// TestListScreensBatched: comment lists (single post, REST, admin), feeds, the shop loop and the admin
// orders, products, media and users lists load what each row shows with one query per kind, not one
// (or several) per row.
func TestListScreensBatched(t *testing.T) {
	ts, c, srv, qc := newCountingTestServer(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=goocommerce[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for Pursae Commerce")
	}
	get(t, c, ts.URL+html.UnescapeString(m[1]))
	seedPerfPosts(t, srv, 8)
	ctx := context.Background()
	var admin int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&admin)

	posts, _, _ := srv.store.QueryPosts(ctx, store.PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, PerPage: 10})
	// Eight comments on the newest post, each by another commenter; replies and one by the admin
	// (whose media-library avatar must show).
	var adminEmail string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, admin).Scan(&adminEmail)
	var parent int64
	for i := 0; i < 8; i++ {
		cm := &store.Comment{PostID: posts[0].ID, ParentID: parent, AuthorName: "Reader " + itoa(i), AuthorEmail: "reader" + itoa(i) + "@example.test",
			Content: "Clover note " + itoa(i), Status: "approved", AuthorIP: "10.0.0.1"}
		if i == 5 {
			cm.UserID, cm.AuthorName, cm.AuthorEmail = admin, "admin", adminEmail
		}
		if err := srv.store.InsertComment(ctx, cm); err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			parent = cm.ID
		}
	}
	_ = srv.store.RecountComments(ctx, posts[0].ID)
	for i := 0; i < 3; i++ { // comments elsewhere, for the site-wide comment feed
		_ = srv.store.InsertComment(ctx, &store.Comment{PostID: posts[i+1].ID, AuthorName: "Other", AuthorEmail: "other@example.test", Content: "Elsewhere", Status: "approved"})
	}

	// Products with images, orders with a refund.
	var prods []*gooProduct
	for i := 0; i < 8; i++ {
		a := &store.Attachment{UploaderID: admin, PostID: posts[0].ID, Slug: "shirt" + itoa(i), FilePath: "2026/11/shirt" + itoa(i) + ".jpg", Mime: "image/jpeg", Width: 800, Height: 800,
			Variants: []store.AttachmentVariant{{Name: "thumbnail", FilePath: "2026/11/shirt" + itoa(i) + "-150x150.jpg", Width: 150, Height: 150, Mime: "image/jpeg"},
				{Name: "medium", FilePath: "2026/11/shirt" + itoa(i) + "-300x300.jpg", Width: 300, Height: 300, Mime: "image/jpeg"}}}
		if err := srv.store.InsertAttachment(ctx, a); err != nil {
			t.Fatal(err)
		}
		prods = append(prods, gooMakeSimple(t, srv, "Shirt "+itoa(i), float64(10+i), func(p *gooProduct) { p.ImageID = a.ID }))
	}
	var orders []*gooOrder
	for i := 0; i < 8; i++ {
		o := &gooOrder{Status: "processing", Billing: map[string]string{"first_name": "Ada", "last_name": "L" + itoa(i), "email": "ada" + itoa(i) + "@example.test", "country": "US"},
			Shipping: map[string]string{}, Items: []*gooOrderItem{{Type: "line_item", Name: prods[i].Name, ProductID: prods[i].ID, Quantity: 2, Subtotal: 20, Total: 20},
				{Type: "line_item", Name: prods[0].Name, ProductID: prods[0].ID, Quantity: 1, Subtotal: 10, Total: 10}}}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		orders = append(orders, o)
	}
	if _, err := srv.gooCreateRefund(ctx, orders[3], 5, "Torn", nil, false, false, admin); err != nil {
		t.Fatal(err)
	}

	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	single := pathOf(t, ts.URL, srv.links(ctx).Permalink(posts[0]))
	shop := pathOf(t, ts.URL, srv.links(ctx).Permalink(mustPost(t, srv, srv.gooPageID("shop"))))
	measure := func(cl *http.Client, path string) (map[string]int, string) {
		t.Helper()
		get(t, cl, ts.URL+path) // warm lazy state
		qc.mu.Lock()
		qc.sql = nil
		qc.mu.Unlock()
		qc.log.Store(true)
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		code, body, _ := get(t, cl, ts.URL+path+sep+"count=1")
		qc.log.Store(false)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
		qc.mu.Lock()
		defer qc.mu.Unlock()
		out := map[string]int{}
		for k, v := range qc.sql {
			out[k] = v
		}
		return out, body
	}
	// atMostOnce fails for every statement holding one of the fragments that ran more than once.
	atMostOnce := func(page string, stmts map[string]int, frags ...string) {
		t.Helper()
		for stmt, n := range stmts {
			for _, f := range frags {
				if strings.Contains(stmt, f) && n > 1 {
					t.Errorf("%s: %d× %s", page, n, stmt[max(0, len(stmt)-200):])
				}
			}
		}
	}
	avatarByEmail := "FROM users WHERE lower(email) = $1"
	perPost := "FROM posts WHERE id = $1"
	perUser := "FROM users WHERE id = $1"

	stmts, body := measure(anon, single)
	atMostOnce("single post", stmts, avatarByEmail, "FROM media WHERE id = $1")
	if n := strings.Count(body, "<div class=\"comment-author vcard\">\n\t\t\t\t\t\t\t<img alt='' src='"); n < 8 {
		t.Errorf("single post: %d comment avatars", n)
	}
	if !strings.Contains(body, "/wp-content/uploads/2026/11/avatar") {
		t.Error("single post: the admin's comment lacks the media-library avatar")
	}

	stmts, body = measure(anon, "/wp-json/wp/v2/comments?per_page=10")
	atMostOnce("REST comments", stmts, avatarByEmail, perPost)
	if !strings.Contains(body, `avatar`) || !strings.Contains(body, "Clover note 7") || !strings.Contains(body, `"link":"`) {
		t.Errorf("REST comments: %.300s", body)
	}

	stmts, body = measure(c, "/wp-admin/edit-comments")
	atMostOnce("admin comments", stmts, avatarByEmail, perPost, "FROM comments WHERE id = $1")
	if !strings.Contains(body, "In reply to") || !strings.Contains(body, "Perf note 8") {
		t.Error("admin comments: reply note or post title missing")
	}

	stmts, body = measure(anon, "/feed/")
	atMostOnce("feed", stmts, perUser, "WHERE pt.post_id = ANY")
	if !strings.Contains(body, "<category><![CDATA[Meadows]]></category>") || !strings.Contains(body, "<dc:creator><![CDATA[") {
		t.Errorf("feed misses categories or creators: %.300s", body)
	}
	stmts, body = measure(anon, "/feed/atom/")
	atMostOnce("atom feed", stmts, perUser, "WHERE pt.post_id = ANY")
	if !strings.Contains(body, `term="Meadows"`) {
		t.Error("atom feed misses categories")
	}
	stmts, body = measure(anon, "/comments/feed/")
	atMostOnce("comments feed", stmts, perPost)
	if !strings.Contains(body, "Comment on Perf note") {
		t.Error("comments feed misses its post titles")
	}

	stmts, body = measure(anon, shop)
	atMostOnce("shop", stmts, "WHERE pt.post_id = $1", "FROM media WHERE id = $1")
	if strings.Count(body, "-300x300.jpg") < 8 {
		t.Errorf("shop: %d product images", strings.Count(body, "-300x300.jpg"))
	}

	stmts, body = measure(c, "/wp-admin/goocommerce?page=orders")
	atMostOnce("orders", stmts, "FROM goo_order_items WHERE order_id", "WHERE parent_id = $1")
	if !strings.Contains(body, "<del aria-hidden=\"true\">") {
		t.Error("orders: the refunded order shows no struck-out total")
	}
	stmts, _ = measure(c, "/wp-admin/goocommerce")
	atMostOnce("store home", stmts, "FROM goo_order_items WHERE order_id", "WHERE parent_id = $1")
	stmts, body = measure(c, "/wp-admin/goocommerce?page=analytics&path=/analytics/orders")
	atMostOnce("orders report", stmts, "FROM goo_order_items WHERE order_id", "WHERE parent_id = $1", "SELECT EXISTS(SELECT 1 FROM goo_orders")

	stmts, body = measure(c, "/wp-admin/edit?post_type=product")
	atMostOnce("products", stmts, "FROM media WHERE id = $1")
	if strings.Count(body, "-150x150.jpg") < 8 {
		t.Errorf("products list: %d thumbnails", strings.Count(body, "-150x150.jpg"))
	}

	stmts, body = measure(c, "/wp-admin/upload?mode=list")
	atMostOnce("media", stmts, perUser, perPost)
	if !strings.Contains(body, "Perf note 8") {
		t.Error("media list misses the parent post")
	}

	for i := 0; i < 4; i++ {
		if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO users (login, email, password_hash, nicename, display_name) VALUES ($1, $2, '', $1, $1)`,
			"user"+itoa(i), "user"+itoa(i)+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	stmts, _ = measure(c, "/wp-admin/users")
	atMostOnce("users", stmts, avatarByEmail)

	// The foreign-key lookups of migration 0087.
	for _, idx := range []string{"media_variants_media", "comments_user", "media_uploader"} {
		var ok bool
		_ = srv.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname = $1)`, idx).Scan(&ok)
		if !ok {
			t.Errorf("index %s is missing", idx)
		}
	}
}

// TestGooV3ReportSalesTotals: wc/v3 reports/sales sums the period's paid orders per day in the database
// (items, refunds, taxes) and leaves unpaid ones out.
func TestGooV3ReportSalesTotals(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Mug", 10, nil)
	mk := func(status string, qty float64, shipping float64) *gooOrder {
		o := &gooOrder{Status: status, Billing: map[string]string{"email": "r@example.test"}, Shipping: map[string]string{},
			Items: []*gooOrderItem{{Type: "line_item", Name: p.Name, ProductID: p.ID, Quantity: qty, Subtotal: 10 * qty, Total: 10 * qty}}}
		if shipping > 0 {
			o.Items = append(o.Items, &gooOrderItem{Type: "shipping", Name: "Flat", Total: shipping})
		}
		srv.gooRecalculate(o)
		if err := srv.gooSaveOrder(ctx, o, true); err != nil {
			t.Fatal(err)
		}
		return o
	}
	a := mk("completed", 2, 5) // 25
	mk("processing", 3, 0)     // 30
	mk("pending", 7, 0)        // unpaid: not counted
	if _, err := srv.gooCreateRefund(ctx, a, 4, "", nil, false, false, 1); err != nil {
		t.Fatal(err)
	}
	ck, cs, _, err := srv.gooRESTNewKey(ctx, 1, "Reports", "read")
	if err != nil {
		t.Fatal(err)
	}
	r := gooReq(t, &http.Client{}, "GET", base+"/wp-json/wc/v3/reports/sales?consumer_key="+url.QueryEscape(ck)+"&consumer_secret="+url.QueryEscape(cs), nil, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("reports/sales: %d %s", r.Code, r.Raw)
	}
	rep := gooJ(r.Body, 0)
	num := func(k string) float64 {
		switch v := gooJ(rep, k).(type) {
		case float64:
			return v
		case string:
			var f float64
			for i, part := range strings.SplitN(v, ".", 2) {
				for _, ch := range part {
					f = f*10 + float64(ch-'0')
				}
				if i == 1 {
					f /= math.Pow(10, float64(len(part)))
				}
			}
			return f
		}
		t.Fatalf("%s: %v", k, gooJ(rep, k))
		return 0
	}
	if n := num("total_orders"); n != 2 {
		t.Errorf("total_orders = %v, want 2", n)
	}
	if n := num("total_items"); n != 5 {
		t.Errorf("total_items = %v, want 5", n)
	}
	if n := num("total_refunds"); n != 4 {
		t.Errorf("total_refunds = %v, want 4", n)
	}
	if n := num("total_shipping"); n != 5 {
		t.Errorf("total_shipping = %v, want 5", n)
	}
	if n := num("total_sales"); math.Abs(n-51) > 0.001 { // 25 + 30 - 4
		t.Errorf("total_sales = %v, want 51", n)
	}
	today := time.Now().In(srv.loc()).Format("2006-01-02")
	if gooJ(rep, "totals", today, "orders") != float64(2) || gooJ(rep, "totals", today, "items") != float64(5) {
		t.Errorf("totals[%s] = %v", today, gooJ(rep, "totals", today))
	}
}
