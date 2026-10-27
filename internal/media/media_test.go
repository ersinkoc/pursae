package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
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
