package server

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Gontact Form form-tags: the [type* name option:value "value"] syntax of Contact Form 7
// (WPCF7_FormTagsManager / WPCF7_FormTag). Only the registered types are recognised; [[tag]] is the
// escaped literal, and a tag may enclose content ([acceptance x]I agree[/acceptance]).

// gfTagTypes are the form-tag types Gontact Form knows; a "*" variant marks the field required.
var gfTagTypes = []string{
	"text", "text*", "email", "email*", "url", "url*", "tel", "tel*",
	"textarea", "textarea*", "number", "number*", "range", "range*", "date", "date*",
	"select", "select*", "checkbox", "checkbox*", "radio", "acceptance", "quiz", "captchac", "captchar",
	"file", "file*", "submit", "hidden", "count", "response",
}

// gfNoName are the types without a name attribute (feature "name-attr" in CF7).
var gfNoName = map[string]bool{"submit": true, "response": true}

type gfPipe struct{ Before, After string }

// gfTag is one scanned form-tag.
type gfTag struct {
	Type, BaseType, RawName, Name string
	Options                       []string
	Values, Labels                []string // values without the "|after" part of pipes
	Pipes                         []gfPipe
	Content                       string
}

var (
	gfTagRe  *regexp.Regexp
	gfNameRe = regexp.MustCompile(`^[A-Za-z][-A-Za-z0-9_:.]*$`)
	gfAttsRe = regexp.MustCompile(`^([-+*=0-9a-zA-Z:.!?#$&@_/|%\s]*?)((?:\s*"[^"]*"|\s*'[^']*')*)$`)
	gfValRe  = regexp.MustCompile(`"[^"]*"|'[^']*'`)
)

func init() {
	types := append([]string{}, gfTagTypes...)
	// Longest first so "text*" wins over "text"; the alternation is leftmost-first in RE2.
	for i := range types {
		for j := i + 1; j < len(types); j++ {
			if len(types[j]) > len(types[i]) {
				types[i], types[j] = types[j], types[i]
			}
		}
		types[i] = regexp.QuoteMeta(types[i])
	}
	gfTagRe = regexp.MustCompile(`(\[?)\[(` + strings.Join(types, "|") + `)(?:\s+([^\]]*?))?(?:\s+(/))?\]`)
}

// gfScan walks content and calls fn for every form-tag; fn's return value replaces the tag (with the
// enclosed content and closing tag). With fn nil the tags are only collected.
func gfScan(content string, fn func(t *gfTag) string) (string, []*gfTag) {
	var tags []*gfTag
	var out strings.Builder
	rest := content
	for {
		loc := gfTagRe.FindStringSubmatchIndex(rest)
		if loc == nil {
			out.WriteString(rest)
			break
		}
		sub := func(i int) string {
			if loc[2*i] < 0 {
				return ""
			}
			return rest[loc[2*i]:loc[2*i+1]]
		}
		end := loc[1]
		typ := sub(2)
		inner := ""
		// Enclosed content: [type …]content[/type], the content holds no "[".
		if sub(4) == "" {
			closing := "[/" + typ + "]"
			if i := strings.Index(rest[end:], closing); i >= 0 && !strings.Contains(rest[end:end+i], "[") {
				inner = rest[end : end+i]
				end += i + len(closing)
			}
		}
		closeEsc := end < len(rest) && rest[end] == ']'
		out.WriteString(rest[:loc[0]])
		whole := rest[loc[0]:end]
		if sub(1) == "[" && closeEsc { // [[tag]] is the literal [tag]
			out.WriteString(whole[1:])
			rest = rest[end+1:]
			continue
		}
		if sub(1) == "[" {
			out.WriteString("[")
			whole = whole[1:]
		}
		t := gfParseTag(typ, sub(3), inner)
		if t == nil {
			out.WriteString(whole)
		} else {
			tags = append(tags, t)
			if fn != nil {
				out.WriteString(fn(t))
			} else {
				out.WriteString(whole)
			}
		}
		rest = rest[end:]
	}
	return out.String(), tags
}

// gfParseTag is scan_callback(): the name, options and quoted values of one tag (nil when invalid).
func gfParseTag(typ, attr, content string) *gfTag {
	t := &gfTag{Type: typ, BaseType: strings.TrimSuffix(typ, "*")}
	attr = strings.NewReplacer(" ", " ", "​", " ").Replace(strings.TrimSpace(attr))
	var options, values []string
	if m := gfAttsRe.FindStringSubmatch(attr); m != nil {
		options = strings.Fields(m[1])
		for _, v := range gfValRe.FindAllString(m[2], -1) {
			values = append(values, v[1:len(v)-1])
		}
	} else if !gfNoName[t.BaseType] {
		return nil
	}
	if !gfNoName[t.BaseType] {
		if len(options) == 0 || !gfNameRe.MatchString(options[0]) {
			return nil
		}
		t.RawName = options[0]
		t.Name = strings.ReplaceAll(t.RawName, ".", "_")
		options = options[1:]
	}
	t.Options = options
	for _, v := range values {
		before, after := v, v
		if i := strings.Index(v, "|"); i >= 0 {
			before, after = v[:i], v[i+1:]
		}
		t.Pipes = append(t.Pipes, gfPipe{Before: before, After: after})
		t.Values = append(t.Values, strings.TrimSpace(before))
	}
	if t.BaseType == "select" && len(t.Values) == 0 {
		t.Values = gfDataList(t.Options)
	}
	t.Labels = append([]string{}, t.Values...)
	content = strings.TrimSpace(content)
	content = gfTrailingBrRe.ReplaceAllString(content, "")
	t.Content = content
	return t
}

func (t *gfTag) required() bool { return strings.HasSuffix(t.Type, "*") }

func (t *gfTag) has(name string) bool {
	for _, o := range t.Options {
		if o == name {
			return true
		}
	}
	return false
}

var gfOptPatterns = map[string]string{
	"":           `.+`,
	"id":         `[-0-9a-zA-Z_]+`,
	"class":      `[-0-9a-zA-Z_]+`,
	"int":        `[0-9]+`,
	"signed_int": `[-]?[0-9]+`,
	"num":        `(?:[0-9]+|(?:[0-9]+)?[.][0-9]+)`,
	"signed_num": `[-]?(?:[0-9]+|(?:[0-9]+)?[.][0-9]+)`,
	"date":       `[0-9]{4,}-[0-9]{2}-[0-9]{2}`,
}

// gfOptionRes caches the "name:value" matchers by name and pattern (names come from the form
// templates' own option names, a small set).
var gfOptionRes sync.Map

func gfOptionRe(name, pattern string) *regexp.Regexp {
	key := name + "\x00" + pattern
	if re, ok := gfOptionRes.Load(key); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `:(` + gfOptPatterns[pattern] + `)$`)
	gfOptionRes.Store(key, re)
	return re
}

// option is get_option(): the values of every "name:value" option matching the pattern.
func (t *gfTag) option(name, pattern string) []string {
	re := gfOptionRe(name, pattern)
	var out []string
	for _, o := range t.Options {
		if m := re.FindStringSubmatch(o); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func (t *gfTag) option1(name, pattern string) string {
	if v := t.option(name, pattern); len(v) > 0 {
		return v[0]
	}
	return ""
}

func (t *gfTag) idOption() string { return t.option1("id", "id") }

// classOption is get_class_option(): the default classes plus every class: option.
func (t *gfTag) classOption(def string) string {
	classes := strings.Fields(def)
	for _, c := range t.option("class", "class") {
		classes = append(classes, c)
	}
	seen := map[string]bool{}
	out := classes[:0]
	for _, c := range classes {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return strings.Join(out, " ")
}

// firstMatch returns the first option matching re (submatches included).
func (t *gfTag) firstMatch(re *regexp.Regexp) []string {
	for _, o := range t.Options {
		if m := re.FindStringSubmatch(o); m != nil {
			return m
		}
	}
	return nil
}

var (
	gfSizeRe = regexp.MustCompile(`^([0-9]*)/([0-9]*)$`)
	gfColsRe = regexp.MustCompile(`^([0-9]*)x([0-9]*)$`)
)

func (t *gfTag) sizeOption(def string) string {
	if v := t.option1("size", "int"); v != "" {
		return v
	}
	if m := t.firstMatch(gfSizeRe); m != nil && m[1] != "" {
		return m[1]
	}
	return def
}

func (t *gfTag) maxlengthOption(def string) string {
	if v := t.option1("maxlength", "int"); v != "" {
		return v
	}
	if m := t.firstMatch(gfSizeRe); m != nil && m[2] != "" {
		return m[2]
	}
	return def
}

func (t *gfTag) minlengthOption() string { return t.option1("minlength", "int") }

func (t *gfTag) colsOption(def string) string {
	if v := t.option1("cols", "int"); v != "" {
		return v
	}
	if m := t.firstMatch(gfColsRe); m != nil && m[1] != "" {
		return m[1]
	}
	return def
}

func (t *gfTag) rowsOption(def string) string {
	if v := t.option1("rows", "int"); v != "" {
		return v
	}
	if m := t.firstMatch(gfColsRe); m != nil && m[2] != "" {
		return m[2]
	}
	return def
}

// autocompleteOption joins the autocomplete: options ("autocomplete:off", "autocomplete:name").
func (t *gfTag) autocompleteOption() string {
	return strings.Join(t.option("autocomplete", ""), " ")
}

// limitOption is get_limit_option(): the file size limit in bytes (1 MB by default).
func (t *gfTag) limitOption() int64 {
	re := gfLimitRe
	if m := t.firstMatch(re); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		switch strings.ToLower(m[2]) {
		case "kb":
			n *= 1024
		case "mb":
			n *= 1024 * 1024
		}
		return n
	}
	return 1024 * 1024
}

// gfDateRe matches the relative dates date options accept: today, today+7days, today-1month …
var gfRelDateRe = regexp.MustCompile(`^today(?:([+-][0-9]+)(days?|weeks?|months?|years?))?$`)

// dateOption is get_date_option(): an absolute YYYY-MM-DD or a date relative to today.
func (t *gfTag) dateOption(name string, today func() (int, int, int)) string {
	v := t.option1(name, "")
	if v == "" {
		return ""
	}
	if gfAbsDateRe.MatchString(v) {
		return v
	}
	m := gfRelDateRe.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	y, mo, d := today()
	n, _ := strconv.Atoi(m[1])
	switch {
	case strings.HasPrefix(m[2], "day"):
		d += n
	case strings.HasPrefix(m[2], "week"):
		d += 7 * n
	case strings.HasPrefix(m[2], "month"):
		mo += n
	case strings.HasPrefix(m[2], "year"):
		y += n
	}
	return gfDate(y, mo, d)
}

// pipeAfter maps a submitted value (a "before" of the tag's pipes) to its "after" part.
func (t *gfTag) pipeAfter(v string) string {
	for _, p := range t.Pipes {
		if strings.TrimSpace(p.Before) == v {
			return p.After
		}
	}
	return v
}

// hasPipes reports whether any value of the tag uses the "label|value" syntax.
func (t *gfTag) hasPipes() bool {
	for _, p := range t.Pipes {
		if p.Before != p.After {
			return true
		}
	}
	return false
}

// Compiled once (they used to be compiled on every call).
var (
	gfTrailingBrRe = regexp.MustCompile(`(?m)<br[\r\n\t ]*/?>$`)
	gfLimitRe      = regexp.MustCompile(`(?i)^limit:([1-9][0-9]*)([kKmM]?[bB])?$`)
	gfAbsDateRe    = regexp.MustCompile(`^` + gfOptPatterns["date"] + `$`)
)

// gfDataList fills a select from a built-in list: `data:countries` (every country, as Pursae
// Commerce knows them) or `data:states:US` (the states of one country). Values are the names.
func gfDataList(options []string) []string {
	var out []string
	for _, o := range options {
		kind, ok := strings.CutPrefix(o, "data:")
		if !ok {
			continue
		}
		switch {
		case kind == "countries":
			for _, c := range gooCountryOrder {
				out = append(out, c.Name)
			}
		case strings.HasPrefix(kind, "states:"):
			for _, st := range gooStates[strings.ToUpper(strings.TrimPrefix(kind, "states:"))] {
				out = append(out, st.Name)
			}
		}
	}
	return out
}
