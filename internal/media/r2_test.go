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
	"strings"
	"testing"
	"time"
)

// exifJPEG is a w×h JPEG whose left half is red and right half blue, with an EXIF APP1 segment
// carrying the given orientation (little- or big-endian TIFF).
func exifJPEG(t *testing.T, w, h, orientation int, bigEndian bool) []byte {
	t.Helper()
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
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	var bo interface {
		binary.ByteOrder
		binary.AppendByteOrder
	} = binary.LittleEndian
	tiff := []byte("II*\x00")
	if bigEndian {
		bo, tiff = binary.BigEndian, []byte("MM\x00*")
	}
	tiff = bo.AppendUint32(tiff, 8)
	tiff = bo.AppendUint16(tiff, 2)
	// an unrelated tag first, then Orientation
	tiff = bo.AppendUint16(tiff, 0x010F)
	tiff = bo.AppendUint16(tiff, 2)
	tiff = bo.AppendUint32(tiff, 1)
	tiff = append(tiff, 'X', 0, 0, 0)
	tiff = bo.AppendUint16(tiff, 0x0112)
	tiff = bo.AppendUint16(tiff, 3)
	tiff = bo.AppendUint32(tiff, 1)
	tiff = bo.AppendUint16(tiff, uint16(orientation))
	tiff = append(tiff, 0, 0)
	tiff = bo.AppendUint32(tiff, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := []byte{0xFF, 0xE1}
	app1 = binary.BigEndian.AppendUint16(app1, uint16(len(seg)+2))
	app1 = append(app1, seg...)
	data := buf.Bytes()
	return append(append(append([]byte{}, data[:2]...), app1...), data[2:]...)
}

func TestJPEGOrientation(t *testing.T) {
	for _, be := range []bool{false, true} {
		for o := 1; o <= 8; o++ {
			if got := JPEGOrientation(bytes.NewReader(exifJPEG(t, 8, 4, o, be))); got != o {
				t.Errorf("big-endian %v: orientation %d read as %d", be, o, got)
			}
		}
	}
	var plain bytes.Buffer
	_ = jpeg.Encode(&plain, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)
	if got := JPEGOrientation(bytes.NewReader(plain.Bytes())); got != 1 {
		t.Errorf("no EXIF: %d", got)
	}
	if got := JPEGOrientation(strings.NewReader("not a jpeg")); got != 1 {
		t.Errorf("garbage: %d", got)
	}
}

func TestJPEGOrientationRejectsInvalidCount(t *testing.T) {
	data := exifJPEG(t, 8, 4, 6, false)
	at := bytes.Index(data, []byte{0x12, 0x01, 0x03, 0x00})
	if at < 0 {
		t.Fatal("orientation tag missing")
	}
	binary.LittleEndian.PutUint32(data[at+4:], 0)
	if got := JPEGOrientation(bytes.NewReader(data)); got != 1 {
		t.Fatalf("invalid count produced orientation %d", got)
	}
}

func TestJPEGOrientationRejectsLargeTIFFOffset(t *testing.T) {
	data := exifJPEG(t, 8, 4, 6, false)
	at := bytes.Index(data, []byte("Exif\x00\x00II"))
	if at < 0 {
		t.Fatal("EXIF missing")
	}
	binary.LittleEndian.PutUint32(data[at+10:], 0x7fffffff)
	if got := JPEGOrientation(bytes.NewReader(data)); got != 1 {
		t.Fatalf("invalid offset produced orientation %d", got)
	}
}

func TestOrient(t *testing.T) {
	// 3×2: pixel value = x + 10*y in the red channel.
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			src.Set(x, y, color.RGBA{uint8(x + 10*y), 0, 0, 255})
		}
	}
	at := func(img image.Image, x, y int) int { r, _, _, _ := img.At(x, y).RGBA(); return int(r >> 8) }
	cases := []struct {
		o    int
		w, h int
		tl   int // the source pixel that ends up top-left
		tr   int // ... top-right
	}{
		{2, 3, 2, 2, 0}, {3, 3, 2, 12, 10}, {4, 3, 2, 10, 12},
		{5, 2, 3, 0, 10}, {6, 2, 3, 10, 0}, {7, 2, 3, 12, 2}, {8, 2, 3, 2, 12},
	}
	for _, c := range cases {
		out := Orient(src, c.o)
		b := out.Bounds()
		if b.Dx() != c.w || b.Dy() != c.h {
			t.Errorf("orientation %d: size %v", c.o, b)
			continue
		}
		if got := at(out, 0, 0); got != c.tl {
			t.Errorf("orientation %d: top-left %d, want %d", c.o, got, c.tl)
		}
		if got := at(out, b.Dx()-1, 0); got != c.tr {
			t.Errorf("orientation %d: top-right %d, want %d", c.o, got, c.tr)
		}
	}
	if Orient(src, 1) != image.Image(src) {
		t.Error("orientation 1 copied the image")
	}
}

// A phone portrait photo (landscape pixels + Orientation 6) gets upright sizes and dimensions and a
// "-rotated" main file; the upload is kept as the original image.
func TestProcessImageEXIFRotation(t *testing.T) {
	dir := t.TempDir()
	data := exifJPEG(t, 400, 200, 6, false)
	st, err := Save(dir, false, time.Now(), "portrait.jpg", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	info, err := ProcessImage(dir, st.RelPath, []Size{{"thumbnail", 50, 50, true}, {"medium", 100, 100, false}})
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 200 || info.Height != 400 {
		t.Fatalf("dimensions %d×%d, want 200×400", info.Width, info.Height)
	}
	if info.RelPath != "portrait-rotated.jpg" || info.OriginalFile != "portrait.jpg" {
		t.Fatalf("main file %q original %q", info.RelPath, info.OriginalFile)
	}
	var medium *Variant
	for i := range info.Variants {
		if info.Variants[i].Name == "medium" {
			medium = &info.Variants[i]
		}
	}
	if medium == nil || medium.Width != 50 || medium.Height != 100 {
		t.Fatalf("medium size %+v", medium)
	}
	f, err := os.Open(filepath.Join(dir, medium.File))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	// Turned clockwise: the red left half of the stored pixels is now the top.
	if r, _, b, _ := img.At(25, 5).RGBA(); r>>8 < 200 || b>>8 > 60 {
		t.Errorf("top of the medium size is not red: r=%d b=%d", r>>8, b>>8)
	}
	if r, _, b, _ := img.At(25, 95).RGBA(); b>>8 < 200 || r>>8 > 60 {
		t.Errorf("bottom of the medium size is not blue: r=%d b=%d", r>>8, b>>8)
	}
	// A new upload of the same name must not take the -rotated name's base.
	if got := uniqueName(dir, "portrait.jpg"); got != "portrait-1.jpg" {
		t.Errorf("uniqueName = %q", got)
	}
}

// Names held only in the bucket (local copy removed) are taken: neither the file nor a name whose
// sizes would overwrite its sizes may be used again.
func TestSaveWithReservedNames(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	var asked string
	reserved := func(sub string) []string {
		asked = sub
		return []string{"photo.jpg", "photo-150x150.jpg", "Logo-300x200.PNG"}
	}
	st, err := SaveWith(dir, true, now, "photo.jpg", strings.NewReader("\xFF\xD8\xFF\xE0x"), reserved)
	if err != nil {
		t.Fatal(err)
	}
	if asked != "2026/11" || st.RelPath != "2026/11/photo-1.jpg" {
		t.Fatalf("asked %q, stored %q", asked, st.RelPath)
	}
	st, _ = SaveWith(dir, true, now, "logo.png", strings.NewReader(pngMagic), reserved)
	if st.RelPath != "2026/11/logo-1.png" {
		t.Errorf("a name whose sizes are in the bucket was used: %q", st.RelPath)
	}
	st, _ = SaveWith(dir, true, now, "other.png", strings.NewReader(pngMagic), reserved)
	if st.RelPath != "2026/11/other.png" {
		t.Errorf("free name: %q", st.RelPath)
	}
}

func TestDecodeRefusesHugeImages(t *testing.T) {
	dir := t.TempDir()
	// A PNG header declaring 20000×20000 is enough: Decode must refuse before reading pixels.
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1)))
	if err := os.WriteFile(filepath.Join(dir, "ok.png"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	b := append([]byte(nil), buf.Bytes()...)
	binary.BigEndian.PutUint32(b[16:20], 20000)
	binary.BigEndian.PutUint32(b[20:24], 20000)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	if err := os.WriteFile(filepath.Join(dir, "huge.png"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode(dir, "huge.png"); err != ErrTooLarge {
		t.Fatalf("Decode of a 400-megapixel image: %v", err)
	}
	if img, _, err := Decode(dir, "ok.png"); err != nil || img.Bounds().Dx() != 1 {
		t.Fatalf("Decode of a small image: %v", err)
	}
}

// SMIL animations can rewrite an attribute after it was checked: <set attributeName="href"
// to="java&#9;script:…"> turns a harmless link into a script link.
func TestSanitizeSVGDropsURLAnimations(t *testing.T) {
	for _, anim := range []string{
		`<set attributeName="href" to="java&#9;script:alert(1)"/>`,
		`<animate attributeName="xlink:href" values="#a;javascript:alert(1)"/>`,
		`<set attributeName=" HREF " to="data:text/html,x"/>`,
		`<set attributeName="onclick" to="alert(1)"/>`,
	} {
		in := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><a href="#a">` + anim + `<text>x</text></a></svg>`
		out, err := SanitizeSVG([]byte(in))
		if err != nil {
			t.Fatalf("%s: %v", anim, err)
		}
		s := strings.ToLower(string(out))
		if strings.Contains(s, "<set") || strings.Contains(s, "<animate") || strings.Contains(s, "script") {
			t.Errorf("%s survived: %s", anim, out)
		}
		if !strings.Contains(s, "<text>x</text>") {
			t.Errorf("%s: the rest of the link was lost: %s", anim, out)
		}
	}
	// Animations of presentation attributes stay.
	in := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"><animate attributeName="opacity" values="0;1" dur="1s"/></rect></svg>`
	out, err := SanitizeSVG([]byte(in))
	if err != nil || !strings.Contains(string(out), `attributeName="opacity"`) {
		t.Errorf("an opacity animation was dropped: %s %v", out, err)
	}
}

const pngMagic = "\x89PNG\r\n\x1a\nx"

// wp_check_filetype_and_ext(): HTML named .jpg is refused; a PNG named .jpg is stored as .png.
func TestSaveChecksImageContent(t *testing.T) {
	dir := t.TempDir()
	if _, err := Save(dir, false, time.Now(), "evil.jpg", strings.NewReader("<html><script>alert(1)</script>")); err != ErrType {
		t.Fatalf("HTML named .jpg: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the refused file was kept: %v", entries)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 300, 200)))
	st, err := Save(dir, false, time.Now(), "shot.jpg", bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if st.RelPath != "shot.png" || st.Mime != "image/png" {
		t.Fatalf("PNG named .jpg stored as %q (%s)", st.RelPath, st.Mime)
	}
	info, err := ProcessImage(dir, st.RelPath, []Size{{"thumbnail", 150, 150, true}})
	if err != nil || len(info.Variants) != 1 || info.Variants[0].File != "shot-150x150.png" {
		t.Fatalf("sizes: %+v %v", info, err)
	}
	// Other types are not sniffed.
	if _, err := Save(dir, false, time.Now(), "notes.txt", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
}
