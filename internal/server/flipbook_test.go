package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"wpgo/internal/store"
)

func TestFlipbookSanitize(t *testing.T) {
	b := flipbookSanitize(flipbook{Title: "  Katalog ", PDFURL: "javascript:alert(1)", Height: 5, Background: "red"})
	if b.Title != "Katalog" || b.PDFURL != "" || b.Height != flipbookDefaultHeight || b.Background != flipbookDefaultBG {
		t.Fatalf("sanitize: %+v", b)
	}
	b = flipbookSanitize(flipbook{PDFURL: "/wp-content/uploads/a.pdf", Height: 480, Background: "#112233"})
	if b.PDFURL != "/wp-content/uploads/a.pdf" || b.Height != 480 || b.Background != "#112233" {
		t.Fatalf("sanitize kept values: %+v", b)
	}
}

func TestFlipbookTitleCutsCharacters(t *testing.T) {
	b := flipbookSanitize(flipbook{Title: strings.Repeat("ğ", 250)})
	if !utf8.ValidString(b.Title) || utf8.RuneCountInString(b.Title) != 200 {
		t.Fatalf("title: %d runes, valid=%v", utf8.RuneCountInString(b.Title), utf8.ValidString(b.Title))
	}
}

// The PDF comes from the Media Library, not the form; an author manages only their own books; and
// /flipbook/ addresses that are not a book's are the site's.
func TestFlipbookAdminAndRoute(t *testing.T) {
	ts, c, srv := newTestServer(t, false)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	if err := srv.settings.Set(ctx, map[string]any{flipbookSetting: true}); err != nil {
		t.Fatal(err)
	}
	pdf := &store.Attachment{UploaderID: 1, Slug: "katalog", FilePath: "2026/11/katalog.pdf", Mime: "application/pdf", Title: "Katalog"}
	png := &store.Attachment{UploaderID: 1, Slug: "logo", FilePath: "2026/11/logo.png", Mime: "image/png", Title: "Logo"}
	for _, a := range []*store.Attachment{pdf, png} {
		if err := srv.store.InsertAttachment(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	nonceRE := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`)
	create := func(cl *http.Client, form url.Values) string {
		t.Helper()
		_, page, _ := get(t, cl, base+"/wp-admin/admin?page="+flipbookSlug)
		m := nonceRE.FindStringSubmatch(page)
		if m == nil {
			t.Fatal("no nonce on the Flipbook screen")
		}
		form.Set("_wpnonce", m[1])
		form.Set("op", "create")
		_, _, loc := post(t, cl, base+"/wp-admin/admin?page="+flipbookSlug, form)
		return loc
	}

	// A forged address or a non-PDF attachment is refused; the real PDF keeps its own URL.
	if loc := create(c, url.Values{"attachment_id": {itoa64(png.ID)}, "pdf_url": {"https://evil.example/x.pdf"}, "mime": {"application/pdf"}}); !strings.Contains(loc, "pfb-error") {
		t.Fatalf("non-PDF attachment accepted: %s", loc)
	}
	if loc := create(c, url.Values{"pdf_url": {"https://evil.example/x.pdf"}, "mime": {"application/pdf"}}); !strings.Contains(loc, "pfb-error") {
		t.Fatalf("PDF without an attachment accepted: %s", loc)
	}
	if loc := create(c, url.Values{"attachment_id": {itoa64(pdf.ID)}, "pdf_url": {"https://evil.example/x.pdf"}}); !strings.Contains(loc, "pfb-msg=created") {
		t.Fatalf("create: %s", loc)
	}
	books := srv.flipbookLoad().Items
	if len(books) != 1 || strings.Contains(books[0].PDFURL, "evil") || !strings.HasSuffix(books[0].PDFURL, "/wp-content/uploads/2026/11/katalog.pdf") ||
		books[0].Author != 1 || books[0].FileName != "katalog.pdf" {
		t.Fatalf("book: %+v", books)
	}
	adminBook := books[0].ID

	// An author creates their own book but may not edit or delete the admin's.
	asCreateUser(t, srv, "writer", "author", asStrongPass)
	ac := asNewClient()
	if code, _, _ := asLogin(t, ac, base, "/wp-login", "writer", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	if loc := create(ac, url.Values{"attachment_id": {itoa64(pdf.ID)}}); !strings.Contains(loc, "pfb-msg=created") {
		t.Fatalf("author create: %s", loc)
	}
	_, list, _ := get(t, ac, base+"/wp-admin/admin?page="+flipbookSlug)
	if strings.Contains(list, "action=edit&amp;id="+strconv.Itoa(adminBook)+`"`) || strings.Contains(list, "action=delete&amp;id="+strconv.Itoa(adminBook)+"&") {
		t.Error("the author sees edit or delete links for the admin's book")
	}
	if _, _, loc := get(t, ac, base+"/wp-admin/admin?page="+flipbookSlug+"&action=edit&id="+strconv.Itoa(adminBook)); !strings.Contains(loc, "pfb-error") {
		t.Errorf("author opened the admin's book: %s", loc)
	}
	m := nonceRE.FindStringSubmatch(list)
	if _, _, loc := post(t, ac, base+"/wp-admin/admin?page="+flipbookSlug, url.Values{"_wpnonce": {m[1]}, "op": {"save"},
		"id": {strconv.Itoa(adminBook)}, "title": {"Hijacked"}}); !strings.Contains(loc, "pfb-error") {
		t.Errorf("author saved the admin's book: %s", loc)
	}
	if b, ok := srv.flipbookGet(adminBook); !ok || b.Title == "Hijacked" {
		t.Fatalf("admin's book changed: %+v %v", b, ok)
	}

	// The full-screen page, with the worker allowed; other /flipbook/ addresses go to the site.
	resp, err := http.Get(base + "/flipbook/" + strconv.Itoa(adminBook))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "worker-src 'self'") {
		t.Fatalf("full page: %d %q", resp.StatusCode, resp.Header.Get("Content-Security-Policy"))
	}
	if code, _, _ := get(t, http.DefaultClient, base+"/flipbook/999/"); code != http.StatusNotFound {
		t.Errorf("missing book: %d", code)
	}
	if err := srv.settings.Set(ctx, map[string]any{flipbookSetting: false}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := get(t, http.DefaultClient, base+"/flipbook/"+strconv.Itoa(adminBook)); code != http.StatusNotFound {
		t.Errorf("full page while off: %d", code)
	}
	if err := srv.settings.Set(ctx, map[string]any{flipbookSetting: true}); err != nil {
		t.Fatal(err)
	}
	parent := wbrPost(t, srv, "page", "Flipbook", "publish", 1)
	child := wbrPost(t, srv, "page", "Guide", "publish", 1)
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET slug = 'flipbook' WHERE id = $1`, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET slug = 'guide', parent_id = $2 WHERE id = $1`, child.ID, parent.ID); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	code, body, _ := get(t, http.DefaultClient, base+"/flipbook/guide/")
	if code != http.StatusOK || !strings.Contains(body, "Guide") {
		t.Errorf("child page under /flipbook/: %d", code)
	}
}

func TestFlipbookEmbedHTMLEscapes(t *testing.T) {
	b := flipbookNew(`"><script>x</script>`, "/wp-content/uploads/a.pdf", "a.pdf", 3, 1)
	b.ID = 7
	out := flipbookEmbedHTML(b, false)
	if strings.Contains(out, "<script>x") {
		t.Fatalf("title not escaped: %s", out)
	}
	for _, want := range []string{`data-pdf="/wp-content/uploads/a.pdf"`, `data-full="/flipbook/7"`, `data-share="/flipbook/7"`, `data-download="1"`, "--pfb-height:640px", "alignwide"} {
		if !strings.Contains(out, want) {
			t.Errorf("embed lacks %s", want)
		}
	}
	if full := flipbookEmbedHTML(b, true); strings.Contains(full, "data-full=") || strings.Contains(full, "--pfb-height") || !strings.Contains(full, "pfb--full") {
		t.Errorf("full page markup: %s", full)
	}
	if flipbookShortcodeText(7) != `[pursae_flipbook id="7"]` {
		t.Error("shortcode text")
	}
}
