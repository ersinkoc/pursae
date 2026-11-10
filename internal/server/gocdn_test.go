package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/gocdn"
	"wpgo/internal/gocdn/s3fake"
)

const (
	gocdnTestKey    = "AKGOCDNTEST"
	gocdnTestSecret = "s3cr3t/Value+With/Slashes"
)

type gocdnTestSite struct {
	t    *testing.T
	ts   *httptest.Server
	c    *http.Client
	srv  *Server
	fake *s3fake.Server
	rest string
}

// gocdnSite installs a site, activates GoCDN from the Plugins screen and points it at a fake S3.
func gocdnSite(t *testing.T) *gocdnTestSite {
	t.Helper()
	gocdnOffloadInline = true // tests read the bucket right after an upload
	t.Cleanup(func() { gocdnOffloadInline = false })
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	_, body, _ := get(t, c, ts.URL+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=gocdn[^"&]*&amp;[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for Pursae CDN on the Plugins screen")
	}
	if code, _, loc := get(t, c, ts.URL+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "activate=true") {
		t.Fatalf("activate Pursae CDN: %d %s", code, loc)
	}
	fake := s3fake.New(gocdnTestKey, gocdnTestSecret, "auto", "media")
	t.Cleanup(fake.Close)
	s := &gocdnTestSite{t: t, ts: ts, c: c, srv: srv, fake: fake}
	_, s.rest, _ = get(t, c, ts.URL+"/wp-admin/admin-ajax?action=rest-nonce")
	return s
}

// configure saves the storage settings through the Storage screen.
func (s *gocdnTestSite) configure(extra url.Values) {
	s.t.Helper()
	_, body, _ := get(s.t, s.c, s.ts.URL+"/wp-admin/gocdn")
	form := url.Values{"_wpnonce": {formNonce(s.t, body)}, "do": {"save"}, "provider": {"minio"}, "endpoint": {s.fake.URL},
		"region": {"auto"}, "bucket": {"media"}, "access-key-id": {gocdnTestKey}, "secret-access-key": {gocdnTestSecret},
		"use-path-style": {"1"}, "object-prefix": {"wp-content/uploads/"}, "local-delivery": {"redirect"}, "public-read": {"1"}}
	for k, v := range extra {
		form[k] = v
	}
	if code, _, loc := post(s.t, s.c, s.ts.URL+"/wp-admin/gocdn", form); code != http.StatusFound || !strings.Contains(loc, "gocdn-msg") {
		s.t.Fatalf("save storage: %d %s", code, loc)
	}
}

// pageForm posts a GoCDN screen's settings form.
func (s *gocdnTestSite) pageForm(page string, form url.Values) (int, string, string) {
	s.t.Helper()
	_, body, _ := get(s.t, s.c, s.ts.URL+"/wp-admin/gocdn?page="+page)
	form.Set("_wpnonce", formNonce(s.t, body))
	if form.Get("do") == "" {
		form.Set("do", "save")
	}
	return post(s.t, s.c, s.ts.URL+"/wp-admin/gocdn?page="+page, form)
}

func gocdnTestImage(w, h int, format string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 90, 255})
		}
	}
	var buf bytes.Buffer
	if format == "png" {
		_ = png.Encode(&buf, img)
	} else {
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85})
	}
	return buf.Bytes()
}

// upload sends a file through POST /wp/v2/media.
func (s *gocdnTestSite) upload(name string, data []byte) map[string]any {
	s.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, s.ts.URL+"/wp-json/wp/v2/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-WP-Nonce", s.rest)
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		s.t.Fatalf("upload %s: %d %.300s", name, resp.StatusCode, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s *gocdnTestSite) restJSON(method, p string, payload any) (int, map[string]any, string) {
	s.t.Helper()
	var rd io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.ts.URL+p, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WP-Nonce", s.rest)
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

// anon is a client without the admin's cookies.
func gocdnAnon() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func doGet(t *testing.T, c *http.Client, u string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readAll(resp *http.Response) []byte {
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b
}

func TestGoCDNOffByDefault(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	if srv.gocdnOn() {
		t.Fatal("Pursae CDN is active on a fresh install")
	}
	_, body, _ := get(t, c, ts.URL+"/wp-admin/plugins")
	if !strings.Contains(body, "Pursae CDN") || !strings.Contains(body, "plugin=gocdn") {
		t.Fatal("Pursae CDN is not on the Plugins screen")
	}
	if code, _, _ := get(t, c, ts.URL+"/wp-admin/gocdn"); code != http.StatusNotFound {
		t.Errorf("settings screen while off: %d", code)
	}
	if code, _, _ := get(t, c, ts.URL+"/cdn-img/2026/01/x.jpg?w=10"); code == http.StatusOK || code == http.StatusForbidden {
		t.Errorf("image service answers while off: %d", code)
	}
	_, admin, _ := get(t, c, ts.URL+"/wp-admin/")
	if strings.Contains(admin, "toplevel_page_gocdn") || strings.Contains(admin, "Remote Storage") {
		t.Error("Pursae CDN menu shown while off")
	}
}

func TestGoCDNSecretsNeverEchoed(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	if strings.Contains(body, gocdnTestSecret) || strings.Contains(body, "s3cr3t") {
		t.Fatal("the secret access key is printed on the Storage screen")
	}
	if !strings.Contains(body, "saved — type to replace") || !strings.Contains(body, `name="secret-access-key-clear"`) {
		t.Error("no saved-secret state on the Storage screen")
	}
	stored, _ := s.srv.settings.Get(gocdnOption).(map[string]any)
	raw, _ := stored["secret-access-key"].(string)
	if !strings.HasPrefix(raw, gocdnSealPrefix) || strings.Contains(raw, "s3cr3t") {
		t.Fatalf("the secret is not encrypted at rest: %.20q", raw)
	}
	if s.srv.gocdnReveal(raw) != gocdnTestSecret {
		t.Fatal("the stored secret does not decrypt")
	}
	// Saving the form without typing the secret keeps it; Clear removes it.
	s.configure(url.Values{"secret-access-key": {""}})
	if s.srv.gocdnReveal(s.srv.gocdnLoad().SecretKey) != gocdnTestSecret {
		t.Fatal("an empty secret field removed the saved secret")
	}
	s.configure(url.Values{"secret-access-key": {""}, "secret-access-key-clear": {"1"}})
	if s.srv.gocdnLoad().SecretKey != "" {
		t.Fatal("Clear did not remove the secret")
	}
	// The Cloudflare token is treated the same way.
	if code, _, _ := s.pageForm("purge", url.Values{"cloudflare": {"1"}, "cloudflare-zone": {strings.Repeat("a", 32)}, "cloudflare-token": {"cf-token-value"}}); code != http.StatusFound {
		t.Fatalf("save purge: %d", code)
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=purge")
	if strings.Contains(body, "cf-token-value") {
		t.Fatal("the Cloudflare token is printed")
	}
}

func TestGoCDNTestConnection(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	code, out, _ := post(t, s.c, s.ts.URL+"/wp-admin/gocdn", url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"test"}})
	if code != http.StatusOK || !strings.Contains(out, "Connection test passed") || !strings.Contains(out, "Upload (PUT") || !strings.Contains(out, "Delete (DELETE): OK") {
		t.Fatalf("test connection: %d %s", code, between(out, "gc-steps", "</ol>"))
	}
	if keys := s.fake.Keys("media"); len(keys) != 0 {
		t.Errorf("the probe object was left behind: %v", keys)
	}
	// A wrong secret is reported with advice, and is not echoed.
	s.configure(url.Values{"secret-access-key": {"wrong-secret-xyz"}})
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	_, out, _ = post(t, s.c, s.ts.URL+"/wp-admin/gocdn", url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"test"}})
	if !strings.Contains(out, "Connection test failed") || !strings.Contains(out, "secret access key is wrong") {
		t.Fatalf("wrong secret not explained: %s", between(out, "gc-steps", "</ol>"))
	}
	if strings.Contains(out, "wrong-secret-xyz") {
		t.Fatal("the secret is echoed in the test result")
	}
	s.configure(url.Values{"bucket": {"nope"}})
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	_, out, _ = post(t, s.c, s.ts.URL+"/wp-admin/gocdn", url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"test"}})
	if !strings.Contains(out, "NoSuchBucket") || !strings.Contains(out, "check the bucket name") {
		t.Fatalf("missing bucket not explained: %s", between(out, "gc-steps", "</ol>"))
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i:]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return s
}

func TestGoCDNOffloadRewriteServeDelete(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"delivery-domain": {"https://media.cdn.test"}})
	att := s.upload("Sunset Photo.jpg", gocdnTestImage(1600, 1000, "jpeg"))
	id := int64(att["id"].(float64))
	src := att["source_url"].(string)
	rel := src[strings.Index(src, "/wp-content/uploads/")+len("/wp-content/uploads/"):]

	a, err := s.srv.store.GetAttachment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	files := gocdnAttachmentFiles(a)
	if len(files) < 3 {
		t.Fatalf("expected generated sizes, got %v", files)
	}
	for _, f := range files {
		o := s.fake.Object("media", "wp-content/uploads/"+f)
		if o == nil {
			t.Fatalf("%s was not copied to the bucket (have %v)", f, s.fake.Keys("media"))
		}
		if o.ContentType != "image/jpeg" || o.ACL != "public-read" || !strings.Contains(o.CacheControl, "immutable") {
			t.Errorf("%s stored with %q %q %q", f, o.ContentType, o.ACL, o.CacheControl)
		}
	}
	if len(s.fake.Errors) != 0 {
		t.Fatalf("signature problems: %v", s.fake.Errors)
	}
	cdn := "https://media.cdn.test/wp-content/uploads/" + rel

	// The REST API (view context) rewrites, the edit context does not.
	_, view, raw := s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(id), nil)
	if view["source_url"] != cdn {
		t.Errorf("REST source_url %v, want %s", view["source_url"], cdn)
	}
	if strings.Contains(raw, s.ts.URL+"/wp-content/uploads/") {
		t.Errorf("REST view still has local upload URLs: %.400s", raw)
	}
	_, edit, _ := s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(id)+"?context=edit", nil)
	if src, _ := edit["source_url"].(string); !strings.HasPrefix(src, s.ts.URL+"/wp-content/uploads/") {
		t.Errorf("edit context rewritten: %v", edit["source_url"])
	}

	// Post content: src and srcset point at the CDN; another site's uploads are untouched.
	thumb := a.Variant("medium")
	content := `<p><img class="wp-image-` + itoa64(id) + `" src="` + src + `" srcset="` + src + ` 1600w, /wp-content/uploads/` + thumb.FilePath + ` 300w" alt=""></p>` +
		`<p><a href="https://other.example/wp-content/uploads/` + rel + `">elsewhere</a></p>`
	code, p, raw := s.restJSON(http.MethodPost, "/wp-json/wp/v2/posts", map[string]any{"title": "CDN", "content": content, "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("create post: %d %.300s", code, raw)
	}
	link := p["link"].(string)
	resp := doGet(t, gocdnAnon(), link, nil)
	page := string(readAll(resp))
	if !strings.Contains(page, `src="`+cdn+`"`) || !strings.Contains(page, "https://media.cdn.test/wp-content/uploads/"+thumb.FilePath+" 300w") {
		t.Fatalf("post content not rewritten: %s", between(page, "wp-image-", "</p>"))
	}
	if !strings.Contains(page, "https://other.example/wp-content/uploads/"+rel) {
		t.Error("another site's upload URL was rewritten")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data: https:") {
		t.Errorf("the page CSP does not allow the delivery origin: %s", csp)
	}
	// wp-admin keeps local URLs.
	_, adminBody, _ := get(t, s.c, s.ts.URL+"/wp-admin/upload")
	if strings.Contains(adminBody, "media.cdn.test") {
		t.Error("wp-admin was rewritten")
	}

	// Remove the local copies: the uploads URL redirects, the proxy and presigned modes answer too.
	if code, _, _ := s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}}); code != http.StatusFound {
		t.Fatal("save offload settings")
	}
	s.srv.gocdnRemoveLocal(context.Background(), id)
	if _, err := os.Stat(filepath.Join(s.srv.cfg.UploadsDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Fatalf("local copy not removed: %v", err)
	}
	resp = doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	readAll(resp)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != cdn {
		t.Fatalf("removed file: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	s.configure(url.Values{"local-delivery": {"proxy"}})
	resp = doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	body := readAll(resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(body, s.fake.Object("media", "wp-content/uploads/"+rel).Body) ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("proxied file: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	s.configure(url.Values{"local-delivery": {"signed"}})
	resp = doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	readAll(resp)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.Contains(loc, "X-Amz-Signature=") {
		t.Fatalf("presigned redirect: %d %s", resp.StatusCode, loc)
	}
	if r2 := doGet(t, http.DefaultClient, loc, nil); r2.StatusCode != http.StatusOK || len(readAll(r2)) == 0 {
		t.Fatalf("the presigned URL does not work: %d", r2.StatusCode)
	}

	// The image editor fetches the file back from the bucket.
	if err := s.srv.gocdnEnsureLocal(context.Background(), rel); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.srv.cfg.UploadsDir, filepath.FromSlash(rel))); err != nil {
		t.Fatal("gocdnEnsureLocal did not restore the file")
	}

	// Deleting the attachment deletes its objects.
	if code, _, raw := s.restJSON(http.MethodDelete, "/wp-json/wp/v2/media/"+itoa64(id)+"?force=true", nil); code != http.StatusOK {
		t.Fatalf("delete: %d %.200s", code, raw)
	}
	for _, f := range files {
		if s.fake.Object("media", "wp-content/uploads/"+f) != nil {
			t.Errorf("%s left in the bucket after the attachment was deleted", f)
		}
	}
	var n int
	_ = s.srv.store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM gocdn_items`).Scan(&n)
	if n != 0 {
		t.Errorf("%d gocdn_items rows left", n)
	}
}

// waitJob polls the progress endpoint until the job stops.
func (s *gocdnTestSite) waitJob() map[string]any {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		code, body, _ := get(s.t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload&action=progress")
		if code != http.StatusOK {
			s.t.Fatalf("progress: %d", code)
		}
		var j map[string]any
		_ = json.Unmarshal([]byte(body), &j)
		if j["running"] == false && (j["finished"] == true || j["stopped"] == true) {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatal("the job did not finish")
	return nil
}

func TestGoCDNBulkJobs(t *testing.T) {
	s := gocdnSite(t)
	// Uploads made before the storage is configured stay local.
	var ids []int64
	for i, name := range []string{"one.png", "two.jpg", "doc.pdf"} {
		data := gocdnTestImage(400+i*10, 300, "png")
		if strings.HasSuffix(name, ".jpg") {
			data = gocdnTestImage(500, 300, "jpeg")
		} else if strings.HasSuffix(name, ".pdf") {
			data = []byte("%PDF-1.4 test")
		}
		ids = append(ids, int64(s.upload(name, data)["id"].(float64)))
	}
	if len(s.fake.Keys("media")) != 0 {
		t.Fatal("files offloaded before the storage was configured")
	}
	s.configure(nil)
	if code, _, _ := s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}}); code != http.StatusFound {
		t.Fatal("save offload settings")
	}
	if code, _, loc := s.pageForm("offload", url.Values{"do": {"job"}, "kind": {"offload"}}); code != http.StatusFound || !strings.Contains(loc, "gocdn-msg") {
		t.Fatalf("start offload: %d %s", code, loc)
	}
	j := s.waitJob()
	if j["finished"] != true || j["done"].(float64) != 3 || j["failed"].(float64) != 0 {
		t.Fatalf("offload job: %v", j)
	}
	st := s.srv.gocdnStats(context.Background())
	if st.Offloaded != 3 || st.Removed != st.Files || st.Files < 4 {
		t.Fatalf("stats after offload: %+v", st)
	}
	if o := s.fake.Object("media", "wp-content/uploads/"+mustAttachment(t, s, ids[2]).FilePath); o == nil || o.ContentType != "application/pdf" {
		t.Fatal("the PDF was not offloaded")
	}
	_, page, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload")
	if !strings.Contains(page, "Offload existing media") || !strings.Contains(page, `id="gc-stat-off">3<`) {
		t.Error("the Media Offload screen does not show the result")
	}

	// Download back: the files return to the server.
	if code, _, _ := s.pageForm("offload", url.Values{"do": {"job"}, "kind": {"download"}}); code != http.StatusFound {
		t.Fatal("start download")
	}
	if j := s.waitJob(); j["done"].(float64) != 3 {
		t.Fatalf("download job: %v", j)
	}
	if st := s.srv.gocdnStats(context.Background()); st.Removed != 0 {
		t.Fatalf("files still removed: %+v", st)
	}
	for _, id := range ids {
		a := mustAttachment(t, s, id)
		if _, err := os.Stat(filepath.Join(s.srv.cfg.UploadsDir, filepath.FromSlash(a.FilePath))); err != nil {
			t.Errorf("%s not downloaded back", a.FilePath)
		}
	}
	// Remove from the bucket.
	if code, _, _ := s.pageForm("offload", url.Values{"do": {"job"}, "kind": {"remove"}}); code != http.StatusFound {
		t.Fatal("start remove")
	}
	if j := s.waitJob(); j["done"].(float64) != 3 {
		t.Fatalf("remove job: %v", j)
	}
	if keys := s.fake.Keys("media"); len(keys) != 0 {
		t.Fatalf("objects left: %v", keys)
	}
	if st := s.srv.gocdnStats(context.Background()); st.Files != 0 || st.Offloaded != 0 {
		t.Fatalf("stats after remove: %+v", st)
	}
	if len(s.fake.Errors) != 0 {
		t.Fatalf("signature problems: %v", s.fake.Errors)
	}
}

func TestGoCDNJobResumesAndReportsFailures(t *testing.T) {
	s := gocdnSite(t)
	for i := 0; i < 3; i++ {
		s.upload("pic.png", gocdnTestImage(50, 50, "png"))
	}
	s.configure(nil)
	// An interrupted job (stored as running by a process that is gone) resumes after its last item.
	var first int64
	_ = s.srv.store.Pool.QueryRow(context.Background(), `SELECT min(id) FROM media`).Scan(&first)
	if err := s.srv.settings.Set(context.Background(), map[string]any{gocdnJobOption: gocdnJobState{Kind: "offload", Running: true, Done: 1, LastID: first, Total: 3}}); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload&action=progress")
	if !strings.Contains(body, `"resumable":true`) {
		t.Fatalf("interrupted job not resumable: %s", body)
	}
	s.fake.FailNext = 1 // the first upload of the resumed job fails
	if code, _, _ := s.pageForm("offload", url.Values{"do": {"job"}, "kind": {"offload"}, "resume": {"1"}}); code != http.StatusFound {
		t.Fatal("resume")
	}
	j := s.waitJob()
	// The failed attachment is retried once when the list is done, so nothing is left failed.
	if j["total"].(float64) != 3 || j["done"].(float64)+j["failed"].(float64) != 3 || j["failed"].(float64) != 0 {
		t.Fatalf("resumed job: %v", j)
	}
	if errs, _ := j["errors"].([]any); len(errs) != 1 || !strings.Contains(errs[0].(string), "InternalError") {
		t.Fatalf("failure not reported: %v", j["errors"])
	}
	var offloaded int
	_ = s.srv.store.Pool.QueryRow(context.Background(), `SELECT count(DISTINCT media_id) FROM gocdn_items`).Scan(&offloaded)
	if offloaded != 2 {
		t.Fatalf("resumed job offloaded %d attachments, want 2 (one skipped as done, one failed then retried)", offloaded)
	}
	if code, csv, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload&action=errors-csv"); code != http.StatusOK || !strings.Contains(csv, "InternalError") {
		t.Fatalf("errors CSV: %d %q", code, csv)
	}
}

// TestGoCDNJobPermanentFailureAndParallel: a job processes attachments in parallel batches and
// keeps what still fails after the retry pass.
func TestGoCDNJobPermanentFailureAndParallel(t *testing.T) {
	s := gocdnSite(t)
	for i := 0; i < 5; i++ {
		s.upload("pic.png", gocdnTestImage(50+i, 50, "png"))
	}
	s.configure(nil)
	s.fake.FailNext = 1000
	if err := s.srv.gocdnStartJob(context.Background(), "offload", false); err != nil {
		t.Fatal(err)
	}
	j := s.waitJob()
	if j["finished"] != true || j["failed"].(float64) != 5 || j["done"].(float64) != 0 {
		t.Fatalf("job: %v", j)
	}
	if csv := s.srv.gocdnErrorsCSV(); strings.Count(csv, "InternalError") < 5 {
		t.Fatalf("CSV lacks the failures: %q", csv)
	}
	s.fake.FailNext = 0
	if err := s.srv.gocdnStartJob(context.Background(), "offload", false); err != nil {
		t.Fatal(err)
	}
	if j := s.waitJob(); j["done"].(float64) != 5 || j["failed"].(float64) != 0 {
		t.Fatalf("second run: %v", j)
	}
}

// TestGoCDNHousekeeping: strays are deleted, a failed bucket delete is retried, and an interrupted
// job continues without anyone pressing Resume.
func TestGoCDNHousekeeping(t *testing.T) {
	s := gocdnSite(t)
	id := int64(s.upload("keep.png", gocdnTestImage(60, 60, "png"))["id"].(float64))
	s.configure(nil)
	ctx := context.Background()
	if code, _, _ := s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}}); code != http.StatusFound {
		t.Fatal("save offload settings")
	}
	if err := s.srv.gocdnStartJob(ctx, "offload", false); err != nil {
		t.Fatal(err)
	}
	s.waitJob()
	rel := mustAttachment(t, s, id).FilePath
	local := filepath.Join(s.srv.cfg.UploadsDir, filepath.FromSlash(rel))
	if _, err := os.Stat(local); err == nil {
		t.Fatal("local copy was not removed")
	}
	// A fetched-back copy left on disk (the timer was lost): old enough, it goes; a fresh one stays.
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := s.srv.gocdnCleanupLocal(ctx); n != 0 {
		t.Fatalf("a fresh file was removed (%d)", n)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(local, old, old)
	if n := s.srv.gocdnCleanupLocal(ctx); n != 1 {
		t.Fatalf("cleanup removed %d files, want 1", n)
	}
	if _, err := os.Stat(local); err == nil {
		t.Fatal("stray file still on disk")
	}
	// Deleting the attachment while the bucket refuses: the object is remembered and retried.
	s.fake.FailNext = 1000
	a, _ := s.srv.store.GetAttachment(ctx, id)
	s.srv.gocdnBeforeDelete(a)
	s.fake.FailNext = 0
	if len(s.srv.gocdnPendingDeletes()) == 0 {
		t.Fatal("failed delete not remembered")
	}
	if left := s.srv.gocdnRetryPendingDeletes(ctx); left != 0 {
		t.Fatalf("%d deletes still pending", left)
	}
	if keys := s.fake.Keys("media"); len(keys) != 0 {
		t.Fatalf("objects left in the bucket: %v", keys)
	}
}

func mustAttachment(t *testing.T, s *gocdnTestSite, id int64) *gocdnAttachmentT {
	t.Helper()
	a, err := s.srv.store.GetAttachment(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return &gocdnAttachmentT{FilePath: a.FilePath}
}

type gocdnAttachmentT struct{ FilePath string }

func TestGoCDNRemoteBrowserAndImport(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"delivery-domain": {"https://media.cdn.test"}})
	s.fake.PutObject("media", "wp-content/uploads/archive/2019/cover.png", gocdnTestImage(320, 200, "png"), "image/png")
	s.fake.PutObject("media", "wp-content/uploads/archive/2019/notes.txt", []byte("hello"), "text/plain")
	s.fake.PutObject("media", "wp-content/uploads/archive/2019/evil.html", []byte("<script>"), "text/html")
	s.fake.PutObject("media", "wp-content/uploads/archive/2020/a.jpg", gocdnTestImage(100, 100, "jpeg"), "image/jpeg")

	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote")
	if !strings.Contains(body, "archive/") || !strings.Contains(body, "Remote Storage") {
		t.Fatalf("root listing: %s", between(body, "gc-browser", "</table>"))
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote&prefix="+url.QueryEscape("wp-content/uploads/archive/2019/"))
	if !strings.Contains(body, "cover.png") || !strings.Contains(body, "notes.txt") || !strings.Contains(body, "action=preview") {
		t.Fatalf("folder listing: %s", between(body, "gc-browser", "</table>"))
	}
	if code, img, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote&action=preview&key="+url.QueryEscape("wp-content/uploads/archive/2019/cover.png")); code != http.StatusOK || !strings.HasPrefix(img, "\x89PNG") && !strings.HasPrefix(img, "\xff\xd8") {
		t.Fatalf("preview: %d", code)
	}
	// The Media menu has Remote Storage.
	if !strings.Contains(body, `gocdn?page=remote`) {
		t.Error("no Media › Remote Storage link")
	}
	nonce := formNonce(t, body)
	code, _, loc := post(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote", url.Values{"_wpnonce": {nonce}, "do": {"import"}, "mode": {"link"},
		"prefix": {"wp-content/uploads/archive/2019/"},
		"keys[]": {"wp-content/uploads/archive/2019/cover.png", "wp-content/uploads/archive/2019/evil.html"}})
	if code != http.StatusFound || !strings.Contains(loc, "1+object") || !strings.Contains(loc, "evil.html") {
		t.Fatalf("link import: %d %s", code, loc)
	}
	var id int64
	var fp string
	var w int
	if err := s.srv.store.Pool.QueryRow(context.Background(), `SELECT id, file_path, coalesce(width,0) FROM media WHERE file_path = 'archive/2019/cover.png'`).Scan(&id, &fp, &w); err != nil {
		t.Fatalf("linked attachment missing: %v", err)
	}
	if w != 320 {
		t.Errorf("linked image width %d", w)
	}
	resp := doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/archive/2019/cover.png", nil)
	readAll(resp)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://media.cdn.test/wp-content/uploads/archive/2019/cover.png" {
		t.Fatalf("linked file URL: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// It is in the media picker's query.
	_, ajax, _ := post(t, s.c, s.ts.URL+"/wp-admin/admin-ajax", url.Values{"action": {"query-attachments"}})
	if !strings.Contains(ajax, `"filename":"cover.png"`) {
		t.Errorf("the linked attachment is not in the media picker: %.300s", ajax)
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote&prefix="+url.QueryEscape("wp-content/uploads/archive/2019/"))
	if !strings.Contains(body, "In library") {
		t.Error("the imported object is not marked as in the library")
	}
	// Copy import: a normal upload, offloaded again under its own key.
	code, _, loc = post(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote", url.Values{"_wpnonce": {nonce}, "do": {"import"}, "mode": {"copy"},
		"keys[]": {"wp-content/uploads/archive/2020/a.jpg"}})
	if code != http.StatusFound || !strings.Contains(loc, "1+object") {
		t.Fatalf("copy import: %d %s", code, loc)
	}
	var cp string
	if err := s.srv.store.Pool.QueryRow(context.Background(), `SELECT file_path FROM media WHERE title = 'a' ORDER BY id DESC LIMIT 1`).Scan(&cp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.srv.cfg.UploadsDir, filepath.FromSlash(cp))); err != nil {
		t.Fatalf("copied file not on the server: %v", err)
	}
	if s.fake.Object("media", "wp-content/uploads/"+cp) == nil {
		t.Fatal("the copied attachment was not offloaded")
	}
	// Pagination with continuation tokens.
	for i := 0; i < 55; i++ {
		s.fake.PutObject("media", "wp-content/uploads/bulk/f"+itoa64(int64(100+i))+".txt", []byte("x"), "text/plain")
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote&prefix="+url.QueryEscape("wp-content/uploads/bulk/"))
	m := regexp.MustCompile(`token=([^"]+)">Next page`).FindStringSubmatch(body)
	if m == nil || strings.Count(body, `name="keys[]"`) != 50 {
		t.Fatalf("first page: %d objects, next %v", strings.Count(body, `name="keys[]"`), m)
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=remote&prefix="+url.QueryEscape("wp-content/uploads/bulk/")+"&token="+html.UnescapeString(m[1]))
	if strings.Count(body, `name="keys[]"`) != 5 || strings.Contains(body, "Next page") {
		t.Fatalf("second page: %d objects", strings.Count(body, `name="keys[]"`))
	}
}

func TestGoCDNImageService(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	att := s.upload("landscape.jpg", gocdnTestImage(1200, 800, "jpeg"))
	src := att["source_url"].(string)
	rel := src[strings.Index(src, "/wp-content/uploads/")+len("/wp-content/uploads/"):]
	if code, _, _ := s.pageForm("images", url.Values{"img-enabled": {"1"}, "img-sign": {"1"}, "img-max-width": {"1000"}, "img-max-height": {"1000"},
		"img-quality": {"70"}, "img-webp": {"1"}, "img-cache-bucket": {"1"}, "img-rewrite-quality": {"80"}, "img-max-source-mp": {"60"}}); code != http.StatusFound {
		t.Fatal("save image settings")
	}
	signed := s.srv.gocdnImageURL(context.Background(), rel, gocdn.ImageParams{W: 300, H: 300, Fit: "cover"})
	signed = s.ts.URL + signed[strings.Index(signed, "/cdn-img/"):]
	anon := gocdnAnon()

	resp := doGet(t, anon, signed, map[string]string{"Accept": "image/avif,image/webp,*/*"})
	body := readAll(resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/webp" || !bytes.HasPrefix(body, []byte("RIFF")) {
		t.Fatalf("WebP variant: %d %s %.80s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept") || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") || resp.Header.Get("ETag") == "" {
		t.Errorf("headers: %v", resp.Header)
	}
	etag := resp.Header.Get("ETag")
	resp = doGet(t, anon, signed, map[string]string{"Accept": "image/webp", "If-None-Match": etag})
	readAll(resp)
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", resp.StatusCode)
	}
	resp = doGet(t, anon, signed, map[string]string{"Accept": "image/webp"})
	readAll(resp)
	if resp.Header.Get("X-GoCDN-Cache") != "disk" {
		t.Errorf("second request not served from the variant cache: %s", resp.Header.Get("X-GoCDN-Cache"))
	}
	resp = doGet(t, anon, signed, map[string]string{"Accept": "image/png,image/*"})
	jb := readAll(resp)
	if resp.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("fallback format: %s", resp.Header.Get("Content-Type"))
	}
	if img, err := jpeg.Decode(bytes.NewReader(jb)); err != nil || img.Bounds().Dx() != 300 || img.Bounds().Dy() != 300 {
		t.Fatalf("cover crop: %v %v", err, img)
	}
	// The variant was also stored in the bucket (in the background).
	deadline := time.Now().Add(5 * time.Second)
	for {
		found := false
		for _, k := range s.fake.Keys("media") {
			if strings.HasPrefix(k, "gocdn-cache/") {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no variant in the bucket cache")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Signatures: missing, tampered and re-ordered parameters.
	unsigned := s.ts.URL + "/cdn-img/" + rel + "?w=300&h=300&fit=cover"
	if resp := doGet(t, anon, unsigned, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("unsigned request: %d", resp.StatusCode)
	}
	if resp := doGet(t, anon, strings.Replace(signed, "w=300", "w=301", 1), nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("tampered request: %d", resp.StatusCode)
	}
	u, _ := url.Parse(signed)
	reordered := s.ts.URL + u.Path + "?s=" + u.Query().Get("s") + "&fit=crop&h=300&w=300"
	if resp := doGet(t, anon, reordered, nil); resp.StatusCode != http.StatusOK {
		t.Errorf("re-ordered parameters with an alias: %d", resp.StatusCode)
	}
	// Limits.
	big := s.srv.gocdnImageURL(context.Background(), rel, gocdn.ImageParams{W: 5000})
	if resp := doGet(t, anon, s.ts.URL+big[strings.Index(big, "/cdn-img/"):], nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("over the size limit: %d", resp.StatusCode)
	}
	if resp := doGet(t, anon, s.ts.URL+"/cdn-img/../../etc/passwd?w=1", nil); resp.StatusCode == http.StatusOK {
		t.Error("path traversal answered")
	}
	// Parameters on uploads URLs, signing off; a removed local file is read from the bucket.
	if code, _, _ := s.pageForm("images", url.Values{"img-enabled": {"1"}, "img-max-width": {"1000"}, "img-max-height": {"1000"}, "img-quality": {"70"}, "img-webp": {"1"}}); code != http.StatusFound {
		t.Fatal("save image settings")
	}
	a, _ := s.srv.store.GetAttachment(context.Background(), int64(att["id"].(float64)))
	if _, err := s.srv.gocdnOffloadAttachment(context.Background(), s.srv.gocdnLoad(), mustClient(t, s.srv), a); err != nil {
		t.Fatal(err)
	}
	s.srv.gocdnRemoveLocal(context.Background(), a.ID)
	resp = doGet(t, anon, s.ts.URL+"/wp-content/uploads/"+rel+"?w=200&fm=png", nil)
	pb := readAll(resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("uploads URL with parameters: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if img, err := png.Decode(bytes.NewReader(pb)); err != nil || img.Bounds().Dx() != 200 || img.Bounds().Dy() != 133 {
		t.Fatalf("resize from the bucket: %v", err)
	}
}

func mustClient(t *testing.T, s *Server) *gocdn.Client {
	t.Helper()
	cl, err := s.gocdnClient(s.gocdnLoad())
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func TestGoCDNContentImagesThroughService(t *testing.T) {
	s := gocdnSite(t)
	att := s.upload("pic.jpg", gocdnTestImage(400, 300, "jpeg"))
	src := att["source_url"].(string)
	if code, _, _ := s.pageForm("images", url.Values{"img-enabled": {"1"}, "img-sign": {"1"}, "img-max-width": {"2000"}, "img-max-height": {"2000"},
		"img-quality": {"70"}, "img-webp": {"1"}, "img-rewrite": {"1"}, "img-rewrite-quality": {"77"}, "img-max-source-mp": {"60"}}); code != http.StatusFound {
		t.Fatal("save image settings")
	}
	code, p, raw := s.restJSON(http.MethodPost, "/wp-json/wp/v2/posts", map[string]any{"title": "Img", "content": `<img src="` + src + `" alt="">`, "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("create post: %d %.200s", code, raw)
	}
	page := string(readAll(doGet(t, gocdnAnon(), p["link"].(string), nil)))
	m := regexp.MustCompile(`<img[^>]* src="([^"]*/cdn-img/[^"]+)"`).FindStringSubmatch(page)
	if m == nil || !strings.Contains(m[1], "q=77") || !strings.Contains(m[1], "s=") {
		t.Fatalf("content image not routed through the service: %s", between(page, "<img", ">"))
	}
	resp := doGet(t, gocdnAnon(), html.UnescapeString(m[1]), map[string]string{"Accept": "image/webp"})
	readAll(resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/webp" {
		t.Fatalf("rewritten image URL: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestGoCDNAssetsRewrite(t *testing.T) {
	s := gocdnSite(t)
	if code, _, loc := s.pageForm("assets", url.Values{"assets-enabled": {"1"}, "assets-url": {"https://cdn.example.net"},
		"assets-dirs": {"/wp-content/themes/\n/wp-includes/\n/wp-admin/edit/"}, "assets-extensions": {"css,js,png,woff2"}, "assets-exclude": {"skip-me"}}); code != http.StatusFound || !strings.Contains(loc, "gocdn-msg") {
		t.Fatalf("save assets: %d %s", code, loc)
	}
	resp := doGet(t, gocdnAnon(), s.ts.URL+"/", nil)
	page := string(readAll(resp))
	if !strings.Contains(page, "https://cdn.example.net/wp-") {
		t.Fatalf("no asset URL rewritten on the front page")
	}
	if regexp.MustCompile(`(src|href)="(` + regexp.QuoteMeta(s.ts.URL) + `)?/wp-includes/[^"]+\.(js|css)`).MatchString(page) {
		t.Errorf("a core asset was left on the origin: %s", regexp.MustCompile(`(src|href)="[^"]*/wp-includes/[^"]+"`).FindString(page))
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "https://cdn.example.net") {
		t.Errorf("CSP lacks the CDN: %s", csp)
	}
	_, admin, _ := get(t, s.c, s.ts.URL+"/wp-admin/")
	if strings.Contains(admin, "cdn.example.net") {
		t.Error("wp-admin was rewritten")
	}
	// Unit checks of the rewriter.
	st := gocdnSettings{AssetsURL: "https://cdn.x", AssetsDirs: "/wp-includes/\n/wp-admin/edit/\n/wp-content/uploads/", AssetsExt: "js,css", AssetsExclude: "skip-me"}
	hosts := map[string]bool{"site.test": true}
	in := `<script src="/wp-includes/a.js?ver=1"></script><link href="http://site.test/wp-includes/b.css"><script src="https://other.test/wp-includes/c.js"></script>` +
		`<a href="/wp-includes/page/">x</a><script src="/wp-includes/skip-me.js"></script><img src="/wp-includes/i.png"><p>see/wp-includes/x.js</p><a href="/wp-admin/edit/x.js">`
	out := string(gocdnRewriteAssets(st, []byte(in), hosts))
	for _, want := range []string{`src="https://cdn.x/wp-includes/a.js?ver=1"`, `href="https://cdn.x/wp-includes/b.css"`, `src="https://other.test/wp-includes/c.js"`,
		`href="/wp-includes/page/"`, `src="/wp-includes/skip-me.js"`, `src="/wp-includes/i.png"`, `see/wp-includes/x.js`, `href="/wp-admin/edit/x.js"`} {
		if !strings.Contains(out, want) {
			t.Errorf("rewritten assets lack %s:\n%s", want, out)
		}
	}
}

func TestGoCDNCloudflarePurge(t *testing.T) {
	var mu sync.Mutex
	var calls []map[string]any
	var auth []string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, body)
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		if !strings.HasSuffix(r.URL.Path, "/zones/"+strings.Repeat("b", 32)+"/purge_cache") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":7003,"message":"Could not route"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"id":"x"}}`))
	}))
	defer cf.Close()
	old := gocdnCFBase
	gocdnCFBase = cf.URL
	defer func() { gocdnCFBase = old }()

	s := gocdnSite(t)
	if code, _, _ := s.pageForm("purge", url.Values{"cloudflare": {"1"}, "cloudflare-zone": {strings.Repeat("b", 32)}, "cloudflare-token": {"tok-123"}}); code != http.StatusFound {
		t.Fatal("save Cloudflare")
	}
	code, _, loc := s.pageForm("purge", url.Values{"do": {"purge-cloudflare"}})
	if code != http.StatusFound || !strings.Contains(loc, "gocdn-msg") {
		t.Fatalf("purge: %d %s", code, loc)
	}
	mu.Lock()
	if len(calls) != 1 || calls[0]["purge_everything"] != true || auth[0] != "Bearer tok-123" {
		t.Fatalf("Cloudflare calls %v %v", calls, auth)
	}
	mu.Unlock()
	// Files are purged 30 per request; API errors are reported.
	st := s.srv.gocdnLoad()
	var urls []string
	for i := 0; i < 61; i++ {
		urls = append(urls, "https://x.test/"+itoa64(int64(i)))
	}
	if err := s.srv.gocdnCloudflarePurge(context.Background(), st, urls); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(calls) != 4 || len(calls[3]["files"].([]any)) != 1 {
		t.Fatalf("batched purge: %d calls", len(calls))
	}
	mu.Unlock()
	st.CFZone = strings.Repeat("c", 32)
	if err := s.srv.gocdnCloudflarePurge(context.Background(), st, nil); err == nil || !strings.Contains(err.Error(), "7003") {
		t.Fatalf("API error not reported: %v", err)
	}
	// The page cache purge button.
	if code, _, loc := s.pageForm("purge", url.Values{"do": {"purge-cache"}}); code != http.StatusFound || !strings.Contains(loc, "Pursae+Cache+purged") {
		t.Fatalf("purge cache: %d %s", code, loc)
	}
}

func TestGoCDNEnvSettingsLock(t *testing.T) {
	s := gocdnSite(t)
	t.Setenv(gocdnEnv, `{"provider":"minio","endpoint":"`+s.fake.URL+`","bucket":"media","access-key-id":"`+gocdnTestKey+`","secret-access-key":"`+gocdnTestSecret+`","use-path-style":true,"region":"auto"}`)
	st := s.srv.gocdnLoad()
	if st.Bucket != "media" || s.srv.gocdnReveal(st.SecretKey) != gocdnTestSecret || !s.srv.gocdnConfigured(st) {
		t.Fatalf("environment settings not used: %+v", st.Bucket)
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	if strings.Contains(body, gocdnTestSecret) || !strings.Contains(body, "set in "+gocdnEnv) {
		t.Fatal("the environment secret is printed or not shown as locked")
	}
	// Saving the form neither overrides nor stores the locked values.
	if code, _, _ := post(t, s.c, s.ts.URL+"/wp-admin/gocdn", url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"save"}, "bucket": {"other"}, "delivery-domain": {"https://d.test"}}); code != http.StatusFound {
		t.Fatal("save")
	}
	stored, _ := s.srv.settings.Get(gocdnOption).(map[string]any)
	if _, ok := stored["secret-access-key"]; ok {
		t.Error("the environment secret was written to the database")
	}
	if st := s.srv.gocdnLoad(); st.Bucket != "media" || st.CDNDomain != "https://d.test" {
		t.Errorf("locked bucket changed or free field not saved: %q %q", st.Bucket, st.CDNDomain)
	}
}

func TestGoCDNPublicBase(t *testing.T) {
	for _, c := range []struct {
		st   gocdnSettings
		want string
	}{
		{gocdnSettings{Provider: "aws", Bucket: "b", Region: "eu-west-1"}, "https://b.s3.eu-west-1.amazonaws.com"},
		{gocdnSettings{Provider: "do", Bucket: "b"}, "https://b.nyc3.digitaloceanspaces.com"},
		{gocdnSettings{Provider: "b2", Bucket: "b", Region: "eu-central-003"}, "https://b.s3.eu-central-003.backblazeb2.com"},
		{gocdnSettings{Provider: "minio", Bucket: "b", Endpoint: "http://127.0.0.1:9000", PathStyle: true}, "http://127.0.0.1:9000/b"},
		{gocdnSettings{Provider: "r2", Bucket: "b", Endpoint: "https://acc.r2.cloudflarestorage.com"}, "https://b.acc.r2.cloudflarestorage.com"},
		{gocdnSettings{Provider: "r2", Bucket: "b", CDNDomain: "media.example.com"}, "https://media.example.com"},
	} {
		if got := gocdnPublicBase(c.st); got != c.want {
			t.Errorf("%+v: %q, want %q", c.st, got, c.want)
		}
	}
	if gocdnEndpoint(gocdnSettings{Provider: "r2"}) != "" {
		t.Error("the R2 placeholder endpoint is used")
	}
	if got := gocdnObjectURL(gocdnSettings{CDNDomain: "https://c.test"}, "wp-content/uploads/a b.jpg"); got != "https://c.test/wp-content/uploads/a%20b.jpg" {
		t.Errorf("object URL %q", got)
	}
}

func TestGoCDNDeactivateRestoresLocalServing(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"delivery-domain": {"https://media.cdn.test"}})
	att := s.upload("x.png", gocdnTestImage(60, 40, "png"))
	src := att["source_url"].(string)
	_, view, _ := s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(int64(att["id"].(float64))), nil)
	if !strings.HasPrefix(view["source_url"].(string), "https://media.cdn.test/") {
		t.Fatalf("not rewritten while active: %v", view["source_url"])
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=deactivate&amp;plugin=gocdn&amp;[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Deactivate link")
	}
	get(t, s.c, s.ts.URL+html.UnescapeString(m[1]))
	_, view, _ = s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(int64(att["id"].(float64))), nil)
	if view["source_url"] != src {
		t.Fatalf("still rewritten after deactivation: %v", view["source_url"])
	}
	if code, _, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn"); code != http.StatusNotFound {
		t.Errorf("screen after deactivation: %d", code)
	}
}
