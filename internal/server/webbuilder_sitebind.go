package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// The bindings a site import makes (docs/developer/web-builder-code-sections.md, "Binding to
// content"; webbuilder.BindSite plans them): the header's navigation as a Pursae menu, groups of
// pages as content under single templates, and lists of those pages as loops.

// wbSiteBindOptions reads the import's binding options: menus (default on) and collections
// (true: every folder that qualifies; a list: those folders; default none).
func wbSiteBindOptions(args map[string]any) (menus bool, groups map[string]bool) {
	menus = args["menus"] != false
	groups = map[string]bool{}
	switch v := args["collections"].(type) {
	case bool:
		if v {
			groups["*"] = true
		}
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				groups[strings.Trim(strings.TrimSpace(s), "/")] = true
			}
		}
	case []string:
		for _, s := range v {
			groups[strings.Trim(strings.TrimSpace(s), "/")] = true
		}
	}
	return menus, groups
}

// wbSiteMenuName is the name of the menu an import of kit makes from the design's navigation.
func wbSiteMenuName(kit string) string { return "Design navigation (" + kit + ")" }

// wbSiteMenu creates (or refills) the import's menu from the plan, assigns it to the primary
// location and returns the items' ids in depth-first order and the menu id. pageByURL maps the
// imported pages' site paths to their ids (those items link the page, so they follow its URL).
func (s *Server) wbSiteMenu(c *gin.Context, kit string, m *webbuilder.SiteMenu, pageByURL map[string]int64) (int64, []int64, error) {
	var plan func([]webbuilder.SiteMenuItem) []store.MenuTreeItem
	plan = func(items []webbuilder.SiteMenuItem) []store.MenuTreeItem {
		out := make([]store.MenuTreeItem, 0, len(items))
		for _, it := range items {
			e := store.EditItem{Kind: "custom", Object: "custom", URL: it.URL,
				Label: it.Label, Target: it.Target, Status: "publish"}
			if id, ok := pageByURL[webbuilder.ActivePath(it.URL)]; ok && id > 0 && !strings.Contains(it.URL, "#") {
				e.Kind, e.Object, e.TargetID, e.URL = "post_type", "page", id, ""
			}
			out = append(out, store.MenuTreeItem{Item: e, Children: plan(it.Children)})
		}
		return out
	}
	return s.store.ReplaceNamedMenu(c, wbSiteMenuName(kit), webbuilder.MenuLocationPrimary, plan(m.Items))
}

var wbSiteFieldKeyRE = regexp.MustCompile(`^(_wb_html_)?(design|list\d+)_`)

// wbSitePageFields stores a page's custom fields (post meta) from the import, replacing the ones
// an earlier import of the design wrote (design_*, list<n>_*).
func (s *Server) wbSitePageFields(c *gin.Context, id int64, fields map[string]string) error {
	p, err := s.store.GetPost(c, id)
	if err != nil {
		return err
	}
	var drop []string
	for k := range p.Meta {
		if wbSiteFieldKeyRE.MatchString(k) {
			if _, keep := fields[k]; !keep {
				drop = append(drop, k)
			}
		}
	}
	if len(fields) == 0 && len(drop) == 0 {
		return nil
	}
	set := map[string]string{}
	for k, v := range fields {
		set[k] = v
	}
	raw, _ := json.Marshal(set)
	if drop == nil {
		drop = []string{}
	}
	_, err = s.store.Pool.Exec(c, `UPDATE posts SET meta = (meta - $2::text[]) || $3::jsonb WHERE id = $1`, id, drop, string(raw))
	return err
}

// wbSitePagePlan sets what a group or a listing needs on a page (or a post a collection made of
// it): title, excerpt, menu_order (the REST route, so the post's own hooks run; a post's order is
// set directly, the posts route has no menu_order) and custom fields.
func (s *Server) wbSitePagePlan(c *gin.Context, id int64, pl *webbuilder.SitePagePlan, content *string, status string) error {
	route := "/wp/v2/pages/"
	isPost := false
	if p, err := s.store.GetPost(c, id); err == nil && p.Type == "post" {
		route, isPost = "/wp/v2/posts/", true
	}
	body := map[string]any{}
	if pl.Title != "" {
		body["title"] = pl.Title
	}
	if pl.Excerpt != "" {
		body["excerpt"] = pl.Excerpt
	}
	if pl.HasOrder && !isPost {
		body["menu_order"] = pl.MenuOrder
	}
	if content != nil {
		body["content"] = *content
		body["status"] = status
	}
	if len(body) > 0 {
		if err := s.mcpREST(c, http.MethodPost, route+itoa64(id), body, nil); err != nil {
			return err
		}
	}
	if pl.HasOrder && isPost {
		// A list of posts loops newest first: the import dates them a second apart in the list's
		// order (the first is now), so a post written later comes first.
		at := time.Now().UTC().Truncate(time.Second).Add(-time.Duration(pl.MenuOrder) * time.Second)
		if _, err := s.store.Pool.Exec(c, `UPDATE posts SET menu_order = $2, published_at = $3 WHERE id = $1`, id, pl.MenuOrder, at); err != nil {
			return err
		}
	}
	return s.wbSitePageFields(c, id, pl.Fields)
}

// wbSitePostSets are the sets of pretty-permalink pages (webbuilder.SiteLeafSets) at the site's
// top that become posts in a collection: only when the site's post permalinks are /%postname%/,
// so a post keeps the page's address (/bursadan-dunyaya/ stays /bursadan-dunyaya/).
func (s *Server) wbSitePostSets(res *webbuilder.SiteImport) map[string]bool {
	out := map[string]bool{}
	if strings.Trim(s.settings.String("permalink_structure"), "/") != "%postname%" {
		return out
	}
	for k := range webbuilder.SiteLeafSets(res) {
		if parent, ok := webbuilder.SiteLeafParent(k); ok && parent == "" {
			out[k] = true
		}
	}
	return out
}

// mcpWBBind is the MCP tool web_builder_bind: binds an element of a code section (by its data-wbc
// id or its HTML id) to a menu, a loop, a post field or the form endpoint (null removes the
// binding), then publishes the page or template, like the element editor's binding panel does.
func mcpWBBind(s *Server, c *gin.Context, args map[string]any) (any, error) {
	if err := s.mcpNeedBuilder(); err != nil {
		return nil, err
	}
	if _, err := s.mcpRenderer(); err != nil {
		return nil, err
	}
	u := restCurrent(c)
	if !wbUnfiltered(c, u) {
		return nil, mcpErrf("bindings are part of a code section: the account needs unfiltered_html (an administrator) and, for a scoped application password, the themes scope")
	}
	id := argInt(args, "post_id")
	if id <= 0 {
		return nil, mcpErrf("post_id is required")
	}
	el, htmlID := strings.TrimSpace(argStr(args, "element")), strings.TrimSpace(argStr(args, "html_id"))
	if el == "" && htmlID == "" {
		return nil, mcpErrf("name the element: element (its data-wbc id) or html_id (its id attribute)")
	}
	var bind *webbuilder.CodeBind
	if raw, ok := args["bind"]; ok && raw != nil {
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &bind); err != nil || bind == nil {
			return nil, mcpErrf("bind must be an object {kind: menu|submenu|loop|field|form, …}")
		}
	}
	d, doc, page, err := s.mcpLoadDoc(c, id, true)
	if err != nil {
		return nil, err
	}
	for _, sec := range doc.Sections {
		if sec["kind"] != webbuilder.KindCode {
			continue
		}
		content, _ := sec["content"].(map[string]any)
		src, _ := content["html"].(string)
		wbc, ok := webbuilder.FindCodeElement(src, el, htmlID)
		if !ok {
			continue
		}
		existing, _ := content[webbuilder.ContentBinds].(string)
		binds := webbuilder.ParseCodeBinds(existing)
		if binds == nil {
			binds = webbuilder.CodeBinds{}
		}
		if bind == nil {
			delete(binds, wbc)
		} else {
			binds[wbc] = bind
		}
		js := binds.String()
		if msgs := webbuilder.CheckCodeBinds(js); len(msgs) > 0 {
			return nil, mcpErrf("%s", strings.Join(msgs, "; "))
		}
		if js == "" {
			delete(content, webbuilder.ContentBinds)
		} else {
			content[webbuilder.ContentBinds] = js
		}
		out, err := s.mcpSaveDoc(c, id, d, doc, page, nil, "publish", nil)
		if err != nil {
			return nil, err
		}
		out["section"], out["element"] = sec["id"], wbc
		if bind != nil && bind.Kind == webbuilder.BindForm {
			if sid, ok := sec["id"].(string); ok {
				out["form"] = webbuilder.BindKey(sid, wbc)
			}
		}
		return out, nil
	}
	return nil, mcpErrf("no element %q of a code section on page %d", el+htmlID, id)
}

// wbSiteGroups saves the groups: each page becomes content (no builder doc of its own, so the
// single template draws it), each group a single-page template (the largest of a folder for every
// child of the folder's parent page, the others for their own pages).
func (s *Server) wbSiteGroups(c *gin.Context, r *webbuilder.ServerRenderer, kit, status string, res *webbuilder.SiteImport, b *webbuilder.SiteBinding,
	pageFor map[int]int64, parents map[string]int64, keep map[string]bool) ([]map[string]any, []string, map[int]bool) {
	var report []map[string]any
	var warnings []string
	bound := map[int]bool{}
	// Largest group of each folder first.
	order := make([]int, len(b.Groups))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return len(b.Groups[order[i]].Posts) > len(b.Groups[order[j]].Posts) })
	firstOf := map[string]bool{}
	nth := map[string]int{}
	for _, gi := range order {
		g := &b.Groups[gi]
		nth[g.Folder]++
		key := "group-" + g.Folder + "-" + itoa(nth[g.Folder])
		var conds []string
		ptype, tplType := "page", webbuilder.TemplateSinglePage
		if g.AsPosts {
			ptype, tplType = "post", webbuilder.TemplateSinglePost
		}
		switch {
		case !firstOf[g.Folder] && g.AsPosts:
			// Pretty-permalink posts: the largest variant is every post's template (a post written
			// later gets it too).
			firstOf[g.Folder] = true
			conds = []string{"include/singular/post"}
		case !firstOf[g.Folder] && parents[g.Folder] > 0:
			firstOf[g.Folder] = true
			conds = []string{"include/singular/page/child_of/" + itoa64(parents[g.Folder])}
		default:
			for _, p := range g.Posts {
				if id := pageFor[p.Page]; id > 0 {
					conds = append(conds, "include/singular/"+ptype+"/"+itoa64(id))
				}
			}
		}
		if len(conds) == 0 || len(conds) > webbuilder.MaxConditions {
			warnings = append(warnings, fmt.Sprintf("the %s/ template %d has no display conditions; its pages were kept as pages", g.Folder, nth[g.Folder]))
			continue
		}
		secs := make([]map[string]any, 0, len(g.Sections))
		for j, p := range g.Sections {
			sec := wbCodeSection(p, g.Head, kit, g.Lang)
			var rule *webbuilder.ActiveRule
			if j == 0 {
				rule = g.Rule
			}
			wbCodeSectionAttrs(sec, g.Attrs[0], g.Attrs[1], rule)
			secs = append(secs, sec)
		}
		title := strings.ToUpper(g.Folder[:1]) + g.Folder[1:] + " page"
		if g.AsPosts {
			title = "Post"
		}
		if nth[g.Folder] > 1 {
			title += " " + itoa(nth[g.Folder])
		}
		// The pages of pretty-permalink posts become posts first (same address under /%postname%/).
		if g.AsPosts {
			for _, p := range g.Posts {
				if id := pageFor[p.Page]; id > 0 {
					if err := s.wbSiteSetType(c, id, "post"); err != nil {
						warnings = append(warnings, res.Pages[p.Page].Path+": could not make it a post: "+err.Error())
					}
				}
			}
		}
		id, err := s.wbSiteTemplate(c, r, kit, key, string(tplType), title, conds, secs)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("the %s/ template was not saved: %v", g.Folder, err))
			continue
		}
		keep[key] = true
		// The pages: content under the template.
		var pages []string
		for _, p := range g.Posts {
			pid := pageFor[p.Page]
			if pid == 0 {
				continue
			}
			pl := b.Plans[p.Page]
			if pl == nil {
				pl = &webbuilder.SitePagePlan{Title: p.Title, Fields: p.Fields}
			}
			content := p.Content
			if err := s.wbSitePagePlan(c, pid, pl, &content, status); err != nil {
				warnings = append(warnings, res.Pages[p.Page].Path+": "+err.Error())
				continue
			}
			if err := s.store.WebBuilder().DeleteDoc(c, pid); err != nil && !errors.Is(err, store.ErrNotFound) {
				warnings = append(warnings, res.Pages[p.Page].Path+": could not remove its builder page: "+err.Error())
				continue
			}
			bound[p.Page] = true
			pages = append(pages, res.Pages[p.Page].Path)
		}
		report = append(report, map[string]any{"folder": g.Folder, "template": id, "conditions": conds, "pages": pages, "fields": g.Fields,
			"type": ptype, "edit_url": s.mcpAdminURL(c, wbEditURL(id))})
	}
	return report, warnings, bound
}
