package webbuilder

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The design's original site (docs/developer/web-builder-code-sections.md, "Importing a site" › The
// original site). A saved copy of a live site (a WordPress scrape) still names the domain it was
// saved from: absolute links to its pages, its canonical URL, and — what matters most — the
// endpoints its scripts and forms talk to (https://example.com/wp-admin/admin-ajax.php in a
// plugin's inline config, a comment form's action). Imported as written, the new site's visitors
// would post forms to the old site and its tracking would call it.
//
//   - DetectOldHosts finds the domain(s): the hosts of the pages' canonical links and og:url, else
//     the absolute host most of the pages link to (<a href>).
//   - ConvertCode with OldHosts: every URL of those hosts is taken as a path of this site. Links,
//     sources and srcsets map like root-relative ones (an imported page → its URL, a kit file → the
//     kit, anything else → the same path here) unless KeepOldLinks; form actions and the URLs in
//     inline scripts and JSON (AJAX endpoints, REST roots) always point at this site (SiteURL), so
//     no form or request of an imported page reaches the old site. Each URL found is reported
//     (CodeImport.OldURLs) and so are forms whose action pointed there (bind them to Pursae forms).
//   - Third-party tracking (analytics, pixels) is kept and reported (CodeImport.Trackers).

// oldHostSkip are hosts a page names that are never the design's own site.
var oldHostSkip = map[string]bool{"www.w3.org": true, "w3.org": true, "schema.org": true, "ogp.me": true, "gmpg.org": true,
	"api.w.org": true, "s.w.org": true, "wordpress.org": true, "rankmath.com": true, "yoast.com": true, "fonts.googleapis.com": true,
	"fonts.gstatic.com": true, "www.googletagmanager.com": true, "www.google-analytics.com": true, "connect.facebook.net": true,
	"www.facebook.com": true, "facebook.com": true, "instagram.com": true, "www.instagram.com": true, "twitter.com": true, "x.com": true,
	"www.youtube.com": true, "youtube.com": true, "wa.me": true, "maps.google.com": true, "www.google.com": true, "cdn.jsdelivr.net": true,
	"cdnjs.cloudflare.com": true, "unpkg.com": true, "ajax.googleapis.com": true, "code.jquery.com": true}

// codeTrackers are the hosts and script markers of third-party tracking (name → markers).
var codeTrackers = []struct{ name, marker string }{
	{"Google Tag Manager", "googletagmanager.com"}, {"Google Analytics", "google-analytics.com"},
	{"Meta Pixel", "connect.facebook.net"}, {"Meta Pixel", "facebook.com/tr"}, {"Hotjar", "hotjar.com"},
	{"Microsoft Clarity", "clarity.ms"}, {"TikTok Pixel", "analytics.tiktok.com"}, {"LinkedIn Insight", "snap.licdn.com"},
	{"Microsoft Ads", "bat.bing.com"}, {"Yandex Metrica", "mc.yandex.ru"}, {"PixelYourSite", "pysOptions"},
}

var oldHrefRE = regexp.MustCompile(`(?i)<(?:a|link|meta)\b[^>]*>`)

// oldAbsURLRE: an absolute or protocol-relative URL in markup or script text (JSON-escaped too).
var oldAbsURLRE = regexp.MustCompile(`(?i)(?:https?:)?(?:\\?/){2}([a-z0-9][a-z0-9.-]*\.[a-z]{2,})(?::[0-9]+)?((?:\\?/[^"'\s<>()\\]*)*)`)

// oldWPPathRE: the path of a WordPress install's own endpoints and folders.
var oldWPPathRE = regexp.MustCompile(`(?i)^/(?:wp-admin/|wp-json|wp-content/|wp-includes/|xmlrpc\.php|wp-comments-post\.php|wp-login\.php)`)

// DetectOldHosts finds the domain(s) a saved site was saved from (see the file comment): a host
// that on at least half of the pages is the absolute host of the canonical link or og:url, of a
// WordPress endpoint or folder (/wp-admin/admin-ajax.php in a plugin's inline config, /wp-json/,
// /wp-content/…), or of a link to one of the site's own pages (not its home: a credit link to an
// agency's site on every page points at that site's root). Lowercase hosts, the www. and bare forms both.
func DetectOldHosts(pages []SitePage) []string {
	own := map[string]bool{} // the site's page paths ("/", "/hakkimizda/", "/a.html")
	for _, p := range pages {
		dir, base := path.Dir(p.Path), path.Base(p.Path)
		if dir == "." {
			dir = ""
		}
		if strings.EqualFold(strings.TrimSuffix(base, path.Ext(base)), "index") {
			own[strings.TrimSuffix("/"+dir, "/")+"/"] = true
		} else {
			own["/"+p.Path] = true
			own["/"+strings.TrimSuffix(p.Path, path.Ext(p.Path))+"/"] = true
		}
	}
	pagesWith := map[string]int{}
	for _, p := range pages {
		seen := map[string]bool{}
		mark := func(h string) {
			h = strings.ToLower(h)
			if h != "" && !oldHostSkip[h] && !seen[h] {
				seen[h] = true
				pagesWith[h]++
			}
		}
		for _, tag := range oldHrefRE.FindAllString(p.HTML, -1) {
			nodes, err := html.ParseFragment(strings.NewReader(tag), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
			if err != nil || len(nodes) == 0 || nodes[0].Type != html.ElementNode {
				continue
			}
			n := nodes[0]
			switch n.DataAtom {
			case atom.Link:
				if strings.Contains(strings.ToLower(attr(n, "rel")), "canonical") {
					mark(oldHostOf(attr(n, "href")))
				}
			case atom.Meta:
				if strings.EqualFold(attr(n, "property"), "og:url") {
					mark(oldHostOf(attr(n, "content")))
				}
			case atom.A:
				if h := oldHostOf(attr(n, "href")); h != "" {
					// A page other than the home page: an agency's credit link points at its own root.
					if u, err := url.Parse(strings.TrimSpace(attr(n, "href"))); err == nil && ActivePath(u.Path) != "/" && own[ActivePath(u.Path)] {
						mark(h)
					}
				}
			}
		}
		for _, m := range oldAbsURLRE.FindAllStringSubmatch(p.HTML, -1) {
			if oldWPPathRE.MatchString(strings.ReplaceAll(m[2], `\/`, "/")) {
				mark(m[1])
			}
		}
	}
	need := max(1, (len(pages)+1)/2)
	out := map[string]bool{}
	for h, n := range pagesWith {
		if n < need {
			continue
		}
		out[h] = true
		if strings.HasPrefix(h, "www.") {
			out[strings.TrimPrefix(h, "www.")] = true
		} else {
			out["www."+h] = true
		}
	}
	list := make([]string, 0, len(out))
	for h := range out {
		list = append(list, h)
	}
	sort.Strings(list)
	return list
}

// oldHostOf is the lowercase host of an absolute http(s) or protocol-relative URL ("" otherwise).
func oldHostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "//") && !strings.HasPrefix(strings.ToLower(raw), "http://") && !strings.HasPrefix(strings.ToLower(raw), "https://") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// oldLocal turns a URL of the old site into a root-relative path of this site ("/a/b/?x#y");
// ok is false for any other URL.
func (cv *codeConv) oldLocal(raw string) (string, bool) {
	if len(cv.o.OldHosts) == 0 {
		return raw, false
	}
	h := oldHostOf(raw)
	if h == "" || !cv.oldHost[h] {
		return raw, false
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw, false
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		p += "#" + u.EscapedFragment()
	}
	cv.oldURLs[strings.TrimSpace(raw)] = true
	return p, true
}

// oldScriptRE finds the old hosts' origins in script text: plain or JSON-escaped (https:\/\/host).
func oldScriptRE(hosts []string) *regexp.Regexp {
	if len(hosts) == 0 {
		return nil
	}
	alts := make([]string, len(hosts))
	for i, h := range hosts {
		alts[i] = regexp.QuoteMeta(h)
	}
	sort.Slice(alts, func(i, j int) bool { return len(alts[i]) > len(alts[j]) })
	return regexp.MustCompile(`(?i)(?:https?:)?(?:\\?/){2}(?:` + strings.Join(alts, "|") + `)(?::[0-9]+)?([^A-Za-z0-9.\-]|$)`)
}

// oldScript points the old site's origin in an inline script or JSON at this site (SiteURL; "" makes
// them root-relative), JSON escaping kept.
func (cv *codeConv) oldScript(n *html.Node) {
	if cv.oldRE == nil {
		return
	}
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type != html.TextNode {
			continue
		}
		k.Data = cv.oldRE.ReplaceAllStringFunc(k.Data, func(m string) string {
			g := cv.oldRE.FindStringSubmatch(m)
			origin := strings.TrimSuffix(m, g[1])
			cv.oldURLs[strings.ReplaceAll(origin, `\/`, "/")] = true
			cv.oldInScripts++
			site := strings.TrimSuffix(cv.o.SiteURL, "/")
			if strings.Contains(origin, `\/`) {
				site = strings.ReplaceAll(site, "/", `\/`)
			}
			return site + g[1]
		})
	}
}

// trackers lists the third-party tracking a page loads (its scripts' sources and inline code).
func (cv *codeConv) trackers(n *html.Node) {
	if n.DataAtom != atom.Script && n.DataAtom != atom.Noscript && n.DataAtom != atom.Img && n.DataAtom != atom.Iframe {
		return
	}
	text := attr(n, "src")
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		if k.Type == html.TextNode {
			text += k.Data
		}
	}
	for _, t := range codeTrackers {
		if strings.Contains(text, t.marker) {
			cv.tracking[t.name] = true
		}
	}
}
