package server

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/auth"
)

// wp2pTestEntry is one file of a test package.
type wp2pTestEntry struct {
	Name string
	Data []byte
}

// wp2pBuildPackage writes a package the way the WordPress plugin does: a tar stream sealed in chunks.
func wp2pBuildPackage(t *testing.T, key wp2pKey, entries []wp2pTestEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := newWP2PWriter(&out, key, [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(w)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.Name, Mode: 0o600, Size: int64(len(e.Data)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func wp2pJSONL(t *testing.T, rows ...any) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, r := range rows {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func TestWP2PKeys(t *testing.T) {
	var k wp2pKey
	for i := range k {
		k[i] = byte(i * 7)
	}
	for _, in := range []string{k.String(), " " + k.String()[:20] + "\n" + k.String()[20:] + " ", strings.Repeat("", 0) + hexKey(k)} {
		got, err := wp2pParseKey(in)
		if err != nil || got != k {
			t.Errorf("parse %q: %v", in, err)
		}
	}
	for _, bad := range []string{"", "short", k.String()[:40], strings.Repeat("z", 64)} {
		if _, err := wp2pParseKey(bad); err == nil {
			t.Errorf("%q must not parse", bad)
		}
	}
	// The token key is HMAC-SHA256(token, label): the plugin computes the same with hash_hmac().
	if got := wp2pTokenKey("pmt_test").String(); got != "J9XMgRgB2wNg1VRI5uGWA7vSpeLC33oZ6E92ALzbKPo" {
		t.Errorf("token key = %s", got)
	}
}

func hexKey(k wp2pKey) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for _, b := range k {
		out = append(out, digits[b>>4], digits[b&15])
	}
	return string(out)
}

// The envelope: a round trip over several chunks; a wrong key, a flipped bit, a cut-off stream,
// dropped or swapped chunks and data after the end all fail.
func TestWP2PEnvelope(t *testing.T) {
	key := wp2pTokenKey("pmt_envelope")
	plain := bytes.Repeat([]byte("0123456789abcdef"), (wp2pMaxChunk*2)/16+1000) // three chunks
	var sealed bytes.Buffer
	w, err := newWP2PWriter(&sealed, key, [16]byte{9})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(plain); i += 1 << 20 {
		_, _ = w.Write(plain[i:min(len(plain), i+1<<20)])
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	read := func(data []byte, k wp2pKey) ([]byte, error) {
		r, err := newWP2PReader(bytes.NewReader(data), k)
		if err != nil {
			return nil, err
		}
		return io.ReadAll(r)
	}
	got, err := read(sealed.Bytes(), key)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip: %v (len %d of %d)", err, len(got), len(plain))
	}
	if _, err := read(sealed.Bytes(), wp2pTokenKey("pmt_other")); !errors.Is(err, errWP2PKey) {
		t.Errorf("wrong key: %v", err)
	}
	flipped := bytes.Clone(sealed.Bytes())
	flipped[len(flipped)/2] ^= 1
	if _, err := read(flipped, key); err == nil {
		t.Error("a flipped bit was accepted")
	}
	// Chunk boundaries: header, then [len][sealed]...
	var offs []int
	data := sealed.Bytes()
	for p := wp2pHeaderLen; p < len(data); {
		offs = append(offs, p)
		p += 4 + int(uint32(data[p])<<24|uint32(data[p+1])<<16|uint32(data[p+2])<<8|uint32(data[p+3]))
	}
	if len(offs) != 3 {
		t.Fatalf("%d chunks, want 3", len(offs))
	}
	// Every chunk carries its own random nonce (format 1's counter nonce repeated when the plugin
	// sealed a chunk again after a request died).
	nonces := map[string]bool{}
	for _, o := range offs {
		nonces[string(data[o+4:o+28])] = true
	}
	if len(nonces) != len(offs) {
		t.Errorf("%d distinct nonces for %d chunks", len(nonces), len(offs))
	}
	if _, err := read([]byte("PURSAEWP\x01"+strings.Repeat("x", 16)), key); err == nil || !strings.Contains(err.Error(), "format 1") {
		t.Errorf("format 1 (counter nonces): %v", err)
	}
	if _, err := read(data[:offs[2]], key); err == nil {
		t.Error("a stream without its last chunk was accepted")
	}
	dropped := append(bytes.Clone(data[:offs[1]]), data[offs[2]:]...)
	if _, err := read(dropped, key); err == nil {
		t.Error("a dropped chunk was accepted")
	}
	swapped := append(append(bytes.Clone(data[:offs[0]]), data[offs[1]:offs[2]]...), data[offs[0]:offs[1]]...)
	swapped = append(swapped, data[offs[2]:]...)
	if _, err := read(swapped, key); err == nil {
		t.Error("swapped chunks were accepted")
	}
	if _, err := read(append(bytes.Clone(data), 0), key); err == nil {
		t.Error("data after the last chunk was accepted")
	}
	if _, err := read([]byte("PURSAEWP\x03"+strings.Repeat("x", 16)), key); err == nil || !strings.Contains(err.Error(), "format 3") {
		t.Errorf("newer format: %v", err)
	}
	if _, err := read([]byte("not a package at all......"), key); err == nil {
		t.Error("not a package")
	}
}

// Extraction writes only the known entries, inside the work directory.
func TestWP2PExtract(t *testing.T) {
	key := wp2pTokenKey("pmt_extract")
	pkg := wp2pBuildPackage(t, key, []wp2pTestEntry{
		{"manifest.json", []byte(`{"format":1,"home_url":"https://Old.Example/","counts":{"users":1}}`)},
		{"users.jsonl", []byte(`{"id":1}` + "\n")},
		{"../evil.txt", []byte("x")},
		{"media/../../evil", []byte("x")},
		{"media/12", []byte("png")},
		{"woo/orders.jsonl", []byte("{}\n")},
		{"woo/other.jsonl", []byte("{}\n")},
		{"users.000002.jsonl", []byte(`{"id":3}` + "\n")},
		{"users.000001.jsonl", []byte(`{"id":2}`)}, // no final newline
		{"users.1.jsonl", []byte("{}")},
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "p.wp2p")
	if err := os.WriteFile(path, pkg, 0o600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	man, ignored, err := wp2pExtract(path, key, work)
	if err != nil {
		t.Fatal(err)
	}
	if man.HomeURL != "https://Old.Example/" || man.Counts["users"] != 1 || wp2pSource(man) != "old.example" {
		t.Errorf("manifest: %+v", man)
	}
	if !slices.Equal(ignored, []string{"../evil.txt", "media/../../evil", "woo/other.jsonl", "users.1.jsonl"}) {
		t.Errorf("ignored: %v", ignored)
	}
	for _, f := range []string{"users.jsonl", "media/12", "woo/orders.jsonl"} {
		if _, err := os.Stat(filepath.Join(work, f)); err != nil {
			t.Errorf("%s not extracted: %v", f, err)
		}
	}
	var ids []string
	_ = wp2pEachLine(context.Background(), filepath.Join(work, "users.jsonl"), func(raw []byte) error {
		ids = append(ids, strings.TrimSpace(string(raw)))
		return nil
	})
	if !slices.Equal(ids, []string{`{"id":1}`, `{"id":2}`, `{"id":3}`}) {
		t.Errorf("parts read as %v", ids)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
		t.Error("an entry escaped the work directory")
	}
	if _, _, err := wp2pExtract(path, wp2pTokenKey("pmt_wrong"), filepath.Join(dir, "w2")); !errors.Is(err, errWP2PKey) {
		t.Errorf("wrong key: %v", err)
	}
}

func TestWP2POptionValues(t *testing.T) {
	cases := []struct {
		in, def, want any
	}{
		{"yes", false, true}, {"no", false, false}, {"1", false, true}, {"12", float64(10), float64(12)}, {"x", float64(10), nil},
		{"EUR", "USD", "EUR"}, {[]any{"DE", "FR"}, []string{}, []any{"DE", "FR"}}, {"", []string{}, []any{}},
	}
	for _, c := range cases {
		got, ok := wp2pOptionValue(c.in, c.def)
		if c.want == nil {
			if ok {
				t.Errorf("%v as %T: %v, want refused", c.in, c.def, got)
			}
			continue
		}
		gj, _ := json.Marshal(got)
		wj, _ := json.Marshal(c.want)
		if !ok || string(gj) != string(wj) {
			t.Errorf("%v as %T: %v (%v), want %v", c.in, c.def, got, ok, c.want)
		}
	}
	if st, ok := wp2pOrderStatus("wc-completed"); st != "completed" || !ok {
		t.Errorf("status: %s", st)
	}
	if st, ok := wp2pOrderStatus("wc-shipped"); st != "on-hold" || ok {
		t.Errorf("custom status: %s", st)
	}
}

// A whole site: users keep their WordPress passwords, content and media come from the package, the
// store's settings, tax rates, coupons, orders (items, taxes, refunds, notes) and customers map to
// Pursae Commerce; importing the package again adds nothing.
func TestWP2PImportSite(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	old := wxrFetchTransport
	wxrFetchTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("offline") })
	t.Cleanup(func() { wxrFetchTransport = old })

	wxr, err := os.ReadFile("testdata/wxr-woocommerce.xml")
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)

	const wpPass = "password" // the hashes below are WordPress' for this password
	created := time.Date(2024, 3, 3, 9, 12, 0, 0, time.UTC).Unix()
	users := wp2pJSONL(t,
		map[string]any{"id": 1, "login": "shopadmin", "email": "owner@shop.example.test", "pass": "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.", "nicename": "shopadmin",
			"display_name": "Shop Admin", "registered": "2020-05-01 10:00:00", "roles": []string{"administrator"}, "meta": map[string]string{"first_name": "Shop", "locale": "de_DE"}},
		map[string]any{"id": 2, "login": "jane", "email": "jane@example.test", "pass": "$wp$2y$05$u//MIaiTMd889nQrv4tRLe6SFvcZuLdJ3PrPDBY/vdbwr4JX/Ye9u",
			"display_name": "Jane Doe", "registered": "2023-01-02 03:04:05", "roles": []string{"customer", "made_up_role"},
			"meta": map[string]string{"first_name": "Jane", "last_name": "Doe", "billing_city": "Berlin", "billing_country": "DE", "session_tokens": "x"}},
		map[string]any{"id": 3, "login": "admin", "email": "someone@else.test", "pass": "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.", "roles": []string{"administrator"}},
		map[string]any{"id": 4, "login": "bad login!", "email": "bad@example.test", "pass": "x", "roles": []string{"subscriber"}},
		map[string]any{"id": 5, "login": "nohash", "email": "nohash@example.test", "pass": "plain-text?", "roles": []string{"subscriber"}},
		// The site's admin under another login: the same e-mail address is the same person.
		map[string]any{"id": 6, "login": "boss", "email": "Admin@Example.test", "pass": "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.", "roles": []string{"customer"}},
	)
	options, _ := json.Marshal(map[string]any{
		"options": map[string]any{"blogname": "Demo Shop", "blogdescription": "Moved", "posts_per_page": "7", "woocommerce_currency": "EUR",
			"woocommerce_calc_taxes": "yes", "woocommerce_stripe_settings": map[string]any{"secret": "s"}, "woocommerce_shop_page_id": "999", "admin_email": "evil@example.test"},
		"theme": "storefront", "active_plugins": []string{"woocommerce/woocommerce.php"},
	})
	lineTax := map[string]any{"total": map[string]any{"7": "6.84"}, "subtotal": map[string]any{"7": "7.6"}}
	orders := wp2pJSONL(t,
		map[string]any{"id": 50, "status": "wc-completed", "currency": "EUR", "customer_id": 2, "order_key": "wc_order_abc",
			"billing":        map[string]string{"first_name": "Jane", "email": "jane@example.test", "city": "Berlin", "country": "DE"},
			"shipping":       map[string]string{"first_name": "Jane", "city": "Berlin"},
			"payment_method": "bacs", "payment_method_title": "Direct bank transfer", "discount_total": "4", "shipping_total": "5", "cart_tax": "6.84",
			"total": "92.84", "total_tax": "6.84", "date_created": created, "date_paid": created + 60, "date_completed": created + 3600,
			"stock_reduced": true, "recorded_sales": true, "meta": map[string]any{"_custom_field": "kept"},
			"items": []any{
				map[string]any{"id": 101, "type": "line_item", "name": "Classic Tee", "meta": map[string]any{"_product_id": "40", "_variation_id": "0", "_qty": "2",
					"_line_subtotal": "40", "_line_subtotal_tax": "7.6", "_line_total": "36", "_line_tax": "6.84", "_line_tax_data": lineTax, "_reduced_stock": "2", "Size": "M"}},
				map[string]any{"id": 102, "type": "line_item", "name": "Hoodie - Blue", "meta": map[string]any{"_product_id": "41", "_variation_id": "43", "_qty": "1",
					"_line_subtotal": "50", "_line_total": "50", "pa_color": "blue"}},
				map[string]any{"id": 103, "type": "shipping", "name": "Flat rate", "meta": map[string]any{"method_id": "flat_rate", "instance_id": "1", "cost": "5", "total_tax": "0"}},
				map[string]any{"id": 104, "type": "coupon", "name": "summer10", "meta": map[string]any{"discount_amount": "4", "discount_amount_tax": "0"}},
				map[string]any{"id": 105, "type": "tax", "name": "DE-MWST-1", "meta": map[string]any{"rate_id": "7", "label": "MwSt", "compound": "", "tax_amount": "6.84", "shipping_tax_amount": "0", "rate_percent": "19"}},
			},
			"notes": []any{map[string]any{"content": "Order status changed from Processing to Completed.", "customer_note": false, "added_by": "system", "date": created + 3600}},
			"refunds": []any{map[string]any{"id": 52, "amount": "10", "reason": "Damaged", "refunded_by": 1, "date_created": created + 7200,
				"items": []any{map[string]any{"id": 106, "type": "line_item", "name": "Classic Tee", "meta": map[string]any{"_product_id": "40", "_qty": "-1",
					"_line_subtotal": "-10", "_line_total": "-10", "_refunded_item_id": "101"}}}}},
		},
		map[string]any{"id": 53, "status": "wc-processing", "currency": "EUR", "billing": map[string]string{"email": "guest@example.test"}, "total": "12",
			"date_created": created, "items": []any{map[string]any{"id": 107, "type": "line_item", "name": "Deleted product", "meta": map[string]any{"_product_id": "999", "_qty": "1", "_line_total": "12"}}}},
		map[string]any{"id": 54, "status": "wc-shipped", "currency": "EUR", "total": "1", "date_created": created},
	)
	coupons := wp2pJSONL(t, map[string]any{"id": 51, "code": "Summer10", "status": "publish", "discount_type": "percent", "amount": "10", "product_ids": []int64{40, 999},
		"product_categories": []int64{16}, "minimum_amount": "20", "maximum_amount": "", "usage_limit": "100", "usage_limit_per_user": "", "usage_count": 1,
		"used_by": []string{"2"}, "date_expires": created + 86400*365, "date_created": created - 86400, "email_restrictions": []string{"*@example.test"}})
	// Two rates that differ only in their postcodes are two rates.
	taxRates := wp2pJSONL(t, map[string]any{"id": 7, "country": "DE", "state": "", "rate": "19.0000", "name": "MwSt", "priority": 1, "compound": false, "shipping": true, "order": 0, "class": ""},
		map[string]any{"id": 8, "country": "DE", "state": "", "rate": "19.0000", "name": "MwSt", "priority": 1, "compound": false, "shipping": true, "order": 1, "class": "", "postcodes": []string{"27498"}})
	customers := wp2pJSONL(t,
		map[string]any{"user_id": 2, "email": "jane@example.test", "billing": map[string]string{"city": "Berlin", "postcode": "10115"}, "shipping": map[string]string{"city": "Berlin"}, "is_paying_customer": true},
		map[string]any{"user_id": 0, "email": "guest@example.test"},
		// The merged admin: an address it has stays, empty fields are filled in.
		map[string]any{"user_id": 6, "email": "admin@example.test", "billing": map[string]string{"city": "Berlin", "postcode": "10115"}})
	media := wp2pJSONL(t, map[string]any{"id": 30, "name": "tee.png"}, map[string]any{"id": 31, "name": "hoodie.png"}, map[string]any{"id": 32, "name": "hoodie-back.png"})
	manifest := []byte(`{"format":1,"generator":"wp-to-pursae/test","site_url":"https://shop.example.test","home_url":"https://shop.example.test","order_storage":"hpos",
		"skipped":[{"kind":"media","item":"huge.mp4","reason":"larger than the size limit"}],"counts":{"users":5}}`)
	key := wp2pTokenKey("pmt_site")
	pkg := wp2pBuildPackage(t, key, []wp2pTestEntry{
		// Batches come as numbered parts; content.xml is cut in the middle of a tag on purpose.
		{"manifest.json", manifest}, {"users.jsonl", users}, {"options.json", options}, {"content.000001.xml", wxr[:len(wxr)/2+3]}, {"content.000002.xml", wxr[len(wxr)/2+3:]},
		{"media.jsonl", media}, {"media/30", pngBuf.Bytes()}, {"media/31", pngBuf.Bytes()}, {"media/32", pngBuf.Bytes()},
		{"woo/tax_rates.jsonl", taxRates}, {"woo/coupons.jsonl", coupons}, {"woo/orders.000001.jsonl", orders[:bytes.IndexByte(orders, '\n')+1]}, {"woo/orders.000002.jsonl", orders[bytes.IndexByte(orders, '\n')+1:]}, {"woo/customers.jsonl", customers},
	})

	importPkg := func() *wp2pRun {
		t.Helper()
		if err := os.MkdirAll(srv.wp2pImportDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		id := wp2pNewID()
		if err := os.WriteFile(srv.wp2pPackagePath(id), pkg, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, package_key, created_by, state) VALUES ($1, $2, 1, 'queued')`, id, key[:]); err != nil {
			t.Fatal(err)
		}
		if err := srv.wp2pImportJob(ctx, map[string]any{"id": id}); err != nil {
			t.Fatal(err)
		}
		var state, msg string
		_ = srv.store.Pool.QueryRow(ctx, `SELECT state, error FROM wp2p_migrations WHERE id = $1`, id).Scan(&state, &msg)
		if state != "done" {
			t.Fatalf("import %s: %s", state, msg)
		}
		r, err := srv.wp2pLoadRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(srv.wp2pPackagePath(id)); err == nil {
			t.Error("the uploaded package was not removed")
		}
		if _, err := os.Stat(srv.wp2pWorkDir(id)); err == nil {
			t.Error("the extracted files were not removed")
		}
		var keyLeft []byte
		_ = srv.store.Pool.QueryRow(ctx, `SELECT package_key FROM wp2p_migrations WHERE id = $1`, id).Scan(&keyLeft)
		if keyLeft != nil {
			t.Error("the package key was kept after the import")
		}
		return r
	}
	siteAdmin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	if err := srv.gooSetUserMeta(ctx, siteAdmin.ID, map[string]any{"billing_city": "Hamburg"}); err != nil {
		t.Fatal(err)
	}
	r := importPkg()
	rep := r.rep
	for k, want := range map[string]int{"users": 4, "users_renamed": 1, "users_merged_by_email": 1, "orders": 3, "refunds": 1, "coupons": 1, "tax_rates": 2,
		"customers": 2, "guest_customers": 1, "media": 3, "products": 6, "variations": 2, "users_password_reset": 1} {
		if rep.Counts[k] != want {
			t.Errorf("count %s = %d, want %d (%v)", k, rep.Counts[k], want, rep.Counts)
		}
	}
	if rep.Existing["users"] != 1 {
		t.Errorf("existing users = %d", rep.Existing["users"])
	}
	skipped := func(kind, item string) bool {
		for _, s := range rep.Skipped {
			if s.Kind == kind && s.Item == item {
				return true
			}
		}
		return false
	}
	if !skipped("user", "#4") || !skipped("media", "huge.mp4") || len(rep.Skipped) != 2 {
		t.Errorf("skipped: %+v", rep.Skipped)
	}
	// The report names users by their WordPress id: no e-mail address or login of anyone is kept.
	var stored string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT report::text FROM wp2p_migrations WHERE id = $1`, r.id).Scan(&stored)
	for _, pii := range []string{"@example.test", "@else.test", "bad login", "nohash", `"admin"`, "jane"} {
		if strings.Contains(stored, pii) {
			t.Errorf("the report holds %q: %s", pii, stored)
		}
	}
	if rep.Unmapped["order lines of products that were not imported (kept by name)"] != 1 || rep.Unmapped["coupon products that were not imported"] != 1 ||
		rep.Unmapped["orders with a status Pursae Commerce does not have (imported as on-hold)"] != 1 {
		t.Errorf("unmapped: %v", rep.Unmapped)
	}

	// Users sign in with their WordPress passwords (and are moved to argon2id on the way).
	for _, login := range []string{"shopadmin", "jane"} {
		u, err := srv.store.UserByLoginOrEmail(ctx, login)
		if err != nil {
			t.Fatalf("%s: %v", login, err)
		}
		if ok, _, _ := auth.CheckUserPassword(ctx, wpPass, u.PasswordHash); !ok {
			t.Errorf("%s: the WordPress password does not verify (%s)", login, auth.HashKind(u.PasswordHash))
		}
	}
	other := newClient()
	if code, _, _ := post(t, other, ts.URL+"/wp-login", url.Values{"log": {"jane"}, "pwd": {wpPass}}); code != http.StatusFound {
		t.Errorf("jane signs in: %d", code)
	}
	jane, _ := srv.store.UserByLoginOrEmail(ctx, "jane")
	if jane.PrimaryRole() != "customer" || jane.Profile["first_name"] != "Jane" || jane.Meta["billing_city"] != "Berlin" || jane.Meta["billing_postcode"] != "10115" ||
		jane.RegisteredAt.Year() != 2023 || jane.Meta["session_tokens"] != nil {
		t.Errorf("jane: roles %v profile %v meta %v registered %v", jane.Roles, jane.Profile, jane.Meta, jane.RegisteredAt)
	}
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	if ok, _, _ := auth.CheckUserPassword(ctx, "correct horse", admin.PasswordHash); !ok || admin.Email == "someone@else.test" {
		t.Error("the existing admin was changed by the import")
	}
	// WordPress' "admin" is someone else (another e-mail address): a new account, not the site's admin.
	admin2, err := srv.store.UserByLoginOrEmail(ctx, "admin-2")
	if err != nil || admin2.Email != "someone@else.test" || admin2.ID == admin.ID {
		t.Fatalf("WordPress' admin: %+v %v", admin2, err)
	}
	if ok, _, _ := auth.CheckUserPassword(ctx, wpPass, admin2.PasswordHash); !ok {
		t.Error("the renamed user's WordPress password does not verify")
	}
	// "boss" has the admin's e-mail address: merged into the admin, whose address is kept and completed.
	var bossID int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT new_id FROM wp2p_map WHERE kind = 'user' AND old_id = 6`).Scan(&bossID)
	if bossID != admin.ID || admin.Meta["billing_city"] != "Hamburg" || admin.Meta["billing_postcode"] != "10115" || admin.PrimaryRole() != "administrator" {
		t.Errorf("merged admin: map %d (admin %d), meta %v, roles %v", bossID, admin.ID, admin.Meta, admin.Roles)
	}
	shopadmin, _ := srv.store.UserByLoginOrEmail(ctx, "shopadmin")
	if shopadmin.PrimaryRole() != "administrator" || shopadmin.Locale != "de_DE" {
		t.Errorf("shopadmin: %v %q", shopadmin.Roles, shopadmin.Locale)
	}

	// Settings: core and store settings; nothing outside the lists.
	if srv.settings.String("blogname") != "Demo Shop" || srv.settings.Int("posts_per_page") != 7 || srv.gooOptString("currency") != "EUR" || !srv.gooOptBool("calc_taxes") {
		t.Errorf("settings: %q %d %q %v", srv.settings.String("blogname"), srv.settings.Int("posts_per_page"), srv.gooOptString("currency"), srv.gooOptBool("calc_taxes"))
	}
	if srv.settings.String("admin_email") == "evil@example.test" || srv.settings.Get(gooOptPrefix+"stripe_settings") != nil {
		t.Error("an option outside the lists was imported")
	}

	// Content: products by the WXR importer, media from the package (not downloaded).
	tee, err := srv.store.PostBySlug(ctx, "product", "classic-tee")
	if err != nil {
		t.Fatal(err)
	}
	var teeImg int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE title = 'tee'`).Scan(&teeImg)
	if teeImg == 0 || tee.FeaturedMediaID != teeImg {
		t.Errorf("tee image %d / featured %d", teeImg, tee.FeaturedMediaID)
	}
	if tee.AuthorID != shopadmin.ID {
		t.Errorf("product author %d, want shopadmin %d", tee.AuthorID, shopadmin.ID)
	}
	hoodie, _ := srv.store.PostBySlug(ctx, "product", "hoodie")
	blue, _ := srv.store.PostBySlug(ctx, "product_variation", "hoodie-blue")

	// Tax rate, coupon, orders.
	var rateID int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT id FROM goo_tax_rates WHERE country = 'DE' AND name = 'MwSt' AND postcodes = '{}'`).Scan(&rateID); err != nil {
		t.Fatal(err)
	}
	var islandRate int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_tax_rates WHERE country = 'DE' AND postcodes = '{27498}'`).Scan(&islandRate)
	if islandRate != 1 {
		t.Errorf("the rate that differs by postcode: %d", islandRate)
	}
	cp, err := srv.gooCouponByCode(ctx, "summer10")
	if err != nil {
		t.Fatal(err)
	}
	clothing, _ := srv.store.TermBySlug(ctx, "product_cat", "clothing")
	if cp.DiscountType != "percent" || cp.Amount != 10 || !slices.Equal(cp.ProductIDs, []int64{tee.ID}) || !slices.Equal(cp.ProductCategories, []int64{clothing.ID}) ||
		cp.UsageCount != 1 || !slices.Equal(cp.UsedBy, []string{strconv.FormatInt(jane.ID, 10)}) || cp.UsageLimit == nil || *cp.UsageLimit != 100 ||
		cp.UsageLimitPerUser != nil || cp.MinimumAmount == nil || *cp.MinimumAmount != 20 || cp.MaximumAmount != nil || cp.DateExpires == nil ||
		cp.DateCreated.Unix() != created-86400 || !slices.Equal(cp.EmailRestrictions, []string{"*@example.test"}) {
		t.Errorf("coupon: %+v", cp)
	}
	var orderID int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT new_id FROM wp2p_map WHERE kind = 'order' AND old_id = 50`).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	o, err := srv.gooGetOrder(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "completed" || o.Total != 92.84 || o.CartTax != 6.84 || o.CustomerID != jane.ID || o.OrderKey != "wc_order_abc" || o.Currency != "EUR" ||
		o.DateCreated.Unix() != created || o.DatePaid == nil || o.DatePaid.Unix() != created+60 || !o.StockReduced || o.PaymentMethodTitle != "Direct bank transfer" ||
		o.Billing["city"] != "Berlin" || o.Meta["_custom_field"] != "kept" || o.Meta["_recorded_sales"] != true || o.Meta["_wp2p_order_id"] != float64(50) {
		t.Errorf("order: %+v", o)
	}
	// The order keeps its WordPress number everywhere it is shown, and new orders are numbered after it.
	if o.Number() != "50" {
		t.Errorf("order number %q, want 50", o.Number())
	}
	if found, _, err := srv.gooQueryOrders(ctx, gooOrderQuery{Search: "#50"}); err != nil || len(found) != 1 || found[0].ID != o.ID {
		t.Errorf("searching #50: %v %v", found, err)
	}
	var nextID int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('goo_orders', 'id'))`).Scan(&nextID)
	if nextID <= 54 {
		t.Errorf("the next order id %d is not after the imported numbers", nextID)
	}
	if len(o.Items) != 5 {
		t.Fatalf("items: %d", len(o.Items))
	}
	li, li2, ship, coup, tax := o.Items[0], o.Items[1], o.Items[2], o.Items[3], o.Items[4]
	rk := strconv.FormatInt(rateID, 10)
	if li.ProductID != tee.ID || li.Quantity != 2 || li.Subtotal != 40 || li.Total != 36 || li.TotalTax != 6.84 || li.Taxes.Total[rk] != 6.84 ||
		li.Taxes.Subtotal[rk] != 7.6 || li.Meta["Size"] != "M" || li.Meta["_reduced_stock"] != float64(2) || li.Meta["_product_id"] != nil {
		t.Errorf("line item: %+v", li)
	}
	if li2.ProductID != hoodie.ID || li2.VariationID != blue.ID || li2.Meta["pa_color"] != "blue" {
		t.Errorf("variation line: %+v", li2)
	}
	if ship.Type != "shipping" || ship.Total != 5 || ship.Meta["method_id"] != "flat_rate" {
		t.Errorf("shipping: %+v", ship)
	}
	if coup.Type != "coupon" || coup.Total != 4 || coup.Meta["coupon_id"] != float64(cp.ID) {
		t.Errorf("coupon line: %+v", coup)
	}
	if tax.Type != "tax" || tax.Meta["rate_id"] != float64(rateID) || tax.Meta["tax_amount"] != 6.84 || tax.Meta["compound"] != false || tax.Name != "DE-MWST-1" {
		t.Errorf("tax line: %+v", tax)
	}
	refunds, _ := srv.gooRefunds(ctx, o.ID)
	if len(refunds) != 1 || refunds[0].Total != -10 || refunds[0].RefundReason != "Damaged" || refunds[0].RefundedBy != shopadmin.ID ||
		len(refunds[0].Items) != 1 || refunds[0].Items[0].Meta["_refunded_item_id"] != float64(li.ID) || refunds[0].DateCreated.Unix() != created+7200 {
		t.Errorf("refunds: %+v", refunds)
	}
	if got := srv.gooTotalRefunded(ctx, o.ID); got != 10 {
		t.Errorf("refunded total %v", got)
	}
	notes, _ := srv.gooOrderNotes(ctx, o.ID, false)
	if len(notes) != 1 || notes[0].AddedBy != "" || notes[0].CreatedAt.Unix() != created+3600 {
		t.Errorf("notes: %+v", notes)
	}
	var guestStatus, customStatus, orig string
	var guestCustomer int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT o.status, o.customer_id FROM goo_orders o JOIN wp2p_map m ON m.new_id = o.id AND m.kind = 'order' WHERE m.old_id = 53`).Scan(&guestStatus, &guestCustomer)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT o.status, o.meta->>'_wp2p_original_status' FROM goo_orders o JOIN wp2p_map m ON m.new_id = o.id AND m.kind = 'order' WHERE m.old_id = 54`).Scan(&customStatus, &orig)
	if guestStatus != "processing" || guestCustomer != 0 || customStatus != "on-hold" || orig != "wc-shipped" {
		t.Errorf("guest order %s/%d, custom status %s (%s)", guestStatus, guestCustomer, customStatus, orig)
	}

	// Again: nothing is added twice.
	count := func(q string) int {
		var n int
		if err := srv.store.Pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	queries := []string{`SELECT count(*) FROM goo_orders`, `SELECT count(*) FROM goo_order_items`, `SELECT count(*) FROM goo_coupons`, `SELECT count(*) FROM goo_tax_rates`,
		`SELECT count(*) FROM users`, `SELECT count(*) FROM posts`, `SELECT count(*) FROM media`, `SELECT count(*) FROM goo_order_notes`}
	before := make([]int, len(queries))
	for i, q := range queries {
		before[i] = count(q)
	}
	r2 := importPkg()
	for i, q := range queries {
		if n := count(q); n != before[i] {
			t.Errorf("%s: %d after the second import, %d before", q, n, before[i])
		}
	}
	if r2.rep.Existing["orders"] != 3 || r2.rep.Counts["orders"] != 0 || r2.rep.Existing["users"] != 5 || r2.rep.Existing["coupons"] != 1 {
		t.Errorf("second run: counts %v existing %v", r2.rep.Counts, r2.rep.Existing)
	}
}

// The push mode over REST: a token from the admin, start, chunks (one resent after a "lost"
// answer), finish; the token works only for its import and only once.
func TestWP2PPushREST(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	token, id, _, err := srv.wp2pNewToken(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	key := wp2pTokenKey(token)
	pkg := wp2pBuildPackage(t, key, []wp2pTestEntry{
		{"manifest.json", []byte(`{"format":1,"home_url":"https://push.example.test"}`)},
		{"users.jsonl", wp2pJSONL(t, map[string]any{"id": 9, "login": "pushed", "email": "pushed@example.test", "pass": "$P$B2quFTY/dB/kUQqA.Jj1pGh9WakUwP.", "roles": []string{"editor"}})},
	})
	do := func(method, path, tok string, body io.Reader, ctype string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+"/wp-json"+path, body)
		if tok != "" {
			req.Header.Set(wp2pTokenHeader, tok)
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	sum := sha256Hex(pkg)
	start := func(tok string) (int, map[string]any) {
		return do("POST", "/pursae/v1/migrate/start", tok, strings.NewReader(`{"size":`+strconv.Itoa(len(pkg))+`,"sha256":"`+sum+`"}`), "application/json")
	}
	if code, _ := start("pmt_wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := start(""); code != http.StatusUnauthorized {
		t.Errorf("no auth: %d", code)
	}
	code, out := start(token)
	if code != http.StatusOK || out["id"] != id {
		t.Fatalf("start: %d %v", code, out)
	}
	half := len(pkg) / 2
	chunk := func(off, end int, tok string) (int, map[string]any) {
		return do("PUT", "/pursae/v1/migrate/"+id+"/chunk?offset="+strconv.Itoa(off), tok, bytes.NewReader(pkg[off:end]), "application/octet-stream")
	}
	if code, out := chunk(0, half, token); code != http.StatusOK || out["received"] != float64(half) {
		t.Fatalf("chunk 1: %d %v", code, out)
	}
	// The answer to the first chunk got lost: the plugin asks and resends from what was received.
	if code, out := do("GET", "/pursae/v1/migrate/"+id, token, nil, ""); code != http.StatusOK || out["received"] != float64(half) || out["state"] != "receiving" {
		t.Fatalf("status: %d %v", code, out)
	}
	if code, _ := chunk(half+10, len(pkg), token); code != http.StatusConflict {
		t.Errorf("a gap was accepted: %d", code)
	}
	if code, _ := chunk(10, len(pkg), "pmt_wrong"); code != http.StatusUnauthorized {
		t.Errorf("chunk with a wrong token: %d", code)
	}
	if code, _ := chunk(half-5, len(pkg), token); code != http.StatusOK {
		t.Fatalf("resend: %d", code)
	}
	if code, out := do("POST", "/pursae/v1/migrate/"+id+"/finish", token, strings.NewReader("{}"), "application/json"); code != http.StatusAccepted || out["state"] != "queued" {
		t.Fatalf("finish: %d %v", code, out)
	}
	if code, _ := start(token); code != http.StatusConflict {
		t.Errorf("the token was usable twice: %d", code)
	}
	if err := srv.wp2pImportJob(ctx, map[string]any{"id": id}); err != nil {
		t.Fatal(err)
	}
	if code, out := do("GET", "/pursae/v1/migrate/"+id, token, nil, ""); code != http.StatusOK || out["state"] != "done" {
		t.Fatalf("after the import: %d %v", code, out)
	} else if rep, _ := out["report"].(map[string]any); rep == nil || rep["skipped"] != nil || rep["content"] != nil {
		t.Errorf("the status report carries item lists: %v", out["report"])
	}
	u, err := srv.store.UserByLoginOrEmail(ctx, "pushed")
	if err != nil || u.PrimaryRole() != "editor" {
		t.Fatalf("pushed user: %v %v", u, err)
	}
	if ok, _, _ := auth.CheckUserPassword(ctx, "password", u.PasswordHash); !ok {
		t.Error("the pushed user's password does not verify")
	}
	// The admin screen shows the import and its report.
	_, body, _ := get(t, c, ts.URL+"/wp-admin/import-wordpress?run="+id)
	if !strings.Contains(body, "All done.") || !strings.Contains(body, "push.example.test") {
		t.Errorf("admin report: %s", body)
	}
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// A package the WordPress plugin built (integrations/wordpress/test: WordPress, WooCommerce with HPOS):
// the PHP writer and this reader agree on the format, and the store arrives as WordPress had it.
func TestWP2PFixtureFromPlugin(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	key, err := wp2pParseKey("oT_EIQ1H0rTK3wMWwHbctyxQKAAkq9JXkPtZGNKeSXY")
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	id := wp2pNewID()
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, created_by, state) VALUES ($1, $2, 'running')`, id, admin.ID); err != nil {
		t.Fatal(err)
	}
	r := &wp2pRun{s: srv, id: id, dir: filepath.Join(t.TempDir(), "work"), pkg: "testdata/wp2p/wordpress-woocommerce.wp2p", keepPackage: true, key: &key, u: admin}
	if err := r.run(ctx); err != nil {
		t.Fatal(err)
	}
	if r.man.OrderStorage != "hpos" || !strings.HasPrefix(r.man.Generator, "wp-to-pursae/") {
		t.Errorf("manifest: %+v", r.man)
	}
	for k, want := range map[string]int{"users": 5, "orders": 3, "refunds": 1, "coupons": 2, "products": 3, "variations": 2, "tax_rates": 1, "menu_items": 2} {
		if r.rep.Counts[k] != want {
			t.Errorf("%s = %d, want %d (%v)", k, r.rep.Counts[k], want, r.rep.Counts)
		}
	}
	for login, pw := range map[string]string{"wpadmin": "Adm1n-pass-2026", "editor1": "Ed1tor-pass-2026", "customer1": "Cust0mer-1-pass"} {
		u, err := srv.store.UserByLoginOrEmail(ctx, login)
		if err != nil {
			t.Fatalf("%s: %v", login, err)
		}
		if ok, _, _ := auth.CheckUserPassword(ctx, pw, u.PasswordHash); !ok {
			t.Errorf("%s: the WordPress password does not verify (%s)", login, auth.HashKind(u.PasswordHash))
		}
	}
	rows, err := srv.store.Pool.Query(ctx, `SELECT status, total::float8, discount_total::float8,
		coalesce((SELECT sum(-x.total)::float8 FROM goo_orders x WHERE x.parent_id = o.id AND x.type = 'shop_order_refund'), 0)
		FROM goo_orders o WHERE type = 'shop_order' ORDER BY (meta->>'_wp2p_order_id')::bigint`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var st string
		var total, disc, refunded float64
		_ = rows.Scan(&st, &total, &disc, &refunded)
		got = append(got, st+" "+strconv.FormatFloat(total, 'f', -1, 64)+" "+strconv.FormatFloat(disc, 'f', -1, 64)+" "+strconv.FormatFloat(refunded, 'f', -1, 64))
	}
	rows.Close()
	if want := []string{"completed 48.79 4 0", "processing 59.5 0 0", "completed 28.56 0 12"}; !slices.Equal(got, want) {
		t.Errorf("orders %v, want %v", got, want)
	}
	if cp, err := srv.gooCouponByCode(ctx, "summer10"); err != nil || cp.UsageCount != 1 || cp.Amount != 10 || len(cp.ProductIDs) != 1 {
		t.Errorf("coupon: %+v %v", cp, err)
	}
	// Again, through the map: nothing new, the draft (no slug) included.
	var posts int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts`).Scan(&posts)
	id2 := wp2pNewID()
	_, _ = srv.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, created_by, state) VALUES ($1, $2, 'running')`, id2, admin.ID)
	r2 := &wp2pRun{s: srv, id: id2, dir: filepath.Join(t.TempDir(), "work2"), pkg: r.pkg, keepPackage: true, key: &key, u: admin}
	if err := r2.run(ctx); err != nil {
		t.Fatal(err)
	}
	var posts2 int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts`).Scan(&posts2)
	if posts2 != posts || r2.rep.Counts["posts"] != 0 || r2.rep.Counts["orders"] != 0 {
		t.Errorf("second import: %d posts after, %d before; counts %v", posts2, posts, r2.rep.Counts)
	}
}

// A shutdown in the middle of a phase (the content and the orders phase here) leaves that phase
// unfinished and the import queued; resuming completes it to the same site a run without
// interruptions makes, nothing missing and nothing twice (the draft without a slug included).
func TestWP2PResumeAfterCancel(t *testing.T) {
	key, err := wp2pParseKey("oT_EIQ1H0rTK3wMWwHbctyxQKAAkq9JXkPtZGNKeSXY")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/wp2p/wordpress-woocommerce.wp2p")
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{`SELECT count(*) FROM posts`, `SELECT count(*) FROM media`, `SELECT count(*) FROM users`, `SELECT count(*) FROM goo_orders`,
		`SELECT count(*) FROM goo_order_items`, `SELECT count(*) FROM goo_coupons`, `SELECT count(*) FROM goo_tax_rates`, `SELECT count(*) FROM terms`,
		`SELECT count(*) FROM menu_items`, `SELECT count(*) FROM comments`, `SELECT count(*) FROM goo_order_notes`, `SELECT count(*) FROM goo_download_permissions`}
	// site imports the fixture, cancelling the run at the n-th record of each phase in cuts.
	site := func(cuts map[string]int) []int {
		ts, c, srv := testServerBuilderOff(t)
		installUsersSite(t, c, ts.URL)
		ctx := context.Background()
		if err := os.MkdirAll(srv.wp2pImportDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		id := wp2pNewID()
		if err := os.WriteFile(srv.wp2pPackagePath(id), fixture, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, package_key, created_by, state) VALUES ($1, $2, 1, 'queued')`, id, key[:]); err != nil {
			t.Fatal(err)
		}
		for phase, n := range cuts {
			jctx, cancel := context.WithCancel(ctx)
			seen := 0
			wp2pRecordHook = func(kind string) {
				if kind == phase {
					if seen++; seen == n {
						cancel() // the process is being stopped
					}
				}
			}
			err := srv.wp2pImportJob(jctx, map[string]any{"id": id})
			wp2pRecordHook = nil
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cut in %s: %v", phase, err)
			}
			var state string
			var prog []byte
			_ = srv.store.Pool.QueryRow(ctx, `SELECT state, progress FROM wp2p_migrations WHERE id = $1`, id).Scan(&state, &prog)
			var p wp2pProgress
			_ = json.Unmarshal(prog, &p)
			if state != "queued" || p.finished(phase) || p.Phase != phase {
				t.Fatalf("after the cut in %s: state %s, progress %+v", phase, state, p)
			}
		}
		if err := srv.wp2pImportJob(ctx, map[string]any{"id": id}); err != nil {
			t.Fatal(err)
		}
		var state, msg string
		_ = srv.store.Pool.QueryRow(ctx, `SELECT state, error FROM wp2p_migrations WHERE id = $1`, id).Scan(&state, &msg)
		if state != "done" {
			t.Fatalf("import %s: %s", state, msg)
		}
		out := make([]int, len(queries))
		for i, q := range queries {
			if err := srv.store.Pool.QueryRow(ctx, q).Scan(&out[i]); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	t.Cleanup(func() { wp2pRecordHook = nil })
	want := site(nil)
	got := site(map[string]int{"content": 16}) // after the posts and pages (the draft too), before the products
	got2 := site(map[string]int{"orders": 2})
	for i, q := range queries {
		if got[i] != want[i] || got2[i] != want[i] {
			t.Errorf("%s: %d (content cut), %d (orders cut), %d without a cut", q, got[i], got2[i], want[i])
		}
	}
}

// wp2p_cleanup: abandoned uploads and unresumed failures lose their files (and their key), tokens of
// finished imports are forgotten, old reports keep their counts only, and files nobody owns go.
func TestWP2PCleanup(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	pool := srv.store.Pool
	if err := os.MkdirAll(srv.wp2pImportDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	type row struct {
		state, token string
		expires      string // SQL, or "" for NULL
		cli          bool
		age          string // updated_at = now() - age
		files        bool
		legacy       bool // a run of the 0.5.0 binary: no heartbeat marker in its progress
	}
	rows := map[string]row{
		"expiredtoken": {state: "waiting", token: "a", expires: "now() - interval '1 hour'", age: "1 day", files: false},
		"freshtoken":   {state: "waiting", token: "b", expires: "now() + interval '1 hour'", age: "1 minute"},
		"idleupload":   {state: "receiving", expires: "", age: "8 days", files: true},
		"activeupload": {state: "receiving", token: "c", expires: "now() + interval '20 hours'", age: "10 minutes", files: true},
		"oldfailure":   {state: "failed", age: "8 days", files: true},
		"newfailure":   {state: "failed", age: "1 day", files: true},
		"killedcli":    {state: "running", cli: true, age: "7 hours", files: true},
		"killedcli1h":  {state: "running", cli: true, age: "45 minutes", files: true},
		"legacycli":    {state: "running", cli: true, age: "2 hours", files: true, legacy: true},
		"legacykilled": {state: "running", cli: true, age: "7 hours", files: true, legacy: true},
		"livecli":      {state: "running", cli: true, age: "2 minutes", files: true},
		"oldfailedcli": {state: "failed", cli: true, age: "8 days", files: true},
		"olddone":      {state: "done", token: "d", age: "31 days"},
		"newdone":      {state: "done", token: "e", age: "2 minutes"},
	}
	ids := map[string]string{}
	report := `{"counts":{"users":2},"skipped":[{"kind":"user","item":"#4","reason":"x"}],"content":{"Posts":1,"Log":["a"]},"notes":["n"]}`
	for name, r := range rows {
		id := wp2pNewID()
		ids[name] = id
		exp := "NULL"
		if r.expires != "" {
			exp = r.expires
		}
		progress := `{"hb":true}`
		if r.legacy {
			progress = `{}`
		}
		if _, err := pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, token_hash, package_key, state, cli, report, progress, updated_at, expires_at)
			VALUES ($1, $2, '\x00', $3, $4, $5, $7::jsonb, now() - $6::interval, `+exp+`)`, id, r.token, r.state, r.cli, report, r.age, progress); err != nil {
			t.Fatal(name, err)
		}
		if r.files {
			_ = os.WriteFile(srv.wp2pPackagePath(id), []byte("x"), 0o600)
			_ = os.MkdirAll(filepath.Join(srv.wp2pWorkDir(id), "media"), 0o700)
			_ = os.WriteFile(filepath.Join(srv.wp2pWorkDir(id), "users.jsonl"), []byte("{}"), 0o600)
		}
	}
	orphan := filepath.Join(srv.wp2pImportDir(), wp2pNewID())
	_ = os.MkdirAll(orphan, 0o700)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(orphan, old, old)
	if err := srv.wp2pCleanup(ctx); err != nil {
		t.Fatal(err)
	}
	type got struct {
		state, token string
		key          bool
		report       string
		files        bool
	}
	read := func(name string) got {
		var g got
		var key []byte
		if err := pool.QueryRow(ctx, `SELECT state, token_hash, package_key, report::text FROM wp2p_migrations WHERE id = $1`, ids[name]).
			Scan(&g.state, &g.token, &key, &g.report); err != nil {
			t.Fatal(name, err)
		}
		g.key = key != nil
		_, err1 := os.Stat(srv.wp2pPackagePath(ids[name]))
		_, err2 := os.Stat(srv.wp2pWorkDir(ids[name]))
		g.files = err1 == nil || err2 == nil
		return g
	}
	for name, want := range map[string]struct {
		state       string
		key, files  bool
		token, list bool
	}{
		"expiredtoken": {state: "expired", token: true, list: true},
		"freshtoken":   {state: "waiting", key: true, token: true, list: true},
		"idleupload":   {state: "expired", list: true},
		"activeupload": {state: "receiving", key: true, files: true, token: true, list: true},
		"oldfailure":   {state: "expired", list: true},
		"newfailure":   {state: "failed", key: true, files: true, list: true},
		"killedcli":    {state: "failed", list: true},
		"killedcli1h":  {state: "failed", list: true},
		"legacycli":    {state: "running", key: true, files: true, list: true},
		"legacykilled": {state: "failed", list: true},
		"livecli":      {state: "running", key: true, files: true, list: true},
		"oldfailedcli": {state: "expired", list: true},
		"olddone":      {state: "done", key: true},
		"newdone":      {state: "done", key: true, token: true, list: true},
	} {
		g := read(name)
		hasList := strings.Contains(g.report, `"skipped"`) && strings.Contains(g.report, `"Log"`)
		if g.state != want.state || g.key != want.key || g.files != want.files || (g.token != "") != want.token || hasList != want.list {
			t.Errorf("%s: %+v, want %+v", name, g, want)
		}
		if !strings.Contains(g.report, `"users": 2`) && !strings.Contains(g.report, `"users":2`) {
			t.Errorf("%s: the counts were dropped: %s", name, g.report)
		}
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Error("an orphaned directory was kept")
	}
}

// The chunk lease: a second request while a chunk is being received is turned away, and an active
// upload moves its token's expiry on.
func TestWP2PChunkLeaseAndExpiry(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	token, id, _, err := srv.wp2pNewToken(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(srv.wp2pImportDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET state = 'receiving', size = 10, expires_at = now() + interval '1 hour' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	put := func(off int, body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/wp-json/pursae/v1/migrate/"+id+"/chunk?offset="+strconv.Itoa(off), strings.NewReader(body))
		req.Header.Set(wp2pTokenHeader, token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET lease = 'other', lease_until = now() + interval '1 minute' WHERE id = $1`, id)
	if code := put(0, "hello"); code != http.StatusConflict {
		t.Errorf("a chunk while another is received: %d", code)
	}
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET lease_until = now() - interval '1 second' WHERE id = $1`, id) // that request died
	if code := put(0, "hello"); code != http.StatusOK {
		t.Fatalf("a chunk after the lease lapsed: %d", code)
	}
	var received int64
	var lease string
	var expiresIn float64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT received, lease, extract(epoch FROM expires_at - now()) FROM wp2p_migrations WHERE id = $1`, id).Scan(&received, &lease, &expiresIn)
	if received != 5 || lease != "" || expiresIn < float64(23*3600) {
		t.Errorf("after the chunk: received %d, lease %q, expires in %.0fs", received, lease, expiresIn)
	}
	// The extension stops 7 days after the token was made.
	_, _ = srv.store.Pool.Exec(ctx, `UPDATE wp2p_migrations SET created_at = now() - interval '7 days' + interval '1 hour' WHERE id = $1`, id)
	if code := put(5, "world"); code != http.StatusOK {
		t.Fatalf("second chunk: %d", code)
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT extract(epoch FROM expires_at - now()) FROM wp2p_migrations WHERE id = $1`, id).Scan(&expiresIn)
	if expiresIn > 3700 {
		t.Errorf("the expiry went past the upload's limit: %.0fs", expiresIn)
	}
}
