package server

import (
	"bytes"
	"context"
	"html"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

// gooPDFCheck validates a PDF's frame: header, %%EOF, startxref pointing at the xref table, and every
// xref entry pointing at its "n 0 obj".
func gooPDFCheck(t *testing.T, data []byte) {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) {
		t.Fatalf("no PDF header: %.20q", data)
	}
	if !bytes.HasSuffix(bytes.TrimRight(data, "\n"), []byte("%%EOF")) {
		t.Fatalf("no %%%%EOF at the end: %q", data[max(0, len(data)-20):])
	}
	m := regexp.MustCompile(`startxref\n(\d+)\n%%EOF`).FindSubmatch(data)
	if m == nil {
		t.Fatal("no startxref")
	}
	off, _ := strconv.Atoi(string(m[1]))
	if off <= 0 || off >= len(data) || !bytes.HasPrefix(data[off:], []byte("xref\n")) {
		t.Fatalf("startxref %d does not point at the xref table", off)
	}
	head := regexp.MustCompile(`^xref\n0 (\d+)\n`).FindSubmatch(data[off:])
	if head == nil {
		t.Fatal("bad xref header")
	}
	n, _ := strconv.Atoi(string(head[1]))
	table := data[off+len(head[0]):]
	for i := 0; i < n; i++ {
		entry := table[i*20 : i*20+20]
		if i == 0 {
			if string(entry) != "0000000000 65535 f \n" {
				t.Fatalf("xref entry 0: %q", entry)
			}
			continue
		}
		o, err := strconv.Atoi(string(entry[:10]))
		if err != nil || entry[17] != 'n' {
			t.Fatalf("xref entry %d: %q", i, entry)
		}
		want := strconv.Itoa(i) + " 0 obj"
		if !bytes.HasPrefix(data[o:], []byte(want)) {
			t.Fatalf("xref entry %d points at %.12q, not %q", i, data[o:], want)
		}
	}
	if !bytes.Contains(data[off:], []byte("trailer\n<< /Size "+strconv.Itoa(n)+" /Root 1 0 R")) {
		t.Fatal("bad trailer")
	}
}

func gooPDFTestOrder(t *testing.T, srv *Server, items int, total float64) *gooOrder {
	t.Helper()
	o := &gooOrder{Status: "completed", Currency: "EUR", PaymentMethod: "bacs", PaymentMethodTitle: "Direct bank transfer",
		Billing: map[string]string{"first_name": "Ayşe", "last_name": "Yılmaz", "address_1": "Bağdat Caddesi 5", "city": "İstanbul", "postcode": "34000",
			"country": "TR", "email": "ayse@example.test", "phone": "+90 555"},
		Shipping:     map[string]string{"first_name": "Ayşe", "last_name": "Yılmaz", "address_1": "Çiçek Sokak 1", "city": "Ankara", "country": "TR"},
		CustomerNote: "Leave the parcel at the door — thanks!", Meta: map[string]any{}}
	for i := 0; i < items; i++ {
		o.Items = append(o.Items, &gooOrderItem{Type: "line_item", Name: "Café crème mug " + strconv.Itoa(i+1), Quantity: 2, Subtotal: 20, Total: 18, TotalTax: 1.8, SubtotalTax: 2,
			Meta: map[string]any{"_display": []any{map[string]any{"key": "Color", "value": "Grün"}}}})
	}
	o.Items = append(o.Items,
		&gooOrderItem{Type: "shipping", Name: "Flat rate", Total: 5, Meta: map[string]any{"method_id": "flat_rate"}},
		&gooOrderItem{Type: "coupon", Name: "save10", Total: 2},
		&gooOrderItem{Type: "tax", Name: "TR-KDV-1", Meta: map[string]any{"label": "KDV", "tax_amount": 1.8, "shipping_tax_amount": 0.0}},
	)
	o.DiscountTotal, o.ShippingTotal, o.CartTax, o.TotalTax, o.Total = 2*float64(items), 5, 1.8*float64(items), 1.8*float64(items), total
	if err := srv.gooSaveOrder(context.Background(), o, true); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestGooExtPDFWriter(t *testing.T) {
	got := gooPDFEncode("€ ₺ é ü ş ğ ı İ ç ö – “x” Ő")
	want := []byte{0x80, ' ', 'T', 'L', ' ', 0xE9, ' ', 0xFC, ' ', 's', ' ', 'g', ' ', 'i', ' ', 'I', ' ', 0xE7, ' ', 0xF6, ' ', 0x96, ' ', 0x93, 'x', 0x94, ' ', 0xD6}
	if !bytes.Equal(got, want) {
		t.Errorf("WinAnsi: % x, want % x", got, want)
	}
	if e := gooPDFEscape([]byte(`a(b)\c`)); string(e) != `a\(b\)\\c` {
		t.Errorf("escape: %s", e)
	}
	if w := gooPDFWidth("Hello", 10, false); w < 22 || w > 24 {
		t.Errorf("width of Hello at 10pt: %v", w)
	}
	lines := gooPDFWrap("The quick brown fox jumps over the lazy dog", 10, false, 80)
	if len(lines) < 3 {
		t.Errorf("wrap: %q", lines)
	}
	for _, l := range lines {
		if gooPDFWidth(l, 10, false) > 80 {
			t.Errorf("line too wide: %q", l)
		}
	}
	p := newGooPDF(gooPDFA4W, gooPDFA4H)
	p.AddPage()
	p.Text(40, 40, 12, true, "Hello (world)")
	p.Rect(40, 60, 100, 20, 0.5)
	p.Line(40, 90, 200, 90, 1, 0)
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for x := 0; x < 8; x++ {
		img.Set(x, 1, color.RGBA{255, 0, 0, 255})
	}
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	i, err := p.AddJPEG(pngBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if w, h := p.ImageSize(i); w != 8 || h != 4 {
		t.Errorf("image size %dx%d", w, h)
	}
	p.Image(i, 40, 100, 80, 40)
	p.AddPage()
	p.Text(40, 40, 12, false, "Page two")
	data := p.Bytes()
	gooPDFCheck(t, data)
	for _, want := range []string{"/Count 2", "/BaseFont /Helvetica-Bold", "/Encoding /WinAnsiEncoding", "/Filter /DCTDecode", `(Hello \(world\)) Tj`, "/Im1 Do"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("PDF lacks %q", want)
		}
	}
}

func TestGooExtPDF(t *testing.T) {
	base, client, srv := gooSite(t)
	ctx := context.Background()
	_ = srv.gooSetOpts(ctx, map[string]any{"currency": "EUR", "currency_pos": "left"})
	o := gooPDFTestOrder(t, srv, 2, 46.6)

	// Off: no route, no attachment, no thank-you link.
	c, _ := gooXCRequest("GET", "/?goo-pdf=invoice&order_id="+itoa64(o.ID)+"&key="+o.OrderKey, nil, nil, nil)
	if gooXCRoute(srv, c) {
		t.Error("invoice route while off")
	}
	for _, h := range gooEmailAttachHooks {
		if files := h(srv, ctx, "customer_completed_order", o); len(files) > 0 {
			t.Error("attachment while off")
		}
	}

	gooExtSwitch(t, srv, gooPDFKey, true)
	// A PNG logo in the uploads.
	logoDir := filepath.Join(srv.cfg.UploadsDir, "2026", "10")
	_ = os.MkdirAll(logoDir, 0o755)
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := 0; x < 40; x++ {
		for y := 0; y < 20; y++ {
			img.Set(x, y, color.RGBA{0, 100, 200, 255})
		}
	}
	f, _ := os.Create(filepath.Join(logoDir, "logo.png"))
	_ = png.Encode(f, img)
	_ = f.Close()
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_pdf_invoices_settings": map[string]any{
		"shop_name": "Kahve Dükkanı", "shop_address": "Moda Cd. 1\nKadıköy, İstanbul", "footer": "Thank you for shopping with us.",
		"number_prefix": "INV-[invoice_year]-", "number_padding": float64(5), "next_number": float64(42),
		"logo_url": "http://example.test/wp-content/uploads/2026/10/logo.png", "attach_to": []any{"customer_completed_order", "customer_invoice"},
		"display_shipping_address": true, "display_email": true, "display_phone": true, "my_account_access": "always", "mark_printed": "download",
	}})

	data, err := srv.gooPDFRender(ctx, "invoice", []*gooOrder{o})
	if err != nil {
		t.Fatal(err)
	}
	gooPDFCheck(t, data)
	num := "INV-" + time.Now().Format("2006") + "-00042"
	for _, want := range []string{num, "INVOICE", "Order Number:", "(" + o.Number() + ")", "Ayse Yilmaz", "Istanbul", "Kahve D\xfckkan", "Direct bank transfer",
		"Subtotal", "Total", "KDV", "Leave the parcel at the door", "Thank you for shopping with us.", "/DCTDecode", "Color: Gr\xfcn", "\x8040.00"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("invoice lacks %q", want)
		}
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if o.MetaString("_wcpdf_invoice_number") != num || o.MetaString("_wcpdf_invoice_date") == "" {
		t.Errorf("invoice meta: %v", o.Meta)
	}
	// The number stays; the next order gets the next one; changing the setting resets the counter.
	if again, _ := srv.gooPDFRender(ctx, "invoice", []*gooOrder{o}); !bytes.Contains(again, []byte(num)) {
		t.Error("the invoice number changed")
	}
	o2 := gooPDFTestOrder(t, srv, 1, 23.3)
	if n, _ := srv.gooPDFInvoiceNumber(ctx, o2); n != "INV-"+time.Now().Format("2006")+"-00043" {
		t.Errorf("second number %s", n)
	}
	settings := srv.gooExtSettings(gooPDFKey)
	settings["next_number"] = float64(100)
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_pdf_invoices_settings": settings})
	o3 := gooPDFTestOrder(t, srv, 1, 23.3)
	if n, _ := srv.gooPDFInvoiceNumber(ctx, o3); !strings.HasSuffix(n, "-00100") {
		t.Errorf("number after the reset %s", n)
	}
	o4 := gooPDFTestOrder(t, srv, 1, 23.3)
	if n, _ := srv.gooPDFInvoiceNumber(ctx, o4); !strings.HasSuffix(n, "-00101") {
		t.Errorf("number after the reset + 1 %s", n)
	}

	// Packing slip: no prices.
	slip, err := srv.gooPDFRender(ctx, "packing-slip", []*gooOrder{o})
	if err != nil {
		t.Fatal(err)
	}
	gooPDFCheck(t, slip)
	if !bytes.Contains(slip, []byte("PACKING SLIP")) || !bytes.Contains(slip, []byte("Shipping Method:")) || bytes.Contains(slip, []byte("\x8040.00")) || bytes.Contains(slip, []byte("(Total)")) {
		t.Error("packing slip content")
	}
	// Long orders break pages; bulk exports join documents.
	big := gooPDFTestOrder(t, srv, 70, 999)
	long, _ := srv.gooPDFRender(ctx, "invoice", []*gooOrder{big})
	gooPDFCheck(t, long)
	if !bytes.Contains(long, []byte("Page 1 of")) || bytes.Contains(long, []byte("/Count 1 ")) {
		t.Error("no page break in a long invoice")
	}
	bulk, _ := srv.gooPDFRender(ctx, "packing-slip", []*gooOrder{o, o2})
	gooPDFCheck(t, bulk)
	if !bytes.Contains(bulk, []byte("/Count 2 ")) {
		t.Error("bulk export pages")
	}

	// Order actions.
	o5 := gooPDFTestOrder(t, srv, 1, 10)
	for _, a := range gooOrderActions {
		if a.Module == gooPDFKey {
			if err := a.Run(srv, gooTestCtx(), o5); err != nil {
				t.Fatal(err)
			}
		}
	}
	o5, _ = srv.gooGetOrder(ctx, o5.ID)
	if o5.MetaString("_wcpdf_invoice_number") == "" || o5.MetaString("_wcpdf_packing_slip_date") == "" {
		t.Errorf("order actions meta: %v", o5.Meta)
	}

	// E-mail attachments.
	var files map[string][]byte
	for _, h := range gooEmailAttachHooks {
		if f := h(srv, ctx, "customer_completed_order", o); len(f) > 0 {
			files = f
		}
	}
	if len(files) != 1 {
		t.Fatalf("attachments: %d", len(files))
	}
	for name, d := range files {
		if !strings.HasPrefix(name, "invoice-") || !strings.HasSuffix(name, ".pdf") || !bytes.HasPrefix(d, []byte("%PDF-1.4")) {
			t.Errorf("attachment %s", name)
		}
	}
	for _, h := range gooEmailAttachHooks {
		if f := h(srv, ctx, "new_order", o); len(f) > 0 {
			t.Error("attached to an e-mail not chosen")
		}
	}

	// Customer download: key or owner.
	c, rec := gooXCRequest("GET", "/?goo-pdf=invoice&order_id="+itoa64(o.ID)+"&key=wrong", nil, nil, nil)
	if gooXCRoute(srv, c); rec.Code != http.StatusForbidden {
		t.Errorf("wrong key: %d", rec.Code)
	}
	c, rec = gooXCRequest("GET", "/?goo-pdf=invoice&order_id="+itoa64(o.ID)+"&key="+o.OrderKey, nil, nil, nil)
	if gooXCRoute(srv, c); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/pdf" || !bytes.Contains(rec.Body.Bytes(), []byte(num)) {
		t.Errorf("customer download: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	owned := gooPDFTestOrder(t, srv, 1, 12)
	owned.CustomerID = 5
	_ = srv.gooSaveOrder(ctx, owned, false)
	c, rec = gooXCRequest("GET", "/?goo-pdf=invoice&order_id="+itoa64(owned.ID), nil, nil, &store.User{ID: 5})
	if gooXCRoute(srv, c); rec.Code != http.StatusOK {
		t.Errorf("owner download: %d", rec.Code)
	}
	if link := srv.gooInvoiceLink(o); !strings.Contains(link, "goo-pdf=invoice") || !strings.Contains(link, o.OrderKey) {
		t.Errorf("invoice link %s", link)
	}
	c, _ = gooXCRequest("GET", "/checkout/order-received/"+itoa64(o.ID)+"/?key="+o.OrderKey, nil, nil, nil)
	if h := string(srv.gooPageHTML(c, "thankyou")); !strings.Contains(h, "Download invoice (PDF)") || !strings.Contains(h, html.EscapeString("&key=")) {
		t.Errorf("thank-you link: %s", h)
	}

	// Free orders.
	settings = srv.gooExtSettings(gooPDFKey)
	settings["disable_free"] = true
	_ = srv.gooSetOpts(ctx, map[string]any{"ext_pdf_invoices_settings": settings})
	free := gooPDFTestOrder(t, srv, 1, 0)
	c, rec = gooXCRequest("GET", "/?goo-pdf=invoice&order_id="+itoa64(free.ID)+"&key="+free.OrderKey, nil, nil, nil)
	if gooXCRoute(srv, c); rec.Code != http.StatusNotFound {
		t.Errorf("free order download: %d", rec.Code)
	}
	for _, h := range gooEmailAttachHooks {
		if f := h(srv, ctx, "customer_completed_order", free); len(f) > 0 {
			t.Error("free order invoice attached")
		}
	}

	// Admin: the list, a download, a bulk export.
	body := gooAdminPageOK(t, client, base+"/wp-admin/goocommerce?page=pdf-documents", "#"+o.Number())
	m := regexp.MustCompile(`href="(/wp-admin/goocommerce\?page=pdf-documents&amp;document=invoice&amp;order_id=` + itoa64(o.ID) + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no invoice link on the admin page")
	}
	resp, err := client.Get(base + html.UnescapeString(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	pdf := bodyOf(resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/pdf" || !strings.HasPrefix(pdf, "%PDF-1.4") {
		t.Fatalf("admin download: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	gooPDFCheck(t, []byte(pdf))
	nonce := regexp.MustCompile(`_wpnonce=([0-9a-f]+)`).FindStringSubmatch(m[1])[1]
	resp, _ = client.Get(base + "/wp-admin/goocommerce?page=pdf-documents&document=invoice&order_ids[]=" + itoa64(o.ID) + "&order_ids[]=" + itoa64(o2.ID) + "&_wpnonce=" + nonce)
	bulkPDF := bodyOf(resp)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(bulkPDF, "/Count 2 ") || !strings.Contains(bulkPDF, num) {
		t.Errorf("bulk admin export: %d", resp.StatusCode)
	}
	if code, _, _ := get(t, client, base+"/wp-admin/goocommerce?page=pdf-documents&document=invoice&order_id="+itoa64(o.ID)+"&_wpnonce=bad"); code != http.StatusForbidden {
		t.Errorf("bad nonce: %d", code)
	}
	o, _ = srv.gooGetOrder(ctx, o.ID)
	if o.MetaString("_wcpdf_invoice_printed_on") == "" {
		t.Error("not marked as printed after the admin download")
	}
}
