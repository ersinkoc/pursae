package gocdn

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"net/url"
	"testing"

	"github.com/gen2brain/webp"
)

func TestParseImageParams(t *testing.T) {
	p, err := ParseImageParams(url.Values{"w": {"300"}, "h": {"200"}, "fit": {"crop"}, "q": {"70"}, "fm": {"JPG"}})
	if err != nil || p != (ImageParams{W: 300, H: 200, Fit: "cover", Q: 70, Format: "jpeg"}) {
		t.Fatalf("%+v %v", p, err)
	}
	if p.Canonical() != "w=300&h=200&fit=cover&q=70&fm=jpeg" {
		t.Errorf("canonical %q", p.Canonical())
	}
	if p, _ := ParseImageParams(url.Values{"width": {"10"}}); p.Canonical() != "w=10" {
		t.Errorf("defaults are not canonicalized away: %q", p.Canonical())
	}
	for _, bad := range []url.Values{{"w": {"0"}}, {"w": {"-5"}}, {"h": {"abc"}}, {"q": {"101"}}, {"fit": {"zoom"}}, {"fm": {"bmp"}}} {
		if _, err := ParseImageParams(bad); !errors.Is(err, ErrBadParam) {
			t.Errorf("%v accepted: %v", bad, err)
		}
	}
	if err := (ImageParams{W: 5000}).Check(Limits{MaxW: 4000}); !errors.Is(err, ErrTooLarge) {
		t.Error("width over the limit accepted")
	}
	if err := (Limits{MaxSourcePix: 100}).CheckSource(image.Config{Width: 20, Height: 20}); !errors.Is(err, ErrTooLarge) {
		t.Error("source over the pixel limit accepted")
	}
	if !HasImageParams(url.Values{"w": {"1"}}) || HasImageParams(url.Values{"ver": {"1"}}) {
		t.Error("HasImageParams")
	}
}

func TestOutputSize(t *testing.T) {
	cases := []struct {
		sw, sh int
		p      ImageParams
		w, h   int
		src    image.Rectangle
	}{
		{1000, 500, ImageParams{W: 300}, 300, 150, image.Rect(0, 0, 1000, 500)},
		{1000, 500, ImageParams{H: 100}, 200, 100, image.Rect(0, 0, 1000, 500)},
		{1000, 500, ImageParams{W: 300, H: 300}, 300, 150, image.Rect(0, 0, 1000, 500)},
		{1000, 500, ImageParams{W: 2000}, 1000, 500, image.Rect(0, 0, 1000, 500)}, // no upscaling
		{1000, 500, ImageParams{W: 200, H: 200, Fit: "cover"}, 200, 200, image.Rect(250, 0, 750, 500)},
		{1000, 500, ImageParams{W: 2000, H: 2000, Fit: "cover"}, 500, 500, image.Rect(250, 0, 750, 500)},
		{1000, 500, ImageParams{W: 100, H: 400, Fit: "fill"}, 100, 400, image.Rect(0, 0, 1000, 500)},
	}
	for _, c := range cases {
		w, h, src := OutputSize(c.sw, c.sh, c.p)
		if w != c.w || h != c.h || src != c.src {
			t.Errorf("%dx%d %+v: got %dx%d %v, want %dx%d %v", c.sw, c.sh, c.p, w, h, src, c.w, c.h, c.src)
		}
	}
}

func TestNegotiate(t *testing.T) {
	chrome := "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"
	old := "image/png,image/*;q=0.8,*/*;q=0.5"
	for _, c := range []struct {
		fm, accept, src   string
		webp, avif, alpha bool
		want              string
	}{
		{"auto", chrome, "jpeg", true, false, false, "webp"},
		{"auto", chrome, "jpeg", true, true, false, "avif"},
		{"auto", chrome, "jpeg", false, true, false, "avif"},
		{"auto", "image/webp,*/*", "jpeg", true, true, false, "webp"},
		{"auto", "image/avif;q=0,image/webp", "jpeg", true, true, false, "webp"},
		{"auto", old, "jpeg", true, true, false, "jpeg"},
		{"auto", chrome, "jpeg", false, false, false, "jpeg"},
		{"auto", old, "webp", true, false, true, "png"},
		{"auto", old, "webp", true, false, false, "jpeg"},
		{"avif", chrome, "png", true, false, true, "webp"},
		{"avif", chrome, "png", false, false, true, "png"},
		{"avif", old, "png", false, true, true, "avif"},
		{"png", chrome, "jpeg", true, true, false, "png"},
		{"auto", "image/webp;q=0", "jpeg", true, false, false, "jpeg"},
	} {
		if got := Negotiate(c.fm, c.accept, c.src, c.webp, c.avif, c.alpha); got != c.want {
			t.Errorf("Negotiate(%q, %q, %q, webp=%v avif=%v) = %q, want %q", c.fm, c.accept, c.src, c.webp, c.avif, got, c.want)
		}
	}
}

func TestAcceptsTypeQualityAfterOtherParameters(t *testing.T) {
	if AcceptsType("image/webp;level=1;q=0", "image/webp") {
		t.Fatal("accepted explicitly disabled type")
	}
	if !AcceptsType("image/webp;level=1;q=0.5", "image/webp") {
		t.Fatal("rejected acceptable type")
	}
}

func TestAcceptsTypeConsidersDuplicateMediaRanges(t *testing.T) {
	if !AcceptsType("image/webp;q=0, image/avif, image/webp;q=0.8", "image/webp") {
		t.Fatal("a disabled duplicate hid a later acceptable media range")
	}
	if AcceptsType("image/webp;q=0, image/avif", "image/webp") {
		t.Fatal("accepted a type with no positive media range")
	}
}

func TestTransformAndEncode(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			src.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	out := Transform(src, ImageParams{W: 100, H: 100, Fit: "cover"})
	if b := out.Bounds(); b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("cover result %v", b)
	}
	if Transform(src, ImageParams{W: 1000}) != image.Image(src) {
		t.Error("a no-op resize copied the image")
	}
	var buf bytes.Buffer
	if err := Encode(&buf, out, "webp", 80); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("RIFF")) || !bytes.Contains(buf.Bytes()[:16], []byte("WEBP")) {
		t.Fatalf("not a WebP file: % x", buf.Bytes()[:16])
	}
	img, err := webp.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil || img.Bounds().Dx() != 100 {
		t.Fatalf("WebP does not decode: %v", err)
	}
	buf.Reset()
	transparent := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	if err := Encode(&buf, transparent, "jpeg", 70); err != nil {
		t.Fatal(err)
	}
	j, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := j.At(5, 5).RGBA(); r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Errorf("transparent pixels not flattened on white: %d %d %d", r>>8, g>>8, b>>8)
	}
	if Encode(&buf, out, "tiff", 0) == nil {
		t.Error("unknown format accepted")
	}
}

func TestImageFormatAliasesAreDetected(t *testing.T) {
	for _, key := range []string{"fm", "format", "f"} {
		q := url.Values{key: {"webp"}}
		p, err := ParseImageParams(q)
		if err != nil || p.Format != "webp" || !HasImageParams(q) {
			t.Errorf("format alias %s: %+v, %v, detected=%v", key, p, err, HasImageParams(q))
		}
	}
	if HasImageParams(url.Values{"ver": {"1"}}) || HasImageParams(nil) {
		t.Fatal("unrelated query classified as transformation")
	}
}

func TestParseImageParamsRejectsNaNFocalCoordinates(t *testing.T) {
	for _, tc := range []struct {
		x, y    string
		wantErr bool
	}{
		{"NaN", "0.5", true},
		{"0.5", "NaN", true},
		{"+Inf", "0", true},
		{"0", "1", false},
		{"0.5", "0.5", false},
	} {
		_, err := ParseImageParams(url.Values{"fx": {tc.x}, "fy": {tc.y}})
		if (err != nil) != tc.wantErr {
			t.Errorf("fx=%q fy=%q: error = %v, want error %t", tc.x, tc.y, err, tc.wantErr)
		}
	}
}

func TestAcceptsTypeRejectsInvalidQuality(t *testing.T) {
	for _, tc := range []struct {
		q    string
		want bool
	}{
		{"-0.5", false}, {"0", false}, {"NaN", false}, {"abc", false}, {"2", false}, {"0.1", true}, {"1", true},
	} {
		if got := AcceptsType("image/webp;q="+tc.q, "image/webp"); got != tc.want {
			t.Errorf("q=%q: got %t, want %t", tc.q, got, tc.want)
		}
	}
}

func TestCheckSourceRejectsPixelCountOverflow(t *testing.T) {
	large := int(^uint(0) >> 1)
	for _, tc := range []struct {
		w, h    int
		wantErr bool
	}{
		{large, large, true}, {large, 1, true}, {10, 10, false}, {10, 11, true}, {1, 100, false},
	} {
		err := (Limits{MaxSourcePix: 100}).CheckSource(image.Config{Width: tc.w, Height: tc.h})
		if (err != nil) != tc.wantErr {
			t.Errorf("%dx%d: error = %v, want error %t", tc.w, tc.h, err, tc.wantErr)
		}
	}
}
