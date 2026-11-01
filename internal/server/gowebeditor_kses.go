package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Builder documents are rendered as stored: the HTML widget, text editors, custom CSS and other fields
// print their values unescaped. Like Elementor's Document::save() (map_deep with wp_kses_post when the
// user lacks unfiltered_html), a save by a user without unfiltered_html (authors, contributors, a
// shop manager denied it) runs every string value through wp_kses_post, so they cannot store script
// that runs for the administrator who previews or publishes their page.

// gweNeedsKses reports whether the user's builder saves must be filtered.
func gweNeedsKses(u *store.User) bool { return !u.Can("unfiltered_html", nil) }

// gweKsesString filters one value. Only values with markup are touched: plain values (URLs, colours,
// numbers, CSS without tags) stay byte for byte as wp_kses_post would leave their meaning anyway.
func gweKsesString(v string) string {
	if !strings.ContainsRune(v, '<') {
		return v
	}
	return wp.KsesPost(v)
}

// gweKsesValue filters every string of a decoded value in place (map_deep).
func gweKsesValue(v any) any {
	switch t := v.(type) {
	case string:
		return gweKsesString(t)
	case map[string]any:
		for k, x := range t {
			t[k] = gweKsesValue(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = gweKsesValue(x)
		}
		return t
	}
	return v
}

// gweKsesJSON filters every string value of a JSON document and keeps everything else as it was,
// including the key order (the stored element tree's order shows in the rendered markup).
func gweKsesJSON(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	type frame struct {
		obj       bool
		n         int  // members / items written
		expectKey bool // in an object: the next string is a key
	}
	var stack []*frame
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	writeString := func(s string) error {
		if err := enc.Encode(s); err != nil {
			return err
		}
		out.Truncate(out.Len() - 1) // Encode appends a newline
		return nil
	}
	// beforeValue writes the separator a value needs in its container.
	beforeValue := func() {
		if len(stack) == 0 {
			return
		}
		f := stack[len(stack)-1]
		if f.obj {
			out.WriteByte(':')
		} else if f.n > 0 {
			out.WriteByte(',')
		}
	}
	// afterValue records a finished value in its container.
	afterValue := func() {
		if len(stack) == 0 {
			return
		}
		f := stack[len(stack)-1]
		f.n++
		if f.obj {
			f.expectKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{', '[':
				beforeValue()
				out.WriteByte(byte(t))
				stack = append(stack, &frame{obj: t == '{', expectKey: t == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
				out.WriteByte(byte(t))
				afterValue()
			}
		case string:
			if len(stack) > 0 && stack[len(stack)-1].obj && stack[len(stack)-1].expectKey {
				f := stack[len(stack)-1]
				if f.n > 0 {
					out.WriteByte(',')
				}
				if err := writeString(t); err != nil {
					return nil, err
				}
				f.expectKey = false
				continue
			}
			beforeValue()
			if err := writeString(gweKsesString(t)); err != nil {
				return nil, err
			}
			afterValue()
		case json.Number:
			beforeValue()
			out.WriteString(t.String())
			afterValue()
		case bool:
			beforeValue()
			if t {
				out.WriteString("true")
			} else {
				out.WriteString("false")
			}
			afterValue()
		case nil:
			beforeValue()
			out.WriteString("null")
			afterValue()
		}
	}
	return out.Bytes(), nil
}
