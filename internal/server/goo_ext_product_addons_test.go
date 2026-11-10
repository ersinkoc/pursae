package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestGooExtAddons(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	gxTestGateway(t, srv)

	mug := gooMakeSimple(t, srv, "Mug", 20, func(p *gooProduct) { p.Virtual = true })
	if err := srv.paoSaveGroup(ctx, &paoGroup{Name: "Wrapping", Priority: 5, AllProducts: true, Fields: []paoAddon{
		{Name: "Gift wrap", Type: "checkbox", Options: []paoOption{{Label: "Yes", Price: 5, PriceType: "flat_fee"}}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.paoSaveGroup(ctx, &paoGroup{Name: "Other category", Priority: 1, Categories: []int64{999999}, Fields: []paoAddon{{Name: "Never", Type: "custom_text"}}}); err != nil {
		t.Fatal(err)
	}
	mug = gxSavePanels(t, srv, mug, paoKey, url.Values{
		"product_addon_name[]":               {"Engraving", "Color", "Extra shots", "Upload", "Tip", "Delivery"},
		"product_addon_type[]":               {"custom_text", "multiple_choice", "input_multiplier", "file_upload", "custom_price", "datepicker"},
		"product_addon_display[]":            {"select", "radiobutton", "select", "select", "select", "select"},
		"product_addon_title_format[]":       {"label", "label", "label", "label", "label", "label"},
		"product_addon_description_enable[]": {"yes", "no", "no", "no", "no", "no"},
		"product_addon_description[]":        {"Up to 10 letters", "", "", "", "", ""},
		"product_addon_required[]":           {"yes", "no", "no", "no", "no", "no"},
		"product_addon_options[]":            {"", "Red | 0\nGold | 10 | percentage_based", "", "", "", ""},
		"product_addon_adjust_price[]":       {"yes", "no", "yes", "no", "no", "no"},
		"product_addon_price_type[]":         {"quantity_based", "flat_fee", "quantity_based", "flat_fee", "flat_fee", "flat_fee"},
		"product_addon_price[]":              {"3", "", "1", "", "", ""},
		"product_addon_restrictions[]":       {"yes", "no", "no", "no", "yes", "no"},
		"product_addon_restrictions_type[]":  {"only_letters", "any_text", "any_text", "any_text", "any_text", "any_text"},
		"product_addon_min[]":                {"", "", "", "", "1", ""},
		"product_addon_max[]":                {"10", "", "", "", "50", ""},
	})
	if got := paoDecode(mug.Meta["_product_addons"]); len(got) != 6 || len(got[1].Options) != 2 || got[1].Options[1].PriceType != "percentage_based" || !got[0].Required {
		t.Fatalf("saved product add-ons: %+v", got)
	}

	// Off: no fields, no validation, no price change.
	c0 := gooTestCtx()
	cart0 := srv.gooCartFor(c0)
	k0, err := cart0.Add(ctx, mug.ID, 1, 0, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gooAlmost(t, "price while off", cart0.Item(k0).Price, 20)
	if out := srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", mug); out != "" {
		t.Error("add-on fields shown while off")
	}

	if err := srv.gooSetExtension(ctx, paoKey, true); err != nil {
		t.Fatal(err)
	}
	addons := srv.paoAddonsFor(ctx, mug)
	if len(addons) != 7 || addons[0].Name != "Gift wrap" {
		t.Fatalf("add-ons of the mug: %d (%+v)", len(addons), addons)
	}
	name := func(i int) string { return paoFieldName(mug.ID, addons[i], i) }
	html := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", mug))
	for _, want := range []string{name(0) + "[]", name(1), name(2) + `" value="gold-2"`, "product-addons-total", "wc-pao-addon-file-upload"} {
		if !strings.Contains(html, want) {
			t.Errorf("add-on fields lack %q", want)
		}
	}

	c := gooTestCtx()
	cart := srv.gooCartFor(c)
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{}, nil); err == nil || !strings.Contains(err.Error(), "required field") {
		t.Errorf("missing required engraving: %v", err)
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hello World"}}, nil); err == nil || !strings.Contains(err.Error(), "maximum allowed length") {
		t.Errorf("too long engraving: %v", err)
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hi5"}}, nil); err == nil || !strings.Contains(err.Error(), "Only letters") {
		t.Errorf("digits in a letters-only engraving: %v", err)
	}
	for _, bad := range []string{"NaN", "Inf", "-Inf"} { // ParseFloat accepts these; a NaN price would make the cart free
		if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hi"}, name(5): {bad}}, nil); err == nil || !strings.Contains(err.Error(), "valid amount") {
			t.Errorf("custom price %q: %v", bad, err)
		}
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hi"}, name(5): {"500"}}, nil); err == nil || !strings.Contains(err.Error(), "maximum allowed amount") {
		t.Errorf("tip above the max: %v", err)
	}
	if _, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Hi"}, name(4): {"https://evil.example/x.php"}}, nil); err == nil {
		t.Error("a foreign file URL was accepted")
	}
	// 2 mugs: 20 + engraving 3 + gold 10% (2) + 2 extra shots × 1 + gift wrap 5 once (2.50 each) + tip 4 each.
	key, err := cart.Add(ctx, mug.ID, 2, 0, nil, map[string][]string{name(0) + "[]": {"yes-1"}, name(1): {"Hi"}, name(2): {"gold-2"}, name(3): {"2"}, name(5): {"4"}, name(6): {"2026-12-24"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := cart.Item(key)
	gooAlmost(t, "unit price with add-ons", it.Price, 20+3+2+2+2.5+4)
	gooAlmost(t, "line subtotal", it.LineSubtotal, 2*(20+3+2+2+4)+5)
	got := map[string]string{}
	for _, kv := range srv.gooItemData(ctx, it) {
		got[kv.Key] = kv.Value
	}
	for k, v := range map[string]string{"Gift wrap (+$5.00)": "Yes", "Engraving (+$3.00)": "Hi", "Color (+10%)": "Gold", "Extra shots (+$2.00)": "2", "Delivery": "2026-12-24"} {
		if got[k] != v {
			t.Errorf("item data %q = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}

	// File upload through the AJAX route, then used in the cart.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("goo_module", "product_addons")
	_ = mw.WriteField("_wpnonce", srv.nonce(gooTestCtx(), paoUploadNonce))
	fw, _ := mw.CreateFormFile("file", "my logo.png")
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	_ = mw.Close()
	w := httptest.NewRecorder()
	uc, _ := gin.CreateTestContext(w)
	uc.Request = httptest.NewRequest(http.MethodPost, "/?goo-ajax=addon_upload", &body)
	uc.Request.Header.Set("Content-Type", mw.FormDataContentType())
	if !paoFrontRoute(srv, uc, "") {
		t.Fatal("upload route not served")
	}
	var up struct{ URL, Name, Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &up)
	if up.URL == "" || !strings.Contains(up.URL, "/wp-content/uploads/goocommerce_uploads/") || up.Name != "my-logo.png" {
		t.Fatalf("upload: %s", w.Body.String())
	}
	rel := up.URL[strings.Index(up.URL, "goocommerce_uploads/"):]
	if _, err := os.Stat(filepath.Join(srv.cfg.UploadsDir, filepath.FromSlash(rel))); err != nil {
		t.Errorf("uploaded file not stored: %v", err)
	}
	// A disallowed type is refused.
	body.Reset()
	mw = multipart.NewWriter(&body)
	_ = mw.WriteField("_wpnonce", srv.nonce(gooTestCtx(), paoUploadNonce))
	fw, _ = mw.CreateFormFile("file", "shell.php")
	_, _ = fw.Write([]byte("<?php"))
	_ = mw.Close()
	w = httptest.NewRecorder()
	uc, _ = gin.CreateTestContext(w)
	uc.Request = httptest.NewRequest(http.MethodPost, "/?goo-ajax=addon_upload", &body)
	uc.Request.Header.Set("Content-Type", mw.FormDataContentType())
	paoFrontRoute(srv, uc, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("a .php upload: %d %s", w.Code, w.Body.String())
	}
	k2, err := cart.Add(ctx, mug.ID, 1, 0, nil, map[string][]string{name(1): {"Yo"}, name(4): {up.URL}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kv := srv.gooItemData(ctx, cart.Item(k2)); len(kv) < 2 || kv[1].Value != "my-logo.png" {
		t.Errorf("upload item data: %+v", kv)
	}

	// Checkout keeps the add-ons in the order item meta.
	cart.sess.Data.Customer.Billing = map[string]string{"country": "US", "state": "CA", "postcode": "90210", "email": "buyer@example.test"}
	o, errs := gxCheckout(t, srv, c, "goo_test")
	if len(errs) > 0 {
		t.Fatalf("checkout: %v", errs)
	}
	var found bool
	for _, li := range o.ItemsOf("line_item") {
		if l, ok := li.Meta["_goo_addons"].([]any); ok && len(l) == 6 {
			found = true
			if d, ok := li.Meta["_display"].([]any); !ok || len(d) != 6 {
				t.Errorf("display meta: %+v", li.Meta["_display"])
			}
		}
	}
	if !found {
		t.Error("no add-on meta on the order item")
	}
	gooAlmost(t, "order total", o.Total, 2*(20+3+2+2+4)+5+(20+3))

	// Excluding the global add-ons.
	mug.Meta["_product_addons_exclude_global"] = true
	if n := srv.paoAddonsCount(ctx, mug); n != 6 {
		t.Errorf("add-ons with globals excluded: %d", n)
	}

	// Admin: the groups list, a new group.
	code, page, _ := get(t, client, base+"/wp-admin/goocommerce?page=product-addons")
	if code != http.StatusOK || strings.Contains(page, "Template error") || !strings.Contains(page, "Wrapping") {
		t.Fatalf("add-ons admin: %d", code)
	}
	_, page, _ = get(t, client, base+"/wp-admin/goocommerce?page=product-addons&edit=0")
	code, _, loc := post(t, client, base+"/wp-admin/goocommerce?page=product-addons", url.Values{"_wpnonce": {formNonce(t, page)}, "addon-reference": {"Personalise"},
		"addon-priority": {"20"}, "addon-all-products": {"1"}, "product_addon_name[]": {"Card"}, "product_addon_type[]": {"custom_textarea"},
		"product_addon_adjust_price[]": {"yes"}, "product_addon_price[]": {"1.5"}})
	if code != http.StatusFound || !strings.Contains(loc, "edit=") {
		t.Fatalf("save group: %d %s", code, loc)
	}
	if code, page, _ := get(t, client, base+loc[strings.Index(loc, "/wp-admin"):]); code != http.StatusOK || !strings.Contains(page, "Personalise") || strings.Contains(page, "Template error") {
		t.Errorf("group edit screen: %d", code)
	}
	if gs := srv.paoGroups(ctx); len(gs) != 3 || gs[2].Fields[0].Price != 1.5 || !gs[2].AllProducts {
		t.Errorf("groups after save: %d", len(gs))
	}
}

// A file upload add-on posted with the add-to-cart form itself (multipart, no script) is stored and
// used like an AJAX upload, with the same size and type checks.
func TestGooExtAddonsFormUpload(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetExtension(ctx, paoKey, true); err != nil {
		t.Fatal(err)
	}
	if err := srv.gooSetOpts(ctx, map[string]any{"ext_" + paoKey + "_settings": map[string]any{"max_upload_mb": float64(1), "allowed_types": "png,pdf"}}); err != nil {
		t.Fatal(err)
	}
	poster := gooMakeSimple(t, srv, "Poster", 15, func(p *gooProduct) { p.Virtual = true })
	poster = gxSavePanels(t, srv, poster, paoKey, url.Values{
		"product_addon_name[]": {"Artwork"}, "product_addon_type[]": {"file_upload"}, "product_addon_required[]": {"yes"},
		"product_addon_adjust_price[]": {"yes"}, "product_addon_price_type[]": {"flat_fee"}, "product_addon_price[]": {"2"},
	})
	addons := srv.paoAddonsFor(ctx, poster)
	if len(addons) != 1 {
		t.Fatalf("add-ons: %+v", addons)
	}
	field := paoFieldName(poster.ID, addons[0], 0)
	if out := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", poster)); !strings.Contains(out, `type="file" name="`+field+`"`) {
		t.Errorf("the file input has no name for the plain form post: %s", out)
	}
	post := func(file string, content []byte) (*gin.Context, error) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("add-to-cart", itoa64(poster.ID))
		_ = mw.WriteField("quantity", "1")
		_ = mw.WriteField(field, "") // the hidden URL field the script would have filled
		if file != "" {
			fw, _ := mw.CreateFormFile(field, file)
			_, _ = fw.Write(content)
		}
		_ = mw.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/product/poster/", &body)
		c.Request.Header.Set("Content-Type", mw.FormDataContentType())
		if !srv.gooAcceptsPost(c) {
			t.Fatal("the add-to-cart post is not accepted")
		}
		_, err := srv.gooAddToCartFromForm(c, c.Request.PostForm)
		return c, err
	}
	if _, err := post("", nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("no file: %v", err)
	}
	if _, err := post("shell.php", []byte("<?php")); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("a .php file: %v", err)
	}
	if _, err := post("huge.png", bytes.Repeat([]byte("x"), 1<<20+10)); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("a file over the limit: %v", err)
	}
	c, err := post("my art.png", []byte("\x89PNG\r\n\x1a\nfake"))
	if err != nil {
		t.Fatal(err)
	}
	cart := srv.gooCartFor(c)
	if len(cart.Items) != 1 {
		t.Fatalf("cart lines: %d", len(cart.Items))
	}
	it := cart.Items[0]
	gooAlmost(t, "price with the add-on", it.Price, 17)
	ad := paoItemAddons(it)
	if len(ad) != 1 || !strings.Contains(gxStr(ad[0]["value"]), "/wp-content/uploads/goocommerce_uploads/") || gxStr(ad[0]["display"]) != "my-art.png" {
		t.Fatalf("add-on data: %+v", ad)
	}
	v := gxStr(ad[0]["value"])
	rel := v[strings.Index(v, "goocommerce_uploads/"):]
	if _, err := os.Stat(filepath.Join(srv.cfg.UploadsDir, filepath.FromSlash(rel))); err != nil {
		t.Errorf("the posted file was not stored: %v", err)
	}
}

func paoTestUpload(srv *Server, nonce, ip string) (int, string) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if nonce != "" {
		_ = mw.WriteField("_wpnonce", nonce)
	}
	fw, _ := mw.CreateFormFile("file", "a.png")
	_, _ = fw.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	_ = mw.Close()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/?goo-ajax=addon_upload", &body)
	c.Request.RemoteAddr = ip + ":1234"
	c.Request.Header.Set("Content-Type", mw.FormDataContentType())
	paoFrontRoute(srv, c, "")
	return w.Code, w.Body.String()
}

// The upload endpoint needs the page's nonce, is rate limited per address, and abandoned uploads are
// removed by the daily job while referenced ones stay.
func TestGooExtAddonsUploadHardening(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetExtension(ctx, paoKey, true); err != nil {
		t.Fatal(err)
	}
	if code, _ := paoTestUpload(srv, "", "203.0.113.10"); code != http.StatusForbidden {
		t.Errorf("no nonce: %d", code)
	}
	if code, _ := paoTestUpload(srv, "bogus", "203.0.113.10"); code != http.StatusForbidden {
		t.Errorf("bad nonce: %d", code)
	}
	nonce := srv.nonce(gooTestCtx(), paoUploadNonce)
	for i := 0; i < paoUploadMax; i++ {
		if code, body := paoTestUpload(srv, nonce, "203.0.113.11"); code != http.StatusOK {
			t.Fatalf("upload %d: %d %s", i, code, body)
		}
	}
	if code, _ := paoTestUpload(srv, nonce, "203.0.113.11"); code != http.StatusBadRequest {
		t.Errorf("over the rate limit: %d", code)
	}
	if code, _ := paoTestUpload(srv, nonce, "203.0.113.12"); code != http.StatusOK {
		t.Errorf("another address: %d", code)
	}

	root := filepath.Join(srv.cfg.UploadsDir, "goocommerce_uploads")
	mk := func(dir string, age time.Duration) string {
		d := filepath.Join(root, dir)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "f.png"), []byte("x"), 0o644)
		old := time.Now().Add(-age)
		_ = os.Chtimes(d, old, old)
		return d
	}
	stale, fresh, ordered := mk("staledir", 48*time.Hour), mk("freshdir", time.Hour), mk("ordereddir", 48*time.Hour)
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO goo_orders (status) VALUES ('processing')`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO goo_order_items (order_id, type, name, meta) SELECT max(id), 'line_item', 'x', '{"_goo_addons":[{"value":"http://x/wp-content/uploads/goocommerce_uploads/ordereddir/f.png"}]}'::jsonb FROM goo_orders`); err != nil {
		t.Fatal(err)
	}
	srv.paoCleanupUploads(ctx, 24*time.Hour)
	if _, err := os.Stat(stale); err == nil {
		t.Error("an abandoned upload was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent upload was deleted")
	}
	if _, err := os.Stat(ordered); err != nil {
		t.Error("an ordered upload was deleted")
	}
}

func TestPaoActiveFields(t *testing.T) {
	addons := []paoAddon{
		{Name: "Engrave?", Type: "multiple_choice", Options: []paoOption{{Label: "Yes"}, {Label: "No"}}},
		{Name: "Text", Type: "custom_text", Required: true, ConditionField: "Engrave?", ConditionValue: "yes"},
		{Name: "Font", Type: "custom_text", ConditionField: "Text"}, // chained: needs Text active and filled
		{Name: "Loop A", Type: "custom_text", ConditionField: "Loop B"},
		{Name: "Loop B", Type: "custom_text", ConditionField: "Loop A"},
	}
	f := func(k string, v ...string) map[string][]string {
		return map[string][]string{paoFieldName(1, addons[0], 0): v, k: v}
	}
	_ = f
	form := map[string][]string{paoFieldName(1, addons[0], 0): {"yes-1"}, paoFieldName(1, addons[1], 1): {"Hi"}, paoFieldName(1, addons[2], 2): {"Arial"}}
	got := paoActiveFields(1, addons, form)
	if !got[0] || !got[1] || !got[2] || got[3] || got[4] {
		t.Errorf("yes: %v", got)
	}
	form[paoFieldName(1, addons[0], 0)] = []string{"no-2"}
	if got := paoActiveFields(1, addons, form); !got[0] || got[1] || got[2] {
		t.Errorf("no: %v", got)
	}
	if got := paoActiveFields(1, addons, map[string][]string{}); got[1] || got[2] {
		t.Errorf("nothing chosen: %v", got)
	}
}

func TestPaoStoreForm(t *testing.T) {
	f := paoStoreForm(map[string]any{"extensions": map[string]any{"product_addons": map[string]any{"a": "x", "b": []any{"1", "2"}, "c": nil}}})
	if len(f["a"]) != 1 || f["a"][0] != "x" || len(f["b"]) != 2 || len(f["c"]) != 0 {
		t.Errorf("form: %v", f)
	}
	if paoStoreForm(map[string]any{}) != nil {
		t.Error("no extensions should give nil")
	}
}

// Conditional fields, date limits and the Store API (add-item body, product schema) end to end.
func TestGooExtAddonsConditionalDatesStore(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetExtension(ctx, paoKey, true); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, "Watch", 100, func(p *gooProduct) { p.Virtual = true })
	p = gxSavePanels(t, srv, p, paoKey, url.Values{
		"product_addon_name[]":            {"Engrave", "Message", "Delivery"},
		"product_addon_type[]":            {"multiple_choice", "custom_text", "datepicker"},
		"product_addon_display[]":         {"radiobutton", "select", "select"},
		"product_addon_required[]":        {"yes", "yes", "no"},
		"product_addon_options[]":         {"Yes | 0\nNo | 0", "", ""},
		"product_addon_condition_field[]": {"", "Engrave", ""},
		"product_addon_condition_value[]": {"", "Yes", ""},
		"product_addon_date_min_days[]":   {"", "", "2"},
		"product_addon_date_max[]":        {"", "", "2099-01-01"},
	})
	addons := srv.paoAddonsFor(ctx, p)
	if len(addons) != 3 || addons[1].ConditionField != "Engrave" || addons[2].DateMinDays == nil || *addons[2].DateMinDays != 2 || addons[2].DateMax != "2099-01-01" {
		t.Fatalf("saved: %+v", addons)
	}
	n := func(i int) string { return paoFieldName(p.ID, addons[i], i) }
	html := string(srv.gooHTML(gooTestCtx(), "single_before_add_to_cart", p))
	lo := time.Now().In(srv.loc()).AddDate(0, 0, 2).Format("2006-01-02")
	for _, want := range []string{`data-pao-condition-field="` + n(0) + `"`, `data-pao-condition-value="Yes"`, `min="` + lo + `"`, `max="2099-01-01"`, `data-option-label="Yes"`} {
		if !strings.Contains(html, want) {
			t.Errorf("front markup misses %s", want)
		}
	}
	cart := srv.gooCartFor(gooTestCtx())
	add := func(form map[string][]string) error {
		_, err := cart.Add(ctx, p.ID, 1, 0, nil, form, nil)
		return err
	}
	if err := add(map[string][]string{n(0): {"no-2"}}); err != nil {
		t.Errorf("hidden required field must not be required: %v", err)
	}
	if err := add(map[string][]string{n(0): {"yes-1"}}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("visible required field: %v", err)
	}
	if err := add(map[string][]string{n(0): {"no-2"}, n(1): {"ignored"}}); err != nil {
		t.Fatal(err)
	}
	for _, it := range cart.Items {
		for _, a := range paoItemAddons(it) {
			if gxStr(a["name"]) == "Message" {
				t.Errorf("a hidden field's value was stored: %v", a)
			}
		}
	}
	if err := add(map[string][]string{n(0): {"no-2"}, n(2): {time.Now().In(srv.loc()).Format("2006-01-02")}}); err == nil || !strings.Contains(err.Error(), "earliest date") {
		t.Errorf("a date before the minimum: %v", err)
	}
	if err := add(map[string][]string{n(0): {"no-2"}, n(2): {"2099-06-01"}}); err == nil || !strings.Contains(err.Error(), "latest date") {
		t.Errorf("a date after the maximum: %v", err)
	}
	if err := add(map[string][]string{n(0): {"yes-1"}, n(1): {"Hi"}, n(2): {lo}}); err != nil {
		t.Errorf("valid add-ons: %v", err)
	}

	// Store API: the product schema lists the fields, add-item carries the values.
	ext := srv.paoStoreExtensions(ctx, p)
	fields, _ := ext["product_addons"].([]any)
	if len(fields) != 3 || gxStr(fields[1].(map[string]any)["name"]) != n(1) || fields[1].(map[string]any)["condition"] == nil {
		t.Errorf("store schema: %v", ext)
	}
	req := &restRequest{Body: map[string]any{"id": float64(p.ID), "quantity": float64(1)}}
	c := gooTestCtx()
	if _, _, e := srv.gooStoreAdd(c, req); e == nil {
		t.Error("a required add-on without values was added through the Store API")
	}
	req.Body["extensions"] = map[string]any{"product_addons": map[string]any{n(0): "yes-1", n(1): "Hi"}}
	cart2, key, e := srv.gooStoreAdd(c, req)
	if e != nil {
		t.Fatalf("store add-item with add-ons: %v", e)
	}
	if got := paoItemAddons(cart2.Item(key)); len(got) != 2 {
		t.Errorf("stored add-ons: %v", got)
	}
}

// Field names stay the same when a global group is added or removed after the product page was
// rendered, so a form posted from an open page still maps to the right add-ons.
func TestGooExtAddonsStableFieldNames(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSetExtension(ctx, paoKey, true); err != nil {
		t.Fatal(err)
	}
	p := gooMakeSimple(t, srv, "Cup", 10, func(p *gooProduct) { p.Virtual = true })
	p = gxSavePanels(t, srv, p, paoKey, url.Values{
		"product_addon_name[]": {"Engraving"}, "product_addon_type[]": {"custom_text"}, "product_addon_required[]": {"yes"},
	})
	names := func() map[string]string {
		m := map[string]string{}
		for i, a := range srv.paoAddonsFor(ctx, p) {
			m[a.Name] = paoFieldName(p.ID, a, i)
		}
		return m
	}
	before := names()
	g := &paoGroup{Name: "Wrapping", Priority: 5, AllProducts: true, Fields: []paoAddon{{Name: "Gift note", Type: "custom_text"}}}
	if err := srv.paoSaveGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	after := names()
	if after["Engraving"] != before["Engraving"] {
		t.Errorf("field name changed after adding a global group: %q -> %q", before["Engraving"], after["Engraving"])
	}
	if after["Gift note"] == after["Engraving"] {
		t.Errorf("field names collide: %v", after)
	}
	// A form posted with the pre-change name still fills the engraving field.
	if _, err := srv.gooCartFor(gooTestCtx()).Add(ctx, p.ID, 1, 0, nil, map[string][]string{before["Engraving"]: {"Hi"}}, nil); err != nil {
		t.Errorf("stale-page form rejected: %v", err)
	}
}
