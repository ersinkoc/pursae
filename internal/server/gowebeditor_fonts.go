package server

import (
	"fmt"
	"html"
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
	if gfontType(family) == "" || !s.gweGoogleFontsEnabled() {
		return false, nil
	}
	if !s.gfontEnsure(family, 20*time.Second) {
		return false, nil
	}
	key := gfontKey(family)
	cssPath := filepath.Join(s.gfontsDir("css"), key+".css")
	st, err := os.Stat(cssPath)
	if err != nil {
		return false, nil
	}
	lb := s.links(c)
	base := "/wp-content/uploads/" + gfontsBase
	out := map[string]any{"font": family, "id": "gwe-gf-local-" + key, "url": lb.HomeURL(base + "/css/" + key + ".css?ver=" + strconv.FormatInt(st.ModTime().Unix(), 10))}
	if b, err := os.ReadFile(cssPath); err == nil {
		if p := gfontPreloadFile(string(b)); p != "" {
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

// gweFontsWait is how long a page waits for a first download.
const gweFontsWait = 4 * time.Second

// gweGoogleFontsEnabled is the "Load Google Fonts Locally" option (default on).
func (s *Server) gweGoogleFontsEnabled() bool {
	return s.settings.String("gwe_local_google_fonts") != "0"
}

// gweLocalFontLinks returns the preload and stylesheet tags of a downloaded family ("" if missing).
func (s *Server) gweLocalFontLinks(c *gin.Context, family string) string {
	key := gfontKey(family)
	cssPath := filepath.Join(s.gfontsDir("css"), key+".css")
	st, err := os.Stat(cssPath)
	if err != nil {
		return ""
	}
	ver := strconv.FormatInt(st.ModTime().Unix(), 10)
	preload := ""
	if v, ok := gfontPreloadCache.Load(cssPath); ok && v.([2]string)[0] == ver {
		preload = v.([2]string)[1]
	} else if b, err := os.ReadFile(cssPath); err == nil {
		preload = gfontPreloadFile(string(b))
		gfontPreloadCache.Store(cssPath, [2]string{ver, preload})
	}
	lb := s.links(c)
	base := "/wp-content/uploads/" + gfontsBase
	var sb strings.Builder
	if preload != "" {
		fmt.Fprintf(&sb, "<link rel=\"preload\" href=\"%s\" as=\"font\" type=\"font/woff2\" crossorigin>\n", html.EscapeString(lb.HomeURL(base+"/fonts/"+preload)))
	}
	fmt.Fprintf(&sb, "<link rel='stylesheet' id='%s-css' href='%s' media='all' />\n", "gwe-gf-local-"+key, html.EscapeString(lb.HomeURL(base+"/css/"+key+".css?ver="+ver)))
	return sb.String()
}

// gweGoogleFontsHead returns the <link> tags that load the given families from this site,
// downloading them first when needed. It is the entry point for other code (classic widgets, the
// kit, atomic elements): families outside the Google Fonts catalogue are skipped, duplicates are
// printed once per request.
func (s *Server) gweGoogleFontsHead(c *gin.Context, families []string) string {
	if !s.gweGoogleFontsEnabled() {
		return ""
	}
	done := map[string]bool{}
	if c != nil {
		if v, ok := c.Get("gwe.fonts.printed"); ok {
			done = v.(map[string]bool)
		}
	}
	var sb strings.Builder
	for _, f := range families {
		f = strings.TrimSpace(strings.Trim(strings.TrimSpace(f), `"'`))
		key := gfontKey(f)
		if done[key] || gfontType(f) == "" {
			continue
		}
		done[key] = true
		if s.gfontEnsure(f, gweFontsWait) {
			sb.WriteString(s.gweLocalFontLinks(c, f))
		}
	}
	if c != nil {
		c.Set("gwe.fonts.printed", done)
	}
	return sb.String()
}

// --- settings ------------------------------------------------------------------------------------------

func (s *Server) gweFontsSettingsSection(c *gin.Context) string {
	on, off := " selected", ""
	if !s.gweGoogleFontsEnabled() {
		on, off = "", " selected"
	}
	n := 0
	if entries, err := os.ReadDir(s.gfontsDir("css")); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".css") {
				n++
			}
		}
	}
	return `<h2 id="fonts">Fonts</h2><table class="form-table" role="presentation"><tbody>` +
		`<tr><th scope="row"><label for="gwe_local_google_fonts">Load Google Fonts Locally</label></th><td><select id="gwe_local_google_fonts" name="gwe_local_google_fonts">` +
		`<option value="1"` + on + `>Enable</option><option value="0"` + off + `>Disable</option></select>` +
		`<p class="description">Google Fonts used on the site are downloaded once by the server and served from this site (font-display: swap), so visitors' browsers never contact Google. Disable to not load Google Fonts at all; pages then use the fallback fonts.</p></td></tr>` +
		`<tr><th scope="row">Downloaded families</th><td>` + strconv.Itoa(n) + ` <label style="margin-left:12px"><input type="checkbox" name="gwe_google_fonts_clear" value="1"/> Delete the downloaded files (they are fetched again when needed)</label></td></tr>` +
		`</tbody></table>`
}

func (s *Server) gweFontsSaveSettings(c *gin.Context, vals map[string]any) {
	v := c.PostForm("gwe_local_google_fonts")
	if v == "0" || v == "1" {
		vals["gwe_local_google_fonts"] = v
	}
	if c.PostForm("gwe_google_fonts_clear") == "1" {
		_ = os.RemoveAll(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(gfontsBase)))
		gfontPreloadCache.Range(func(k, _ any) bool { gfontPreloadCache.Delete(k); return true })
		gfontFailures.Range(func(k, _ any) bool { gfontFailures.Delete(k); return true })
	}
}
