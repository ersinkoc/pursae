package server

import (
	"context"
	"html"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Appearance › Theme Builder: the site's theme parts (the working copy of Hello GoWebEditor)
// grouped by type, with Add New (opens the editor), conditions, delete, "Save as theme", and the
// list of saved GoWebEditor themes with activate, preview, export, import and delete.

const gweThemeBuilderPath = "/wp-admin/gowebeditor-theme-builder"

type gweTBRow struct {
	ID                                   int64
	Title, Status, StatusLabel, Modified string
	EditURL, ConditionsURL, PreviewURL   string
	Conditions                           []string
	Author                               string
}

type gweTBGroup struct {
	Type, Label, Plural string
	Rows                []gweTBRow
}

type gweTBTheme struct {
	Slug, Name, Description, Screenshot string
	Loaded                              bool // the working copy was loaded from this snapshot
	Parts                               int
	Saved                               string
	ExportURL, PreviewURL, ActivateURL  string
}

type gweTBData struct {
	ActiveName  string
	Hello       bool // Hello GoWebEditor is the active theme
	Groups      []gweTBGroup
	Total       int
	Themes      []gweTBTheme
	Nonce       string
	ShowSave    bool
	DefaultName string
	// conditions screen
	Part      *gweTBRow
	PartType  string
	Rows      []gweCondRow
	RuleOpts  []gweRuleGroup
	BackURL   string
	SingleOpt bool
}

type gweCondRow struct {
	Mode, Rule string
}

type gweRuleOpt struct {
	Value, Label string
}

type gweRuleGroup struct {
	Label string
	Opts  []gweRuleOpt
}

var gweRuleLabels = map[string]string{
	"general": "Entire Site", "singular": "All Singular", "singular/front_page": "Front Page", "singular/post": "All Posts",
	"singular/page": "All Pages", "singular/not_found404": "404 Page", "archive": "All Archives", "archive/post_archive": "Blog (Posts Page)",
	"archive/category": "All Categories", "archive/post_tag": "All Tags", "archive/author": "Author Archives",
	"archive/date": "Date Archives", "archive/search": "Search Results",
}

// gweConditionLabel turns "include/singular/page/12" into "Page: About".
func (s *Server) gweConditionLabel(c *gin.Context, cond string) string {
	mode, rule, _ := strings.Cut(cond, "/")
	label := gweRuleLabels[rule]
	if label == "" {
		parts := strings.Split(rule, "/")
		if len(parts) == 3 {
			id, _ := strconv.ParseInt(parts[2], 10, 64)
			switch parts[0] + "/" + parts[1] {
			case "singular/post", "singular/page":
				name := "#" + parts[2]
				if p, err := s.store.GetPost(c, id); err == nil {
					name = defaultStr(p.Title, "(no title)")
				}
				label = map[string]string{"post": "Post: ", "page": "Page: "}[parts[1]] + name
			case "archive/category", "archive/post_tag":
				name := "#" + parts[2]
				if t, err := s.store.GetTerm(c, id); err == nil {
					name = t.Name
				}
				label = map[string]string{"category": "Category: ", "post_tag": "Tag: "}[parts[1]] + name
			}
		}
		if label == "" {
			label = rule
		}
	}
	if mode == "exclude" {
		return "Exclude: " + label
	}
	return label
}

func (s *Server) gweTBRowFor(c *gin.Context, p *store.Post) gweTBRow {
	r := gweTBRow{ID: p.ID, Title: defaultStr(p.Title, "(no title)"), Status: p.Status, StatusLabel: gweStatusLabels[p.Status],
		Modified: wp.FormatDate(p.ModifiedAt.In(s.loc()), "Y/m/d \\a\\t g:i a"), EditURL: gweEditURL(p.ID),
		ConditionsURL: gweThemeBuilderPath + "?action=conditions&post=" + itoa64(p.ID)}
	if u, err := s.store.UserByID(c, p.AuthorID); err == nil {
		r.Author = u.DisplayName
	}
	for _, cond := range gweConditionsOf(p) {
		r.Conditions = append(r.Conditions, s.gweConditionLabel(c, cond))
	}
	return r
}

// gweThemeBuilder is the Theme Builder screen.
func (s *Server) gweThemeBuilder(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_theme_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if c.Request.Method == http.MethodPost {
		s.gweThemeBuilderPost(c)
		return
	}
	switch c.Query("action") {
	case "conditions":
		s.gweConditionsScreen(c)
		return
	case "export":
		if !s.checkNonce(c, "gwe-theme-builder") {
			s.denyNonce(c)
			return
		}
		slug := c.Query("theme")
		b, err := s.gweExportTheme(c, slug)
		if err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.Header("Content-Disposition", `attachment; filename="`+slug+`.zip"`)
		c.Data(http.StatusOK, "application/zip", b)
		return
	}
	active := s.activeTheme()
	d := gweTBData{ActiveName: active.Name, Hello: active.Slug == helloThemeSlug, Nonce: s.nonce(c, "gwe-theme-builder"),
		ShowSave: c.Query("action") == "save-theme"}
	d.DefaultName = s.settings.String("blogname") + " Theme"
	posts, err := s.gwePartPosts(c, "")
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	gweSortParts(posts)
	byType := map[string][]gweTBRow{}
	for _, p := range posts {
		byType[gweDocType(p)] = append(byType[gweDocType(p)], s.gweTBRowFor(c, p))
	}
	for _, t := range gwePartTypes {
		d.Groups = append(d.Groups, gweTBGroup{Type: t.Type, Label: t.Label, Plural: t.Plural, Rows: byType[t.Type]})
		d.Total += len(byType[t.Type])
	}
	d.Themes = s.gweSavedThemeCards(c)
	var notices []notice
	for key, msg := range map[string]string{"saved": "Theme saved. It is listed under <a href=\"/wp-admin/themes\">Appearance › Themes</a>.",
		"activated": "Theme saved; Hello GoWebEditor is now the active theme. <a href=\"/\">Visit site</a>", "deleted": "Theme part deleted.",
		"conditions": "Display conditions saved.", "imported": "Theme imported. It is listed under <a href=\"/wp-admin/themes\">Appearance › Themes</a>.",
		"theme-deleted": "Theme deleted."} {
		if c.Query("message") == key {
			notices = append(notices, notice{Type: "success", Message: template.HTML(msg), Dismissible: true})
		}
	}
	if e := c.Query("error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(html.EscapeString(e)), Dismissible: true})
	}
	s.enqueueMedia(c, 0)
	s.adminPage(c, "gowebeditor-theme-builder", "Theme Builder", "appearance_page_gowebeditor-theme-builder", "admin/gowebeditor-theme-builder", d, notices...)
}

func (s *Server) gweTBRedirect(c *gin.Context, q string) {
	c.Redirect(http.StatusFound, gweThemeBuilderPath+q)
}

func (s *Server) gweThemeBuilderPost(c *gin.Context) {
	if !s.checkNonce(c, "gwe-theme-builder") {
		s.denyNonce(c)
		return
	}
	u := currentUser(c)
	fail := func(msg string) { s.gweTBRedirect(c, "?error="+url.QueryEscape(msg)) }
	switch c.PostForm("action") {
	case "new":
		typ := c.PostForm("type")
		if gwePartLabel(typ) == "" {
			fail("Choose a template type.")
			return
		}
		if err := s.gweEnsurePartType(c); err != nil {
			fail(err.Error())
			return
		}
		title := strings.TrimSpace(c.PostForm("title"))
		if title == "" {
			title = gwePartLabel(typ)
		}
		conds := []any{}
		for _, cd := range gweDefaultConditions[typ] {
			conds = append(conds, cd)
		}
		p := &store.Post{Type: gwePartType, Title: title, Status: "draft", AuthorID: u.ID, CommentStatus: "closed", PingStatus: "closed",
			Meta: map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": typ, "_elementor_version": gweVersion,
				"_elementor_data": "[]", "_gwe_conditions": conds, "_gwe_theme": ""}}
		if err := s.store.InsertPost(c, p); err != nil {
			fail(err.Error())
			return
		}
		c.Redirect(http.StatusFound, gweEditURL(p.ID))
	case "delete":
		p, err := s.store.GetPost(c, gweInt(c.PostForm("post")))
		if err != nil || p.Type != gwePartType || !u.Can("delete_post", p) {
			fail("That theme part cannot be deleted.")
			return
		}
		if err := s.store.DeletePost(c, p.ID); err != nil {
			fail(err.Error())
			return
		}
		s.gweDropCSS(p.ID)
		s.purgeAll(c)
		s.gweTBRedirect(c, "?message=deleted")
	case "conditions":
		p, err := s.store.GetPost(c, gweInt(c.PostForm("post")))
		if err != nil || p.Type != gwePartType || !u.Can("edit_post", p) {
			fail("That theme part cannot be edited.")
			return
		}
		modes, rules := c.PostFormArray("mode[]"), c.PostFormArray("rule[]")
		conds := []any{}
		seen := map[string]bool{}
		for i, r := range rules {
			mode := "include"
			if i < len(modes) && modes[i] == "exclude" {
				mode = "exclude"
			}
			if r == "" || !gweValidRule(r) {
				continue
			}
			cd := mode + "/" + r
			if !seen[cd] {
				seen[cd] = true
				conds = append(conds, cd)
			}
		}
		p.Meta["_gwe_conditions"] = conds
		if err := s.store.UpdatePost(c, p, u.ID); err != nil {
			fail(err.Error())
			return
		}
		s.purgeAll(c)
		s.gweTBRedirect(c, "?message=conditions")
	case "save-theme":
		if !u.Can("switch_themes", nil) {
			fail("Sorry, you are not allowed to add themes.")
			return
		}
		in := gweSaveThemeInput{Name: c.PostForm("name"), Description: strings.TrimSpace(c.PostForm("description")),
			ScreenshotID: gweInt(c.PostForm("screenshot_id")), Activate: c.PostForm("activate") == "1"}
		if in.ScreenshotID > 0 {
			if a, err := s.store.GetAttachment(c, in.ScreenshotID); err != nil || !strings.HasPrefix(a.Mime, "image/") {
				in.ScreenshotID = 0
			}
		}
		if _, err := s.gweSaveAsTheme(c, in); err != nil {
			fail(err.Error())
			return
		}
		if in.Activate {
			s.gweTBRedirect(c, "?message=activated")
			return
		}
		s.gweTBRedirect(c, "?message=saved")
	case "import":
		if !u.Can("switch_themes", nil) || !u.Can("upload_files", nil) {
			fail("Sorry, you are not allowed to import themes.")
			return
		}
		fh, err := c.FormFile("theme_zip")
		if err != nil {
			fail("Choose a theme .zip file to import.")
			return
		}
		f, err := fh.Open()
		if err != nil {
			fail(err.Error())
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, int64(maxUploadBytes)+1))
		f.Close()
		if err != nil || len(data) > maxUploadBytes {
			fail("The theme file is too large.")
			return
		}
		if _, err := s.gweImportTheme(c, data, c.PostForm("activate") == "1"); err != nil {
			fail(err.Error())
			return
		}
		s.gweTBRedirect(c, "?message=imported")
	case "delete-theme":
		if !u.Can("delete_themes", nil) && !u.Can("switch_themes", nil) {
			fail("Sorry, you are not allowed to delete themes.")
			return
		}
		if err := s.gweDeleteTheme(c, c.PostForm("theme")); err != nil {
			fail(err.Error())
			return
		}
		s.purgeAll(c)
		if c.PostForm("back") == "themes" { // the Themes screen's saved-themes section
			c.Redirect(http.StatusFound, "/wp-admin/themes?gwe=theme-deleted")
			return
		}
		s.gweTBRedirect(c, "?message=theme-deleted")
	default:
		fail("Unknown action.")
	}
}

// gweEnsurePartType registers the theme part content type (the migration does it too).
func (s *Server) gweEnsurePartType(ctx context.Context) error {
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO content_types (slug, name, hierarchical, public, rest_base, is_builtin, supports, labels)
		VALUES ($1, 'Theme Parts', false, false, '', false, '{"title":true,"editor":true,"author":true,"revisions":true}',
		'{"name":"Theme Parts","singular_name":"Theme Part"}') ON CONFLICT (slug) DO NOTHING`, gwePartType)
	return err
}

// gweValidRule checks a condition rule (without include/exclude).
func gweValidRule(r string) bool {
	if _, ok := gweRuleLabels[r]; ok {
		return true
	}
	parts := strings.Split(r, "/")
	if len(parts) != 3 {
		return false
	}
	if _, err := strconv.ParseInt(parts[2], 10, 64); err != nil {
		return false
	}
	switch parts[0] + "/" + parts[1] {
	case "singular/post", "singular/page", "archive/category", "archive/post_tag", "archive/author":
		return true
	}
	return false
}

// gweConditionsScreen edits a part's display conditions.
func (s *Server) gweConditionsScreen(c *gin.Context) {
	p, err := s.store.GetPost(c, gweInt(c.Query("post")))
	if err != nil || p.Type != gwePartType || !currentUser(c).Can("edit_post", p) {
		c.String(http.StatusNotFound, "Theme part not found.")
		return
	}
	row := s.gweTBRowFor(c, p)
	d := gweTBData{Part: &row, PartType: gwePartLabel(gweDocType(p)), Nonce: s.nonce(c, "gwe-theme-builder"), BackURL: gweThemeBuilderPath}
	for _, cd := range gweConditionsOf(p) {
		mode, rule, _ := strings.Cut(cd, "/")
		d.Rows = append(d.Rows, gweCondRow{Mode: mode, Rule: rule})
	}
	if len(d.Rows) == 0 {
		d.Rows = append(d.Rows, gweCondRow{Mode: "include", Rule: "general"})
	}
	general := gweRuleGroup{Label: "General", Opts: []gweRuleOpt{{"general", "Entire Site"}, {"singular", "All Singular"}, {"archive", "All Archives"}}}
	singular := gweRuleGroup{Label: "Singular", Opts: []gweRuleOpt{{"singular/front_page", "Front Page"}, {"singular/post", "All Posts"},
		{"singular/page", "All Pages"}, {"singular/not_found404", "404 Page"}}}
	archive := gweRuleGroup{Label: "Archives", Opts: []gweRuleOpt{{"archive/post_archive", "Blog (Posts Page)"}, {"archive/category", "All Categories"},
		{"archive/post_tag", "All Tags"}, {"archive/author", "Author Archives"}, {"archive/date", "Date Archives"}, {"archive/search", "Search Results"}}}
	d.RuleOpts = []gweRuleGroup{general, singular, archive}
	for _, typ := range []string{"page", "post"} {
		posts, _, _ := s.store.QueryPosts(c, store.PostQuery{Types: []string{typ}, Statuses: []string{"publish", "private"}, PerPage: 200, Page: 1, OrderBy: "title", Order: "ASC"})
		g := gweRuleGroup{Label: map[string]string{"page": "Specific Page", "post": "Specific Post"}[typ]}
		for _, sp := range posts {
			g.Opts = append(g.Opts, gweRuleOpt{"singular/" + typ + "/" + itoa64(sp.ID), defaultStr(sp.Title, "(no title)")})
		}
		if len(g.Opts) > 0 {
			d.RuleOpts = append(d.RuleOpts, g)
		}
	}
	for _, tax := range []string{"category", "post_tag"} {
		terms, _, _ := s.store.QueryTerms(c, store.TermQuery{Taxonomy: tax, OrderBy: "name", Order: "ASC", PerPage: 200, Page: 1})
		g := gweRuleGroup{Label: map[string]string{"category": "Specific Category", "post_tag": "Specific Tag"}[tax]}
		for _, t := range terms {
			g.Opts = append(g.Opts, gweRuleOpt{"archive/" + tax + "/" + itoa64(t.ID), t.Name})
		}
		if len(g.Opts) > 0 {
			d.RuleOpts = append(d.RuleOpts, g)
		}
	}
	// Keep a saved rule selectable even when it is not in the lists above.
	known := map[string]bool{}
	for _, g := range d.RuleOpts {
		for _, o := range g.Opts {
			known[o.Value] = true
		}
	}
	var other gweRuleGroup
	other.Label = "Other"
	for _, r := range d.Rows {
		if !known[r.Rule] {
			other.Opts = append(other.Opts, gweRuleOpt{r.Rule, s.gweConditionLabel(c, "include/"+r.Rule)})
			known[r.Rule] = true
		}
	}
	if len(other.Opts) > 0 {
		d.RuleOpts = append(d.RuleOpts, other)
	}
	s.adminPage(c, "gowebeditor-theme-builder", "Display Conditions", "appearance_page_gowebeditor-theme-builder", "admin/gowebeditor-theme-conditions", d)
}
