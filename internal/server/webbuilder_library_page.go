package server

import (
	"html"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Web Builder › Theme Library: the ready-made themes of the Pursae Manager, importable from the admin
// as well as from the editor. The gallery is the editor's own (builder/host/theme-library.ts): the
// browser draws and publishes each imported template (contract §4), so the screen loads the editor
// bundle in "library mode". The bundle's stylesheet is a full-page reset, so it never touches the
// admin page: the admin screen holds an iframe and the iframe is a bare document of its own.

const wbLibraryPageSlug = "web-builder-library"

// wbLibraryURL is Web Builder › Theme Library (site-relative).
func wbLibraryURL() string { return "/wp-admin/themes?page=" + wbLibraryPageSlug }

// wbLibraryPage runs the Theme Library screen when the Themes request asks for it
// (themes?page=web-builder-library; themesScreen calls it).
func (s *Server) wbLibraryPage(c *gin.Context) bool {
	if c.Query("page") != wbLibraryPageSlug {
		return false
	}
	u := currentUser(c)
	if !s.wbOn() || u == nil || !u.Can(webbuilder.TemplateCap, nil) {
		s.wbTBDeny(c)
		return true
	}
	if c.Query("frame") == "1" {
		s.wbLibraryFrame(c)
		return true
	}
	s.adminPage(c, "themes?page="+wbLibraryPageSlug, "Theme Library", "appearance_page_"+wbLibraryPageSlug,
		"admin/web-builder-library", map[string]any{"FrameURL": wbLibraryURL() + "&frame=1"})
	return true
}

// wbLibraryBoot is the small window.__WEB_BUILDER__ of the library frame: no document, only what the
// REST client and the renderer check need.
type wbLibraryBoot struct {
	APIVersion      int                  `json:"apiVersion"`
	RendererVersion int                  `json:"rendererVersion"`
	REST            webbuilder.BootREST  `json:"rest"`
	Links           webbuilder.BootLinks `json:"links"`
	Locale          string               `json:"locale"`
}

// wbLibraryFrame writes the bare document the admin screen embeds.
func (s *Server) wbLibraryFrame(c *gin.Context) {
	s.wbBundleFrame(c, "__WEB_BUILDER_LIBRARY__", "Theme Library")
}

// wbBundleFrame writes a bare document that loads the editor bundle in a screen mode: the page sets
// window.<flag> and ./editor-entry loads that screen instead of the editor (Theme Library,
// regenerate).
func (s *Server) wbBundleFrame(c *gin.Context, flag, title string) {
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
	m, err := s.wbManifest()
	if err == nil {
		var prefix string
		var entry webbuilder.ManifestEntry
		if prefix, entry, err = wbEditorBundle(m); err == nil {
			lb := s.links(c)
			boot := wbLibraryBoot{
				APIVersion: webbuilder.APIVersion, RendererVersion: webbuilder.RendererVersion,
				REST:   webbuilder.BootREST{Root: lb.HomeURL("/wp-json/"), Namespace: webbuilder.RESTNamespace, Nonce: s.nonce(c, "wp_rest")},
				Links:  webbuilder.BootLinks{Site: strings.TrimRight(lb.HomeURL("/"), "/")},
				Locale: s.userLocale(c),
			}
			lang := strings.ReplaceAll(boot.Locale, "_", "-")
			c.Data(http.StatusOK, "text/html; charset=UTF-8", []byte(wbLibraryHTML(lang, s.links(c).HomeURL(prefix), entry, wbScriptJSON(boot), flag, title)))
			return
		}
	}
	c.String(http.StatusInternalServerError, "Pursae Web Builder is not built: "+err.Error())
}

// wbLibraryHTML is the library frame document. Links leave the frame (<base target="_top">).
func wbLibraryHTML(lang, base string, entry webbuilder.ManifestEntry, boot, flag, title string) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"" + html.EscapeString(lang) + "\" dir=\"ltr\">\n<head>\n")
	b.WriteString("<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta name=\"robots\" content=\"noindex, nofollow\">\n<base target=\"_top\">\n<title>" + html.EscapeString(title) + "</title>\n")
	for _, css := range entry.CSS {
		b.WriteString("<link rel=\"stylesheet\" href=\"" + html.EscapeString(base+css) + "\">\n")
	}
	b.WriteString("</head>\n<body>\n<div id=\"root\"></div>\n<noscript>Pursae Web Builder needs JavaScript.</noscript>\n")
	b.WriteString("<script>window." + flag + " = true; window." + webbuilder.BootGlobal + " = " + boot + ";</script>\n")
	b.WriteString("<script type=\"module\" src=\"" + html.EscapeString(base+entry.JS) + "\"></script>\n")
	b.WriteString("</body>\n</html>\n")
	return b.String()
}
