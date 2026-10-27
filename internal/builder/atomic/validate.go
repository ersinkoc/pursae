package atomic

// Validation of saved atomic data against the props schemas, ported from upstream 4.3's
// modules/atomic-widgets/parsers (Style_Parser, Props_Parser) and the prop types' validate()
// methods. The REST routes (global classes, default styles, components) use it to refuse invalid
// style data with upstream's error strings ("<path>: <reason>").

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// validStyleStates are Style_States::get_valid_states (null included).
var validStyleStates = map[string]bool{"hover": true, "active": true, "focus": true, "checked": true,
	"e--selected": true, "e--disabled": true, "e--playing": true, "e--paused": true}

// sizeUnits are Size_Constants::all_supported_units.
var sizeUnits = map[string]bool{"px": true, "em": true, "rem": true, "vw": true, "vh": true, "ch": true, "ms": true, "s": true,
	"deg": true, "rad": true, "grad": true, "turn": true, "auto": true, "custom": true, "vmin": true, "vmax": true, "%": true, "fr": true}

var (
	classNameRe = regexp.MustCompile(`(?i)^[a-z][a-z-_0-9]*$`)
	isoTimeRe   = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?$`)
)

// ValidationError is one "<key>: <error>" entry (Parse_Errors).
type ValidationError struct{ Key, Error string }

func (e ValidationError) String() string { return e.Key + ": " + e.Error }

// JoinErrors is Parse_Errors::to_string.
func JoinErrors(errs []ValidationError) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.String()
	}
	return strings.Join(parts, ", ")
}

func prefixErrors(prefix string, errs []ValidationError) []ValidationError {
	out := make([]ValidationError, len(errs))
	for i, e := range errs {
		out[i] = ValidationError{prefix + "." + e.Key, e.Error}
	}
	return out
}

// ValidateStyle mirrors Style_Parser::parse (with the v4 opt-in on, so class labels are
// checked): it returns the errors and the style with its variants' props reduced to the props the
// style schema knows. v is a decoded JSON value (map[string]any or *Map).
func ValidateStyle(v any) (any, []ValidationError) {
	var errs []ValidationError
	add := func(k, e string) { errs = append(errs, ValidationError{k, e}) }
	m := asMap(FromAny(v))
	if m == nil {
		return v, []ValidationError{{"style", "invalid_type"}}
	}
	if _, ok := m.Get("id").(string); !ok {
		add("id", "missing_or_invalid")
	}
	if m.Get("type") != "class" {
		add("type", "missing_or_invalid")
	}
	if label, ok := m.Get("label").(string); !ok {
		add("label", "missing_or_invalid")
	} else if e := ClassLabelError(label); e != "" {
		add("label", e)
	}
	variants, ok := m.Get("variants").([]any)
	if !ok {
		if vm, isMap := m.Get("variants").(*Map); isMap && vm.Len() == 0 {
			variants, ok = []any{}, true
		}
	}
	if !ok {
		add("variants", "missing_or_invalid")
		m.Delete("variants")
		return m, errs
	}
	schema := StyleSchema()
	kept := []any{}
	for i, vv := range variants {
		vm := asMap(vv)
		if vm == nil || !vm.Has("meta") || vm.Get("meta") == nil {
			add("meta", "missing")
			continue
		}
		metaErrs := validateMeta(vm.Get("meta"))
		errs = append(errs, prefixErrors("meta", metaErrs)...)
		if css := asMap(vm.Get("custom_css")); css != nil && !phpEmpty(css.Get("raw")) {
			if _, isStr := css.Get("raw").(string); !isStr {
				add("custom_css.custom_css", "invalid_type")
			}
		}
		if len(metaErrs) > 0 {
			continue
		}
		props := asMap(vm.Get("props"))
		if props == nil {
			props = NewMap()
		}
		valid, perrs := ValidateProps(schema, props)
		errs = append(errs, prefixErrors("variants["+strconv.Itoa(i)+"]", perrs)...)
		vm.Set("props", valid)
		kept = append(kept, vm)
	}
	m.Set("variants", kept)
	return m, errs
}

func validateMeta(v any) []ValidationError {
	meta := asMap(v)
	if meta == nil {
		return []ValidationError{{"meta", "invalid_type"}}
	}
	st, present := meta.Lookup("state")
	if !present {
		return []ValidationError{{"state", "missing_or_invalid_value"}}
	}
	if st != nil {
		s, ok := st.(string)
		if !ok || !validStyleStates[s] {
			return []ValidationError{{"state", "missing_or_invalid_value"}}
		}
	}
	if _, ok := meta.Get("breakpoint").(string); !ok {
		return []ValidationError{{"breakpoint", "missing_or_invalid_value"}}
	}
	return nil
}

// ClassLabelError mirrors Style_Parser::validate_style_label ("" = valid).
func ClassLabelError(label string) string {
	l := strings.ToLower(label)
	switch {
	case len(l) > 50:
		return "class_name_too_long"
	case len(l) < 2:
		return "class_name_too_short"
	case l == "container":
		return "reserved_class_name"
	case l[0] >= '0' && l[0] <= '9':
		return "class_name_starts_with_digit"
	case strings.ContainsAny(l, " \t\n\r\f\v"):
		return "class_name_contains_spaces"
	case !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(l):
		return "class_name_invalid_chars"
	case strings.HasPrefix(l, "--"):
		return "class_name_double_hyphen"
	case len(l) > 1 && l[0] == '-' && l[1] >= '0' && l[1] <= '9':
		return "class_name_starts_with_hyphen_digit"
	}
	return ""
}

// ValidateProps mirrors Props_Parser::validate: every schema prop is validated (missing ones
// against their default); the result keeps the valid, non-null props in schema order.
func ValidateProps(s *Schema, props *Map) (*Map, []ValidationError) {
	out := NewMap()
	var errs []ValidationError
	if s == nil {
		return out, nil
	}
	for _, key := range s.Keys {
		pt := s.Props[key]
		v, _ := props.Lookup(key)
		check := v
		if check == nil {
			check = pt.Default
		}
		if !ValidateProp(pt, check) {
			errs = append(errs, ValidationError{key, "invalid_value"})
			continue
		}
		if v != nil {
			out.Set(key, v)
		}
	}
	return out, errs
}

// ValidateProp mirrors Prop_Type::validate for the prop type kinds of the editor schema.
func ValidateProp(pt *PropType, v any) bool {
	if pt == nil {
		return true
	}
	required := pt.Settings != nil && pt.Settings.Get("required") == true
	switch pt.Kind {
	case "union":
		if v == nil {
			return !required
		}
		var sub *PropType
		if m, typ, ok := transformable(v); ok && m != nil {
			sub = pt.Types[typ]
		} else if m := asMap(v); m != nil && m.Has("$$type") {
			sub = pt.Types[phpString(m.Get("$$type"))]
		} else {
			switch v.(type) {
			case float64:
				sub = pt.Types["number"]
			case bool:
				sub = pt.Types["boolean"]
			case string:
				if isNumeric(v) {
					sub = pt.Types["number"]
				} else {
					sub = pt.Types["string"]
				}
			}
		}
		return sub != nil && ValidateProp(sub, v)
	case "object", "array":
		if v == nil {
			return !required
		}
		m, ok := isTransformableFor(v, pt.Key)
		if !ok {
			return false
		}
		if pt.Kind == "array" {
			return validateArray(pt, m.Get("value"))
		}
		return validateObject(pt, m.Get("value"))
	default: // plain, string, number, boolean
		if v == nil {
			return !required
		}
		m, ok := isTransformableFor(v, pt.Key)
		if ok && phpEmpty(m.Get("value")) {
			return !required
		}
		if !ok {
			return false
		}
		return validatePlain(pt, m.Get("value"))
	}
}

// isTransformableFor is Has_Transformable_Validation::is_transformable for one key.
func isTransformableFor(v any, key string) (*Map, bool) {
	m := asMap(v)
	if m == nil || !m.Has("$$type") || !m.Has("value") || phpString(m.Get("$$type")) != key {
		return nil, false
	}
	if d, present := m.Lookup("disabled"); present {
		if _, isBool := d.(bool); !isBool {
			return nil, false
		}
	}
	return m, true
}

func validateArray(pt *PropType, v any) bool {
	if !isArray(v) {
		return false
	}
	for _, it := range listValues(v) {
		if pt.Item != nil && !ValidateProp(pt.Item, it) {
			return false
		}
	}
	return true
}

// listValues returns the values of a PHP array (list or map) in order.
func listValues(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case *Map:
		out := make([]any, 0, x.Len())
		for _, k := range x.Keys() {
			out = append(out, x.Get(k))
		}
		return out
	}
	return nil
}

func validateObject(pt *PropType, v any) bool {
	if !isArray(v) {
		return false
	}
	m := asMap(v)
	switch pt.Key {
	case "size", "grid-track-size", "time-size": // Size_Prop_Type and its subclasses
		return validateSize(m)
	case "image-src":
		if !(phpEmpty(m.Get("id")) != phpEmpty(m.Get("url"))) {
			return false
		}
	case "video-src":
		if nonEmptyCount(m) != 1 {
			return false
		}
	case "svg-src":
		if nonEmptyCount(m) < 1 {
			return false
		}
	case "icon":
		if phpString(get(m.Get("value"), "value")) == "" || phpString(get(m.Get("library"), "value")) == "" {
			return false
		}
	}
	if pt.Shape != nil {
		for _, key := range pt.Shape.Keys {
			sub := pt.Shape.Props[key]
			x, _ := m.Lookup(key)
			if x == nil {
				x = sub.Default
			}
			if !ValidateProp(sub, x) {
				return false
			}
		}
	}
	switch pt.Key {
	case "emails":
		to := m.Get("to")
		if tm := asMap(to); tm != nil && phpString(tm.Get("$$type")) == "dynamic" {
			return true
		}
		tm := asMap(to)
		return tm != nil && !phpEmpty(tm.Get("value"))
	case "date-range", "time-range", "number-range":
		lo, hi := innerValue(m.Get("min")), innerValue(m.Get("max"))
		if lo == nil || hi == nil {
			return true
		}
		if pt.Key == "number-range" {
			return toFloat(hi) >= toFloat(lo)
		}
		return phpString(hi) >= phpString(lo)
	}
	return true
}

func innerValue(v any) any {
	if m := asMap(v); m != nil && m.Has("value") {
		v = m.Get("value")
	}
	if phpEmpty(v) && v != float64(0) {
		return nil
	}
	return v
}

func nonEmptyCount(m *Map) int {
	n := 0
	for _, k := range m.Keys() {
		if !phpEmpty(m.Get(k)) {
			n++
		}
	}
	return n
}

// validateSize mirrors Size_Prop_Type::validate_value.
func validateSize(m *Map) bool {
	if m == nil || !m.Has("size") || !m.Has("unit") {
		return false
	}
	unit, _ := m.Get("unit").(string)
	if unit == "" || !sizeUnits[unit] {
		return false
	}
	size := m.Get("size")
	switch unit {
	case "custom":
		return size != nil
	case "auto":
		return true
	}
	isZero := false
	if f, ok := size.(float64); ok && f == 0 && f == math.Trunc(f) {
		isZero = true
	}
	return (!phpEmpty(size) || isZero) && isNumeric(size)
}

func validatePlain(pt *PropType, v any) bool {
	switch pt.Kind {
	case "string":
		s, ok := v.(string)
		if !ok {
			return false
		}
		if pt.Settings != nil {
			if enum, ok := pt.Settings.Get("enum").([]any); ok && len(enum) > 0 {
				found := false
				for _, e := range enum {
					if e == s {
						found = true
					}
				}
				if !found {
					return false
				}
			}
			if re, ok := pt.Settings.Get("regex").(string); ok && re != "" {
				if rx := phpRegex(re); rx != nil && !rx.MatchString(s) {
					return false
				}
			}
		}
		switch pt.Key {
		case "url":
			if pt.Settings != nil && !phpEmpty(pt.Settings.Get("skip_validation")) {
				return true
			}
			return validURL(s)
		case "date-string":
			_, err := time.Parse("2006-01-02", s)
			return err == nil
		case "time-string":
			return isoTimeRe.MatchString(s)
		}
		return true
	case "number":
		return isNumeric(v)
	case "boolean":
		_, ok := v.(bool)
		return ok
	}
	switch pt.Key {
	case "classes":
		if !isArray(v) {
			return false
		}
		for _, c := range listValues(v) {
			s, ok := c.(string)
			if !ok || !classNameRe.MatchString(s) {
				return false
			}
		}
		return true
	case "html":
		_, ok := v.(string)
		return ok
	}
	return true // dynamic tags, overridables, overrides: validated by their own flows
}

// phpRegex compiles a PCRE pattern like "/^...$/i" into a Go regexp (nil when it cannot).
func phpRegex(p string) *regexp.Regexp {
	if len(p) < 2 {
		return nil
	}
	d := p[0]
	end := strings.LastIndexByte(p, d)
	if end <= 0 {
		return nil
	}
	body, flags := p[1:end], p[end+1:]
	prefix := ""
	if strings.Contains(flags, "i") {
		prefix = "(?i)"
	}
	rx, err := regexp.Compile(prefix + body)
	if err != nil {
		return nil
	}
	return rx
}

// validURL approximates filter_var(FILTER_VALIDATE_URL): a scheme and a host (or a path for
// mailto/tel/file-like schemes), no spaces.
func validURL(s string) bool {
	if strings.HasPrefix(s, "@@") { // an asset URL placeholder of the editor data (@@GWEASSETS@@...)
		if i := strings.Index(s[2:], "@@"); i >= 0 {
			s = "http://localhost/" + s[i+4:]
		}
	}
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return false
	}
	i := strings.Index(s, ":")
	if i <= 0 {
		return false
	}
	scheme := strings.ToLower(s[:i])
	for _, r := range scheme {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	rest := s[i+1:]
	switch scheme {
	case "mailto", "news", "file":
		return rest != ""
	}
	if !strings.HasPrefix(rest, "//") {
		return false
	}
	host := strings.TrimPrefix(rest, "//")
	if j := strings.IndexAny(host, "/?#"); j >= 0 {
		host = host[:j]
	}
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return host != ""
}

// ValidateSettings mirrors Has_Atomic_Base::parse_atomic_settings (Props_Parser against the
// element's props schema); unknown element types pass.
func ValidateSettings(typ string, settings any) []ValidationError {
	et := Lookup(typ)
	if et == nil {
		return nil
	}
	m := asMap(FromAny(settings))
	if m == nil {
		m = NewMap()
	}
	_, errs := ValidateProps(et.Schema, m)
	return errs
}
