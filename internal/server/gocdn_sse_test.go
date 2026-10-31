package server

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoCDNSSEKMS(t *testing.T) {
	s := gocdnSite(t)
	arn := "arn:aws:kms:eu-west-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
	s.configure(url.Values{"sse-type": {"kms"}, "sse-kms-key-id": {arn}})
	if st := s.srv.gocdnLoad(); st.SSEType != "kms" || st.SSEKMSKey != arn || st.SSE {
		t.Fatalf("settings %+v", st)
	}
	att := s.upload("kms.png", gocdnTestImage(40, 40, "png"))
	o := s.fake.Object("media", "wp-content/uploads/"+gocdnRelOfURL(att["source_url"].(string)))
	if o == nil || o.SSE != "aws:kms" || o.KMSKeyID != arn {
		t.Fatalf("object %+v", o)
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	if !strings.Contains(body, `name="sse-type" value="kms" checked`) || !strings.Contains(body, arn) {
		t.Error("the Storage screen does not show SSE-KMS")
	}
	// A key that is not an ID, alias or ARN is refused.
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	form := url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"save"}, "sse-type": {"kms"}, "sse-kms-key-id": {"bad key!"}}
	if _, _, loc := post(t, s.c, s.ts.URL+"/wp-admin/gocdn", form); !strings.Contains(loc, "gocdn-error") {
		t.Errorf("bad KMS key accepted: %s", loc)
	}
}

func TestGoCDNSSEC(t *testing.T) {
	s := gocdnSite(t)
	keyHex := strings.Repeat("0123456789abcdef", 4)
	// A key of the wrong size, or none, is refused.
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	if _, _, loc := post(t, s.c, s.ts.URL+"/wp-admin/gocdn", url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"save"}, "sse-type": {"customer"}, "sse-c-key": {"tooshort"}}); !strings.Contains(loc, "gocdn-error") {
		t.Errorf("short key accepted: %s", loc)
	}
	s.configure(url.Values{"sse-type": {"customer"}, "sse-c-key": {keyHex}, "delivery-domain": {"https://media.cdn.test"}, "multipart-threshold-mb": {"5"}})
	// Sealed at rest, never printed.
	stored, _ := s.srv.settings.Get(gocdnOption).(map[string]any)
	if v, _ := stored["sse-c-key"].(string); !strings.HasPrefix(v, gocdnSealPrefix) || strings.Contains(v, keyHex) {
		t.Fatalf("stored key %q", v)
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	if strings.Contains(body, keyHex) || !strings.Contains(body, "saved — type to replace") {
		t.Error("the customer key is printed or not marked as saved")
	}
	// Objects (single PUT and multipart) are written with the key and need it to be read.
	big := bytes.Repeat([]byte("0123456789abcdef"), (6<<20)/16+100)
	att := s.upload("big-sse.zip", big)
	rel := gocdnRelOfURL(att["source_url"].(string))
	o := s.fake.Object("media", "wp-content/uploads/"+rel)
	if o == nil || o.Parts != 2 || o.SSECKeyMD5 == "" || o.SSE != "" || !bytes.Equal(o.Body, big) {
		t.Fatalf("multipart object %+v", o)
	}
	img := s.upload("small-sse.png", gocdnTestImage(40, 40, "png"))
	imgRel := gocdnRelOfURL(img["source_url"].(string))
	if so := s.fake.Object("media", "wp-content/uploads/"+imgRel); so == nil || so.SSECKeyMD5 == "" {
		t.Fatalf("small object %+v", so)
	}
	if len(s.fake.Errors) != 0 {
		t.Fatalf("errors %v", s.fake.Errors)
	}
	// No public URL: media URLs stay local, and a removed file is proxied (with the key) instead of
	// redirected to the delivery domain.
	_, view, _ := s.restJSON(http.MethodGet, "/wp-json/wp/v2/media/"+itoa64(int64(img["id"].(float64))), nil)
	if u, _ := view["source_url"].(string); strings.Contains(u, "media.cdn.test") {
		t.Errorf("SSE-C media URL was rewritten to the CDN: %s", u)
	}
	if code, _, _ := s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "remove-local-file": {"1"}, "serve-from-s3": {"1"}}); code != http.StatusFound {
		t.Fatal("save offload settings")
	}
	s.srv.gocdnRemoveLocal(context.Background(), int64(img["id"].(float64)))
	if _, err := os.Stat(filepath.Join(s.srv.cfg.UploadsDir, filepath.FromSlash(imgRel))); !os.IsNotExist(err) {
		t.Fatalf("local copy not removed: %v", err)
	}
	resp := doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+imgRel, nil)
	data := readAll(resp)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(data, s.fake.Object("media", "wp-content/uploads/"+imgRel).Body) {
		t.Fatalf("proxied SSE-C file: %d", resp.StatusCode)
	}
	s.configure(url.Values{"sse-type": {"customer"}, "sse-c-key": {keyHex}, "local-delivery": {"signed"}})
	resp = doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+imgRel, nil)
	readAll(resp)
	if resp.StatusCode != http.StatusOK { // no presigned redirect is possible: the proxy answers
		t.Fatalf("signed delivery with SSE-C: %d", resp.StatusCode)
	}
	// Download back works with the key.
	it := s.srv.gocdnItemByPath(context.Background(), imgRel)
	if it == nil {
		t.Fatal("no item")
	}
	if err := s.srv.gocdnDownload(context.Background(), s.srv.gocdnLoad(), it); err != nil {
		t.Fatalf("download with SSE-C: %v", err)
	}
	// Changing the bucket keeps the old key for the old objects.
	s.fake.AddBucket("media2")
	s.configure(url.Values{"bucket": {"media2"}, "sse-type": {"customer"}, "sse-c-key": {strings.Repeat("ff", 32)}})
	st := s.srv.gocdnLoad()
	if len(st.Previous) != 1 || !strings.HasPrefix(st.Previous[0].SSECKey, gocdnSealPrefix) {
		t.Fatalf("previous connection without its key: %+v", st.Previous)
	}
	old := gocdnConnFor(st, it)
	if cl, err := s.srv.gocdnClient(old); err != nil || len(cl.SSECKey) != 32 || cl.SSECKey[0] != 0x01 {
		t.Fatalf("old client key: %v", err)
	}
	// Clearing the key while SSE-C is selected is refused; switching SSE-C off clears nothing else.
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	if _, _, loc := post(t, s.c, s.ts.URL+"/wp-admin/gocdn", url.Values{"_wpnonce": {formNonce(t, body)}, "do": {"save"}, "sse-type": {"customer"}, "sse-c-key-clear": {"1"}}); !strings.Contains(loc, "gocdn-error") {
		t.Errorf("clearing the key under SSE-C accepted: %s", loc)
	}
}
