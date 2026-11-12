package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func waitNextSecond() {
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second + 20*time.Millisecond)))
}

// asyncUploadT uploads a file through the media modal's endpoint and returns the attachment ID.
func asyncUploadT(t *testing.T, c *http.Client, base, nonce, name string, data []byte, fields map[string]string) int64 {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_wpnonce", nonce)
	_ = mw.WriteField("action", "upload-attachment")
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, _ := mw.CreateFormFile("async-upload", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-admin/async-upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	m := regexp.MustCompile(`"id":(\d+)`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("upload %s: %d %.300s", name, resp.StatusCode, raw)
	}
	return gooAtoi64(string(m[1]))
}

// Object versioning: new objects get a time-stamped folder, and sizes written again under the same
// name (regenerated thumbnails) replace their bucket copies instead of leaving stale ones.
func TestGoCDNObjectVersioningOverwrite(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"object-versioning": {"1"}})
	if !s.srv.gocdnLoad().ObjectVersioning {
		t.Fatal("object versioning not saved")
	}
	ctx := context.Background()
	id := int64(s.upload("photo.jpg", gocdnTestImage(900, 600, "jpeg"))["id"].(float64))
	a, _ := s.srv.store.GetAttachment(ctx, id)
	it := s.srv.gocdnItemByPath(ctx, a.FilePath)
	if it == nil || !regexp.MustCompile(`/\d{14}/photo\.jpg$`).MatchString(it.Key) {
		t.Fatalf("versioned item %+v (keys %v)", it, s.fake.Keys("media"))
	}
	thumb := a.Variant("thumbnail")
	if thumb == nil {
		t.Fatal("no thumbnail")
	}
	oldThumb := s.srv.gocdnItemByPath(ctx, thumb.FilePath)
	if oldThumb == nil {
		t.Fatal("thumbnail not offloaded")
	}
	waitNextSecond() // the version folder has a one-second resolution
	if err := s.srv.regenerateAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	newThumb := s.srv.gocdnItemByPath(ctx, thumb.FilePath)
	if newThumb == nil || newThumb.Key == oldThumb.Key {
		t.Fatalf("regenerated thumbnail kept its key: %+v", newThumb)
	}
	if s.fake.Object("media", oldThumb.Key) != nil {
		t.Errorf("stale object %s still in the bucket", oldThumb.Key)
	}
	if !slices.Contains(s.fake.Keys("media"), newThumb.Key) {
		t.Errorf("new object %s missing", newThumb.Key)
	}
}

// An upload whose offload fails is remembered, counted on the screen and offloaded by Retry failed.
func TestGoCDNFailedOffloadRetry(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	ctx := context.Background()
	s.fake.FailNext = 1000
	id := int64(s.upload("later.png", gocdnTestImage(40, 40, "png"))["id"].(float64))
	s.fake.FailNext = 0
	if n := s.srv.gocdnFailedCount(ctx); n != 1 {
		t.Fatalf("failed count %d, want 1", n)
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload")
	if !strings.Contains(body, "could not be copied to the bucket") || !strings.Contains(body, `value="retry-failed"`) {
		t.Fatal("no failed uploads notice / Retry failed button")
	}
	if code, _, loc := s.pageForm("offload", url.Values{"do": {"retry-failed"}}); code != 302 || !strings.Contains(loc, "gocdn-msg") {
		t.Fatalf("retry failed: %d %s", code, loc)
	}
	if n := s.srv.gocdnFailedCount(ctx); n != 0 {
		t.Fatalf("still %d failed", n)
	}
	a, _ := s.srv.store.GetAttachment(ctx, id)
	if s.srv.gocdnItemByPath(ctx, a.FilePath) == nil {
		t.Fatal("not offloaded after the retry")
	}
}

// The orphan scan lists objects under the prefix that no record points at, and deletes only those.
func TestGoCDNOrphanScan(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	ctx := context.Background()
	id := int64(s.upload("kept.png", gocdnTestImage(40, 40, "png"))["id"].(float64))
	s.fake.PutObject("media", "wp-content/uploads/2020/01/stray.png", []byte("x"), "image/png")
	s.fake.PutObject("media", "elsewhere/other.png", []byte("x"), "image/png")
	if code, _, loc := s.pageForm("offload", url.Values{"do": {"orphans-scan"}}); code != 302 || !strings.Contains(loc, "orphans=1") {
		t.Fatalf("scan: %d %s", code, loc)
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload&orphans=1")
	if !strings.Contains(body, "2020/01/stray.png") || strings.Contains(body, "kept.png</li>") || strings.Contains(body, "other.png") {
		t.Fatalf("orphan list wrong: %s", between(body, `id="gc-orphans"`, "</div>"))
	}
	if code, _, _ := s.pageForm("offload", url.Values{"do": {"orphans-delete"}}); code != 302 {
		t.Fatal("delete orphans")
	}
	if s.fake.Object("media", "wp-content/uploads/2020/01/stray.png") != nil {
		t.Error("orphan not deleted")
	}
	if s.fake.Object("media", "elsewhere/other.png") == nil {
		t.Error("an object outside the prefix was deleted")
	}
	a, _ := s.srv.store.GetAttachment(ctx, id)
	if it := s.srv.gocdnItemByPath(ctx, a.FilePath); it == nil || s.fake.Object("media", it.Key) == nil {
		t.Error("a recorded object was deleted")
	}
}

// Commerce's "Choose file" uploads (type=downloadable_product) land in the protected folder.
func TestMediaDownloadableUploadIsPrivate(t *testing.T) {
	base, c, srv := gooSite(t)
	_, page, _ := get(t, c, base+"/wp-admin/media-new")
	nonce := mediaFormNonceRe.FindStringSubmatch(page)
	if nonce == nil {
		t.Skip("no upload nonce on media-new")
	}
	id := asyncUploadT(t, c, base, nonce[1], "manual.pdf", []byte("%PDF-1.4\n%%EOF\n"), map[string]string{"type": "downloadable_product"})
	a, err := srv.store.GetAttachment(context.Background(), id)
	if err != nil || !mediaIsPrivate(a) {
		t.Fatalf("downloadable upload is not private: %+v %v", a, err)
	}
	resp := doGet(t, gocdnAnon(), base+"/wp-content/uploads/"+a.FilePath, nil)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("private file answered %d", resp.StatusCode)
	}
}
