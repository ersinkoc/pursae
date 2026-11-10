package server

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Pursae Multi Language: visual front-end translation (TranslatePress style).
//
//   - Entry: the front toolbar's "Translate Page" (one item per language), "Translate visually"
//     on Languages > Translations and a row action in posts lists. All open
//     /wp-admin/admin?page=mlang_visual&url=<same-origin path>&lang=<slug>: a full-screen shell
//     (panel on the left, the page in a same-origin iframe on the right, like the Customizer).
//   - Session: the shell creates a token (pllVisualSessions, 12 hours, bound to the user) and loads
//     the page with ?pll_visual=<token>. Only that user, logged in with manage_options, gets the
//     visual page: no toolbar, never cached, every translatable string wrapped in <pll-t> or marked
//     with data-pll-a-<attr>, post content regions as <pll-uc data-pll-post="N">, images carrying
//     data-pll-img="<media id>", a JSON data island and pll-visual.js/css. Every other request
//     (anonymous, no token, a foreign or expired token) is the normal page: no marker, no script.
//   - Writes: POST pll/v1/visual/strings (manage_options + wp_rest nonce) stores string
//     translations; image alt texts go through pll/v1/media/{id}/i18n (golylang_media.go); post
//     content opens the post's translation in Pursae Editor or the Web Builder in a new tab.

const (
	pllVisualParam   = "pll_visual"
	pllVisualTTL     = 12 * time.Hour
	pllVisualMaxSave = 200   // strings per save
	pllVisualMaxTr   = 10000 // characters of one translation
	pllVisualMaxKey  = 2400  // bytes of one original (pll_string_sources key limit)
	pllVisualMaxList = 3000  // strings listed for one page
	pllVisualMaxImgs = 300
	pllVisualMaxPost = 60
)

// pllVisualSession is one visual translation session (the shell's iframe token).
type pllVisualSession struct {
	UserID  int64
	Lang    string // the language the shell was opened for
	Expires time.Time
}

var pllVisualSessions = struct {
	sync.Mutex
	m map[string]*pllVisualSession
}{m: map[string]*pllVisualSession{}}

const pllVisualGinKey = "golylang.visual"

// pllVisualFrom returns the visual session of this front request, or nil. Cheap without the
// parameter. A valid session marks the response as never cached.
func (s *Server) pllVisualFrom(c *gin.Context) *pllVisualSession {
	if c == nil || c.Request == nil {
		return nil
	}
	if v, ok := c.Get(pllVisualGinKey); ok {
		vs, _ := v.(*pllVisualSession)
		return vs
	}
	tok := c.Query(pllVisualParam)
	if tok == "" {
		return nil
	}
	var vs *pllVisualSession
	if s.golylangOn() {
		if u := currentUser(c); pllCanTranslateAny(u) {
			pllVisualSessions.Lock()
			cur := pllVisualSessions.m[tok]
			pllVisualSessions.Unlock()
			if cur != nil && cur.UserID == u.ID && time.Now().Before(cur.Expires) {
				vs = cur
			}
		}
	}
	c.Set(pllVisualGinKey, vs)
	if vs != nil {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Header("X-WPGo-Cache-Control", "no-store")
	}
	return vs
}

// pllVisualOn reports whether this front request is a visual translation session (the toolbar
// is hidden there: site_view.go).
func (s *Server) pllVisualOn(c *gin.Context) bool { return s.pllVisualFrom(c) != nil }

func pllVisualNewSession(uid int64, lang string) string {
	tok := newChangesetID()
	now := time.Now()
	pllVisualSessions.Lock()
	for k, vs := range pllVisualSessions.m {
		if now.After(vs.Expires) {
			delete(pllVisualSessions.m, k)
		}
	}
	pllVisualSessions.m[tok] = &pllVisualSession{UserID: uid, Lang: lang, Expires: now.Add(pllVisualTTL)}
	pllVisualSessions.Unlock()
	return tok
}

// pllVisualPath keeps a same-origin site path of the url parameter (open-redirect safe): a path
// starting with one "/", no scheme or host, no control character, not an admin, login or REST
// path; anything else is "/". The visual parameter and the fragment are dropped.
func pllVisualPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, "/\\") ||
		strings.Contains(raw, "\\") || hasControlChar(raw) {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	low := strings.ToLower(u.Path)
	for _, p := range []string{"/wp-admin", "/wp-login", "/wp-json", "/xmlrpc", "/wp-content", "/wp-includes"} {
		if strings.HasPrefix(low, p) {
			return "/"
		}
	}
	q := u.Query()
	q.Del(pllVisualParam)
	q.Del(czParam)
	out := u.EscapedPath()
	if e := q.Encode(); e != "" {
		out += "?" + e
	}
	return out
}

// pllVisualURL is the admin URL of the visual editor for a site path and a language.
func pllVisualURL(path, lang string) string {
	out := "/wp-admin/admin?page=mlang_visual&url=" + url.QueryEscape(path)
	if lang != "" {
		out += "&lang=" + url.QueryEscape(lang)
	}
	return out
}

// pllVisualUnsupported is the notice shown when the URL mode puts languages on other hosts
// (subdomains or one domain per language): the shell, the iframe and the page script talk through
// same-origin postMessage, so the editor works only when every language is on the admin's host.
const pllVisualUnsupported = "Visual translation is not available while the language is set from subdomains or different domains. Choose the directory or content URL mode in Languages > Settings > URL modifications, or translate strings on Languages > Translations."

// pllVisualSupported reports whether the URL mode keeps all languages on one host.
func (s *Server) pllVisualSupported() bool { return s.pllOpts().ForceLang < 2 }

// pllVisualTargets are the languages one translates into (active, not the default). None when the
// URL mode is not supported by the visual editor, so the entry points are hidden.
func (s *Server) pllVisualTargets(ctx context.Context) []*pllLanguage {
	var out []*pllLanguage
	if !s.pllVisualSupported() {
		return nil
	}
	langs := s.pllActiveLanguages(ctx)
	if len(langs) < 2 {
		return nil
	}
	for _, l := range langs {
		if !l.IsDefault {
			out = append(out, l)
		}
	}
	return out
}

// Shell --------------------------------------------------------------------------------------

type pllVisualShell struct {
	Title, SiteName  string
	Langs            []*pllLanguage
	Lang             *pllLanguage
	Path, Frame      string
	Close            string
	Token, Param     string
	RESTNonce        string
	MT, Media        bool
	Version          string
	Error            string
	CreateNonce      string
	DefaultLangName  string
	ContentLangsJSON template.JS
}

// pllVisualScreen serves admin?page=mlang_visual (manage_options was checked by golylangAdmin).
func (s *Server) pllVisualScreen(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	d := &pllVisualShell{Title: "Translate visually", SiteName: s.settings.String("blogname"), Param: pllVisualParam, Version: Version}
	d.Path = pllVisualPath(c.Query("url"))
	d.Close = d.Path
	d.Langs = pllTranslatableLangs(u, s.pllVisualTargets(ctx))
	if def := s.pllDefault(ctx); def != nil {
		d.DefaultLangName = def.Name
	}
	c.Header("Cache-Control", "no-store")
	if !s.pllVisualSupported() {
		d.Error = pllVisualUnsupported
		s.renderAdminTemplate(c, http.StatusOK, "admin/golylang-visual", d)
		return
	}
	if len(d.Langs) == 0 {
		d.Error = "Add at least two languages on Languages before translating pages visually."
		s.renderAdminTemplate(c, http.StatusOK, "admin/golylang-visual", d)
		return
	}
	for _, l := range d.Langs {
		if l.Slug == c.Query("lang") {
			d.Lang = l
		}
	}
	if d.Lang == nil {
		d.Lang = d.Langs[0]
	}
	d.Token = pllVisualNewSession(u.ID, d.Lang.Slug)
	sep := "?"
	if strings.Contains(d.Path, "?") {
		sep = "&"
	}
	d.Frame = d.Path + sep + pllVisualParam + "=" + url.QueryEscape(d.Token)
	d.RESTNonce = s.nonce(c, "wp_rest")
	d.MT = s.pllMTEnabled()
	d.Media = s.pllMediaOn()
	s.renderAdminTemplate(c, http.StatusOK, "admin/golylang-visual", d)
}

// Entry points ------------------------------------------------------------------------------

// pllVisualAdminBarNode is the front toolbar's "Translate Page" with one item per language.
func (s *Server) pllVisualAdminBarNode(c *gin.Context, u *store.User) string {
	if c == nil || u == nil || !s.golylangOn() || !pllCanTranslateAny(u) {
		return ""
	}
	langs := pllTranslatableLangs(u, s.pllVisualTargets(c))
	if len(langs) == 0 {
		return ""
	}
	path := pllVisualPath(c.Request.URL.RequestURI())
	cur := ""
	if l := s.pllCurrent(c); l != nil && !l.IsDefault {
		cur = l.Slug
	}
	first := langs[0].Slug
	if cur != "" {
		first = cur
	}
	esc := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, `<li role='group' id='wp-admin-bar-translate-page' class="menupop"><a class='ab-item' role="menuitem" aria-expanded="false" href='%s'><span class="ab-icon dashicons-translation" aria-hidden="true"></span><span class="ab-label">Translate Page</span></a><div class="ab-sub-wrapper"><ul role='menu' id='wp-admin-bar-translate-page-default' class="ab-submenu">`,
		esc(pllVisualURL(path, first)))
	for _, l := range langs {
		fmt.Fprintf(&b, `<li role='group' id='wp-admin-bar-translate-page-%s'><a class='ab-item' role="menuitem" lang="%s" href='%s'>%s %s</a></li>`,
			esc(l.Slug), esc(l.W3C()), esc(pllVisualURL(path, l.Slug)), l.FlagHTML(), esc(l.Name))
	}
	b.WriteString(`</ul></div></li>`)
	return b.String()
}

// pllVisualRowAction is the posts list's "Translate visually" of a published post (view URL).
func (s *Server) pllVisualRowAction(c *gin.Context, viewURL, own string) string {
	if viewURL == "" || !pllCanTranslateAny(currentUser(c)) {
		return ""
	}
	langs := pllTranslatableLangs(currentUser(c), s.pllVisualTargets(c))
	if len(langs) == 0 {
		return ""
	}
	u, err := url.Parse(viewURL)
	if err != nil {
		return ""
	}
	lang := langs[0].Slug
	for _, l := range langs {
		if l.Slug == own {
			lang = own
		}
	}
	return pllVisualURL(pllVisualPath(u.RequestURI()), lang)
}

// Visual page ---------------------------------------------------------------------------------

func init() {
	pllVisualPage = func(s *Server, c *gin.Context, page []byte, l *pllLanguage) ([]byte, bool) {
		if s.pllVisualFrom(c) == nil {
			return nil, false
		}
		return s.pllVisualRender(c, page, l), true
	}
}

// pllVisualItem is one translatable string of a visual page.
type pllVisualItem struct {
	M           string   `json:"m"`           // marker
	Key         string   `json:"key"`         // pll_strings key (context\x04original for _x strings)
	Original    string   `json:"original"`    // the text in the default language
	Context     string   `json:"context"`     // gettext context
	Translation string   `json:"translation"` // stored translation ("" none)
	Shown       string   `json:"shown"`       // what the page shows
	Attrs       []string `json:"attrs"`       // attributes it appears in ("" = text)
	Count       int      `json:"count"`
}

type pllVisualPost struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Type       string `json:"type"`
	Lang       string `json:"lang"`
	LangName   string `json:"lang_name"`
	EditURL    string `json:"edit_url,omitempty"`
	BuilderURL string `json:"builder_url,omitempty"`
	Target     string `json:"target"`
	TargetName string `json:"target_name"`
	TrID       int64  `json:"tr_id,omitempty"`
	TrEditURL  string `json:"tr_edit_url,omitempty"`
	TrBuilder  string `json:"tr_builder_url,omitempty"`
	CreateURL  string `json:"create_url,omitempty"`
}

type pllVisualImage struct {
	ID  int64  `json:"id"`
	Alt string `json:"alt"`
}

var (
	pllVisualRegionRe = regexp.MustCompile(`<pll-uc data-pll-post="(\d+)"`)
	pllVisualAltRe    = regexp.MustCompile(`(?is)\salt\s*=\s*(?:"([^"]*)"|'([^']*)')`)
)

func pllVisualMarker(key string) string {
	h := sha1.Sum([]byte(key))
	return "s" + hex.EncodeToString(h[:6])
}

// pllKnownKey is the string key a text rendered this request was translated from (pllMarkKnown).
func pllKnownKey(r *pllReq, text string) (string, bool) {
	if r == nil {
		return "", false
	}
	v, ok := r.cache.Load("known:" + text)
	if !ok {
		return "", false
	}
	k, _ := v.(string)
	return k, k != ""
}

// pllKnownPlural reports whether text is a plural form a gettext call printed this request
// (pllMarkKnown with an empty key).
func pllKnownPlural(r *pllReq, text string) bool {
	if r == nil {
		return false
	}
	v, ok := r.cache.Load("known:" + text)
	k, _ := v.(string)
	return ok && k == ""
}

// pllVisualRender is the page of a visual session: strings marked, regions and images tagged,
// data island and editor script added.
func (s *Server) pllVisualRender(c *gin.Context, page []byte, l *pllLanguage) []byte {
	ctx := c.Request.Context()
	r := pllFrom(c)
	vs := s.pllVisualFrom(c)
	tr := s.pllStringMap(ctx, l.Slug)
	table := s.pllFrontTableFor(ctx, l.Slug)
	items := map[string]*pllVisualItem{}
	var order []string
	visit := func(str, attr string) string {
		key := str
		if k, ok := pllKnownKey(r, str); ok {
			key = k
		} else if pllKnownPlural(r, str) {
			return "" // a plural form (_n): no single key to edit
		}
		if len(key) > pllVisualMaxKey {
			return ""
		}
		m := pllVisualMarker(key)
		it := items[m]
		if it == nil {
			if len(order) >= pllVisualMaxList {
				return ""
			}
			it = &pllVisualItem{M: m, Key: key, Original: key, Shown: str}
			if i := strings.IndexByte(key, '\x04'); i >= 0 {
				it.Context, it.Original = key[:i], key[i+1:]
			}
			if !l.IsDefault {
				it.Translation = tr[key]
				if it.Translation == "" {
					it.Translation = table.tr[pllCollapseWS(key)]
				}
			}
			items[m] = it
			order = append(order, m)
		}
		it.Count++
		found := false
		for _, a := range it.Attrs {
			found = found || a == attr
		}
		if !found {
			it.Attrs = append(it.Attrs, attr)
		}
		return m
	}
	out := s.pllApplyStrings(c, page, l, visit)
	out = pllUCFinish(out, true)

	// Images: data-pll-img="<media id>" (wp-image-N, data-wb-media-id, or an uploads file).
	var imgs []pllVisualImage
	tags := contentImgRe.FindAllIndex(out, pllVisualMaxImgs)
	ids := make([]int64, len(tags))
	var paths []string
	for i, loc := range tags {
		tag := string(out[loc[0]:loc[1]])
		if ids[i] = pllImgID(tag); ids[i] == 0 {
			if p := pllImgUploadPath(tag); p != "" {
				paths = append(paths, p)
			}
		}
	}
	byPath := s.pllMediaByPath(ctx, paths)
	if len(tags) > 0 {
		var b bytes.Buffer
		last := 0
		seen := map[int64]bool{}
		for i, loc := range tags {
			tag := string(out[loc[0]:loc[1]])
			id := ids[i]
			if id == 0 {
				id = byPath[pllImgUploadPath(tag)]
			}
			if id == 0 {
				continue
			}
			b.Write(out[last : loc[0]+4]) // "<img"
			b.WriteString(` data-pll-img="` + strconv.FormatInt(id, 10) + `"`)
			last = loc[0] + 4
			if !seen[id] {
				seen[id] = true
				alt := ""
				if m := pllVisualAltRe.FindStringSubmatch(tag); m != nil {
					alt = html.UnescapeString(m[1] + m[2])
				}
				imgs = append(imgs, pllVisualImage{ID: id, Alt: alt})
			}
		}
		b.Write(out[last:])
		out = b.Bytes()
	}

	// Post content regions.
	target := l
	if l.IsDefault {
		if t := s.pllLang(ctx, vs.Lang); t != nil {
			target = t
		}
	}
	u := currentUser(c)
	var posts []pllVisualPost
	seenPost := map[int64]bool{}
	for _, m := range pllVisualRegionRe.FindAllSubmatch(out, -1) {
		id, _ := strconv.ParseInt(string(m[1]), 10, 64)
		if id <= 0 || seenPost[id] || len(posts) >= pllVisualMaxPost {
			continue
		}
		seenPost[id] = true
		if p := s.pllVisualPostInfo(c, u, id, target); p != nil {
			posts = append(posts, *p)
		}
	}

	list := make([]*pllVisualItem, 0, len(order))
	for _, m := range order {
		list = append(list, items[m])
	}
	alts := map[string]string{}
	if r != nil {
		for _, a := range r.alternates {
			if pu, err := url.Parse(a.URL); err == nil && a.Lang != nil {
				alts[a.Lang.Slug] = pu.RequestURI()
			}
		}
	}
	langInfo := func(x *pllLanguage) map[string]any {
		return map[string]any{"slug": x.Slug, "name": x.Name, "locale": x.Locale, "rtl": x.RTL, "default": x.IsDefault, "w3c": x.W3C()}
	}
	data := map[string]any{
		"lang":       langInfo(l),
		"want":       vs.Lang,
		"strings":    list,
		"posts":      posts,
		"images":     imgs,
		"alternates": alts,
		"param":      pllVisualParam,
	}
	js, err := json.Marshal(data)
	if err != nil {
		js = []byte("{}")
	}
	js = bytes.ReplaceAll(js, []byte(" "), []byte(` `))
	js = bytes.ReplaceAll(js, []byte(" "), []byte(` `))
	inject := `<script type="application/json" id="pll-visual-data">` + string(js) + "</script>\n" +
		`<link rel="stylesheet" id="pll-visual-css" href="/wp-includes/css/pll-visual.css?ver=` + Version + `" media="all" />` + "\n" +
		`<script src="/wp-includes/js/pll-visual.js?ver=` + Version + `" id="pll-visual-js"></script>` + "\n"
	if i := bytes.LastIndex(out, []byte("</body>")); i >= 0 {
		var b bytes.Buffer
		b.Grow(len(out) + len(inject))
		b.Write(out[:i])
		b.WriteString(inject)
		b.Write(out[i:])
		return b.Bytes()
	}
	return append(out, inject...)
}

// pllVisualPostInfo describes a post content region: its language and where its translation in
// target is edited or created.
func (s *Server) pllVisualPostInfo(c *gin.Context, u *store.User, id int64, target *pllLanguage) *pllVisualPost {
	ctx := c.Request.Context()
	p, err := s.store.GetPost(ctx, id)
	if err != nil || p == nil {
		return nil
	}
	info := &pllVisualPost{ID: p.ID, Title: p.Title, Type: p.Type, Target: target.Slug, TargetName: target.Name}
	own := s.pllObjLang(ctx, pllPost, p.ID)
	if own == "" {
		own = defaultSlug(s.pllDefault(ctx))
	}
	info.Lang = own
	if ol := s.pllLang(ctx, own); ol != nil {
		info.LangName = ol.Name
	}
	builder := func(q *store.Post) string {
		if s.wbCanBuild(u, q) && s.wbDocIDs(ctx, []int64{q.ID})[q.ID] {
			return wbEditURL(q.ID)
		}
		return ""
	}
	if u.Can("edit_post", p) {
		info.EditURL = "/wp-admin/post?post=" + itoa64(p.ID) + "&action=edit"
		info.BuilderURL = builder(p)
	}
	if own == target.Slug || !s.pllTranslatedType(p.Type) {
		return info
	}
	trs := s.pllTranslations(ctx, pllPost, p.ID)
	if tid := trs[target.Slug]; tid > 0 && tid != p.ID {
		if t, err := s.store.GetPost(ctx, tid); err == nil && t != nil && t.Status != "trash" {
			info.TrID = t.ID
			if u.Can("edit_post", t) {
				info.TrEditURL = "/wp-admin/post?post=" + itoa64(t.ID) + "&action=edit"
				info.TrBuilder = builder(t)
			}
			return info
		}
	}
	if u.Can("edit_post", p) {
		v := url.Values{}
		v.Set("post_type", p.Type)
		v.Set("from_post", itoa64(p.ID))
		v.Set("new_lang", target.Slug)
		v.Set("_wpnonce", s.nonce(c, "new-post-translation"))
		info.CreateURL = "/wp-admin/post-new?" + v.Encode()
	}
	return info
}

// REST ----------------------------------------------------------------------------------------

// registerPllVisualREST adds POST pll/v1/visual/strings {lang, items:[{original, translation}]}:
// stores string translations (an empty translation deletes one), lists strings the Strings
// screen does not know yet as page text, and purges the page cache.
func (s *Server) registerPllVisualREST() {
	s.rest.register(pllRESTNamespace, "/pll/v1/visual/strings", map[string]restHandler{
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			u := restCurrent(c)
			if !pllCanTranslateAny(u) {
				restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			ctx := c.Request.Context()
			slug, _ := r.Str(c, "lang")
			l := s.pllLang(ctx, slug)
			if l == nil || l.IsDefault {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): lang")
				return
			}
			if !pllCanTranslate(u, l.Slug) {
				restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
				return
			}
			raw, ok := r.Body["items"].([]any)
			if !ok || len(raw) == 0 || len(raw) > pllVisualMaxSave {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): items")
				return
			}
			type pair struct{ k, v string }
			var pairs []pair
			for _, x := range raw {
				m, _ := x.(map[string]any)
				k, _ := m["original"].(string)
				v, _ := m["translation"].(string)
				if strings.TrimSpace(k) == "" || len(k) > pllVisualMaxKey || !utf8.ValidString(k) || !utf8.ValidString(v) ||
					strings.ContainsRune(k, 0) || strings.ContainsRune(v, 0) || utf8.RuneCountInString(v) > pllVisualMaxTr {
					restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): items")
					return
				}
				pairs = append(pairs, pair{k, v})
			}
			var fresh []pllCaptureItem
			for _, p := range pairs {
				if err := s.pllSetString(ctx, l.Slug, p.k, p.v); err != nil {
					restErr(c, http.StatusInternalServerError, "pll_save_failed", "The translation could not be saved.")
					return
				}
				if strings.TrimSpace(p.v) != "" && !strings.ContainsRune(p.k, '\x04') {
					fresh = append(fresh, pllCaptureItem{original: p.k, source: "page", url: pllVisualPath(c.GetHeader("X-Pll-Page"))})
				}
			}
			s.pllVisualEnsureSources(ctx, fresh)
			s.pllFlushStrings()
			s.purgeAll(ctx)
			saved := s.pllStringMap(ctx, l.Slug)
			outItems := make([]map[string]string, 0, len(pairs))
			for _, p := range pairs {
				outItems = append(outItems, map[string]string{"original": p.k, "translation": saved[p.k]})
			}
			c.Header("Cache-Control", "no-store")
			restJSON(c, http.StatusOK, map[string]any{"lang": l.Slug, "items": outItems})
		},
	}, nil)
}

// pllVisualEnsureSources lists strings translated in the visual editor on the Strings screen (and
// keeps Clean from removing their translation) when no theme, plugin or page row has them yet.
func (s *Server) pllVisualEnsureSources(ctx context.Context, items []pllCaptureItem) {
	if len(items) == 0 {
		return
	}
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.original
	}
	have := map[string]bool{}
	if rows, err := s.store.Pool.Query(ctx, `SELECT `+pllKeySQL+` FROM pll_string_sources WHERE original = ANY($1)`, keys); err == nil {
		for rows.Next() {
			var k string
			if rows.Scan(&k) == nil {
				have[k] = true
			}
		}
		rows.Close()
	}
	var add []pllCaptureItem
	for _, it := range items {
		if !have[it.original] {
			have[it.original] = true
			add = append(add, it)
		}
	}
	sort.Slice(add, func(i, j int) bool { return add[i].original < add[j].original })
	_ = s.pllCaptureWrite(ctx, add)
}
