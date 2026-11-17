package server

import (
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Pursae MCP: HTML designs imported as code sections (method "code" of web_builder_import_html)
// and the design kit tools. The design's markup stays as written (1:1 by construction); the editor
// edits its texts, links and images in place and reorders its parts. See
// docs/developer/web-builder-code-sections.md.

var wbCodeSeq atomic.Int64

// wbCodeID is a new section id (the contract's id pattern: letters, digits, - and _).
func wbCodeID() string {
	return "code-" + strconv.FormatInt(time.Now().UnixMilli(), 36) + "-" + strconv.FormatInt(wbCodeSeq.Add(1), 36)
}

// wbCodeSection is a code section of the document.
func wbCodeSection(p webbuilder.CodePart, head, kit, lang string) map[string]any {
	content := map[string]any{"html": p.HTML, "head": head, "kit": kit}
	if p.Wrap != "" {
		content["wrap"] = p.Wrap
	}
	if lang != "" {
		content["lang"] = lang
	}
	if p.Binds != "" {
		content[webbuilder.ContentBinds] = p.Binds
	}
	return map[string]any{
		"id": wbCodeID(), "kind": webbuilder.KindCode, "name": p.Name, "hidden": false, "content": content, "items": []any{},
		"style": map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}},
	}
}

func mcpWBImportCode(s *Server, c *gin.Context, args map[string]any) (any, error) {
	u := restCurrent(c)
	if u == nil || !u.Can("unfiltered_html", nil) {
		return nil, mcpErrf("method code keeps the design's own HTML, CSS and JavaScript: the account needs unfiltered_html (an administrator)")
	}
	if !restScopeSiteCode(c) {
		return nil, mcpErrf("method code keeps the design's own HTML, CSS and JavaScript: an Application Password needs the Appearance (themes) scope or full access for it; method exact works with Content")
	}
	status, err := mcpPublishStatus(args)
	if err != nil {
		return nil, err
	}
	opts := webbuilder.CodeImportOptions{Split: argBool(args, "templates"), LinkMap: map[string]string{}}
	kit := argStr(args, "kit")
	if kit != "" {
		k := s.wbKitList()[kit]
		if k == nil {
			return nil, mcpErrf("no design kit %q; upload the design's files first with web_builder_kit_upload", kit)
		}
		files, err := wbKitFiles(filepath.Join(s.wbKitRoot(), kit))
		if err != nil {
			return nil, err
		}
		opts.KitFiles = map[string]bool{}
		for p := range files {
			opts.KitFiles[p] = true
		}
		opts.KitURL = wbKitURLPrefix + kit + "/"
	}
	if p := argStr(args, "path"); p != "" {
		opts.Base = path.Dir(strings.TrimPrefix(p, "/"))
		if opts.Base == "." {
			opts.Base = ""
		}
	}
	for k, v := range argObj(args, "link_map") {
		if sv, ok := v.(string); ok {
			opts.LinkMap[k] = sv
		}
	}
	res, err := webbuilder.ConvertCode(argStr(args, "html"), opts)
	if err != nil {
		return nil, mcpErrf("the HTML could not be read: %v", err)
	}
	r, err := s.mcpRenderer()
	if err != nil {
		return nil, err
	}
	var warnings []string
	warnings = append(warnings, res.Warnings...)
	if len(res.Missing) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d referenced file(s) are not in the kit (upload them with web_builder_kit_upload and import again): %s",
			len(res.Missing), strings.Join(wbFirst(res.Missing, 12), ", ")))
	}
	if len(res.Unmapped) > 0 {
		warnings = append(warnings, fmt.Sprintf("%d link(s) point at the design's other HTML files; map them to site URLs with link_map: %s",
			len(res.Unmapped), strings.Join(wbFirst(res.Unmapped, 12), ", ")))
	}
	if w := s.wbCodeScriptsWarning(res); w != "" {
		warnings = append(warnings, w)
	}
	if opts.Split && s.activeTheme().Slug != wbHelloSlug {
		warnings = append(warnings, "the header and footer templates show only with the Hello Pursae theme (activate Pursae Web Builder's theme)")
	}
	secs := make([]map[string]any, 0, len(res.Sections))
	for _, p := range res.Sections {
		sec := wbCodeSection(p, res.Head, kit, res.Lang)
		wbCodeSectionAttrs(sec, res.HTMLAttrs, res.BodyAttrs, nil)
		secs = append(secs, sec)
	}
	out := map[string]any{"method": "code (the design's own markup)", "editable_elements": res.Editable}
	var pageID int64

	// The page (templates_only: the file only gives the site its header and footer, e.g. a design's
	// forum page whose content the forum itself renders).
	if argBool(args, "templates_only") && !opts.Split {
		return nil, mcpErrf("templates_only needs templates: true")
	}
	if len(secs) > 0 && !argBool(args, "templates_only") {
		if argStr(args, "title") == "" && argInt(args, "post_id") == 0 {
			args["title"] = res.SEO.Title
			if args["title"] == "" {
				args["title"] = "Imported page"
			}
		}
		id, createdRoute, err := s.mcpEnsurePost(c, args)
		if err != nil {
			return nil, err
		}
		d, doc, page, err := s.mcpLoadDoc(c, id, false)
		if err != nil {
			return nil, err
		}
		doc.Sections = secs
		if doc.Site, err = mcpSite(r, doc.Site, nil); err != nil {
			return nil, err
		}
		pagePatch := map[string]any{"layout": "canvas"}
		if opts.Split {
			pagePatch["layout"] = "site"
		}
		if t := argStr(args, "title"); t != "" {
			pagePatch["title"] = t
		}
		if dsc := strings.TrimSpace(res.SEO.Description); dsc != "" {
			pagePatch["description"] = truncRunes(dsc, 300)
		}
		if strings.Contains(strings.ToLower(res.SEO.Robots), "noindex") {
			pagePatch["indexable"] = false
		}
		saved, err := s.mcpSaveDoc(c, id, d, doc, page, pagePatch, status, nil)
		if err != nil {
			if createdRoute != "" {
				_ = s.mcpREST(c, http.MethodDelete, createdRoute+"/"+itoa64(id)+"?force=true", nil, nil)
			}
			return nil, err
		}
		saved["created"] = createdRoute != ""
		pageID = id
		out["page"] = saved
		if fields, warn := s.wbImportSEO(c, id, res.SEO); warn != "" {
			warnings = append(warnings, warn)
		} else if len(fields) > 0 {
			out["seo"] = fields
		}
		if argBool(args, "front_page") {
			if err := s.mcpREST(c, http.MethodPost, "/wp/v2/settings", map[string]any{"show_on_front": "page", "page_on_front": id}, nil); err != nil {
				warnings = append(warnings, "could not set the front page: "+err.Error())
			} else {
				out["front_page"] = true
			}
		}
	}
	// The header and footer templates (site-wide).
	sec := func(p webbuilder.CodePart) map[string]any {
		sec := wbCodeSection(p, res.Head, kit, res.Lang)
		wbCodeSectionAttrs(sec, res.HTMLAttrs, res.BodyAttrs, nil)
		return sec
	}
	var headerSecs, footerSecs []map[string]any
	if res.Header != nil {
		for _, p := range res.HeaderBefore {
			headerSecs = append(headerSecs, sec(p))
		}
		headerSecs = append(headerSecs, sec(*res.Header))
	}
	if res.Footer != nil {
		footerSecs = append(footerSecs, sec(*res.Footer))
		for _, p := range res.FooterAfter {
			footerSecs = append(footerSecs, sec(p))
		}
	}
	for _, x := range []struct {
		typ  string
		secs []map[string]any
		arg  string
	}{{"header", headerSecs, "header_template"}, {"footer", footerSecs, "footer_template"}} {
		if len(x.secs) == 0 {
			continue
		}
		var conds []string
		if x.typ == "footer" && res.FooterInPage && pageID > 0 {
			// The page keeps the footer where the design put it (inside main); the template serves the rest.
			conds = []string{"include/general", "exclude/singular/page/" + itoa64(pageID)}
		}
		tid, err := s.wbCodeTemplate(c, r, x.typ, argInt(args, x.arg), x.secs, argStr(args, "site_name"), conds)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("the %s template was not saved: %v", x.typ, err))
			continue
		}
		out[x.typ+"_template"] = map[string]any{"id": tid, "edit_url": s.mcpAdminURL(c, wbEditURL(tid))}
	}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out, nil
}

// wbCodeTemplate saves a header or footer template made of code sections: the template id given,
// or a new one shown on the whole site.
func (s *Server) wbCodeTemplate(c *gin.Context, r *webbuilder.ServerRenderer, typ string, id int64, secs []map[string]any, siteName string, conds []string) (int64, error) {
	if id == 0 && conds == nil {
		conds = []string{"include/general"}
	}
	if id == 0 {
		title := strings.TrimSpace(siteName + " " + typ)
		var created webbuilder.TemplateCreateResponse
		if err := s.mcpREST(c, http.MethodPost, "/web-builder/v2/templates", map[string]any{"type": typ, "title": strings.ToUpper(title[:1]) + title[1:]}, &created); err != nil {
			return 0, err
		}
		id = created.ID
	}
	if conds != nil {
		if err := s.mcpREST(c, http.MethodPut, "/web-builder/v2/templates/"+itoa64(id)+"/conditions", map[string]any{"conditions": conds, "priority": 0}, nil); err != nil {
			return id, err
		}
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

// wbImportSEO writes an imported page's search and social snippets to Pursae SEO (when it is on).
func (s *Server) wbImportSEO(c *gin.Context, id int64, seo webbuilder.ExtractSEO) (map[string]any, string) {
	if !s.seogoOn() {
		return nil, ""
	}
	fields := map[string]any{}
	if seo.Title != "" {
		fields["title"] = truncRunes(seo.Title, 120)
	}
	if seo.Description != "" {
		fields["description"] = truncRunes(seo.Description, 300)
	}
	if seo.OGTitle != "" {
		fields["og_title"] = truncRunes(seo.OGTitle, 120)
	}
	if seo.OGDescription != "" {
		fields["og_description"] = truncRunes(seo.OGDescription, 300)
	}
	img := seo.OGImage
	if strings.HasPrefix(img, "/") && !strings.HasPrefix(img, "//") {
		img = s.links(c).HomeURL(img)
	}
	if strings.HasPrefix(img, "https://") || strings.HasPrefix(img, "http://") {
		fields["og_image"] = img
	}
	if len(fields) == 0 {
		return nil, ""
	}
	if err := s.mcpREST(c, http.MethodPost, "/seogo/v1/meta/"+itoa64(id), map[string]any{"fields": fields}, nil); err != nil {
		return nil, "Pursae SEO fields were not saved: " + err.Error()
	}
	return fields, ""
}

func wbFirst(list []string, n int) []string {
	if len(list) > n {
		return append(append([]string{}, list[:n]...), "…")
	}
	return list
}

// mcpWBKitUpload stages files into a design kit and (commit: true, the default) publishes them.
func mcpWBKitUpload(s *Server, c *gin.Context, args map[string]any) (any, error) {
	slug := argStr(args, "kit")
	if !wbKitSlugRE.MatchString(slug) {
		return nil, mcpErrf("kit must be a slug: lowercase letters, digits and dashes (e.g. merge)")
	}
	files := argList(args, "files")
	del := asStrings(args["delete"])
	out := map[string]any{"kit": slug}
	if len(files) > 0 || len(del) > 0 || argBool(args, "fresh") {
		var res map[string]any
		body := map[string]any{"files": files, "delete": del, "fresh": argBool(args, "fresh")}
		if err := s.mcpREST(c, http.MethodPost, "/web-builder/v2/kits/"+slug+"/files", body, &res); err != nil {
			return nil, err
		}
		out["staged"] = res
	}
	commit := true
	if v, ok := args["commit"].(bool); ok {
		commit = v
	}
	if commit {
		var res map[string]any
		if err := s.mcpREST(c, http.MethodPost, "/web-builder/v2/kits/"+slug+"/commit", map[string]any{"name": argStr(args, "name")}, &res); err != nil {
			return nil, err
		}
		out["kit"] = res
	} else {
		out["note"] = "Staged; send more files, then call again with commit: true (no files) to publish them together."
	}
	return out, nil
}

// wbCodeScriptsWarning: a design's scripts get the response's CSP nonce only while the site trusts
// stored administrator scripts (csp_admin_scripts, csp_trusted.go). With that off they are blocked
// under the enforced nonce policy and reported under the report-only one.
func (s *Server) wbCodeScriptsWarning(res *webbuilder.CodeImport) string {
	if s.cspTrustOn() || s.cspMode() == cspModeOff || !wbCodeHasScript(res) {
		return ""
	}
	if s.cspMode() == cspModeEnforce {
		return "the site enforces its Content Security Policy and does not trust stored administrator scripts (csp_admin_scripts is off): " +
			"the design's scripts get no nonce and do not run; turn csp_admin_scripts on (Settings › Security) or the design works without its JavaScript"
	}
	return "the site does not trust stored administrator scripts (csp_admin_scripts is off): the design's scripts get no nonce, " +
		"so the report-only Content Security Policy reports them and enforcing it would block them"
}

// wbCodeHasScript reports whether the import has a <script> element.
func wbCodeHasScript(res *webbuilder.CodeImport) bool {
	has := func(src string) bool { return containsFold(src, "<script") }
	if has(res.Head) || (res.Header != nil && has(res.Header.HTML)) || (res.Footer != nil && has(res.Footer.HTML)) {
		return true
	}
	for _, p := range res.Sections {
		if has(p.HTML) {
			return true
		}
	}
	return false
}
