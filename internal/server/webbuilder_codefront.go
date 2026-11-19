package server

import (
	"container/list"
	"context"
	"encoding/binary"
	"hash/maphash"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// The front end of imported designs (docs/developer/web-builder-code-sections.md): the parts of a
// request made of code sections share their shell (the design's wrapper around its header, main and
// footer, printed once), the header marks the current page's link (its ActiveRule), and the design's
// <html> lang and attributes and <body> attributes are printed.

// wbLangRE is a language tag a design may give its <html> (tr, en-US, zh-Hant-TW).
var wbLangRE = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8}){0,3}$`)

var (
	wbCodeInfoCache sync.Map // "post:rev:updated" → *wbCodeInfoEntry
	wbCodeInfoN     atomic.Int64
)

type wbCodeInfoEntry struct{ info *webbuilder.CodeDocInfo }

// wbContentTags is wp_filter_content_tags over a filled snapshot, except a page of code sections:
// the design's images are printed as written (no loading="lazy", decoding or added dimensions, which
// change what the design loads and when).
func (s *Server) wbContentTags(ctx context.Context, src string) string {
	if webbuilder.IsCodePage(src) {
		return src
	}
	return s.filterContentTags(ctx, src)
}

// wbCodeInfo is what a published doc's code sections say about the document (nil without any),
// read once per doc revision.
func (s *Server) wbCodeInfo(d *store.WebBuilderDoc) *webbuilder.CodeDocInfo {
	if d == nil || !webbuilder.HasCode(d.Doc) {
		return nil
	}
	key := strconv.FormatInt(d.PostID, 10) + ":" + strconv.Itoa(d.Rev) + ":" + strconv.FormatInt(d.UpdatedAt.UnixNano(), 36)
	if v, ok := wbCodeInfoCache.Load(key); ok {
		return v.(*wbCodeInfoEntry).info
	}
	info := webbuilder.CodeInfo(d.Doc)
	if wbCodeInfoN.Add(1) > 512 {
		wbCodeInfoCache.Clear()
		wbCodeInfoN.Store(0)
	}
	wbCodeInfoCache.Store(key, &wbCodeInfoEntry{info})
	return info
}

// wbCodeParts finishes the code parts of a Hello Pursae request: the header's and footer's active
// links for the requested path, the shared shell, and the document attributes (lang, <html> and
// <body> attributes) of the parts, main first.
//
// The work is a pure function of the parts' markup, their code infos, the path and the kits'
// versions (wbCodePartsOf); its result is kept for the next response with the same inputs
// (wbCodePartsMemo), which are compared in full, never by a hash alone.
func (s *Server) wbCodeParts(c *gin.Context, tp *wbThemeParts, header, main, footer *wbPart) {
	info := func(p *wbPart) *webbuilder.CodeDocInfo {
		if p == nil || !p.codePage {
			return nil
		}
		return s.wbCodeInfo(p.doc)
	}
	in := wbCodePartsIn{
		header: string(tp.view.Header), main: string(tp.view.Main), footer: string(tp.view.Footer),
		current: c.Request.URL.Path, hi: info(header), mi: info(main), fi: info(footer), kits: s.wbKitVersionRef(),
	}
	out := wbCodePartsMemo.get(in)
	if out == nil {
		out = wbCodePartsOf(in)
		wbCodePartsMemo.put(in, out)
	}
	tp.view.Header, tp.view.Main, tp.view.Footer = template.HTML(out.header), template.HTML(out.main), template.HTML(out.footer)
	if out.lang != "" {
		tp.view.Lang = out.lang
	}
	if out.htmlAttrs != "" {
		tp.view.HTMLAttrs = template.HTMLAttr(out.htmlAttrs)
	}
	if out.bodyAttrs != "" {
		tp.view.BodyAttrs = template.HTMLAttr(out.bodyAttrs)
	}
	tp.bodyClass = out.bodyClass
}

// wbCodePartsIn is what wbCodeParts works from: the filled header, main and footer, the requested
// path, the parts' code infos (nil for a part that is not code; one stored doc revision's info,
// never changed once read, wbCodeInfo) and the kits' versions (wbKitVersionRef).
type wbCodePartsIn struct {
	header, main, footer, current string
	hi, mi, fi                    *webbuilder.CodeDocInfo
	kits                          *map[string]int
}

// wbCodePartsOut is what wbCodeParts gives the view.
type wbCodePartsOut struct {
	header, main, footer string
	lang                 string
	htmlAttrs, bodyAttrs string // with their leading space, "" for none
	bodyClass            string
}

// wbCodePartsOf is wbCodeParts' work, a function of its inputs only.
func wbCodePartsOf(in wbCodePartsIn) *wbCodePartsOut {
	out := &wbCodePartsOut{header: in.header, main: in.main, footer: in.footer}
	hi, mi, fi := in.hi, in.mi, in.fi
	versioned := func(w string) string {
		if !strings.Contains(w, wbKitURLPrefix) {
			return w
		}
		return wbKitVersionedWith(w, *in.kits)
	}
	if hi != nil && hi.Active != nil {
		out.header = webbuilder.ApplyActive(out.header, hi.Active, in.current)
	}
	if fi != nil && fi.Active != nil {
		out.footer = webbuilder.ApplyActive(out.footer, fi.Active, in.current)
	}
	// A single template made from a group of the design's pages marks the current page in its
	// side menu the same way (webbuilder.ConvertGroups).
	if mi != nil && mi.Active != nil && out.main != "" {
		out.main = webbuilder.ApplyActive(out.main, mi.Active, in.current)
	}
	// One shell around header, main and footer: a code main must sit in the same wrappers; any
	// other main (a theme view, builder sections, a forum view) is printed inside them. A page
	// without a footer (or header) shares it with the header (or footer) alone (ShellPlan).
	mainCode := mi != nil && out.main != ""
	var hc, fc []string
	if hi != nil && out.header != "" {
		hc = hi.Last
	}
	if fi != nil && out.footer != "" {
		fc = fi.First
	}
	mInfo := mi
	if !mainCode {
		mInfo = nil
	}
	k, withH, withF := webbuilder.ShellPlan(hc, fc, mInfo)
	kh, kf := webbuilder.ShellDepths(hc, fc, mInfo)
	if k > 0 && withH && withF && mainCode && (kh > k || kf > k) {
		// The page shares more wrappers with the footer (or the header) than with both.
		from := fc
		if kh > k {
			from = hc
		}
		chain := make([]string, max(kh, kf))
		for i, w := range from[:len(chain)] {
			chain[i] = versioned(w)
		}
		if h, m, f, ok := webbuilder.ShareShellDeep(out.header, out.main, out.footer, chain, k, kh, kf); ok {
			out.header, out.main, out.footer = h, m, f
			k = 0 // shared
		}
	}
	if k > 0 {
		from := hc
		if !withH {
			from = fc
		}
		chain := make([]string, k)
		for i, w := range from[:k] {
			chain[i] = versioned(w)
		}
		hv, fv := out.header, out.footer
		if !withH {
			hv = ""
		}
		if !withF {
			fv = ""
		}
		if h, m, f, ok := webbuilder.ShareShell(hv, out.main, fv, chain, mainCode); ok {
			if withH {
				out.header = h
			}
			if withF {
				out.footer = f
			}
			out.main = m
		}
	}
	var langs, htmlAttrs, bodyAttrs []string
	for _, x := range []*webbuilder.CodeDocInfo{mi, hi, fi} {
		if x == nil {
			continue
		}
		langs = append(langs, x.Lang)
		htmlAttrs = append(htmlAttrs, x.HTMLAttrs)
		bodyAttrs = append(bodyAttrs, x.BodyAttrs)
	}
	for _, l := range langs {
		if wbLangRE.MatchString(l) {
			out.lang = l
			break
		}
	}
	a, cls := webbuilder.MergeDocAttrs([]string{"lang"}, htmlAttrs...)
	if cls != "" {
		a = strings.TrimSpace(`class="` + template.HTMLEscapeString(cls) + `" ` + a)
	}
	if a != "" {
		out.htmlAttrs = " " + a
	}
	a, cls = webbuilder.MergeDocAttrs([]string{"lang"}, bodyAttrs...)
	if a != "" {
		out.bodyAttrs = " " + a
	}
	out.bodyClass = cls
	return out
}

// wbCodePartsMemo keeps the last results of wbCodePartsOf: an LRU of at most wbCodePartsMemoMax
// results and wbCodePartsMemoBytes of their markup. Entries are found by a hash of the inputs and
// used only when every input is equal (the strings compared in full, the infos and the kits'
// version map by identity), so a result is only ever given for the inputs it was made from.
var wbCodePartsMemo = newWBCodePartsCache(wbCodePartsMemoMax, wbCodePartsMemoBytes)

const (
	wbCodePartsMemoMax   = 256
	wbCodePartsMemoBytes = 24 << 20
)

type wbCodePartsCache struct {
	mu       sync.Mutex
	seed     maphash.Seed
	max      int
	maxBytes int64
	bytes    int64
	ll       *list.List // front: most recently used; values *wbCodePartsEntry
	m        map[uint64]*list.Element
}

type wbCodePartsEntry struct {
	key  uint64
	in   wbCodePartsIn
	out  *wbCodePartsOut
	size int64
}

func newWBCodePartsCache(max int, maxBytes int64) *wbCodePartsCache {
	return &wbCodePartsCache{seed: maphash.MakeSeed(), max: max, maxBytes: maxBytes, ll: list.New(), m: map[uint64]*list.Element{}}
}

func (m *wbCodePartsCache) hash(in wbCodePartsIn) uint64 {
	var h maphash.Hash
	h.SetSeed(m.seed)
	var n [8]byte
	for _, s := range []string{in.header, in.main, in.footer, in.current} {
		binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.WriteString(s)
	}
	return h.Sum64()
}

// get is the result for in, nil when none is kept.
func (m *wbCodePartsCache) get(in wbCodePartsIn) *wbCodePartsOut {
	key := m.hash(in)
	m.mu.Lock()
	defer m.mu.Unlock()
	el, ok := m.m[key]
	if !ok {
		return nil
	}
	e := el.Value.(*wbCodePartsEntry)
	if e.in != in { // every field: the strings by content, the pointers by identity
		return nil
	}
	m.ll.MoveToFront(el)
	return e.out
}

// put keeps out as the result for in (replacing whatever had the same hash).
func (m *wbCodePartsCache) put(in wbCodePartsIn, out *wbCodePartsOut) {
	size := int64(len(in.header) + len(in.main) + len(in.footer) + len(in.current) +
		len(out.header) + len(out.main) + len(out.footer) + len(out.htmlAttrs) + len(out.bodyAttrs) + len(out.bodyClass) + 256)
	if size > m.maxBytes {
		return
	}
	key := m.hash(in)
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.m[key]; ok {
		m.remove(el)
	}
	m.m[key] = m.ll.PushFront(&wbCodePartsEntry{key: key, in: in, out: out, size: size})
	m.bytes += size
	for m.ll.Len() > m.max || m.bytes > m.maxBytes {
		m.remove(m.ll.Back())
	}
}

func (m *wbCodePartsCache) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ll.Len()
}

// purge drops every result.
func (m *wbCodePartsCache) purge() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ll.Init()
	m.m = map[uint64]*list.Element{}
	m.bytes = 0
}

func (m *wbCodePartsCache) remove(el *list.Element) {
	e := el.Value.(*wbCodePartsEntry)
	m.ll.Remove(el)
	delete(m.m, e.key)
	m.bytes -= e.size
}

// wbCodeDocAttrs is the document attributes of a canvas page of code sections: its lang and the
// <html> and <body> attribute strings (body class apart).
func (s *Server) wbCodeDocAttrs(d *store.WebBuilderDoc) (lang, htmlAttrs, bodyAttrs, bodyClass string) {
	info := s.wbCodeInfo(d)
	if info == nil {
		return "", "", "", ""
	}
	if wbLangRE.MatchString(info.Lang) {
		lang = info.Lang
	}
	a, cls := webbuilder.MergeDocAttrs([]string{"lang"}, info.HTMLAttrs)
	if cls != "" {
		a = strings.TrimSpace(`class="` + template.HTMLEscapeString(cls) + `" ` + a)
	}
	htmlAttrs = a
	bodyAttrs, bodyClass = webbuilder.MergeDocAttrs([]string{"lang"}, info.BodyAttrs)
	return
}
