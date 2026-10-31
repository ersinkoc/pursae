package server

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // logo formats re-encoded to JPEG
	"image/jpeg"
	_ "image/png"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// A small PDF 1.4 writer for Goocommerce's invoices and packing slips: the standard Helvetica and
// Helvetica-Bold fonts in WinAnsiEncoding (text outside WinAnsi is transliterated), text, lines,
// rectangles, JPEG images (DCTDecode) and pages. Coordinates are points from the top-left corner.

type gooPDF struct {
	W, H   float64
	pages  []*bytes.Buffer
	images []gooPDFImage
	gray   float64 // current fill gray for text
	Title  string
}

type gooPDFImage struct {
	data          []byte
	width, height int
	comps         int
}

// Paper sizes in points.
const (
	gooPDFA4W, gooPDFA4H         = 595.28, 841.89
	gooPDFLetterW, gooPDFLetterH = 612, 792
)

func newGooPDF(w, h float64) *gooPDF { return &gooPDF{W: w, H: h} }

// AddPage starts a new page; drawing goes to the last page.
func (p *gooPDF) AddPage() { p.pages = append(p.pages, &bytes.Buffer{}) }

// PageCount is the number of pages.
func (p *gooPDF) PageCount() int { return len(p.pages) }

func (p *gooPDF) page(i int) *bytes.Buffer {
	if len(p.pages) == 0 {
		p.AddPage()
	}
	if i < 0 || i >= len(p.pages) {
		return p.pages[len(p.pages)-1]
	}
	return p.pages[i]
}

func gooPDFNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// TextOn draws text on page i (-1 = the current page) with its baseline at y.
func (p *gooPDF) TextOn(i int, x, y, size float64, bold bool, s string) {
	if s == "" {
		return
	}
	font := "F1"
	if bold {
		font = "F2"
	}
	b := p.page(i)
	fmt.Fprintf(b, "BT %s g /%s %s Tf %s %s Td (", gooPDFNum(p.gray), font, gooPDFNum(size), gooPDFNum(x), gooPDFNum(p.H-y))
	b.Write(gooPDFEscape(gooPDFEncode(s)))
	b.WriteString(") Tj ET\n")
}

// Text draws text on the current page.
func (p *gooPDF) Text(x, y, size float64, bold bool, s string) { p.TextOn(-1, x, y, size, bold, s) }

// TextRight draws text ending at x.
func (p *gooPDF) TextRight(x, y, size float64, bold bool, s string) {
	p.Text(x-gooPDFWidth(s, size, bold), y, size, bold, s)
}

// TextCenterOn draws text centered on x on page i.
func (p *gooPDF) TextCenterOn(i int, x, y, size float64, bold bool, s string) {
	p.TextOn(i, x-gooPDFWidth(s, size, bold)/2, y, size, bold, s)
}

// SetGray sets the text gray (0 black … 1 white).
func (p *gooPDF) SetGray(g float64) { p.gray = g }

// Line draws a line.
func (p *gooPDF) Line(x1, y1, x2, y2, width, gray float64) {
	fmt.Fprintf(p.page(-1), "%s G %s w %s %s m %s %s l S\n", gooPDFNum(gray), gooPDFNum(width), gooPDFNum(x1), gooPDFNum(p.H-y1), gooPDFNum(x2), gooPDFNum(p.H-y2))
}

// Rect fills a rectangle (top-left x, y).
func (p *gooPDF) Rect(x, y, w, h, gray float64) {
	fmt.Fprintf(p.page(-1), "%s g %s %s %s %s re f\n", gooPDFNum(gray), gooPDFNum(x), gooPDFNum(p.H-y-h), gooPDFNum(w), gooPDFNum(h))
}

// AddJPEG registers a JPEG (other image formats are converted) and returns its index.
func (p *gooPDF) AddJPEG(data []byte) (int, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return 0, err
		}
		// Flatten transparency on white and re-encode.
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
		draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Over)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 90}); err != nil {
			return 0, err
		}
		data = buf.Bytes()
	}
	w, h, comps, err := gooJPEGInfo(data)
	if err != nil {
		return 0, err
	}
	p.images = append(p.images, gooPDFImage{data: data, width: w, height: h, comps: comps})
	return len(p.images) - 1, nil
}

// ImageSize is an image's pixel size.
func (p *gooPDF) ImageSize(i int) (int, int) {
	if i < 0 || i >= len(p.images) {
		return 0, 0
	}
	return p.images[i].width, p.images[i].height
}

// Image draws image i at x, y (top-left) scaled to w × h points.
func (p *gooPDF) Image(i int, x, y, w, h float64) {
	if i < 0 || i >= len(p.images) {
		return
	}
	fmt.Fprintf(p.page(-1), "q %s 0 0 %s %s %s cm /Im%d Do Q\n", gooPDFNum(w), gooPDFNum(h), gooPDFNum(x), gooPDFNum(p.H-y-h), i+1)
}

// gooJPEGInfo reads a JPEG's size and components from its SOF marker.
func gooJPEGInfo(d []byte) (int, int, int, error) {
	if len(d) < 4 || d[0] != 0xFF || d[1] != 0xD8 {
		return 0, 0, 0, errors.New("not a JPEG")
	}
	i := 2
	for i+4 <= len(d) {
		if d[i] != 0xFF {
			i++
			continue
		}
		m := d[i+1]
		if m == 0xFF {
			i++
			continue
		}
		if m == 0xD8 || m == 0x01 || (m >= 0xD0 && m <= 0xD7) {
			i += 2
			continue
		}
		if i+4 > len(d) {
			break
		}
		l := int(d[i+2])<<8 | int(d[i+3])
		if m >= 0xC0 && m <= 0xCF && m != 0xC4 && m != 0xC8 && m != 0xCC {
			if i+9 >= len(d) {
				break
			}
			h := int(d[i+5])<<8 | int(d[i+6])
			w := int(d[i+7])<<8 | int(d[i+8])
			c := int(d[i+9])
			if w == 0 || h == 0 || (c != 1 && c != 3 && c != 4) {
				return 0, 0, 0, errors.New("unsupported JPEG")
			}
			return w, h, c, nil
		}
		if m == 0xDA || l < 2 {
			break
		}
		i += 2 + l
	}
	return 0, 0, 0, errors.New("JPEG size not found")
}

// Bytes assembles the document: catalog, pages, the two fonts, images, page contents, info, xref.
func (p *gooPDF) Bytes() []byte {
	if len(p.pages) == 0 {
		p.AddPage()
	}
	var out bytes.Buffer
	var offsets []int
	obj := func(body func()) int {
		offsets = append(offsets, out.Len())
		n := len(offsets)
		fmt.Fprintf(&out, "%d 0 obj\n", n)
		body()
		out.WriteString("\nendobj\n")
		return n
	}
	out.WriteString("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n")
	nPages := len(p.pages)
	nImages := len(p.images)
	// Object numbers: 1 catalog, 2 pages, 3 F1, 4 F2, 5.. images, then page + content pairs, then info.
	firstImage := 5
	firstPage := firstImage + nImages
	infoObj := firstPage + 2*nPages
	obj(func() { out.WriteString("<< /Type /Catalog /Pages 2 0 R >>") })
	obj(func() {
		out.WriteString("<< /Type /Pages /Kids [")
		for i := 0; i < nPages; i++ {
			fmt.Fprintf(&out, "%d 0 R ", firstPage+2*i)
		}
		fmt.Fprintf(&out, "] /Count %d >>", nPages)
	})
	obj(func() {
		out.WriteString("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	})
	obj(func() {
		out.WriteString("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	})
	for _, im := range p.images {
		im := im
		obj(func() {
			cs, decode := "/DeviceRGB", ""
			switch im.comps {
			case 1:
				cs = "/DeviceGray"
			case 4:
				cs, decode = "/DeviceCMYK", " /Decode [1 0 1 0 1 0 1 0]"
			}
			fmt.Fprintf(&out, "<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode%s /Length %d >>\nstream\n",
				im.width, im.height, cs, decode, len(im.data))
			out.Write(im.data)
			out.WriteString("\nendstream")
		})
	}
	xobjects := ""
	if nImages > 0 {
		var b strings.Builder
		b.WriteString(" /XObject <<")
		for i := 0; i < nImages; i++ {
			fmt.Fprintf(&b, " /Im%d %d 0 R", i+1, firstImage+i)
		}
		b.WriteString(" >>")
		xobjects = b.String()
	}
	for i, pg := range p.pages {
		content := firstPage + 2*i + 1
		obj(func() {
			fmt.Fprintf(&out, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Resources << /Font << /F1 3 0 R /F2 4 0 R >>%s >> /Contents %d 0 R >>",
				gooPDFNum(p.W), gooPDFNum(p.H), xobjects, content)
		})
		obj(func() {
			fmt.Fprintf(&out, "<< /Length %d >>\nstream\n", pg.Len())
			out.Write(pg.Bytes())
			out.WriteString("\nendstream")
		})
	}
	obj(func() {
		out.WriteString("<< /Producer (Pursae Commerce PDF Invoices) /Title (")
		out.Write(gooPDFEscape(gooPDFEncode(p.Title)))
		out.WriteString(") >>")
	})
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, infoObj, xref)
	return out.Bytes()
}

// gooPDFEscape escapes a literal string's delimiters.
func gooPDFEscape(b []byte) []byte {
	var out bytes.Buffer
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			out.WriteByte('\\')
			out.WriteByte(c)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteByte(' ')
		default:
			out.WriteByte(c)
		}
	}
	return out.Bytes()
}

// WinAnsi (CP1252) code points 0x80–0x9F.
var gooWinAnsiHigh = map[rune]byte{
	'€': 0x80, '‚': 0x82, 'ƒ': 0x83, '„': 0x84, '…': 0x85, '†': 0x86, '‡': 0x87, 'ˆ': 0x88, '‰': 0x89, 'Š': 0x8A, '‹': 0x8B, 'Œ': 0x8C,
	'Ž': 0x8E, '‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97, '˜': 0x98, '™': 0x99, 'š': 0x9A, '›': 0x9B,
	'œ': 0x9C, 'ž': 0x9E, 'Ÿ': 0x9F,
}

// Characters WinAnsi lacks, written as their usual Latin transliteration.
var gooPDFTranslit = map[rune]string{
	'ş': "s", 'Ş': "S", 'ğ': "g", 'Ğ': "G", 'ı': "i", 'İ': "I", '₺': "TL", 'ł': "l", 'Ł': "L", 'đ': "d", 'Đ': "D", 'ħ': "h", 'ŋ': "ng",
	'ő': "ö", 'Ő': "Ö", 'ű': "ü", 'Ű': "Ü", 'ț': "t", 'Ț': "T", 'ș': "s", 'Ș': "S", '₽': "RUB", '₹': "Rs", '₩': "W", '₪': "ILS", '₫': "d",
	'₴': "UAH", '₦': "N", '₱': "PHP", '₸': "KZT", '₼': "AZN", '₾': "GEL", ' ': " ", ' ': " ", ' ': " ", '−': "-", '≥': ">=", '≤': "<=",
	'→': "->", '←': "<-", '✓': "v", '×': "x",
}

// gooPDFEncode converts UTF-8 text to WinAnsiEncoding bytes.
func gooPDFEncode(s string) []byte {
	out := make([]byte, 0, len(s))
	var put func(r rune)
	put = func(r rune) {
		switch {
		case r == '×':
			out = append(out, 0xD7) // in Latin-1
		case r < 0x80:
			if r >= 0x20 || r == '\n' || r == '\r' || r == '\t' {
				out = append(out, byte(r))
			}
		case r >= 0xA0 && r <= 0xFF:
			out = append(out, byte(r))
		default:
			if b, ok := gooWinAnsiHigh[r]; ok {
				out = append(out, b)
				return
			}
			if t, ok := gooPDFTranslit[r]; ok {
				for _, x := range t {
					put(x)
				}
				return
			}
			// Decompose (ā → a + ¯) and keep the letters.
			done := false
			for _, x := range norm.NFD.String(string(r)) {
				if unicode.Is(unicode.Mn, x) {
					continue
				}
				if x != r && (x < 0x80 || (x >= 0xA0 && x <= 0xFF)) {
					out = append(out, byte(x))
					done = true
				}
			}
			if !done {
				out = append(out, '?')
			}
		}
	}
	for _, r := range s {
		put(r)
	}
	return out
}

// Helvetica / Helvetica-Bold advance widths (1/1000 em) for ASCII 32–126.
var gooHelv = [95]int{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
	1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
	333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556, 556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584,
}

var gooHelvBold = [95]int{
	278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278, 556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611,
	975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778, 667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556,
	333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611, 611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584,
}

// Widths of the WinAnsi characters above 127 that differ from their base letter.
var gooHelvHigh = map[byte][2]int{
	0x80: {556, 556}, 0x85: {1000, 1000}, 0x91: {222, 278}, 0x92: {222, 278}, 0x93: {333, 500}, 0x94: {333, 500}, 0x95: {350, 350},
	0x96: {556, 556}, 0x97: {1000, 1000}, 0x99: {1000, 1000}, 0xA0: {278, 278}, 0xA9: {737, 737}, 0xAE: {737, 737}, 0xB0: {400, 400},
	0xC6: {1000, 1000}, 0xD7: {584, 584}, 0xDF: {611, 611}, 0xE6: {889, 889}, 0xD8: {778, 778}, 0xF8: {611, 611}, 0xA3: {556, 556},
	0xA5: {556, 556}, 0xA7: {556, 556}, 0xAB: {556, 556}, 0xBB: {556, 556}, 0xB7: {278, 278},
}

// gooPDFWidth is a text's width in points.
func gooPDFWidth(s string, size float64, bold bool) float64 {
	var w int
	for _, c := range gooPDFEncode(s) {
		w += gooPDFCharWidth(c, bold)
	}
	return float64(w) * size / 1000
}

func gooPDFCharWidth(c byte, bold bool) int {
	tbl := &gooHelv
	if bold {
		tbl = &gooHelvBold
	}
	if c >= 32 && c <= 126 {
		return tbl[c-32]
	}
	if v, ok := gooHelvHigh[c]; ok {
		if bold {
			return v[1]
		}
		return v[0]
	}
	if c >= 0xC0 {
		// An accented Latin-1 letter: its base letter's width.
		base := norm.NFD.String(string(rune(c)))
		if len(base) > 0 && base[0] >= 32 && base[0] <= 126 {
			return tbl[base[0]-32]
		}
	}
	return 556
}

// gooPDFWrap breaks text into lines of at most maxW points (words longer than a line are cut).
func gooPDFWrap(s string, size float64, bold bool, maxW float64) []string {
	var lines []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		cur := ""
		for _, w := range words {
			for gooPDFWidth(w, size, bold) > maxW && len([]rune(w)) > 1 {
				// Cut an over-long word.
				r := []rune(w)
				n := len(r)
				for n > 1 && gooPDFWidth(string(r[:n]), size, bold) > maxW {
					n--
				}
				if cur != "" {
					lines = append(lines, cur)
					cur = ""
				}
				lines = append(lines, string(r[:n]))
				w = string(r[n:])
			}
			try := w
			if cur != "" {
				try = cur + " " + w
			}
			if gooPDFWidth(try, size, bold) <= maxW || cur == "" {
				cur = try
			} else {
				lines = append(lines, cur)
				cur = w
			}
		}
		lines = append(lines, cur)
	}
	return lines
}
