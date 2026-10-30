package i18n

import (
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// layer is one translation file of a catalog.
type layer struct {
	domain string // "default" for core, else the plugin slug, "wpgo" for WordPress Go's own strings
	mo     *moFile
}

// Catalog is every installed translation of one locale, searched core first, then the plugin
// packs, then WordPress Go's own strings.
type Catalog struct {
	Locale string
	layers []layer

	patOnce  sync.Once
	patterns map[string][]*pattern // by the first word of the pattern's longest literal
	markup   map[string][]*pattern // messages with HTML markup, matched against inner HTML

	ctxOnce sync.Once
	anyCtx  map[string]string // msgid -> translation of its first contextual entry
	norm    map[string]string // msgid with entities decoded and whitespace collapsed -> translation
}

// Empty reports whether nothing is installed for the locale (English, or a missing pack).
func (c *Catalog) Empty() bool { return c == nil || len(c.layers) == 0 }

func (c *Catalog) find(key string) (entry, *pluralRule) {
	if c == nil {
		return nil, nil
	}
	for _, l := range c.layers {
		if e, ok := l.mo.entries[key]; ok {
			return e, l.mo.plural
		}
	}
	return nil, nil
}

// T translates msgid (WordPress' __()).
func (c *Catalog) T(msgid string) string {
	if e, _ := c.find(msgid); e != nil {
		return e[0]
	}
	return msgid
}

// X translates msgid in a context (_x()).
func (c *Catalog) X(msgid, ctx string) string {
	if e, _ := c.find(ctx + ctxSep + msgid); e != nil {
		return e[0]
	}
	return msgid
}

// N picks the singular or plural translation for n (_n()).
func (c *Catalog) N(single, plural string, n int) string {
	if e, rule := c.find(single); e != nil {
		if i := rule.index(int64(n)); i >= 0 && i < len(e) && e[i] != "" {
			return e[i]
		}
	}
	if n == 1 {
		return single
	}
	return plural
}

// lookup is T without the fallback: ok is false when nothing translates msgid. Rendered text
// carries no context, so a msgid only known with one ("Add New Post" in "post") still matches.
func (c *Catalog) lookup(msgid string) (string, bool) {
	if e, _ := c.find(msgid); e != nil && e[0] != msgid {
		return e[0], true
	}
	c.ctxOnce.Do(func() {
		c.anyCtx, c.norm = map[string]string{}, map[string]string{}
		for i := len(c.layers) - 1; i >= 0; i-- { // earlier layers win
			for k, e := range c.layers[i].mo.entries {
				id := k
				if _, after, ok := strings.Cut(k, ctxSep); ok {
					id = after
					if e[0] != id {
						c.anyCtx[id] = e[0]
					}
				}
				if n := normalizeMsgid(id); n != id && e[0] != id {
					c.norm[n] = e[0]
				}
			}
		}
	})
	if tr, ok := c.anyCtx[msgid]; ok {
		return tr, true
	}
	// "What&#8217;s new" in the catalog, "What’s new" on the page.
	if tr, ok := c.norm[normalizeMsgid(msgid)]; ok {
		return tr, true
	}
	return c.lookupUpstream(msgid)
}

// upstreamNames maps WordPress Go's product names to the upstream ones its strings were written
// after: "You have the latest version of WordPress Go." is WordPress' message with the name changed.
var upstreamNames = []struct{ ours, theirs string }{
	{"WordPress Go", "WordPress"}, {"Golementor Pro", "Elementor Pro"}, {"Golementor", "Elementor"}, {"Gontact Form", "Contact Form 7"},
	{"SEO-Go", "Yoast SEO"}, {"SEO-Go", "Rank Math"}, {"Golylang", "Polylang"}, {"WPGo Cache", "LiteSpeed Cache"},
	{"WPGo Mail SMTP", "WP Mail SMTP"}, {"ACF-Go", "ACF"}, {"ACF-Go", "Advanced Custom Fields"}, {"Gotenberg", "Gutenberg"},
}

func (c *Catalog) lookupUpstream(msgid string) (string, bool) {
	for _, n := range upstreamNames {
		if !strings.Contains(msgid, n.ours) {
			continue
		}
		up := strings.ReplaceAll(msgid, n.ours, n.theirs)
		if n.ours == "WordPress Go" && strings.Contains(msgid, "WordPress Go") {
			up = strings.ReplaceAll(msgid, "WordPress Go", "WordPress")
		}
		tr, ok := c.lookupDirect(up)
		if !ok {
			continue
		}
		if n.ours == "WordPress Go" {
			return strings.ReplaceAll(tr, "WordPress", "WordPress Go"), true
		}
		return strings.ReplaceAll(rebrandString(tr), n.theirs, n.ours), true
	}
	return "", false
}

// lookupDirect is lookup without the upstream-name fallback.
func (c *Catalog) lookupDirect(msgid string) (string, bool) {
	if e, _ := c.find(msgid); e != nil && e[0] != msgid {
		return e[0], true
	}
	if tr, ok := c.anyCtx[msgid]; ok {
		return tr, true
	}
	if tr, ok := c.norm[normalizeMsgid(msgid)]; ok {
		return tr, true
	}
	return "", false
}

// Lookup returns the translation of msgid (in any context) and whether one exists.
func (c *Catalog) Lookup(msgid string) (string, bool) { return c.lookup(msgid) }

var wsRun = regexp.MustCompile(`\s+`)

func normalizeMsgid(s string) string {
	return strings.TrimSpace(wsRun.ReplaceAllString(html.UnescapeString(s), " "))
}

// Manager loads catalogs from a WordPress-style languages directory (WP_LANG_DIR): core files
// at the top, plugin packs in plugins/. Extra holds WordPress Go's own translations
// (wpgo-<locale>.mo), shipped inside the binary.
type Manager struct {
	Dir   string
	Extra fs.FS

	mu   sync.Mutex
	cats map[string]*Catalog
}

func NewManager(dir string, extra fs.FS) *Manager {
	return &Manager{Dir: dir, Extra: extra, cats: map[string]*Catalog{}}
}

// Catalog returns the (cached) catalog of locale; English has an empty one.
func (m *Manager) Catalog(locale string) *Catalog {
	if locale == "" || locale == "en_US" || !validLocale(locale) {
		return &Catalog{Locale: "en_US"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.cats[locale]; ok {
		return c
	}
	c := m.load(locale)
	m.cats[locale] = c
	return c
}

// Reset drops cached catalogs (after a language pack was installed or updated).
func (m *Manager) Reset() {
	m.mu.Lock()
	m.cats = map[string]*Catalog{}
	m.mu.Unlock()
}

var localeRe = regexp.MustCompile(`^[a-z]{2,3}(_[A-Za-z0-9]{2,8}){0,2}$`)

func validLocale(l string) bool { return localeRe.MatchString(l) }

func (m *Manager) load(locale string) *Catalog {
	c := &Catalog{Locale: locale}
	add := func(domain string, b []byte, err error) {
		if err != nil {
			return
		}
		f, err := parseMO(b)
		if err != nil {
			return
		}
		if domain != "default" && domain != "wpgo" {
			rebrand(f)
		}
		c.layers = append(c.layers, layer{domain: domain, mo: f})
	}
	if m.Dir != "" {
		for _, name := range []string{"admin-" + locale + ".mo", locale + ".mo", "admin-network-" + locale + ".mo", "continents-cities-" + locale + ".mo"} {
			b, err := os.ReadFile(filepath.Join(m.Dir, name))
			add("default", b, err)
		}
		files, _ := filepath.Glob(filepath.Join(m.Dir, "plugins", "*-"+locale+".mo"))
		sort.Strings(files)
		for _, p := range files {
			slug := strings.TrimSuffix(filepath.Base(p), "-"+locale+".mo")
			b, err := os.ReadFile(p)
			add(slug, b, err)
		}
	}
	if m.Extra != nil {
		b, err := fs.ReadFile(m.Extra, "wpgo-"+locale+".mo")
		add("wpgo", b, err)
		// WordPress Go's own strings can also ship as JSON: {"msgid": "translation"} (a list for
		// plural forms, "context\u0004msgid" keys for a context).
		if b, err := fs.ReadFile(m.Extra, "wpgo-"+locale+".json"); err == nil {
			if f := parseJSONCatalog(b); f != nil {
				c.layers = append(c.layers, layer{domain: "wpgo", mo: f})
			}
		}
	}
	return c
}

// Installed lists the locales with a core pack in the languages directory.
func (m *Manager) Installed() []string {
	if m.Dir == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(m.Dir, "admin-*.mo"))
	var out []string
	for _, f := range files {
		l := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "admin-"), ".mo")
		if !strings.HasPrefix(l, "network-") && validLocale(l) {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// IsInstalled reports whether the core pack of locale is present (English always is).
func (m *Manager) IsInstalled(locale string) bool {
	if locale == "" || locale == "en_US" {
		return true
	}
	if m.Dir == "" || !validLocale(locale) {
		return false
	}
	_, err := os.Stat(filepath.Join(m.Dir, "admin-"+locale+".mo"))
	return err == nil
}

// brands maps the upstream product names in plugin translations to WordPress Go's own. Longer
// names first, so "Yoast SEO" wins over "Yoast".
var brands = []struct{ from, to string }{
	{"Elementor Pro", "Golementor Pro"}, {"Elementor", "Golementor"},
	{"Contact Form 7", "Gontact Form"}, {"Yoast SEO Premium", "SEO-Go"}, {"Yoast SEO", "SEO-Go"}, {"Yoast", "SEO-Go"},
	{"Rank Math SEO", "SEO-Go"}, {"Rank Math", "SEO-Go"}, {"Polylang Pro", "Golylang"}, {"Polylang", "Golylang"},
	{"LiteSpeed Cache", "WPGo Cache"}, {"WP Mail SMTP", "WPGo Mail SMTP"}, {"Advanced Custom Fields PRO", "ACF-Go"},
	{"Advanced Custom Fields", "ACF-Go"}, {"CookieYes", "Gooike"}, {"Cookie Notice", "Gooike"}, {"WPS Hide Login", "AdminSecGo"},
}

func rebrand(f *moFile) {
	for k, e := range f.entries {
		changed := false
		for i, s := range e {
			n := rebrandString(s)
			if n != s {
				if !changed {
					e = append(entry(nil), e...)
					changed = true
				}
				e[i] = n
			}
		}
		if changed {
			f.entries[k] = e
		}
	}
}

func rebrandString(s string) string {
	for _, b := range brands {
		if !strings.Contains(s, b.from) {
			continue
		}
		var out strings.Builder
		rest := s
		for {
			i := strings.Index(rest, b.from)
			if i < 0 {
				out.WriteString(rest)
				break
			}
			// Leave names that already carry WordPress Go's prefix ("Golementor" contains "lementor" only).
			if i > 0 && (strings.HasSuffix(rest[:i], "Go") || strings.HasSuffix(rest[:i], "G")) {
				out.WriteString(rest[:i+len(b.from)])
			} else {
				out.WriteString(rest[:i] + b.to)
			}
			rest = rest[i+len(b.from):]
		}
		s = out.String()
	}
	return s
}

func (c *Catalog) String() string {
	return fmt.Sprintf("Catalog(%s, %d files)", c.Locale, len(c.layers))
}

func parseJSONCatalog(b []byte) *moFile {
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return nil
	}
	f := &moFile{entries: make(map[string]entry, len(raw)), plurals: map[string]string{}}
	for k, v := range raw {
		if k == "" || strings.HasPrefix(k, "_") {
			continue
		}
		var one string
		if json.Unmarshal(v, &one) == nil {
			if one != "" {
				f.entries[k] = entry{one}
			}
			continue
		}
		var forms []string
		if json.Unmarshal(v, &forms) == nil && len(forms) > 0 && forms[0] != "" {
			f.entries[k] = forms
		}
	}
	return f
}
