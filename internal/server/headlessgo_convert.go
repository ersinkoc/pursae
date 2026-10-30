package server

import (
	"encoding/json"
	"math"
	"math/big"
	"net/mail"
	"strconv"
	"strings"
)

// Changing a field in the Content-Type Builder converts the values stored in the entries (drafts
// and published versions, also inside components and dynamic zones): text <-> number <-> boolean,
// dates and times, rich text formats, number formats, single <-> multiple media and relations,
// renamed enumeration values, single <-> repeatable components. A value that cannot be converted
// is removed; the builder warns before such a lossy change is saved.

type hgConversion struct{ from, to *hgField }

// hgNeedsConversion reports whether stored values of from must be converted to fit to.
func hgNeedsConversion(from, to *hgField) bool {
	if from.Type != to.Type || from.MappedBy != to.MappedBy {
		return true
	}
	switch to.Type {
	case "number", "richtext":
		return from.Format != to.Format
	case "media":
		return from.Multiple && !to.Multiple
	case "relation":
		return from.Target != to.Target || (from.multi() && !to.multi())
	case "enumeration":
		if len(to.EnumRenames) > 0 {
			return true
		}
		for _, v := range from.Enum {
			if !containsStr(to.Enum, v) {
				return true
			}
		}
	case "component":
		return from.Component != to.Component || from.Repeatable != to.Repeatable
	case "dynamiczone":
		for _, c := range from.Components {
			if !containsStr(to.Components, c) {
				return true
			}
		}
	}
	return false
}

// hgScalarKind groups field types for conversion.
func hgScalarKind(f *hgField) string {
	switch f.Type {
	case "text", "email", "uid", "enumeration", "date", "datetime", "time":
		return "string"
	case "richtext":
		if f.Format == "markdown" {
			return "string"
		}
		return "blocks"
	case "number", "boolean", "json", "password":
		return f.Type
	}
	return "complex" // media, relation, component, dynamiczone
}

// hgConvertValue converts one stored value of from to field to; keep is false when the value
// must be removed.
func hgConvertValue(from, to *hgField, v any) (any, bool) {
	if v == nil || to.MappedBy != "" {
		return nil, false
	}
	fk, tk := hgScalarKind(from), hgScalarKind(to)
	if fk == "password" || tk == "password" {
		return v, fk == tk
	}
	if from.Type == to.Type && fk == "complex" {
		return hgConvertComplex(from, to, v)
	}
	if fk == "complex" || tk == "complex" {
		return nil, false
	}
	if tk == "json" {
		return v, true
	}
	if tk == "blocks" {
		if fk == "blocks" {
			return v, true
		}
		s, ok := hgScalarString(from, v)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, false
		}
		h := &hgCoercer{}
		out, ok := h.blocks(s, nil)
		return out, ok && out != nil
	}
	if tk == "number" {
		return hgToNumber(to, from, v)
	}
	if tk == "boolean" {
		switch t := v.(type) {
		case bool:
			return t, true
		case float64:
			return t != 0, true
		}
		s, ok := hgScalarString(from, v)
		if !ok {
			return nil, false
		}
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes", "on", "y":
			return true, true
		case "false", "0", "no", "off", "n", "":
			return false, true
		}
		return nil, false
	}
	// A string type.
	s, ok := hgScalarString(from, v)
	if !ok {
		return nil, false
	}
	switch to.Type {
	case "text":
		if to.Regex != "" && !hgRegexOK(to.Regex, s) {
			return nil, false
		}
		return s, s != ""
	case "richtext":
		return s, s != ""
	case "email":
		s = strings.TrimSpace(s)
		if a, err := mail.ParseAddress(s); err != nil || a.Address != s {
			return nil, false
		}
		return s, true
	case "uid":
		s = strings.TrimSpace(s)
		if !hgUIDValueRe.MatchString(s) {
			s = hgSlugify(s)
		}
		if to.Regex != "" && !hgRegexOK(to.Regex, s) {
			return nil, false
		}
		return s, s != ""
	case "enumeration":
		if n, ok := to.EnumRenames[s]; ok && from.Type == "enumeration" {
			s = n
		}
		if containsStr(to.Enum, s) {
			return s, true
		}
		for _, e := range to.Enum {
			if strings.EqualFold(e, strings.TrimSpace(s)) {
				return e, true
			}
		}
		return nil, false
	case "date", "datetime", "time":
		out, ok := hgNormTemporal(to.Type, strings.TrimSpace(s))
		return out, ok
	}
	return nil, false
}

func hgRegexOK(re, s string) bool {
	h := &hgCoercer{}
	f := &hgField{Type: "text", Regex: re}
	_, ok := h.value(nil, f, s, nil, nil)
	return ok
}

// hgScalarString is a scalar value as text.
func hgScalarString(from *hgField, v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(t), true
	case []any:
		if hgScalarKind(from) == "blocks" {
			return hgBlocksText(t), true
		}
	}
	if from.Type == "json" {
		raw, err := json.Marshal(v)
		return string(raw), err == nil
	}
	return "", false
}

// hgBlocksText is the plain text of rich-text blocks, paragraphs separated by blank lines.
func hgBlocksText(blocks []any) string {
	var paras []string
	var walk func(nodes []any, b *strings.Builder)
	walk = func(nodes []any, b *strings.Builder) {
		for _, n := range nodes {
			m, ok := n.(map[string]any)
			if !ok {
				continue
			}
			if m["type"] == "text" {
				s, _ := m["text"].(string)
				b.WriteString(s)
				continue
			}
			kids, _ := m["children"].([]any)
			walk(kids, b)
			if m["type"] == "list-item" {
				b.WriteString("\n")
			}
		}
	}
	for _, blk := range blocks {
		var b strings.Builder
		walk([]any{blk}, &b)
		if t := strings.TrimRight(b.String(), "\n"); t != "" {
			paras = append(paras, t)
		}
	}
	return strings.Join(paras, "\n\n")
}

// hgToNumber converts a value to a number of format to.Format.
func hgToNumber(to, from *hgField, v any) (any, bool) {
	var n float64
	var str string
	switch t := v.(type) {
	case float64:
		n = t
		str = strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			n, str = 1, "1"
		} else {
			n, str = 0, "0"
		}
	default:
		s, ok := hgScalarString(from, v)
		if !ok {
			return nil, false
		}
		str = strings.TrimSpace(s)
		f, err := strconv.ParseFloat(str, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, false
		}
		n = f
	}
	switch to.Format {
	case "biginteger":
		if bi, ok := new(big.Int).SetString(str, 10); ok {
			return bi.String(), true
		}
		return strconv.FormatFloat(math.Round(n), 'f', 0, 64), true
	case "integer":
		return math.Round(n), true
	}
	return n, true
}

// hgConvertComplex converts media, relation, component and dynamic zone values between options of
// the same type.
func hgConvertComplex(from, to *hgField, v any) (any, bool) {
	switch to.Type {
	case "media", "relation":
		if to.Type == "relation" && from.Target != to.Target {
			return nil, false
		}
		list, ok := v.([]any)
		if !ok {
			return nil, false
		}
		if !to.multi() && len(list) > 1 {
			list = list[:1]
		}
		return list, len(list) > 0
	case "component":
		if from.Component != to.Component {
			return nil, false
		}
		switch t := v.(type) {
		case map[string]any:
			if to.Repeatable {
				return []any{t}, true
			}
			return t, true
		case []any:
			if to.Repeatable {
				return t, true
			}
			if len(t) == 0 {
				return nil, false
			}
			m, ok := t[0].(map[string]any)
			return m, ok
		}
		return nil, false
	case "dynamiczone":
		list, ok := v.([]any)
		if !ok {
			return nil, false
		}
		out := []any{}
		for _, it := range list {
			if m, ok := it.(map[string]any); ok {
				if cu, _ := m["__component"].(string); containsStr(to.Components, cu) {
					out = append(out, m)
				}
			}
		}
		return out, len(out) > 0
	}
	return nil, false
}
