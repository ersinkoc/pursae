package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/blocks"
	"wpgo/internal/store"
)

// The Classic Editor ships as a native plugin: wp_editor() with TinyMCE and Quicktags, exactly as
// WordPress prints it (exported from class-wp-editor.php by tools/wp-registry/editor.php).

//go:embed defaults/classic-editor.json
var classicEditorJSON []byte

var classicEditorOnce sync.Once
var classicEditorParts struct {
	Markup string `json:"markup"`
	JS     string `json:"js"`
	Link   string `json:"link"`
}

func classicEditor() (markup, js, link string) {
	classicEditorOnce.Do(func() { _ = json.Unmarshal(classicEditorJSON, &classicEditorParts) })
	return classicEditorParts.Markup, classicEditorParts.JS, classicEditorParts.Link
}

const tinymceVersion = "49110-20250317"

// wpEditor returns the editor markup for the post content and queues TinyMCE's settings, scripts and the
// link dialog after the footer scripts (admin_print_footer_scripts priority 50 in WordPress).
func (s *Server) wpEditor(c *gin.Context, p *store.Post) template.HTML {
	markup, js, link := classicEditor()
	markup = strings.Replace(markup, "POSTCONTENT", html.EscapeString(p.Content), 1)

	bodyClass := "content post-type-" + p.Type + " post-status-" + p.Status
	if p.Format != "" && p.Format != "standard" {
		bodyClass += " post-format-" + p.Format
	}
	js = strings.Replace(js, "content post-type-post post-status-draft", bodyClass, 1)
	// Like class-wp-editor.php: 'wpautop' => ! has_blocks(), so block markup survives the visual editor.
	if blocks.HasBlocks(p.Content) {
		js = strings.Replace(js, "wpautop:true", "wpautop:false", 1)
	}
	theme := s.activeTheme()
	themeCSS := ""
	for _, f := range theme.EditorStyles {
		themeCSS += "," + theme.URI() + "/" + f + "?ver=" + theme.Version
	}
	themeCSS += ",/wp-includes/css/wpgo-editor-neutral.css?ver=1"
	js = strings.Replace(js, `wp-content.css?ver=7.1.3"`, `wp-content.css?ver=7.1.3`+themeCSS+`"`, 1)
	js = strings.Replace(js, "<script>\ntinymce.addI18n",
		`<script src="/wp-includes/js/tinymce/wp-tinymce.js?ver=`+tinymceVersion+`"></script>`+"\n<script>\ntinymce.addI18n", 1)
	link = strings.Replace(link, "NONCE:internal-linking", s.nonce(c, "internal-linking"), 1)
	addLateFooterHTML(c, template.HTML(js+"\n"+link))

	a := s.pageAssets(c)
	a.Script("editor", "quicktags", "wplink", "jquery-ui-autocomplete", "word-count", "post", "editor-expand")
	// wp_editor() enqueues editor-buttons after <head> is printed, so WordPress prints it as a late
	// style in the footer. That puts it after the TinyMCE skin (which TinyMCE appends to <head>), and
	// its .mce-btn.mce-active rules win over the skin's white active icons.
	if r := registry.Styles["editor-buttons"]; r != nil {
		addLateFooterHTML(c, template.HTML(fmt.Sprintf("<link rel='stylesheet' id='editor-buttons-css' href='%s?ver=%s' media='all' />\n",
			html.EscapeString(s.assetSrc(srcString(r.Src))), html.EscapeString(verString(r.Ver)))))
	}
	return template.HTML(markup)
}

// addLateFooterHTML queues markup printed after the footer scripts.
func addLateFooterHTML(c *gin.Context, h template.HTML) {
	prev, _ := c.Get("wpgo.lateFooterHTML")
	cur, _ := prev.(template.HTML)
	c.Set("wpgo.lateFooterHTML", cur+h)
}

// classicEditorEnabled reports whether the Classic Editor plugin is active.
func (s *Server) classicEditorEnabled() bool {
	return s.settings.String("classic-editor-replace") == "classic" || s.settings.String("classic-editor-allow-users") == "allow"
}
