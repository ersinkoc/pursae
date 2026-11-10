package media

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResizeDims(t *testing.T) {
	cases := []struct {
		ow, oh, mw, mh int
		crop           bool
		w, h           int
		ok             bool
	}{
		{1200, 800, 150, 150, true, 150, 150, true},
		{1200, 800, 300, 300, false, 300, 200, true},
		{1200, 800, 768, 0, false, 768, 512, true},
		{200, 100, 300, 300, false, 0, 0, false}, // no upscaling
		{100, 100, 150, 150, true, 0, 0, false},
	}
	for _, c := range cases {
		w, h, ok := resizeDims(c.ow, c.oh, c.mw, c.mh, c.crop)
		if w != c.w || h != c.h || ok != c.ok {
			t.Errorf("%+v: got %d %d %v", c, w, h, ok)
		}
	}
}

func TestApplyOpsUnsupportedRotationPreservesImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	for _, angle := range []int{45, -45, 405} {
		got := ApplyOps(img, []Op{{Rotate: angle}})
		if got.Bounds() != img.Bounds() || got.At(0, 0) != img.At(0, 0) {
			t.Errorf("rotation %d changed the image", angle)
		}
	}
}

func TestSaveAndProcess(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	for x := 0; x < 1200; x++ {
		img.Set(x, 10, color.RGBA{200, 0, 0, 255})
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	st, err := Save(dir, true, now, "Güzel Fotoğraf.JPG", bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if st.RelPath != "2026/10/Guzel-Fotograf.jpg" || st.Mime != "image/jpeg" {
		t.Fatalf("stored %+v", st)
	}
	again, _ := Save(dir, true, now, "Güzel Fotoğraf.JPG", bytes.NewReader(buf.Bytes()))
	if again.RelPath != "2026/10/Guzel-Fotograf-1.jpg" {
		t.Errorf("unique name = %s", again.RelPath)
	}
	info, err := ProcessImage(dir, st.RelPath, []Size{{"thumbnail", 150, 150, true}, {"medium", 300, 300, false}, {"large", 1024, 1024, false}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 1200 || len(info.Variants) != 3 {
		t.Fatalf("info %+v", info)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026/10/Guzel-Fotograf-150x150.jpg")); err != nil {
		t.Error("thumbnail missing")
	}
	if _, err := Save(dir, true, now, "evil.php", bytes.NewReader(nil)); err != ErrType {
		t.Errorf("php upload allowed: %v", err)
	}
}

// TestProcessImageWebP checks the image output format mapping (image_editor_output_format): by
// default a WebP upload gets JPEG sizes; mapped to WebP it keeps WebP sizes; a JPEG mapped to WebP
// is converted, its sizes are WebP and the upload stays as the original image.
func TestProcessImageWebP(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 900; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	write := func(name, format string) string {
		var buf bytes.Buffer
		if err := EncodeTo(&buf, img, format); err != nil {
			t.Fatal(err)
		}
		st, err := Save(dir, false, time.Now(), name, &buf)
		if err != nil {
			t.Fatal(err)
		}
		return st.RelPath
	}
	sizes := []Size{{"thumbnail", 150, 150, true}, {"medium", 300, 300, false}}
	isWebP := func(name string) bool {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return err == nil && len(b) > 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP"
	}

	rel := write("flower.webp", "webp")
	info, err := ProcessImage(dir, rel, sizes)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Variants) != 2 || info.Variants[0].File != "flower-150x150.jpg" || info.Variants[0].Mime != "image/jpeg" || info.RelPath != rel {
		t.Errorf("default: %+v", info)
	}

	webp := Options{OutputFormat: map[string]string{"image/webp": "image/webp"}}
	info, err = ProcessImageWith(dir, rel, sizes, webp)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Variants) != 2 || info.Variants[1].File != "flower-300x200.webp" || info.Variants[1].Mime != "image/webp" ||
		!isWebP("flower-300x200.webp") || info.OriginalFile != "" || info.RelPath != rel {
		t.Errorf("webp: %+v", info)
	}

	rel = write("meadow.jpg", "jpeg")
	all := Options{OutputFormat: map[string]string{"image/jpeg": "image/webp", "image/webp": "image/webp"}}
	info, err = ProcessImageWith(dir, rel, sizes, all)
	if err != nil {
		t.Fatal(err)
	}
	if info.RelPath != "meadow.webp" || info.OriginalFile != "meadow.jpg" || !isWebP("meadow.webp") || info.Width != 900 ||
		len(info.Variants) != 2 || info.Variants[0].File != "meadow-150x150.webp" || !isWebP("meadow-150x150.webp") {
		t.Errorf("jpeg to webp: %+v", info)
	}
	if _, err := os.Stat(filepath.Join(dir, "meadow.jpg")); err != nil {
		t.Error("the JPEG original is gone")
	}
	// A mapping to a format that cannot be written is ignored.
	info, _ = ProcessImageWith(dir, "meadow.jpg", sizes, Options{OutputFormat: map[string]string{"image/jpeg": "image/avif"}})
	if info.RelPath != "meadow.jpg" || info.Variants[0].Mime != "image/jpeg" {
		t.Errorf("avif mapping: %+v", info)
	}
}

// TestProcessImageWebPScaled checks a big image mapped to WebP: the "-scaled" main file is WebP.
func TestProcessImageWebPScaled(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3000, 1000)), nil); err != nil {
		t.Fatal(err)
	}
	st, err := Save(dir, false, time.Now(), "wide.jpg", &buf)
	if err != nil {
		t.Fatal(err)
	}
	info, err := ProcessImageWith(dir, st.RelPath, []Size{{"medium", 300, 300, false}}, Options{OutputFormat: map[string]string{"image/jpeg": "image/webp"}})
	if err != nil {
		t.Fatal(err)
	}
	if info.RelPath != "wide-scaled.webp" || info.OriginalFile != "wide.jpg" || info.Width != 2560 || info.Variants[0].File != "wide-300x100.webp" {
		t.Errorf("scaled: %+v", info)
	}
}

// TestProcessImagePixelLimit: an image declaring more than MaxImagePixels (a tiny file can claim a
// 30000×30000 canvas) is measured from its header and not decoded or resized.
func TestProcessImagePixelLimit(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	// IHDR data starts at byte 16 (8 signature + 4 length + 4 type); its CRC follows the 13 data bytes.
	binary.BigEndian.PutUint32(b[16:], 30000)
	binary.BigEndian.PutUint32(b[20:], 30000)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29]))
	if err := os.WriteFile(filepath.Join(dir, "bomb.png"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := ProcessImage(dir, "bomb.png", []Size{{Name: "thumbnail", Width: 150, Height: 150, Crop: true}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 30000 || info.Height != 30000 || len(info.Variants) != 0 || info.RelPath != "bomb.png" {
		t.Errorf("info %+v", info)
	}
}

// A new upload must not take a name whose generated sizes would overwrite another file:
// logo-150x150.png already exists, so logo.png is stored as logo-1.png (wp_unique_filename()).
func TestSaveAvoidsSizeVariantCollision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logo-150x150.png"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Save(dir, false, time.Now(), "logo.png", bytes.NewReader([]byte("\x89PNG\r\n\x1a\nsecond")))
	if err != nil {
		t.Fatal(err)
	}
	if st.RelPath != "logo-1.png" {
		t.Errorf("stored as %q, want logo-1.png", st.RelPath)
	}
	if err := os.WriteFile(filepath.Join(dir, "photo-scaled.JPG"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := uniqueName(dir, "photo.jpg"); got != "photo-1.jpg" {
		t.Errorf("uniqueName next to a -scaled copy = %q", got)
	}
	// Unrelated names that only share a prefix stay free.
	if err := os.WriteFile(filepath.Join(dir, "cat-photo.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := uniqueName(dir, "cat.png"); got != "cat.png" {
		t.Errorf("uniqueName(cat.png) = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "logo-150x150.png")); string(b) != "first" {
		t.Errorf("existing file changed")
	}
}
