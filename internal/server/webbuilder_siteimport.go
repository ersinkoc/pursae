package server

import (
	"fmt"
	"html"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Pursae MCP web_builder_import_site: a whole static site (a folder of HTML pages whose CSS, JS,
// fonts and images are a design kit) imported in one call as code sections
// (docs/developer/web-builder-code-sections.md, "Importing a site"):
//
//  1. the page tree (webbuilder.PlanSite): index.html is the front page, a/b.html the page b under
//     the page of folder a (a.html or a/index.html, else a draft placeholder, so /a/ stays a 404 as
//     in the design), 404.html the Theme Builder 404 template;
//  2. the pages are found (by their post meta, so a second import updates them) or created, and
//     their URLs make the link map of every link between the design's pages;
//  3. webbuilder.ConvertSite converts every page and clusters the pages' headers and footers into
//     as few templates as reproduce them, each with the rule that marks the current page's link;
//  4. each page is published (layout site), with its title, description and social tags; each
//     header and footer template gets display conditions (the largest one the whole site, the
//     others their pages); templates of an earlier import that are no longer needed are disabled.
//
// Everything the import creates carries _wb_site_kit (the kit) and _wb_site_key (the page's file,
// "dir:<folder>" for a placeholder parent, "header-<n>", "footer-<n>", "404" for templates).

const (
	wbSiteMetaKit = "_wb_site_kit"
	wbSiteMetaKey = "_wb_site_key"
	// wbSiteMaxPages bounds one import (a call carries every page's HTML).
	wbSiteMaxPages = 500
	// wbSiteTemplatePriority makes the import's templates win a tie with other templates of their
	// type that apply to the whole site.
	wbSiteTemplatePriority = 10
)

var wbSiteTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// wbSiteOwned finds the non-trashed post of a type the import of kit made for key (0: none).
// A page of the import that a collection made a post (wbSiteGroups) is still the import's page:
// "page" finds both.
func (s *Server) wbSiteOwned(c *gin.Context, typ, kit, key string) int64 {
	var id int64
	types := []string{typ}
	if typ == "page" {
		types = append(types, "post")
	}
	_ = s.store.Pool.QueryRow(c, `SELECT id FROM posts WHERE type = ANY($1) AND meta->>$2 = $3 AND meta->>$4 = $5
		AND status NOT IN ('trash','auto-draft') ORDER BY id DESC LIMIT 1`, types, wbSiteMetaKit, kit, wbSiteMetaKey, key).Scan(&id)
	return id
}

// wbSiteSetType makes an imported page a post or a post a page again (a collection of
// pretty-permalink posts, wbSiteGroups); a post has no parent.
func (s *Server) wbSiteSetType(c *gin.Context, id int64, typ string) error {
	q := `UPDATE posts SET type = $2, updated_at = now() WHERE id = $1 AND type IN ('page','post') AND type <> $2`
	if typ == "post" {
		q = `UPDATE posts SET type = $2, parent_id = NULL, updated_at = now() WHERE id = $1 AND type IN ('page','post') AND type <> $2`
	}
	_, err := s.store.Pool.Exec(c, q, id, typ)
	return err
}

func (s *Server) wbSiteMark(c *gin.Context, id int64, kit, key string) error {
	_, err := s.store.Pool.Exec(c, `UPDATE posts SET meta = meta || jsonb_build_object($2::text, $3::text, $4::text, $5::text) WHERE id = $1`,
		id, wbSiteMetaKit, kit, wbSiteMetaKey, key)
	return err
}

// wbSitePage finds or creates the page of key (draft until its doc is published) under parent,
// with slug and title; an existing page gets the slug and parent of the plan.
func (s *Server) wbSitePage(c *gin.Context, kit, key, slug, title string, parent int64) (int64, bool, error) {
	if id := s.wbSiteOwned(c, "page", kit, key); id > 0 {
		// A post an earlier import made of it is a page again until this import groups it.
		if err := s.wbSiteSetType(c, id, "page"); err != nil {
			return id, false, err
		}
		p, err := s.store.GetPost(c, id)
		if err == nil && (p.Slug != slug || p.ParentID != parent) {
			if err := s.mcpREST(c, http.MethodPost, "/wp/v2/pages/"+itoa64(id), map[string]any{"slug": slug, "parent": parent}, nil); err != nil {
				return id, false, err
			}
		}
		return id, false, nil
	}
	body := map[string]any{"title": title, "status": "draft", "slug": slug}
	if parent > 0 {
		body["parent"] = parent
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := s.mcpREST(c, http.MethodPost, "/wp/v2/pages", body, &created); err != nil {
		return 0, false, err
	}
	return created.ID, true, s.wbSiteMark(c, created.ID, kit, key)
}

func mcpWBImportSite(s *Server, c *gin.Context, args map[string]any) (any, error) {
	if err := s.mcpNeedBuilder(); err != nil {
		return nil, err
	}
	u := restCurrent(c)
	if u == nil || !wbUnfiltered(c, u) || !u.Can(webbuilder.TemplateCap, nil) {
		return nil, mcpErrf("a site import keeps the design's own HTML, CSS and JavaScript and makes Theme Builder templates: the account needs unfiltered_html and edit_theme_options (an administrator), and an Application Password the Appearance (themes) scope or full access")
	}
	status, err := mcpPublishStatus(args)
	if err != nil {
		return nil, err
	}
	kit := argStr(args, "kit")
	k := s.wbKitList()[kit]
	if k == nil {
		return nil, mcpErrf("no design kit %q; upload the site's folder first with web_builder_kit_upload (its CSS, JS, fonts and images; the HTML pages go in pages)", kit)
	}
	kfiles, err := wbKitFiles(filepath.Join(s.wbKitRoot(), kit))
	if err != nil {
		return nil, err
	}
	// templates: false keeps each page's header and footer in the page (layout canvas) instead of
	// making header and footer templates.
	noSplit := args["templates"] == false
	opts := webbuilder.SiteImportOptions{KitURL: wbKitURLPrefix + kit + "/", KitFiles: map[string]bool{}, URLs: map[string]string{}, NoSplit: noSplit}
	for p := range kfiles {
		opts.KitFiles[p] = true
	}
	// media: files of the design outside the kit (too large for it, put into the Media Library):
	// {"<path in the folder>": "<URL>"}; the pages point at those URLs.
	if mm, ok := args["media"].(map[string]any); ok {
		opts.Remap = map[string]string{}
		for k, v := range mm {
			u, _ := v.(string)
			rel := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(k, "\\", "/")), "/")
			lu := strings.ToLower(u)
			if rel == "" || !(strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") || strings.HasPrefix(lu, "https://") || strings.HasPrefix(lu, "http://")) || strings.ContainsAny(u, "\"'<> ") {
				return nil, mcpErrf("media: %q → %q is not a file path and a URL", k, u)
			}
			opts.Remap[rel] = u
		}
	}
	// The pages.
	src := map[string]string{}
	var paths []string
	for _, it := range argList(args, "pages") {
		m, _ := it.(map[string]any)
		p := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(argStr(m, "path"), "\\", "/")), "/")
		if p == "" || !webbuilder.IsSitePage(p) {
			return nil, mcpErrf("pages: %q is not an .html file path inside the folder", argStr(m, "path"))
		}
		if _, dup := src[p]; dup {
			return nil, mcpErrf("pages: %q is given twice", p)
		}
		src[p] = argStr(m, "html")
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return nil, mcpErrf("pages is required: [{path: \"index.html\", html: \"<!doctype html>…\"}, …] (every HTML file of the folder)")
	}
	if len(paths) > wbSiteMaxPages {
		return nil, mcpErrf("at most %d pages per import", wbSiteMaxPages)
	}
	r, err := s.mcpRenderer()
	if err != nil {
		return nil, err
	}
	plan := webbuilder.PlanSite(paths)
	pages := map[string]webbuilder.SitePlanPage{}
	for _, p := range plan.Pages {
		pages[p.Path] = p
	}
	folders := map[string]webbuilder.SitePlanFolder{}
	for _, f := range plan.Folders {
		folders[f.Key] = f
	}
	var warnings []string
	ids := map[string]int64{}
	created, updated := []string{}, []string{}
	titleOf := func(p string) string {
		if m := wbSiteTitleRE.FindStringSubmatch(src[p]); m != nil {
			if t := strings.TrimSpace(html.UnescapeString(m[1])); t != "" {
				return truncRunes(t, 200)
			}
		}
		return strings.TrimSuffix(path.Base(p), path.Ext(p))
	}
	var ensurePage func(p string, depth int) (int64, error)
	var ensureFolder func(key string, depth int) (int64, error)
	ensureFolder = func(key string, depth int) (int64, error) {
		if key == "" {
			return 0, nil
		}
		if id, ok := ids[key]; ok {
			return id, nil
		}
		f, ok := folders[key]
		if !ok || depth > 32 {
			return 0, fmt.Errorf("folder %s", key)
		}
		if f.Page != "" {
			id, err := ensurePage(f.Page, depth+1)
			ids[key] = id
			return id, err
		}
		parent, err := ensureFolder(f.Parent, depth+1)
		if err != nil {
			return 0, err
		}
		title := strings.ToUpper(f.Slug[:1]) + f.Slug[1:]
		id, isNew, err := s.wbSitePage(c, kit, key, f.Slug, title, parent)
		if err != nil {
			return 0, fmt.Errorf("the parent page of %s/: %v", strings.TrimPrefix(key, "dir:"), err)
		}
		if isNew {
			created = append(created, key+" (draft parent)")
		}
		ids[key] = id
		return id, nil
	}
	ensurePage = func(p string, depth int) (int64, error) {
		if id, ok := ids[p]; ok {
			return id, nil
		}
		pl := pages[p]
		parent, err := ensureFolder(pl.Parent, depth+1)
		if err != nil {
			return 0, err
		}
		id, isNew, err := s.wbSitePage(c, kit, p, pl.Slug, titleOf(p), parent)
		if err != nil {
			return 0, fmt.Errorf("%s: %v", p, err)
		}
		if isNew {
			created = append(created, p)
		} else {
			updated = append(updated, p)
		}
		ids[p] = id
		return id, nil
	}
	notFound := ""
	for _, p := range plan.Pages {
		if p.NotFound {
			notFound = p.Path
			continue
		}
		if _, err := ensurePage(p.Path, 0); err != nil {
			return nil, err
		}
	}
	// The URLs (as they will be once published).
	lb := s.links(c)
	home := strings.TrimSuffix(lb.HomeURL("/"), "/")
	frontID := int64(0)
	for _, p := range plan.Pages {
		if p.NotFound {
			continue
		}
		if p.Front {
			opts.URLs[p.Path] = webbuilder.ActivePath(lb.HomeURL("/"))
			frontID = ids[p.Path]
			continue
		}
		post, err := s.store.GetPost(c, ids[p.Path])
		if err != nil {
			return nil, err
		}
		cp := *post
		cp.Status = "publish"
		link := lb.Permalink(&cp)
		if strings.Contains(link, "?") {
			warnings = append(warnings, "the site has plain permalinks (?page_id=…): links between pages work, but the menu cannot mark the current page; choose a pretty permalink structure (Settings › Permalinks) and import again")
		}
		opts.URLs[p.Path] = strings.TrimPrefix(link, home)
		if !strings.HasPrefix(opts.URLs[p.Path], "/") {
			opts.URLs[p.Path] = "/" + opts.URLs[p.Path]
		}
	}
	// Pages at addresses the router gives to an archive (a WordPress copy's /author/admin/,
	// /category/news/, /2026/09/) claim them, so they answer there as in the design.
	claims := map[string]int64{}
	var claimed []string
	for _, p := range plan.Pages {
		if p.NotFound || p.Front {
			continue
		}
		if why := s.wbRouteShadowed(c, opts.URLs[p.Path]); why != "" {
			claims[opts.URLs[p.Path]] = ids[p.Path]
			claimed = append(claimed, opts.URLs[p.Path]+" ("+why+")")
		}
	}
	if err := s.wbSiteSetClaims(c, kit, claims); err != nil {
		warnings = append(warnings, "could not keep the pages' addresses from the archives: "+err.Error())
	} else if len(claimed) > 0 {
		sort.Strings(claimed)
		warnings = append(warnings, "these pages sit at addresses that are WordPress archives on this site; the imported pages answer there instead of the archive (page 1; /page/2/ and feeds stay the archive's): "+
			strings.Join(wbFirst(claimed, 20), ", "))
	}
	var list []webbuilder.SitePage
	for _, p := range plan.Pages {
		list = append(list, webbuilder.SitePage{Path: p.Path, HTML: src[p.Path]})
	}
	// The design's original site: its URLs are this site's paths, its endpoints never called.
	opts.OldHosts, opts.KeepOldLinks = wbSiteOldOptions(args, list)
	opts.SiteURL = home
	res, err := webbuilder.ConvertSite(list, opts)
	if err != nil {
		return nil, mcpErrf("the site could not be read: %v", err)
	}
	warnings = append(warnings, res.Warnings...)
	warnings = append(warnings, wbSiteOldWarnings(res, opts.OldHosts, opts.KeepOldLinks, k.Origins)...)
	// Binding to content (webbuilder_sitebind.go): the header's navigation as a menu, folders of
	// pages as content under single templates, lists of those pages as loops.
	menusOn, groups := wbSiteBindOptions(args)
	if noSplit {
		menusOn, groups = false, map[string]bool{}
	}
	parents := map[string]int64{}
	folderParents := map[string]string{}
	for f := range webbuilder.SiteFolders(res) {
		if id := ids["dir:"+f]; id > 0 {
			parents[f] = id
			folderParents[f] = itoa64(id)
		}
	}
	postSets := s.wbSitePostSets(res)
	binding := webbuilder.BindSite(res, webbuilder.SiteBindOptions{Menus: menusOn, Groups: groups, FolderParents: folderParents, PostSets: postSets})
	bindReport := map[string]any{}
	if m := binding.Menu; m != nil && len(res.Headers) > 0 && m.Section < len(res.Headers[0].Sections) {
		pageByURL := map[string]int64{}
		for p, u := range opts.URLs {
			pageByURL[webbuilder.ActivePath(u)] = ids[p]
		}
		menuID, itemIDs, err := s.wbSiteMenu(c, kit, m, pageByURL)
		if err != nil {
			warnings = append(warnings, "the navigation menu was not made: "+err.Error())
		} else {
			sec := &res.Headers[0].Sections[m.Section]
			sec.Binds = m.Binds(sec.Binds, webbuilder.MenuLocationPrimary, itemIDs)
			bindReport["menu"] = map[string]any{"id": menuID, "name": wbSiteMenuName(kit), "location": webbuilder.MenuLocationPrimary, "items": m.Items,
				"edit_url": s.mcpAdminURL(c, "/wp-admin/nav-menus?menu="+itoa64(menuID))}
		}
	}
	section := func(p webbuilder.CodePart, head, lang string, attrs [2]string, active *webbuilder.ActiveRule) map[string]any {
		sec := wbCodeSection(p, head, kit, lang)
		wbCodeSectionAttrs(sec, attrs[0], attrs[1], active)
		return sec
	}
	// The pages.
	out := map[string]any{"kit": kit}
	report := []map[string]any{}
	var pageFor = map[int]int64{}
	for i, pg := range res.Pages {
		if pg.Path == notFound {
			continue
		}
		id := ids[pg.Path]
		pageFor[i] = id
		cr := pg.Code
		if binding.GroupMember(i) {
			// Content under a single template (wbSiteGroups); its SEO is the page's still.
			if _, warn := s.wbImportSEO(c, id, cr.SEO); warn != "" {
				warnings = append(warnings, pg.Path+": "+warn)
			}
			continue
		}
		secs := make([]map[string]any, 0, len(cr.Sections))
		for _, p := range cr.Sections {
			secs = append(secs, section(p, cr.Head, cr.Lang, [2]string{cr.HTMLAttrs, cr.BodyAttrs}, nil))
		}
		d, doc, page, err := s.mcpLoadDoc(c, id, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", pg.Path, err)
		}
		doc.Sections = secs
		if doc.Site, err = mcpSite(r, doc.Site, nil); err != nil {
			return nil, err
		}
		title := strings.TrimSpace(cr.SEO.Title)
		if title == "" {
			title = titleOf(pg.Path)
		}
		pl := binding.Plans[i]
		if pl != nil && pl.Title != "" {
			title = pl.Title // a page a list shows: its title is its <h1>, as the list shows it
		}
		patch := map[string]any{"layout": "site", "title": truncRunes(title, 200)}
		if noSplit {
			patch["layout"] = "canvas"
		}
		if dsc := strings.TrimSpace(cr.SEO.Description); dsc != "" {
			patch["description"] = truncRunes(dsc, 300)
		}
		if strings.Contains(strings.ToLower(cr.SEO.Robots), "noindex") {
			patch["indexable"] = false
		}
		saved, err := s.mcpSaveDoc(c, id, d, doc, page, patch, status, nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", pg.Path, err)
		}
		if _, warn := s.wbImportSEO(c, id, cr.SEO); warn != "" {
			warnings = append(warnings, pg.Path+": "+warn)
		}
		if pl != nil {
			if err := s.wbSitePagePlan(c, id, pl, nil, status); err != nil {
				warnings = append(warnings, pg.Path+": "+err.Error())
			}
		}
		report = append(report, map[string]any{"path": pg.Path, "post_id": id, "url": saved["url"], "sections": len(secs)})
	}
	out["pages"] = report
	out["pages_created"], out["pages_updated"] = created, updated
	if len(claimed) > 0 {
		out["claimed_paths"] = claimed
	}
	if len(opts.OldHosts) > 0 {
		out["original_site"] = map[string]any{"domains": opts.OldHosts, "urls": len(res.OldURLs), "script_urls": res.OldInScripts,
			"forms": res.OldForms, "links": map[bool]string{true: "keep", false: "rewrite"}[opts.KeepOldLinks]}
	}
	if len(res.Trackers) > 0 {
		out["tracking"] = res.Trackers
	}

	// The header and footer templates.
	keep := map[string]bool{}
	tplReport := []map[string]any{}
	for _, kind := range []struct {
		typ  string
		list []webbuilder.SiteTemplate
	}{{"header", res.Headers}, {"footer", res.Footers}} {
		for ti, t := range kind.list {
			key := kind.typ + "-" + itoa(ti)
			keep[key] = true
			conds := []string{"include/general"}
			if ti == 0 {
				// Pages without this part in the design (a bare 404, a footer kept inside main).
				for _, pi := range res.Uncovered(kind.typ == "header") {
					if id, ok := pageFor[pi]; ok {
						conds = append(conds, "exclude/singular/page/"+itoa64(id))
					} else if res.Pages[pi].Path == notFound {
						conds = append(conds, "exclude/404")
					}
				}
			} else {
				conds = nil
				for _, pi := range t.Pages {
					if id, ok := pageFor[pi]; ok {
						conds = append(conds, "include/singular/page/"+itoa64(id))
					} else if res.Pages[pi].Path == notFound {
						conds = append(conds, "include/404")
					}
				}
			}
			secs := make([]map[string]any, 0, len(t.Sections))
			for j, p := range t.Sections {
				var rule *webbuilder.ActiveRule
				if j == 0 {
					rule = t.Rule
				}
				secs = append(secs, section(p, t.Head, t.Lang, t.Attrs, rule))
			}
			title := strings.TrimSpace(argStr(args, "site_name") + " " + webbuilder.TemplateTypeLabels[webbuilder.TemplateType(kind.typ)])
			if ti > 0 {
				title += " " + itoa(ti+1)
			}
			id, err := s.wbSiteTemplate(c, r, kit, key, kind.typ, title, conds, secs)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("the %s template %d was not saved: %v", kind.typ, ti+1, err))
				continue
			}
			e := map[string]any{"type": kind.typ, "id": id, "pages": len(t.Pages), "conditions": conds, "edit_url": s.mcpAdminURL(c, wbEditURL(id))}
			if t.Rule != nil {
				e["active_links"] = len(t.Rule.Items)
			}
			tplReport = append(tplReport, e)
		}
	}
	// The 404 template.
	if notFound != "" {
		for _, pg := range res.Pages {
			if pg.Path != notFound {
				continue
			}
			cr := pg.Code
			secs := make([]map[string]any, 0, len(cr.Sections))
			for _, p := range cr.Sections {
				secs = append(secs, section(p, cr.Head, cr.Lang, [2]string{cr.HTMLAttrs, cr.BodyAttrs}, nil))
			}
			keep["404"] = true
			id, err := s.wbSiteTemplate(c, r, kit, "404", string(webbuilder.Template404), strings.TrimSpace(argStr(args, "site_name")+" 404"), []string{"include/404"}, secs)
			if err != nil {
				warnings = append(warnings, "the 404 template was not saved: "+err.Error())
			} else {
				tplReport = append(tplReport, map[string]any{"type": string(webbuilder.Template404), "id": id, "conditions": []string{"include/404"}, "edit_url": s.mcpAdminURL(c, wbEditURL(id))})
			}
		}
	}
	// Single templates made from groups of pages.
	if len(binding.Groups) > 0 {
		gr, warn := s.wbSiteGroups(c, r, kit, status, res, binding, pageFor, parents, keep)
		warnings = append(warnings, warn...)
		bindReport["groups"] = gr
	}
	if len(binding.Listings) > 0 {
		var ls []map[string]any
		for _, l := range binding.Listings {
			ls = append(ls, map[string]any{"page": res.Pages[l.Page].Path, "post_id": pageFor[l.Page], "folder": l.Folder, "items": len(l.Members), "offset": l.Offset})
		}
		bindReport["listings"] = ls
	}
	if len(binding.Notes) > 0 {
		bindReport["notes"] = binding.Notes
	}
	if len(binding.Candidates) > 0 {
		bindReport["collection_candidates"] = binding.Candidates
	}
	if len(bindReport) > 0 {
		out["bindings"] = bindReport
	}
	out["templates"] = tplReport
	// Templates of an earlier import of this kit that this one no longer needs.
	if disabled := s.wbSiteDisableStale(c, kit, keep); len(disabled) > 0 {
		out["templates_disabled"] = disabled
	}
	if others := s.wbSiteOtherTemplates(c, kit); len(others) > 0 {
		warnings = append(warnings, "other published templates of the same types may compete with the imported ones (the imported ones have priority "+
			itoa(wbSiteTemplatePriority)+"): "+strings.Join(others, ", "))
	}
	// The design's folders are the site's paths now: its files are served at the root too.
	if err := s.wbKitSetRoot(c, kit, true); err != nil {
		warnings = append(warnings, "could not serve the kit's files at the site's root: "+err.Error())
	}
	if frontID > 0 && !(args["front_page"] == false) {
		if err := s.mcpREST(c, http.MethodPost, "/wp/v2/settings", map[string]any{"show_on_front": "page", "page_on_front": frontID}, nil); err != nil {
			warnings = append(warnings, "could not set the front page: "+err.Error())
		} else {
			out["front_page"] = frontID
		}
	}
	if s.activeTheme().Slug != wbHelloSlug {
		warnings = append(warnings, "the header, footer and 404 templates show only with the Hello Pursae theme (activate Pursae Web Builder's theme)")
	}
	codeRes := &webbuilder.CodeImport{}
	for _, pg := range res.Pages {
		if pg.Code != nil {
			codeRes.Sections = append(codeRes.Sections, pg.Code.Sections...)
			codeRes.Head += pg.Code.Head
		}
	}
	if w := s.wbCodeScriptsWarning(codeRes); w != "" {
		warnings = append(warnings, w)
	}
	if len(res.Unmapped) > 0 {
		out["unmapped_links"] = wbFirst(res.Unmapped, 40)
	}
	if len(res.Missing) > 0 {
		out["missing_files"] = wbFirst(res.Missing, 40)
	}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out, nil
}

// wbCodeSectionAttrs adds the design's document attributes and the active-link rule to a code
// section's content.
func wbCodeSectionAttrs(sec map[string]any, htmlAttrs, bodyAttrs string, active *webbuilder.ActiveRule) {
	content, _ := sec["content"].(map[string]any)
	if content == nil {
		return
	}
	if htmlAttrs != "" {
		content["htmlAttrs"] = htmlAttrs
	}
	if bodyAttrs != "" {
		content["bodyAttrs"] = bodyAttrs
	}
	if active != nil {
		content["active"] = active.String()
	}
}

// wbSiteTemplate saves a template of the import: found by its key (or created and marked), its
// display conditions set, its sections published.
func (s *Server) wbSiteTemplate(c *gin.Context, r *webbuilder.ServerRenderer, kit, key, typ, title string, conds []string, secs []map[string]any) (int64, error) {
	id := s.wbSiteOwned(c, webbuilder.TemplatePostType, kit, key)
	if id == 0 {
		if title == "" {
			title = typ
		}
		var created webbuilder.TemplateCreateResponse
		if err := s.mcpREST(c, http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": typ, "title": strings.ToUpper(title[:1]) + title[1:]}, &created); err != nil {
			return 0, err
		}
		id = created.ID
		if err := s.wbSiteMark(c, id, kit, key); err != nil {
			return id, err
		}
	}
	if err := s.mcpREST(c, http.MethodPut, "/web-builder/v2/templates/"+itoa64(id)+"/conditions", map[string]any{"conditions": conds, "priority": wbSiteTemplatePriority}, nil); err != nil {
		return id, err
	}
	d, doc, page, err := s.mcpLoadDoc(c, id, false)
	if err != nil {
		return id, err
	}
	doc.Sections = secs
	if doc.Site, err = mcpSite(r, doc.Site, nil); err != nil {
		return id, err
	}
	_, err = s.mcpSaveDoc(c, id, d, doc, page, nil, "publish", nil)
	return id, err
}

// wbSiteDisableStale turns the earlier import's templates that are not in keep into drafts.
func (s *Server) wbSiteDisableStale(c *gin.Context, kit string, keep map[string]bool) []string {
	rows, err := s.store.Pool.Query(c, `SELECT id, meta->>$2, title FROM posts WHERE type = $1 AND meta->>$3 = $4 AND status = 'publish'`,
		webbuilder.TemplatePostType, wbSiteMetaKey, wbSiteMetaKit, kit)
	if err != nil {
		return nil
	}
	type row struct {
		id    int64
		key   string
		title string
	}
	var stale []row
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.key, &x.title) == nil && !keep[x.key] {
			stale = append(stale, x)
		}
	}
	rows.Close()
	var out []string
	for _, x := range stale {
		if _, err := s.store.Pool.Exec(c, `UPDATE posts SET status = 'draft', modified_at = now(), updated_at = now() WHERE id = $1 AND type = $2`,
			x.id, webbuilder.TemplatePostType); err == nil {
			out = append(out, x.title)
		}
	}
	if len(out) > 0 {
		s.wbTemplatesChanged(c)
	}
	s.wbPurgePages()
	return out
}

// wbSiteOtherTemplates lists published header, footer and 404 templates the import did not make.
func (s *Server) wbSiteOtherTemplates(c *gin.Context, kit string) []string {
	all, err := s.store.WebBuilder().Templates(c, store.WBTemplateFilter{Statuses: []string{webbuilder.StatusPublish}})
	if err != nil {
		return nil
	}
	var out []string
	for _, t := range all {
		switch t.Type {
		case webbuilder.TemplateHeader, webbuilder.TemplateFooter, webbuilder.Template404:
		default:
			continue
		}
		var owner string
		_ = s.store.Pool.QueryRow(c, `SELECT coalesce(meta->>$2, '') FROM posts WHERE id = $1`, t.ID, wbSiteMetaKit).Scan(&owner)
		if owner == kit {
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s, #%d)", t.Title, t.Type, t.ID))
	}
	sort.Strings(out)
	return out
}
