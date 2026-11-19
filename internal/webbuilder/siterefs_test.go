package webbuilder

import (
	"sort"
	"strings"
	"testing"
)

// What a WordPress copy's pages reach: srcset variants, a lazy loader's data-*-srcset, a video
// source in data-*, the original domain's absolute URLs, a <noscript> image, CSS fonts and images,
// and the files a script names by a path it builds at run time (a suffix of the file's path).
func TestSiteReferences(t *testing.T) {
	files := map[string]int64{
		"wp-content/themes/t/style.css": 1, "wp-content/themes/t/fonts/icons.woff2": 1, "wp-content/themes/t/img/bg.webp": 1,
		"wp-content/themes/t/js/init.js": 1, "wp-content/themes/t/js/build/third-party/anime.min.js": 1,
		"wp-content/uploads/a.png": 1, "wp-content/uploads/a-300x169.png": 1, "wp-content/uploads/a-768x432.png": 1, "wp-content/uploads/b.png": 1,
		"wp-content/uploads/film.mp4": 1, "wp-content/uploads/og.jpg": 1, "wp-content/uploads/pixel.gif": 1,
		"wp-content/uploads/unused-1536x864.png": 1, "wp-includes/blocks/x/style.min.css": 1,
	}
	body := map[string]string{
		"wp-content/themes/t/style.css":  `@font-face{src:url(fonts/icons.woff2)} .x{background:url("img/bg.webp")}`,
		"wp-content/themes/t/js/init.js": `load(nectarOptions.theme + "/js/build/third-party/anime.min.js")`,
	}
	page := `<!doctype html><html><head><link rel="canonical" href="https://old.example/blog/">
<meta property="og:image" content="https://old.example/wp-content/uploads/og.jpg">
<link rel="stylesheet" href="../wp-content/themes/t/style.css?ver=1"><script src="../wp-content/themes/t/js/init.js"></script></head>
<body><img src="data:image/svg+xml,x" data-nectar-img-src="../wp-content/uploads/a.png" data-nectar-img-srcset="../wp-content/uploads/a.png 1489w, ../wp-content/uploads/a-300x169.png 300w">
<img src="../wp-content/uploads/b.png" srcset="../wp-content/uploads/a-768x432.png 768w">
<video><source data-nectar-video-src="../wp-content/uploads/film.mp4"></video>
<noscript><img src="../wp-content/uploads/pixel.gif"></noscript></body></html>`
	refs := SiteReferences([]SitePage{{Path: "blog/index.html", HTML: page}}, files, func(rel string) ([]byte, bool) {
		b, ok := body[rel]
		return []byte(b), ok
	})
	var unused []string
	for f := range files {
		if !refs.Used[f] {
			unused = append(unused, f)
		}
	}
	sort.Strings(unused)
	if strings.Join(unused, " ") != "wp-content/uploads/unused-1536x864.png wp-includes/blocks/x/style.min.css" {
		t.Errorf("unused: %v", unused)
	}
	if refs.From["wp-content/themes/t/fonts/icons.woff2"] != "wp-content/themes/t/style.css" || refs.From["wp-content/themes/t/js/build/third-party/anime.min.js"] != "wp-content/themes/t/js/init.js" {
		t.Errorf("from: %v", refs.From)
	}
}
