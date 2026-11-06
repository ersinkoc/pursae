package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Theme library of Pursae Web Builder (docs/developer/web-builder-theme-library.md): the library
// themes (the theme market's packages, theme_library.go) that carry Theme Builder parts, listed in
// the editor's Items panel and imported into the site in one go.
//
// A package opts in with the manifest key "web_builder": "web-builder/parts.json", a file with the
// theme's templates (header, footer, front page, single, archive, search, 404, each a builder doc
// with display conditions) and its demo pages. Menus and images come from the package's demo file
// (demo/demo.json), which the theme market uses too: the same themes, the same media and menus.
//
// An import is a short conversation between the editor and the server, because the front-end
// snapshot of a doc is drawn in the browser (contract §4):
//
//  1. POST themes/{slug}/import creates the templates and pages as drafts with their docs (no
//     snapshot yet), the demo images and nothing else, and answers with the ids and revisions;
//  2. the editor draws every doc and publishes it through the ordinary publish route;
//  3. POST themes/{slug}/finish disables the superseded templates ("replace") and creates the demo
//     menus. Until then nothing of the theme's is live, and a run that stops half-way leaves
//     drafts the next import reuses.
//
// Everything the import creates carries the post meta _wb_theme (the slug) and _wb_theme_key (the
// part), which is how a second import finds its own work instead of creating copies. Nothing else
// on the site is changed: other templates are disabled only on "replace", never deleted.

const (
	wbThemeMetaSlug = "_wb_theme"     // the library theme a template, page or image came from
	wbThemeMetaKey  = "_wb_theme_key" // the part inside the theme
	wbThemeFormat   = "web-builder-theme"

	wbThemeModeReplace = "replace" // the theme's templates go live, the ones they replace are disabled
	wbThemeModeKeep    = "keep"    // the theme's templates are saved as drafts, nothing changes on the site

	wbThemeMaxPack = 2 << 20
)

// wbThemePack is web-builder/parts.json.
type wbThemePack struct {
	Format    string           `json:"format"`
	Version   int              `json:"version"`
	Templates []wbPackTemplate `json:"templates"`
	Pages     []wbPackPage     `json:"pages"`
}

type wbPackTemplate struct {
	Key        string          `json:"key"`
	Type       string          `json:"type"`
	Title      string          `json:"title"`
	Conditions []string        `json:"conditions"`
	Priority   int             `json:"priority"`
	Doc        json.RawMessage `json:"doc"`
}

type wbPackPage struct {
	Key   string          `json:"key"`
	Slug  string          `json:"slug"`
	Title string          `json:"title"`
	Doc   json.RawMessage `json:"doc"`
	Page  json.RawMessage `json:"page"`
}

// wbThemePackOf reads and checks a theme's parts file; nil, nil when the theme has none.
func (s *Server) wbThemePackOf(ctx context.Context, lt *libraryTheme) (*wbThemePack, fs.FS, error) {
	rel := lt.Manifest.WebBuilder
	if rel == "" {
		return nil, nil, nil
	}
	fsys, dir, err := s.libraryFiles(ctx, lt)
	if err != nil {
		return nil, nil, err
	}
	b, err := fs.ReadFile(fsys, path.Join(dir, rel))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %v", rel, err)
	}
	if len(b) > wbThemeMaxPack {
		return nil, nil, fmt.Errorf("%s is larger than %d MiB", rel, wbThemeMaxPack>>20)
	}
	var p wbThemePack
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, nil, fmt.Errorf("%s: %v", rel, err)
	}
	if err := p.validate(); err != nil {
		return nil, nil, fmt.Errorf("%s: %v", rel, err)
	}
	return &p, fsys, nil
}

func (p *wbThemePack) validate() error {
	if p.Format != wbThemeFormat || p.Version != 1 {
		return fmt.Errorf("format must be %q version 1", wbThemeFormat)
	}
	if len(p.Templates) == 0 || len(p.Templates) > 24 || len(p.Pages) > 24 {
		return errors.New("a theme has 1 to 24 templates and at most 24 pages")
	}
	seen := map[string]bool{}
	keyOK := func(kind, k string) bool {
		if k == "" || len(k) > 40 || seen[kind+k] {
			return false
		}
		for _, r := range k {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
		seen[kind+k] = true
		return true
	}
	for i, t := range p.Templates {
		if !keyOK("t:", t.Key) {
			return fmt.Errorf("templates[%d]: the key %q is empty, not a-z0-9- or used twice", i, t.Key)
		}
		if _, ok := webbuilder.TemplateTypeLabels[webbuilder.TemplateType(t.Type)]; !ok || webbuilder.TemplateType(t.Type) == webbuilder.TemplateSingle {
			return fmt.Errorf("templates[%d]: unknown type %q", i, t.Type)
		}
		if len(t.Doc) == 0 {
			return fmt.Errorf("templates[%d]: no doc", i)
		}
		if norm, errs := webbuilder.ValidateConditionsRequest(webbuilder.TemplateConditionsRequest{Conditions: t.Conditions, Priority: t.Priority}); len(errs) > 0 || len(norm.Conditions) == 0 {
			return fmt.Errorf("templates[%d]: the display conditions are not valid", i)
		}
	}
	for i, pg := range p.Pages {
		if !keyOK("p:", pg.Key) || pg.Slug == "" || pg.Title == "" || len(pg.Doc) == 0 {
			return fmt.Errorf("pages[%d]: needs a key (a-z0-9-, unique), a slug, a title and a doc", i)
		}
	}
	return nil
}

// wbThemeLibrary lists the library themes that carry builder parts.
func (s *Server) wbThemeLibrary(ctx context.Context) ([]libraryTheme, error) {
	all, err := s.libraryThemesErr(ctx)
	if err != nil {
		return nil, err
	}
	var out []libraryTheme
	for _, lt := range all {
		if lt.Manifest.WebBuilder != "" {
			out = append(out, lt)
		}
	}
	return out, nil
}

// wbRegisterThemeREST adds the theme library routes through reg.
func (s *Server) wbRegisterThemeREST(reg func(pattern string, methods map[string]restHandler, handshake bool)) {
	const th = `/web-builder/v2/themes/(?P<slug>[a-z0-9]+(?:[_-][a-z0-9]+)*)`
	reg(`/web-builder/v2/themes`, map[string]restHandler{http.MethodGet: s.wbRESTThemes}, true)
	reg(th+`/import`, map[string]restHandler{http.MethodPost: s.wbRESTThemeImport}, true)
	reg(th+`/finish`, map[string]restHandler{http.MethodPost: s.wbRESTThemeFinish}, true)
}

// wbThemeUser is the user of a theme library route: edit_theme_options (templates), and for the
// import also edit_pages and publish_pages (the demo pages).
func wbThemeUser(c *gin.Context, pages bool) *store.User {
	u := wbUser(c, webbuilder.TemplateCap)
	if u == nil {
		return nil
	}
	if pages && !(u.Can("edit_pages", nil) && u.Can("publish_pages", nil)) {
		wbForbidden(c, u)
		return nil
	}
	return u
}

// wbThemeCard is one theme of GET themes.
type wbThemeCard struct {
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Category    string            `json:"category,omitempty"`
	Style       string            `json:"style,omitempty"`
	Screenshot  string            `json:"screenshot"`
	Templates   []wbThemeCardPart `json:"templates"`
	Pages       []wbThemeCardPart `json:"pages"`
	Menus       int               `json:"menus"`
	Images      int               `json:"images"`
	Store       bool              `json:"store"`
	Imported    bool              `json:"imported"`
	Error       string            `json:"error,omitempty"`
}

type wbThemeCardPart struct {
	Key   string `json:"key"`
	Type  string `json:"type,omitempty"`
	Label string `json:"label"`
}

// wbThemesResponse is GET themes: the cards, and what the site has now (so the editor can ask
// before replacing): the number of published templates per type.
type wbThemesResponse struct {
	Themes   []wbThemeCard  `json:"themes"`
	Existing map[string]int `json:"existing"`
	Source   string         `json:"source"` // "" or why the library is not available
}

func (s *Server) wbRESTThemes(c *gin.Context, r *restRequest) {
	if wbThemeUser(c, false) == nil {
		return
	}
	resp := wbThemesResponse{Themes: []wbThemeCard{}, Existing: map[string]int{}}
	themes, err := s.wbThemeLibrary(c)
	if err != nil {
		resp.Source = "The theme library is not available: " + err.Error()
		if errors.Is(err, errLibraryOff) {
			resp.Source = "No theme library is configured for this site."
		}
	}
	for _, lt := range themes {
		m := lt.Manifest
		card := wbThemeCard{Slug: m.Slug, Name: m.Name, Description: m.Description, Version: m.Version, Category: themeCategoryLabel(m.Category),
			Style: m.Style, Screenshot: libraryScreenshotURL(m), Templates: []wbThemeCardPart{}, Pages: []wbThemeCardPart{},
			Store: m.Supports["woocommerce"] == true}
		if card.Screenshot != "" {
			card.Screenshot = s.links(c).HomeURL(card.Screenshot)
		}
		if pack, fsys, perr := s.wbThemePackOf(c, &lt); perr != nil {
			card.Error = perr.Error()
		} else if pack != nil {
			for _, t := range pack.Templates {
				card.Templates = append(card.Templates, wbThemeCardPart{Key: t.Key, Type: t.Type, Label: webbuilder.TemplateTypeLabels[webbuilder.TemplateType(t.Type)]})
			}
			for _, p := range pack.Pages {
				card.Pages = append(card.Pages, wbThemeCardPart{Key: p.Key, Label: p.Title})
			}
			if d := wbThemeDemo(fsys, &lt, m); d != nil {
				card.Menus, card.Images = len(d.Menus), len(d.Media)
			}
		}
		n, _ := s.wbThemeOwned(c, m.Slug)
		card.Imported = n > 0
		resp.Themes = append(resp.Themes, card)
	}
	all, terr := s.store.WebBuilder().Templates(c, store.WBTemplateFilter{Statuses: []string{webbuilder.StatusPublish}})
	if terr == nil {
		for _, t := range all {
			resp.Existing[string(t.Type)]++
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// wbThemeDemo reads the theme's demo file (nil when it has none or it cannot be read; the demo
// file is validated when the theme is installed, here only menus and media are used).
func wbThemeDemo(fsys fs.FS, lt *libraryTheme, m *themeManifest) *themeDemo {
	if m.Demo == "" || fsys == nil {
		return nil
	}
	dir := lt.Dir
	if lt.Item != nil {
		dir = m.Version
	}
	b, err := fs.ReadFile(fsys, path.Join(dir, m.Demo))
	if err != nil {
		return nil
	}
	var d themeDemo
	if json.Unmarshal(b, &d) != nil {
		return nil
	}
	return &d
}

// wbThemeOwned counts the theme's non-trashed templates and pages of the slug.
func (s *Server) wbThemeOwned(ctx context.Context, slug string) (int, error) {
	var n int
	err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE meta->>$1 = $2 AND status NOT IN ('trash','auto-draft')
		AND type IN ('`+webbuilder.TemplatePostType+`','page')`, wbThemeMetaSlug, slug).Scan(&n)
	return n, err
}

// wbThemeOwnedPost finds the non-trashed post of a type the theme's import made for a part.
func (s *Server) wbThemeOwnedPost(ctx context.Context, typ, slug, key string, draftOnly bool) int64 {
	q := `SELECT id FROM posts WHERE type = $1 AND meta->>$2 = $3 AND meta->>$4 = $5 AND status NOT IN ('trash','auto-draft')`
	if draftOnly {
		q += ` AND status = 'draft'`
	}
	var id int64
	_ = s.store.Pool.QueryRow(ctx, q+` ORDER BY id DESC LIMIT 1`, typ, wbThemeMetaSlug, slug, wbThemeMetaKey, key).Scan(&id)
	return id
}

type wbThemeImportRequest struct {
	Mode  string `json:"mode"`
	Pages *bool  `json:"pages"`
}

type wbThemeImported struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`
	Type  string `json:"type,omitempty"`
	Title string `json:"title"`
	Rev   int    `json:"rev"`
}

type wbThemeImportResponse struct {
	Mode      string            `json:"mode"`
	Templates []wbThemeImported `json:"templates"`
	Pages     []wbThemeImported `json:"pages"`
	Media     int               `json:"media"`
}

var errWBThemeNoPack = errors.New("this theme has no Pursae Web Builder parts")

// wbThemeFind returns the library entry and its pack for a route, answering the error itself.
func (s *Server) wbThemeFind(c *gin.Context, slug string) (*libraryTheme, *wbThemePack, fs.FS) {
	themes, err := s.wbThemeLibrary(c)
	if err != nil {
		restErr(c, http.StatusServiceUnavailable, "wb_theme_library", "The theme library is not available: "+err.Error())
		return nil, nil, nil
	}
	for i := range themes {
		if themes[i].Manifest.Slug != slug {
			continue
		}
		pack, fsys, perr := s.wbThemePackOf(c, &themes[i])
		if perr != nil {
			restErr(c, http.StatusUnprocessableEntity, "wb_theme_invalid", "The theme cannot be imported: "+perr.Error())
			return nil, nil, nil
		}
		if pack == nil {
			restErr(c, http.StatusUnprocessableEntity, "wb_theme_invalid", errWBThemeNoPack.Error()+".")
			return nil, nil, nil
		}
		return &themes[i], pack, fsys
	}
	restErr(c, http.StatusNotFound, "wb_theme_not_found", "That theme is not in the library.")
	return nil, nil, nil
}

// wbThemeMedia imports the theme's demo images (once: a second import finds them by their meta)
// and returns the token map {media:id} → URL.
func (s *Server) wbThemeMedia(ctx context.Context, uid int64, lt *libraryTheme, fsys fs.FS, d *themeDemo) (map[string]string, int, error) {
	urls := map[string]string{}
	if d == nil {
		return urls, 0, nil
	}
	dir := lt.Dir
	if lt.Item != nil {
		dir = lt.Manifest.Version
	}
	slug := lt.Manifest.Slug
	made := 0
	for _, md := range d.Media {
		var id int64
		_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE meta->>$1 = $2 AND meta->>$3 = $4 ORDER BY id LIMIT 1`,
			wbThemeMetaSlug, slug, wbThemeMetaKey, md.ID).Scan(&id)
		if id == 0 { // the theme market's demo import made it already: reuse its file
			id = s.demoItemID(ctx, "media", "", slug, md.ID)
		}
		if id > 0 {
			if a, err := s.store.GetAttachment(ctx, id); err == nil {
				urls[md.ID] = s.uploadURL(ctx, a.FilePath)
				continue
			}
		}
		b, err := fs.ReadFile(fsys, path.Join(dir, md.File))
		if err != nil {
			return nil, made, fmt.Errorf("%s: %v", md.File, err)
		}
		name := path.Base(md.File)
		if !strings.HasPrefix(name, slug+"-") {
			name = slug + "-" + name
		}
		a, err := s.insertAttachmentFile(ctx, name, bytes.NewReader(b), uid, 0)
		if err != nil {
			return nil, made, fmt.Errorf("%s: %v", md.File, err)
		}
		if md.Title != "" {
			a.Title = md.Title
		}
		a.Alt, a.Caption = md.Alt, md.Caption
		a.Meta[wbThemeMetaSlug], a.Meta[wbThemeMetaKey] = slug, md.ID
		if err := s.store.UpdateAttachment(ctx, a); err != nil {
			return nil, made, err
		}
		urls[md.ID] = s.uploadURL(ctx, a.FilePath)
		made++
	}
	return urls, made, nil
}

// wbThemeTokens replaces {{media:id}} in a doc.
func wbThemeTokens(doc json.RawMessage, urls map[string]string) json.RawMessage {
	s := string(doc)
	for id, u := range urls {
		jsonURL, _ := json.Marshal(u)
		s = strings.ReplaceAll(s, "{{media:"+id+"}}", strings.Trim(string(jsonURL), `"`))
	}
	return json.RawMessage(s)
}

// wbRESTThemeImport is POST themes/{slug}/import {mode, pages}.
func (s *Server) wbRESTThemeImport(c *gin.Context, r *restRequest) {
	raw, ok := wbBody(c, r, 4<<10)
	if !ok {
		return
	}
	var body wbThemeImportRequest
	if !wbDecode(c, raw, &body) {
		return
	}
	if body.Mode == "" {
		body.Mode = wbThemeModeReplace
	}
	if body.Mode != wbThemeModeReplace && body.Mode != wbThemeModeKeep {
		restErr(c, http.StatusUnprocessableEntity, "wb_theme_invalid", "The mode is replace or keep.")
		return
	}
	withPages := body.Pages == nil || *body.Pages
	u := wbThemeUser(c, withPages)
	if u == nil {
		return
	}
	lt, pack, fsys := s.wbThemeFind(c, r.Params["slug"])
	if lt == nil {
		return
	}
	slug := lt.Manifest.Slug
	demo := wbThemeDemo(fsys, lt, lt.Manifest)
	urls, nMedia, err := s.wbThemeMedia(c, u.ID, lt, fsys, demo)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "wb_theme_media", "The theme's images could not be imported: "+err.Error())
		return
	}
	wb := s.store.WebBuilder()
	unfiltered := u.Can("unfiltered_html", nil)
	var created []int64 // posts made by this call, trashed again when it fails
	fail := func(msg string, status int) {
		for _, id := range created {
			_ = s.store.TrashPost(c, id)
		}
		restErr(c, status, "wb_theme_invalid", msg)
	}
	saveDoc := func(id int64, key string, docJSON json.RawMessage, page json.RawMessage) (int, string) {
		docJSON, _, _, _ = wbRegenerateSectionIDs(wbThemeTokens(docJSON, urls), "", "")
		res, perr := webbuilder.ParseDoc(docJSON, webbuilder.DocOptions{UnfilteredHTML: unfiltered, PathPrefix: "/doc"})
		if perr != nil || !res.Valid() {
			msg := "the design is not valid"
			if perr == nil && len(res.Errors) > 0 {
				msg = res.Errors[0].Path + ": " + res.Errors[0].Message
			}
			return 0, key + ": " + msg
		}
		baseRev, _ := wb.DocRev(c, id)
		d := &store.WebBuilderDoc{PostID: id, Doc: res.JSON, Page: page, Renderer: 0, SnapshotUser: u.ID, SnapshotUnfiltered: unfiltered}
		if err := wb.PublishDoc(c, d, baseRev); err != nil {
			return 0, key + ": the design could not be saved"
		}
		return d.Rev, ""
	}
	mark := func(id int64, key string) error {
		_, err := s.store.Pool.Exec(c, `UPDATE posts SET meta = meta || jsonb_build_object($2::text, $3::text, $4::text, $5::text, $6::text, '1') WHERE id = $1`,
			id, wbThemeMetaSlug, slug, wbThemeMetaKey, key, webbuilder.PostMarkerMeta)
		return err
	}

	resp := wbThemeImportResponse{Mode: body.Mode, Templates: []wbThemeImported{}, Pages: []wbThemeImported{}, Media: nMedia}
	for _, t := range pack.Templates {
		norm, _ := webbuilder.ValidateConditionsRequest(webbuilder.TemplateConditionsRequest{Conditions: t.Conditions, Priority: t.Priority})
		title := wbClip(lt.Manifest.Name+" · "+t.Title, wbTitleMax)
		// "replace" reuses the theme's earlier template of the part (whatever its status), "keep"
		// only an earlier draft, so a kept copy never overwrites a template the owner edited.
		id := s.wbThemeOwnedPost(c, webbuilder.TemplatePostType, slug, t.Key, body.Mode == wbThemeModeKeep)
		if id == 0 {
			nt, cerr := wb.CreateTemplate(c, store.NewWBTemplate{Type: webbuilder.TemplateType(t.Type), Title: title, Conditions: norm.Conditions,
				Priority: norm.Priority, AuthorID: u.ID})
			if cerr != nil {
				fail("The template could not be created.", http.StatusInternalServerError)
				return
			}
			id = nt.ID
			created = append(created, id)
		} else {
			if err := wb.SetTemplateConditions(c, id, norm.Conditions, norm.Priority); err != nil {
				fail("The template could not be updated.", http.StatusInternalServerError)
				return
			}
			_, _ = s.store.Pool.Exec(c, `UPDATE posts SET title = $2 WHERE id = $1`, id, title)
		}
		if err := mark(id, t.Key); err != nil {
			fail("The template could not be marked.", http.StatusInternalServerError)
			return
		}
		rev, msg := saveDoc(id, "templates/"+t.Key, t.Doc, json.RawMessage(`{}`))
		if msg != "" {
			fail("The theme cannot be imported: "+msg+".", http.StatusUnprocessableEntity)
			return
		}
		resp.Templates = append(resp.Templates, wbThemeImported{ID: id, Key: t.Key, Type: t.Type, Title: title, Rev: rev})
	}
	if withPages {
		for _, pg := range pack.Pages {
			id := s.wbThemeOwnedPost(c, "page", slug, pg.Key, false)
			if id == 0 {
				p := &store.Post{Type: "page", Title: pg.Title, Slug: pg.Slug, Status: "draft", AuthorID: u.ID, Meta: map[string]any{}}
				if err := s.store.InsertPost(c, p); err != nil {
					fail("A page could not be created.", http.StatusInternalServerError)
					return
				}
				id = p.ID
				created = append(created, id)
			}
			if err := mark(id, pg.Key); err != nil {
				fail("A page could not be marked.", http.StatusInternalServerError)
				return
			}
			page := pg.Page
			if len(page) == 0 {
				page = json.RawMessage(`{}`)
			}
			rev, msg := saveDoc(id, "pages/"+pg.Key, pg.Doc, page)
			if msg != "" {
				fail("The theme cannot be imported: "+msg+".", http.StatusUnprocessableEntity)
				return
			}
			resp.Pages = append(resp.Pages, wbThemeImported{ID: id, Key: pg.Key, Title: pg.Title, Rev: rev})
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

type wbThemeFinishRequest struct {
	Mode      string  `json:"mode"`
	Templates []int64 `json:"templates"` // the ids the import answered, published by the editor
	Pages     []int64 `json:"pages"`
	Menus     *bool   `json:"menus"`
}

type wbThemeFinishResponse struct {
	Disabled []string `json:"disabled"` // titles of the templates the import replaced
	Menus    int      `json:"menus"`
	Pages    []string `json:"pages"` // links of the published demo pages, by key
}

// wbRESTThemeFinish is POST themes/{slug}/finish: the second half of an import.
func (s *Server) wbRESTThemeFinish(c *gin.Context, r *restRequest) {
	if wbThemeUser(c, false) == nil {
		return
	}
	raw, ok := wbBody(c, r, 16<<10)
	if !ok {
		return
	}
	var body wbThemeFinishRequest
	if !wbDecode(c, raw, &body) {
		return
	}
	lt, pack, fsys := s.wbThemeFind(c, r.Params["slug"])
	if lt == nil {
		return
	}
	slug := lt.Manifest.Slug
	resp := wbThemeFinishResponse{Disabled: []string{}, Pages: []string{}}
	// Only what this import made counts: the ids must be the theme's own.
	own := func(ids []int64, typ string) map[string]int64 {
		out := map[string]int64{}
		for _, id := range ids {
			var key string
			var status string
			err := s.store.Pool.QueryRow(c, `SELECT coalesce(meta->>$2,''), status FROM posts WHERE id = $1 AND type = $3 AND meta->>$4 = $5`,
				id, wbThemeMetaKey, typ, wbThemeMetaSlug, slug).Scan(&key, &status)
			if err == nil && key != "" && status == "publish" {
				out[key] = id
			}
		}
		return out
	}
	tpls := own(body.Templates, webbuilder.TemplatePostType)
	pages := own(body.Pages, "page")

	if body.Mode == wbThemeModeReplace && len(tpls) > 0 {
		types := map[string]bool{}
		for _, t := range pack.Templates {
			if tpls[t.Key] != 0 {
				types[t.Type] = true
			}
		}
		keep := make([]int64, 0, len(tpls))
		for _, id := range tpls {
			keep = append(keep, id)
		}
		all, err := s.store.WebBuilder().Templates(c, store.WBTemplateFilter{Statuses: []string{webbuilder.StatusPublish}})
		if err != nil {
			restErr(c, http.StatusInternalServerError, "db_error", "Could not read the templates.")
			return
		}
		for _, t := range all {
			if !types[string(t.Type)] || slices.Contains(keep, t.ID) {
				continue
			}
			if _, err := s.store.Pool.Exec(c, `UPDATE posts SET status = 'draft', modified_at = now(), updated_at = now() WHERE id = $1 AND type = $2`,
				t.ID, webbuilder.TemplatePostType); err == nil {
				resp.Disabled = append(resp.Disabled, t.Title)
			}
		}
	}
	if len(tpls) > 0 {
		s.wbTemplatesChanged(c)
	}
	s.wbPurgePages()

	if (body.Menus == nil || *body.Menus) && body.Mode != wbThemeModeKeep {
		n, err := s.wbThemeMenus(c, lt, fsys, pages)
		if err != nil {
			restErr(c, http.StatusInternalServerError, "wb_theme_menus", "The menus could not be created: "+err.Error())
			return
		}
		resp.Menus = n
	}
	for key, id := range pages {
		if p, err := s.store.GetPost(c, id); err == nil {
			resp.Pages = append(resp.Pages, key+"="+s.links(c).Permalink(p))
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

// wbThemeMenus creates the demo menus of the theme (named after the demo's menus, reused when a menu
// of that name exists) and assigns them to the locations of the active theme. Items that point at
// a demo page the import did not make are left out.
func (s *Server) wbThemeMenus(ctx context.Context, lt *libraryTheme, fsys fs.FS, pages map[string]int64) (int, error) {
	d := wbThemeDemo(fsys, lt, lt.Manifest)
	if d == nil || len(d.Menus) == 0 {
		return 0, nil
	}
	registered := map[string]bool{}
	for _, l := range s.activeTheme().MenuLocations {
		registered[l.Slug] = true
	}
	existing, err := s.store.Menus(ctx)
	if err != nil {
		return 0, err
	}
	made := 0
	for _, mn := range d.Menus {
		if mn.Location == "" || !registered[mn.Location] {
			continue
		}
		var id int64
		for _, m := range existing {
			if m.Name == mn.Name {
				id = m.ID
			}
		}
		if id == 0 {
			if id, err = s.store.CreateMenu(ctx, mn.Name); err != nil {
				return made, err
			}
			order := 0
			var add func(list []demoMenuItem, parent int64) error
			add = func(list []demoMenuItem, parent int64) error {
				for _, it := range list {
					mi := &store.EditItem{MenuID: id, ParentID: parent, Label: it.Title, Order: order + 1, Status: "publish"}
					switch {
					case it.Page != "":
						pid := pages[it.Page]
						if pid == 0 {
							continue
						}
						mi.Kind, mi.Object, mi.TargetID = "post_type", "page", pid
					case it.URL != "":
						mi.Kind, mi.Object, mi.URL = "custom", "custom", it.URL
					default:
						continue
					}
					order++
					if err := s.store.AddMenuItem(ctx, mi); err != nil {
						return err
					}
					if err := add(it.Children, mi.ID); err != nil {
						return err
					}
				}
				return nil
			}
			if err := add(mn.Items, 0); err != nil {
				return made, err
			}
			made++
		}
		if err := s.store.SetMenuLocations(ctx, map[string]int64{mn.Location: id}); err != nil {
			return made, err
		}
	}
	return made, nil
}
