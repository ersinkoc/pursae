package gocdn

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gen2brain/avif"
)

func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x ^ y) & 0xff), 255})
		}
	}
	return img
}

// TestAVIFEncode encodes AVIF (wasm libavif through wazero, no cgo), checks the file decodes to
// the right size and reports the encode time, also with one encoder per CPU at once (the image
// service's concurrency limit).
func TestAVIFEncode(t *testing.T) {
	img := photo(1000, 667)
	var buf bytes.Buffer
	start := time.Now()
	if err := Encode(&buf, img, "avif", 82); err != nil { // first call also compiles the module
		t.Fatal(err)
	}
	first := time.Since(start)
	if !bytes.Contains(buf.Bytes()[:32], []byte("ftypavif")) {
		t.Fatalf("not an AVIF file: % x", buf.Bytes()[:32])
	}
	cfg, err := avif.DecodeConfig(bytes.NewReader(buf.Bytes()))
	if err != nil || cfg.Width != 1000 || cfg.Height != 667 {
		t.Fatalf("AVIF config %+v %v", cfg, err)
	}
	start = time.Now()
	buf.Reset()
	if err := Encode(&buf, img, "avif", 82); err != nil {
		t.Fatal(err)
	}
	warm := time.Since(start)
	var wb bytes.Buffer
	start = time.Now()
	_ = Encode(&wb, img, "webp", 82)
	webpTime := time.Since(start)
	t.Logf("system libavif: %v", avif.Dynamic() == nil)
	t.Logf("1000×667: AVIF %d bytes in %v (first call %v), WebP %d bytes in %v", buf.Len(), warm, first, wb.Len(), webpTime)

	n := max(2, runtime.NumCPU())
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start = time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var b bytes.Buffer
			errs <- Encode(&b, photo(400, 300), "avif", 70)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("%d concurrent 400×300 AVIF encodes in %v", n, time.Since(start))
	if warm > 20*time.Second {
		t.Errorf("AVIF encoding is far too slow: %v", warm)
	}
}

func animGIF(frames, w, h int, transparent bool) *gif.GIF {
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 255, 0, 255}}
	if transparent {
		pal = append(pal, color.RGBA{})
	}
	g := &gif.GIF{LoopCount: 0, Config: image.Config{Width: w, Height: h, ColorModel: pal}}
	for i := 0; i < frames; i++ {
		f := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				ci := uint8(i % 3)
				if transparent && x < w/4 {
					ci = 3
				}
				f.SetColorIndex(x, y, ci)
			}
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 10+i)
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
	}
	return g
}

func TestTransformGIFKeepsAnimation(t *testing.T) {
	src := animGIF(5, 200, 100, true)
	out, err := TransformGIF(src, ImageParams{W: 50}, AnimLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Image) != 5 || out.Config.Width != 50 || out.Config.Height != 25 {
		t.Fatalf("frames %d, size %dx%d", len(out.Image), out.Config.Width, out.Config.Height)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, out); err != nil {
		t.Fatal(err)
	}
	back, err := gif.DecodeAll(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Image) != 5 || back.LoopCount != 0 {
		t.Fatalf("decoded %d frames, loop %d", len(back.Image), back.LoopCount)
	}
	for i, d := range back.Delay {
		if d != 10+i {
			t.Errorf("frame %d delay %d", i, d)
		}
	}
	// Frame colours survive (red, blue, green …) and the transparent strip stays transparent.
	want := []color.RGBA{{255, 0, 0, 255}, {0, 0, 255, 255}, {0, 255, 0, 255}}
	for i, f := range back.Image {
		r, g, b, _ := f.At(40, 12).RGBA()
		if w := want[i%3]; uint8(r>>8) != w.R || uint8(g>>8) != w.G || uint8(b>>8) != w.B {
			t.Errorf("frame %d colour %d,%d,%d", i, r>>8, g>>8, b>>8)
		}
		if _, _, _, a := f.At(2, 12).RGBA(); a != 0 {
			t.Errorf("frame %d: transparent pixel became opaque", i)
		}
	}
}

func TestTransformGIFBudget(t *testing.T) {
	src := animGIF(20, 100, 100, false)
	if _, err := TransformGIF(src, ImageParams{W: 50}, AnimLimits{MaxFrames: 10}); !errors.Is(err, ErrAnimBudget) {
		t.Errorf("frame limit: %v", err)
	}
	if _, err := TransformGIF(src, ImageParams{W: 50}, AnimLimits{MaxPixels: 20 * 100 * 100}); !errors.Is(err, ErrAnimBudget) {
		t.Errorf("pixel limit: %v", err)
	}
	if !IsAnimatedGIF(src) || IsAnimatedGIF(animGIF(1, 10, 10, false)) {
		t.Error("IsAnimatedGIF")
	}
}
