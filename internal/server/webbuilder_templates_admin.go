package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
	"wpgo/internal/wp"
)

// Appearance › Theme Builder (themes?page=web-builder-templates, contract §11.1): the list of
// Theme Builder templates as a WordPress list table, the Add New dialog, the display conditions
// dialog (web/admin/assets/js/web-builder-templates.js) and the row and bulk actions. The screen
// exists only while Pursae Web Builder is on and needs edit_theme_options everywhere
// (webbuilder.TemplateCap): the core maps wb_template meta caps to *_posts, which would admit
// authors and editors.
//
// The core post screens send templates here too (wbTemplatePostScreen, wbTemplatesListRedirect):
// post?post=<template> opens the builder, its trash/untrash/delete links need edit_theme_options,
// and edit?post_type=wb_template is this screen.

const (
	wbTBPerPage     = 20
	wbTBBulkNonce   = "bulk-wb-templates"
	wbTBCreateNonce = "wb-template-create"
	wbTBHelloNonce  = "wb-template-activate-hello"
	wbTBTypeParam   = "template_type" // the subsubsub filter
	wbTBCopySuffix  = " (Copy)"
)

var (
	errWBTForbidden  = errors.New("web builder: not allowed")
	errWBTNoSnapshot = errors.New("web builder: template has no published design")
	errWBTUnknown    = errors.New("web builder: unknown action")
)

// wbTBStarterLabels describe the starter designs of the Add New dialog.
var wbTBStarterLabels = map[webbuilder.StarterID][2]string{
	webbuilder.StarterBlank:   {"Blank", "An empty template."},
	webbuilder.StarterHeader:  {"Header", "Logo, site title and the primary menu."},
	webbuilder.StarterFooter:  {"Footer", "Footer menu, tagline and copyright."},
	webbuilder.StarterSingle:  {"Single", "Title, meta, featured image, content and comments."},
	webbuilder.StarterArchive: {"Archive", "Archive title, a post grid and pagination."},
	webbuilder.StarterSearch:  {"Search Results", "Search form, results grid and pagination."},
}

// wbTemplatesPage runs the Theme Builder screen when the Themes request asks for it
// (themes?page=web-builder-templates; themesScreen calls it first).
func (s *Server) wbTemplatesPage(c *gin.Context) bool {
	if c.Query("page") != webbuilder.ThemeBuilderPage {
		return false
	}
	s.wbTemplatesScreen(c)
	return true
}

// wbThemeBuilderMenu adds Appearance › Theme Builder while the plugin is on, for users with
// edit_theme_options (pluginMenuEntries calls it; add_theme_page() appends to the submenu).
func (s *Server) wbThemeBuilderMenu(menu []*menuTop, screen string, u *store.User) {
	if !s.wbOn() || u == nil || !u.Can(webbuilder.TemplateCap, nil) {
		return
	}
	slug := strings.TrimPrefix(wbThemeBuilderURL(), "/wp-admin/")
	for _, top := range menu {
		if top.ID != "menu-appearance" {
			continue
		}
		sub := &menuSub{Slug: slug, Title: template.HTML("Theme Builder"), Current: slug == screen}
		if sub.Current {
			top.Current = true
			for _, o := range top.Sub {
				o.Current = false
			}
		}
		top.Sub = append(top.Sub, sub)
	}
}

func (s *Server) wbTBDeny(c *gin.Context) {
	c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
}

// wbTemplatesScreen is GET themes?page=web-builder-templates: the list, or one of its actions.
func (s *Server) wbTemplatesScreen(c *gin.Context) {
	u := currentUser(c)
	if !s.wbOn() || u == nil || !u.Can(webbuilder.TemplateCap, nil) {
		s.wbTBDeny(c)
		return
	}
	if a := bulkAction(c); a != "" {
		s.wbTemplatesBulk(c, a)
		return
	}
	// A row action names its template; the bulk selects also send action/action2 with a search,
	// which only lists.
	switch action := c.Query("action"); {
	case action == "create":
		s.wbTemplateCreate(c)
	case action == "activate-hello":
		s.wbTemplateActivateHello(c)
	case action != "" && action != "-1" && c.Query("post") != "":
		s.wbTemplateRowAction(c, action)
	default:
		s.wbTemplatesList(c)
	}
}

// --- list --------------------------------------------------------------------------------------

type wbTBView struct {
	Key, Label, URL string
	Count           int
	Current         bool
}

type wbTBCond struct {
	Label   string
	Exclude bool
}

type wbTBRow struct {
	ID                                int64
	Title, Type, TypeLabel, Status    string
	IsTrash, Disabled, HasHTML        bool
	Conditions                        []wbTBCond
	ConditionsJSON, LabelsJSON        string
	Priority                          int
	Author                            string
	DateLabel, DateValue              string
	EditURL, PreviewURL, DuplicateURL string
	ToggleURL, ToggleLabel            string
	TrashURL, RestoreURL, DeleteURL   string
	CanDelete                         bool
	Classes                           string
}

type wbTBOption struct {
	Value, Label string
	Selected     bool
}

type wbTBStarter struct {
	ID, Label, Description string
}

type wbTemplatesData struct {
	Views      []wbTBView
	Rows       []wbTBRow
	Search     string
	Type       string // the subsubsub filter, "" = all
	IsTrash    bool
	BulkNonce  string
	Pagination pagination

	Types       []wbTBOption // Add New: template types
	PostTypes   []wbTBOption // Add New: custom post types for "Single"
	Starters    []wbTBStarter
	CreateNonce string
	Config      template.JS // the dialogs' settings (web-builder-templates.js)
	Ver         string

	ThemeNotice bool   // the active theme is not Hello Pursae
	ThemeName   string // the active theme's name
	ActivateURL string // "" when the user may not switch themes
}

// wbTemplatesList renders the list table.
func (s *Server) wbTemplatesList(c *gin.Context) {
	ctx := c.Request.Context()
	u := currentUser(c)
	wb := s.store.WebBuilder()
	d := wbTemplatesData{Search: strings.TrimSpace(c.Query("s")), BulkNonce: s.nonce(c, wbTBBulkNonce),
		CreateNonce: s.nonce(c, wbTBCreateNonce), Ver: Version}
	d.IsTrash = c.Query("post_status") == "trash"
	if typ := webbuilder.TemplateType(c.Query(wbTBTypeParam)); typ != "" {
		if _, ok := webbuilder.TemplateTypeLabels[typ]; ok {
			d.Type = string(typ)
		}
	}
	counts, err := wb.TemplateCounts(ctx)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	base := wbThemeBuilderURL()
	d.Views = append(d.Views, wbTBView{Key: "all", Label: "All", URL: base, Count: counts.All, Current: !d.IsTrash && d.Type == ""})
	for _, t := range webbuilder.TemplateTypes {
		d.Views = append(d.Views, wbTBView{Key: string(t), Label: webbuilder.TemplateTypeLabels[t], URL: base + "&" + wbTBTypeParam + "=" + string(t),
			Count: counts.ByType[t], Current: !d.IsTrash && d.Type == string(t)})
	}
	if counts.Trash > 0 || d.IsTrash {
		d.Views = append(d.Views, wbTBView{Key: "trash", Label: "Trash", URL: base + "&post_status=trash", Count: counts.Trash, Current: d.IsTrash})
	}

	f := store.WBTemplateFilter{Type: webbuilder.TemplateType(d.Type), Search: d.Search}
	if d.IsTrash {
		f.Statuses = []string{"trash"}
	}
	list, err := wb.Templates(ctx, f)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	page, _ := strconv.Atoi(c.Query("paged"))
	page = max(page, 1)
	pu, _ := url.Parse(c.Request.URL.RequestURI())
	d.Pagination = newPagination(len(list), wbTBPerPage, page, *pu)
	from := min((page-1)*wbTBPerPage, len(list))
	list = list[from:min(from+wbTBPerPage, len(list))]

	site := s.wbSiteBoot(c, nil)
	names := &wbCondNames{s: s, ctx: ctx, posts: map[int64]string{}, terms: map[int64]string{}, users: map[int64]string{}}
	ids := make([]int64, 0, len(list))
	for _, t := range list {
		ids = append(ids, t.ID)
	}
	withHTML := s.wbTemplatesWithHTML(ctx, ids)
	loc := s.loc()
	for _, t := range list {
		d.Rows = append(d.Rows, s.wbTemplateRow(c, u, t, site, names, withHTML[t.ID], loc))
	}
	s.wbTemplatesDialogs(c, &d, site)

	th := s.activeTheme()
	if th == nil || th.Slug != webbuilder.HelloPursaeTheme {
		if _, offered := s.wbThemeNotice(c); !offered { // the core offer already says it on Themes
			d.ThemeNotice = true
			if th != nil {
				d.ThemeName = th.Name
			}
			if u.Can("switch_themes", nil) {
				d.ActivateURL = base + "&action=activate-hello&_wpnonce=" + s.nonce(c, wbTBHelloNonce)
			}
		}
	}
	s.adminPage(c, strings.TrimPrefix(base, "/wp-admin/"), "Theme Builder", "appearance_page_"+webbuilder.ThemeBuilderPage,
		"admin/web-builder-templates", d, s.wbTemplatesNotices(c)...)
}

// wbTemplateRow is one list row.
func (s *Server) wbTemplateRow(c *gin.Context, u *store.User, t *store.WBTemplate, site webbuilder.SiteBoot, names *wbCondNames, hasHTML bool, loc *time.Location) wbTBRow {
	id := itoa64(t.ID)
	base := wbThemeBuilderURL() + "&post=" + id + "&action="
	r := wbTBRow{ID: t.ID, Title: t.Title, Type: string(t.Type), TypeLabel: webbuilder.TemplateTypeLabels[t.Type], Status: t.Status,
		IsTrash: t.Status == "trash", Disabled: t.Status != webbuilder.StatusPublish, HasHTML: hasHTML, Priority: t.Priority,
		Author: t.AuthorName, EditURL: wbEditURL(t.ID),
		CanDelete: u.Can("delete_others_posts", nil)}
	if strings.TrimSpace(r.Title) == "" {
		r.Title = "(no title)"
	}
	if r.TypeLabel == "" {
		r.TypeLabel = "Unknown"
	}
	labels := map[string]string{}
	for _, rule := range t.Conditions {
		label, exclude, idLabel := s.wbConditionLabel(site, rule, names)
		r.Conditions = append(r.Conditions, wbTBCond{Label: label, Exclude: exclude})
		if idLabel != "" {
			labels[rule] = idLabel
		}
	}
	cj, _ := json.Marshal(t.Conditions)
	lj, _ := json.Marshal(labels)
	r.ConditionsJSON, r.LabelsJSON = string(cj), string(lj)
	if r.IsTrash {
		r.RestoreURL = base + "untrash&_wpnonce=" + s.nonce(c, "untrash-wb-template_"+id)
		r.DeleteURL = base + "delete&_wpnonce=" + s.nonce(c, "delete-wb-template_"+id)
	} else {
		info, _ := s.wbTemplatePreviewInfo(c, t)
		r.PreviewURL = info.URL
		r.DuplicateURL = base + "duplicate&_wpnonce=" + s.nonce(c, "duplicate-wb-template_"+id)
		r.TrashURL = base + "trash&_wpnonce=" + s.nonce(c, "trash-wb-template_"+id)
		if r.Disabled {
			r.ToggleLabel, r.ToggleURL = "Enable", base+"enable&_wpnonce="+s.nonce(c, "enable-wb-template_"+id)
		} else {
			r.ToggleLabel, r.ToggleURL = "Disable", base+"disable&_wpnonce="+s.nonce(c, "disable-wb-template_"+id)
		}
	}
	m := t.ModifiedAt.In(loc)
	r.DateLabel, r.DateValue = "Last Modified", wp.FormatDate(m, "Y/m/d")+" at "+wp.FormatDate(m, "g:i a")
	if t.Status == webbuilder.StatusPublish {
		r.DateLabel = "Published"
	}
	classes := []string{"iedit", "level-0", "post-" + id, "type-" + webbuilder.TemplatePostType, "status-" + t.Status}
	if t.AuthorID == u.ID {
		classes = append(classes, "author-self")
	} else {
		classes = append(classes, "author-other")
	}
	r.Classes = strings.Join(classes, " ")
	return r
}

// wbTemplatesWithHTML reports which templates have a published snapshot (only those can be enabled).
func (s *Server) wbTemplatesWithHTML(ctx context.Context, ids []int64) map[int64]bool {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT post_id FROM web_builder_docs WHERE post_id = ANY($1) AND html <> ''`, ids)
	if err != nil {
		log.Printf("web builder: template snapshots: %v", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// wbTemplatesDialogs fills the Add New choices and the dialogs' JSON settings.
func (s *Server) wbTemplatesDialogs(c *gin.Context, d *wbTemplatesData, site webbuilder.SiteBoot) {
	sel := webbuilder.TemplateType(d.Type)
	if sel == "" {
		sel = webbuilder.TemplateHeader
	}
	types := make([]map[string]string, 0, len(webbuilder.TemplateTypes))
	for _, t := range webbuilder.TemplateTypes {
		d.Types = append(d.Types, wbTBOption{Value: string(t), Label: webbuilder.TemplateTypeLabels[t], Selected: t == sel})
		types = append(types, map[string]string{"slug": string(t), "label": webbuilder.TemplateTypeLabels[t], "starter": string(webbuilder.DefaultStarters[t])})
	}
	rest := map[string]string{"post": "posts", "page": "pages"}
	for _, pt := range siteRoutedTypes() {
		d.PostTypes = append(d.PostTypes, wbTBOption{Value: pt.Name, Label: wbTypePlural(pt.Name)})
		if pt.ShowInREST {
			base := pt.RestBase
			if base == "" {
				base = pt.Name
			}
			rest[pt.Name] = base
		}
	}
	for _, id := range webbuilder.StarterIDs {
		l := wbTBStarterLabels[id]
		d.Starters = append(d.Starters, wbTBStarter{ID: string(id), Label: l[0], Description: l[1]})
	}
	cfg := map[string]any{
		"rest":       s.links(c).HomeURL("/wp-json/"),
		"nonce":      s.nonce(c, "wp_rest"),
		"apiHeader":  webbuilder.APIHeader,
		"api":        webbuilder.APIVersion,
		"namespace":  webbuilder.RESTNamespace,
		"postTypes":  site.PostTypes,
		"taxonomies": site.Taxonomies,
		"restBases":  rest,
		"types":      types,
		"pattern":    webbuilder.ConditionPattern,
		"limits": map[string]int{"maxConditions": webbuilder.MaxConditions, "maxConditionBytes": webbuilder.MaxConditionBytes,
			"priorityMin": webbuilder.PriorityMin, "priorityMax": webbuilder.PriorityMax},
		"reservedSingular": webbuilder.ReservedSingularWords,
		"reservedArchive":  webbuilder.ReservedArchiveWords,
	}
	b, _ := json.Marshal(cfg)
	d.Config = template.JS(strings.NewReplacer("<", `<`, ">", `>`, "&", `&`).Replace(string(b)))
}

// wbTemplatesNotices are the result notices of an action (query arguments of the redirect).
func (s *Server) wbTemplatesNotices(c *gin.Context) []notice {
	var out []notice
	n := func(key, one, many string) {
		if v, _ := strconv.Atoi(c.Query(key)); v > 0 {
			msg := one
			if v != 1 {
				msg = strconv.Itoa(v) + " " + many
			}
			out = append(out, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(msg)), Dismissible: true})
		}
	}
	if v, _ := strconv.Atoi(c.Query("trashed")); v > 0 {
		msg := "1 template moved to the Trash."
		if v != 1 {
			msg = strconv.Itoa(v) + " templates moved to the Trash."
		}
		if ids := c.Query("ids"); ids != "" && wbTBIDsRE(ids) {
			undo := wbThemeBuilderURL() + "&action=untrash&bulk_action=1&_wpnonce=" + s.nonce(c, wbTBBulkNonce)
			for _, id := range strings.Split(ids, ",") {
				undo += "&post%5B%5D=" + id
			}
			msg = template.HTMLEscapeString(msg) + ` <a href="` + template.HTMLEscapeString(undo) + `">Undo</a>`
			out = append(out, notice{Type: "success", Message: template.HTML(msg), Dismissible: true})
		} else {
			out = append(out, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(msg)), Dismissible: true})
		}
	}
	n("untrashed", "1 template restored from the Trash. It stays disabled until you enable it.", "templates restored from the Trash. They stay disabled until you enable them.")
	n("deleted", "1 template permanently deleted.", "templates permanently deleted.")
	n("enabled", "1 template enabled.", "templates enabled.")
	n("disabled", "1 template disabled.", "templates disabled.")
	if id, _ := strconv.ParseInt(c.Query("duplicated"), 10, 64); id > 0 {
		out = append(out, notice{Type: "success", Dismissible: true, Message: template.HTML(`Template duplicated. The copy is disabled. <a href="` +
			template.HTMLEscapeString(wbEditURL(id)) + `">Edit with Pursae Web Builder</a>`)})
	}
	switch c.Query("wb_error") {
	case "snapshot":
		out = append(out, notice{Type: "error", Dismissible: true, Message: "A template can only be enabled after it was published once in Pursae Web Builder."})
	case "forbidden":
		out = append(out, notice{Type: "error", Dismissible: true, Message: "Sorry, you are not allowed to do that."})
	case "create":
		out = append(out, notice{Type: "error", Dismissible: true, Message: "The template could not be created. Choose a template type (and a post type for a Single template)."})
	case "failed":
		out = append(out, notice{Type: "error", Dismissible: true, Message: "The action could not be completed."})
	}
	if c.Query("hello") == "1" {
		out = append(out, notice{Type: "success", Dismissible: true, Message: "Hello Pursae is now the active theme."})
	}
	return out
}

func wbTBIDsRE(ids string) bool {
	for _, p := range strings.Split(ids, ",") {
		if _, err := strconv.ParseInt(p, 10, 64); err != nil {
			return false
		}
	}
	return true
}

// --- display condition labels --------------------------------------------------------------------

// wbCondNames looks up (and remembers) the names a rule's id stands for.
type wbCondNames struct {
	s                   *Server
	ctx                 context.Context
	posts, terms, users map[int64]string
}

func (n *wbCondNames) post(id int64) string {
	if v, ok := n.posts[id]; ok {
		return v
	}
	v := ""
	if p, err := n.s.store.GetPost(n.ctx, id); err == nil && p.Type != webbuilder.TemplatePostType {
		v = strings.TrimSpace(wbPlain(p.Title))
		if v == "" {
			v = "(no title)"
		}
	}
	n.posts[id] = v
	return v
}

func (n *wbCondNames) term(id int64) string {
	if v, ok := n.terms[id]; ok {
		return v
	}
	v := ""
	if t, err := n.s.store.GetTerm(n.ctx, id); err == nil {
		v = t.Name
	}
	n.terms[id] = v
	return v
}

func (n *wbCondNames) user(id int64) string {
	if v, ok := n.users[id]; ok {
		return v
	}
	v := ""
	if u, err := n.s.store.UserByID(n.ctx, id); err == nil {
		v = u.DisplayName
	}
	n.users[id] = v
	return v
}

// wbConditionLabel is the human wording of a rule, the same as the editor's conditions dialog
// (builder/host/conditions.ts ruleLabel): "Entire Site", "Pages: “About”", "Excluded: Categories:
// “News”". idLabel is the name of the rule's id ("" without one or when it names nothing).
func (s *Server) wbConditionLabel(site webbuilder.SiteBoot, rule string, names *wbCondNames) (label string, exclude bool, idLabel string) {
	c, err := webbuilder.ParseCondition(strings.TrimSpace(rule))
	if err != nil {
		return rule, false, ""
	}
	typeLabel := func(slug string) string {
		for _, p := range site.PostTypes {
			if p.Slug == slug && p.Label != "" {
				return p.Label
			}
		}
		return slug
	}
	taxLabel := func(slug string) string {
		for _, x := range site.Taxonomies {
			if x.Slug == slug && x.Label != "" {
				return x.Label
			}
		}
		return slug
	}
	switch c.Scope {
	case webbuilder.ScopeEntry, webbuilder.ScopeChildOf:
		idLabel = names.post(c.ID)
	case webbuilder.ScopeInTerm, webbuilder.ScopeTaxonomy:
		if c.ID != 0 {
			idLabel = names.term(c.ID)
		}
	case webbuilder.ScopeByAuthor, webbuilder.ScopeAuthor:
		if c.ID != 0 {
			idLabel = names.user(c.ID)
		}
	}
	item := "#" + strconv.FormatInt(c.ID, 10)
	if idLabel != "" {
		item = "“" + idLabel + "”"
	}
	switch c.Scope {
	case webbuilder.ScopeGeneral:
		label = "Entire Site"
	case webbuilder.Scope404:
		label = "404 Page"
	case webbuilder.ScopeSingular:
		label = "All Singular"
	case webbuilder.ScopeFrontPage:
		label = "Front Page"
	case webbuilder.ScopePostType:
		label = typeLabel(c.PostType)
	case webbuilder.ScopeEntry:
		label = typeLabel(c.PostType) + ": " + item
	case webbuilder.ScopeChildOf:
		label = typeLabel(c.PostType) + ": child of " + item
	case webbuilder.ScopeByAuthor:
		label = typeLabel(c.PostType) + " by " + item
	case webbuilder.ScopeInTerm:
		label = typeLabel(c.PostType) + " in " + taxLabel(c.Taxonomy) + ": " + item
	case webbuilder.ScopeArchive:
		label = "All Archives"
	case webbuilder.ScopeHome:
		label = "Posts Page"
	case webbuilder.ScopeDate:
		label = "Date Archives"
	case webbuilder.ScopeSearch:
		label = "Search Results"
	case webbuilder.ScopeTypeArchive:
		label = typeLabel(c.PostType) + " Archive"
	case webbuilder.ScopeAuthor:
		label = "Author Archives"
		if c.ID != 0 {
			label += ": " + item
		}
	case webbuilder.ScopeTaxonomy:
		label = taxLabel(c.Taxonomy)
		if c.ID != 0 {
			label += ": " + item
		}
	default:
		label = rule
	}
	if c.Exclude {
		label = "Excluded: " + label
	}
	return label, c.Exclude, idLabel
}

// --- actions -----------------------------------------------------------------------------------

// wbTBRedirect goes back to the list (the trash view when trash is set) with result arguments.
func (s *Server) wbTBRedirect(c *gin.Context, trash bool, args url.Values) {
	target := wbThemeBuilderURL()
	if trash {
		target += "&post_status=trash"
	}
	if t := c.Query(wbTBTypeParam); t != "" && !trash {
		if _, ok := webbuilder.TemplateTypeLabels[webbuilder.TemplateType(t)]; ok {
			target += "&" + wbTBTypeParam + "=" + t
		}
	}
	if len(args) > 0 {
		target += "&" + args.Encode()
	}
	c.Redirect(http.StatusFound, target)
}

// wbApplyTemplateAction runs one row action on the template p as u. It answers the result key of
// the redirect (trashed, untrashed, deleted, enabled, disabled) or, for duplicate, the copy's id.
func (s *Server) wbApplyTemplateAction(c *gin.Context, u *store.User, p *store.Post, action string) (string, int64, error) {
	ctx := c.Request.Context()
	if !u.Can(webbuilder.TemplateCap, nil) {
		return "", 0, errWBTForbidden
	}
	wb := s.store.WebBuilder()
	var err error
	var key string
	switch action {
	case "trash":
		if p.Status == "trash" {
			return "", 0, errWBTUnknown
		}
		err, key = s.store.TrashPost(ctx, p.ID), "trashed"
	case "untrash":
		if p.Status != "trash" {
			return "", 0, errWBTUnknown
		}
		err, key = s.store.UntrashPost(ctx, p.ID), "untrashed"
	case "delete":
		if !u.Can("delete_others_posts", nil) {
			return "", 0, errWBTForbidden
		}
		err, key = s.store.DeletePost(ctx, p.ID), "deleted"
	case "enable", "disable":
		if p.Status == "trash" {
			return "", 0, errWBTUnknown
		}
		status, k := webbuilder.StatusDraft, "disabled"
		if action == "enable" {
			status, k = webbuilder.StatusPublish, "enabled"
			if !s.wbTemplatesWithHTML(ctx, []int64{p.ID})[p.ID] {
				return "", 0, errWBTNoSnapshot
			}
		}
		key = k
		if p.Status != status {
			_, err = s.store.Pool.Exec(ctx, `UPDATE posts SET status = $2, modified_at = now(), updated_at = now() WHERE id = $1 AND type = $3`,
				p.ID, status, webbuilder.TemplatePostType)
		}
	case "duplicate":
		if p.Status == "trash" {
			return "", 0, errWBTUnknown
		}
		t, terr := wb.Template(ctx, p.ID)
		if terr != nil {
			return "", 0, terr
		}
		cp, derr := s.wbDuplicateTemplate(ctx, t, u.ID)
		if derr != nil {
			return "", 0, derr
		}
		return "duplicated", cp.ID, nil // a disabled copy: nothing on the site changes
	default:
		return "", 0, errWBTUnknown
	}
	if err != nil {
		return "", 0, err
	}
	if fresh, ferr := s.store.GetPost(ctx, p.ID); ferr == nil {
		s.afterPostSave(ctx, fresh) // save_post side effects; wbAfterPostSave calls wbTemplatesChanged
	} else {
		s.wbTemplatesChanged(ctx) // deleted
	}
	return key, 0, nil
}

func wbTBErrorKey(err error) string {
	switch {
	case errors.Is(err, errWBTForbidden):
		return "forbidden"
	case errors.Is(err, errWBTNoSnapshot):
		return "snapshot"
	}
	return "failed"
}

// wbTemplateRowAction is a row action link (…&action=<a>&post=<id>&_wpnonce=…).
func (s *Server) wbTemplateRowAction(c *gin.Context, action string) {
	switch action {
	case "trash", "untrash", "delete", "enable", "disable", "duplicate":
	default:
		c.String(http.StatusBadRequest, "Unknown action.")
		return
	}
	id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
	if !s.checkNonce(c, action+"-wb-template_"+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	p, err := s.store.GetPost(c, id)
	if err != nil || !wbIsTemplate(p) {
		c.String(http.StatusNotFound, "You attempted to edit an item that does not exist. Perhaps it was deleted?")
		return
	}
	trash := p.Status == "trash" && action != "untrash"
	key, newID, err := s.wbApplyTemplateAction(c, currentUser(c), p, action)
	if err != nil {
		if !errors.Is(err, errWBTForbidden) && !errors.Is(err, errWBTNoSnapshot) && !errors.Is(err, errWBTUnknown) {
			log.Printf("web builder: template %d %s: %v", id, action, err)
		}
		s.wbTBRedirect(c, trash, url.Values{"wb_error": {wbTBErrorKey(err)}})
		return
	}
	if action == "untrash" || action == "delete" {
		trash = action == "delete"
	}
	args := url.Values{key: {"1"}}
	switch action {
	case "duplicate":
		args = url.Values{"duplicated": {itoa64(newID)}}
	case "trash":
		args.Set("ids", itoa64(id))
	}
	s.wbTBRedirect(c, trash, args)
}

// wbTemplatesBulk applies a bulk action to the checked templates (post[]).
func (s *Server) wbTemplatesBulk(c *gin.Context, action string) {
	switch action {
	case "trash", "untrash", "delete", "enable", "disable":
	default:
		c.String(http.StatusBadRequest, "Unknown action.")
		return
	}
	if !s.checkNonce(c, wbTBBulkNonce) {
		s.denyNonce(c)
		return
	}
	u := currentUser(c)
	trashView := c.Query("post_status") == "trash"
	n := 0
	var done []string
	var firstErr error
	key := ""
	for _, raw := range c.QueryArray("post[]") {
		id, _ := strconv.ParseInt(raw, 10, 64)
		p, err := s.store.GetPost(c, id)
		if err != nil || !wbIsTemplate(p) {
			continue
		}
		k, _, err := s.wbApplyTemplateAction(c, u, p, action)
		if err != nil {
			if firstErr == nil && !errors.Is(err, errWBTUnknown) {
				firstErr = err
			}
			continue
		}
		key = k
		n++
		done = append(done, raw)
	}
	args := url.Values{}
	if key != "" {
		args.Set(key, strconv.Itoa(n))
		if key == "trashed" {
			args.Set("ids", strings.Join(done, ","))
		}
	}
	if firstErr != nil {
		args.Set("wb_error", wbTBErrorKey(firstErr))
	}
	if action == "untrash" {
		trashView = false
	} else if action == "delete" {
		trashView = true
	}
	s.wbTBRedirect(c, trashView, args)
}

// wbTemplateCreate is the Add New form without JavaScript (the dialog posts to REST
// web-builder/v2/templates instead): it creates the draft and opens it in the builder.
func (s *Server) wbTemplateCreate(c *gin.Context) {
	if !s.checkNonce(c, wbTBCreateNonce) {
		s.denyNonce(c)
		return
	}
	u := currentUser(c)
	typ := webbuilder.TemplateType(c.Query("type"))
	starter := webbuilder.StarterID(c.Query("starter"))
	if _, ok := webbuilder.TemplateTypeLabels[typ]; !ok || (starter != "" && !slices.Contains(webbuilder.StarterIDs, starter)) {
		s.wbTBRedirect(c, false, url.Values{"wb_error": {"create"}})
		return
	}
	n := store.NewWBTemplate{Type: typ, Title: wbClip(strings.TrimSpace(c.Query("title")), wbTitleMax), Starter: starter, AuthorID: u.ID}
	if typ == webbuilder.TemplateSingle {
		pt := c.Query("post_type")
		if pt == "" || siteCPT(pt) == nil {
			s.wbTBRedirect(c, false, url.Values{"wb_error": {"create"}})
			return
		}
		n.Conditions = []string{webbuilder.CondInclude + "/singular/" + pt}
	}
	t, err := s.store.WebBuilder().CreateTemplate(c, n)
	if err != nil {
		log.Printf("web builder: create template: %v", err)
		s.wbTBRedirect(c, false, url.Values{"wb_error": {"create"}})
		return
	}
	c.Redirect(http.StatusFound, wbEditURL(t.ID))
}

// wbTemplateActivateHello is the notice's Activate: Hello Pursae becomes the active theme.
func (s *Server) wbTemplateActivateHello(c *gin.Context) {
	if !s.checkNonce(c, wbTBHelloNonce) {
		s.denyNonce(c)
		return
	}
	if !currentUser(c).Can("switch_themes", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to switch themes for this site.")
		return
	}
	s.wbSwitchToHello(c.Request.Context())
	c.Redirect(http.StatusFound, wbThemeBuilderURL()+"&hello=1")
}

// --- duplicate ---------------------------------------------------------------------------------

// wbDuplicateTemplate copies a template as a disabled draft "<title> (Copy)" with its conditions,
// priority, preview object and published doc. Every section id of the copy is regenerated in the
// doc and, consistently, in the snapshot and its CSS (ids are HTML ids, CSS selectors, loop and
// form keys), so the copy never shares ids with its source; the copy keeps the _wb_dup marker
// for the editor (contract §11.1). Should an id be too short to be replaced safely in the
// snapshot, the copy keeps only the doc and is rendered on its first publish.
func (s *Server) wbDuplicateTemplate(ctx context.Context, src *store.WBTemplate, authorID int64) (*store.WBTemplate, error) {
	if src.Type == "" {
		return nil, store.ErrTemplateType
	}
	wb := s.store.WebBuilder()
	title := wbClip(strings.TrimSpace(src.Title)+wbTBCopySuffix, wbTitleMax)
	cp, err := wb.CreateTemplate(ctx, store.NewWBTemplate{Type: src.Type, Title: title, Starter: src.Starter,
		Conditions: append([]string{}, src.Conditions...), Priority: src.Priority, Preview: src.Preview, AuthorID: authorID})
	if err != nil {
		return nil, err
	}
	from, err := wb.Doc(ctx, src.ID)
	if errors.Is(err, store.ErrNotFound) {
		return cp, nil // nothing designed yet
	}
	if err != nil {
		return cp, err
	}
	doc, html, css, kept := wbRegenerateSectionIDs(from.Doc, from.HTML, from.CSS)
	page := map[string]any{}
	_ = json.Unmarshal(from.Page, &page)
	if page == nil {
		page = map[string]any{}
	}
	page["title"] = cp.Title
	pageJSON, _ := json.Marshal(page)
	var content string
	_ = s.store.Pool.QueryRow(ctx, `SELECT content FROM posts WHERE id = $1`, src.ID).Scan(&content)
	d := &store.WebBuilderDoc{PostID: cp.ID, Doc: doc, Page: pageJSON, Fonts: from.Fonts, Features: from.Features,
		ContentHash: store.WebBuilderContentHash(content), SnapshotUser: from.SnapshotUser, SnapshotUnfiltered: from.SnapshotUnfiltered}
	if kept {
		html = wbRetargetForms(html, src.ID, cp.ID)
		html = strings.ReplaceAll(html, `data-wb-post="`+itoa64(src.ID)+`"`, `data-wb-post="`+itoa64(cp.ID)+`"`)
		d.HTML, d.CSS, d.Renderer = html, css, from.Renderer
	}
	if d.Features == nil {
		d.Features = []string{}
	}
	if err := wb.PublishDoc(ctx, d, 0); err != nil {
		return cp, err
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET content = $2, meta = meta || jsonb_build_object($3::text, '1') WHERE id = $1`,
		cp.ID, content, webbuilder.PostMarkerMeta); err != nil {
		return cp, err
	}
	if err := wb.SetTemplateDup(ctx, cp.ID, true); err != nil {
		return cp, err
	}
	return cp, nil
}

// wbRegenerateSectionIDs gives every section of a doc (nested layout sections included) a new id
// and applies the same renaming to the snapshot html and css. When every old id is distinctive
// enough to be renamed as a whole token (the editor's ids look like "s_k3j9x0a"), the three texts
// are rewritten token by token and kept is true; otherwise only the doc is rewritten and kept is
// false (the snapshot must be rendered again).
func wbRegenerateSectionIDs(doc json.RawMessage, html, css string) (json.RawMessage, string, string, bool) {
	dec := json.NewDecoder(strings.NewReader(string(doc)))
	dec.UseNumber()
	var root any
	if dec.Decode(&root) != nil {
		return doc, html, css, html != ""
	}
	var ids []string
	wbWalkSections(root, func(sec map[string]any) {
		if id, ok := sec["id"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	})
	if len(ids) == 0 {
		return doc, html, css, true
	}
	all := string(doc) + html + css
	m := make(map[string]string, len(ids))
	safe := true
	for _, id := range ids {
		if _, dup := m[id]; dup {
			continue
		}
		m[id] = wbNewSectionID(func(n string) bool { return strings.Contains(all, n) || slices.Contains(ids, n) })
		if !wbDistinctiveID(id) {
			safe = false
		}
	}
	if safe {
		return json.RawMessage(wbReplaceTokens(string(doc), m)), wbReplaceTokens(html, m), wbReplaceTokens(css, m), true
	}
	wbWalkSections(root, func(sec map[string]any) {
		if id, ok := sec["id"].(string); ok && m[id] != "" {
			sec["id"] = m[id]
		}
	})
	out, err := json.Marshal(root)
	if err != nil {
		return doc, html, css, html != ""
	}
	return out, "", "", false
}

// wbWalkSections calls fn for every section of a DocState: doc.sections[] and, recursively, the
// sub sections of their items.
func wbWalkSections(root any, fn func(map[string]any)) {
	var section func(v any)
	section = func(v any) {
		sec, ok := v.(map[string]any)
		if !ok {
			return
		}
		fn(sec)
		items, _ := sec["items"].([]any)
		for _, it := range items {
			if im, ok := it.(map[string]any); ok {
				section(im["sub"])
			}
		}
	}
	if m, ok := root.(map[string]any); ok {
		secs, _ := m["sections"].([]any)
		for _, sec := range secs {
			section(sec)
		}
	}
}

// wbDistinctiveID: long enough and not a plain word, so renaming it as a whole token cannot hit an
// element name, a class or a CSS keyword.
func wbDistinctiveID(id string) bool {
	if len(id) < 5 {
		return false
	}
	return strings.ContainsAny(id, "0123456789_")
}

// wbNewSectionID is a fresh editor-style id ("s_" + 7 base-36 characters) for which taken is false.
func wbNewSectionID(taken func(string) bool) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for {
		b := []byte("s_")
		for range 7 {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				panic(err)
			}
			b = append(b, alphabet[n.Int64()])
		}
		if id := string(b); !taken(id) {
			return id
		}
	}
}

// wbReplaceTokens replaces every maximal run of id characters ([A-Za-z0-9_-], IDPattern) that is a
// key of m by its value.
func wbReplaceTokens(s string, m map[string]string) string {
	isID := func(b byte) bool {
		return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
	}
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); {
		if !isID(s[i]) {
			out.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && isID(s[j]) {
			j++
		}
		if v, ok := m[s[i:j]]; ok {
			out.WriteString(v)
		} else {
			out.WriteString(s[i:j])
		}
		i = j
	}
	return out.String()
}

// --- core post screens -------------------------------------------------------------------------

// wbTemplatePostScreen handles post?post=<id> for a template (postEdit calls it first): trash,
// untrash and delete need edit_theme_options (and delete_others_posts to delete) and return to
// the Theme Builder; editing opens the builder. It reports whether it answered the request.
func (s *Server) wbTemplatePostScreen(c *gin.Context, p *store.Post) bool {
	if !wbIsTemplate(p) {
		return false
	}
	u := currentUser(c)
	action := c.DefaultQuery("action", "edit")
	switch action {
	case "trash", "untrash", "delete":
		if !s.checkNonce(c, action+"-post_"+itoa64(p.ID)) {
			s.denyNonce(c)
			return true
		}
		if u == nil || !u.Can(webbuilder.TemplateCap, nil) || (action == "delete" && !u.Can("delete_others_posts", nil)) {
			c.String(http.StatusForbidden, "Sorry, you are not allowed to move this item to the Trash.")
			return true
		}
		key, _, err := s.wbApplyTemplateAction(c, u, p, action)
		if err != nil {
			s.wbTBRedirect(c, p.Status == "trash", url.Values{"wb_error": {wbTBErrorKey(err)}})
			return true
		}
		args := url.Values{key: {"1"}}
		if action == "trash" {
			args.Set("ids", itoa64(p.ID))
		}
		s.wbTBRedirect(c, action == "delete", args)
		return true
	}
	if u == nil || !u.Can(webbuilder.TemplateCap, nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return true
	}
	if !s.wbOn() {
		c.String(http.StatusForbidden, "Theme Builder templates can only be edited while Pursae Web Builder is active.")
		return true
	}
	if action == wbEditAction || action == wbEditActionDev {
		return false // the builder (postEdit)
	}
	if p.Status == "trash" {
		c.String(http.StatusForbidden, "You cannot edit this item because it is in the Trash. Please restore it and try again.")
		return true
	}
	c.Redirect(http.StatusFound, wbEditURL(p.ID)) // no core editor for templates
	return true
}

// wbTemplatesListRedirect sends edit?post_type=wb_template to the Theme Builder (postsList calls
// it first), keeping the trash view and the result arguments.
func (s *Server) wbTemplatesListRedirect(c *gin.Context) bool {
	if c.Query("post_type") != webbuilder.TemplatePostType {
		return false
	}
	u := currentUser(c)
	if !s.wbOn() || u == nil || !u.Can(webbuilder.TemplateCap, nil) {
		s.wbTBDeny(c)
		return true
	}
	args := url.Values{}
	for _, k := range []string{"trashed", "untrashed", "deleted", "ids", "s"} {
		if v := c.Query(k); v != "" {
			args.Set(k, v)
		}
	}
	s.wbTBRedirect(c, c.Query("post_status") == "trash", args)
	return true
}
