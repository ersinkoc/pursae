package server

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// Pursae Forum site templates: a site's own markup for forum views, kept in the database (option
// pursae_forum_templates) instead of a theme package, so an imported HTML design (Pursae Web
// Builder code sections, docs/developer/web-builder-code-sections.md) can give the forum its own
// look on any site. A site template is a view override (docs/developer/themes/forum.md, "View
// overrides") with the plugin's partials and functions in reach; it wins over the active theme's
// templates/forum/<view>.html and the plugin's own template, and falls back to them when it fails.
//
// With "main": true the template prints the page's whole <main>: under Hello Pursae with Pursae Web
// Builder it replaces the theme's page markup (title, wrappers) like a main part made of code
// sections, and the plugin's forum.css is not loaded on that view (the design brings its CSS).
//
//	GET    pursae/v1/forum/templates          the views and which ones have a site template
//	GET    pursae/v1/forum/templates/{view}   {view, source, main, updated, default (the plugin's template)}
//	POST   pursae/v1/forum/templates/{view}   {source, main}: parse-checked, then render-checked (rolled back on error)
//	DELETE pursae/v1/forum/templates/{view}   back to the theme's or the plugin's template
//
// Writing needs edit_theme_options and unfiltered_html: the markup is trusted, like a code section.

const (
	forumTplOption    = "pursae_forum_templates"
	forumTplMaxLen    = 256 << 10
	forumCtxSiteMain  = "forum.front.site_main" // bool: the view body came from a "main" site template
	forumTplPluginDir = "forum/templates"
)

// forumSiteViews are the views a site template can replace.
var forumSiteViews = []string{forumViewIndex, forumViewForum, forumViewTopic, forumViewNewTopic, forumViewEdit, forumViewSearch,
	forumViewProfile, forumViewMembers, forumViewLogin, forumViewRegister, forumViewVerify, forumViewAccount, "shortcode-index", "shortcode-recent",
	forumSiteParts}

// forumSiteParts is not a view: its {{define}}s (avatars, rows, …) are shared by the site's view
// templates.
const forumSiteParts = "parts"

func forumSiteView(v string) bool {
	for _, x := range forumSiteViews {
		if x == v {
			return true
		}
	}
	return false
}

// forumSiteTpl is one stored template.
type forumSiteTpl struct {
	Source  string `json:"source"`
	Main    bool   `json:"main,omitempty"`
	Updated string `json:"updated,omitempty"`
}

// forumSiteTpls reads the stored templates (view → template).
func (s *Server) forumSiteTpls() map[string]forumSiteTpl {
	out := map[string]forumSiteTpl{}
	m, _ := s.settings.Get(forumTplOption).(map[string]any)
	for v, raw := range m {
		x, _ := raw.(map[string]any)
		src, _ := x["source"].(string)
		if !forumSiteView(v) || src == "" {
			continue
		}
		t := forumSiteTpl{Source: src}
		t.Main, _ = x["main"].(bool)
		t.Updated, _ = x["updated"].(string)
		out[v] = t
	}
	return out
}

// forumSiteCache holds each Server's compiled site templates, rebuilt when the option changes.
var forumSiteCache struct {
	sync.Mutex
	sets map[*Server]*forumSiteSet
}

type forumSiteSet struct {
	key  string
	set  *template.Template
	main map[string]bool // "forum/<view>" → main
}

// forumSiteKey identifies the stored templates (their sources and flags).
func forumSiteKey(tpls map[string]forumSiteTpl) string {
	views := make([]string, 0, len(tpls))
	for v := range tpls {
		views = append(views, v)
	}
	sort.Strings(views)
	var b strings.Builder
	for _, v := range views {
		t := tpls[v]
		fmt.Fprintf(&b, "%s\x00%t\x00%d\x00%s\x00", v, t.Main, len(t.Source), t.Source)
	}
	return b.String()
}

// forumSiteCompile parses the site templates over a fresh copy of the plugin's set (html/template
// cannot clone a set that already ran), so their partials (forum/part-*) and functions work. A view
// whose source does not parse is left out (and logged).
func (s *Server) forumSiteCompile(tpls map[string]forumSiteTpl) (*template.Template, map[string]bool, map[string]error) {
	set, err := parseTree(s.web, forumTplPluginDir, s.forumPluginFuncs())
	if err != nil {
		return nil, nil, map[string]error{"*": err}
	}
	main := map[string]bool{}
	errs := map[string]error{}
	views := make([]string, 0, len(tpls))
	for v := range tpls {
		views = append(views, v)
	}
	sort.Slice(views, func(i, j int) bool { // the shared parts first, then the views
		return views[i] == forumSiteParts || views[j] != forumSiteParts && views[i] < views[j]
	})
	for _, v := range views {
		t := tpls[v]
		name := "forum/" + v
		if _, err := set.New(name).Parse(t.Source); err != nil {
			errs[v] = err
			continue
		}
		main[name] = t.Main
	}
	return set, main, errs
}

// forumSiteFor is the compiled set holding view template name ("forum/<view>") and whether it
// prints the page's main; nil when the site has no template for it.
func (s *Server) forumSiteFor(name string) (*template.Template, bool) {
	tpls := s.forumSiteTpls()
	if len(tpls) == 0 {
		return nil, false
	}
	if _, ok := tpls[strings.TrimPrefix(name, "forum/")]; !ok {
		return nil, false
	}
	key := forumSiteKey(tpls)
	forumSiteCache.Lock()
	defer forumSiteCache.Unlock()
	if forumSiteCache.sets == nil {
		forumSiteCache.sets = map[*Server]*forumSiteSet{}
	}
	cur := forumSiteCache.sets[s]
	if cur == nil || cur.key != key {
		set, main, errs := s.forumSiteCompile(tpls)
		for v, err := range errs {
			log.Printf("forum: site template %s: %v", v, err)
		}
		cur = &forumSiteSet{key: key, set: set, main: main}
		forumSiteCache.sets[s] = cur
	}
	if cur.set == nil || cur.set.Lookup(name) == nil {
		return nil, false
	}
	return cur.set, cur.main[name]
}

// forumSiteLastErr is the last site template failure (the render check after a save reads it).
var forumSiteLastErr atomic.Value

// forumSiteExec runs the site template of view template name. ok is false when the site has none
// or it failed (logged; the caller falls back to the theme's or the plugin's template).
func (s *Server) forumSiteExec(c *gin.Context, name string, data any) (string, bool) {
	set, main := s.forumSiteFor(name)
	if set == nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("forum: site template %s: %v", name, err)
		forumSiteLastErr.Store(name + ": " + err.Error())
		return "", false
	}
	if main && c != nil && !strings.HasPrefix(name, "forum/shortcode-") {
		c.Set(forumCtxSiteMain, true)
	}
	return buf.String(), true
}

// forumSiteMain is the forum view body of the request when a "main" site template printed it.
func forumSiteMain(c *gin.Context, q *siteQuery) (string, bool) {
	if c == nil || q == nil || q.Forum == "" || !c.GetBool(forumCtxSiteMain) {
		return "", false
	}
	body, _ := c.Get(forumCtxRender)
	str, _ := body.(string)
	return str, str != ""
}

// --- REST --------------------------------------------------------------------------------------

func (s *Server) registerForumTemplatesREST(need func(string, restHandler) restHandler) {
	api := s.rest
	write := func(h restHandler) restHandler {
		return need("edit_theme_options", func(c *gin.Context, r *restRequest) {
			if !restCurrent(c).Can("unfiltered_html", nil) {
				restErr(c, http.StatusForbidden, "rest_forbidden", "Site templates hold raw HTML: you need the unfiltered_html capability (an administrator).")
				return
			}
			h(c, r)
		})
	}
	api.register(pursaeNS, "/pursae/v1/forum/templates", map[string]restHandler{http.MethodGet: need("edit_theme_options", s.restForumTemplates)}, nil)
	api.register(pursaeNS, `/pursae/v1/forum/templates/(?P<view>[a-z-]+)`, map[string]restHandler{
		http.MethodGet:    need("edit_theme_options", s.restForumTemplate),
		http.MethodPost:   write(s.restForumTemplateSave),
		http.MethodPut:    write(s.restForumTemplateSave),
		http.MethodDelete: write(s.restForumTemplateDelete),
	}, nil)
}

func (s *Server) restForumTemplates(c *gin.Context, _ *restRequest) {
	tpls := s.forumSiteTpls()
	views := []map[string]any{}
	for _, v := range forumSiteViews {
		row := map[string]any{"view": v, "site": false}
		if t, ok := tpls[v]; ok {
			row["site"], row["main"], row["updated"], row["bytes"] = true, t.Main, t.Updated, len(t.Source)
		}
		views = append(views, row)
	}
	c.JSON(http.StatusOK, map[string]any{"views": views,
		"docs": "Site templates are Pursae Forum view overrides (the dot is the forumPage; docs/developer/themes/forum.md). GET a view for its plugin default."})
}

func (s *Server) restForumTemplateView(c *gin.Context, r *restRequest) (string, bool) {
	v := r.Params["view"]
	if !forumSiteView(v) {
		restErr(c, http.StatusNotFound, "forum_template_unknown", "Unknown forum view. Views: "+strings.Join(forumSiteViews, ", ")+".")
		return "", false
	}
	return v, true
}

// forumPluginSource is the plugin's own template file of a view (what a site template replaces).
func (s *Server) forumPluginSource(view string) string {
	b, err := fs.ReadFile(s.web, forumTplPluginDir+"/"+view+".html")
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Server) restForumTemplate(c *gin.Context, r *restRequest) {
	v, ok := s.restForumTemplateView(c, r)
	if !ok {
		return
	}
	t, has := s.forumSiteTpls()[v]
	out := map[string]any{"view": v, "site": has, "source": t.Source, "main": t.Main, "updated": t.Updated,
		"default":  s.forumPluginSource(v),
		"partials": "The default uses the plugin's partials (forum/part-*); a site template may call them too: " + forumTplPluginDir + "/parts*.html."}
	c.JSON(http.StatusOK, out)
}

var forumTplMu sync.Mutex

func (s *Server) restForumTemplateSave(c *gin.Context, r *restRequest) {
	v, ok := s.restForumTemplateView(c, r)
	if !ok {
		return
	}
	src, ok := r.Str(c, "source")
	if !ok || strings.TrimSpace(src) == "" {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): source", map[string]any{"params": []string{"source"}})
		return
	}
	if len(src) > forumTplMaxLen || !utf8.ValidString(src) {
		restErr(c, http.StatusBadRequest, "forum_template_invalid", fmt.Sprintf("source must be UTF-8 text of at most %d KB.", forumTplMaxLen>>10))
		return
	}
	main, _ := r.Bool(c, "main")
	if main && (strings.HasPrefix(v, "shortcode-") || v == forumSiteParts) {
		restErr(c, http.StatusBadRequest, "forum_template_invalid", "main applies to forum views only, not to the shortcode templates or the shared parts.")
		return
	}
	forumTplMu.Lock()
	defer forumTplMu.Unlock()
	prev := s.forumSiteTpls()
	next := map[string]forumSiteTpl{}
	for k, t := range prev {
		next[k] = t
	}
	next[v] = forumSiteTpl{Source: src, Main: main, Updated: time.Now().UTC().Format(time.RFC3339)}
	if _, _, errs := s.forumSiteCompile(next); errs[v] != nil || errs["*"] != nil {
		err := errs[v]
		if err == nil {
			err = errs["*"]
		}
		restErr(c, http.StatusBadRequest, "forum_template_parse", "The template does not parse: "+err.Error())
		return
	}
	if err := s.forumSiteStore(c, next); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the template.")
		return
	}
	if err := s.forumSiteRenderCheck(c, v); err != nil {
		_ = s.forumSiteStore(c, prev)
		restErr(c, http.StatusBadRequest, "forum_template_render", "The template was rolled back because the view failed to render with it: "+err.Error())
		return
	}
	s.purgeAll(c)
	c.JSON(http.StatusOK, map[string]any{"view": v, "saved": true, "main": main, "updated": next[v].Updated})
}

func (s *Server) restForumTemplateDelete(c *gin.Context, r *restRequest) {
	v, ok := s.restForumTemplateView(c, r)
	if !ok {
		return
	}
	forumTplMu.Lock()
	defer forumTplMu.Unlock()
	next := s.forumSiteTpls()
	_, had := next[v]
	delete(next, v)
	if err := s.forumSiteStore(c, next); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the templates.")
		return
	}
	s.purgeAll(c)
	c.JSON(http.StatusOK, map[string]any{"view": v, "deleted": had})
}

func (s *Server) forumSiteStore(c *gin.Context, tpls map[string]forumSiteTpl) error {
	m := map[string]any{}
	for v, t := range tpls {
		row := map[string]any{"source": t.Source, "updated": t.Updated}
		if t.Main {
			row["main"] = true
		}
		m[v] = row
	}
	return s.settings.Set(c.Request.Context(), map[string]any{forumTplOption: m})
}

// forumSiteRenderCheck renders the view in-process (as the caller) where it has a public URL and
// fails when the site template errors there. Views that need a state (a form error, an edit
// target, a signed-in member) are checked when they are next shown, falling back on failure.
func (s *Server) forumSiteRenderCheck(c *gin.Context, view string) error {
	ctx := c.Request.Context()
	var target string
	switch view {
	case forumViewIndex, forumSiteParts:
		target = s.forumURL(ctx)
	case forumViewLogin, forumViewRegister, forumViewVerify, forumViewMembers:
		target = s.forumURL(ctx, view)
	case forumViewSearch:
		target = s.forumURL(ctx, "search") + "?q=a"
	case forumViewForum:
		if list, err := s.forumList(ctx); err == nil {
			var first func([]*forumForum) *forumForum
			first = func(l []*forumForum) *forumForum {
				for _, f := range l {
					if f.Kind != "category" {
						return f
					}
					if x := first(f.Children); x != nil {
						return x
					}
				}
				return nil
			}
			if f := first(list); f != nil {
				target = s.forumForumURL(ctx, f)
			}
		}
	case forumViewTopic:
		if ts, err := s.forumRecentTopics(ctx, 1); err == nil && len(ts) > 0 {
			target = s.forumTopicURL(ctx, ts[0], 1)
		}
	}
	if target == "" {
		return nil
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil
	}
	forumSiteLastErr.Store("")
	req := httptest.NewRequest(http.MethodGet, u.RequestURI(), nil).WithContext(ctx)
	req.Host = c.Request.Host
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	if msg, _ := forumSiteLastErr.Load().(string); msg != "" {
		return errors.New(msg)
	}
	if rec.Code >= 500 {
		return fmt.Errorf("%s returned %d", u.RequestURI(), rec.Code)
	}
	return nil
}
