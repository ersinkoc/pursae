// Package atomic renders GoWebEditor's atomic (v4) elements and their styles: a port of upstream
// 4.3's modules/atomic-widgets (props resolver, settings and style transformers, Twig templates,
// styles renderer) plus the global classes, variables and components modules it depends on.
//
// Values follow PHP semantics: associative arrays are *Map (insertion ordered, because PHP array
// order shows in the rendered markup and CSS), lists are []any, scalars are string, float64, bool
// or nil (JSON numbers decode to float64).
//
// The element templates are the upstream Twig templates themselves (embedded from the editor
// config, see data/), executed by the small Twig interpreter in twig*.go. Running the original
// templates instead of hand-porting them keeps the markup (including Twig's whitespace control)
// byte-identical to upstream and lets the editor's client-side Twing renderer and the server agree.
package atomic

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Map is an insertion-ordered string map (a PHP associative array).
type Map struct {
	keys []string
	m    map[string]any
}

// NewMap returns an empty map.
func NewMap() *Map { return &Map{m: map[string]any{}} }

// MapOf builds a map from alternating key, value arguments.
func MapOf(kv ...any) *Map {
	o := NewMap()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// Set inserts or replaces a key (a replaced key keeps its position, like PHP).
func (o *Map) Set(k string, v any) {
	if o.m == nil {
		o.m = map[string]any{}
	}
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

// Get returns a value (nil when missing).
func (o *Map) Get(k string) any {
	if o == nil {
		return nil
	}
	return o.m[k]
}

// Lookup returns a value and whether the key exists.
func (o *Map) Lookup(k string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.m[k]
	return v, ok
}

// Has reports whether a key exists.
func (o *Map) Has(k string) bool { _, ok := o.Lookup(k); return ok }

// Keys returns the keys in order.
func (o *Map) Keys() []string {
	if o == nil {
		return nil
	}
	return o.keys
}

// Len is the number of entries.
func (o *Map) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Delete removes a key.
func (o *Map) Delete(k string) {
	if o == nil {
		return
	}
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

// Clone copies the map (shallow).
func (o *Map) Clone() *Map {
	c := NewMap()
	for _, k := range o.Keys() {
		c.Set(k, o.m[k])
	}
	return c
}

// MarshalJSON writes the keys in order.
func (o *Map) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys() {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := json.Marshal(o.m[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnmarshalJSON decodes an object keeping key order (nested objects become *Map too).
func (o *Map) UnmarshalJSON(data []byte) error {
	v, err := Decode(data)
	if err != nil {
		return err
	}
	m, ok := v.(*Map)
	if !ok {
		*o = Map{m: map[string]any{}}
		return nil
	}
	*o = *m
	return nil
}

// Decode parses JSON into PHP-like values: objects become *Map, arrays []any, numbers float64.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		switch x {
		case '{':
			o := NewMap()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(kt.(string), v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			l := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				l = append(l, v)
			}
			_, err := dec.Token()
			return l, err
		}
	case json.Number:
		f, _ := x.Float64()
		return f, nil
	}
	return t, nil
}

// FromAny converts values decoded by encoding/json (map[string]any) into *Map values; map keys
// are sorted since their original order is lost.
func FromAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		o := NewMap()
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			o.Set(k, FromAny(x[k]))
		}
		return o
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = FromAny(e)
		}
		return l
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return v
}

// ---- PHP helpers ----------------------------------------------------------------------------

// asMap returns v as a map; a PHP empty array ([]) is an empty map.
func asMap(v any) *Map {
	switch x := v.(type) {
	case *Map:
		return x
	case []any:
		o := NewMap()
		for i, e := range x {
			o.Set(strconv.Itoa(i), e)
		}
		return o
	}
	return nil
}

// isArray reports whether v is a PHP array (map or list).
func isArray(v any) bool {
	switch v.(type) {
	case *Map, []any:
		return true
	}
	return false
}

// phpEmpty mirrors PHP empty().
func phpEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case string:
		return x == "" || x == "0"
	case float64:
		return x == 0
	case int:
		return x == 0
	case []any:
		return len(x) == 0
	case *Map:
		return x.Len() == 0
	case Markup:
		return x == "" || x == "0"
	}
	return false
}

// phpString mirrors PHP string conversion.
func phpString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case Markup:
		return string(x)
	case bool:
		if x {
			return "1"
		}
		return ""
	case float64:
		return phpNumber(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []any, *Map:
		return "Array"
	}
	return ""
}

// phpNumber formats a number the way PHP prints it (integers without a fraction, floats with
// precision 14 like PHP's default string conversion).
func phpNumber(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "0"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	s := strconv.FormatFloat(f, 'G', 14, 64)
	if strings.ContainsAny(s, "E") {
		s = strconv.FormatFloat(f, 'g', -1, 64)
	}
	return s
}

// isNumeric mirrors PHP 8 is_numeric.
func isNumeric(v any) bool {
	switch x := v.(type) {
	case float64, int, int64:
		return true
	case string:
		return numericRe.MatchString(x)
	}
	return false
}

var numericRe = regexp.MustCompile(`^[ \t\n\r\v\f]*[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?[ \t\n\r\v\f]*$`)

// toFloat converts a scalar to a number (PHP +$x).
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(x)
		end := 0
		for end < len(s) {
			c := s[end]
			if (c >= '0' && c <= '9') || c == '.' || ((c == '-' || c == '+') && end == 0) || ((c == 'e' || c == 'E') && end > 0) {
				end++
				continue
			}
			break
		}
		for end > 0 {
			if f, err := strconv.ParseFloat(s[:end], 64); err == nil {
				return f
			}
			end--
		}
	}
	return 0
}

// looseEq mirrors PHP 8 ==.
func looseEq(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	switch x := a.(type) {
	case bool:
		return x == !phpEmpty(b)
	case nil:
		return phpEmpty(b) && !isNumericString(b)
	}
	if y, ok := b.(bool); ok {
		return y == !phpEmpty(a)
	}
	if b == nil {
		return phpEmpty(a) && !isNumericString(a)
	}
	an, aNum := numberOf(a)
	bn, bNum := numberOf(b)
	if aNum && bNum {
		return an == bn
	}
	as, aStr := a.(string)
	bs, bStr := b.(string)
	if aStr && bStr {
		return as == bs
	}
	if aNum && bStr || bNum && aStr {
		return phpString(a) == phpString(b)
	}
	return phpString(a) == phpString(b)
}

func isNumericString(v any) bool {
	s, ok := v.(string)
	return ok && isNumeric(s)
}

func numberOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case string:
		if isNumeric(x) {
			return toFloat(x), true
		}
	}
	return 0, false
}

// strictEq mirrors PHP === for scalars.
func strictEq(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	}
	return false
}

// listOf returns the values of a PHP array in order.
func listOf(v any) []any {
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

// isList reports whether a value is a PHP list.
func isList(v any) bool { _, ok := v.([]any); return ok }

// get reads a nested key path from maps.
func get(v any, keys ...string) any {
	for _, k := range keys {
		m := asMap(v)
		if m == nil {
			return nil
		}
		v = m.Get(k)
	}
	return v
}
