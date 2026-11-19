package webbuilder

import (
	"strings"
	"testing"
)

// A saved copy of a live WordPress site: its canonical URL, absolute links to its own pages and
// files, a comment form and an AJAX endpoint of the original domain, third-party tracking.
const oldSitePage = `<!doctype html><html><head>
<link rel="canonical" href="https://old.example/blog/">
<meta property="og:url" content="https://old.example/blog/">
<script src="https://www.googletagmanager.com/gtag/js?id=G-1"></script>
<script>var cfg = {"ajax_url":"https:\/\/old.example\/wp-admin\/admin-ajax.php","home":"https://old.example","other":"https://old.example.tr/x","cdn":"https://cdn.example/a.js"};</script>
<script type="application/ld+json">{"@id":"https://old.example/#org","url":"https://www.old.example/"}</script>
</head><body><main><section>
<a href="https://old.example/about/">About</a> <a href="https://old.example/">Home</a> <a href="https://instagram.com/x">IG</a>
<img src="https://www.old.example/wp-content/uploads/a.png" srcset="https://old.example/wp-content/uploads/a.png 1x, https://old.example/wp-content/uploads/a-2x.png 2x" alt="">
<div data-bg="https://old.example/wp-content/uploads/a.png"></div>
<form action="https://old.example/wp-comments-post.php" method="post"><input name="comment"></form>
<form action="/search/"><input name="s"></form>
</section></main></body></html>`

func TestDetectOldHosts(t *testing.T) {
	got := DetectOldHosts([]SitePage{{Path: "blog/index.html", HTML: oldSitePage}})
	if strings.Join(got, ",") != "old.example,www.old.example" {
		t.Fatalf("hosts: %v", got)
	}
	// A wget copy (relative canonical): the host of the WordPress endpoints in the pages' scripts,
	// not the agency's credit link every page has.
	wp := `<html><head><link rel="canonical" href="index.html"><link rel="https://api.w.org/" href="https://shop.example/wp-json/">
<script>var x = {"ajaxurl":"https:\/\/shop.example\/wp-admin\/admin-ajax.php"};</script></head>
<body><a href="https://agency.example">Made by Agency</a><a href="https://instagram.com/x">i</a></body></html>`
	got = DetectOldHosts([]SitePage{{Path: "index.html", HTML: wp}, {Path: "a/index.html", HTML: wp}, {Path: "b/index.html", HTML: "<html><body></body></html>"}})
	if strings.Join(got, ",") != "shop.example,www.shop.example" {
		t.Fatalf("hosts from endpoints: %v", got)
	}
	// Links to the site's own pages by their absolute URL.
	page := `<html><body><a href="https://shop.example/a/">a</a><a href="https://agency.example/">by</a></body></html>`
	got = DetectOldHosts([]SitePage{{Path: "index.html", HTML: page}, {Path: "a/index.html", HTML: page}})
	if strings.Join(got, ",") != "shop.example,www.shop.example" {
		t.Fatalf("hosts from links: %v", got)
	}
	if got := DetectOldHosts([]SitePage{{Path: "a.html", HTML: `<a href="https://instagram.com/x">i</a><a href="https://agency.example/">by</a>`}}); len(got) != 0 {
		t.Fatalf("a social or credit link is not the design's site: %v", got)
	}
}

func TestConvertCodeOldSite(t *testing.T) {
	o := CodeImportOptions{KitURL: "/k/", Base: "blog", KitFiles: map[string]bool{"wp-content/uploads/a.png": true, "wp-content/uploads/a-2x.png": true},
		LinkMap: map[string]string{"about/index.html": "/hakkimizda/", "about": "/hakkimizda/", "": "/"}, SiteURL: "http://new.test",
		OldHosts: []string{"old.example", "www.old.example"}}
	res, err := ConvertCode(oldSitePage, o)
	if err != nil {
		t.Fatal(err)
	}
	body := codeHTML(res)
	for _, want := range []string{`href="/hakkimizda/"`, `href="/" data-wbc="3">Home`, `href="https://instagram.com/x"`, `src="/k/wp-content/uploads/a.png"`,
		`srcset="/k/wp-content/uploads/a.png 1x, /k/wp-content/uploads/a-2x.png 2x"`, `data-bg="/k/wp-content/uploads/a.png"`,
		`action="/wp-comments-post.php"`, `action="/search/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body: missing %s in %s", want, body)
		}
	}
	for _, want := range []string{`"ajax_url":"http:\/\/new.test\/wp-admin\/admin-ajax.php"`, `"home":"http://new.test"`, `"other":"https://old.example.tr/x"`,
		`"cdn":"https://cdn.example/a.js"`, `"@id":"http://new.test/#org","url":"http://new.test/"`, `https://www.googletagmanager.com/gtag/js?id=G-1`} {
		if !strings.Contains(res.Head, want) {
			t.Errorf("head: missing %s in %s", want, res.Head)
		}
	}
	if strings.Contains(body+res.Head, "old.example/") {
		t.Errorf("a URL of the old site is left: %s %s", res.Head, body)
	}
	if res.OldForms != 1 || res.OldInScripts != 4 || strings.Join(res.Trackers, ",") != "Google Tag Manager" || len(res.OldURLs) < 5 {
		t.Errorf("report: forms %d, scripts %d, trackers %v, urls %v", res.OldForms, res.OldInScripts, res.Trackers, res.OldURLs)
	}
	// Keeping the links: links and files stay on the old site, but no form or script goes there.
	o.KeepOldLinks = true
	res, err = ConvertCode(oldSitePage, o)
	if err != nil {
		t.Fatal(err)
	}
	body = codeHTML(res)
	if !strings.Contains(body, `href="https://old.example/about/"`) || !strings.Contains(body, `action="/wp-comments-post.php"`) ||
		!strings.Contains(res.Head, `http:\/\/new.test\/wp-admin\/admin-ajax.php`) {
		t.Errorf("keep links: %s %s", body, res.Head)
	}
}
