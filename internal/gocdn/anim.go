package gocdn

import (
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/gif"
	"io"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"golang.org/x/image/draw"
)

// Animated GIFs. Resizing only the first frame (what image.Decode returns) would freeze the
// animation, so the service decodes every frame, composes it onto the logical screen the way a
// browser does (frame offsets and disposal methods), resizes each full frame and writes an
// animated GIF again with the original delays and loop count. Each output frame is mapped to the
// palette of its source frame (nearest colour, no dithering, so the frames do not shimmer). WebP and
// AVIF answers (TransformGIFTo) keep the composed true-colour frames and use the libraries' EncodeAll.
// A frame/pixel budget keeps a huge animation from tying up a CPU: over it, callers fall back to
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

// animFrame is one composed, resized frame of an animation and the source frame it came from.
type animFrame struct {
	img *image.RGBA
	src *image.Paletted
}

// composeGIF composes every frame of g onto the logical screen and resizes it for p.
func composeGIF(g *gif.GIF, p ImageParams, l AnimLimits) (frames []animFrame, w, h int, err error) {
	if g == nil || len(g.Image) == 0 {
		return nil, 0, 0, fmt.Errorf("%w: empty GIF", ErrBadParam)
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
		return nil, 0, 0, fmt.Errorf("%w: GIF without size", ErrBadParam)
	}
	w, h, src := OutputSize(sw, sh, p)
	n := len(g.Image)
	if n > l.MaxFrames || int64(n)*(int64(sw)*int64(sh)+int64(w)*int64(h)) > l.MaxPixels {
		return nil, 0, 0, ErrAnimBudget
	}

	canvas := image.NewRGBA(image.Rect(0, 0, sw, sh))
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
		frames = append(frames, animFrame{img: scaled, src: frame})

		switch disposal {
		case gif.DisposalBackground:
			stddraw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, stddraw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, restore.Pix)
		}
	}
	return frames, w, h, nil
}

// TransformGIF resizes every frame of an animated GIF for p and returns the new animation.
func TransformGIF(g *gif.GIF, p ImageParams, l AnimLimits) (*gif.GIF, error) {
	frames, w, h, err := composeGIF(g, p, l)
	if err != nil {
		return nil, err
	}
	out := &gif.GIF{LoopCount: g.LoopCount, Config: image.Config{Width: w, Height: h}}
	transparentOut := false
	for i, f := range frames {
		pal := framePalette(g, f.src)
		if composed := exactPalette(f.img); composed != nil {
			pal = composed
		}
		ti := transparentIndex(pal)
		if ti < 0 && hasTransparentPixels(f.img) && len(pal) < 256 {
			pal = append(pal, color.RGBA{})
			ti = len(pal) - 1
		}
		pm := image.NewPaletted(f.img.Bounds(), pal)
		quantize(pm, f.img, ti)
		if ti >= 0 {
			transparentOut = true
		}
		out.Image = append(out.Image, pm)
		delay := 0
		if i < len(g.Delay) {
			delay = g.Delay[i]
		}
		out.Delay = append(out.Delay, delay)
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

// exactPalette preserves all visible colors when the composed frame fits a GIF palette.
func exactPalette(img *image.RGBA) color.Palette {
	var palette color.Palette
	seen := make(map[color.RGBA]bool)
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			c := img.RGBAAt(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)
			if c.A < 128 {
				c = color.RGBA{}
			} else {
				n := color.NRGBAModel.Convert(c).(color.NRGBA)
				c = color.RGBA{R: n.R, G: n.G, B: n.B, A: 255}
			}
			if !seen[c] {
				if len(palette) == 256 {
					return nil
				}
				seen[c] = true
				palette = append(palette, c)
			}
		}
	}
	return palette
}

// TransformGIFTo is TransformGIF for an animated WebP or AVIF answer (format "webp" or "avif"):
// the frames stay true colour (no palette) and are encoded as one animation at quality q. GIF
// delays (1/100 s) are kept.
func TransformGIFTo(w io.Writer, g *gif.GIF, p ImageParams, l AnimLimits, format string, q int) error {
	frames, _, _, err := composeGIF(g, p, l)
	if err != nil {
		return err
	}
	imgs := make([]image.Image, len(frames))
	delays := make([]int, len(frames))
	for i, f := range frames {
		imgs[i] = f.img
		if i < len(g.Delay) {
			delays[i] = g.Delay[i] * 10
		}
	}
	return EncodeAnimation(w, imgs, delays, g.LoopCount, format, q)
}

// EncodeAnimation writes frames (equal bounds) with per-frame delays in milliseconds as an animated
// WebP or AVIF. loop is the GIF convention: 0 = forever, n = n repetitions; GIF's -1 (play once)
// becomes one repetition.
func EncodeAnimation(w io.Writer, frames []image.Image, delaysMs []int, loop int, format string, q int) error {
	if q <= 0 || q > 100 {
		q = 82
	}
	if loop < 0 {
		loop = 1
	}
	switch format {
	case "webp":
		a := &webp.WEBP{Image: frames, Delay: delaysMs, LoopCount: loop}
		return webp.EncodeAll(w, a, webp.Options{Quality: q, Method: 4})
	case "avif":
		aq := max(1, min(100, q*3/4))
		d := make([]float64, len(delaysMs))
		for i, ms := range delaysMs {
			d[i] = float64(ms) / 1000
		}
		a := &avif.AVIF{Image: frames, Delay: d, LoopCount: loop}
		return avif.EncodeAll(w, a, avif.Options{Quality: aq, QualityAlpha: aq, Speed: AVIFSpeed, ChromaSubsampling: image.YCbCrSubsampleRatio420})
	}
	return fmt.Errorf("%w: no animated %q output", ErrBadParam, format)
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
