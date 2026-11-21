package server

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The login screens draw the Pursae mark as an inline SVG whose eyes follow the pointer and blink
// (login.html, login-mascot.js). That only makes sense for the stock mark: when the Hub replaced the
// logo (brand.go) with a different image, the screens keep showing that image as before.
//
// brandStockLogo tells the two apart. The mark is stock when the Hub has no logo override, or when
// every override (light and dark) is an SVG drawing the shipped mark: the same P outline path data as
// web/wp-includes/images/brand/logo.svg, in any color.

// brandStockState caches brandStockLogo's answer against the override files it looked at.
type brandStockState struct {
	mu    sync.Mutex
	key   string
	stock bool
}

// brandStockLogo reports whether the site shows the stock Pursae mark (no Hub logo, or a Hub logo
// that is the stock mark recolored).
func (s *Server) brandStockLogo() bool {
	var key strings.Builder
	var files []string
	for _, name := range []string{brandLight, brandDark} {
		p, ct := s.brandOverrideFile(name)
		if p == "" {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			return false
		}
		if ct != "image/svg+xml" {
			return false // a PNG or WebP logo is never the animated mark
		}
		key.WriteString(p + "|" + st.ModTime().Format(time.RFC3339Nano) + "|" + strconv.FormatInt(st.Size(), 10) + ";")
		files = append(files, p)
	}
	if len(files) == 0 {
		return true
	}
	c := &s.brandStock
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key == key.String() {
		return c.stock
	}
	stock := true
	want := s.brandStockPath()
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil || want == "" || svgMarkPath(b) != want {
			stock = false
			break
		}
	}
	c.key, c.stock = key.String(), stock
	return stock
}

// brandStockPath is the normalized path data of the shipped mark.
func (s *Server) brandStockPath() string {
	b, err := fs.ReadFile(s.web, "wp-includes/images/brand/logo.svg")
	if err != nil {
		return ""
	}
	return svgMarkPath(b)
}

// svgMarkPath is the normalized d of the only <path> in an SVG document; "" when the document is not
// an SVG with exactly one path.
func svgMarkPath(b []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	var d string
	paths, root := 0, false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ""
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !root {
			if el.Name.Local != "svg" {
				return ""
			}
			root = true
			continue
		}
		if el.Name.Local != "path" {
			continue
		}
		paths++
		for _, a := range el.Attr {
			if a.Name.Local == "d" {
				d = a.Value
			}
		}
	}
	if !root || paths != 1 {
		return ""
	}
	return normalizePathData(d)
}

// normalizePathData collapses the separators of SVG path data, so "M14 59C13.2,50" and
// "M 14 59 C 13.2 50" compare equal.
func normalizePathData(d string) string {
	var b strings.Builder
	space := false
	for _, r := range d {
		switch {
		case r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r':
			space = true
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = true
		default:
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		}
	}
	return b.String()
}
