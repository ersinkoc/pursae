package server

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Pursae Web Builder's use of the local Google Fonts (gfonts.go): the families of the rendered
// documents and the kit, and the editor preview's font loading.

// gweKitFonts are the font families the site kit uses (global typography and theme styles).
func (s *Server) gweKitFonts(c *gin.Context) []string {
	kit, err := s.gweKit(c)
	if err != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if strings.HasSuffix(k, "font_family") {
					if f := gweStr(t[k]); f != "" {
						out = append(out, f)
					}
					continue
				}
				walk(t[k])
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(gweDocument(kit).Settings)
	return out
}

// gweFontsHead prints the local Google Fonts of the rendered documents and the kit (wp_head), and
// lets pages with a PayPal button submit to PayPal (gowebeditor_payments.go).
func (s *Server) gweFontsHead(c *gin.Context, results []gweRendered) string {
	gwePaymentsCSP(c, results)
	var fams []string
	for _, r := range results {
		if r.res != nil {
			fams = append(fams, r.res.Fonts...)
		}
	}
	fams = append(fams, s.gweKitFonts(c)...)
	return s.gweGoogleFontsHead(c, fams)
}

// gweFontsEnqueueAjax is the editor's enqueue_google_fonts ajax action: it downloads a family (if
// needed) and returns its local stylesheet so the preview can link it.
func (s *Server) gweFontsEnqueueAjax(c *gin.Context, data map[string]any) (any, error) {
	family := strings.TrimSpace(gweStr(data["font_name"]))
	if gweGoogleFontType(family) == "" || !s.gweGoogleFontsEnabled() {
		return false, nil
	}
	if !s.gweEnsureGoogleFont(family, 20*time.Second) {
		return false, nil
	}
	key := gweFontKey(family)
	cssPath := filepath.Join(s.gweFontsDir("css"), key+".css")
	st, err := os.Stat(cssPath)
	if err != nil {
		return false, nil
	}
	lb := s.links(c)
	base := "/wp-content/uploads/" + gweFontsBase
	out := map[string]any{"font": family, "id": "gwe-gf-local-" + key, "url": lb.HomeURL(base + "/css/" + key + ".css?ver=" + strconv.FormatInt(st.ModTime().Unix(), 10))}
	if b, err := os.ReadFile(cssPath); err == nil {
		if p := gweFontPreloadFile(string(b)); p != "" {
			out["preload"] = lb.HomeURL(base + "/fonts/" + p)
		}
	}
	return out, nil
}

// gweFontsEditorScript makes the editor's preview use the local copies: the font control marks every
// family as a system font (so the editor never links fonts.googleapis.com) and fires
// font:insertion, on which this script asks the server for the local stylesheet and links it.
func (s *Server) gweFontsEditorScript(c *gin.Context) string {
	if !s.gweGoogleFontsEnabled() {
		return ""
	}
	return `<script id="gwe-local-google-fonts-js">
(function () {
	var cache = {};
	function inject(doc, r) {
		if (!doc || !r || !r.url || doc.getElementById(r.id + '-css')) { return; }
		var head = doc.head || doc.getElementsByTagName('head')[0];
		if (r.preload) {
			var p = doc.createElement('link');
			p.rel = 'preload'; p.as = 'font'; p.type = 'font/woff2'; p.crossOrigin = 'anonymous'; p.href = r.preload;
			head.appendChild(p);
		}
		var l = doc.createElement('link');
		l.rel = 'stylesheet'; l.id = r.id + '-css'; l.href = r.url;
		head.appendChild(l);
	}
	function preview() {
		var c = window.elementor && elementor.$previewContents;
		return c && c[0] ? c[0] : null;
	}
	function load(font) {
		if (!font) { return; }
		if (cache[font]) {
			cache[font].then(function (r) { inject(preview(), r); });
			return;
		}
		cache[font] = new Promise(function (resolve) {
			elementorCommon.ajax.addRequest('enqueue_google_fonts', {
				data: { font_name: font },
				unique_id: 'gwe_local_font_' + font,
				success: function (r) { resolve(r); },
				error: function () { resolve(null); }
			}, true);
		});
		cache[font].then(function (r) { inject(preview(), r); });
	}
	jQuery(window).on('elementor:init', function () {
		elementor.channels.editor.on('font:insertion', function (type, font) { load(font); });
	});
})();
</script>
`
}
