package server

import (
	"errors"
	"strconv"
	"strings"
)

// A reader for PHP's serialize() format, the form WordPress stores array post meta in (WooCommerce's
// _product_attributes, _downloadable_files, _upsell_ids, ...). WXR files carry those values as they
// are in the database. Only data is read: an object (O:) is read as an array of its properties.

// phpArray is a PHP array with its keys in order (int64 or string keys).
type phpArray struct {
	Keys []any
	Vals []any
}

// Get returns the value of a key (compared as text, like PHP's "1" == 1 keys).
func (a *phpArray) Get(key string) any {
	if a == nil {
		return nil
	}
	for i, k := range a.Keys {
		if phpString(k) == key {
			return a.Vals[i]
		}
	}
	return nil
}

var errPHPSerialized = errors.New("not a serialized PHP value")

// phpUnserialize reads one serialized value: nil, bool, int64, float64, string or *phpArray.
func phpUnserialize(v string) (any, error) {
	r := &phpReader{s: v}
	out, err := r.value(0)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type phpReader struct {
	s string
	i int
}

func (r *phpReader) until(c byte) (string, error) {
	j := strings.IndexByte(r.s[r.i:], c)
	if j < 0 {
		return "", errPHPSerialized
	}
	out := r.s[r.i : r.i+j]
	r.i += j + 1
	return out, nil
}

func (r *phpReader) expect(c byte) error {
	if r.i >= len(r.s) || r.s[r.i] != c {
		return errPHPSerialized
	}
	r.i++
	return nil
}

func (r *phpReader) value(depth int) (any, error) {
	if depth > 32 || r.i+1 >= len(r.s) {
		return nil, errPHPSerialized
	}
	t := r.s[r.i]
	if t == 'N' {
		r.i++
		return nil, r.expect(';')
	}
	r.i++
	if err := r.expect(':'); err != nil {
		return nil, err
	}
	switch t {
	case 'b', 'i', 'd':
		raw, err := r.until(';')
		if err != nil {
			return nil, err
		}
		switch t {
		case 'b':
			return raw == "1", nil
		case 'i':
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, errPHPSerialized
			}
			return n, nil
		default:
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, errPHPSerialized
			}
			return f, nil
		}
	case 's':
		return r.str()
	case 'a', 'O':
		if t == 'O' { // O:8:"stdClass":2:{...}
			if _, err := r.until(':'); err != nil {
				return nil, err
			}
			if _, err := r.until(':'); err != nil {
				return nil, err
			}
		}
		raw, err := r.until(':')
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > len(r.s) {
			return nil, errPHPSerialized
		}
		if err := r.expect('{'); err != nil {
			return nil, err
		}
		arr := &phpArray{}
		for range n {
			k, err := r.value(depth + 1)
			if err != nil {
				return nil, err
			}
			switch k.(type) {
			case int64, string:
			default:
				return nil, errPHPSerialized
			}
			val, err := r.value(depth + 1)
			if err != nil {
				return nil, err
			}
			arr.Keys = append(arr.Keys, k)
			arr.Vals = append(arr.Vals, val)
		}
		if err := r.expect('}'); err != nil {
			return nil, err
		}
		return arr, nil
	}
	return nil, errPHPSerialized
}

// str reads the rest of s:<bytes>:"...";
func (r *phpReader) str() (string, error) {
	raw, err := r.until(':')
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return "", errPHPSerialized
	}
	if err := r.expect('"'); err != nil {
		return "", err
	}
	if n > len(r.s)-r.i { // not r.i+n > len: a length near MaxInt overflows the sum and passes
		return "", errPHPSerialized
	}
	out := r.s[r.i : r.i+n]
	r.i += n
	if err := r.expect('"'); err != nil {
		return "", err
	}
	return out, r.expect(';')
}

// phpString is a scalar as PHP prints it ("" for arrays and null).
func phpString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
	}
	return ""
}

// phpTruthy is PHP's boolean cast of a scalar.
func phpTruthy(v any) bool {
	s := phpString(v)
	return s != "" && s != "0"
}

// phpIDs reads a list of post ids: a serialized array (_upsell_ids, _children) or a comma list
// (_product_image_gallery).
func phpIDs(v string) []int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var raw []string
	if parsed, err := phpUnserialize(v); err == nil {
		if arr, ok := parsed.(*phpArray); ok {
			for _, x := range arr.Vals {
				raw = append(raw, phpString(x))
			}
		}
	} else {
		raw = strings.Split(v, ",")
	}
	var out []int64
	for _, s := range raw {
		if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil && id > 0 {
			out = append(out, id)
		}
	}
	return out
}
