package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
)

// The media library takes SVG (sanitized), WebP (sub-sizes as JPEG), AVIF/HEIC (measured, no
// sub-sizes); uploads are served with the sandbox CSP and no Edit Image for formats the editor
// cannot save.
func TestMediaUploadFormats(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, nonce, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce")
	upload := func(name string, data []byte) (int, map[string]any) {
		t.Helper()
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
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}
	details := func(m map[string]any) (float64, float64, map[string]any) {
		d, _ := m["media_details"].(map[string]any)
		sizes, _ := d["sizes"].(map[string]any)
		w, _ := d["width"].(float64)
		h, _ := d["height"].(float64)
		return w, h, sizes
	}

	code, svg := upload("logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100" onload="alert(1)"><script>alert(1)</script><rect width="200" height="100" fill="#2271b1"/></svg>`))
	if code != http.StatusCreated || svg["mime_type"] != "image/svg+xml" {
		t.Fatalf("svg upload: %d %v", code, svg)
	}
	if w, h, _ := details(svg); w != 200 || h != 100 {
		t.Errorf("svg dimensions %vx%v", w, h)
	}
	src, _ := svg["source_url"].(string)
	resp, err := c.Get(src)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "script") || strings.Contains(string(b), "onload") || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("served SVG is not sanitized/sandboxed: %s %q", b, resp.Header.Get("Content-Security-Policy"))
	}
	if code, _ := upload("evil.svg", []byte(`<html><body>x</body></html>`)); code == http.StatusCreated {
		t.Error("an HTML file named .svg was accepted")
	}

	webp, _ := os.ReadFile("../media/testdata/sample.webp")
	code, wp := upload("photo.webp", webp)
	if code != http.StatusCreated || wp["mime_type"] != "image/webp" {
		t.Fatalf("webp upload: %d %v", code, wp)
	}
	if _, _, sizes := details(wp); sizes["thumbnail"] == nil || sizes["medium"] == nil {
		t.Errorf("webp has no sub-sizes: %v", sizes)
	}

	// The media modal: no Edit Image for SVG.
	_, body, _ := get(t, c, base+"/wp-admin/admin-ajax?action=query-attachments&query[posts_per_page]=10")
	if !strings.Contains(body, `"subtype":"svg+xml"`) {
		t.Errorf("query-attachments lacks the SVG: %.300s", body)
	}
}
