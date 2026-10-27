package builder

// PHP value semantics used by upstream's PHP code (empty(), (string) casts, is_numeric, loose ==,
// array-key normalisation). Settings decoded from JSON are nil, bool, float64, string, []any or
// map[string]any; these helpers make them behave the way the PHP originals do.

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// isEmpty mirrors PHP empty().
func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case bool:
		return !x
	case float64:
		return x == 0
	case int:
		return x == 0
	case int64:
		return x == 0
	case string:
		return x == "" || x == "0"
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	}
	return false
}

func truthy(v any) bool { return !isEmpty(v) }

// phpString mirrors a PHP (string) cast.
func phpString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		if x {
			return "1"
		}
		return ""
	case string:
		return x
	case float64:
		return phpFloatString(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case []any, map[string]any:
		return "Array"
	}
	return ""
}

// phpFloatString formats a float like PHP's (string) cast (precision=14).
func phpFloatString(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	s := strconv.FormatFloat(f, 'G', 14, 64)
	if strings.ContainsAny(s, "E") {
		// PHP prints 1.0E-5 style; keep Go's but normalise the exponent form.
		mant, exp, _ := strings.Cut(s, "E")
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		e, _ := strconv.Atoi(exp)
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		return mant + "E" + sign + strconv.Itoa(e)
	}
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	return s
}

// isNumeric mirrors PHP is_numeric().
func isNumeric(v any) bool {
	switch x := v.(type) {
	case float64, int, int64:
		return true
	case string:
		return numericString(x)
	}
	return false
}

func numericString(s string) bool {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	s = strings.TrimRight(s, " \t\n\r\v\f")
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		ed := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			ed++
		}
		if ed == 0 {
			return false
		}
	}
	return i == len(s)
}

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
		// leading numeric prefix, like PHP's (float) cast
		end := 0
		for end < len(s) && strings.ContainsRune("+-.0123456789eE", rune(s[end])) {
			end++
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

func toInt(v any) int { return int(toFloat(v)) }

// phpKey normalises a value used as a PHP array key (isset($arr[$value])).
func phpKey(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case bool:
		if x {
			return "1", true
		}
		return "0", true
	case float64:
		return strconv.FormatInt(int64(x), 10), true
	case int:
		return strconv.Itoa(x), true
	case string:
		return x, true
	}
	return "", false
}

// looseEq mirrors PHP 8 ==.
func looseEq(a, b any) bool {
	if a == nil || b == nil {
		if a == nil && b == nil {
			return true
		}
		o := a
		if a == nil {
			o = b
		}
		return isEmpty(o) && !isNumericZeroString(o)
	}
	switch x := a.(type) {
	case bool:
		return x == truthy(b)
	case string:
		switch y := b.(type) {
		case string:
			if numericString(x) && numericString(y) {
				return toFloat(x) == toFloat(y)
			}
			return x == y
		case bool:
			return truthy(x) == y
		case float64, int:
			if numericString(x) {
				return toFloat(x) == toFloat(y)
			}
			return x == phpString(y)
		}
	case float64, int:
		switch y := b.(type) {
		case string:
			return looseEq(y, a)
		case bool:
			return truthy(a) == y
		case float64, int:
			return toFloat(a) == toFloat(y)
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			if bb, ok := b.(bool); ok {
				return truthy(a) == bb
			}
			return false
		}
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !looseEq(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			if bb, ok := b.(bool); ok {
				return truthy(a) == bb
			}
			return false
		}
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if !looseEq(v, y[k]) {
				return false
			}
		}
		return true
	}
	return false
}

// "0" == null is false in PHP 8? (null == "0" → false since null converts to "" for string comparison)
func isNumericZeroString(v any) bool {
	s, ok := v.(string)
	return ok && s == "0"
}

// strictEq mirrors PHP ===.
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
		switch y := b.(type) {
		case float64:
			return x == y
		case int:
			return x == float64(y)
		}
		return false
	case int:
		switch y := b.(type) {
		case float64:
			return float64(x) == y
		case int:
			return x == y
		}
		return false
	case []any:
		y, ok := b.([]any)
		if !ok {
			if m, ok := b.(map[string]any); ok && len(m) == 0 && len(x) == 0 {
				return true
			}
			return false
		}
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !strictEq(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			if l, ok := b.([]any); ok && len(l) == 0 && len(x) == 0 {
				return true
			}
			return false
		}
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !strictEq(v, w) {
				return false
			}
		}
		return true
	}
	return false
}

// inArrayStrict mirrors in_array($needle, $haystack, true).
func inArrayStrict(needle any, haystack any) bool {
	switch h := haystack.(type) {
	case []any:
		for _, v := range h {
			if strictEq(needle, v) {
				return true
			}
		}
	case map[string]any:
		for _, v := range h {
			if strictEq(needle, v) {
				return true
			}
		}
	}
	return false
}

// asMap returns v as a map (PHP arrays decoded as JSON lists are treated as maps keyed by index).
func asMap(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case []any:
		m := make(map[string]any, len(x))
		for i, e := range x {
			m[strconv.Itoa(i)] = e
		}
		return m
	}
	return nil
}

func asList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, ea := strconv.Atoi(keys[i])
			b, eb := strconv.Atoi(keys[j])
			if ea == nil && eb == nil {
				return a < b
			}
			return keys[i] < keys[j]
		})
		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, x[k])
		}
		return out
	}
	return nil
}

func isArray(v any) bool {
	switch v.(type) {
	case []any, map[string]any:
		return true
	}
	return false
}

// str returns a setting as a string ("" for missing or non-scalar values).
func str(v any) string {
	switch v.(type) {
	case []any, map[string]any:
		return ""
	}
	return phpString(v)
}

// sub returns m[key] for map-like values.
func sub(v any, key string) any {
	if m := asMap(v); m != nil {
		return m[key]
	}
	return nil
}

// deepCopy copies JSON-like values.
func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, e := range x {
			m[k] = deepCopy(e)
		}
		return m
	case []any:
		l := make([]any, len(x))
		for i, e := range x {
			l[i] = deepCopy(e)
		}
		return l
	}
	return v
}

func itoa(i int) string { return strconv.Itoa(i) }
