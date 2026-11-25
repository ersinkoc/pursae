package server

import (
	"bytes"
	"math/rand"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The page scanners (scanBlockClasses, gocdnUploadsURL, mediaImgTags) replace regexps that cost most
// of a page's post-processing. Each is checked here against its old regexp, kept as the oracle, on
// fixed cases and random inputs built from the pieces that matter to the pattern.

var (
	blockClassReOracle  = regexp.MustCompile(`wp-block-([a-z0-9]+(?:-[a-z0-9]+)*)`)
	uploadsURLReOracle  = regexp.MustCompile(`(?:https?:)?(?://([A-Za-z0-9.\-]+(?::\d+)?))?/wp-content/uploads/([^\s"'<>()\\?#,&;]+)`)
	mediaImgTagReOracle = regexp.MustCompile(`(?is)<img\s[^>]*>`)
)

func fuzzRounds(t *testing.T, n int) int {
	if testing.Short() {
		return n / 10
	}
	return n
}

// scanTestPage is a rendered page of block markup with upload images.
func scanTestPage() []byte {
	var sb []byte
	sb = append(sb, "<!DOCTYPE html><html lang=\"en\"><head>"+blockStylesMarker+"</head><body>"...)
	for range 150 {
		sb = append(sb, `<div class="wp-block-group is-layout-constrained wp-block-group-is-layout-constrained"><h2 class="wp-block-heading">Title</h2><p class="has-text-color">Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.</p><figure class="wp-block-image size-large"><img src="/wp-content/uploads/2026/01/a.jpg" width="800" height="600" alt=""/></figure><div class="wp-block-buttons is-layout-flex"><div class="wp-block-button"><a class="wp-block-button__link wp-element-button" href="/x">Go</a></div></div></div>`...)
	}
	sb = append(sb, "</body></html>"...)
	return sb
}

func randomPieces(r *rand.Rand, pieces []string, maxN int) []byte {
	var b []byte
	for k := r.Intn(maxN); k >= 0; k-- {
		b = append(b, pieces[r.Intn(len(pieces))]...)
	}
	return b
}

// pageBlocksOracle is pageBlocks as it was, on the regexp.
func (s *Server) pageBlocksOracle(page []byte) []string {
	dirs := s.blockStyleDirs()
	found := map[string]bool{}
	for _, m := range blockClassReOracle.FindAllSubmatch(page, -1) {
		name := string(m[1])
		for name != "" {
			if dirs[name] {
				found[name] = true
				break
			}
			i := strings.LastIndexByte(name, '-')
			if i < 0 {
				break
			}
			name = name[:i]
		}
	}
	if found["gallery"] {
		found["image"] = true
	}
	out := make([]string, 0, len(found))
	for n := range found {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestScanBlockClassesMatchesRegexp(t *testing.T) {
	check := func(b []byte) {
		t.Helper()
		var want, got []string
		for _, m := range blockClassReOracle.FindAllSubmatch(b, -1) {
			want = append(want, string(m[1]))
		}
		scanBlockClasses(b, func(x []byte) { got = append(got, string(x)) })
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%q:\nwant %q\n got %q", b, want, got)
		}
	}
	for _, c := range []string{"", "wp-block-", "wp-block-a", "wp-block--a", "wp-block-a-", "wp-block-a--b", "wp-block-wp-block-x",
		"wp-wp-block-post-title-is-layout-flow", "xwp-block-A wp-block-9z-q_", "wp-block-é wp-block-a-b-c-d"} {
		check([]byte(c))
	}
	alpha := []byte("wp-block-ab-c-1--x Z_")
	r := rand.New(rand.NewSource(1))
	for range fuzzRounds(t, 200000) {
		b := make([]byte, r.Intn(60))
		for i := range b {
			if r.Intn(6) == 0 && i+9 <= len(b) {
				copy(b[i:], "wp-block-")
			} else {
				b[i] = alpha[r.Intn(len(alpha))]
			}
		}
		check(b)
	}
	check(scanTestPage())
	s := &Server{web: os.DirFS("../../web")}
	page := append(scanTestPage(), `<div class="wp-block-gallery wp-block-post-title-is-layout-flow wp-block-quote wp-block-code">`...)
	if got, want := s.pageBlocks(page), s.pageBlocksOracle(page); !reflect.DeepEqual(got, want) || len(want) == 0 {
		t.Fatalf("pageBlocks %v, regexp version %v", got, want)
	}
}

func TestGocdnUploadsURLMatchesRegexp(t *testing.T) {
	check := func(b []byte) {
		t.Helper()
		want := uploadsURLReOracle.FindAllSubmatchIndex(b, -1)
		got := gocdnUploadsURL.FindAllSubmatchIndex(b, -1)
		if len(want) == 0 && len(got) == 0 {
			return
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%q:\nwant %v\n got %v", b, want, got)
		}
	}
	for _, c := range []string{"/wp-content/uploads/", "/wp-content/uploads/a", "https:/wp-content/uploads/a", "https:///wp-content/uploads/a",
		"http://h:80/wp-content/uploads/a b", "//a:80:90/wp-content/uploads/x", "http://wp-content/uploads/wp-content/uploads/x",
		"x//h-1.b/wp-content/uploads/a?b /wp-content/uploads/\"/wp-content/uploads/c,d", "http:https://h/wp-content/uploads/x",
		`<img src="http://site.test/wp-content/uploads/2026/10/a-300x200.jpg 300w, //site.test/wp-content/uploads/2026/10/a.jpg">`} {
		check([]byte(c))
	}
	pieces := []string{"/wp-content/uploads/", "https:", "http:", "//", "a.b", ":80", "8", "x", " ", "\"", "?", "/", ":", "-", "h", "/wp-content/", "uploads/", "é", "\xff", "\v", "\t", "9"}
	r := rand.New(rand.NewSource(2))
	for range fuzzRounds(t, 300000) {
		check(randomPieces(r, pieces, 10))
	}
	check(scanTestPage())
}

func TestMediaImgTagsMatchesRegexp(t *testing.T) {
	// fix changes some tags (by content), so both the copy and the no-change paths run.
	fixes := map[string]func(string) string{
		"same": func(tag string) string { return tag },
		"some": func(tag string) string {
			if strings.Contains(tag, "a") {
				return strings.Replace(tag, "a", "[A]", 1)
			}
			return tag
		},
		"all": func(tag string) string { return "<" + tag + ">" },
	}
	check := func(b []byte) {
		t.Helper()
		for name, fix := range fixes {
			want := mediaImgTagReOracle.ReplaceAllFunc(bytes.Clone(b), func(tag []byte) []byte { return []byte(fix(string(tag))) })
			got := mediaImgTags(bytes.Clone(b), fix)
			if !bytes.Equal(want, got) {
				t.Fatalf("%s %q:\nwant %q\n got %q", name, b, want, got)
			}
		}
	}
	for _, c := range []string{"", "<img", "<img>", "<img >", "<IMG\tsrc=a>", "<iMg\nx", "<img\fa><img\rb>", "<imga >", "<img\v a>",
		"<<img a>>", "<img a<img b>", "<img\u00a0a>", "<img a>tail"} {
		check([]byte(c))
	}
	pieces := []string{"<img", "<IMG", "<Img", "<im", "g", "<", ">", " ", "\t", "\n", "\v", "a", "src=x", "é", "\u00a0"}
	r := rand.New(rand.NewSource(3))
	for range fuzzRounds(t, 200000) {
		check(randomPieces(r, pieces, 12))
	}
	check(scanTestPage())
}

func BenchmarkPageBlocks(b *testing.B) {
	s := &Server{web: os.DirFS("../../web")}
	page := scanTestPage()
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for b.Loop() {
		_ = s.pageBlocks(page)
	}
}

func BenchmarkGocdnUploadsURL(b *testing.B) {
	page := scanTestPage()
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for b.Loop() {
		_ = gocdnUploadsURL.FindAllSubmatchIndex(page, -1)
	}
}

func BenchmarkMediaImgTags(b *testing.B) {
	page := scanTestPage()
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for b.Loop() {
		_ = mediaImgTags(page, func(tag string) string { return tag })
	}
}
