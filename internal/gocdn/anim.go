package gocdn

import (
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/gif"

	"golang.org/x/image/draw"
)

// Animated GIFs. Resizing only the first frame (what image.Decode returns) would freeze the
// animation, so the service decodes every frame, composes it onto the logical screen the way a
// browser does (frame offsets and disposal methods), resizes each full frame and writes an
// animated GIF again with the original delays and loop count. Each output frame is mapped to the
// palette of its source frame (nearest colour, no dithering, so the frames do not shimmer). Neither
// the WebP nor the AVIF library used here can encode animations at a reasonable cost (webp has no
// animation encoder; animated AVIF takes seconds per frame), so animations are always answered as
// GIF. A frame/pixel budget keeps a huge animation from tying up a CPU: over it, callers fall back to
// the first frame.

// AnimLimits is the budget of an animation.
type AnimLimits struct {
	MaxFrames int   // most frames processed (0 = DefaultAnimLimits)
	MaxPixels int64 // frames × (source width × height + output width × height)
}

// DefaultAnimLimits are used when a limit is 0.
var DefaultAnimLimits = AnimLimits{MaxFrames: 300, MaxPixels: 150_000_000}

// ErrAnimBudget is returned when an animation is over the frame/pixel budget.
var ErrAnimBudget = fmt.Errorf("%w: animation over the frame/pixel budget", ErrTooLarge)

// IsAnimatedGIF reports whether a GIF has more than one frame.
func IsAnimatedGIF(g *gif.GIF) bool { return g != nil && len(g.Image) > 1 }

// TransformGIF resizes every frame of an animated GIF for p and returns the new animation.
func TransformGIF(g *gif.GIF, p ImageParams, l AnimLimits) (*gif.GIF, error) {
	if g == nil || len(g.Image) == 0 {
		return nil, fmt.Errorf("%w: empty GIF", ErrBadParam)
	}
	if l.MaxFrames <= 0 {
		l.MaxFrames = DefaultAnimLimits.MaxFrames
	}
	if l.MaxPixels <= 0 {
		l.MaxPixels = DefaultAnimLimits.MaxPixels
	}
	sw, sh := g.Config.Width, g.Config.Height
	if sw <= 0 || sh <= 0 { // no logical screen: the union of the frames
		var u image.Rectangle
		for _, f := range g.Image {
			u = u.Union(f.Bounds())
		}
		sw, sh = u.Max.X, u.Max.Y
	}
	if sw <= 0 || sh <= 0 {
		return nil, fmt.Errorf("%w: GIF without size", ErrBadParam)
	}
	w, h, src := OutputSize(sw, sh, p)
	n := len(g.Image)
	if n > l.MaxFrames || int64(n)*(int64(sw)*int64(sh)+int64(w)*int64(h)) > l.MaxPixels {
		return nil, ErrAnimBudget
	}

	canvas := image.NewRGBA(image.Rect(0, 0, sw, sh))
	out := &gif.GIF{LoopCount: g.LoopCount, Config: image.Config{Width: w, Height: h}}
	transparentOut := false
	for i, frame := range g.Image {
		// The disposal of the previous frame was applied at the end of the last iteration.
		var restore *image.RGBA
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			restore = image.NewRGBA(canvas.Bounds())
			copy(restore.Pix, canvas.Pix)
		}
		stddraw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, stddraw.Over)

		scaled := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), canvas, src, draw.Src, nil)

		pal := framePalette(g, frame)
		ti := transparentIndex(pal)
		if ti < 0 && hasTransparentPixels(scaled) && len(pal) < 256 {
			pal = append(pal, color.RGBA{})
			ti = len(pal) - 1
		}
		pm := image.NewPaletted(scaled.Bounds(), pal)
		quantize(pm, scaled, ti)
		if ti >= 0 {
			transparentOut = true
		}
		out.Image = append(out.Image, pm)
		delay := 0
		if i < len(g.Delay) {
			delay = g.Delay[i]
		}
		out.Delay = append(out.Delay, delay)

		switch disposal {
		case gif.DisposalBackground:
			stddraw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, stddraw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, restore.Pix)
		}
	}
	// Every output frame is the whole composed picture: with transparency each one must clear the
	// previous one (restore to background), otherwise they are simply drawn over each other.
	disp := byte(gif.DisposalNone)
	if transparentOut {
		disp = gif.DisposalBackground
	}
	for range out.Image {
		out.Disposal = append(out.Disposal, disp)
	}
	return out, nil
}

func framePalette(g *gif.GIF, f *image.Paletted) color.Palette {
	if len(f.Palette) > 0 {
		return append(color.Palette(nil), f.Palette...)
	}
	if p, ok := g.Config.ColorModel.(color.Palette); ok && len(p) > 0 {
		return append(color.Palette(nil), p...)
	}
	return append(color.Palette(nil), webSafe()...)
}

func webSafe() color.Palette {
	var p color.Palette
	for r := 0; r < 6; r++ {
		for g := 0; g < 6; g++ {
			for b := 0; b < 6; b++ {
				p = append(p, color.RGBA{uint8(r * 51), uint8(g * 51), uint8(b * 51), 255})
			}
		}
	}
	return append(p, color.RGBA{})
}

func transparentIndex(p color.Palette) int {
	for i, c := range p {
		if _, _, _, a := c.RGBA(); a == 0 {
			return i
		}
	}
	return -1
}

func hasTransparentPixels(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] < 128 {
			return true
		}
	}
	return false
}

// quantize maps src onto dst's palette: pixels under half alpha become the transparent index,
// the others the nearest opaque palette colour (cached per colour, animations repeat colours).
func quantize(dst *image.Paletted, src *image.RGBA, transparent int) {
	cache := map[uint32]uint8{}
	opaque := make(color.Palette, 0, len(dst.Palette))
	index := make([]int, 0, len(dst.Palette))
	for i, c := range dst.Palette {
		if i == transparent {
			continue
		}
		opaque = append(opaque, c)
		index = append(index, i)
	}
	b := src.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			o := src.PixOffset(x, y)
			px := src.Pix[o : o+4 : o+4]
			if px[3] < 128 && transparent >= 0 {
				dst.SetColorIndex(x, y, uint8(transparent))
				continue
			}
			k := uint32(px[0])<<16 | uint32(px[1])<<8 | uint32(px[2])
			ci, ok := cache[k]
			if !ok {
				if len(opaque) == 0 {
					ci = 0
				} else {
					ci = uint8(index[opaque.Index(color.RGBA{px[0], px[1], px[2], 255})])
				}
				cache[k] = ci
			}
			dst.SetColorIndex(x, y, ci)
		}
	}
}
