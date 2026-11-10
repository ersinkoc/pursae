package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Golylang's REST API: pll/v1/languages and pll/v1/settings (Polylang 3.7+), the lang parameter of the
// wp/v2 collections (golylangRequest narrows the queries) and the lang and translations fields of posts,
// pages, categories and tags (Pro). While the plugin is off the namespace is hidden (rest.go) and the
// fields are not added.

const pllRESTNamespace = "pll/v1"

// golylangRESTFields adds lang and translations to a post or term response.
func (s *Server) golylangRESTFields(c *gin.Context, k pllKind, id int64, typ string, out map[string]any) {
	if !s.golylangOn() || len(s.pllLanguages(c)) == 0 {
		return
	}
	if (k == pllPost && !s.pllTranslatedType(typ)) || (k == pllTerm && !s.pllTranslatedTaxonomy(typ)) {
		return
	}
	out["lang"] = s.pllObjLang(c, k, id)
	out["translations"] = s.pllTranslations(c, k, id)
}

const (
	pllRESTLangKey         = "pll_rest_lang"         // lang sent in a wp/v2 write body (golylangRESTParams)
	pllRESTTranslationsKey = "pll_rest_translations" // translations sent in a wp/v2 write body
)

// golylangRESTParams reads the lang and translations fields of a wp/v2 write from the JSON or form
// body, as Polylang registers them as REST fields: {"title": …, "lang": "fr", "translations": {"en": 12}}.
// The body wins over the query string like every other REST parameter (restRequest.get). A valid
// lang becomes the request language before the handler runs, exactly as ?lang= does in pllDetect,
// so the Share slugs check and the term name check see the right language.
func (s *Server) golylangRESTParams(c *gin.Context, rt *restRoute, req *restRequest) {
	if !s.golylangOn() || rt.Namespace != "wp/v2" || req.Method == http.MethodGet || req.Method == http.MethodHead {
		return
	}
	ct := c.ContentType()
	form := ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data" // never parse a raw upload body
	lang := ""
	if v, ok := req.Body["lang"]; ok {
		lang, _ = v.(string)
	} else if form {
		lang = c.PostForm("lang")
	}
	if l := s.pllLang(c, lang); lang != "" && l != nil {
		c.Set(pllRESTLangKey, l.Slug)
		pllSet(c, &pllReq{srv: s, Lang: l, Filter: true})
	}
	m := map[string]int64{}
	if v, ok := req.Body["translations"].(map[string]any); ok {
		for lang, raw := range v {
			var id int64
			switch t := raw.(type) {
			case float64:
				id = int64(t)
			case string:
				id, _ = strconv.ParseInt(t, 10, 64)
			}
			if id > 0 {
				m[lang] = id
			}
		}
	} else if form {
		for lang, raw := range c.PostFormMap("translations") {
			if id, _ := strconv.ParseInt(raw, 10, 64); id > 0 {
				m[lang] = id
			}
		}
	}
	if len(m) > 0 {
		c.Set(pllRESTTranslationsKey, m)
	}
}

// pllRESTLang is the lang parameter of a REST write: the body field, else ?lang=.
func pllRESTLang(c *gin.Context) string {
	if v := c.GetString(pllRESTLangKey); v != "" {
		return v
	}
	return c.Query("lang")
}

// pllRESTTranslations is the translations parameter of a REST write: the body field, else
// ?translations[lang]=id. Ids that do not parse are dropped.
func pllRESTTranslations(c *gin.Context) map[string]int64 {
	if v, ok := c.Get(pllRESTTranslationsKey); ok {
		if m, ok := v.(map[string]int64); ok {
			return m
		}
	}
	out := map[string]int64{}
	for lang, raw := range c.QueryMap("translations") {
		if id, _ := strconv.ParseInt(raw, 10, 64); id > 0 {
			out[lang] = id
		}
	}
	return out
}

// golylangRESTSaved applies the lang and translations[lang]=id parameters of a term write.
func (s *Server) golylangRESTSaved(c *gin.Context, k pllKind, id int64) {
	if !s.golylangOn() || len(s.pllLanguages(c)) == 0 {
		return
	}
	if l := s.pllLang(c, pllRESTLang(c)); l != nil {
		_ = s.pllSetLang(c, k, id, l.Slug)
	} else if s.pllObjLangRow(c, k, id) == "" {
		if d := s.pllDefault(c); d != nil {
			_ = s.pllSetLang(c, k, id, d.Slug)
		}
	}
	if m := pllRESTTranslations(c); len(m) > 0 {
		tr := s.pllTranslations(c, k, id)
		own := s.pllObjLang(c, k, id)
		for lang, oid := range m {
			if lang == own || oid == id {
				continue // the saved object keeps its own place in the group
			}
			if s.pllObjLang(c, k, oid) == lang && s.pllRESTLinkable(c, k, id, oid) {
				tr[lang] = oid
			}
		}
		_ = s.pllSaveTranslations(c, k, tr)
	}
}

// pllRESTLinkable reports whether oid may join the translation group of the saved object id: for a term,
// a term of the same taxonomy that the user may edit (edit_term maps to manage_categories), as
// golylangSaved requires of posts.
func (s *Server) pllRESTLinkable(c *gin.Context, k pllKind, id, oid int64) bool {
	if k != pllTerm {
		return true
	}
	u := restCurrent(c)
	if u == nil || !u.Can("manage_categories", nil) {
		return false
	}
	saved, err := s.store.GetTerm(c, id)
	if err != nil {
		return false
	}
	t, err := s.store.GetTerm(c, oid)
	return err == nil && t.Taxonomy == saved.Taxonomy
}

// pllObjLangRow is the stored language of an object, "" when it has none.
func (s *Server) pllObjLangRow(c *gin.Context, k pllKind, id int64) string {
	tbl, col := k.table()
	var lang string
	_ = s.store.Pool.QueryRow(c, `SELECT lang FROM `+tbl+` WHERE `+col+` = $1`, id).Scan(&lang)
	return lang
}

// pllRESTLanguage is a language as /pll/v1/languages returns it.
func (s *Server) pllRESTLanguage(c *gin.Context, l *pllLanguage) map[string]any {
	front := int64(0)
	posts := int64(0)
	if s.settings.String("show_on_front") == "page" {
		if id := int64(s.settings.Int("page_on_front")); id > 0 {
			front = s.pllGet(c, pllPost, id, l.Slug)
		}
		if id := int64(s.settings.Int("page_for_posts")); id > 0 {
			posts = s.pllGet(c, pllPost, id, l.Slug)
		}
	}
	return map[string]any{"term_id": l.ID, "name": l.Name, "slug": l.Slug, "locale": l.Locale, "w3c": l.W3C(), "facebook": l.Locale,
		"is_rtl": l.RTL, "term_group": l.Order, "flag_code": l.Flag, "flag": string(l.FlagHTML()), "flag_url": "", "custom_flag_url": "", "custom_flag": "",
		"is_default": l.IsDefault, "active": l.Active || l.IsDefault, "home_url": s.pllHomeURL(c, l), "search_url": s.pllHomeURL(c, l),
		"host": "", "page_on_front": front, "page_for_posts": posts, "fallbacks": []string{}}
}

func (s *Server) registerGolylangREST() {
	s.registerPllMTREST()     // pll/v1/translate (golylang_mt.go)
	s.registerPllVisualREST() // pll/v1/visual/strings (golylang_visual.go)
	api := s.rest
	findLang := func(c *gin.Context, key string) *pllLanguage {
		for _, l := range s.pllLanguages(c) {
			if itoa64(l.ID) == key || l.Slug == key {
				return l
			}
		}
		return nil
	}
	canManage := func(c *gin.Context) bool {
		u := restCurrent(c)
		if u == nil || !u.Can("manage_options", nil) {
			restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to do that.")
			return false
		}
		return true
	}
	saveLang := func(c *gin.Context, r *restRequest, l *pllLanguage) {
		isNew := l == nil
		f := &pllLanguage{Active: true}
		if !isNew {
			*f = *l
		}
		if v, ok := r.Str(c, "locale"); ok {
			f.Locale = v
		}
		if p, ok := pllPredefinedByLocale(f.Locale); ok && isNew {
			f.Name, f.Slug, f.RTL, f.Flag = p.Name, p.Code, p.RTL, p.Flag
		}
		if v, ok := r.Str(c, "name"); ok {
			f.Name = v
		}
		if v, ok := r.Str(c, "slug"); ok {
			f.Slug = v
		}
		if v, ok := r.Bool(c, "is_rtl"); ok {
			f.RTL = v
		}
		if v, ok := r.Str(c, "flag_code"); ok {
			f.Flag = v
		}
		if v, ok := r.Int(c, "term_group"); ok {
			f.Order = int(v)
		}
		if isNew && f.Locale == "" {
			restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): locale")
			return
		}
		if err := s.pllSaveLanguage(c, f, isNew); err != nil {
			restErr(c, http.StatusBadRequest, "pll_invalid_language", err.Error())
			return
		}
		if v, ok := r.Bool(c, "active"); ok && !isNew {
			_, _ = s.store.Pool.Exec(c, `UPDATE pll_languages SET active = $2 WHERE id = $1`, f.ID, v)
			s.pllFlush()
		}
		status := http.StatusOK
		if isNew {
			status = http.StatusCreated
		}
		restJSON(c, status, s.pllRESTLanguage(c, findLang(c, f.Slug)))
	}
	api.register(pllRESTNamespace, "/pll/v1/languages", map[string]restHandler{
		http.MethodGet: func(c *gin.Context, r *restRequest) {
			out := []map[string]any{}
			for _, l := range s.pllLanguages(c) {
				out = append(out, s.pllRESTLanguage(c, l))
			}
			restJSON(c, http.StatusOK, out)
		},
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			if canManage(c) {
				saveLang(c, r, nil)
			}
		},
	}, nil)
	api.register(pllRESTNamespace, `/pll/v1/languages/(?P<id>[a-z0-9_-]+)`, map[string]restHandler{
		http.MethodGet: func(c *gin.Context, r *restRequest) {
			l := findLang(c, r.Params["id"])
			if l == nil {
				restErr(c, http.StatusNotFound, "pll_rest_invalid_language", "Invalid language.")
				return
			}
			restJSON(c, http.StatusOK, s.pllRESTLanguage(c, l))
		},
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			if !canManage(c) {
				return
			}
			l := findLang(c, r.Params["id"])
			if l == nil {
				restErr(c, http.StatusNotFound, "pll_rest_invalid_language", "Invalid language.")
				return
			}
			saveLang(c, r, l)
		},
		http.MethodDelete: func(c *gin.Context, r *restRequest) {
			if !canManage(c) {
				return
			}
			l := findLang(c, r.Params["id"])
			if l == nil {
				restErr(c, http.StatusNotFound, "pll_rest_invalid_language", "Invalid language.")
				return
			}
			prev := s.pllRESTLanguage(c, l)
			if err := s.pllDeleteLanguage(c, l); err != nil {
				restErr(c, http.StatusInternalServerError, "pll_rest_error", err.Error())
				return
			}
			restJSON(c, http.StatusOK, map[string]any{"deleted": true, "previous": prev})
		},
	}, nil)
	settingsOut := func(c *gin.Context) map[string]any {
		o := s.pllOpts()
		return map[string]any{"force_lang": o.ForceLang, "domains": o.Domains, "hide_default": o.HideDefault, "rewrite": o.Rewrite,
			"redirect_lang": o.RedirectLang, "browser": o.Browser, "media_support": o.MediaSupport, "post_types": o.PostTypes,
			"taxonomies": o.Taxonomies, "sync": o.Sync, "default_lang": defaultSlug(s.pllDefault(c)), "nav_menus": o.NavMenus,
			"media": map[string]any{"duplicate": o.MediaDuplicate}, "version": Version}
	}
	api.register(pllRESTNamespace, "/pll/v1/settings", map[string]restHandler{
		http.MethodGet: func(c *gin.Context, r *restRequest) {
			if canManage(c) {
				restJSON(c, http.StatusOK, settingsOut(c))
			}
		},
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			if !canManage(c) {
				return
			}
			o := s.pllOpts()
			if v, ok := r.Int(c, "force_lang"); ok && v >= 0 && v <= 3 {
				o.ForceLang = int(v)
			}
			for key, dst := range map[string]*bool{"hide_default": &o.HideDefault, "rewrite": &o.Rewrite, "redirect_lang": &o.RedirectLang,
				"browser": &o.Browser, "media_support": &o.MediaSupport} {
				if v, ok := r.Bool(c, key); ok {
					*dst = v
				}
			}
			if v, ok := r.Str(c, "default_lang"); ok {
				if s.pllLang(c, v) == nil {
					restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): default_lang")
					return
				}
				o.DefaultLang = v
			}
			if v, ok := r.Body["sync"].([]any); ok {
				o.Sync = []string{}
				for _, x := range v {
					if k, ok := x.(string); ok {
						for _, sl := range pllSyncLabels {
							if sl[0] == k {
								o.Sync = append(o.Sync, k)
							}
						}
					}
				}
			}
			if v, ok := r.Body["domains"].(map[string]any); ok {
				o.Domains = map[string]string{}
				for k, d := range v {
					ds, ok := d.(string)
					if !ok || s.pllLang(c, k) == nil {
						continue
					}
					// The checks of the URL modifications form (pllSettingsSave): an http(s) URL with a host.
					if ds = strings.TrimRight(strings.TrimSpace(ds), "/"); ds == "" {
						continue
					}
					if u, err := url.Parse(ds); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
						restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): domains")
						return
					}
					o.Domains[k] = ds
				}
			}
			if (o.ForceLang == 2 || o.ForceLang == 3) && s.settings.String("permalink_structure") == "" {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): force_lang (subdomains and domains need pretty permalinks)")
				return
			}
			if o.ForceLang == 3 {
				for _, l := range s.pllLanguages(c) {
					if o.Domains[l.Slug] == "" {
						restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): domains (every language needs a domain)")
						return
					}
				}
			}
			if err := s.pllSaveOpts(c, o); err != nil {
				restErr(c, http.StatusInternalServerError, "pll_rest_error", err.Error())
				return
			}
			s.pllFlush()
			restJSON(c, http.StatusOK, settingsOut(c))
		},
	}, nil)
	s.registerGolylangMediaREST(api) // pll/v1/media/{id}/i18n (golylang_media.go)
}
