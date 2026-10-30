package server

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"html"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"wpgo/internal/builder"
	"wpgo/internal/gocdn"
	"wpgo/internal/gocdn/s3fake"
)

// relOf is the uploads path of an attachment's source URL.
func gocdnRelOfURL(src string) string {
	return src[strings.Index(src, "/wp-content/uploads/")+len("/wp-content/uploads/"):]
}

func gocdnAnimGIF(frames, w, h int) []byte {
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 255, 0, 255}}
	g := &gif.GIF{Config: image.Config{Width: w, Height: h, ColorModel: pal}}
	for i := 0; i < frames; i++ {
		f := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for p := range f.Pix {
			f.Pix[p] = uint8(i % 3)
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 7)
	}
	var buf bytes.Buffer
	_ = gif.EncodeAll(&buf, g)
	return buf.Bytes()
}

// waitJobDirect waits for the background job without the (plugin-only) progress endpoint.
func (s *gocdnTestSite) waitJobDirect() gocdnJobState {
	s.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j := s.srv.gocdnJob()
		if !j.Running && (j.Finished || j.Stopped) {
			return j
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatal("the job did not finish")
	return gocdnJobState{}
}

func TestGoCDNMultipartSSEStorageClass(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"multipart-threshold-mb": {"5"}, "server-side-encryption": {"1"}, "storage-class": {"standard_ia"}})
	st := s.srv.gocdnLoad()
	if st.MultipartMB != 5 || !st.SSE || st.StorageClass != "STANDARD_IA" {
		t.Fatalf("settings %d %v %q", st.MultipartMB, st.SSE, st.StorageClass)
	}
	big := bytes.Repeat([]byte("0123456789abcdef"), (6<<20)/16+100) // ~6 MB
	att := s.upload("big-archive.zip", big)
	rel := gocdnRelOfURL(att["source_url"].(string))
	o := s.fake.Object("media", "wp-content/uploads/"+rel)
	if o == nil || !bytes.Equal(o.Body, big) {
		t.Fatal("the large file is not in the bucket")
	}
	if o.Parts != 2 || o.SSE != "AES256" || o.StorageClass != "STANDARD_IA" || o.ACL != "public-read" || o.ContentType != "application/zip" {
		t.Fatalf("object: %d parts, SSE %q, class %q, ACL %q, type %q", o.Parts, o.SSE, o.StorageClass, o.ACL, o.ContentType)
	}
	img := s.upload("small.png", gocdnTestImage(40, 40, "png"))
	so := s.fake.Object("media", "wp-content/uploads/"+gocdnRelOfURL(img["source_url"].(string)))
	if so == nil || so.Parts != 0 || so.SSE != "AES256" || so.StorageClass != "STANDARD_IA" {
		t.Fatalf("small object %+v", so)
	}
	if s.fake.OpenUploads() != 0 || len(s.fake.Errors) != 0 {
		t.Fatalf("open uploads %d, errors %v", s.fake.OpenUploads(), s.fake.Errors)
	}
	// An invalid storage class is refused by the form.
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"save"}, "bucket": {"media"}, "storage-class": {"cold storage!"}}
	if _, _, loc := post(t, s.c, s.ts.URL+"/wp-admin/gocdn", form); !strings.Contains(loc, "gocdn-error") {
		t.Errorf("invalid storage class accepted: %s", loc)
	}
	if !strings.Contains(body, `name="server-side-encryption" value="1" checked`) || !strings.Contains(body, `value="STANDARD_IA"`) {
		t.Error("the Storage screen does not show the SSE / storage class settings")
	}
}

func TestGoCDNImageAVIFAndAnimation(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	att := s.upload("photo.jpg", gocdnTestImage(800, 500, "jpeg"))
	rel := gocdnRelOfURL(att["source_url"].(string))
	animAtt := s.upload("spinner.gif", gocdnAnimGIF(6, 120, 80))
	animRel := gocdnRelOfURL(animAtt["source_url"].(string))
	save := func(extra url.Values) {
		form := url.Values{"img-enabled": {"1"}, "img-max-width": {"2000"}, "img-max-height": {"2000"}, "img-quality": {"70"}, "img-webp": {"1"},
			"img-rewrite-quality": {"80"}, "img-max-source-mp": {"60"}, "img-max-frames": {"50"}}
		for k, v := range extra {
			form[k] = v
		}
		if code, _, _ := s.pageForm("images", form); code != http.StatusFound {
			t.Fatal("save image settings")
		}
	}
	save(url.Values{"img-avif": {"1"}})
	anon := gocdnAnon()
	chrome := map[string]string{"Accept": "image/avif,image/webp,image/apng,*/*;q=0.8"}
	resp := doGet(t, anon, s.ts.URL+"/cdn-img/"+rel+"?w=200", chrome)
	body := readAll(resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/avif" || !bytes.Contains(body[:32], []byte("ftypavif")) {
		t.Fatalf("AVIF variant: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(resp.Header.Get("Vary"), "Accept") {
		t.Error("no Vary: Accept")
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(body)); err != nil || cfg.Width != 200 {
		t.Errorf("AVIF does not decode to 200 px: %+v %v", cfg, err)
	}
	resp = doGet(t, anon, s.ts.URL+"/cdn-img/"+rel+"?w=200", map[string]string{"Accept": "image/webp,*/*"})
	readAll(resp)
	if resp.Header.Get("Content-Type") != "image/webp" {
		t.Errorf("WebP-only browser got %s", resp.Header.Get("Content-Type"))
	}
	resp = doGet(t, anon, s.ts.URL+"/cdn-img/"+rel+"?w=200&fm=avif", map[string]string{"Accept": "*/*"})
	readAll(resp)
	if resp.Header.Get("Content-Type") != "image/avif" {
		t.Errorf("fm=avif got %s", resp.Header.Get("Content-Type"))
	}
	// The animated GIF stays animated (all frames, resized), whatever the browser accepts.
	resp = doGet(t, anon, s.ts.URL+"/cdn-img/"+animRel+"?w=60", chrome)
	body = readAll(resp)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/gif" {
		t.Fatalf("animated GIF: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	g, err := gif.DecodeAll(bytes.NewReader(body))
	if err != nil || len(g.Image) != 6 || g.Config.Width != 60 || g.Config.Height != 40 || g.Delay[0] != 7 {
		t.Fatalf("animation: err %v, %d frames, %dx%d", err, len(g.Image), g.Config.Width, g.Config.Height)
	}
	// Over the frame budget: the first frame only, still a GIF (a different variant).
	save(url.Values{"img-avif": {"1"}, "img-max-frames": {"3"}})
	resp = doGet(t, anon, s.ts.URL+"/cdn-img/"+animRel+"?w=60", chrome)
	body = readAll(resp)
	g, err = gif.DecodeAll(bytes.NewReader(body))
	if err != nil || len(g.Image) != 1 || resp.Header.Get("X-GoCDN-Animation") != "flattened" {
		t.Fatalf("over budget: err %v, frames %d, header %q", err, len(g.Image), resp.Header.Get("X-GoCDN-Animation"))
	}
	// An explicit still format is honoured.
	resp = doGet(t, anon, s.ts.URL+"/cdn-img/"+animRel+"?w=60&fm=png", chrome)
	readAll(resp)
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("fm=png of an animation: %s", resp.Header.Get("Content-Type"))
	}
	// AVIF off: AVIF-accepting browsers get WebP, fm=avif falls back to WebP.
	save(nil)
	resp = doGet(t, anon, s.ts.URL+"/cdn-img/"+rel+"?w=200&fm=avif", chrome)
	readAll(resp)
	if resp.Header.Get("Content-Type") != "image/webp" {
		t.Errorf("AVIF off, fm=avif: %s", resp.Header.Get("Content-Type"))
	}
	if !gocdnGIFAnimated(gocdnAnimGIF(2, 4, 4)) || gocdnGIFAnimated(gocdnAnimGIF(1, 4, 4)) || gocdnGIFAnimated([]byte("GIF89a")) {
		t.Error("gocdnGIFAnimated")
	}
}

func TestGoCDNMoveToNewBucket(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"public-read": {"1"}})
	if code, _, _ := s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}}); code != http.StatusFound {
		t.Fatal("save offload settings")
	}
	att := s.upload("old.png", gocdnTestImage(300, 200, "png"))
	id := int64(att["id"].(float64))
	rel := gocdnRelOfURL(att["source_url"].(string))
	oldKey := "wp-content/uploads/" + rel
	if s.fake.Object("media", oldKey) == nil {
		t.Fatal("not offloaded")
	}
	// Another bucket on the same endpoint and a new path.
	s.fake.AddBucket("media2")
	s.configure(url.Values{"bucket": {"media2"}, "object-prefix": {"site/"}})
	st := s.srv.gocdnLoad()
	if len(st.Previous) != 1 || st.Previous[0].Bucket != "media" || !strings.HasPrefix(st.Previous[0].SecretKey, gocdnSealPrefix) {
		t.Fatalf("previous connection not kept (sealed): %+v", st.Previous)
	}
	// Until moved: the old URL (old bucket) is used and the removed file is served from there.
	_, view, _ := s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(id), nil)
	if u := view["source_url"].(string); !strings.Contains(u, "/media/"+oldKey) {
		t.Fatalf("old item URL %s", u)
	}
	resp := doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	readAll(resp)
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "/media/"+oldKey) {
		t.Fatalf("removed file before the move: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload")
	if !strings.Contains(body, `value="move"`) || !strings.Contains(body, "Move to the current bucket") {
		t.Fatal("the move job is not offered")
	}
	files, _ := s.srv.gocdnMoveCount(context.Background(), st)
	if files < 2 {
		t.Fatalf("move count %d", files)
	}
	if code, _, _ := s.pageForm("offload", url.Values{"do": {"job"}, "kind": {"move"}, "delete-old": {"1"}}); code != http.StatusFound {
		t.Fatal("start move")
	}
	j := s.waitJob()
	if j["failed"].(float64) != 0 || j["done"].(float64) != 1 || j["files"].(float64) != float64(files) {
		t.Fatalf("move job %v", j)
	}
	newKey := "site/" + rel
	if o := s.fake.Object("media2", newKey); o == nil || o.ContentType != "image/png" || !strings.Contains(o.CacheControl, "immutable") {
		t.Fatalf("not moved to media2/%s", newKey)
	}
	if s.fake.Object("media", oldKey) != nil {
		t.Error("old object not deleted")
	}
	copied := false
	for _, r := range s.fake.Requests() {
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			copied = true
		}
	}
	if !copied {
		t.Error("same endpoint and key: CopyObject was not used")
	}
	it := s.srv.gocdnItemByPath(context.Background(), rel)
	if it == nil || it.Bucket != "media2" || it.Key != newKey || it.Endpoint != s.fake.URL {
		t.Fatalf("row not updated: %+v", it)
	}
	if len(s.srv.gocdnLoad().Previous) != 0 {
		t.Error("the unused previous connection was not dropped")
	}
	_, view, _ = s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(id), nil)
	if u := view["source_url"].(string); !strings.Contains(u, "/media2/"+newKey) {
		t.Fatalf("URL after the move %s", u)
	}

	// Another provider (other endpoint and credentials): downloaded and uploaded again; the old
	// object stays without "delete".
	other := s3fake.New("AKOTHER", "other-secret", "eu-west-1", "assets")
	defer other.Close()
	s.configure(url.Values{"endpoint": {other.URL}, "bucket": {"assets"}, "access-key-id": {"AKOTHER"}, "secret-access-key": {"other-secret"},
		"region": {"eu-west-1"}, "object-prefix": {"site/"}})
	// Still served from media2 (the remembered connection with the old credentials).
	resp = doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	readAll(resp)
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "/media2/"+newKey) {
		t.Fatalf("before the cross-provider move: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if err := s.srv.gocdnEnsureLocal(context.Background(), rel); err != nil {
		t.Fatalf("fetch back from the old provider: %v", err)
	}
	os.Remove(s.srv.gocdnLocalPath(rel))
	if code, _, _ := s.pageForm("offload", url.Values{"do": {"job"}, "kind": {"move"}}); code != http.StatusFound {
		t.Fatal("start move")
	}
	if j := s.waitJob(); j["failed"].(float64) != 0 {
		t.Fatalf("cross-provider move %v", j)
	}
	if o := other.Object("assets", newKey); o == nil || len(other.Errors) != 0 {
		t.Fatalf("not in the other provider: %v", other.Errors)
	}
	if s.fake.Object("media2", newKey) == nil {
		t.Error("the old object was deleted without being asked")
	}
	if it := s.srv.gocdnItemByPath(context.Background(), rel); it == nil || it.Bucket != "assets" || it.Endpoint != other.URL {
		t.Fatalf("row after the cross-provider move: %+v", it)
	}
}

// cloudfrontFake checks CreateInvalidation requests' SigV4 signature (service cloudfront).
func cloudfrontFake(t *testing.T, keyID, secret string, got *[]string, mu *sync.Mutex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cred, signed, sig, ok := gocdn.ParseAuthorization(r.Header.Get("Authorization"))
		tm, _ := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
		signer := gocdn.Signer{AccessKey: keyID, SecretKey: secret, Region: "us-east-1", Service: "cloudfront"}
		vals := map[string][]string{}
		for _, n := range strings.Split(signed, ";") {
			if n == "host" {
				vals[n] = []string{r.Host}
			} else {
				vals[n] = r.Header.Values(n)
			}
		}
		names, block := gocdn.CanonicalHeaderBlock(vals)
		creq := gocdn.CanonicalRequest(r.Method, gocdn.CanonicalURI(r.URL), gocdn.CanonicalQuery(r.URL.Query()), block, names, gocdn.SHA256Hex(body))
		if !ok || cred != keyID+"/"+signer.Scope(tm) || signer.SignatureFor(tm, creq) != sig || r.Header.Get("X-Amz-Content-Sha256") != gocdn.SHA256Hex(body) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<ErrorResponse><Error><Code>SignatureDoesNotMatch</Code><Message>bad signature</Message></Error></ErrorResponse>`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/2020-05-31/distribution/E2ABCDEF123/invalidation" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<ErrorResponse><Error><Code>NoSuchDistribution</Code><Message>The specified distribution does not exist.</Message></Error></ErrorResponse>`))
			return
		}
		var x struct {
			Quantity int      `xml:"Paths>Quantity"`
			Items    []string `xml:"Paths>Items>Path"`
			Ref      string   `xml:"CallerReference"`
		}
		if err := xml.Unmarshal(body, &x); err != nil || x.Quantity != len(x.Items) || x.Ref == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		*got = append(*got, strings.Join(x.Items, ","))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`<Invalidation><Id>I123</Id><Status>InProgress</Status></Invalidation>`))
	}))
}

func TestGoCDNPurgeProviders(t *testing.T) {
	var mu sync.Mutex
	var cf, bunny, keycdn, fastly []string
	cfSrv := cloudfrontFake(t, "AKCFKEY", "cf/secret+1", &cf, &mu)
	defer cfSrv.Close()
	bunnySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("AccessKey") != "bunny-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"Message":"Authorization has been denied for this request."}`))
			return
		}
		mu.Lock()
		bunny = append(bunny, r.Method+" "+r.URL.Path+"?"+r.URL.Query().Get("url"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer bunnySrv.Close()
	keySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "kc-key" || pass != "" {
			_, _ = w.Write([]byte(`{"status":"error","description":"Unauthorized"}`))
			return
		}
		var body struct {
			URLs []string `json:"urls"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		keycdn = append(keycdn, r.Method+" "+r.URL.Path+" "+strings.Join(body.URLs, ","))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"status":"success","description":"Cache has been cleared for zone 123."}`))
	}))
	defer keySrv.Close()
	fastlySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Fastly-Key") != "fastly-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"msg":"Provided credentials are missing or invalid"}`))
			return
		}
		mu.Lock()
		fastly = append(fastly, r.Method+" "+r.URL.Path)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer fastlySrv.Close()
	saved := []*string{&gocdnCloudFrontBase, &gocdnBunnyBase, &gocdnKeyCDNBase, &gocdnFastlyBase}
	olds := []string{gocdnCloudFrontBase, gocdnBunnyBase, gocdnKeyCDNBase, gocdnFastlyBase}
	gocdnCloudFrontBase, gocdnBunnyBase, gocdnKeyCDNBase, gocdnFastlyBase = cfSrv.URL, bunnySrv.URL, keySrv.URL, fastlySrv.URL
	defer func() {
		for i, p := range saved {
			*p = olds[i]
		}
	}()

	s := gocdnSite(t)
	urls := []string{"https://media.cdn.test/wp-content/uploads/a b.jpg", "https://media.cdn.test/wp-content/uploads/c.jpg?ver=2"}
	cases := []struct {
		provider, zone, keyID, secret string
		all, files                    func() string
	}{
		{"cloudfront", "E2ABCDEF123", "AKCFKEY", "cf/secret+1", func() string { return cf[0] }, func() string { return cf[1] }},
		{"bunny", "4242", "", "bunny-key", func() string { return bunny[0] }, func() string { return bunny[1] + " | " + bunny[2] }},
		{"keycdn", "123", "", "kc-key", func() string { return keycdn[0] }, func() string { return keycdn[1] }},
		{"fastly", "SU1Z0isxPaozGVKXdv0eY", "", "fastly-token", func() string { return fastly[0] }, func() string { return fastly[1] + " | " + fastly[2] }},
	}
	want := map[string][2]string{
		"cloudfront": {"/*", "/wp-content/uploads/a%20b.jpg,/wp-content/uploads/c.jpg?ver=2"},
		"bunny":      {"POST /pullzone/4242/purgeCache?", "POST /purge?" + urls[0] + " | POST /purge?" + urls[1]},
		"keycdn":     {"DELETE /zones/purge/123.json ", "DELETE /zones/purgeurl/123.json media.cdn.test/wp-content/uploads/a b.jpg,media.cdn.test/wp-content/uploads/c.jpg?ver=2"},
		"fastly":     {"POST /service/SU1Z0isxPaozGVKXdv0eY/purge_all", "POST /purge/media.cdn.test/wp-content/uploads/a b.jpg | POST /purge/media.cdn.test/wp-content/uploads/c.jpg"},
	}
	for _, c := range cases {
		// Choosing the provider shows its fields; then save them.
		if code, _, _ := s.pageForm("purge", url.Values{"purge-provider": {c.provider}}); code != http.StatusFound {
			t.Fatalf("%s: choose", c.provider)
		}
		_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=purge")
		if !strings.Contains(body, `name="purge-zone"`) || (c.keyID != "") != strings.Contains(body, `name="purge-access-key-id"`) {
			t.Fatalf("%s: fields not shown", c.provider)
		}
		if code, _, loc := s.pageForm("purge", url.Values{"purge-provider": {c.provider}, "purge-zone": {c.zone}, "purge-access-key-id": {c.keyID},
			"purge-secret": {c.secret}}); code != http.StatusFound || !strings.Contains(loc, "gocdn-msg") {
			t.Fatalf("%s: save %d %s", c.provider, code, loc)
		}
		raw, _ := json.Marshal(s.srv.settings.Get(gocdnOption))
		if strings.Contains(string(raw), c.secret) {
			t.Fatalf("%s: secret stored in clear", c.provider)
		}
		_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=purge")
		if strings.Contains(body, c.secret) || !strings.Contains(body, "saved — type to replace") {
			t.Fatalf("%s: secret echoed or not marked saved", c.provider)
		}
		if code, _, loc := s.pageForm("purge", url.Values{"do": {"purge-cdn"}}); code != http.StatusFound || !strings.Contains(loc, "gocdn-msg") {
			t.Fatalf("%s: purge everything: %s", c.provider, loc)
		}
		if err := s.srv.gocdnCDNPurge(context.Background(), s.srv.gocdnLoad(), urls); err != nil {
			t.Fatalf("%s: purge files: %v", c.provider, err)
		}
		mu.Lock()
		if got := c.all(); got != want[c.provider][0] {
			t.Errorf("%s everything: %q", c.provider, got)
		}
		if got := c.files(); got != want[c.provider][1] {
			t.Errorf("%s files: %q", c.provider, got)
		}
		mu.Unlock()
	}
	// Errors from the APIs are reported.
	st := s.srv.gocdnLoad()
	st.PurgeSecret = s.srv.gocdnSeal("wrong")
	if err := s.srv.gocdnCDNPurge(context.Background(), st, nil); err == nil || !strings.Contains(err.Error(), "credentials are missing") {
		t.Errorf("Fastly error: %v", err)
	}
	st.PurgeProvider, st.PurgeZone, st.PurgeKeyID = "cloudfront", "E2ABCDEF123", "AKCFKEY"
	if err := s.srv.gocdnCDNPurge(context.Background(), st, nil); err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Errorf("CloudFront error: %v", err)
	}
	st.PurgeProvider, st.PurgeZone = "keycdn", "123"
	if err := s.srv.gocdnCDNPurge(context.Background(), st, nil); err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("KeyCDN error: %v", err)
	}
	// Switching provider forgets the other provider's secret; an invalid zone is refused.
	if _, _, loc := s.pageForm("purge", url.Values{"purge-provider": {"bunny"}, "purge-zone": {"abc"}}); !strings.Contains(loc, "gocdn-error") {
		t.Errorf("invalid pull zone accepted: %s", loc)
	}
	s.pageForm("purge", url.Values{"purge-provider": {"bunny"}, "purge-zone": {"77"}})
	if st := s.srv.gocdnLoad(); st.PurgeSecret != "" || s.srv.gocdnPurgeReady(st) {
		t.Error("Fastly's token was kept for Bunny")
	}
	// Automatic purge of a deleted offloaded file goes to the configured provider.
	s.configure(url.Values{"delivery-domain": {"https://media.cdn.test"}})
	s.pageForm("purge", url.Values{"purge-provider": {"fastly"}, "purge-zone": {"SU1Z0isxPaozGVKXdv0eY"}, "purge-secret": {"fastly-token"}, "cloudflare-auto": {"1"}})
	att := s.upload("gone.png", gocdnTestImage(20, 20, "png"))
	mu.Lock()
	before := len(fastly)
	mu.Unlock()
	if code, _, _ := s.restJSON(http.MethodDelete, "/wp-json/wp/v2/media/"+itoa64(int64(att["id"].(float64)))+"?force=true", nil); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(fastly)
		var last string
		if n > 0 {
			last = fastly[n-1]
		}
		mu.Unlock()
		if n > before && strings.Contains(last, "/purge/media.cdn.test/wp-content/uploads/") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deleted file not purged at Fastly: %v", fastly[before:])
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGoCDNFetchBack(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	if code, _, _ := s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}}); code != http.StatusFound {
		t.Fatal("save offload settings")
	}
	att := s.upload("icon.png", gocdnTestImage(600, 600, "png"))
	id := int64(att["id"].(float64))
	rel := gocdnRelOfURL(att["source_url"].(string))
	local := s.srv.gocdnLocalPath(rel)
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("the local copy was not removed")
	}
	old := gocdnReRemoveDelay
	gocdnReRemoveDelay = 200 * time.Millisecond
	defer func() { gocdnReRemoveDelay = old }()

	// The helper: a removed file is back on disk for the reader, then removed again.
	if got := s.srv.gocdnFetchBack(context.Background(), local); got != local {
		t.Fatalf("returned %q", got)
	}
	if _, err := os.Stat(local); err != nil {
		t.Fatal("not fetched back")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(local); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fetched-back copy was not removed again")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Outside the uploads directory, or an untracked file: nothing happens.
	if s.srv.gocdnRelOf("/etc/passwd") != "" || s.srv.gocdnRelOf(s.srv.cfg.UploadsDir) != "" {
		t.Error("gocdnRelOf accepted a path outside the uploads")
	}

	// Customizer site icon: the sizes are made from the removed original and offloaded.
	s.srv.siteIconSizes(context.Background(), id)
	a, _ := s.srv.store.GetAttachment(context.Background(), id)
	v := a.Variant("site_icon-512")
	if v == nil {
		t.Fatal("site icon sizes not generated from a removed file")
	}
	if it := s.srv.gocdnItemByPath(context.Background(), v.FilePath); it == nil || s.fake.Object("media", it.Key) == nil {
		t.Fatal("the new site icon size was not offloaded")
	}

	// Golementor's SVG loader reads a removed SVG.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="red"/></svg>`)
	satt := s.upload("logo.svg", svg)
	srel := gocdnRelOfURL(satt["source_url"].(string))
	if _, err := os.Stat(s.srv.gocdnLocalPath(srel)); !os.IsNotExist(err) {
		t.Fatal("svg not removed")
	}
	s.srv.gweSetSVGLoader()
	if out := builder.SVGLoader(int64(satt["id"].(float64))); !strings.Contains(out, "<rect") {
		t.Fatalf("SVG loader on a removed file: %q", out)
	}

	// Plugin off without the fallback (nothing removed is tracked): no fetch.
	os.Remove(s.srv.gocdnLocalPath(srel))
	_ = s.srv.settings.Set(context.Background(), map[string]any{gocdnSetting: false, gocdnFallbackOption: false})
	s.srv.gocdnFetchBack(context.Background(), s.srv.gocdnLocalPath(srel))
	if _, err := os.Stat(s.srv.gocdnLocalPath(srel)); !os.IsNotExist(err) {
		t.Error("fetched back while GoCDN is off without the fallback")
	}
}

func TestGoCDNSafeDeactivation(t *testing.T) {
	s := gocdnSite(t)
	s.configure(url.Values{"delivery-domain": {"https://media.cdn.test"}})
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}})
	att := s.upload("kept.png", gocdnTestImage(200, 100, "png"))
	rel := gocdnRelOfURL(att["source_url"].(string))
	deactivateLink := func() string {
		_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/plugins")
		m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=deactivate&amp;plugin=gocdn&amp;[^"]*)"`).FindStringSubmatch(body)
		if m == nil {
			t.Fatal("no Deactivate link")
		}
		return s.ts.URL + html.UnescapeString(m[1])
	}
	code, _, loc := get(t, s.c, deactivateLink())
	if code != http.StatusFound || !strings.Contains(loc, "/wp-admin/gocdn?page=deactivate") || !s.srv.gocdnOn() {
		t.Fatalf("deactivate with removed files: %d %s (on %v)", code, loc, s.srv.gocdnOn())
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=deactivate")
	if !strings.Contains(body, "deactivate-download") || !strings.Contains(body, "deactivate-keep") || !strings.Contains(body, "exist only in the bucket") {
		t.Fatal("confirmation screen")
	}
	// Keep serving from the bucket.
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"deactivate-keep"}}
	if code, _, loc := post(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=deactivate", form); code != http.StatusFound || !strings.Contains(loc, "deactivate=true") {
		t.Fatalf("deactivate-keep: %d %s", code, loc)
	}
	if s.srv.gocdnOn() || !s.srv.settings.Bool(gocdnFallbackOption) {
		t.Fatal("not deactivated with the fallback")
	}
	resp := doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	readAll(resp)
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "https://media.cdn.test/wp-content/uploads/") {
		t.Fatalf("removed file while off: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, view, _ := s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(int64(att["id"].(float64))), nil)
	if strings.Contains(view["source_url"].(string), "media.cdn.test") {
		t.Error("URLs still rewritten while off")
	}
	if code, _, _ := get(t, s.c, s.ts.URL+"/cdn-img/"+rel+"?w=10"); code == http.StatusOK {
		t.Error("image service runs while off")
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/plugins")
	if !strings.Contains(body, "still served from the bucket") {
		t.Error("the Plugins screen does not explain the fallback")
	}
	// Activation clears the flag; the download path deactivates once every file is back.
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=gocdn[^"&]*&amp;[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link")
	}
	get(t, s.c, s.ts.URL+html.UnescapeString(m[1]))
	if !s.srv.gocdnOn() || s.srv.settings.Bool(gocdnFallbackOption) {
		t.Fatal("activation")
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=deactivate")
	form = url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"deactivate-download"}}
	if code, _, loc := post(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=deactivate", form); code != http.StatusFound || !strings.Contains(loc, "page=offload") {
		t.Fatalf("deactivate-download: %d %s", code, loc)
	}
	j := s.waitJobDirect()
	if !j.Finished || j.Failed != 0 {
		t.Fatalf("download job %+v", j)
	}
	if s.srv.gocdnOn() || s.srv.settings.Bool(gocdnFallbackOption) {
		t.Fatalf("after the download: on %v, fallback %v", s.srv.gocdnOn(), s.srv.settings.Bool(gocdnFallbackOption))
	}
	if _, err := os.Stat(s.srv.gocdnLocalPath(rel)); err != nil {
		t.Fatal("the file is not back on the server")
	}
	// Nothing removed: the Deactivate link deactivates directly (after re-activation).
	_ = s.srv.settings.Set(context.Background(), map[string]any{gocdnSetting: true})
	if code, _, loc := get(t, s.c, deactivateLink()); code != http.StatusFound || !strings.Contains(loc, "deactivate=true") || s.srv.gocdnOn() {
		t.Fatalf("plain deactivate: %d %s", code, loc)
	}
	// Bulk deactivation with removed files never asks, but keeps the fallback on.
	_ = s.srv.settings.Set(context.Background(), map[string]any{gocdnSetting: true})
	_, _ = s.srv.store.Pool.Exec(context.Background(), `UPDATE gocdn_items SET local_removed = true WHERE path = $1`, rel)
	os.Remove(s.srv.gocdnLocalPath(rel))
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/plugins")
	bulk := url.Values{"_wpnonce": {regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)[1]}, "action": {"deactivate-selected"}, "checked[]": {"gocdn"}}
	post(t, s.c, s.ts.URL+"/wp-admin/plugins", bulk)
	if s.srv.gocdnOn() || !s.srv.settings.Bool(gocdnFallbackOption) {
		t.Fatalf("bulk deactivation: on %v, fallback %v", s.srv.gocdnOn(), s.srv.settings.Bool(gocdnFallbackOption))
	}
}

func TestGoCDNMediaModalRemoteTab(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	s.fake.PutObject("media", "wp-content/uploads/library/pic.png", gocdnTestImage(64, 48, "png"), "image/png")
	s.fake.PutObject("media", "wp-content/uploads/library/clip.bin", []byte("x"), "application/octet-stream")
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/upload")
	if !strings.Contains(body, "window.gocdnModal") || !strings.Contains(body, "Remote storage") || !strings.Contains(body, "content:render:gocdn") {
		t.Fatal("the modal tab script is not on a screen with the media modal")
	}
	nonce := regexp.MustCompile(`"nonce":"([^"]+)"`).FindStringSubmatch(between(body, "window.gocdnModal", ";"))
	if nonce == nil {
		t.Fatal("no nonce in the modal settings")
	}
	_, raw, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?action=remote-list&prefix=wp-content/uploads/")
	var list struct {
		Folders []struct{ Prefix string }
		Objects []struct {
			Key     string
			Allowed bool
			MediaID int64 `json:"media_id"`
		}
	}
	_ = json.Unmarshal([]byte(raw), &list)
	if len(list.Folders) != 1 || list.Folders[0].Prefix != "wp-content/uploads/library/" {
		t.Fatalf("folders %s", raw)
	}
	_, raw, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?action=remote-list&prefix=wp-content/uploads/library/")
	_ = json.Unmarshal([]byte(raw), &list)
	if len(list.Objects) != 2 || !list.Objects[1].Allowed || list.Objects[0].Allowed {
		t.Fatalf("objects %s", raw)
	}
	imp := func(key, mode, n string) (int, map[string]any) {
		code, out, _ := post(t, s.c, s.ts.URL+"/wp-admin/gocdn?action=remote-import", url.Values{"_wpnonce": {n}, "key": {key}, "mode": {mode}})
		var m map[string]any
		_ = json.Unmarshal([]byte(out), &m)
		return code, m
	}
	if code, _ := imp("wp-content/uploads/library/pic.png", "link", "bad"); code != http.StatusForbidden {
		t.Errorf("import without a valid nonce: %d", code)
	}
	if code, m := imp("wp-content/uploads/library/clip.bin", "copy", nonce[1]); code != http.StatusBadRequest || m["error"] == nil {
		t.Errorf("disallowed type: %d %v", code, m)
	}
	code, m := imp("wp-content/uploads/library/pic.png", "link", nonce[1])
	if code != http.StatusOK || m["id"] == nil {
		t.Fatalf("import: %d %v", code, m)
	}
	id := int64(m["id"].(float64))
	if a, err := s.srv.store.GetAttachment(context.Background(), id); err != nil || a.Width != 64 {
		t.Fatalf("linked attachment %v %v", a, err)
	}
	// Picking it again selects the same attachment; the listing shows it in the library.
	if code, m := imp("wp-content/uploads/library/pic.png", "copy", nonce[1]); code != http.StatusOK || int64(m["id"].(float64)) != id || m["existing"] != true {
		t.Fatalf("second pick: %d %v", code, m)
	}
	_, raw, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?action=remote-list&prefix=wp-content/uploads/library/")
	_ = json.Unmarshal([]byte(raw), &list)
	if list.Objects[1].MediaID != id {
		t.Errorf("listing: %s", raw)
	}
	// The media modal can fetch it (get-attachment).
	code, out, _ := post(t, s.c, s.ts.URL+"/wp-admin/admin-ajax", url.Values{"action": {"get-attachment"}, "id": {itoa64(id)}})
	if code != http.StatusOK || !strings.Contains(out, `"success":true`) {
		t.Fatalf("get-attachment: %d %.200s", code, out)
	}
	// Plugin off: no tab.
	_ = s.srv.settings.Set(context.Background(), map[string]any{gocdnSetting: false})
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/upload")
	if strings.Contains(body, "gocdnModal") {
		t.Error("the tab is added while GoCDN is off")
	}
}
