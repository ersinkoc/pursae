package media

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestCropRectFocal(t *testing.T) {
	// Without a focal point (or at the centre) the crop is WordPress' centre crop, as before.
	for _, f := range []*Focal{nil, {X: 0.5, Y: 0.5}, {X: 0.502, Y: 0.499}} {
		if got := CropRect(1000, 500, 150, 150, f); got != image.Rect(250, 0, 750, 500) {
			t.Errorf("centre crop with %v: %v", f, got)
		}
	}
	cases := []struct {
		sw, sh, w, h int
		f            Focal
		want         image.Rectangle
	}{
		{1000, 500, 150, 150, Focal{0.1, 0.5}, image.Rect(0, 0, 500, 500)},     // near the left edge: clamped
		{1000, 500, 150, 150, Focal{0.9, 0.5}, image.Rect(500, 0, 1000, 500)},  // near the right edge
		{1000, 500, 150, 150, Focal{0.6, 0.2}, image.Rect(350, 0, 850, 500)},   // y does not matter for a wide crop
		{500, 1000, 150, 150, Focal{0.5, 0.1}, image.Rect(0, 0, 500, 500)},     // tall image: the top
		{500, 1000, 150, 150, Focal{0.5, 0.4}, image.Rect(0, 150, 500, 650)},   // centred on 40%
		{1200, 800, 300, 100, Focal{0.5, 0.95}, image.Rect(0, 400, 1200, 800)}, // wide crop of the bottom
		{1200, 800, 1200, 800, Focal{0.2, 0.2}, image.Rect(0, 0, 1200, 800)},   // same ratio: whole image
		{2000, 1000, 300, 300, Focal{0.75, 0.25}, image.Rect(1000, 0, 2000, 1000)},
	}
	for _, c := range cases {
		f := c.f
		if got := CropRect(c.sw, c.sh, c.w, c.h, &f); got != c.want {
			t.Errorf("%dx%d → %dx%d at %v: got %v, want %v", c.sw, c.sh, c.w, c.h, c.f, got, c.want)
		}
	}
}

func TestFocalCSSAndValid(t *testing.T) {
	if got := (Focal{0.3, 0.205}).CSS(); got != "30% 20.5%" {
		t.Errorf("CSS: %q", got)
	}
	if (Focal{1.2, 0}).Valid() || (Focal{0, -0.1}).Valid() || !(Focal{0, 1}).Valid() {
		t.Error("Valid")
	}
	if !(Focal{0.5, 0.5}).Centered() || (Focal{0.52, 0.5}).Centered() {
		t.Error("Centered")
	}
}

// TestProcessImageFocalCrop: the cropped size follows the focal point; the other sizes do not
// change.
func TestProcessImageFocalCrop(t *testing.T) {
	dir := t.TempDir()
	// 900×300: red left third, green middle, blue right third.
	img := image.NewRGBA(image.Rect(0, 0, 900, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 900; x++ {
			c := color.RGBA{0, 0, 255, 255}
			if x < 300 {
				c = color.RGBA{255, 0, 0, 255}
			} else if x < 600 {
				c = color.RGBA{0, 255, 0, 255}
			}
			img.Set(x, y, c)
		}
	}
	if err := WriteFile(dir, "bands.png", img, "png"); err != nil {
		t.Fatal(err)
	}
	sizes := []Size{{Name: "thumbnail", Width: 150, Height: 150, Crop: true}, {Name: "medium", Width: 300, Height: 300}}
	thumbColor := func(opts Options) color.RGBA {
		t.Helper()
		info, err := ProcessImageWith(dir, "bands.png", sizes, opts)
		if err != nil {
			t.Fatal(err)
		}
		var thumb string
		for _, v := range info.Variants {
			if v.Name == "thumbnail" {
				thumb = v.File
			}
		}
		f, err := os.Open(filepath.Join(dir, thumb))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		got, _, err := image.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := got.At(75, 75).RGBA()
		return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 255}
	}
	if c := thumbColor(Options{}); c.G < 200 || c.R > 50 {
		t.Errorf("centre crop should be green: %v", c)
	}
	if c := thumbColor(Options{Focal: &Focal{X: 0.1, Y: 0.5}}); c.R < 200 || c.G > 50 {
		t.Errorf("focal left should be red: %v", c)
	}
	if c := thumbColor(Options{Focal: &Focal{X: 0.95, Y: 0.5}}); c.B < 200 || c.G > 50 {
		t.Errorf("focal right should be blue: %v", c)
	}
}

func TestSaveInFolder(t *testing.T) {
	dir := t.TempDir()
	st, err := SaveIn(dir, "2024/05", "Photo One.PNG", pngReader(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.RelPath != "2024/05/Photo-One.png" || st.Mime != "image/png" {
		t.Fatalf("saved %+v", st)
	}
	if st, err := SaveIn(dir, "", "x.png", pngReader(t), nil); err != nil || st.RelPath != "x.png" {
		t.Fatalf("root: %+v %v", st, err)
	}
	if _, err := SaveIn(dir, "", "x.exe", pngReader(t), nil); err != ErrType {
		t.Fatalf("type: %v", err)
	}
}

func pngReader(t *testing.T) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	if err := EncodeTo(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)), "png"); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}
