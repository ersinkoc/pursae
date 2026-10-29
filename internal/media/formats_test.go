package media

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeSVG(t *testing.T) {
	in := `<?xml version="1.0"?>
<!DOCTYPE svg [<!ENTITY x "boom">]>
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="120" height="60" onload="alert(1)">
  <script>alert(1)</script>
  <style>@import url(https://evil.example/x.css); .a{fill:red}</style>
  <a xlink:href="javascript:alert(1)"><rect class="a" width="10" height="10"/></a>
  <use xlink:href="https://evil.example/sprite.svg#i"/>
  <use xlink:href="#local"/>
  <image href="data:text/html;base64,PHNjcmlwdD4=" />
  <image href="data:image/png;base64,iVBORw0KGgo=" />
  <foreignObject><div>html</div></foreignObject>
  <circle id="local" cx="5" cy="5" r="4" fill="blue" onclick="x()"/>
</svg>`
	out, err := SanitizeSVG([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"<script", "onload", "onclick", "@import", "javascript:", "evil.example", "foreignObject", "data:text/html", "ENTITY"} {
		if strings.Contains(s, bad) {
			t.Errorf("sanitized SVG still contains %q:\n%s", bad, s)
		}
	}
	for _, good := range []string{`xmlns:xlink="http://www.w3.org/1999/xlink"`, `xlink:href="#local"`, `<circle id="local"`, "data:image/png;base64", `width="120"`} {
		if !strings.Contains(s, good) {
			t.Errorf("sanitized SVG lost %q:\n%s", good, s)
		}
	}
	if w, h := svgDimensions(out); w != 120 || h != 60 {
		t.Errorf("dimensions %dx%d", w, h)
	}
	if _, err := SanitizeSVG([]byte(`<html><script>x</script></html>`)); err == nil {
		t.Error("a non-SVG document was accepted")
	}
	if _, err := SanitizeSVG([]byte(`<svg><rect`)); err == nil {
		t.Error("broken XML was accepted")
	}
	if w, h := svgDimensions([]byte(`<svg viewBox="0 0 300 150"/>`)); w != 300 || h != 150 {
		t.Errorf("viewBox dimensions %dx%d", w, h)
	}
	if w, h := svgDimensions([]byte(`<svg width="60" viewBox="0 0 300 150"/>`)); w != 60 || h != 30 {
		t.Errorf("width + viewBox dimensions %dx%d", w, h)
	}
}

func sizesForTest() []Size {
	return []Size{{Name: "thumbnail", Width: 150, Height: 150, Crop: true}, {Name: "medium", Width: 300, Height: 300}}
}

func TestUploadFormats(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)

	// WebP: decoded, sub-sizes written as JPEG (no WebP encoder), original kept.
	webp, _ := os.ReadFile("testdata/sample.webp")
	st, err := Save(dir, true, now, "photo.webp", bytes.NewReader(webp))
	if err != nil || st.Mime != "image/webp" {
		t.Fatalf("save webp: %v %+v", err, st)
	}
	info, err := ProcessImage(dir, st.RelPath, sizesForTest())
	if err != nil || info.Width == 0 || len(info.Variants) == 0 {
		t.Fatalf("webp: %v %+v", err, info)
	}
	for _, v := range info.Variants {
		if !strings.HasSuffix(v.File, ".jpg") || v.Mime != "image/jpeg" {
			t.Errorf("webp variant %+v", v)
		}
		if _, err := os.Stat(filepath.Join(dir, "2026/10", v.File)); err != nil {
			t.Error(err)
		}
	}

	// AVIF: measured from the ispe box, no sub-sizes.
	avif := append([]byte("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), make([]byte, 40)...)
	ispe := make([]byte, 20)
	binary.BigEndian.PutUint32(ispe[0:], 20)
	copy(ispe[4:], "ispe")
	binary.BigEndian.PutUint32(ispe[12:], 1920)
	binary.BigEndian.PutUint32(ispe[16:], 1080)
	avif = append(avif, ispe...)
	st, err = Save(dir, true, now, "photo.avif", bytes.NewReader(avif))
	if err != nil || st.Mime != "image/avif" {
		t.Fatalf("save avif: %v", err)
	}
	if info, err = ProcessImage(dir, st.RelPath, sizesForTest()); err != nil || info.Width != 1920 || info.Height != 1080 || len(info.Variants) != 0 {
		t.Fatalf("avif: %v %+v", err, info)
	}

	// SVG: sanitized on save and measured.
	st, err = Save(dir, true, now, "logo.svg", strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 32"><script>x</script><rect width="64" height="32"/></svg>`))
	if err != nil || st.Mime != "image/svg+xml" {
		t.Fatalf("save svg: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, st.RelPath))
	if strings.Contains(string(b), "script") {
		t.Error("the stored SVG was not sanitized")
	}
	if info, err = ProcessImage(dir, st.RelPath, sizesForTest()); err != nil || info.Width != 64 || info.Height != 32 {
		t.Fatalf("svg: %v %+v", err, info)
	}
	if _, err := Save(dir, true, now, "bad.svg", strings.NewReader(`<html></html>`)); err == nil {
		t.Error("an unsafe SVG was stored")
	}
	if MimeFor("x.heic") != "image/heic" || MimeFor("x.heif") != "image/heif" || MimeFor("x.svg") != "image/svg+xml" {
		t.Error("MIME table lacks heic/heif/svg")
	}
}
