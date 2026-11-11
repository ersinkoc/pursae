package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// testImage encodes a w×h image whose left half is red and right half blue.
func testImage(t *testing.T, w, h int, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x < w/2 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := media.EncodeTo(&buf, img, format); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// replaceViaAdmin submits the edit screen's Replace media form and returns the redirect.
func replaceViaAdmin(t *testing.T, c *http.Client, base string, id int64, name string, data []byte) string {
	t.Helper()
	_, edit, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(id)+"&action=edit")
	m := regexp.MustCompile(`id="wpgo-replace-form"[^>]*>\s*<input type="hidden" name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(edit)
	if m == nil {
		t.Fatal("no Replace media form on the edit screen")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_wpnonce", m[1])
	_ = mw.WriteField("post_ID", itoa64(id))
	fw, _ := mw.CreateFormFile("replacement", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-admin/media-replace", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("replace: %d", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

func headers(t *testing.T, c *http.Client, u string) (int, http.Header) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, resp.Header
}

// TestMediaReplace replaces an image in place (same type: same URLs; new aspect ratio: new size
// names redirected and rewritten in pages), then with another type, refuses another kind, and
// replaces through the REST API.
func TestMediaReplace(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)

	a, err := srv.insertAttachmentFile(ctx, "logo.jpg", bytes.NewReader(testImage(t, 900, 600, "jpeg")), uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	oldMain, oldMedium := a.FilePath, a.Variant("medium").FilePath
	if !strings.HasSuffix(oldMedium, "logo-300x200.jpg") {
		t.Fatalf("medium before: %s", oldMedium)
	}
	// A published post that shows the medium size the way the editor saved it.
	p := &store.Post{Type: "post", Title: "Logo post", Status: "publish", AuthorID: uid, CommentStatus: "open", PingStatus: "open", Meta: map[string]any{},
		Content: `<p><img class="wp-image-` + itoa64(a.ID) + ` size-medium" src="/wp-content/uploads/` + oldMedium + `" width="300" height="200" alt=""></p>` +
			`<p><a href="/wp-content/uploads/` + oldMain + `">full</a></p>`}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if code, page, _ := postPage(t, c, base, p.ID); code != http.StatusOK || strings.Contains(page, "?v=") {
		t.Fatalf("before: %d, versioned=%v", code, strings.Contains(page, "?v="))
	}

	// 1. Same type, square: the main URL stays, the medium size gets a new name.
	loc := replaceViaAdmin(t, c, base, a.ID, "new-logo.jpg", testImage(t, 600, 600, "jpeg"))
	if !strings.Contains(loc, "replaced=1") {
		t.Fatalf("redirect: %s", loc)
	}
	b, _ := srv.store.GetAttachment(ctx, a.ID)
	if b.FilePath != oldMain || b.Width != 600 || b.Height != 600 {
		t.Fatalf("after: %s %dx%d", b.FilePath, b.Width, b.Height)
	}
	ver := attachmentFileVersion(b)
	newMedium := b.Variant("medium").FilePath
	if ver == "" || !strings.HasSuffix(newMedium, "logo-300x300.jpg") {
		t.Fatalf("version %q, medium %s", ver, newMedium)
	}
	if _, err := os.Stat(filepath.Join(srv.cfg.UploadsDir, oldMedium)); !os.IsNotExist(err) {
		t.Errorf("old medium still on disk: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(srv.cfg.UploadsDir, path.Dir(oldMain)))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".wpgo-replace") || strings.HasPrefix(e.Name(), "wpgo-replace") {
			t.Errorf("work file left behind: %s", e.Name())
		}
	}
	// The main file's own URL shows the new image; without the version it revalidates.
	if code, h := headers(t, c, base+"/wp-content/uploads/"+oldMain); code != http.StatusOK || h.Get("Cache-Control") != "public, no-cache" {
		t.Errorf("unversioned main: %d %q", code, h.Get("Cache-Control"))
	}
	if code, h := headers(t, c, base+"/wp-content/uploads/"+oldMain+"?v="+ver); code != http.StatusOK || !strings.Contains(h.Get("Cache-Control"), "immutable") {
		t.Errorf("versioned main: %d %q", code, h.Get("Cache-Control"))
	}
	// The old medium redirects to the new one.
	if code, h := headers(t, c, base+"/wp-content/uploads/"+oldMedium); code != http.StatusMovedPermanently || !strings.Contains(h.Get("Location"), newMedium+"?v="+ver) {
		t.Errorf("old medium: %d %q", code, h.Get("Location"))
	}
	// The post now points at the new size with the new shape, versioned.
	_, page, _ := postPage(t, c, base, p.ID)
	if !strings.Contains(page, newMedium+"?v="+ver) || strings.Contains(page, oldMedium) || !strings.Contains(page, `height="300"`) ||
		!strings.Contains(page, oldMain+"?v="+ver) {
		t.Errorf("post markup after replace:\n%s", excerptAround(page, "wp-image-"))
	}
	// The media modal and REST give versioned URLs and the replace link.
	if u := srv.uploadURL(ctx, b.FilePath); !strings.HasSuffix(u, "?v="+ver) {
		t.Errorf("uploadURL: %s", u)
	}

	// 2. Another type: a PNG takes a new name, the JPEG URLs redirect to it.
	replaceViaAdmin(t, c, base, a.ID, "logo.png", testImage(t, 600, 600, "png"))
	b2, _ := srv.store.GetAttachment(ctx, a.ID)
	if b2.Mime != "image/png" || !strings.HasSuffix(b2.FilePath, "logo.png") {
		t.Fatalf("png: %s %s", b2.Mime, b2.FilePath)
	}
	if code, h := headers(t, c, base+"/wp-content/uploads/"+oldMain); code != http.StatusMovedPermanently || !strings.Contains(h.Get("Location"), b2.FilePath) {
		t.Errorf("old jpg main: %d %q", code, h.Get("Location"))
	}
	// The chain: the first old medium now leads to the PNG's medium too.
	if code, h := headers(t, c, base+"/wp-content/uploads/"+oldMedium); code != http.StatusMovedPermanently || !strings.Contains(h.Get("Location"), b2.Variant("medium").FilePath) {
		t.Errorf("chained medium: %d %q", code, h.Get("Location"))
	}

	// 3. Another kind is refused and nothing changes.
	loc = replaceViaAdmin(t, c, base, a.ID, "notes.txt", []byte("hello"))
	if !strings.Contains(loc, "replace_error=") {
		t.Errorf("text over image: %s", loc)
	}
	if b3, _ := srv.store.GetAttachment(ctx, a.ID); b3.FilePath != b2.FilePath || attachmentFileVersion(b3) != attachmentFileVersion(b2) {
		t.Error("a refused replacement changed the attachment")
	}

	// 4. Back to JPEG through the REST API (raw body): the JPEG name is free again.
	req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/pursae/v1/media/"+itoa64(a.ID)+"/replace", bytes.NewReader(testImage(t, 400, 200, "jpeg")))
	req.Header.Set("Content-Disposition", `attachment; filename="logo.jpg"`)
	req.Header.Set("Content-Type", "image/jpeg")
	req.Header.Set("X-WP-Nonce", restNonce(t, srv, c, base))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasSuffix(strings.SplitN(out["source_url"].(string), "?", 2)[0], oldMain) {
		t.Fatalf("REST replace: %d %v", resp.StatusCode, out["source_url"])
	}
	if code, _ := headers(t, c, base+"/wp-content/uploads/"+oldMain); code != http.StatusOK {
		t.Errorf("jpg main after REST replace: %d", code)
	}
	var moved int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM media_moved_files WHERE path = $1`, oldMain).Scan(&moved)
	if moved != 0 {
		t.Error("the restored JPEG path is still a redirect")
	}

	// 5. Deleting the attachment drops its index entries and moved rows.
	b4, _ := srv.store.GetAttachment(ctx, a.ID)
	srv.deleteAttachmentFiles(b4)
	_ = srv.store.DeleteAttachment(ctx, a.ID)
	if _, ok := srv.mediaIdx.lookup(b4.FilePath); ok {
		t.Error("index entry left after delete")
	}
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM media_moved_files WHERE media_id = $1`, a.ID).Scan(&moved)
	if moved != 0 {
		t.Error("moved rows left after delete")
	}
}

// TestMediaFocalPoint sets a focal point through the REST API: the cropped thumbnail is cut around
// it, the REST answer and the modal carry it, pages position the image, and a reset clears it.
func TestMediaFocalPoint(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)
	if err := srv.settings.Set(ctx, map[string]any{"thumbnail_crop": true}); err != nil {
		t.Fatal(err)
	}
	a, err := srv.insertAttachmentFile(ctx, "wide.png", bytes.NewReader(testImage(t, 1200, 300, "png")), uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	thumbPixel := func(a *store.Attachment) color.RGBA {
		t.Helper()
		f, err := os.Open(filepath.Join(srv.cfg.UploadsDir, a.Variant("thumbnail").FilePath))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := img.At(75, 75).RGBA()
		return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
	}
	restPost := func(body string) (int, map[string]any) {
		req, _ := http.NewRequest(http.MethodPost, base+"/wp-json/wp/v2/media/"+itoa64(a.ID), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-WP-Nonce", restNonce(t, srv, c, base))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, out := restPost(`{"focal_point": {"x": 0.9, "y": 0.5}}`)
	if code != http.StatusOK {
		t.Fatalf("set focal: %d %v", code, out)
	}
	fp, _ := out["media_details"].(map[string]any)["focal_point"].(map[string]any)
	if fp["x"] != 0.9 || fp["y"] != 0.5 {
		t.Errorf("REST focal_point: %v", out["media_details"])
	}
	b, _ := srv.store.GetAttachment(ctx, a.ID)
	if px := thumbPixel(b); px.B < 200 || px.R > 50 {
		t.Errorf("thumbnail should show the right (blue) side: %v", px)
	}
	if attachmentFileVersion(b) == "" {
		t.Error("re-cropped files have no version")
	}

	// A page showing the image positions it; the media modal has the field.
	p := &store.Post{Type: "post", Title: "Cover", Status: "publish", AuthorID: uid, CommentStatus: "open", PingStatus: "open", Meta: map[string]any{},
		Content: `<img class="wp-image-` + itoa64(a.ID) + `" src="/wp-content/uploads/` + b.FilePath + `" width="1200" height="300" alt="">`}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, page, _ := postPage(t, c, base, p.ID); !strings.Contains(page, "object-position:90% 50%") {
		t.Errorf("no object-position on the page:\n%s", excerptAround(page, "wp-image-"))
	}
	if _, js, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"get-attachment"}, "id": {itoa64(a.ID)}}); !strings.Contains(js, "wpgo-focal-picker") ||
		!strings.Contains(js, `"focalPoint":{"x":0.9,"y":0.5}`) || !strings.Contains(js, "#wpgo-replace-media") {
		t.Errorf("media modal data: %.600s", js)
	}

	// Invalid values are refused; null resets to the centre crop.
	if code, _ := restPost(`{"focal_point": {"x": 3, "y": 0}}`); code != http.StatusBadRequest {
		t.Errorf("invalid focal: %d", code)
	}
	if code, _ := restPost(`{"focal_point": null}`); code != http.StatusOK {
		t.Fatalf("reset: %d", code)
	}
	b, _ = srv.store.GetAttachment(ctx, a.ID)
	if attachmentFocal(b) != nil {
		t.Error("focal point kept after reset")
	}
	if px := thumbPixel(b); px.R < 200 && px.B < 200 {
		t.Errorf("centre crop expected after reset: %v", px)
	}
	if _, page, _ := postPage(t, c, base, p.ID); strings.Contains(page, "object-position") {
		t.Error("object-position kept after reset")
	}
}

// postPage fetches a post's page through its ?p= link (which redirects to the permalink).
func postPage(t *testing.T, c *http.Client, base string, id int64) (int, string, string) {
	t.Helper()
	code, body, loc := get(t, c, base+"/?p="+itoa64(id))
	if code == http.StatusMovedPermanently || code == http.StatusFound {
		if strings.HasPrefix(loc, "/") {
			loc = base + loc
		}
		return get(t, c, loc)
	}
	return code, body, loc
}

// excerptAround returns some text around the first occurrence of needle, for failure messages.
func excerptAround(s, needle string) string {
	i := strings.Index(s, needle)
	if i < 0 {
		if len(s) > 400 {
			return s[:400]
		}
		return s
	}
	return s[max(0, i-300):min(len(s), i+500)]
}
