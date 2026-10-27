package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/store"
)

// Golementor Pro front end: Pro's stylesheets and scripts (frontend.min.js, elements-handlers,
// the widget styles, sticky/motion/popup/transitions assets), the Pro frontend config, the popups
// printed in the footer and the custom code snippets.

// gweProStyleFiles maps Pro style handles to files under web/gowebeditor/pro/.
var gweProStyleFiles = map[string]string{
	"e-popup": "css/conditionals/popup.min.css", "e-transitions": "css/conditionals/transitions.min.css", "e-ribbon": "css/conditionals/ribbon.min.css",
	"e-sticky": "css/modules/sticky.min.css", "e-motion-fx": "css/modules/motion-fx.min.css", "e-loop-grid-cta": "css/modules/loop-grid-cta.min.css",
	"pro-editor-preview": "css/preview.min.css", "elementor-pro-css": "css/editor.min.css",
}

// gweProCoreStyles are style handles Pro widgets add that live in Golementor's core assets.
var gweProCoreStyles = map[string]string{"flatpickr": "lib/flatpickr/flatpickr.min.css"}

// gweProScriptFiles maps Pro script handles to files (under pro/ unless absolute).
var gweProScriptFiles = map[string]string{
	"smartmenus": "lib/smartmenus/jquery.smartmenus.min.js", "e-sticky": "lib/sticky/jquery.sticky.min.js", "elementor-sticky": "lib/sticky/jquery.sticky.min.js",
	"lottie": "lib/lottie/lottie.min.js", "instant-page": "lib/instant-page/instant-page.min.js", "page-transitions": "js/page-transitions.min.js",
	"flatpickr": "/wp-content/plugins/gowebeditor/assets/lib/flatpickr/flatpickr.min.js",
	"elementor-pro-collection-loop-pagination-handler": "js/collection-loop-pagination-handler.min.js",
	// reCAPTCHA (only printed when the site has keys under Golementor › Settings › Integrations)
	"elementor-recaptcha-api":    "https://www.google.com/recaptcha/api.js?render=explicit",
	"elementor-recaptcha_v3-api": "https://www.google.com/recaptcha/api.js?render=explicit",
}

// gweProScriptOrder is the print order of the optional Pro scripts.
var gweProScriptOrder = []string{"smartmenus", "lottie", "flatpickr", "elementor-recaptcha-api", "elementor-recaptcha_v3-api", "instant-page",
	"page-transitions", "e-sticky", "elementor-sticky"}

// gweProStyleHref resolves a Pro style handle to a path under the Golementor assets; "" = not Pro's.
func (s *Server) gweProStyleHref(handle string) string {
	if rel, ok := gweProCoreStyles[handle]; ok {
		return rel
	}
	rel, ok := gweProStyleFiles[handle]
	if !ok && strings.HasPrefix(handle, "widget-") {
		rel = "css/" + handle + ".min.css"
	}
	if rel == "" {
		return ""
	}
	if !s.webFileExists("gowebeditor/pro/" + rel) {
		return ""
	}
	return "pro/" + rel
}

// gweProHead prints Pro's stylesheets: the preview gets every widget style, pages get the
// stylesheets of the popups they carry and the custom code head snippets.
func (s *Server) gweProHead(c *gin.Context, results []gweRendered, preview bool) string {
	assets := s.links(c).HomeURL(gweAssetsPath)
	ver := "?ver=" + gweVersion
	var sb strings.Builder
	if css := s.gweProFontFaceCSS(); css != "" { // Custom Fonts
		sb.WriteString("<style id=\"gwe-custom-fonts\">\n" + css + "</style>\n")
	}
	sb.WriteString(s.gweCustomIconsHead(c, results, preview)) // gap-widgets: Custom Icons stylesheets (gwe_custom_icons.go)
	if preview {
		for _, st := range gwePro.previewStyles {
			if s.webFileExists("gowebeditor/pro/" + st[1]) {
				sb.WriteString(gweLink(st[0], assets+"pro/"+st[1]+ver))
			}
		}
		for _, h := range []string{"e-popup", "e-motion-fx", "e-sticky", "e-transitions", "e-ribbon", "pro-editor-preview"} {
			sb.WriteString(gweLink(h, assets+"pro/"+gweProStyleFiles[h]+ver))
		}
		sb.WriteString(gweLink("flatpickr", "/wp-content/plugins/gowebeditor/assets/lib/flatpickr/flatpickr.min.css"+ver))
		return sb.String()
	}
	if len(s.gweProPopupsFor(c, results)) > 0 { // their CSS files and widget styles come with the recorded results
		sb.WriteString(gweLink("e-popup", assets+gweProStyleFiles["e-popup"]+ver))
	}
	sb.WriteString(s.gweProSnippets(c, "head"))
	return sb.String()
}

// gweProFooter prints the Pro frontend config, Pro's frontend scripts and the popups.
func (s *Server) gweProFooter(c *gin.Context, results []gweRendered, preview bool, scripts map[string]bool) string {
	assets := s.links(c).HomeURL(gweAssetsPath)
	ver := "?ver=" + gweVersion
	var sb strings.Builder
	popups := s.gweProPopupsFor(c, results)
	if !preview {
		// gap-widgets: "body start" snippets print at wp_body_open (siteView.BodyOpen), not here.
		for _, p := range popups {
			sb.WriteString(s.gweProPopupMarkup(c, p, scripts))
		}
		sb.WriteString(gweScript("wp-hooks", "/wp-includes/js/dist/hooks.min.js"))
		sb.WriteString(gweScript("wp-i18n", "/wp-includes/js/dist/i18n.min.js"))
		sb.WriteString("<script id=\"wp-i18n-js-after\">\nwp.i18n.setLocaleData( { 'text direction\\u0004ltr': [ 'ltr' ] } );\n</script>\n")
	}
	if preview {
		scripts["smartmenus"], scripts["lottie"], scripts["flatpickr"], scripts["e-sticky"] = true, true, true, true
		sb.WriteString(gweScript("elementor-pro-preview", assets+"pro/js/preview.min.js"+ver))
	}
	gwePrismScripts(scripts, preview) // gap-widgets: Code Highlight's local Prism (gwe_prism.go)
	for _, h := range gweProScriptOrder {
		if !scripts[h] {
			continue
		}
		src := gweProScriptFiles[h]
		if strings.HasPrefix(src, "https://") {
			if s.settings.String("gwe_recaptcha_site_key") == "" && s.settings.String("gwe_recaptcha_v3_site_key") == "" {
				continue
			}
			sb.WriteString(gweScript(h, src))
			continue
		}
		if !strings.HasPrefix(src, "/") {
			src = assets + "pro/" + src + ver
		} else {
			src += ver
		}
		sb.WriteString(gweScript(h, src))
		if h == "e-sticky" {
			scripts["elementor-sticky"] = false
		}
	}
	cfg := make(map[string]any, len(gwePro.frontCfg)+4)
	for k, v := range gwePro.frontCfg {
		cfg[k] = v
	}
	cfg["nonce"] = s.nonce(c, "elementor-pro-frontend")
	cfg["popup"] = map[string]any{"hasPopUps": len(popups) > 0 || preview}
	sb.WriteString("<script id=\"elementor-pro-frontend-js-before\">\nvar ElementorProFrontendConfig = " + s.gweFill(c, gweJSON(cfg)) + ";\n</script>\n")
	// Code Highlight's handler calls Prism, which upstream loads from a CDN; Golementor loads no
	// third-party scripts, so the code shows unhighlighted.
	sb.WriteString("<script id=\"gowebeditor-prism-shim\">window.Prism = window.Prism || { highlightAllUnder: function() {}, highlightElement: function() {} };</script>\n")
	sb.WriteString(gweScript("elementor-pro-frontend", assets+"pro/js/frontend.min.js"+ver))
	sb.WriteString(gweScript("pro-elements-handlers", assets+"pro/js/elements-handlers.min.js"+ver))
	if !preview {
		sb.WriteString(s.gweProSnippets(c, "body_end"))
	}
	return sb.String()
}

// --- popups --------------------------------------------------------------------------------------

var gweProPopupActionRe = regexp.MustCompile(`elementor-action(?:%3A|:)action(?:%3D|=)popup(?:%3A|:)open(?:%26|&)settings(?:%3D|=)([A-Za-z0-9%=+/]+)`)

// gweProPopupsFor lists the published popups to print on this page: those whose display
// conditions match the request, plus those the page's links open.
func (s *Server) gweProPopupsFor(c *gin.Context, results []gweRendered) []*store.Post {
	if v, ok := c.Get("gwe.pro.popups"); ok {
		return v.([]*store.Post)
	}
	var out []*store.Post
	seen := map[int64]bool{}
	rows, err := s.store.Pool.Query(c, `SELECT id FROM posts WHERE type = $1 AND status = 'publish' AND meta->>'_elementor_template_type' = 'popup' ORDER BY id`, gweLibrary)
	if err == nil {
		var ids []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		v, _ := c.Get(ctxGweView)
		view, _ := v.(*siteView)
		for _, id := range ids {
			p, err := s.store.GetPost(c, id)
			if err != nil {
				continue
			}
			conds := gweConditionsOf(p)
			if len(conds) > 0 && view != nil && view.Q != nil && gweMatch(conds, view.Q) > 0 {
				out = append(out, p)
				seen[p.ID] = true
			}
		}
	}
	// popups opened by links in the rendered content
	for _, r := range results {
		if r.res == nil {
			continue
		}
		for _, m := range gweProPopupActionRe.FindAllStringSubmatch(r.res.HTML, -1) {
			raw := strings.ReplaceAll(strings.ReplaceAll(m[1], "%3D", "="), "%2B", "+")
			b, err := base64Decode(raw)
			if err != nil {
				continue
			}
			var st struct {
				ID any `json:"id"`
			}
			if json.Unmarshal(b, &st) != nil {
				continue
			}
			id := gweInt(st.ID)
			if id == 0 || seen[id] {
				continue
			}
			if p, err := s.store.GetPost(c, id); err == nil && p.Status == "publish" && gweDocType(p) == "popup" {
				out = append(out, p)
				seen[id] = true
			}
		}
	}
	c.Set("gwe.pro.popups", out)
	return out
}

// gweProRenderPopup renders a popup document (cached per request) and writes its CSS file.
func (s *Server) gweProRenderPopup(c *gin.Context, p *store.Post) *builder.Result {
	key := "gwe.pro.popup." + itoa64(p.ID)
	if v, ok := c.Get(key); ok {
		return v.(*builder.Result)
	}
	res, err := builder.Render(gweDocument(p), s.gweRenderContext(c, false))
	if err != nil || res == nil {
		return nil
	}
	res.HTML, res.CSS = s.gweFixURLs(c, res.HTML), s.gweFixURLs(c, res.CSS)
	s.gweWriteCSS(p, res.CSS)
	c.Set(key, res)
	return res
}

// gweProPopupMarkup mirrors Theme_Document::print_elements_with_wrapper for a popup: the wrapper
// with data-elementor-settings (animations, triggers, timing) the frontend turns into a dialog.
func (s *Server) gweProPopupMarkup(c *gin.Context, p *store.Post, scripts map[string]bool) string {
	res := s.gweProRenderPopup(c, p)
	if res == nil {
		return ""
	}
	for _, h := range res.Scripts {
		scripts[h] = true
	}
	doc := gweDocument(p)
	settings := builder.DocumentFrontendSettings(doc, s.gweRenderContext(c, false))
	ds := gweProPopupDisplaySettings(p)
	byCondition := len(gweConditionsOf(p)) > 0
	if byCondition {
		settings["triggers"] = gweProFrontendGroup(ds["triggers"])
	}
	settings["timing"] = gweProFrontendGroup(ds["timing"])
	sj, _ := json.Marshal(settings)
	inner := res.HTML
	// Render gives the document wrapper; the popup needs its own attributes on it.
	if i := strings.Index(inner, ">"); i > 0 && strings.HasPrefix(inner, "<div ") {
		inner = inner[i+1:]
	}
	inner = strings.TrimSuffix(strings.TrimRight(inner, "\n\t "), "</div>")
	return fmt.Sprintf("<div data-elementor-type=\"popup\" data-elementor-id=\"%d\" class=\"elementor elementor-%d elementor-location-popup\" data-elementor-settings=\"%s\">\n%s\n</div>\n",
		p.ID, p.ID, htmlEscape(string(sj)), inner)
}

// gweProFrontendGroup keeps the enabled trigger/timing groups' settings (the popup frontend reads
// the raw saved values; empty values are dropped like get_frontend_settings).
func gweProFrontendGroup(v any) map[string]any {
	out := map[string]any{}
	m, _ := v.(map[string]any)
	for k, x := range m {
		if x == nil || x == "" {
			continue
		}
		out[k] = x
	}
	return out
}

func base64Decode(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	for len(s)%4 != 0 {
		s += "="
	}
	return base64.StdEncoding.DecodeString(s)
}

// --- custom code ---------------------------------------------------------------------------------

// gweProSnippets prints the published custom code snippets of a location (head, body_start,
// body_end) whose display conditions match the page, by priority.
func (s *Server) gweProSnippets(c *gin.Context, location string) string {
	rows, err := s.store.Pool.Query(c, `SELECT id FROM posts WHERE type = $1 AND status = 'publish' AND meta->>'_elementor_template_type' = 'code_snippet'
		AND coalesce(meta->>'_gwe_code_location','head') = $2 ORDER BY coalesce((meta->>'_gwe_code_priority')::int, 5), id`, gweLibrary, location)
	if err != nil {
		return ""
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if len(ids) == 0 {
		return ""
	}
	v, _ := c.Get(ctxGweView)
	view, _ := v.(*siteView)
	var sb strings.Builder
	for _, id := range ids {
		p, err := s.store.GetPost(c, id)
		if err != nil {
			continue
		}
		conds := gweConditionsOf(p)
		if len(conds) == 0 {
			conds = []string{"include/general"}
		}
		if view == nil || view.Q == nil || gweMatch(conds, view.Q) == 0 {
			continue
		}
		code, _ := p.Meta["_gwe_code"].(string)
		if code == "" {
			code = p.Content
		}
		sb.WriteString("<!-- Golementor custom code: " + html.EscapeString(p.Title) + " -->\n" + code + "\n")
	}
	return sb.String()
}

// --- global widgets REST -----------------------------------------------------------------------------

// gweProGlobalWidgetTemplatesREST answers elementor/v1/global-widget/templates?ids=1,2 with each
// template's widget element.
func (s *Server) gweProGlobalWidgetTemplatesREST(c *gin.Context) {
	out := map[string]any{}
	u := restCurrent(c)
	for _, raw := range strings.Split(c.Query("ids"), ",") {
		id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if id == 0 {
			continue
		}
		p, err := s.store.GetPost(c, id)
		if err != nil || (!u.Can("edit_post", p) && p.Status != "publish") {
			continue
		}
		var els []json.RawMessage
		if json.Unmarshal(gweElementsRaw(p), &els) != nil || len(els) == 0 {
			continue
		}
		out[itoa64(id)] = els[0]
	}
	c.JSON(http.StatusOK, out)
}

// gweProRefreshLoop is POST elementor-pro/v1/refresh-loop: a loop widget re-rendered with the
// taxonomy filter selections ({widget_filters: {taxonomy: {<tax>: {terms: [slugs]}}}}) and the
// pagination base URL.
func (s *Server) gweProRefreshLoop(c *gin.Context, r *restRequest) {
	postID, _ := r.Int(c, "post_id")
	widgetID, _ := r.Str(c, "widget_id")
	p, err := s.store.GetPost(c, postID)
	if err != nil || !gweBuilt(p) || (p.Status != "publish" && !currentUser(c).Can("edit_post", p)) {
		restErr(c, http.StatusNotFound, "rest_not_found", "Post not found.")
		return
	}
	doc := gweDocument(p)
	el := builder.FindElement(doc.Elements, widgetID)
	if el == nil || el.ElType != "widget" {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid widget.")
		return
	}
	filters := map[string]string{}
	if raw, ok := r.get(c, "widget_filters"); ok {
		var m map[string]any
		switch v := raw.(type) {
		case map[string]any:
			m = v
		case string:
			_ = json.Unmarshal([]byte(v), &m)
		}
		if tx, ok := m["taxonomy"].(map[string]any); ok {
			for tax, f := range tx {
				fm, _ := f.(map[string]any)
				var slugs []string
				for _, t := range asAnyList(fm["terms"]) {
					slugs = append(slugs, gweStr(t))
				}
				filters["e-filter-"+widgetID+"-"+tax] = strings.Join(slugs, ",")
			}
		}
	}
	rc := s.gweRenderContext(c, false)
	base := rc.Pro.Request
	rc.Pro.Request = func(kind, name string) string {
		if v, ok := filters[name]; ok && kind == "get" {
			return v
		}
		return base(kind, name)
	}
	if u, ok := r.Str(c, "pagination_base_url"); ok && u != "" {
		rc.Pro.CurrentURL = u
	}
	c.JSON(http.StatusOK, map[string]any{"data": s.gweFixURLs(c, builder.RenderWidgetFront(el, doc, rc))})
}

// gweProFrontScripts adds the core script handles Pro needs before the core footer prints them:
// popups use the dialog library and bring their widgets' scripts.
func (s *Server) gweProFrontScripts(c *gin.Context, results []gweRendered, preview bool, scripts map[string]bool) {
	if preview {
		scripts["elementor-dialog"] = true
		scripts["imagesloaded"] = true
		return
	}
	popups := s.gweProPopupsFor(c, results)
	if len(popups) > 0 {
		scripts["elementor-dialog"] = true
	}
	for _, p := range popups {
		if res := s.gweProRenderPopup(c, p); res != nil {
			for _, h := range res.Scripts {
				scripts[h] = true
			}
		}
	}
}

// gweProRecordPopups renders the page's popups and records them with the page's Golementor
// documents, so wp_head and wp_footer print their CSS files, widget styles and scripts (and the
// frontend stack on pages that are not built with Golementor).
func (s *Server) gweProRecordPopups(c *gin.Context) {
	if c == nil || gwePreviewing(c) {
		return
	}
	if _, done := c.Get("gwe.pro.popups.recorded"); done {
		return
	}
	c.Set("gwe.pro.popups.recorded", true)
	for _, p := range s.gweProPopupsFor(c, gweResults(c)) {
		if res := s.gweProRenderPopup(c, p); res != nil {
			s.gweRecord(c, p, res)
		}
	}
}
