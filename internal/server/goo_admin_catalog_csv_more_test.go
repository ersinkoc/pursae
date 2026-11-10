package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGooMetaCellRoundTrip(t *testing.T) {
	for _, v := range []any{"plain", "007", "true", "[1]", `"quoted"`, 9.5, float64(3), true, []any{"a", float64(1)}, map[string]any{"k": "v"}} {
		got := gooMetaFromCell(gooMetaCell(v))
		a, _ := json.Marshal(v)
		b, _ := json.Marshal(got)
		if string(a) != string(b) {
			t.Errorf("%#v -> %q -> %#v", v, gooMetaCell(v), got)
		}
	}
	if gooMetaCell(nil) != "" || gooMetaCell("") != "" {
		t.Error("empty meta must be an empty cell")
	}
}

func TestGooImportCleanupDir(t *testing.T) {
	dir := t.TempDir()
	old, fresh, other := filepath.Join(dir, "goo-import-1.csv"), filepath.Join(dir, "goo-import-2.csv"), filepath.Join(dir, "keep.csv")
	oldState := filepath.Join(dir, "goo-import-1.state.json")
	for _, f := range []string{old, fresh, other, oldState} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	for _, f := range []string{old, oldState, other} {
		_ = os.Chtimes(f, past, past)
	}
	if err := gooImportCleanupDir(dir, time.Now().Add(-gooImportStaleAfter)); err != nil {
		t.Fatal(err)
	}
	for f, want := range map[string]bool{old: false, oldState: false, fresh: true, other: true} {
		if _, err := os.Stat(f); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", filepath.Base(f), err == nil, want)
		}
	}
}

// gooCatUpload posts the CSV to the importer's mapping step and returns the stored file token and nonce.
func gooCatUpload(t *testing.T, c *http.Client, base string, data []byte) (token, nonce string) {
	t.Helper()
	_, page, _ := get(t, c, base+"/wp-admin/edit?post_type=product&page=product_importer")
	nonce = formNonce(t, page)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("import", "products.csv")
	_, _ = fw.Write(data)
	_ = mw.WriteField("_wpnonce", nonce)
	_ = mw.WriteField("step", "mapping")
	_ = mw.WriteField("delimiter", ",")
	mw.Close()
	resp, err := c.Post(base+"/wp-admin/goocommerce?page=product_importer", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return gooCatHref(t, string(b), `name="file" value="([^"]+)"`), nonce
}

func TestGooCSVMetaRoundTrip(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Monthly box", 20, func(p *gooProduct) {
		p.SKU = "BOX"
		p.Meta["_subscription_price"] = 9.5
		p.Meta["_subscription_period"] = "month"
		p.Meta["_gift_card_amounts"] = []any{float64(10), float64(25)}
		p.Meta["_code"] = "007"
		p.Meta["_wc_booking_opts"] = map[string]any{"min": float64(2)}
	})
	_, page, _ := get(t, c, base+"/wp-admin/edit?post_type=product&page=product_exporter")
	if !strings.Contains(page, `name="export_meta"`) {
		t.Fatal("the exporter has no custom meta option")
	}
	nonce := formNonce(t, page)
	exp := base + "/wp-admin/edit?post_type=product&page=product_exporter&action=download_product_csv&_wpnonce=" + nonce
	_, plain, _ := get(t, c, exp)
	if strings.Contains(plain, "Meta: ") {
		t.Error("meta columns exported without the option")
	}
	_, data, _ := get(t, c, exp+"&export_meta=1")
	rows, err := gooReadCSV([]byte(data), ",")
	if err != nil || len(rows) != 2 {
		t.Fatalf("export: %v %d\n%s", err, len(rows), data)
	}
	if !strings.Contains(strings.Join(rows[0], "|"), "Meta: _subscription_price") {
		t.Fatalf("no meta column in %v", rows[0])
	}
	// Re-import under another SKU: a new product with the same settings.
	for i, h := range rows[0] {
		switch h {
		case "SKU":
			rows[1][i] = "BOX-COPY"
		case "ID":
			rows[1][i] = ""
		}
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write(rows[0])
	_ = w.Write(rows[1])
	w.Flush()
	gooCatImport(t, c, base, buf.Bytes(), false)
	id := srv.gooRefID(ctx, "BOX-COPY", nil)
	if id == 0 {
		t.Fatal("the copy was not imported")
	}
	cp, err := srv.gooGetProduct(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range p.Meta {
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(cp.Meta[k])
		if string(a) != string(b) {
			t.Errorf("meta %s: got %s, want %s", k, b, a)
		}
	}
}

func TestGooCSVImagesDownloaded(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/img/mug.png" {
			hits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
			return
		}
		if r.URL.Path == "/page.html" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	csv := "ID,Type,SKU,Name,Regular price,Images\n,simple,IMG-1,Mug,5," + ts.URL + "/img/mug.png\n,simple,IMG-2,Cup,6," + ts.URL + "/img/mug.png\n"
	// Private addresses are refused unless a test allows them.
	done := gooCatImport(t, c, base, []byte(csv), false)
	if !strings.Contains(done, "private network") {
		t.Fatalf("a loopback image URL was fetched or the error is missing: %.600s", done)
	}
	gooImportAllowPrivate.Store(true)
	defer gooImportAllowPrivate.Store(false)
	done = gooCatImport(t, c, base, []byte(csv), false)
	if hits.Load() != 1 {
		t.Errorf("image downloaded %d times, want once (the second row reuses it)", hits.Load())
	}
	var ids []int64
	for _, sku := range []string{"IMG-1", "IMG-2"} {
		p, err := srv.gooGetProduct(ctx, srv.gooRefID(ctx, sku, nil))
		if err != nil || p.ImageID == 0 {
			t.Fatalf("%s has no image (%v)\n%.600s", sku, err, done)
		}
		ids = append(ids, p.ImageID)
	}
	if ids[0] != ids[1] {
		t.Errorf("the same URL created two attachments: %v", ids)
	}
	a, err := srv.store.GetAttachment(ctx, ids[0])
	if err != nil || !strings.HasPrefix(a.Mime, "image/") || a.Meta[gooImportSourceKey] != ts.URL+"/img/mug.png" {
		t.Errorf("attachment: %v %+v", err, a)
	}
	bad := "ID,Type,SKU,Name,Regular price,Images\n,simple,IMG-3,Page,5," + ts.URL + "/page.html\n"
	if done := gooCatImport(t, c, base, []byte(bad), false); !strings.Contains(done, "not an image") || srv.gooRefID(ctx, "IMG-3", nil) != 0 {
		t.Errorf("a non-image URL must fail the row: %.500s", done)
	}
}

func TestGooCSVImportBatches(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	var sb strings.Builder
	sb.WriteString("ID,Type,SKU,Name,Regular price\n")
	const n = gooImportBatchSize*2 + 7
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, ",simple,B-%d,Batch %d,%d\n", i, i, i)
	}
	sb.WriteString(",simple,B-1,Duplicate,1\n") // skipped: SKU exists (created in an earlier batch)
	token, nonce := gooCatUpload(t, c, base, []byte(sb.String()))
	step := func() map[string]any {
		code, body, _ := post(t, c, base+"/wp-admin/goocommerce?page=product_importer", url.Values{"_wpnonce": {nonce}, "step": {"batch"}, "delimiter": {","}, "file": {token}})
		if code != http.StatusOK {
			t.Fatalf("batch: %d %.300s", code, body)
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("batch JSON: %v %.300s", err, body)
		}
		return r
	}
	r := step()
	if r["done"] != false || r["position"] != float64(gooImportBatchSize) || r["total"] != float64(n+1) {
		t.Fatalf("first batch: %v", r)
	}
	if srv.gooRefID(ctx, "B-50", nil) == 0 || srv.gooRefID(ctx, "B-51", nil) != 0 {
		t.Error("the first batch must import exactly its rows")
	}
	steps := 1
	for r["done"] == false && steps < 10 {
		r = step()
		steps++
	}
	if steps != 3 || r["imported"] != float64(n) || r["skipped"] != float64(1) {
		t.Fatalf("after %d batches: %v", steps, r)
	}
	log := fmt.Sprint(r["log"])
	if !strings.Contains(log, fmt.Sprintf("Row %d", n+2)) {
		t.Errorf("log row numbers: %s", log)
	}
	if _, err := os.Stat(filepath.Join(os.TempDir(), token)); err == nil {
		t.Error("the upload was not removed after the last batch")
	}
	if _, ok := gooLoadImportState(token); ok {
		t.Error("the state file was not removed after the last batch")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/goocommerce?page=product_importer", url.Values{"_wpnonce": {nonce}, "step": {"batch"}, "file": {"../../etc/passwd"}}); code != http.StatusBadRequest {
		t.Errorf("bad token: %d", code)
	}
}

func TestGooListExports(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	p := gooMakeSimple(t, srv, "Teapot", 10, func(p *gooProduct) { p.SKU = "TEA" })
	o1 := gooTestOrder(t, srv, p, 2)
	o2 := gooTestOrder(t, srv, p, 1)
	_ = srv.gooSetStatus(ctx, o2, "completed", "", "")
	cp := &gooCoupon{Code: "=sum(1)", DiscountType: "percent", Amount: 10, Status: "publish", Meta: map[string]any{}}
	if err := srv.gooSaveCoupon(ctx, cp); err != nil {
		t.Fatal(err)
	}
	link := func(listURL, action string) string {
		_, page, _ := get(t, c, listURL)
		m := regexp.MustCompile(`href="([^"]*action=export_csv[^"]*)"`).FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("%s has no Export CSV link", listURL)
		}
		return html.UnescapeString(m[1])
	}
	// Orders: the link keeps the status filter and exports every matching order.
	ou := link(base+"/wp-admin/goocommerce?page=orders&status=wc-completed", "orders")
	code, data, _ := get(t, c, base+ou)
	rows, err := gooReadCSV([]byte(data), ",")
	if code != http.StatusOK || err != nil || len(rows) != 2 || rows[1][0] != itoa64(o2.ID) || !strings.Contains(rows[1][23], "Teapot x 1") {
		t.Fatalf("orders export (completed): %d %v\n%s", code, err, data)
	}
	all := link(base+"/wp-admin/goocommerce?page=orders", "orders")
	_, data, _ = get(t, c, base+all)
	if rows, _ := gooReadCSV([]byte(data), ","); len(rows) != 3 || rows[1][0] != itoa64(o1.ID) {
		t.Errorf("orders export (all): %s", data)
	}
	if code, _, _ := get(t, c, base+strings.Split(all, "&_wpnonce=")[0]); code != http.StatusForbidden {
		t.Errorf("orders export without a nonce: %d", code)
	}
	// Coupons: formula-looking codes are neutralised.
	cu := link(base+"/wp-admin/goocommerce?page=coupons", "coupons")
	_, data, _ = get(t, c, base+cu)
	if rows, _ := gooReadCSV([]byte(data), ","); len(rows) != 2 || rows[1][1] != "'=sum(1)" || rows[1][3] != "percent" {
		t.Errorf("coupons export: %s", data)
	}
	// Subscriptions.
	sub := &gooSubscription{Status: "active", Period: "month", Interval: 1, Currency: "USD", RecurringTotal: 9, Start: time.Now(), PaymentMethod: "bacs",
		Billing: map[string]string{"first_name": "Ann", "email": "ann@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{},
		Items: []gsubItem{{ProductID: p.ID, Name: "Teapot club", Qty: 1, Total: 9}}}
	if err := srv.gsubSave(ctx, sub); err != nil {
		t.Fatal(err)
	}
	su := link(base+"/wp-admin/goocommerce?page=subscriptions", "subscriptions")
	_, data, _ = get(t, c, base+su)
	if rows, _ := gooReadCSV([]byte(data), ","); len(rows) != 2 || rows[1][1] != "active" || !strings.Contains(rows[1][17], "Teapot club x 1") || rows[1][5] != "ann@example.test" {
		t.Errorf("subscriptions export: %s", data)
	}
}
