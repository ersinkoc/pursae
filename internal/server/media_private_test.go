package server

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMediaIsPrivateRel(t *testing.T) {
	for rel, want := range map[string]bool{
		"private/2026/11/a.zip": true, "/private/a.zip": true, "Private/a.zip": true, "./private/a.zip": true, "x/../private/a.zip": true,
		`private\a.zip`: true, "private": true, "private./a.zip": true,
		"2026/11/a.zip": false, "privateer/a.zip": false, "2026/private/a.zip": false, "": false,
	} {
		if got := mediaIsPrivateRel(rel); got != want {
			t.Errorf("mediaIsPrivateRel(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestGoCDNKeys(t *testing.T) {
	s := &Server{}
	s.cfg.Secret = "test-secret"
	st := gocdnSettings{Prefix: "up/"}
	if k := s.gocdnNewKey(st, "2026/11/a.jpg"); k != "up/2026/11/a.jpg" || !gocdnKeyFits(st, k, "2026/11/a.jpg") {
		t.Errorf("plain key %q", k)
	}
	st.ObjectVersioning = true
	k := s.gocdnNewKey(st, "2026/11/a.jpg")
	if !regexp.MustCompile(`^up/2026/11/\d{14}/a\.jpg$`).MatchString(k) || !gocdnKeyFits(st, k, "2026/11/a.jpg") {
		t.Errorf("versioned key %q", k)
	}
	pk := s.gocdnNewKey(st, "private/2026/11/a.zip")
	if !regexp.MustCompile(`^up/private/2026/11/[0-9a-f]{32}/\d{14}/a\.zip$`).MatchString(pk) || !gocdnKeyFits(st, pk, "private/2026/11/a.zip") {
		t.Errorf("private key %q", pk)
	}
	if gocdnKeyFits(st, "up/private/2026/11/a.zip", "private/2026/11/a.zip") {
		t.Error("a guessable key was accepted for a private file")
	}
	if gocdnKeyFits(st, "other/2026/11/a.jpg", "2026/11/a.jpg") || gocdnKeyFits(st, "up/2026/11/b.jpg", "2026/11/a.jpg") {
		t.Error("a foreign key fits")
	}
	if o := gocdnPutOptions(gocdnSettings{PublicRead: true}, "private/a.zip"); o.ACL != "private" || !strings.Contains(o.CacheControl, "no-store") {
		t.Errorf("private put options %+v", o)
	}
}

func testUpload(t *testing.T, c *http.Client, base, name string, data []byte) (int64, string) {
	t.Helper()
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/wp/v2/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-WP-Nonce", nonce)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	m := regexp.MustCompile(`"id":(\d+)`).FindSubmatch(raw)
	src := regexp.MustCompile(`"source_url":"([^"]+)"`).FindSubmatch(raw)
	if resp.StatusCode != http.StatusCreated || m == nil || src == nil {
		t.Fatalf("upload %s: %d %.300s", name, resp.StatusCode, raw)
	}
	return gooAtoi64(string(m[1])), strings.ReplaceAll(string(src[1]), `\/`, "/")
}

// A private attachment has no public URL; Commerce delivers it after its checks, directly or
// through a link that expires.
func TestMediaPrivateLocalAndCommerceDownload(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	img := gocdnTestImage(900, 600, "jpeg")
	id, src := testUpload(t, c, base, "ebook cover.jpg", img)
	anon := gocdnAnon()
	status := func(u string) int {
		resp := doGet(t, anon, u, nil)
		resp.Body.Close()
		return resp.StatusCode
	}
	if status(src) != http.StatusOK {
		t.Fatalf("public file not served: %s", src)
	}
	rel := src[strings.Index(src, "/wp-content/uploads/")+len("/wp-content/uploads/"):]

	_, list, _ := get(t, c, base+"/wp-admin/upload?mode=list")
	m := regexp.MustCompile(`href="(/wp-admin/upload\?action=make-private&amp;media=` + itoa64(id) + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(list)
	if m == nil {
		t.Fatal("no Make private row action")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "gocdn-media-msg") {
		t.Fatalf("make private: %d %s", code, loc)
	}
	a, err := srv.store.GetAttachment(ctx, id)
	if err != nil || !mediaIsPrivate(a) || a.FilePath != "private/"+rel {
		t.Fatalf("attachment after make private: %+v %v", a, err)
	}
	for _, v := range a.Variants {
		if !mediaIsPrivateRel(v.FilePath) {
			t.Errorf("size %s stayed public", v.FilePath)
		}
	}
	for _, u := range []string{src, base + "/wp-content/uploads/private/" + rel, base + "/wp-content/uploads/Private/" + rel,
		base + "/wp-content/uploads/x/../private/" + rel, base + "/wp-content/uploads/private/" + rel + "?e=99999999999&s=00"} {
		if code := status(u); code != http.StatusForbidden && code != http.StatusNotFound {
			t.Errorf("%s answered %d, want 403/404", u, code)
		}
	}
	// The admin list shows a signed link; the signature, not the session, opens it.
	_, list, _ = get(t, c, base+"/wp-admin/upload?mode=list")
	sm := regexp.MustCompile(`data-clipboard-text="([^"]*/wp-content/uploads/private/[^"]*[?&](?:amp;)?s=[^"]+)"`).FindStringSubmatch(list)
	if sm == nil || !strings.Contains(list, ">Private<") {
		t.Fatalf("no signed link / Private state in the list")
	}
	signed := html.UnescapeString(sm[1])
	resp := doGet(t, anon, signed, nil)
	if got := readAll(resp); resp.StatusCode != http.StatusOK || !bytes.Equal(got, img) || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Errorf("signed link: %d, %d bytes, Cache-Control %q", resp.StatusCode, len(got), resp.Header.Get("Cache-Control"))
	}
	if code := status(srv.mediaPrivateURL(ctx, a.FilePath, time.Now().Add(-time.Second), false)); code != http.StatusForbidden {
		t.Errorf("expired link answered %d", code)
	}
	if code := status(strings.Replace(signed, "?e=", "?e=1", 1)); code != http.StatusForbidden {
		t.Errorf("a link with a changed expiry answered %d", code)
	}
	// Anonymous REST readers get no signed link.
	resp = doGet(t, anon, base+"/wp-json/wp/v2/media/"+itoa64(id), nil)
	if raw := string(readAll(resp)); strings.Contains(raw, "s=") && strings.Contains(raw, "e=") && strings.Contains(raw, "private") {
		if regexp.MustCompile(`private[^"]*[?&]e=\d+`).MatchString(raw) {
			t.Errorf("anonymous REST response carries a signed link: %.300s", raw)
		}
	}

	// Commerce: the product still has the old public URL; the download works after the checks.
	p := gooMakeSimple(t, srv, "E-book", 5, func(p *gooProduct) {
		p.Downloadable, p.Virtual = true, true
		p.Downloads = []gooDownload{{ID: "f1", Name: "cover", File: src}}
	})
	o := &gooOrder{Status: "completed", Currency: "USD", Total: 5, Billing: map[string]string{"email": "dl@example.test"}, Shipping: map[string]string{}, Meta: map[string]any{},
		Items: []*gooOrderItem{{Type: "line_item", Name: "E-book", ProductID: p.ID, Quantity: 1, Subtotal: 5, Total: 5, Meta: map[string]any{}}}}
	if err := srv.gooSaveOrder(ctx, o, true); err != nil {
		t.Fatal(err)
	}
	srv.gooGrantDownloads(ctx, o)
	dl := fmt.Sprintf("%s/?download_file=%d&order=%s&email=%s&key=f1", base, p.ID, o.OrderKey, url.QueryEscape("dl@example.test"))
	resp = doGet(t, gooGuest(), dl, nil)
	if got := readAll(resp); resp.StatusCode != http.StatusOK || !bytes.Equal(got, img) || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("forced download: %d, %d bytes, %q", resp.StatusCode, len(got), resp.Header.Get("Content-Disposition"))
	}
	if code := status(strings.Replace(dl, "key=f1", "key=nope", 1)); code != http.StatusForbidden {
		t.Errorf("download without permission: %d", code)
	}
	if err := srv.gooSetOpts(ctx, map[string]any{"file_download_method": "redirect"}); err != nil {
		t.Fatal(err)
	}
	resp = doGet(t, gooGuest(), dl, nil)
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.Contains(loc, "/wp-content/uploads/private/") || !strings.Contains(loc, "dl=1") {
		t.Fatalf("redirect download: %d %s", resp.StatusCode, loc)
	}
	lu, _ := url.Parse(loc)
	if exp := gooAtoi64(lu.Query().Get("e")) - time.Now().Unix(); exp <= 0 || exp > int64(mediaPrivateLinkTTL/time.Second)+5 {
		t.Errorf("link lives %d s", exp)
	}
	resp = doGet(t, anon, loc, nil)
	if got := readAll(resp); resp.StatusCode != http.StatusOK || !bytes.Equal(got, img) {
		t.Errorf("following the expiring link: %d", resp.StatusCode)
	}

	// Back to public.
	if err := srv.mediaSetPrivate(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if status(src) != http.StatusOK || a.FilePath != rel {
		t.Errorf("file not public again: %s", a.FilePath)
	}
}

// With Pursae CDN: the public objects go, the private ones have an unguessable key and no public
// ACL, and a file that is only in the bucket is handed out through a short presigned URL.
func TestGoCDNPrivateFiles(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	ctx := context.Background()
	att := s.upload("secret.jpg", gocdnTestImage(800, 500, "jpeg"))
	id := int64(att["id"].(float64))
	a, _ := s.srv.store.GetAttachment(ctx, id)
	old := gocdnAttachmentFiles(a)
	if s.fake.Object("media", "wp-content/uploads/"+a.FilePath) == nil {
		t.Fatal("not offloaded")
	}
	if err := s.srv.mediaSetPrivate(ctx, a, true); err != nil {
		t.Fatal(err)
	}
	for _, f := range old {
		if s.fake.Object("media", "wp-content/uploads/"+f) != nil {
			t.Errorf("public object %s is still in the bucket", f)
		}
	}
	it := s.srv.gocdnItemByPath(ctx, a.FilePath)
	if it == nil || !regexp.MustCompile(`^wp-content/uploads/private/.*/[0-9a-f]{32}/secret\.jpg$`).MatchString(it.Key) {
		t.Fatalf("private item %+v (keys %v)", it, s.fake.Keys("media"))
	}
	if o := s.fake.Object("media", it.Key); o == nil || o.ACL != "private" || !strings.Contains(o.CacheControl, "no-store") {
		t.Errorf("private object %+v", o)
	}
	s.srv.gocdnRemoveLocal(ctx, id)
	anon := gocdnAnon()
	plain := s.ts.URL + "/wp-content/uploads/" + a.FilePath
	for _, u := range []string{plain, s.ts.URL + "/cdn-img/" + a.FilePath + "?w=100", plain + "?w=100"} {
		resp := doGet(t, anon, u, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %d", u, resp.StatusCode)
		}
	}
	resp := doGet(t, anon, s.srv.mediaPrivateURL(ctx, a.FilePath, time.Now().Add(time.Minute), true), nil)
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, s.fake.URL) || !strings.Contains(loc, "X-Amz-Expires=300") || !strings.Contains(loc, "X-Amz-Signature=") {
		t.Fatalf("bucket delivery: %d %s", resp.StatusCode, loc)
	}
	// Page markup with the private URL is not rewritten to the bucket.
	body := []byte(`<img src="` + plain + `">`)
	gc := gooTestCtx()
	if out := s.srv.gocdnRewriteMedia(gc, s.srv.gocdnLoad(), body, map[string]bool{}, true, false); !bytes.Equal(out, body) {
		t.Errorf("private URL rewritten: %s", out)
	}
}
