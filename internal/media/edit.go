package media

import (
	"image"
	"image/draw"
	"math"
)

// The image operations of the REST media edit endpoint (WP_REST_Attachments_Controller::
// edit_media_item, WP_Image_Editor::rotate / crop / flip).

// RotateDegrees turns img counter-clockwise by angle degrees, like WP_Image_Editor::rotate()
// (imagerotate() with a transparent background). Multiples of 90 are exact pixel moves; any other
// angle is resampled bilinearly onto the bounding box of the turned image, the corners transparent
// (black once written as JPEG, as with GD).
func RotateDegrees(img image.Image, angle float64) image.Image {
	a := math.Mod(angle, 360)
	if a < 0 {
		a += 360
	}
	if q := math.Round(a / 90); math.Abs(a-q*90) < 1e-9 {
		n := int(q) * 90 % 360
		if n == 0 {
			return img
		}
		return rotate(img, n)
	}
	src := toRGBA(img)
	w, h := src.Rect.Dx(), src.Rect.Dy()
	rad := a * math.Pi / 180
	sin, cos := math.Sin(rad), math.Cos(rad)
	nw, nh := RotatedSize(w, h, angle)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	cx, cy := float64(w)/2, float64(h)/2
	ncx, ncy := float64(nw)/2, float64(nh)/2
	for y := 0; y < nh; y++ {
		dy := float64(y) + 0.5 - ncy
		for x := 0; x < nw; x++ {
			dx := float64(x) + 0.5 - ncx
			// Inverse of a counter-clockwise turn on screen (y grows downwards).
			sx := dx*cos - dy*sin + cx - 0.5
			sy := dx*sin + dy*cos + cy - 0.5
			bilinear(src, sx, sy, dst.Pix[y*dst.Stride+x*4:y*dst.Stride+x*4+4])
		}
	}
	return dst
}

// RotatedSize is the size of a w×h image after RotateDegrees(img, angle): the bounding box of
// the turned image (w and h swapped for odd multiples of 90).
func RotatedSize(w, h int, angle float64) (int, int) {
	a := math.Mod(angle, 360)
	if a < 0 {
		a += 360
	}
	if q := math.Round(a / 90); math.Abs(a-q*90) < 1e-9 {
		if int(q)%2 == 1 {
			return h, w
		}
		return w, h
	}
	rad := a * math.Pi / 180
	sin, cos := math.Sin(rad), math.Cos(rad)
	nw := int(math.Ceil(math.Abs(float64(w)*cos) + math.Abs(float64(h)*sin) - 1e-9))
	nh := int(math.Ceil(math.Abs(float64(w)*sin) + math.Abs(float64(h)*cos) - 1e-9))
	return max(nw, 1), max(nh, 1)
}

// bilinear samples src (premultiplied RGBA) at (x, y), pixels outside counting as transparent.
func bilinear(src *image.RGBA, x, y float64, out []uint8) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	if x0 < -1 || y0 < -1 || x0 >= w || y0 >= h {
		return
	}
	fx, fy := x-float64(x0), y-float64(y0)
	var acc [4]float64
	for j := 0; j < 2; j++ {
		for i := 0; i < 2; i++ {
			px, py := x0+i, y0+j
			if px < 0 || py < 0 || px >= w || py >= h {
				continue
			}
			wt := (1 - math.Abs(float64(i)-fx)) * (1 - math.Abs(float64(j)-fy))
			o := py*src.Stride + px*4
			for k := 0; k < 4; k++ {
				acc[k] += wt * float64(src.Pix[o+k])
			}
		}
	}
	for k := 0; k < 4; k++ {
		out[k] = uint8(math.Min(255, math.Round(acc[k])))
	}
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

// Flip mirrors img horizontally and/or vertically (WP_Image_Editor::flip( $horz, $vert )).
func Flip(img image.Image, horizontal, vertical bool) image.Image {
	if !horizontal && !vertical {
		return img
	}
	return flip(img, vertical, horizontal)
}

// Crop cuts r (in pixels from the image's top-left corner) out of img. It reports false when r does
// not overlap the image.
func Crop(img image.Image, r image.Rectangle) (image.Image, bool) {
	b := img.Bounds()
	r = r.Add(b.Min).Intersect(b)
	if r.Empty() {
		return img, false
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	return dst, true
}
