package builder

// WordPress escaping helpers (esc_attr, esc_html, esc_url) and a json_encode compatible encoder,
// so that rendered markup matches what WordPress + upstream print.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var entityRe = regexp.MustCompile(`^&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);`)

// escAttr mirrors esc_attr()/esc_html(): htmlspecialchars with ENT_QUOTES that leaves existing
// entities alone.
func escAttr(s string) string {
	if !strings.ContainsAny(s, "&<>\"'") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '&':
			if m := entityRe.FindString(s[i:]); m != "" {
				b.WriteString(m)
				i += len(m) - 1
				continue
			}
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#039;")
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func escHTML(s string) string { return escAttr(s) }

var allowedProtocols = []string{"http", "https", "ftp", "ftps", "mailto", "news", "irc", "irc6", "ircs", "gopher", "nntp", "feed", "telnet", "mms", "rtsp", "sms", "svn", "tel", "fax", "xmpp", "webcal", "urn", "skype", "viber"}

var escURLStrip = regexp.MustCompile(`[^a-zA-Z0-9-~+_.?#=!&;,/:%@$|*'()\[\]\x{80}-\x{ffff}]`)

// escURL mirrors esc_url() for display.
func escURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	u = strings.ReplaceAll(u, " ", "%20")
	u = escURLStrip.ReplaceAllString(u, "")
	if u == "" {
		return ""
	}
	if strings.Contains(u, ":") && !strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "#") && !strings.HasPrefix(u, "?") {
		scheme, _, _ := strings.Cut(u, ":")
		if !strings.ContainsAny(scheme, "/?#") {
			ok := false
			for _, p := range allowedProtocols {
				if strings.EqualFold(scheme, p) {
					ok = true
					break
				}
			}
			if !ok {
				return ""
			}
		}
	} else if !strings.Contains(u, ":") && !strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "#") && !strings.HasPrefix(u, "?") && !strings.HasPrefix(u, "&") && !strings.HasSuffix(strings.SplitN(u, "/", 2)[0], ".php") {
		u = "http://" + u
	}
	// wp_kses_normalize_entities then escape & and '
	var b strings.Builder
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c == '&' {
			if m := entityRe.FindString(u[i:]); m != "" {
				if m == "&amp;" {
					b.WriteString("&#038;")
				} else {
					b.WriteString(m)
				}
				i += len(m) - 1
				continue
			}
			b.WriteString("&#038;")
			continue
		}
		if c == '\'' {
			b.WriteString("&#039;")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// omap is an insertion-ordered string map used where PHP array order is observable.
type omap struct {
	keys []string
	m    map[string]any
}

func newOmap() *omap { return &omap{m: map[string]any{}} }

func (o *omap) Set(k string, v any) {
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

func (o *omap) Get(k string) any { return o.m[k] }

func (o *omap) Has(k string) bool { _, ok := o.m[k]; return ok }

func (o *omap) Len() int { return len(o.keys) }

func (o *omap) Delete(k string) {
	if _, ok := o.m[k]; !ok {
		return
	}
	delete(o.m, k)
	for i, x := range o.keys {
		if x == k {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// keyRank gives nested settings maps (slider, dimensions, media, url...) the key order PHP would
// produce after merging with the control type defaults.
var keyRank = func() map[string]int {
	order := []string{"url", "id", "unit", "top", "right", "bottom", "left", "isLinked", "size", "sizes", "alt", "source", "value", "library", "is_external", "nofollow", "custom_attributes", "horizontal", "vertical", "blur", "spread", "color", "width", "height"}
	m := map[string]int{}
	for i, k := range order {
		m[k] = i
	}
	return m
}()

var gapKeyRank = map[string]int{"column": 0, "row": 1, "isLinked": 2, "unit": 3, "size": 4}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	rank := keyRank
	if _, ok := m["column"]; ok {
		if _, ok := m["row"]; ok {
			rank = gapKeyRank
		}
	}
	sort.SliceStable(keys, func(i, j int) bool {
		ri, oi := rank[keys[i]]
		rj, oj := rank[keys[j]]
		if oi && oj {
			return ri < rj
		}
		if oi != oj {
			return oi
		}
		return keys[i] < keys[j]
	})
	return keys
}

// phpJSON mirrors json_encode() with default flags (escaped slashes and unicode).
func phpJSON(v any) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		b.WriteString(jsonFloat(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case string:
		writeJSONString(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, e)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('{')
		for i, k := range sortedKeys(x) {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			writeJSON(b, x[k])
		}
		b.WriteByte('}')
	case *omap:
		if x.Len() == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('{')
		for i, k := range x.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			writeJSON(b, x.m[k])
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

func jsonFloat(f float64) string {
	if f == float64(int64(f)) && f < 1e15 && f > -1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func writeJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '/':
			b.WriteString(`\/`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteString(strconv.FormatInt(int64(r)>>4, 16))
				b.WriteString(strconv.FormatInt(int64(r)&0xf, 16))
			} else if r < 0x80 {
				b.WriteRune(r)
			} else if r < 0x10000 {
				writeU(b, r)
			} else {
				r -= 0x10000
				writeU(b, 0xD800+(r>>10))
				writeU(b, 0xDC00+(r&0x3FF))
			}
		}
	}
	b.WriteByte('"')
}

func writeU(b *strings.Builder, r rune) {
	h := strconv.FormatInt(int64(r), 16)
	b.WriteString(`\u`)
	b.WriteString(strings.Repeat("0", 4-len(h)))
	b.WriteString(h)
}

// attrs is an ordered attribute set, like upstream's render attributes for one element.
type attrs struct {
	keys []string
	vals map[string][]string
}

func newAttrs() *attrs { return &attrs{vals: map[string][]string{}} }

// add appends values (upstream add_render_attribute without overwrite).
func (a *attrs) add(key string, values ...string) *attrs {
	if _, ok := a.vals[key]; !ok {
		a.keys = append(a.keys, key)
		a.vals[key] = nil
	}
	a.vals[key] = append(a.vals[key], values...)
	return a
}

// set overwrites (set_render_attribute).
func (a *attrs) set(key string, values ...string) *attrs {
	if _, ok := a.vals[key]; !ok {
		a.keys = append(a.keys, key)
	}
	a.vals[key] = append([]string(nil), values...)
	return a
}

func (a *attrs) has(key string) bool { _, ok := a.vals[key]; return ok }

func (a *attrs) get(key string) string { return strings.Join(a.vals[key], " ") }

func (a *attrs) remove(key string) {
	if _, ok := a.vals[key]; !ok {
		return
	}
	delete(a.vals, key)
	for i, k := range a.keys {
		if k == key {
			a.keys = append(a.keys[:i:i], a.keys[i+1:]...)
			break
		}
	}
}

// String mirrors Utils::render_html_attributes.
func (a *attrs) String() string {
	if a == nil || len(a.keys) == 0 {
		return ""
	}
	parts := make([]string, 0, len(a.keys))
	for _, k := range a.keys {
		parts = append(parts, k+`="`+escAttr(strings.Join(a.vals[k], " "))+`"`)
	}
	return strings.Join(parts, " ")
}
