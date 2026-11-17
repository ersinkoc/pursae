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

// SiteReferences finds the files of a static site that its pages use (docs/developer/
// web-builder-code-sections.md, "Importing from the admin"): a saved copy of a live site (a
// WordPress scrape) brings every size of every image, plugin folders and fonts the pages never
// name; only the files the pages reach are worth a design kit. Followed transitively:
//
//   - HTML: every URL attribute (src, href, poster, data-src, srcset candidates, the sources of
//     <video>/<audio>/<picture>), every data-* value that is a path, a srcset-like list or a JSON
//     list of paths, style attributes and <style> (url(), @import), the head's links and meta
//     images, and the string literals of inline scripts and JSON that name a file of the folder;
//   - CSS: url() (fonts, images, @font-face sources) and @import, relative to the stylesheet;
//   - JS and JSON: string literals naming a file of the folder — relative to the script, to the
//     site's root or, when that names none, any file whose path ends with the literal
//     ("js/build/third-party/anime.min.js" built from a theme URL at run time).
//
// URLs of the design's original domain (DetectOldHosts) count as root-relative paths. Files are
// keyed by their path in the folder; read gives a file's bytes (CSS, JS and JSON are read).
type SiteRefs struct {
	// Used: the files the pages reach (HTML pages aside).
	Used map[string]bool
	// From: for each used file, one file or page that names it (for reports).
	From map[string]string
}

// siteRefLiteralRE: a quoted string that looks like a file path (an extension, no spaces).
var siteRefLiteralRE = regexp.MustCompile(`["'` + "`" + `]((?:[A-Za-z0-9_@%~+.\-]+:)?(?://)?[^"'` + "`" + `\s<>(){}\[\]\\,;]{0,400}?\.[A-Za-z0-9]{1,6})(?:[?#][^"'` + "`" + `\s<>]*)?["'` + "`" + `]`)

// SiteReferences walks the site (see the type comment). files lists every file of the folder that
// is not a page (path → size).
func SiteReferences(pages []SitePage, files map[string]int64, read func(rel string) ([]byte, bool)) SiteRefs {
	r := &siteRefWalker{files: files, read: read, out: SiteRefs{Used: map[string]bool{}, From: map[string]string{}}, bySuffix: map[string][]string{}}
	for f := range files {
		r.bySuffix[path.Base(f)] = append(r.bySuffix[path.Base(f)], f)
	}
	for _, list := range r.bySuffix {
		sort.Strings(list)
	}
	r.old = map[string]bool{}
	for _, h := range DetectOldHosts(pages) {
		r.old[h] = true
	}
	for _, p := range pages {
		r.page(p)
	}
	for len(r.queue) > 0 {
		f := r.queue[0]
		r.queue = r.queue[1:]
		r.file(f)
	}
	return r.out
}

type siteRefWalker struct {
	files    map[string]int64
	read     func(string) ([]byte, bool)
	out      SiteRefs
	queue    []string
	bySuffix map[string][]string // base name → paths
	old      map[string]bool
}

// use marks a file used (and queues it to be read when it can name others).
func (r *siteRefWalker) use(rel, from string) bool {
	if _, ok := r.files[rel]; !ok {
		return false
	}
	if r.out.Used[rel] {
		return true
	}
	r.out.Used[rel] = true
	r.out.From[rel] = from
	switch strings.ToLower(path.Ext(rel)) {
	case ".css", ".js", ".mjs", ".json", ".svg", ".map":
		r.queue = append(r.queue, rel)
	}
	return true
}

// resolve gives the folder path a reference names from a file in dir ("" for none): a relative
// path against dir, a root-relative one against the root, a URL of the original domain by its
// path; query and fragment dropped, percent-escapes decoded.
func (r *siteRefWalker) resolve(ref, dir string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "data:") {
		return ""
	}
	if h := oldHostOf(ref); h != "" {
		if !r.old[h] {
			return ""
		}
		u, err := url.Parse(ref)
		if err != nil {
			return ""
		}
		ref = u.Path
	} else if strings.HasPrefix(ref, "//") || codeSchemeRE.MatchString(ref) {
		return ""
	}
	if i := strings.IndexAny(ref, "?#"); i >= 0 {
		ref = ref[:i]
	}
	if u, err := url.PathUnescape(ref); err == nil {
		ref = u
	}
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "/") {
		return strings.TrimPrefix(path.Clean(ref), "/")
	}
	return strings.TrimPrefix(path.Clean("/"+path.Join(dir, ref)), "/")
}

// ref follows one reference from a file in dir.
func (r *siteRefWalker) ref(ref, dir, from string) {
	if rel := r.resolve(ref, dir); rel != "" {
		r.use(rel, from)
	}
}

// literal follows a string literal of a script or JSON: the file it names relative to dir, to the
// root, or (when it names none) any file whose path ends with it.
func (r *siteRefWalker) literal(lit, dir, from string) {
	lit = strings.ReplaceAll(lit, `\/`, "/")
	if strings.ContainsAny(lit, " \t") || len(lit) > 400 {
		return
	}
	if rel := r.resolve(lit, dir); rel != "" && r.use(rel, from) {
		return
	}
	if rel := r.resolve("/"+strings.TrimLeft(lit, "./"), ""); rel != "" && r.use(rel, from) {
		return
	}
	clean := lit
	if i := strings.IndexAny(clean, "?#"); i >= 0 {
		clean = clean[:i]
	}
	if h := oldHostOf(clean); h != "" || strings.HasPrefix(clean, "//") || codeSchemeRE.MatchString(clean) {
		return
	}
	for strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "./") || strings.HasPrefix(clean, "/") {
		clean = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(clean, "../"), "./"), "/")
	}
	if clean == "" || !strings.Contains(clean, "/") && len(r.bySuffix[clean]) != 1 {
		return // a bare name matches only one file of that name
	}
	for _, f := range r.bySuffix[path.Base(clean)] {
		if f == clean || strings.HasSuffix(f, "/"+clean) {
			r.use(f, from)
		}
	}
}

func (r *siteRefWalker) literals(text, dir, from string) {
	for _, m := range siteRefLiteralRE.FindAllStringSubmatch(text, -1) {
		r.literal(m[1], dir, from)
	}
}

func (r *siteRefWalker) css(text, dir, from string) {
	for _, m := range codeCSSURLRE.FindAllStringSubmatch(text, -1) {
		r.ref(m[2], dir, from)
	}
	for _, m := range codeCSSImportRE.FindAllStringSubmatch(text, -1) {
		r.ref(m[2], dir, from)
	}
}

// file reads a used CSS, JS, JSON or SVG file for what it names.
func (r *siteRefWalker) file(rel string) {
	b, ok := r.read(rel)
	if !ok {
		return
	}
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	text := string(b)
	switch strings.ToLower(path.Ext(rel)) {
	case ".css":
		r.css(text, dir, rel)
	case ".svg":
		r.css(text, dir, rel)
		for _, m := range regexp.MustCompile(`(?:xlink:)?href="([^"#][^"]*)"`).FindAllStringSubmatch(text, -1) {
			r.ref(m[1], dir, rel)
		}
	default:
		r.literals(text, dir, rel)
		if strings.HasSuffix(rel, ".js") || strings.HasSuffix(rel, ".mjs") {
			// a source map comment, an ES module import
			for _, m := range regexp.MustCompile(`(?m)//[#@] sourceMappingURL=(\S+)`).FindAllStringSubmatch(text, -1) {
				r.ref(m[1], dir, rel)
			}
		}
	}
}

// page follows a page's references.
func (r *siteRefWalker) page(p SitePage) {
	doc, err := html.Parse(strings.NewReader(p.HTML))
	if err != nil {
		return
	}
	dir := path.Dir(p.Path)
	if dir == "." {
		dir = ""
	}
	from := p.Path
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				key := a.Key
				if a.Namespace != "" {
					key = a.Namespace + ":" + a.Key
				}
				switch {
				case codeURLAttrs[key] || key == "content" || key == "data" || key == "background" || key == "manifest":
					if key == "content" && !strings.Contains(a.Val, "/") && !strings.Contains(a.Val, ".") {
						continue
					}
					r.ref(a.Val, dir, from)
				case codeSrcsetAttrs[key] || strings.HasSuffix(key, "srcset"):
					for _, c := range strings.Split(a.Val, ",") {
						if f := strings.Fields(c); len(f) > 0 {
							r.ref(f[0], dir, from)
						}
					}
				case key == "style":
					r.css(a.Val, dir, from)
				case strings.HasPrefix(key, "data-"):
					v := strings.TrimSpace(a.Val)
					if v == "" {
						continue
					}
					if v[0] == '[' || v[0] == '{' {
						r.literals(v, dir, from)
						continue
					}
					if cands, ok := codeSrcsetList(v); ok {
						for _, c := range cands {
							r.ref(c, dir, from)
						}
						continue
					}
					r.ref(v, dir, from)
				}
			}
			switch n.DataAtom {
			case atom.Style:
				for k := n.FirstChild; k != nil; k = k.NextSibling {
					if k.Type == html.TextNode {
						r.css(k.Data, dir, from)
					}
				}
			case atom.Script, atom.Noscript:
				for k := n.FirstChild; k != nil; k = k.NextSibling {
					if k.Type == html.TextNode {
						r.literals(k.Data, dir, from)
						if n.DataAtom == atom.Noscript {
							r.noscript(k.Data, dir, from)
						}
					}
				}
			}
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	walk(doc)
}

// noscript follows the markup of a <noscript> (raw text to the parser): its images and links.
func (r *siteRefWalker) noscript(text, dir, from string) {
	nodes, err := html.ParseFragment(strings.NewReader(text), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if codeURLAttrs[a.Key] {
					r.ref(a.Val, dir, from)
				} else if codeSrcsetAttrs[a.Key] {
					for _, c := range strings.Split(a.Val, ",") {
						if f := strings.Fields(c); len(f) > 0 {
							r.ref(f[0], dir, from)
						}
					}
				}
			}
		}
		for k := n.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
}
