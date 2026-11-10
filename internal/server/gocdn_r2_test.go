package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"image"
	"image/color"
	"image/gif"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// With "Remove files from the server", a second upload of the same name must not take the first
// one's path: it would overwrite its bucket objects and take over its gocdn_items rows.
func TestGoCDNUploadDoesNotReuseRemovedNames(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	if code, _, _ := s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}}); code != http.StatusFound {
		t.Fatal("save offload settings")
	}
	first := s.upload("photo.jpg", gocdnTestImage(800, 600, "jpeg"))
	rel1 := gocdnRelOfURL(first["source_url"].(string))
	if _, err := os.Stat(s.srv.gocdnLocalPath(rel1)); !os.IsNotExist(err) {
		t.Fatal("the first upload's local copy was not removed")
	}
	obj := s.fake.Object("media", "wp-content/uploads/"+rel1)
	if obj == nil {
		t.Fatal("first upload not in the bucket")
	}
	before := append([]byte(nil), obj.Body...)

	second := s.upload("photo.jpg", gocdnTestImage(640, 480, "jpeg"))
	rel2 := gocdnRelOfURL(second["source_url"].(string))
	if rel2 == rel1 {
		t.Fatalf("the second upload took the removed file's path %s", rel1)
	}
	if o := s.fake.Object("media", "wp-content/uploads/"+rel1); o == nil || !bytes.Equal(o.Body, before) {
		t.Fatal("the first upload's object was overwritten")
	}
	id1 := int64(first["id"].(float64))
	if it := s.srv.gocdnItemByPath(context.Background(), rel1); it == nil || it.MediaID != id1 {
		t.Fatalf("the first upload's row was taken over: %+v", it)
	}
}

// Offloaded files proxied through the site answer byte ranges (video seeking, Safari).
func TestGoCDNProxyAnswersRanges(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"local-delivery": {"proxy"}})
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}})
	data := bytes.Repeat([]byte("0123456789"), 300)
	att := s.upload("notes.txt", data)
	rel := gocdnRelOfURL(att["source_url"].(string))
	if _, err := os.Stat(s.srv.gocdnLocalPath(rel)); !os.IsNotExist(err) {
		t.Fatal("local copy not removed")
	}
	u := s.ts.URL + "/wp-content/uploads/" + rel
	resp := doGet(t, gocdnAnon(), u, map[string]string{"Range": "bytes=10-19"})
	body := readAll(resp)
	if resp.StatusCode != http.StatusPartialContent || string(body) != "0123456789" ||
		resp.Header.Get("Content-Range") != "bytes 10-19/3000" || resp.Header.Get("Content-Length") != "10" {
		t.Fatalf("range: %d %q %q %q", resp.StatusCode, body, resp.Header.Get("Content-Range"), resp.Header.Get("Content-Length"))
	}
	resp = doGet(t, gocdnAnon(), u, nil)
	body = readAll(resp)
	if resp.StatusCode != http.StatusOK || len(body) != 3000 || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("whole file: %d %d %q", resp.StatusCode, len(body), resp.Header.Get("Accept-Ranges"))
	}
}

// Deleting an attachment while Pursae CDN is deactivated (files left only in the bucket) still
// deletes its objects: the rows go with the attachment, so nothing could find them afterwards.
func TestGoCDNDeleteWhileDeactivated(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}})
	att := s.upload("gone.png", gocdnTestImage(300, 300, "png"))
	id := int64(att["id"].(float64))
	a, err := s.srv.store.GetAttachment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	files := gocdnAttachmentFiles(a)
	if err := s.srv.gocdnDeactivate(context.Background()); err != nil || s.srv.gocdnOn() {
		t.Fatal("deactivate")
	}
	if code, _, raw := s.restJSON(http.MethodDelete, "/wp-json/wp/v2/media/"+itoa64(id)+"?force=true", nil); code != http.StatusOK {
		t.Fatalf("delete: %d %.200s", code, raw)
	}
	for _, f := range files {
		if s.fake.Object("media", "wp-content/uploads/"+f) != nil {
			t.Errorf("%s left in the bucket", f)
		}
	}
}

// Image service URLs carry "&" in their query; in a sitemap or feed it must be escaped or the
// whole document stops being XML.
func TestGoCDNRewriteEscapesXML(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	att := s.upload("map.jpg", gocdnTestImage(400, 300, "jpeg"))
	src := att["source_url"].(string)
	if code, _, _ := s.pageForm("images", url.Values{"img-enabled": {"1"}, "img-sign": {"1"}, "img-max-width": {"2000"}, "img-max-height": {"2000"},
		"img-quality": {"70"}, "img-webp": {"1"}, "img-rewrite": {"1"}, "img-rewrite-quality": {"77"}, "img-max-source-mp": {"60"}}); code != http.StatusFound {
		t.Fatal("save image settings")
	}
	st := s.srv.gocdnLoad()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, s.ts.URL+"/wp-sitemap.xml", nil)
	doc := `<?xml version="1.0"?><urlset><url><loc>` + s.ts.URL + `/</loc><image><loc>` + src + `</loc></image></url></urlset>`
	out := string(s.srv.gocdnRewriteBody(c, st, []byte(doc), false, false, false, true))
	if !strings.Contains(out, "/cdn-img/") {
		t.Fatalf("not rewritten: %s", out)
	}
	d := xml.NewDecoder(strings.NewReader(out))
	for {
		if _, err := d.Token(); err != nil {
			if err.Error() != "EOF" {
				t.Fatalf("rewritten sitemap is not XML (%v): %s", err, out)
			}
			break
		}
	}
	// HTML keeps a raw "&" (inline scripts read the URL as is).
	htmlOut := string(s.srv.gocdnRewriteBody(c, st, []byte(`<img src="`+src+`">`), false, false, true, false))
	if strings.Contains(htmlOut, "&amp;") || !strings.Contains(htmlOut, "&s=") {
		t.Errorf("HTML rewrite: %s", htmlOut)
	}
}

// A GIF with many large frames is flattened before gif.DecodeAll allocates every frame.
func TestGoCDNGIFFrameBudgetBeforeDecode(t *testing.T) {
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{Config: image.Config{Width: 64, Height: 64, ColorModel: pal}}
	for i := 0; i < 50; i++ {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 64, 64), pal))
		g.Delay = append(g.Delay, 1)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	if n := gocdnGIFFrames(buf.Bytes(), 1000); n != 50 {
		t.Fatalf("counted %d frames, want 50", n)
	}
	if n := gocdnGIFFrames(buf.Bytes(), 10); n != 10 {
		t.Fatalf("limit: %d", n)
	}
	if !gocdnGIFAnimated(buf.Bytes()) || gocdnGIFAnimated(buf.Bytes()[:20]) {
		t.Error("gocdnGIFAnimated")
	}
}

// Deleting an attachment removes the image editor's backup (the original upload and its sizes)
// and earlier edits too, not only the current files.
func TestDeleteAttachmentRemovesEditorFiles(t *testing.T) {
	s := gocdnSite(t)
	att := s.upload("edit-me.jpg", gocdnTestImage(400, 300, "jpeg"))
	id := int64(att["id"].(float64))
	ctx := context.Background()
	edit := func(history string) {
		a, err := s.srv.store.GetAttachment(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, s.ts.URL+"/wp-admin/admin-ajax", strings.NewReader(url.Values{"history": {history}, "target": {"all"}}.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if msg := s.srv.imgeditSave(c, a, false); msg["error"] != nil {
			t.Fatalf("edit: %v", msg["error"])
		}
	}
	original, _ := s.srv.store.GetAttachment(ctx, id)
	all := map[string]bool{}
	for _, f := range gocdnAttachmentFiles(original) {
		all[f] = true
	}
	edit(`[{"r":90}]`)
	mid, _ := s.srv.store.GetAttachment(ctx, id)
	for _, f := range gocdnAttachmentFiles(mid) {
		all[f] = true
	}
	edit(`[{"f":1}]`)
	last, _ := s.srv.store.GetAttachment(ctx, id)
	for _, f := range gocdnAttachmentFiles(last) {
		all[f] = true
	}
	if mid.FilePath == original.FilePath || last.FilePath == mid.FilePath {
		t.Fatalf("edits did not create new files: %s %s %s", original.FilePath, mid.FilePath, last.FilePath)
	}
	for f := range all {
		if _, err := os.Stat(s.srv.gocdnLocalPath(f)); err != nil {
			t.Fatalf("%s missing before the delete: %v", f, err)
		}
	}
	if got := gocdnAttachmentFiles(last); !containsStr(got, mid.FilePath) || !containsStr(got, original.FilePath) {
		t.Fatalf("the earlier edit or the original is not tracked: %v", got)
	}
	// Restore keeps the edits tracked as well.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, s.ts.URL+"/wp-admin/admin-ajax", nil)
	if msg := s.srv.imgeditRestore(c, last); msg["error"] != nil {
		t.Fatal(msg["error"])
	}
	restored, _ := s.srv.store.GetAttachment(ctx, id)
	if restored.FilePath != original.FilePath || !containsStr(gocdnAttachmentFiles(restored), last.FilePath) {
		t.Fatalf("after restore: %s %v", restored.FilePath, gocdnAttachmentFiles(restored))
	}
	if code, _, raw := s.restJSON(http.MethodDelete, "/wp-json/wp/v2/media/"+itoa64(id)+"?force=true", nil); code != http.StatusOK {
		t.Fatalf("delete: %d %.200s", code, raw)
	}
	for f := range all {
		if _, err := os.Stat(s.srv.gocdnLocalPath(f)); !os.IsNotExist(err) {
			t.Errorf("%s left on disk after the attachment was deleted", f)
		}
	}
}

// With an image CDN URL the content images' /cdn-img/ URLs are written to that host.
func TestGoCDNImageCDNURL(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	att := s.upload("cdnimg.jpg", gocdnTestImage(400, 300, "jpeg"))
	src := att["source_url"].(string)
	if code, _, _ := s.pageForm("images", url.Values{"img-enabled": {"1"}, "img-sign": {"1"}, "img-max-width": {"2000"}, "img-max-height": {"2000"},
		"img-quality": {"70"}, "img-webp": {"1"}, "img-rewrite": {"1"}, "img-rewrite-quality": {"77"}, "img-max-source-mp": {"60"},
		"img-cdn-url": {"https://img.example.com/"}}); code != http.StatusFound {
		t.Fatal("save image settings")
	}
	st := s.srv.gocdnLoad()
	if st.ImgCDNURL != "https://img.example.com" {
		t.Fatalf("ImgCDNURL %q", st.ImgCDNURL)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, s.ts.URL+"/", nil)
	out := string(s.srv.gocdnRewriteBody(c, st, []byte(`<img src="`+src+`">`), false, false, true, false))
	if !strings.Contains(out, `src="https://img.example.com/cdn-img/`) {
		t.Fatalf("not on the image CDN: %s", out)
	}
}
