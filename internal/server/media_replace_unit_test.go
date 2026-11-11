package server

import (
	"image"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/gocdn"
	"wpgo/internal/media"
	"wpgo/internal/store"
)

// No database: the markup rewriting, the version query, focal parsing and the move map.

func testMediaIndexServer() *Server {
	s := &Server{}
	f := &media.Focal{X: 0.25, Y: 0.1}
	s.mediaIdx.put(7, map[string]mediaFileInfo{
		"2026/11/photo.jpg":         {ID: 7, Ver: "abc1", Focal: f, W: 1200, H: 1200},
		"2026/11/photo-300x300.jpg": {ID: 7, Ver: "abc1", Focal: f, W: 300, H: 300},
		"2026/11/photo-300x200.jpg": {ID: 7, Focal: f, W: 300, H: 300, Moved: "2026/11/photo-300x300.jpg"},
	})
	return s
}

func TestMediaFixImgTag(t *testing.T) {
	s := testMediaIndexServer()
	hosts := map[string]bool{"example.test": true}
	cases := []struct{ in, want string }{
		// Focal point and the new aspect ratio of a moved size (300x200 → 300x300).
		{`<img src="/wp-content/uploads/2026/11/photo-300x200.jpg" width="300" height="200" class="wp-image-7">`,
			`<img style="object-position:25% 10%;" src="/wp-content/uploads/2026/11/photo-300x200.jpg" width="300" height="300" class="wp-image-7">`},
		// An existing style keeps its declarations after ours; a scaled-down width keeps its ratio.
		{`<img src="https://example.test/wp-content/uploads/2026/11/photo.jpg?v=old" width="600" height="400" style="border:0">`,
			`<img src="https://example.test/wp-content/uploads/2026/11/photo.jpg?v=old" width="600" height="600" style="object-position:25% 10%;border:0">`},
		// The tag positions itself: nothing to add; object-fit: the shape is the tag's own.
		{`<img src="/wp-content/uploads/2026/11/photo.jpg" width="600" height="400" style='object-fit:cover;object-position:0 0'>`,
			`<img src="/wp-content/uploads/2026/11/photo.jpg" width="600" height="400" style='object-fit:cover;object-position:0 0'>`},
		// Another site's uploads and unknown files are left alone.
		{`<img src="https://other.test/wp-content/uploads/2026/11/photo.jpg" width="1" height="2">`,
			`<img src="https://other.test/wp-content/uploads/2026/11/photo.jpg" width="1" height="2">`},
		{`<img src="/wp-content/uploads/2026/11/other.jpg">`, `<img src="/wp-content/uploads/2026/11/other.jpg">`},
	}
	for _, c := range cases {
		if got := s.mediaFixImgTag(c.in, hosts); got != c.want {
			t.Errorf("\n in: %s\ngot: %s\nwant: %s", c.in, got, c.want)
		}
	}
}

func TestMediaVersionURLs(t *testing.T) {
	s := testMediaIndexServer()
	hosts := map[string]bool{"example.test": true}
	in := `<a href="/wp-content/uploads/2026/11/photo.jpg">x</a>` +
		`<img src="http://example.test/wp-content/uploads/2026/11/photo-300x200.jpg" srcset="/wp-content/uploads/2026/11/photo.jpg?v=old 1200w, /wp-content/uploads/2026/11/photo-300x300.jpg 300w">` +
		`<div style="background-image:url(/wp-content/uploads/2026/11/photo.jpg)"></div>` +
		`<img src="/wp-content/uploads/2026/11/photo.jpg?w=200&amp;fm=webp">` +
		`<img src="https://other.test/wp-content/uploads/2026/11/photo.jpg">` +
		`<img src="/wp-content/uploads/2026/11/unrelated.jpg">`
	want := `<a href="/wp-content/uploads/2026/11/photo.jpg?v=abc1">x</a>` +
		`<img src="http://example.test/wp-content/uploads/2026/11/photo-300x300.jpg?v=abc1" srcset="/wp-content/uploads/2026/11/photo.jpg?v=abc1 1200w, /wp-content/uploads/2026/11/photo-300x300.jpg?v=abc1 300w">` +
		`<div style="background-image:url(/wp-content/uploads/2026/11/photo.jpg?v=abc1)"></div>` +
		`<img src="/wp-content/uploads/2026/11/photo.jpg?w=200&amp;fm=webp">` +
		`<img src="https://other.test/wp-content/uploads/2026/11/photo.jpg">` +
		`<img src="/wp-content/uploads/2026/11/unrelated.jpg">`
	if got := string(s.mediaVersionURLs([]byte(in), hosts)); got != want {
		t.Errorf("\ngot:  %s\nwant: %s", got, want)
	}
	// Nothing indexed: the body is returned as it is.
	if got := (&Server{}).mediaVersionURLs([]byte(in), hosts); string(got) != in {
		t.Error("empty index changed the body")
	}
}

func TestMediaVersionQueryLen(t *testing.T) {
	for in, want := range map[string]int{`?v=abc1"`: 7, `?v=abc1`: 7, `?v=abc1&w=1"`: 0, `?w=1"`: 0, `?v="`: 0, `?v=AB"`: 0, `?v=9z) `: 5} {
		if got := mediaVersionQueryLen([]byte(in)); got != want {
			t.Errorf("%q: %d, want %d", in, got, want)
		}
	}
}

func TestParseFocal(t *testing.T) {
	for in, want := range map[string]*media.Focal{"0.25,0.1": {X: 0.25, Y: 0.1}, " 30% , 70% ": {X: 0.3, Y: 0.7}, "": nil, "reset": nil} {
		got, ok := parseFocal(in)
		if !ok || (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%q: %v %v", in, got, ok)
		}
	}
	for _, bad := range []string{"1.2,0", "a,b", "0.5", "-1,0"} {
		if _, ok := parseFocal(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if f, ok := focalFromJSON(map[string]any{"x": 0.1, "y": 0.9}); !ok || f.X != 0.1 || f.Y != 0.9 {
		t.Errorf("json object: %v %v", f, ok)
	}
	if f, ok := focalFromJSON(nil); !ok || f != nil {
		t.Errorf("json null: %v %v", f, ok)
	}
	if _, ok := focalFromJSON(map[string]any{"x": 2.0, "y": 0.5}); ok {
		t.Error("out of range accepted")
	}
}

func TestMediaMoves(t *testing.T) {
	old := &store.Attachment{FilePath: "a/photo-scaled.jpg", Meta: map[string]any{"original_image": "photo.jpg", "backup_file": "a/photo-e1.jpg"},
		Variants: []store.AttachmentVariant{{Name: "thumbnail", FilePath: "a/photo-150x150.jpg"}, {Name: "medium", FilePath: "a/photo-300x200.jpg"}, {Name: "large", FilePath: "a/photo-1024x683.jpg"}}}
	cur := &store.Attachment{FilePath: "a/photo.png", Meta: map[string]any{},
		Variants: []store.AttachmentVariant{{Name: "thumbnail", FilePath: "a/photo-150x150.png"}, {Name: "medium", FilePath: "a/photo-300x300.png"}}}
	got := mediaMoves(old, gocdnAttachmentFiles(old), cur)
	want := map[string]string{
		"a/photo-scaled.jpg": "a/photo.png", "a/photo.jpg": "a/photo.png", "a/photo-e1.jpg": "a/photo.png",
		"a/photo-150x150.jpg": "a/photo-150x150.png", "a/photo-300x200.jpg": "a/photo-300x300.png",
		"a/photo-1024x683.jpg": "a/photo.png", // no large size any more: the main file
	}
	if len(got) != len(want) {
		t.Fatalf("moves: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s → %s, want %s", k, got[k], v)
		}
	}
	// Same names: nothing moves.
	if m := mediaMoves(cur, gocdnAttachmentFiles(cur), cur); len(m) != 0 {
		t.Errorf("same files moved: %v", m)
	}
}

func TestReplaceSource(t *testing.T) {
	for _, c := range []struct {
		a         *store.Attachment
		src, stem string
	}{
		{&store.Attachment{FilePath: "x/p.jpg", Meta: map[string]any{}}, "x/p.jpg", "p"},
		{&store.Attachment{FilePath: "x/p-scaled.jpg", Meta: map[string]any{"original_image": "p.jpg"}}, "x/p.jpg", "p"},
		{&store.Attachment{FilePath: "x/p.webp", Meta: map[string]any{"original_image": "p.jpg"}}, "x/p.jpg", "p"},
		{&store.Attachment{FilePath: "x/p-e123-scaled.jpg", Meta: map[string]any{"original_image": "p.jpg", "backup_file": "x/p.jpg"}}, "x/p-e123-scaled.jpg", "p-e123"},
		{&store.Attachment{FilePath: "x/p-scaled.jpg", Meta: map[string]any{}}, "x/p-scaled.jpg", "p-scaled"}, // a file uploaded under that name
	} {
		src, stem := replaceSource(c.a)
		if src != c.src || stem != c.stem {
			t.Errorf("%s: %s %s", c.a.FilePath, src, stem)
		}
	}
}

func TestGoCDNCoverFocal(t *testing.T) {
	q, _ := url.ParseQuery("w=100&h=100&fit=cover&fx=0.1&fy=0.5")
	p, err := gocdn.ParseImageParams(q)
	if err != nil || !p.Focal {
		t.Fatalf("parse: %+v %v", p, err)
	}
	if !strings.Contains(p.Canonical(), "fx=0.1&fy=0.5") {
		t.Errorf("canonical: %s", p.Canonical())
	}
	if _, _, src := gocdn.OutputSize(1000, 500, p); src != image.Rect(0, 0, 500, 500) {
		t.Errorf("focal cover: %v", src)
	}
	p.Focal = false
	if _, _, src := gocdn.OutputSize(1000, 500, p); src != image.Rect(250, 0, 750, 500) {
		t.Errorf("centre cover: %v", src)
	}
	if _, err := gocdn.ParseImageParams(url.Values{"fx": {"2"}, "fy": {"0"}}); err == nil {
		t.Error("fx out of range accepted")
	}
}
