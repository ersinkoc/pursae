package server

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/store"
)

// Golylang is a native plugin: a clone of Polylang Pro. It is off on a fresh install;
// while it is off its routes answer 404/403 and it adds nothing to the admin, the front end or the REST
// API. The switch is the option native_golylang. Its settings live in the option "golylang" with
// Polylang's keys (force_lang, hide_default, rewrite, …); languages, translation groups and string
// translations live in the pll_* tables (migration 0009_golylang.sql).
//
// golylang.go holds the model and the URL layer, golylang_admin.go the wp-admin screens,
// golylang_front.go the front end (switcher, hreflang, menus, strings) and golylang_rest.go the REST API.

const (
	golylangSlug    = "golylang"
	golylangSetting = "native_golylang"
	golylangOption  = "golylang"
	pllCookie       = "pll_language"
)

// golylangOn reports whether the Golylang plugin is active: one cached option read.
func (s *Server) golylangOn() bool { return s.settings.Bool(golylangSetting) }

// golylangOnly is the middleware of Golylang's own routes: 404 while the plugin is off.
func (s *Server) golylangOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.golylangOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// Options ----------------------------------------------------------------------------

// pllOptions is Polylang's "polylang" option array.
type pllOptions struct {
	ForceLang    int                                    `json:"force_lang"`    // 0 content, 1 directory, 2 subdomain, 3 domains
	HideDefault  bool                                   `json:"hide_default"`  // no language code in URLs of the default language
	Rewrite      bool                                   `json:"rewrite"`       // remove /language/ in pretty permalinks
	RedirectLang bool                                   `json:"redirect_lang"` // the front page URL is the language home
	Browser      bool                                   `json:"browser"`       // detect the browser language on the front page
	MediaSupport bool                                   `json:"media_support"` // translate media
	PostTypes    []string                               `json:"post_types"`    // translated custom post types
	Taxonomies   []string                               `json:"taxonomies"`    // translated custom taxonomies
	Sync         []string                               `json:"sync"`          // synchronized fields
	Domains      map[string]string                      `json:"domains"`       // language slug => URL (force_lang 3)
	DefaultLang  string                                 `json:"default_lang"`
	NavMenus     map[string]map[string]map[string]int64 `json:"nav_menus"` // theme > location > lang > menu id
	// Pro
	MediaDuplicate bool `json:"media_duplicate"` // create all media translations at upload
	ShareSlugs     bool `json:"share_slugs"`     // translations may share a slug (golylang_slugs.go)
	TranslateSlugs bool `json:"translate_slugs"` // translated category, tag and author bases
	// Strings found on pages (golylang_capture.go) and machine translation (golylang_mt.go).
	StringCapture bool   `json:"string_capture"`      // record untranslated page strings for logged-in managers and visual mode
	MT            bool   `json:"machine_translation"` // the Machine translation module
	MTProvider    string `json:"mt_provider"`         // deepl, google or openai (OpenAI-compatible)
	MTKey         string `json:"mt_key"`              // sealed API key (pllSealMTKey); never rendered back
	MTEndpoint    string `json:"mt_endpoint"`         // OpenAI-compatible base URL, https only
	MTModel       string `json:"mt_model"`            // OpenAI-compatible model
	MTFormality   string `json:"mt_formality"`        // DeepL formality: default, more, less, prefer_more, prefer_less
}

func pllDefaultOptions() pllOptions {
	return pllOptions{ForceLang: 1, HideDefault: true, Rewrite: true, Sync: []string{}, PostTypes: []string{}, Taxonomies: []string{},
		Domains: map[string]string{}, NavMenus: map[string]map[string]map[string]int64{},
		StringCapture: true, MTProvider: "deepl", MTModel: "gpt-4o-mini", MTFormality: "default"}
}

// pllOpts reads the option; missing keys keep Polylang's defaults.
func (s *Server) pllOpts() pllOptions {
	o := pllDefaultOptions()
	if v := s.settings.Get(golylangOption); v != nil {
		if raw, err := json.Marshal(v); err == nil {
			_ = json.Unmarshal(raw, &o)
		}
	}
	if o.Domains == nil {
		o.Domains = map[string]string{}
	}
	if o.NavMenus == nil {
		o.NavMenus = map[string]map[string]map[string]int64{}
	}
	if o.ForceLang < 0 || o.ForceLang > 3 {
		o.ForceLang = 1
	}
	if o.ForceLang == 3 {
		o.HideDefault = false
	}
	return o
}

func (s *Server) pllSaveOpts(ctx context.Context, o pllOptions) error {
	raw, _ := json.Marshal(o)
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	err := s.settings.Set(ctx, map[string]any{golylangOption: v})
	s.purgeAll(ctx)
	return err
}

func (o pllOptions) syncs(key string) bool {
	for _, k := range o.Sync {
		if k == key {
			return true
		}
	}
	return false
}

// Languages --------------------------------------------------------------------------

type pllLanguage struct {
	ID        int64
	Slug      string
	Name      string
	Locale    string
	RTL       bool
	Flag      string
	Order     int
	Active    bool
	IsDefault bool
}

// W3C is the language tag of <html lang> and hreflang (en-US).
func (l pllLanguage) W3C() string { return strings.ReplaceAll(l.Locale, "_", "-") }

// FlagEmoji turns the ISO 3166-1 flag code into the regional indicator pair.
func (l pllLanguage) FlagEmoji() string { return pllFlagEmoji(l.Flag) }

// FlagHTML is the flag Polylang prints next to language names (an <img> there, an emoji here).
func (l pllLanguage) FlagHTML() template.HTML {
	if l.Flag == "" {
		return ""
	}
	return template.HTML(`<span class="pll-flag pll-flag-` + html.EscapeString(l.Flag) + `" role="img" aria-label="` + html.EscapeString(l.Name) + `">` + pllFlagEmoji(l.Flag) + `</span>`)
}

func pllFlagEmoji(code string) string {
	code = strings.ToUpper(code)
	if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' {
		return ""
	}
	return string([]rune{rune(code[0]-'A') + 0x1F1E6, rune(code[1]-'A') + 0x1F1E6})
}

type pllState struct {
	mu    sync.Mutex
	langs []*pllLanguage
	ok    bool
}

var pllStates sync.Map // *Server => *pllState

func (s *Server) pllState() *pllState {
	v, _ := pllStates.LoadOrStore(s, &pllState{})
	return v.(*pllState)
}

// pllFlush drops the languages cache after a write.
func (s *Server) pllFlush() {
	st := s.pllState()
	st.mu.Lock()
	st.ok, st.langs = false, nil
	st.mu.Unlock()
}

// pllLanguages lists every language in switcher order; the default one is marked.
func (s *Server) pllLanguages(ctx context.Context) []*pllLanguage {
	st := s.pllState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.ok {
		rows, err := s.store.Pool.Query(ctx, `SELECT id, slug, name, locale, rtl, flag, term_order, active FROM pll_languages ORDER BY term_order, name, id`)
		if err != nil {
			return nil
		}
		var list []*pllLanguage
		for rows.Next() {
			l := &pllLanguage{}
			if rows.Scan(&l.ID, &l.Slug, &l.Name, &l.Locale, &l.RTL, &l.Flag, &l.Order, &l.Active) == nil {
				list = append(list, l)
			}
		}
		rows.Close()
		st.langs, st.ok = list, true
	}
	def := s.pllOpts().DefaultLang
	out := make([]*pllLanguage, 0, len(st.langs))
	for _, l := range st.langs {
		cp := *l
		cp.IsDefault = cp.Slug == def
		out = append(out, &cp)
	}
	if def == "" || !pllHas(out, def) {
		if len(out) > 0 {
			out[0].IsDefault = true
		}
	}
	return out
}

func pllHas(list []*pllLanguage, slug string) bool {
	for _, l := range list {
		if l.Slug == slug {
			return true
		}
	}
	return false
}

// pllActiveLanguages are the languages visitors see (Pro can deactivate a language).
func (s *Server) pllActiveLanguages(ctx context.Context) []*pllLanguage {
	var out []*pllLanguage
	for _, l := range s.pllLanguages(ctx) {
		if l.Active || l.IsDefault {
			out = append(out, l)
		}
	}
	return out
}

func (s *Server) pllLang(ctx context.Context, slug string) *pllLanguage {
	for _, l := range s.pllLanguages(ctx) {
		if l.Slug == slug {
			return l
		}
	}
	return nil
}

func (s *Server) pllDefault(ctx context.Context) *pllLanguage {
	for _, l := range s.pllLanguages(ctx) {
		if l.IsDefault {
			return l
		}
	}
	return nil
}

// pllPostTypes are the translated post types: posts and pages, plus the custom types ticked in Settings.
func (s *Server) pllPostTypes() []string {
	return append(append(append([]string{"post", "page"}, s.gooPllPostTypes()...), s.wbPllPostTypes()...), s.pllOpts().PostTypes...) // Goocommerce: products (goo_golylang.go); Web Builder templates
}

// pllTaxonomies are the translated taxonomies: categories and tags, plus the custom ones ticked in Settings.
func (s *Server) pllTaxonomies() []string {
	return append(append([]string{"category", "post_tag"}, s.gooPllTaxonomies()...), s.pllOpts().Taxonomies...) // Goocommerce: product terms
}

func (s *Server) pllTranslatedType(typ string) bool     { return containsStr(s.pllPostTypes(), typ) }
func (s *Server) pllTranslatedTaxonomy(tax string) bool { return containsStr(s.pllTaxonomies(), tax) }

// Translation groups ----------------------------------------------------------------------

// pllKind names an object kind: its table and id column.
type pllKind string

const (
	pllPost  pllKind = "post"
	pllTerm  pllKind = "term"
	pllMedia pllKind = "media"
)

func (k pllKind) table() (string, string) {
	switch k {
	case pllTerm:
		return "pll_term_langs", "term_id"
	case pllMedia:
		return "pll_media_langs", "media_id"
	}
	return "pll_post_langs", "post_id"
}

// pllObjLang is the language slug of an object, the default language when it has none.
func (s *Server) pllObjLang(ctx context.Context, k pllKind, id int64) string {
	if r := pllFrom(ctx); r != nil {
		if v, ok := r.cache.Load(string(k) + ":" + strconv.FormatInt(id, 10)); ok {
			return v.(string)
		}
	}
	tbl, col := k.table()
	var lang string
	_ = s.store.Pool.QueryRow(ctx, `SELECT lang FROM `+tbl+` WHERE `+col+` = $1`, id).Scan(&lang)
	if lang == "" {
		if d := s.pllDefault(ctx); d != nil {
			lang = d.Slug
		}
	}
	if r := pllFrom(ctx); r != nil {
		r.cache.Store(string(k)+":"+strconv.FormatInt(id, 10), lang)
	}
	return lang
}

// pllPrimeObjLangs loads the languages of many objects in one query into the request cache, so a loop,
// a menu or a page list does not ask for each link's language on its own (pllObjLang). Objects without
// a language row get the default language, as pllObjLang gives them.
func (s *Server) pllPrimeObjLangs(ctx context.Context, k pllKind, ids []int64) {
	r := pllFrom(ctx)
	if r == nil || len(ids) == 0 || !s.golylangOn() {
		return
	}
	var need []int64
	for _, id := range ids {
		if _, ok := r.cache.Load(string(k) + ":" + strconv.FormatInt(id, 10)); !ok && id > 0 {
			need = append(need, id)
		}
	}
	if len(need) == 0 {
		return
	}
	tbl, col := k.table()
	rows, err := s.store.Pool.Query(ctx, `SELECT `+col+`, lang FROM `+tbl+` WHERE `+col+` = ANY($1)`, need)
	if err != nil {
		return
	}
	found := make(map[int64]string, len(need))
	for rows.Next() {
		var id int64
		var lang string
		if rows.Scan(&id, &lang) == nil {
			found[id] = lang
		}
	}
	rows.Close()
	def := ""
	if d := s.pllDefault(ctx); d != nil {
		def = d.Slug
	}
	for _, id := range need {
		lang := found[id]
		if lang == "" {
			lang = def
		}
		r.cache.Store(string(k)+":"+strconv.FormatInt(id, 10), lang)
	}
}

// pllPrimePosts is pllPrimeObjLangs for a list of posts.
func (s *Server) pllPrimePosts(ctx context.Context, posts []*store.Post) {
	if len(posts) == 0 || !s.golylangOn() {
		return
	}
	ids := make([]int64, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.ID)
	}
	s.pllPrimeObjLangs(ctx, pllPost, ids)
}

// pllTranslations is pll_get_post_translations(): language => id, the object itself included.
func (s *Server) pllTranslations(ctx context.Context, k pllKind, id int64) map[string]int64 {
	tbl, col := k.table()
	out := map[string]int64{}
	rows, err := s.store.Pool.Query(ctx, `SELECT `+col+`, lang FROM `+tbl+` WHERE grp = (SELECT grp FROM `+tbl+` WHERE `+col+` = $1)`, id)
	if err == nil {
		for rows.Next() {
			var oid int64
			var lang string
			if rows.Scan(&oid, &lang) == nil {
				out[lang] = oid
			}
		}
		rows.Close()
	}
	if len(out) == 0 {
		out[s.pllObjLang(ctx, k, id)] = id
	}
	return out
}

// pllGet is pll_get_post() / pll_get_term(): the translation in lang, 0 when there is none.
func (s *Server) pllGet(ctx context.Context, k pllKind, id int64, lang string) int64 {
	return s.pllTranslations(ctx, k, id)[lang]
}

// pllCopyLang gives a copy (a duplicate, not a translation) the language of its source, in a
// translation group of its own. Nothing happens when the source has no language row.
func (s *Server) pllCopyLang(ctx context.Context, k pllKind, from, to int64) {
	tbl, col := k.table()
	var lang string
	if s.store.Pool.QueryRow(ctx, `SELECT lang FROM `+tbl+` WHERE `+col+` = $1`, from).Scan(&lang) != nil || lang == "" {
		return
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO `+tbl+` (`+col+`, lang, grp) VALUES ($1, $2, nextval('pll_translation_groups'))
		ON CONFLICT (`+col+`) DO UPDATE SET lang = EXCLUDED.lang, grp = EXCLUDED.grp`, to, lang); err != nil {
		log.Printf("golylang: copy the language of %d to %d: %v", from, to, err)
	}
	if r := pllFrom(ctx); r != nil {
		r.cache.Delete(string(k) + ":" + strconv.FormatInt(to, 10))
	}
}

// pllSetLang is pll_set_post_language(). A new language keeps the translation group unless another member
// already has that language, as Polylang drops the link then.
func (s *Server) pllSetLang(ctx context.Context, k pllKind, id int64, lang string) error {
	if s.pllLang(ctx, lang) == nil {
		return errors.New("invalid language")
	}
	tbl, col := k.table()
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO `+tbl+` (`+col+`, lang, grp) VALUES ($1, $2, nextval('pll_translation_groups'))
		ON CONFLICT (`+col+`) DO UPDATE SET lang = EXCLUDED.lang,
		grp = CASE WHEN EXISTS (SELECT 1 FROM `+tbl+` o WHERE o.grp = `+tbl+`.grp AND o.lang = EXCLUDED.lang AND o.`+col+` <> $1)
			THEN EXCLUDED.grp ELSE `+tbl+`.grp END`, id, lang)
	pllForget(ctx, k, id) // the object's language, and translation maps it may have left
	return err
}

// pllForget drops what the request cache knows about the languages and translation groups of the given
// objects after a write: their "<kind>:<id>" language keys and the cached front page translations
// ("fronttr:"). Other request state in the cache (gettext "known:" strings, the string capture counter,
// media translations) is left alone, so a lazy language assignment mid-render does not reset it.
func pllForget(ctx context.Context, k pllKind, ids ...int64) {
	r := pllFrom(ctx)
	if r == nil {
		return
	}
	for _, id := range ids {
		r.cache.Delete(string(k) + ":" + strconv.FormatInt(id, 10))
	}
	r.cache.Range(func(key, _ any) bool {
		if ks, _ := key.(string); strings.HasPrefix(ks, "fronttr:") {
			r.cache.Delete(key)
		}
		return true
	})
}

// pllSaveTranslations is pll_save_post_translations(): the given objects (language => id) become one group.
// Objects that were in the group of one of them but are not listed leave it.
func (s *Server) pllSaveTranslations(ctx context.Context, k pllKind, m map[string]int64) error {
	tbl, col := k.table()
	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var grp int64
	if err := tx.QueryRow(ctx, `SELECT nextval('pll_translation_groups')`).Scan(&grp); err != nil {
		return err
	}
	var ids []int64
	for _, id := range m {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	// Former partners not listed get a group of their own.
	if _, err := tx.Exec(ctx, `UPDATE `+tbl+` SET grp = nextval('pll_translation_groups')
		WHERE grp IN (SELECT grp FROM `+tbl+` WHERE `+col+` = ANY($1)) AND NOT (`+col+` = ANY($1))`, ids); err != nil {
		return err
	}
	for lang, id := range m {
		if id <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+tbl+` (`+col+`, lang, grp) VALUES ($1, $2, $3)
			ON CONFLICT (`+col+`) DO UPDATE SET lang = EXCLUDED.lang, grp = EXCLUDED.grp`, id, lang, grp); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	pllForget(ctx, k, ids...)
	return nil
}

// pllUnlink takes an object out of its translation group.
func (s *Server) pllUnlink(ctx context.Context, k pllKind, id int64) error {
	tbl, col := k.table()
	_, err := s.store.Pool.Exec(ctx, `UPDATE `+tbl+` SET grp = nextval('pll_translation_groups') WHERE `+col+` = $1`, id)
	pllForget(ctx, k, id)
	return err
}

// pllAssignDefault gives every object without a language the default language
// ("You can set them all to the default language.").
func (s *Server) pllAssignDefault(ctx context.Context) error {
	d := s.pllDefault(ctx)
	if d == nil {
		return errors.New("no language")
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO pll_post_langs (post_id, lang, grp)
		SELECT id, $1, nextval('pll_translation_groups') FROM posts WHERE type = ANY($2) AND status <> 'auto-draft'
		ON CONFLICT (post_id) DO NOTHING`, d.Slug, s.pllPostTypes()); err != nil {
		return err
	}
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO pll_term_langs (term_id, lang, grp)
		SELECT id, $1, nextval('pll_translation_groups') FROM terms WHERE taxonomy = ANY($2)
		ON CONFLICT (term_id) DO NOTHING`, d.Slug, s.pllTaxonomies()); err != nil {
		return err
	}
	if s.pllOpts().MediaSupport {
		if _, err := s.store.Pool.Exec(ctx, `INSERT INTO pll_media_langs (media_id, lang, grp)
			SELECT id, $1, nextval('pll_translation_groups') FROM media WHERE deleted_at IS NULL
			ON CONFLICT (media_id) DO NOTHING`, d.Slug); err != nil {
			return err
		}
	}
	return nil
}

// pllUntranslatedContent reports whether posts, pages, categories or tags have no language yet.
func (s *Server) pllUntranslatedContent(ctx context.Context) bool {
	var n bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts p WHERE p.type = ANY($1) AND p.status NOT IN ('auto-draft','trash')
		AND NOT EXISTS (SELECT 1 FROM pll_post_langs l WHERE l.post_id = p.id))
		OR EXISTS (SELECT 1 FROM terms t WHERE t.taxonomy = ANY($2) AND NOT EXISTS (SELECT 1 FROM pll_term_langs l WHERE l.term_id = t.id))`,
		s.pllPostTypes(), s.pllTaxonomies()).Scan(&n)
	return n
}

// Request language ---------------------------------------------------------------------

// pllReq is the language of a request: the current language on the front end, the admin language filter
// in wp-admin ("Show all languages" is nil), the lang parameter of a REST request.
type pllReq struct {
	srv    *Server
	Lang   *pllLanguage
	Filter bool   // QueryPosts and QueryTerms keep to Lang
	Front  bool   // a front-end request
	Path   string // front end: the request path without the language part
	cache  sync.Map

	alternates []pllAlternate // hreflang links of the page (golylangView)

	cookieScript bool // the page sets the language cookie with pllCookieScript (page cache on)
}

type pllCtxKey struct{}

const pllGinKey = "golylang.req"

// pllFrom returns the request language a context carries (a *gin.Context or a request context).
func pllFrom(ctx context.Context) *pllReq {
	if ctx == nil {
		return nil
	}
	if gc, ok := ctx.(*gin.Context); ok {
		if v, ok := gc.Get(pllGinKey); ok {
			r, _ := v.(*pllReq)
			return r
		}
		if gc.Request == nil {
			return nil
		}
		ctx = gc.Request.Context()
	}
	r, _ := ctx.Value(pllCtxKey{}).(*pllReq)
	return r
}

func pllSet(c *gin.Context, r *pllReq) {
	c.Set(pllGinKey, r)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), pllCtxKey{}, r))
}

// pllCurrent is pll_current_language(): nil outside a language (admin "Show all languages", plugin off).
func (s *Server) pllCurrent(ctx context.Context) *pllLanguage {
	if !s.golylangOn() {
		return nil
	}
	if r := pllFrom(ctx); r != nil {
		return r.Lang
	}
	return nil
}

func init() {
	// The language filter of QueryPosts and QueryTerms: objects in the request language, and objects
	// without one when it is the default language. Only translated types and taxonomies are narrowed.
	store.QueryFilter = func(ctx context.Context, kind string, add func(any) string) string {
		r := pllFrom(ctx)
		if r == nil || !r.Filter || r.Lang == nil || !r.srv.golylangOn() {
			return ""
		}
		def := r.Lang.IsDefault
		switch kind {
		case "post":
			c := "(NOT (type = ANY(" + add(r.srv.pllPostTypes()) + ")) OR id IN (SELECT post_id FROM pll_post_langs WHERE lang = " + add(r.Lang.Slug) + ")"
			if def {
				c += " OR NOT EXISTS (SELECT 1 FROM pll_post_langs pl WHERE pl.post_id = posts.id)"
			}
			return c + ")"
		case "term":
			c := "(NOT (taxonomy = ANY(" + add(r.srv.pllTaxonomies()) + ")) OR id IN (SELECT term_id FROM pll_term_langs WHERE lang = " + add(r.Lang.Slug) + ")"
			if def {
				c += " OR NOT EXISTS (SELECT 1 FROM pll_term_langs tl WHERE tl.term_id = terms.id)"
			}
			return c + ")"
		}
		return ""
	}
}

// golylangRequest is the middleware that sets the language of every request while the plugin is on.
func (s *Server) golylangRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.golylangOn() && !s.pllDetect(c) {
			c.Abort()
			return
		}
		c.Next()
	}
}

// pllAdminFiltered are the admin screens whose lists follow the admin bar language filter.
// The Media Library is not filtered: a media item is one row for every language (golylang_media.go).
var pllAdminFiltered = map[string]bool{"/wp-admin/edit": true, "/wp-admin/edit-tags": true, "/wp-admin/nav-menus": true}

// pllDetect sets the request language; false when it answered (a redirect).
func (s *Server) pllDetect(c *gin.Context) bool {
	ctx := c.Request.Context()
	if len(s.pllLanguages(ctx)) == 0 {
		return true
	}
	p := c.Request.URL.Path
	switch {
	case strings.HasPrefix(p, "/wp-admin"):
		r := &pllReq{srv: s}
		u := currentUser(c)
		if u != nil {
			if v, ok := c.GetQuery("lang"); ok && (v == "all" || s.pllLang(ctx, v) != nil) && c.Query("page") != "mlang" {
				if v == "all" { // the admin bar filter: Show all languages
					v = ""
				}
				_ = s.setUserMeta(c, u, "pll_filter_content", v)
			}
			r.Lang = s.pllLang(ctx, s.pllUserFilter(u))
		}
		r.Filter = r.Lang != nil && pllAdminFiltered[strings.TrimSuffix(p, ".php")]
		if c.Request.Method == http.MethodGet && u != nil {
			s.pllEditorLang(c, r)
		}
		if c.Request.Method == http.MethodPost && (p == "/wp-admin/edit-tags" || p == "/wp-admin/term") {
			// A term is unique by name within its language: the duplicate check sees that language only.
			if l := s.pllLang(ctx, c.PostForm("term_lang_choice")); l != nil {
				r.Lang, r.Filter = l, true
			}
		}
		pllSet(c, r)
		return true
	case strings.HasPrefix(p, "/wp-json") || (strings.Trim(p, "/") == "" && c.Query("rest_route") != ""):
		if l := s.pllLang(ctx, c.Query("lang")); l != nil {
			pllSet(c, &pllReq{srv: s, Lang: l, Filter: true})
		} else {
			// No lang parameter: no language and no filter, but the request still carries the plugin, so
			// the Share slugs check (golylang_slugs.go) scopes an edited post's slug to its own language.
			pllSet(c, &pllReq{srv: s})
		}
		return true
	case strings.HasPrefix(p, "/wp-content/") || strings.HasPrefix(p, "/wp-includes/") || strings.HasPrefix(p, "/wp-login") || p == "/wp-comments-post":
		return true
	}
	return s.pllFront(c)
}

// pllEditorLang scopes the editor screens to the language of the edited post, as Polylang does: the
// category checklist, the tags and the parent page dropdown list that language only (and objects
// without a language when it is the default one). post-new uses new_lang, the admin filter or the
// default language, the language golylangNewPost gives the new post.
func (s *Server) pllEditorLang(c *gin.Context, r *pllReq) {
	ctx := c.Request.Context()
	switch strings.TrimSuffix(c.Request.URL.Path, ".php") {
	case "/wp-admin/post":
		id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
		if id <= 0 || c.Query("action") != "edit" {
			return
		}
		var typ string
		if s.store.Pool.QueryRow(ctx, `SELECT type FROM posts WHERE id = $1`, id).Scan(&typ) != nil || !s.pllTranslatedType(typ) {
			return
		}
		if l := s.pllLang(ctx, s.pllObjLang(ctx, pllPost, id)); l != nil {
			r.Lang, r.Filter = l, true
		}
	case "/wp-admin/post-new":
		if !s.pllTranslatedType(c.DefaultQuery("post_type", "post")) {
			return
		}
		l := s.pllLang(ctx, c.Query("new_lang"))
		if l == nil {
			l = r.Lang
		}
		if l == nil {
			l = s.pllDefault(ctx)
		}
		if l != nil {
			r.Lang, r.Filter = l, true
		}
	}
}

// postTermIDsByNames is the tag box of a post save (classic editor, Quick Edit, Bulk Edit, product
// editor): names resolve among the terms of the post's language and new terms get that language, as
// Polylang's tag box does. Without Multi Language, or for untranslated types and taxonomies, it is
// store.TermIDsByNames.
func (s *Server) postTermIDsByNames(ctx context.Context, p *store.Post, tax string, names []string) []int64 {
	if p == nil || !s.golylangOn() || !s.pllTranslatedType(p.Type) || !s.pllTranslatedTaxonomy(tax) || len(s.pllLanguages(ctx)) == 0 {
		ids, _ := s.store.TermIDsByNames(ctx, tax, names)
		return ids
	}
	l := s.pllLang(ctx, s.pllObjLang(ctx, pllPost, p.ID))
	if l == nil {
		ids, _ := s.store.TermIDsByNames(ctx, tax, names)
		return ids
	}
	ids, _ := s.store.TermIDsByNames(context.WithValue(ctx, pllCtxKey{}, &pllReq{srv: s, Lang: l, Filter: true}), tax, names)
	if len(ids) == 0 {
		return ids
	}
	// Terms without a language row are the ones just made (or, in the default language, terms from
	// before Multi Language, which count as default anyway).
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM unnest($1::bigint[]) AS t(id)
		WHERE NOT EXISTS (SELECT 1 FROM pll_term_langs l WHERE l.term_id = t.id)`, ids)
	if err != nil {
		return ids
	}
	var bare []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			bare = append(bare, id)
		}
	}
	rows.Close()
	for _, id := range bare {
		_ = s.pllSetLang(ctx, pllTerm, id, l.Slug)
	}
	return ids
}

// pllUserFilter is the user's admin language filter (user meta pll_filter_content), "" for all languages.
func (s *Server) pllUserFilter(u *store.User) string {
	v, _ := u.Meta["pll_filter_content"].(string)
	return v
}

// pllQueryMode reports whether languages travel in ?lang= (language from content, or plain permalinks).
func (s *Server) pllQueryMode(o pllOptions) bool {
	return o.ForceLang == 0 || s.settings.String("permalink_structure") == ""
}

// pllFront reads the language of a front-end request from the URL, redirects the home page to the
// preferred language (cookie, then browser) and hides the default language code when asked to.
func (s *Server) pllFront(c *gin.Context) bool {
	ctx := c.Request.Context()
	o := s.pllOpts()
	langs := s.pllActiveLanguages(ctx)
	def := s.pllDefault(ctx)
	if def == nil {
		return true
	}
	path := c.Request.URL.Path
	if path == "" {
		path = "/"
	}
	var lang *pllLanguage
	stripped := path
	byURL := false
	switch {
	case s.pllQueryMode(o):
		if l := pllFind(langs, c.Query("lang")); l != nil {
			lang, byURL = l, true
		}
	case o.ForceLang == 1:
		rest := path
		if !o.Rewrite {
			if !strings.HasPrefix(rest, "/language/") {
				break
			}
			rest = strings.TrimPrefix(rest, "/language")
		}
		seg := strings.SplitN(strings.TrimPrefix(rest, "/"), "/", 2)
		if l := pllFind(langs, seg[0]); l != nil {
			lang, byURL = l, true
			stripped = "/"
			if len(seg) > 1 {
				// "/en//evil.example" must not become the protocol-relative "//evil.example" (nor
				// "/\evil.example", which browsers read the same way) when the code is dropped below.
				stripped = "/" + strings.TrimLeft(seg[1], "/\\")
			}
		}
	case o.ForceLang == 2:
		host := c.Request.Host
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h
		}
		if l := pllFind(langs, strings.SplitN(host, ".", 2)[0]); l != nil {
			lang, byURL = l, true
		}
	case o.ForceLang == 3:
		for _, l := range langs {
			if u, err := url.Parse(o.Domains[l.Slug]); err == nil && u.Host != "" && strings.EqualFold(u.Host, c.Request.Host) {
				lang, byURL = l, true
			}
		}
	}
	get := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead
	if byURL && lang.IsDefault && o.HideDefault && o.ForceLang == 1 && !s.pllQueryMode(o) && get {
		// The default language has no code in its URLs.
		target := stripped
		if hasControlChar(target) || strings.Contains(target, "\\") { // browsers drop tabs and read "\" as "/"
			target = "/"
		}
		if c.Request.URL.RawQuery != "" {
			target += "?" + c.Request.URL.RawQuery
		}
		c.Redirect(http.StatusMovedPermanently, target)
		return false
	}
	if lang == nil {
		home := path == "/" && c.Request.URL.RawQuery == ""
		if home && get {
			// Without browser detection a hidden default language is served in place (no redirect).
			// Polylang's get_home_language: with the default language hidden, a click from another page of
			// this site (the language switcher's link to /) is an explicit choice of the default language,
			// so neither the cookie nor the browser language sends it away again.
			pref := def
			chosen := o.HideDefault && pllSameSiteReferer(c)
			if ck, err := c.Cookie(pllCookie); !chosen && err == nil && pllFind(langs, ck) != nil && (o.Browser || !o.HideDefault) {
				pref = pllFind(langs, ck)
			} else if !chosen && o.Browser {
				if l := pllBrowserLang(c.GetHeader("Accept-Language"), langs); l != nil {
					pref = l
				}
			}
			if !pref.IsDefault || !o.HideDefault {
				if target := s.pllHomeURL(ctx, pref); target != "" && !pllSameURL(target, c) {
					c.Header("Vary", "Accept-Language")
					s.pllSetCookie(c, pref)
					c.Redirect(http.StatusFound, target)
					return false
				}
			}
			if o.Browser && o.HideDefault {
				// The home page served in place is this visitor's answer only (Accept-Language and the
				// language cookie decide it, and the cache key holds neither): a stored copy would answer
				// every later visitor before this redirect runs.
				c.Header(cache.HeaderControl, "no-store")
				c.Header("Vary", "Accept-Language, Cookie")
			}
		}
		lang = def
	}
	// Polylang with a page cache: a Set-Cookie header would keep every first visit out of the cache,
	// so the page remembers its language with a script instead (pllCookieScript), printed on every
	// page so the cached copy is the same for all visitors.
	cookieScript := get && s.pageCacheOn()
	if ck, _ := c.Cookie(pllCookie); ck != lang.Slug && get && !cookieScript {
		s.pllSetCookie(c, lang)
	}
	pllSet(c, &pllReq{srv: s, Lang: lang, Filter: true, Front: true, Path: stripped, cookieScript: cookieScript})
	return true
}

// pllSameSiteReferer is WordPress' wp_get_referer as Polylang uses it: a Referer on this host that is
// not the requested URL itself (a reload is no navigation).
func pllSameSiteReferer(c *gin.Context) bool {
	ref := c.GetHeader("Referer")
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" || !strings.EqualFold(u.Host, c.Request.Host) {
		return false
	}
	return u.RequestURI() != c.Request.URL.RequestURI()
}

func pllSameURL(target string, c *gin.Context) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	return (u.Host == "" || strings.EqualFold(u.Host, c.Request.Host)) && strings.TrimSuffix(u.Path, "/") == strings.TrimSuffix(c.Request.URL.Path, "/") && u.RawQuery == c.Request.URL.RawQuery
}

// pageCacheOn reports whether Pursae Cache stores pages.
func (s *Server) pageCacheOn() bool {
	pc := s.currentCache()
	return pc != nil && pc.Config().Enabled
}

// pllCookieScript is Polylang's cookie script for sites behind a page cache: the language cookie is
// written in the browser (same name, lifetime, path and SameSite as pllSetCookie), so the page itself
// sends no Set-Cookie header and stays cacheable.
func (s *Server) pllCookieScript(c *gin.Context) string {
	r := pllFrom(c.Request.Context())
	if r == nil || !r.cookieScript || r.Lang == nil {
		return ""
	}
	return `<script id="pll-cookie-js">(function(){var d=new Date();d.setTime(d.getTime()+31536000000);document.cookie="` +
		pllCookie + `=` + url.QueryEscape(r.Lang.Slug) + `; expires="+d.toUTCString()+"; path=/; SameSite=Lax"+("https:"===location.protocol?"; secure":"");}());</script>` + "\n"
}

func (s *Server) pllSetCookie(c *gin.Context, l *pllLanguage) {
	http.SetCookie(c.Writer, &http.Cookie{Name: pllCookie, Value: l.Slug, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode,
		Secure: c.Request.TLS != nil})
}

func pllFind(langs []*pllLanguage, slug string) *pllLanguage {
	if slug == "" {
		return nil
	}
	for _, l := range langs {
		if l.Slug == slug {
			return l
		}
	}
	return nil
}

// pllBrowserLang matches Accept-Language (by q-value) against the languages: the full tag against the
// locale first, then the primary subtag against the code or the locale's language.
func pllBrowserLang(header string, langs []*pllLanguage) *pllLanguage {
	type pref struct {
		tag string
		q   float64
	}
	var prefs []pref
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			q, _ = strconv.ParseFloat(v, 64)
		}
		if tag != "" && tag != "*" && q > 0 {
			prefs = append(prefs, pref{strings.ToLower(tag), q})
		}
	}
	sort.SliceStable(prefs, func(i, j int) bool { return prefs[i].q > prefs[j].q })
	for _, p := range prefs {
		for _, l := range langs {
			if strings.ToLower(l.W3C()) == p.tag || l.Slug == p.tag {
				return l
			}
		}
		primary := strings.SplitN(p.tag, "-", 2)[0]
		for _, l := range langs {
			if l.Slug == primary || strings.ToLower(strings.SplitN(l.Locale, "_", 2)[0]) == primary {
				return l
			}
		}
	}
	return nil
}

// golylangSitePath is the front-end path without its language part (site() resolves it).
func (s *Server) golylangSitePath(c *gin.Context, path string) string {
	if r := pllFrom(c); r != nil && r.Front && s.golylangOn() {
		return strings.Trim(s.pllTranslateBase(c, r.Path, r.Lang.Slug, false), "/") // Translate slugs: categorie/ => category/
	}
	return path
}

// golylangResolved makes the language of the queried post or term the current language: the language
// set from content, and the right strings and menus when a URL in one language shows another's object
// (the canonical redirect then sends the visitor to the right URL).
func (s *Server) golylangResolved(c *gin.Context, q *siteQuery) {
	r := pllFrom(c)
	if r == nil || !r.Front || q == nil {
		return
	}
	slug, storeView := s.gooPllResolvedLang(c, q) // Goocommerce: a product, product term or shop view (goo_golylang.go)
	switch {
	case storeView:
	case q.Post != nil && s.pllTranslatedType(q.Post.Type):
		slug = s.pllObjLang(c, pllPost, q.Post.ID)
	case q.PostsPage != nil:
		slug = s.pllObjLang(c, pllPost, q.PostsPage.ID)
	case q.Term != nil && s.pllTranslatedTaxonomy(q.Term.Taxonomy):
		slug = s.pllObjLang(c, pllTerm, q.Term.ID)
	}
	if l := s.pllLang(c, slug); l != nil && l.Slug != r.Lang.Slug {
		nr := &pllReq{srv: s, Lang: l, Filter: true, Front: true, Path: r.Path, cookieScript: r.cookieScript}
		pllSet(c, nr)
		s.pllResetCookie(c, l)
	}
}

// pllResetCookie remembers the resolved language instead of the one pllFront read from the URL: it
// drops the pll_language Set-Cookie pllFront may have added and sets l's when the visitor's cookie
// differs. Behind the page cache the cookie script (cookieScript, kept on the new pllReq) does this.
func (s *Server) pllResetCookie(c *gin.Context, l *pllLanguage) {
	if r := pllFrom(c); r != nil && r.cookieScript {
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return
	}
	h := c.Writer.Header()
	if old := h.Values("Set-Cookie"); len(old) > 0 {
		h.Del("Set-Cookie")
		for _, v := range old {
			if !strings.HasPrefix(v, pllCookie+"=") {
				h.Add("Set-Cookie", v)
			}
		}
	}
	if ck, _ := c.Cookie(pllCookie); ck != l.Slug {
		s.pllSetCookie(c, l)
	}
}

// golylangPage is the translation of a static page option (page_on_front, page_for_posts) in the current
// language, the page itself when it has none.
func (s *Server) golylangPage(ctx context.Context, id int64) int64 {
	if id <= 0 {
		return id
	}
	l := s.pllCurrent(ctx)
	if l == nil {
		return id
	}
	if t := s.pllGet(ctx, pllPost, id, l.Slug); t > 0 {
		return t
	}
	return id
}

// URLs ---------------------------------------------------------------------------------

// pllHomeURL is pll_home_url(): the home page URL of a language.
func (s *Server) pllHomeURL(ctx context.Context, l *pllLanguage) string {
	root := s.links(ctx).Home
	return s.pllConvert(ctx, root, root+"/", l, "home", s.pllOpts())
}

// golylangLinkFilter is the links.Builder hook: post, term and archive URLs carry their language.
// Everything that does not depend on the link (options, languages, translated types, the front page and
// its translations) is read once per builder, not once per link: a menu or a page list converts
// hundreds of links on one page.
func (s *Server) golylangLinkFilter(ctx context.Context, root string) func(kind string, obj any, link string) string {
	if !s.golylangOn() {
		return nil
	}
	langs := s.pllLanguages(ctx)
	if len(langs) == 0 {
		return nil
	}
	o := s.pllOpts()
	bySlug := make(map[string]*pllLanguage, len(langs))
	for _, l := range langs {
		bySlug[l.Slug] = l
	}
	types := map[string]bool{}
	for _, t := range s.pllPostTypes() {
		types[t] = true
	}
	taxes := map[string]bool{}
	for _, t := range s.pllTaxonomies() {
		taxes[t] = true
	}
	var front int64
	if s.settings.String("show_on_front") == "page" {
		front = int64(s.settings.Int("page_on_front"))
	}
	return func(kind string, obj any, link string) string {
		var lang *pllLanguage
		switch kind {
		case "post":
			p, ok := obj.(*store.Post)
			if !ok || !types[p.Type] {
				return link
			}
			lang = bySlug[s.pllObjLang(ctx, pllPost, p.ID)]
			if lang != nil && p.Type == "page" && front > 0 && p.Status == "publish" {
				if front == p.ID || s.pllFrontTranslations(ctx, front)[lang.Slug] == p.ID {
					return s.pllConvertLangs(ctx, root, root+"/", lang, "home", o, langs) // the front page of a language is its home
				}
			}
		case "term":
			t, ok := obj.(*store.Term)
			if !ok || !taxes[t.Taxonomy] {
				return link
			}
			lang = bySlug[s.pllObjLang(ctx, pllTerm, t.ID)]
		default:
			lang = s.pllCurrent(ctx)
		}
		if lang == nil {
			return link
		}
		return s.pllConvertLangs(ctx, root, link, lang, kind, o, langs)
	}
}

// pllFrontTranslations are the translations of the static front page, read once per request (the
// request cache is emptied whenever translation groups change).
func (s *Server) pllFrontTranslations(ctx context.Context, front int64) map[string]int64 {
	r := pllFrom(ctx)
	key := "fronttr:" + strconv.FormatInt(front, 10)
	if r != nil {
		if v, ok := r.cache.Load(key); ok {
			return v.(map[string]int64)
		}
	}
	tr := s.pllTranslations(ctx, pllPost, front)
	if r != nil {
		r.cache.Store(key, tr)
	}
	return tr
}

// pllLangHome is the URL a language's paths hang from (no trailing slash).
func (s *Server) pllLangHome(root string, l *pllLanguage, o pllOptions) string {
	return pllLangHomeOf(root, l, o)
}

func pllLangHomeOf(root string, l *pllLanguage, o pllOptions) string {
	if l.IsDefault && o.HideDefault && o.ForceLang != 3 {
		return root
	}
	switch o.ForceLang {
	case 1:
		if o.Rewrite {
			return root + "/" + l.Slug
		}
		return root + "/language/" + l.Slug
	case 2:
		if u, err := url.Parse(root); err == nil && u.Host != "" {
			u.Host = l.Slug + "." + u.Host
			return strings.TrimRight(u.String(), "/")
		}
	case 3:
		if d := strings.TrimRight(o.Domains[l.Slug], "/"); d != "" {
			return d
		}
	}
	return root
}

// pllConvert moves a URL of this site to a language: it strips whatever language it carries and adds l's.
func (s *Server) pllConvert(ctx context.Context, root, link string, l *pllLanguage, kind string, o pllOptions) string {
	return s.pllConvertLangs(ctx, root, link, l, kind, o, s.pllLanguages(ctx))
}

// pllConvertLangs is pllConvert with the languages already read.
func (s *Server) pllConvertLangs(ctx context.Context, root, link string, l *pllLanguage, kind string, o pllOptions, langs []*pllLanguage) string {
	rel, ok := pllRelativeLangs(root, link, o, langs)
	if !ok {
		return link
	}
	if s.pllQueryMode(o) {
		// Language from content keeps post and term URLs; archives and the plain permalink scheme get ?lang=.
		if kind != "home" && o.ForceLang == 0 {
			return root + rel
		}
		if l.IsDefault && o.HideDefault {
			return root + rel
		}
		u, err := url.Parse(root + rel)
		if err != nil {
			return link
		}
		q := u.Query()
		q.Set("lang", l.Slug)
		u.RawQuery = q.Encode()
		if u.Path == "" {
			u.Path = "/"
		}
		return u.String()
	}
	if !o.TranslateSlugs {
		return s.pllLangHome(root, l, o) + rel
	}
	return s.pllLangHome(root, l, o) + s.pllTranslateBase(ctx, rel, l.Slug, true)
}

// pllRelative strips the site home or a language home off a URL; ok is false for foreign URLs.
func (s *Server) pllRelative(ctx context.Context, root, link string, o pllOptions) (string, bool) {
	return pllRelativeLangs(root, link, o, s.pllLanguages(ctx))
}

// pllRelativeLangs is pllRelative with the languages already read.
func pllRelativeLangs(root, link string, o pllOptions, langs []*pllLanguage) (string, bool) {
	homes := []string{}
	for _, l := range langs {
		if h := pllLangHomeOf(root, l, o); h != root {
			homes = append(homes, h)
		}
		if o.ForceLang == 1 { // both /fr and /language/fr, so a changed setting still converts
			homes = append(homes, root+"/"+l.Slug, root+"/language/"+l.Slug)
		}
	}
	sort.Slice(homes, func(i, j int) bool { return len(homes[i]) > len(homes[j]) })
	rel := ""
	found := false
	for _, h := range append(homes, root) {
		if link == h || strings.HasPrefix(link, h+"/") || strings.HasPrefix(link, h+"?") {
			rel, found = strings.TrimPrefix(link, h), true
			break
		}
	}
	if !found {
		return "", false
	}
	if rel == "" || rel[0] == '?' {
		rel = "/" + rel
	}
	if i := strings.IndexByte(rel, '?'); i >= 0 {
		if q, err := url.ParseQuery(rel[i+1:]); err == nil && q.Has("lang") {
			q.Del("lang")
			rel = rel[:i]
			if len(q) > 0 {
				rel += "?" + q.Encode()
			}
		}
	}
	return rel, true
}
