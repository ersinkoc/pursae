package server

import (
	"bytes"
	"encoding/json"
	"hash/fnv"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
)

// Golementor in the user's language: the editor's scripts get the Elementor plugin pack's JSON
// translations (rebranded, i18n.rebrand), and the labels in its configuration (widgets,
// controls, panels) are looked up in the catalog, which holds the plugin's PHP strings.

// gweLocaleScript is the wp-i18n inline script of the editor: text direction, the English
// rebrand map, then the language pack for the "elementor" domain.
func (s *Server) gweLocaleScript(c *gin.Context, rebrand []byte) string {
	loc := s.userLocale(c)
	dir := "ltr"
	if i18n.IsRTL(loc) {
		dir = "rtl"
	}
	js := "wp.i18n.setLocaleData( { 'text direction\\u0004ltr': [ '" + dir + "' ] } );\nwp.i18n.setLocaleData( " + string(rebrand) + ", 'elementor' );"
	if tr := s.gweScriptTranslations(c); len(tr) > 0 {
		js += "\nwp.i18n.setLocaleData( " + string(tr) + ", 'elementor' );"
	}
	return js
}

// gweScriptTranslations is the merged JSON locale data of the Elementor pack for the user's
// language (nil for English or without the pack).
func (s *Server) gweScriptTranslations(c *gin.Context) []byte {
	if s.lang == nil {
		return nil
	}
	loc := s.userLocale(c)
	if loc == "en_US" {
		return nil
	}
	return s.lang.PluginScriptTranslations(loc, "elementor")
}

// gweLabelKeys are the configuration keys whose string values are UI text.
var gweLabelKeys = map[string]bool{"title": true, "label": true, "description": true, "placeholder": true, "label_on": true,
	"label_off": true, "help": true, "tooltip": true, "heading": true, "button_text": true, "text_on": true, "text_off": true}

// gweUserDataKeys hold the user's content: elements, saved values, titles, pages, templates.
var gweUserDataKeys = map[string]bool{"elements": true, "values": true, "post_title": true, "user": true, "pages": true,
	"templates": true, "revisions": true, "kit_config": true, "kits": true, "custom_fonts": true, "global_widgets": true,
	"widget_templates": true, "data": true, "preview_post": true}

var gweConfigCache sync.Map // locale + hash of the English JSON -> translated JSON

// gweTranslateConfig translates the UI labels of an editor configuration JSON. cache keeps the
// result for JSON that is the same on every request (the widgets config); the editor config
// carries nonces and is translated each time.
func (s *Server) gweTranslateConfig(c *gin.Context, raw []byte, cache bool) []byte {
	cat := s.catalog(c)
	if cat.Empty() || len(raw) == 0 {
		return raw
	}
	h := fnv.New64a()
	h.Write(raw)
	key := cat.Locale + ":" + strconv.FormatUint(h.Sum64(), 36)
	if v, ok := gweConfigCache.Load(key); ok && cache {
		return v.([]byte)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return raw
	}
	v = gweTranslateValue(cat, v, "")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return raw
	}
	// Like gweJSON: "</" stays escaped so a string never ends the inline script.
	out := bytes.ReplaceAll(bytes.TrimRight(buf.Bytes(), "\n"), []byte("</"), []byte(`<\/`))
	if cache {
		gweConfigCache.Store(key, out)
	}
	return out
}

func gweTranslateValue(cat *i18n.Catalog, v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if key == "options" { // select options: value -> label
				if str, ok := child.(string); ok {
					t[k] = gweTranslateLabel(cat, str)
					continue
				}
			}
			if gweUserDataKeys[k] {
				continue // the user's content and setting values are never translated
			}
			if m, ok := child.(map[string]any); ok && k == "settings" {
				if _, defs := m["controls"]; !defs {
					continue // saved values, not control definitions
				}
			}
			if str, ok := child.(string); ok && gweLabelKeys[k] {
				t[k] = gweTranslateLabel(cat, str)
				continue
			}
			t[k] = gweTranslateValue(cat, child, k)
		}
	case []any:
		for i, child := range t {
			t[i] = gweTranslateValue(cat, child, key)
		}
	}
	return v
}

func gweTranslateLabel(cat *i18n.Catalog, s string) string {
	core := strings.TrimSpace(s)
	if core == "" {
		return s
	}
	if tr, ok := cat.Lookup(core); ok {
		return tr
	}
	if strings.HasSuffix(core, ":") {
		if tr, ok := cat.Lookup(strings.TrimSuffix(core, ":")); ok {
			return tr + ":"
		}
	}
	return s
}

// Golementor Studio and the "Edit with Golementor" button are built in the browser, so their text
// is translated there: the server sends the translations of the UI strings in the Studio
// scripts, and an observer swaps them in Golementor's own elements (class "gwe-…") as they appear.

var (
	gweJSStringsOnce sync.Once
	gweJSStringsList []string
	gweJSLiteral     = regexp.MustCompile(`'((?:[^'\\\n]|\\.){2,160})'|"((?:[^"\\\n]|\\.){2,160})"|` + "`([^`$\\n]{2,160})`")
	gweJSNotText     = regexp.MustCompile(`[{}<>=;]|^\.|^#|gwe-|e-con|elementor|\\u|https?:|^[A-Z][a-z]+[A-Z]|^[A-Z_]+$`)
	gweJSHasWord     = regexp.MustCompile(`[A-Za-z]{2}`)
)

// gweJSStrings lists the UI strings of the Studio scripts (literals that start with a capital).
func (s *Server) gweJSStrings() []string {
	gweJSStringsOnce.Do(func() {
		seen := map[string]bool{"Edit with Golementor": true}
		files, _ := fs.Glob(s.web, "gowebeditor/studio/*.js")
		for _, f := range files {
			b, err := fs.ReadFile(s.web, f)
			if err != nil {
				continue
			}
			for _, m := range gweJSLiteral.FindAllStringSubmatch(string(b), -1) {
				lit := m[1] + m[2] + m[3]
				lit = strings.NewReplacer(`\'`, "'", `\"`, `"`).Replace(lit)
				t := strings.TrimSpace(lit)
				if t == "" || !gweJSHasWord.MatchString(t) || gweJSNotText.MatchString(t) || !strings.ContainsAny(t[:1], "ABCDEFGHIJKLMNOPQRSTUVWXYZ“\"(") {
					continue
				}
				seen[lit] = true
			}
		}
		for k := range seen {
			gweJSStringsList = append(gweJSStringsList, k)
		}
		sort.Strings(gweJSStringsList)
	})
	return gweJSStringsList
}

const gweL10nObserver = `(function(){var D=window.GWEL10n||{};var A=['title','placeholder','aria-label'];
function tr(s){if(!s)return s;var v=D[s];if(v)return v;var t=s.trim();if(t!==s&&D[t])return s.replace(t,D[t]);return s;}
function ours(n){var e=n.nodeType===1?n:n.parentElement;return !!(e&&e.closest&&e.closest('[class*="gwe-"],[id^="gwe"],[id^="gowebeditor"]')&&!e.closest('[translate="no"]'));}
function walk(n){if(n.nodeType===3){if(ours(n)){var v=tr(n.nodeValue);if(v!==n.nodeValue)n.nodeValue=v;}return;}
if(n.nodeType!==1||/^(SCRIPT|STYLE|TEXTAREA|IFRAME)$/.test(n.tagName))return;
if(ours(n)){for(var i=0;i<A.length;i++){var a=n.getAttribute(A[i]);if(a){var t=tr(a);if(t!==a)n.setAttribute(A[i],t);}}}
for(var c=n.firstChild;c;c=c.nextSibling)walk(c);}
function start(){walk(document.body);new MutationObserver(function(ms){for(var i=0;i<ms.length;i++){var m=ms[i];
if(m.type==='characterData'){walk(m.target);}else{for(var j=0;j<m.addedNodes.length;j++)walk(m.addedNodes[j]);}}})
.observe(document.body,{childList:true,subtree:true,characterData:true});}
if(document.body)start();else document.addEventListener('DOMContentLoaded',start);})();`

// gweL10nScript is the dictionary and observer for the user's language ("" for English).
func (s *Server) gweL10nScript(c *gin.Context) string {
	cat := s.catalog(c)
	if cat.Empty() {
		return ""
	}
	dict := map[string]string{}
	for _, lit := range s.gweJSStrings() {
		if tr, ok := cat.Lookup(lit); ok {
			dict[lit] = tr
		} else if t := strings.TrimSpace(lit); t != lit {
			if tr, ok := cat.Lookup(t); ok {
				dict[lit] = strings.Replace(lit, t, tr, 1)
			}
		}
	}
	if len(dict) == 0 {
		return ""
	}
	return "<script id=\"gwe-l10n\">window.GWEL10n = " + gweJSON(dict) + ";\n" + gweL10nObserver + "</script>\n"
}
