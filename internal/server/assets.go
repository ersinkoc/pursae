package server

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
)

// wp-registry.json is WordPress 7.1.3's own script and style registry (wp_default_scripts(),
// wp_default_packages(), wp_default_styles()), exported by tools/wp-registry/registry.php.
// Pages enqueue handles exactly like WordPress does and the loader prints them in
// dependency order, so the vendored core JavaScript behaves as it does in WordPress.
//
//go:embed defaults/wp-registry.json
var registryJSON []byte

type regScript struct {
	Src          any      `json:"src"` // string or false for alias handles
	Deps         []string `json:"deps"`
	Ver          any      `json:"ver"`
	Footer       bool     `json:"footer"`
	Data         any      `json:"data"`
	Before       any      `json:"before"`
	After        any      `json:"after"`
	Translations bool     `json:"translations"`
	Strategy     any      `json:"strategy"`
}

type regStyle struct {
	Src   any      `json:"src"`
	Deps  []string `json:"deps"`
	Ver   any      `json:"ver"`
	After any      `json:"after"`
}

var registry struct {
	Scripts map[string]*regScript `json:"scripts"`
	Styles  map[string]*regStyle  `json:"styles"`
}

func init() {
	if err := json.Unmarshal(registryJSON, &registry); err != nil {
		panic("wp-registry.json: " + err.Error())
	}
	// Handles WordPress Go adds on top of core.
	registry.Styles["wpgo-admin-clone"] = &regStyle{Src: "/wp-admin/assets/css/admin-overrides.css", Ver: Version}
}

// assets collects enqueued handles for one page, like the global $wp_scripts/$wp_styles.
type assets struct {
	scripts []string
	styles  []string
	inline  map[string][]string // extra inline JS printed after a handle
	before  map[string][]string
}

func newAssets() *assets {
	return &assets{inline: map[string][]string{}, before: map[string][]string{}}
}

func (a *assets) Script(handles ...string) *assets {
	a.scripts = append(a.scripts, handles...)
	return a
}
func (a *assets) Style(handles ...string) *assets { a.styles = append(a.styles, handles...); return a }

// AddInline is wp_add_inline_script( handle, js, 'after' ).
func (a *assets) AddInline(handle, js string) *assets {
	a.inline[handle] = append(a.inline[handle], js)
	return a
}

func (a *assets) AddBefore(handle, js string) *assets {
	a.before[handle] = append(a.before[handle], js)
	return a
}

// resolve orders handles so dependencies come first (WP_Dependencies::all_deps()).
func resolve(handles []string, deps func(string) ([]string, bool)) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(h string)
	visit = func(h string) {
		if seen[h] {
			return
		}
		seen[h] = true
		d, ok := deps(h)
		if !ok {
			return
		}
		for _, dep := range d {
			visit(dep)
		}
		out = append(out, h)
	}
	for _, h := range handles {
		visit(h)
	}
	return out
}

func srcString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func verString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprint(t)
	}
	return "7.1.3"
}

// inlineList normalizes the registry's before/after values (false or a list with a leading false).
func inlineList(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

var noncePlaceholder = regexp.MustCompile(`NONCE:([A-Za-z0-9_\-]+)`)

// scriptTags prints the enqueued scripts of one group (header or footer) the way
// WP_Scripts::do_item() does: extra data, before, the tag, after.
func (s *Server) scriptTags(c *gin.Context, a *assets, footer bool) template.HTML {
	order := resolve(a.scripts, func(h string) ([]string, bool) {
		r, ok := registry.Scripts[h]
		if !ok {
			return nil, false
		}
		return r.Deps, true
	})
	printed := c.GetStringMap("wpgo.printedScripts")
	if printed == nil {
		printed = map[string]any{}
		c.Set("wpgo.printedScripts", printed)
	}
	var sb strings.Builder
	for _, h := range order {
		r := registry.Scripts[h]
		if _, done := printed[h]; done {
			continue
		}
		// Header pass prints everything not marked footer; the footer pass prints the rest.
		if !footer && r.Footer {
			continue
		}
		printed[h] = true
		if d, ok := r.Data.(string); ok && d != "" {
			fmt.Fprintf(&sb, "<script id=\"%s-js-extra\">\n%s\n</script>\n", h, s.fillPlaceholders(c, h, d))
		}
		before := append(inlineList(r.Before), a.before[h]...)
		if len(before) > 0 {
			fmt.Fprintf(&sb, "<script id=\"%s-js-before\">\n%s\n</script>\n", h, s.fillPlaceholders(c, h, strings.Join(before, "\n")))
		}
		if src := s.assetSrc(srcString(r.Src)); src != "" {
			sb.WriteString(s.scriptTranslations(c, h, src))
			extra := ""
			if st, ok := r.Strategy.(string); ok && st != "" {
				extra = fmt.Sprintf(" %s data-wp-strategy=\"%s\"", st, st)
			}
			fmt.Fprintf(&sb, "<script src=\"%s?ver=%s\" id=\"%s-js\"%s></script>\n", html.EscapeString(src), html.EscapeString(verString(r.Ver)), h, extra)
		}
		after := append(s.afterFor(c, h, inlineList(r.After)), a.inline[h]...)
		if len(after) > 0 {
			fmt.Fprintf(&sb, "<script id=\"%s-js-after\">\n%s\n</script>\n", h, s.fillPlaceholders(c, h, strings.Join(after, "\n")))
		}
	}
	return template.HTML(sb.String())
}

// styleTags prints enqueued stylesheets in dependency order (WP_Styles::do_items()).
func (s *Server) styleTags(a *assets) template.HTML {
	order := resolve(a.styles, func(h string) ([]string, bool) {
		r, ok := registry.Styles[h]
		if !ok {
			return nil, false
		}
		return r.Deps, true
	})
	var sb strings.Builder
	for _, h := range order {
		r := registry.Styles[h]
		if src := s.assetSrc(srcString(r.Src)); src != "" {
			fmt.Fprintf(&sb, "<link rel='stylesheet' id='%s-css' href='%s?ver=%s' media='all' />\n", h, html.EscapeString(src), html.EscapeString(verString(r.Ver)))
		}
		if after := inlineList(r.After); len(after) > 0 {
			fmt.Fprintf(&sb, "<style id='%s-inline-css'>\n%s\n</style>\n", h, strings.Join(after, "\n"))
		}
	}
	return template.HTML(sb.String())
}

// afterFor replaces the per-request inline scripts that WordPress builds from the
// current user and site settings.
func (s *Server) afterFor(c *gin.Context, handle string, def []string) []string {
	switch handle {
	case "wp-api-fetch":
		return []string{
			`wp.apiFetch.use( wp.apiFetch.createRootURLMiddleware( "` + s.links(c).HomeURL("/wp-json/") + `" ) );`,
			"wp.apiFetch.nonceMiddleware = wp.apiFetch.createNonceMiddleware( \"" + s.nonce(c, "wp_rest") + "\" );\n" +
				"wp.apiFetch.use( wp.apiFetch.nonceMiddleware );\nwp.apiFetch.use( wp.apiFetch.mediaUploadMiddleware );\n" +
				"wp.apiFetch.nonceEndpoint = \"/wp-admin/admin-ajax?action=rest-nonce\";",
		}
	case "wp-preferences":
		u := currentUser(c)
		if u == nil {
			return def
		}
		data := "null"
		if p, ok := u.Meta["persisted_preferences"]; ok {
			if b, err := json.Marshal(p); err == nil {
				data = string(b)
			}
		}
		return []string{fmt.Sprintf(`( function() {
				var serverData = %s;
				var userId = "%d";
				var persistenceLayer = wp.preferencesPersistence.__unstableCreatePersistenceLayer( serverData, userId );
				var preferencesStore = wp.preferences.store;
				wp.data.dispatch( preferencesStore ).setPersistenceLayer( persistenceLayer );
			} ) ();`, data, u.ID)}
	case "wp-date":
		return []string{s.dateSettingsJS()}
	case "wp-i18n":
		if loc := s.userLocale(c); loc != "en_US" && !s.catalog(c).Empty() {
			// The language pack names the editor; keep only the text direction.
			dir := "ltr"
			if i18n.IsRTL(loc) {
				dir = "rtl"
			}
			return []string{`wp.i18n.setLocaleData( { "text direction\u0004ltr": [ "` + dir + `" ] } );`}
		}
		return []string{gotenbergLocale}
	}
	return def
}

// fillPlaceholders swaps the exporter's placeholders for live values.
func (s *Server) fillPlaceholders(c *gin.Context, handle, js string) string {
	js = noncePlaceholder.ReplaceAllStringFunc(js, func(m string) string {
		return s.nonce(c, strings.TrimPrefix(m, "NONCE:"))
	})
	if handle == "utils" {
		uid := int64(0)
		if u := currentUser(c); u != nil {
			uid = u.ID
		}
		js = fmt.Sprintf(`var userSettings = {"url":"\/","uid":"%d","time":"%d","secure":""};`, uid, time.Now().Unix())
	}
	return strings.NewReplacer("SITEURL", s.links(c).Home, "HOMEURL", s.links(c).Home, "RESTURL", s.links(c).HomeURL("/wp-json/"),
		"MAXUPLOAD", fmt.Sprint(maxUploadBytes)).Replace(js)
}

// dateSettingsJS is the wp.date.setSettings() call with the site's formats and time zone.
func (s *Server) dateSettingsJS() string {
	loc := s.loc()
	now := time.Now().In(loc)
	name, offset := now.Zone()
	settings := map[string]any{
		"l10n": map[string]any{
			"locale":        "en_US",
			"months":        []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
			"monthsShort":   []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
			"weekdays":      []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"},
			"weekdaysShort": []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
			"meridiem":      map[string]string{"am": "am", "pm": "pm", "AM": "AM", "PM": "PM"},
			"relative": map[string]string{"future": "%s from now", "past": "%s ago", "s": "a second", "ss": "%d seconds", "m": "a minute",
				"mm": "%d minutes", "h": "an hour", "hh": "%d hours", "d": "a day", "dd": "%d days", "M": "a month", "MM": "%d months", "y": "a year", "yy": "%d years"},
			"startOfWeek": s.settings.Int("start_of_week"),
		},
		"formats": map[string]string{
			"time":                s.settings.String("time_format"),
			"date":                s.settings.String("date_format"),
			"datetime":            s.settings.String("date_format") + " " + s.settings.String("time_format"),
			"datetimeAbbreviated": "M j, Y " + s.settings.String("time_format"),
		},
		"timezone": map[string]any{
			"offset":          float64(offset) / 3600,
			"offsetFormatted": strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%+.2f", float64(offset)/3600), "0"), ".0"),
			"string":          s.settings.String("timezone_string"),
			"abbr":            name,
		},
	}
	b, _ := json.Marshal(settings)
	return "wp.date.setSettings( " + string(b) + " );"
}

// maxUploadBytes is the upload limit shown to the media uploader and enforced by the server.
const maxUploadBytes = 64 << 20

// gotenbergLocale keeps WordPress' text direction setting and renames a few block
// editor strings: in WordPress Go the block editor is called Gotenberg.
const gotenbergLocale = `wp.i18n.setLocaleData( {
	"text direction\u0004ltr": [ "ltr" ],
	"Welcome to the editor": [ "Welcome to Gotenberg" ],
	"In the WordPress editor, each paragraph, image, or video is presented as a distinct \u201cblock\u201d of content.": [ "In Gotenberg, each paragraph, image, or video is a distinct \u201cblock\u201d of content \u2014 compiled, never interpreted." ],
	"New to the block editor? Want to learn more about using it? <a>Here's a detailed guide.</a>": [ "New to Gotenberg? It speaks fluent Gutenberg, just with fewer PHP warnings. <a>Here's a detailed guide.</a>" ]
} );`

// assetSrc maps a registered source to the vendored file: only minified copies are
// shipped, so "foo.js" falls back to "foo.min.js" when that is what exists.
func (s *Server) assetSrc(src string) string {
	if src == "" || !strings.HasPrefix(src, "/") {
		return src
	}
	if _, err := fs.Stat(s.web, strings.TrimPrefix(src, "/")); err == nil {
		return src
	}
	for _, ext := range []string{".js", ".css"} {
		if strings.HasSuffix(src, ext) && !strings.HasSuffix(src, ".min"+ext) {
			min := strings.TrimSuffix(src, ext) + ".min" + ext
			if _, err := fs.Stat(s.web, strings.TrimPrefix(min, "/")); err == nil {
				return min
			}
		}
	}
	return src
}

// importMap is WP_Script_Modules::print_import_map(): bare specifiers such as
// "@wordpress/latex-to-mathml" that the editors import dynamically, mapped to the bundled files.
func (s *Server) importMap() string {
	if v, ok := importMapCache.Load().(string); ok && s.cfg.WebDir == "" {
		return v
	}
	v := s.buildImportMap()
	importMapCache.Store(v)
	return v
}

var importMapCache atomic.Value

func (s *Server) buildImportMap() string {
	imports := map[string]string{}
	_ = fs.WalkDir(s.web, "wp-includes/js/dist/script-modules", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(p) != "index.min.js" {
			return nil
		}
		dir := strings.TrimPrefix(path.Dir(p), "wp-includes/js/dist/script-modules/")
		imports["@wordpress/"+dir] = "/" + p + "?ver=" + Version
		return nil
	})
	if len(imports) == 0 {
		return ""
	}
	raw, _ := json.Marshal(map[string]any{"imports": imports})
	return `<script type="importmap" id="wp-importmap">` + string(raw) + "</script>\n"
}
