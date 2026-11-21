package media

import (
	"image"
	"image/color"
	"testing"
)

// halves is a w×h image, its left half red and its right half blue.
func halves(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 0, 0, 255}
			if x >= w/2 {
				c = color.RGBA{0, 0, 255, 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func isRed(c color.Color) bool { r, g, b, _ := c.RGBA(); return r > 0xc000 && g < 0x4000 && b < 0x4000 }
func isBlue(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return b > 0xc000 && g < 0x4000 && r < 0x4000
}

func TestRotateDegrees(t *testing.T) {
	src := halves(40, 20)
	// Counter-clockwise 90: the left (red) half ends at the bottom.
	out := RotateDegrees(src, 90)
	if b := out.Bounds(); b.Dx() != 20 || b.Dy() != 40 {
		t.Fatalf("90: %v", b)
	}
	if !isBlue(out.At(10, 5)) || !isRed(out.At(10, 35)) {
		t.Errorf("90: top %v bottom %v", out.At(10, 5), out.At(10, 35))
	}
	// -90 (clockwise): the red half at the top.
	out = RotateDegrees(src, -90)
	if !isRed(out.At(10, 5)) || !isBlue(out.At(10, 35)) {
		t.Errorf("-90: top %v bottom %v", out.At(10, 5), out.At(10, 35))
	}
	if out := RotateDegrees(src, 360); out != image.Image(src) {
		t.Error("360 should leave the image as it is")
	}
	if out := RotateDegrees(src, 540); out.Bounds().Dx() != 40 || !isBlue(out.At(5, 10)) {
		t.Errorf("540 = 180: %v", out.At(5, 10))
	}
	// 45 degrees: the bounding box of the turned image, transparent corners, colour kept in the middle.
	sq := halves(100, 100)
	out = RotateDegrees(sq, 45)
	if b := out.Bounds(); b.Dx() != 142 || b.Dy() != 142 {
		t.Fatalf("45: %v", b)
	}
	if _, _, _, a := out.At(1, 1).RGBA(); a != 0 {
		t.Errorf("45: corner not transparent (alpha %d)", a)
	}
	if !isRed(out.At(71, 100)) && !isBlue(out.At(71, 100)) {
		t.Errorf("45: centre-bottom colour lost: %v", out.At(71, 100))
	}
}

func TestFlipAndCrop(t *testing.T) {
	src := halves(40, 20)
	out := Flip(src, true, false)
	if !isBlue(out.At(1, 1)) || !isRed(out.At(39, 1)) {
		t.Errorf("horizontal flip: %v %v", out.At(1, 1), out.At(39, 1))
	}
	if Flip(src, false, false) != image.Image(src) {
		t.Error("a no-op flip should return the image")
	}
	out, ok := Crop(src, image.Rect(20, 0, 40, 20))
	if !ok || out.Bounds().Dx() != 20 || !isBlue(out.At(0, 0)) || !isBlue(out.At(19, 19)) {
		t.Errorf("crop right half: %v %v", ok, out.Bounds())
	}
	if _, ok := Crop(src, image.Rect(50, 50, 60, 60)); ok {
		t.Error("a crop outside the image succeeded")
	}
}
