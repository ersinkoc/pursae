package server

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/config"
	"wpgo/internal/webbuilder"
)

// The literal scanners (webbuilder_scan.go) must return exactly what the regular expressions they
// replace return. The expressions are kept here as the reference.
var (
	refCSSURLRE = regexp.MustCompile(`(?i)(?:url\(\s*|@import\s+)(?:&#39;|&quot;|['"])?(https://[^'"\s)&;]+)`)
	refMainRE   = regexp.MustCompile(`(?i)<main[\s>]|\srole\s*=\s*["']?main\b`)
	refKitRefRE = regexp.MustCompile(`(/wp-content/uploads/web-builder/kits/([a-z0-9-]{1,64})/[A-Za-z0-9_./@+-]{1,512}?\.[A-Za-z0-9]{1,8})(\?v=\d+)?(["'\s)>,])`)
)

func refCSSURLs(src string) []string {
	var out []string
	for _, m := range refCSSURLRE.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

func scanCSSURLs(src string) []string {
	var out []string
	wbCSSURLs(src, func(u string) { out = append(out, u) })
	return out
}

func refKitRefs(src string) []string {
	var out []string
	for _, m := range refKitRefRE.FindAllStringSubmatchIndex(src, -1) {
		out = append(out, src[m[0]:m[1]]+"|"+src[m[2]:m[3]]+"|"+src[m[4]:m[5]]+"|"+src[m[8]:m[9]])
	}
	return out
}

func scanKitRefs(src string) []string {
	var out []string
	wbKitRefs(src, func(start, end int, ref, slug, term string) {
		out = append(out, src[start:end]+"|"+ref+"|"+slug+"|"+term)
	})
	return out
}

// checkScanners compares every scanner with its expression on src.
func checkScanners(t *testing.T, src string) {
	t.Helper()
	if got, want := scanCSSURLs(src), refCSSURLs(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("wbCSSURLs(%q)\n got %q\nwant %q", src, got, want)
	}
	if got, want := wbHasMainLandmark(src), refMainRE.MatchString(src); got != want {
		t.Fatalf("wbHasMainLandmark(%q) = %v, want %v", src, got, want)
	}
	if got, want := scanKitRefs(src), refKitRefs(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("wbKitRefs(%q)\n got %q\nwant %q", src, got, want)
	}
}

var scanCases = []string{
	"", "url(", "url(https://", "url(https://a", "URL( 'HTTPS://Fonts.Example/x.css' )", "url(&quot;https://a.b/c&quot;)",
	"url(&QUOT;https://a.b/c)", "url(&#39;https://a.b/c&#39;)", `url("https://a.b/c")`, "url(\"\"https://a.b)",
	"@import 'https://x.y/z.css';", "@import\t\nhttps://x.y/z.css", "@importhttps://x.y", "@IMPORT url(https://a.b)",
	"@import url( https://a.b/c)", "xurl(https://a.b)url(https://c.d)", "url(https://a.b/url(https://c.d))",
	"url(httpſ://a.b)", "url(HTTPſ://a.b)", "@ımport https://a.b", "url(https://)", "url(https://;)", "url(https://a&b)",
	"url(\vhttps://a)", "url(\fhttps://a.b)", "url(https://a.b\xff\xfe/c)", "@@import https://a.b", "u@import https://a.b",
	"(url(https://a.b)", "url(url(https://a.b)", "url(  'https://a.b' ) @import \"https://c.d\"",
	"<main>", "<MAIN class=x>", "<main", "<mainx>", "<main\n", "< main>", "x role=main", "x role = 'main'", "x ROLE=\"Main\"",
	"xrole=main", "x role=mainly", "x role=main-x", "x role=main_x", "x role=\"\"main", "x role=main", "\trole\t=\tmain", "role=main",
	" role=\"mai", " role=", "=", " role==main", " data-role=main", " role =main9", "<div role=\"main\">",
	`<link href="/wp-content/uploads/web-builder/kits/kit-a/css/style.min.css">`,
	`<script src="/wp-content/uploads/web-builder/kits/kit-a/js/app.js?v=3"></script>`,
	`/wp-content/uploads/web-builder/kits/kit-a/x.css?v=3a"`, `/wp-content/uploads/web-builder/kits/kit-a/x.css?v="`,
	`/wp-content/uploads/web-builder/kits/kit-a/x.abcdefghi"`, `/wp-content/uploads/web-builder/kits/kit-a/x.abcdefgh"`,
	`/wp-content/uploads/web-builder/kits/Kit/x.css"`, `/wp-content/uploads/web-builder/kits/kit-a/"x.css"`,
	`/wp-content/uploads/web-builder/kits/kit-a/.css"`, `/wp-content/uploads/web-builder/kits/kit-a/a..css"`,
	`/wp-content/uploads/web-builder/kits/kit-a/a.b.c.d)`, `/wp-content/uploads/web-builder/kits/kit-a/a.b/wp-content/uploads/web-builder/kits/k/c.d"`,
	`/wp-content/uploads/web-builder/kits/kit-a/x.css`, `/wp-content/uploads/web-builder/kits/` + strings.Repeat("a", 64) + `/x.css"`,
	`/wp-content/uploads/web-builder/kits/` + strings.Repeat("a", 65) + `/x.css"`,
	`/wp-content/uploads/web-builder/kits/k/` + strings.Repeat("p", 511) + `.css"`, `/wp-content/uploads/web-builder/kits/k/` + strings.Repeat("p", 512) + `.css"`,
	`/wp-content/uploads/web-builder/kits/k/` + strings.Repeat("p", 513) + `.css"`, `/wp-content/uploads/web-builder/kits/k/` + strings.Repeat("p.", 300) + `css"`,
	`url(/wp-content/uploads/web-builder/kits/k/i.png),url(/wp-content/uploads/web-builder/kits/k/j.png)`,
}

func TestWBScannersCases(t *testing.T) {
	for _, src := range scanCases {
		checkScanners(t, src)
	}
}

// scanTokens are the pieces random documents are made of: the patterns' literals, their
// near-misses and the bytes that end them.
var scanTokens = []string{
	"url(", "URL(", "uRl(", "url", "(", "@import", "@IMPORT", "@ımport", "@", "import", " ", "\t", "\n", "\f", "\r", "\v",
	"'", "\"", "&#39;", "&quot;", "&QUOT;", "&", ";", ")", "https://", "HTTPS://", "httpſ://", "http://", "https:/", "https",
	"a", "b.c", "/x", "-", "_", "9", "é", "\xff", "<", ">", "<main", "<MAIN", "main", "Main", "mainly", "role", "ROLE", "=",
	"/wp-content/uploads/web-builder/kits/", "kit-a/", "k/", "A/", ".", ".css", ".js", ".abcdefghi", "?v=", "?v=12", "12",
	",", "/", "@", "+",
}

func randomScanDoc(r *rand.Rand) string {
	var b strings.Builder
	for n := r.IntN(40); n >= 0; n-- {
		b.WriteString(scanTokens[r.IntN(len(scanTokens))])
	}
	return b.String()
}

func TestWBScannersRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 200000; i++ {
		checkScanners(t, randomScanDoc(r))
	}
}

// TestWBScannersCorpus runs the scanners over the repository's HTML fixtures and, when
// PURSAE_SCAN_CORPUS names folders (a list, separated by the OS's path list separator), over every
// .html / .css / .js file under them (imported designs).
func TestWBScannersCorpus(t *testing.T) {
	dirs := []string{"../webbuilder/testdata", "testdata"}
	if v := config.Env("SCAN_CORPUS"); v != "" {
		dirs = append(dirs, filepath.SplitList(v)...)
	}
	n := 0
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".html", ".htm", ".css", ".js", ".json":
			default:
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil || len(b) > 8<<20 {
				return nil
			}
			checkScanners(t, string(b))
			n++
			return nil
		})
	}
	t.Logf("%d files", n)
}

func FuzzWBScanners(f *testing.F) {
	for _, s := range scanCases {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		checkScanners(t, src)
	})
}

// refKitVersioned is wbKitVersioned as it was (the regular expression).
func refKitVersioned(src string, vers map[string]int) string {
	if !strings.Contains(src, wbKitURLPrefix) {
		return src
	}
	return refKitRefRE.ReplaceAllStringFunc(src, func(m string) string {
		g := refKitRefRE.FindStringSubmatch(m)
		v, ok := vers[g[2]]
		if !ok {
			return m
		}
		return g[1] + "?v=" + strconv.Itoa(v) + g[4]
	})
}

func TestWBKitVersionedSame(t *testing.T) {
	vers := map[string]int{"kit-a": 3, "k": 12, "site": 7}
	docs := append([]string{}, scanCases...)
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 50000; i++ {
		docs = append(docs, randomScanDoc(r))
	}
	docs = append(docs, benchPage(t))
	for _, src := range docs {
		if got, want := wbKitVersionedWith(src, vers), refKitVersioned(src, vers); got != want {
			t.Fatalf("wbKitVersionedWith(%.300q)\n got %.300q\nwant %.300q", src, got, want)
		}
	}
}

// benchPage is a large imported page: PURSAE_BENCH_HTML (a file), else a synthetic one of about
// 160 KB with kit files, CSS url() references, many tags and no main landmark.
func benchPage(tb testing.TB) string {
	if p := config.Env("BENCH_HTML"); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			tb.Fatal(err)
		}
		return string(raw)
	}
	var sb strings.Builder
	sb.WriteString(`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter"><link href="/wp-content/uploads/web-builder/kits/site/css/style.css" rel="stylesheet">`)
	for sb.Len() < 160<<10 {
		sb.WriteString(`<section class="gallery-item" style="background-image:url(/wp-content/uploads/web-builder/kits/site/img/a.jpg)"><div class="row"><div class="col-md-4"><a href="/galeri/x" role="link"><img src="/wp-content/uploads/web-builder/kits/site/img/b.jpg" alt="Galeri (foto)"></a><p>Lorem ipsum (dolor) sit amet, consectetur @adipiscing elit; sed do eiusmod = tempor.</p></div></div></section>` + "\n")
	}
	sb.WriteString(`<script src="/wp-content/uploads/web-builder/kits/site/js/main.js"></script><iframe src="https://www.google.com/maps/embed?pb=1"></iframe>`)
	return sb.String()
}

func BenchmarkWBScanners(b *testing.B) {
	src := benchPage(b)
	vers := map[string]int{"site": 7}
	bench := func(name string, f func()) {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(src)))
			for b.Loop() {
				f()
			}
		})
	}
	bench("CSSURLs/regexp", func() { refCSSURLs(src) })
	bench("CSSURLs/scan", func() { scanCSSURLs(src) })
	bench("MainLandmark/regexp", func() { refMainRE.MatchString(src) })
	bench("MainLandmark/scan", func() { wbHasMainLandmark(src) })
	bench("KitVersioned/regexp", func() { refKitVersioned(src, vers) })
	bench("KitVersioned/scan", func() { wbKitVersionedWith(src, vers) })
}

// BenchmarkWBFillProgram: what wbFill did per response before it compiled once per doc revision
// (the stored scripts given the nonce placeholder, then the program cache keyed by the text's
// hash) and the keyed hit it does now.
func BenchmarkWBFillProgram(b *testing.B) {
	page := benchPage(b) + `<script>window.x=1</script>`
	c := webbuilder.NewProgramCache(0)
	b.Run("trust+hash", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := c.Compile(1, 1, cspTrustScripts(page)); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("keyed", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := c.CompileFor("fill:1:1:1:trusted", func() string { return cspTrustScripts(page) }); err != nil {
				b.Fatal(err)
			}
		}
	})
}
