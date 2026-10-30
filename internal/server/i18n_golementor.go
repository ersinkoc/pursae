package server

import (
	"bytes"
	"encoding/json"
	"hash/fnv"
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
