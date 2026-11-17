package server

import (
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Kit overrides (docs/developer/web-builder-code-sections.md, "Classes and variables"): the
// element editor's edits of a design's classes and variables, kept per kit beside the kit's files
// (never in them, so a kit update keeps them) in the wb_kit_overrides option:
//
//	{"<slug>": {"live": {rules}, "version": 3, "draft": {rules}, "draftVersion": 7, "updated": "…"}}
//
// The editor writes the draft (PUT kits/{slug}/override, every change, versioned: a stale base
// is a 409) and its frame shows it; publishing a page that uses the kit makes the draft live
// (POST kits/{slug}/override/publish). Every page whose code head loads the kit's files then
// prints the live override after the design's stylesheets (wbKitOverrideStyles), so a class or a
// variable changes site-wide at once. Writing needs unfiltered_html and edit_theme_options, like
// the kit itself; the override is validated by webbuilder.ParseKitOverride (CSS sanitizer).

const wbKitOverridesOption = "wb_kit_overrides"

// wbKitOverrideEntry is one kit's override state.
type wbKitOverrideEntry struct {
	Live         webbuilder.KitOverride  `json:"live"`
	Version      int                     `json:"version"`
	Draft        *webbuilder.KitOverride `json:"draft,omitempty"`
	DraftVersion int                     `json:"draftVersion"`
	Updated      string                  `json:"updated,omitempty"`
}

var (
	wbKitOverrideMu    sync.Mutex
	wbKitOverrideCache sync.Map // "css" → *map[string]string (slug → live CSS)
)

func (s *Server) wbKitOverrides() map[string]*wbKitOverrideEntry {
	out := map[string]*wbKitOverrideEntry{}
	raw := s.settings.Get(wbKitOverridesOption)
	if raw == nil {
		return out
	}
	if b, err := json.Marshal(raw); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	for slug, e := range out {
		if e == nil || !wbKitSlugRE.MatchString(slug) {
			delete(out, slug)
		}
	}
	return out
}

func (s *Server) wbSaveKitOverrides(c *gin.Context, all map[string]*wbKitOverrideEntry) error {
	if err := s.settings.Set(c, map[string]any{wbKitOverridesOption: all}); err != nil {
		return err
	}
	wbKitOverrideCache.Delete("css")
	return nil
}

// wbKitOverrideView is a kit's override as the REST API shows it.
func wbKitOverrideView(slug string, e *wbKitOverrideEntry) map[string]any {
	if e == nil {
		e = &wbKitOverrideEntry{}
	}
	draft := e.Draft
	if draft == nil {
		draft = &e.Live
	}
	return map[string]any{"slug": slug, "live": e.Live, "version": e.Version, "draft": draft, "draftVersion": e.DraftVersion,
		"pending": webbuilder.KitOverrideCSS(draft) != webbuilder.KitOverrideCSS(&e.Live), "updated": e.Updated}
}

func (s *Server) wbRegisterKitOverrideREST(reg func(pattern string, methods map[string]restHandler, handshake bool)) {
	const kit = `/web-builder/v2/kits/(?P<slug>[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)`
	reg(kit+`/override`, map[string]restHandler{http.MethodGet: s.wbRESTKitOverride, http.MethodPut: s.wbRESTKitOverridePut}, true)
	reg(kit+`/override/publish`, map[string]restHandler{http.MethodPost: s.wbRESTKitOverridePublish}, true)
	reg(kit+`/pages`, map[string]restHandler{http.MethodGet: s.wbRESTKitPages}, true)
}

func (s *Server) wbKitExists(c *gin.Context, slug string) bool {
	if s.wbKitList()[slug] == nil {
		restErr(c, http.StatusNotFound, "wb_kit_not_found", "No design kit has that slug.")
		return false
	}
	return true
}

func (s *Server) wbRESTKitOverride(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, false) {
		return
	}
	slug := r.Params["slug"]
	if !s.wbKitExists(c, slug) {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, wbKitOverrideView(slug, s.wbKitOverrides()[slug]))
}

// wbRESTKitOverridePut is PUT kits/{slug}/override {draft: {rules}, base: draftVersion}: replaces
// the draft. base must be the draft version the editor read (409 wb_kit_override_conflict with
// the current state otherwise).
func (s *Server) wbRESTKitOverridePut(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, true) {
		return
	}
	slug := r.Params["slug"]
	raw, ok := wbBody(c, r, webbuilder.MaxOverrideBytes+4<<10)
	if !ok {
		return
	}
	var in struct {
		Draft json.RawMessage `json:"draft"`
		Base  *int            `json:"base"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_json", "The body must be {draft: {rules}, base}.")
		return
	}
	o, err := webbuilder.ParseKitOverride(in.Draft)
	if err != nil {
		if errors.Is(err, webbuilder.ErrTooLargeOverride) {
			restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, "A kit override holds at most 256 KB.")
			return
		}
		restErr(c, http.StatusUnprocessableEntity, "bad_css", err.Error(), map[string]any{"key": "draft"})
		return
	}
	wbKitOverrideMu.Lock()
	defer wbKitOverrideMu.Unlock()
	if !s.wbKitExists(c, slug) {
		return
	}
	all := s.wbKitOverrides()
	e := all[slug]
	if e == nil {
		e = &wbKitOverrideEntry{}
		all[slug] = e
	}
	if in.Base != nil && *in.Base != e.DraftVersion {
		restErr(c, http.StatusConflict, "wb_kit_override_conflict", "The design's classes or variables were changed in another editor. Reload to see their changes.",
			map[string]any{"override": wbKitOverrideView(slug, e)})
		return
	}
	e.Draft = o
	e.DraftVersion++
	e.Updated = wbTime(time.Now())
	if err := s.wbSaveKitOverrides(c, all); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the kit override.")
		return
	}
	c.JSON(http.StatusOK, wbKitOverrideView(slug, e))
}

// wbRESTKitOverridePublish is POST kits/{slug}/override/publish {base?}: the draft goes live on
// every page using the kit.
func (s *Server) wbRESTKitOverridePublish(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, true) {
		return
	}
	slug := r.Params["slug"]
	var in struct {
		Base *int `json:"base"`
	}
	if raw, ok := wbBody(c, r, 1<<10); !ok {
		return
	} else if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	wbKitOverrideMu.Lock()
	defer wbKitOverrideMu.Unlock()
	if !s.wbKitExists(c, slug) {
		return
	}
	all := s.wbKitOverrides()
	e := all[slug]
	if e == nil || e.Draft == nil {
		c.JSON(http.StatusOK, wbKitOverrideView(slug, e))
		return
	}
	if in.Base != nil && *in.Base != e.DraftVersion {
		restErr(c, http.StatusConflict, "wb_kit_override_conflict", "The design's classes or variables were changed in another editor. Reload to see their changes.",
			map[string]any{"override": wbKitOverrideView(slug, e)})
		return
	}
	if webbuilder.KitOverrideCSS(e.Draft) != webbuilder.KitOverrideCSS(&e.Live) {
		e.Live = *e.Draft
		e.Version++
		e.Updated = wbTime(time.Now())
	}
	e.Draft = nil
	if err := s.wbSaveKitOverrides(c, all); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not publish the kit override.")
		return
	}
	s.purgeAll(c)
	c.JSON(http.StatusOK, wbKitOverrideView(slug, e))
}

// wbKitOverrideLive is slug → the live override's CSS of every kit that has one (cached until an
// override changes).
func (s *Server) wbKitOverrideLive() map[string]string {
	if p, ok := wbKitOverrideCache.Load("css"); ok {
		if m, _ := p.(*map[string]string); m != nil {
			return *m
		}
	}
	m := map[string]string{}
	for slug, e := range s.wbKitOverrides() {
		if css := webbuilder.KitOverrideCSS(&e.Live); css != "" {
			m[slug] = css
		}
	}
	wbKitOverrideCache.Store("css", &m)
	return m
}

// wbKitOverrideStyles is what follows a code head: one <style data-wb-kit-override> per kit
// whose files the head loads and that has a live override ("" without any).
func (s *Server) wbKitOverrideStyles(head string) string {
	if !strings.Contains(head, wbKitURLPrefix) {
		return ""
	}
	live := s.wbKitOverrideLive()
	if len(live) == 0 {
		return ""
	}
	var slugs []string
	seen := map[string]bool{}
	for _, m := range wbKitRefRE.FindAllStringSubmatch(head, -1) {
		if slug := m[2]; !seen[slug] && live[slug] != "" {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	var b strings.Builder
	for _, slug := range slugs {
		b.WriteString(`<style data-wb-kit-override="` + html.EscapeString(slug) + `">` + "\n" + live[slug] + "</style>\n")
	}
	return b.String()
}

// wbRESTKitPages is GET kits/{slug}/pages: the code-section markup of every builder page and
// template whose code sections use the kit (published docs; trashed posts left out), for the
// element editor's "From this design" palette and its class counts. At most 500 pages and 8 MB.
func (s *Server) wbRESTKitPages(c *gin.Context, r *restRequest) {
	if !wbKitUser(c, false) {
		return
	}
	slug := r.Params["slug"]
	if !s.wbKitExists(c, slug) {
		return
	}
	rows, err := s.store.Pool.Query(c, `SELECT d.post_id, p.title, p.type, d.doc FROM web_builder_docs d JOIN posts p ON p.id = d.post_id
		WHERE p.status <> 'trash' AND d.doc::text LIKE '%' || $1 || '%' ORDER BY d.post_id LIMIT 2000`, `"kit": "`+slug+`"`)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the pages.")
		return
	}
	defer rows.Close()
	type page struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Type  string `json:"type"`
		HTML  string `json:"html"`
	}
	out := []page{}
	total := 0
	for rows.Next() {
		var p page
		var doc []byte
		if err := rows.Scan(&p.ID, &p.Title, &p.Type, &doc); err != nil {
			continue
		}
		list, _ := webbuilder.CodeSections(doc)
		var b strings.Builder
		for _, sec := range list {
			if sec.Kit == slug {
				b.WriteString(sec.HTML)
				b.WriteByte('\n')
			}
		}
		if b.Len() == 0 {
			continue
		}
		if total+b.Len() > 8<<20 || len(out) >= 500 {
			break
		}
		total += b.Len()
		p.HTML = b.String()
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{"slug": slug, "pages": out})
}

// wbKitOverrideDrop removes a deleted kit's override.
func (s *Server) wbKitOverrideDrop(c *gin.Context, slug string) {
	wbKitOverrideMu.Lock()
	defer wbKitOverrideMu.Unlock()
	all := s.wbKitOverrides()
	if _, ok := all[slug]; !ok {
		return
	}
	delete(all, slug)
	_ = s.wbSaveKitOverrides(c, all)
}
