package builder

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
)

// UnmarshalJSON decodes an element, accepting PHP's empty-array settings ("settings": []) and
// remembering the saved settings key order.
func (e *Element) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID         any             `json:"id"`
		ElType     string          `json:"elType"`
		WidgetType string          `json:"widgetType"`
		IsInner    any             `json:"isInner"`
		Settings   json.RawMessage `json:"settings"`
		Elements   []*Element      `json:"elements"`
		TemplateID any             `json:"templateID"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	e.ID = phpString(aux.ID)
	e.ElType = aux.ElType
	e.WidgetType = aux.WidgetType
	e.IsInner = truthy(aux.IsInner)
	e.Elements = aux.Elements
	e.TemplateID = toInt64(aux.TemplateID)
	e.Settings = map[string]any{}
	e.settingsKeys = nil
	e.Raw = append(json.RawMessage(nil), data...) // GoWebEditor v4: atomic elements render from it
	raw := bytes.TrimSpace(aux.Settings)
	if len(raw) > 0 && raw[0] == '{' {
		if err := json.Unmarshal(raw, &e.Settings); err != nil {
			return err
		}
		e.settingsKeys = objectKeys(raw)
		e.nestedKeys = map[string][]string{}
		collectNestedKeys(raw, "", e.nestedKeys)
	}
	return nil
}

// collectNestedKeys records the key order of every object below the top level of raw.
func collectNestedKeys(raw []byte, path string, out map[string][]string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return
	}
	switch raw[0] {
	case '{':
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		if path != "" {
			out[path] = objectKeys(raw)
		}
		for k, v := range m {
			p := k
			if path != "" {
				p = path + "/" + k
			}
			collectNestedKeys(v, p, out)
		}
	case '[':
		var l []json.RawMessage
		if json.Unmarshal(raw, &l) != nil {
			return
		}
		for i, v := range l {
			collectNestedKeys(v, path+"/"+strconv.Itoa(i), out)
		}
	}
}

// orderedValue converts nested maps of a setting into insertion-ordered maps following the saved
// order (merged control defaults first, as PHP's array_merge does), for JSON output.
func orderedValue(el *Element, path string, v any, defaults any) any {
	switch x := v.(type) {
	case map[string]any:
		o := newOmap()
		if dm, ok := defaults.(map[string]any); ok {
			for _, k := range sortedKeys(dm) {
				if _, ok := x[k]; ok {
					o.Set(k, nil)
				}
			}
		}
		if el != nil {
			for _, k := range el.nestedKeys[path] {
				if _, ok := x[k]; ok {
					o.Set(k, nil)
				}
			}
		}
		if fo, ok := defaults.(repeaterFieldOrder); ok { // repeater item: missing fields follow in field order
			for _, k := range fo {
				if _, ok := x[k]; ok {
					o.Set(k, nil)
				}
			}
		}
		for _, k := range sortedKeys(x) {
			o.Set(k, nil)
		}
		for _, k := range o.keys {
			o.m[k] = orderedValue(el, path+"/"+k, x[k], nil)
		}
		return o
	case []any:
		l := make([]any, len(x))
		var itemDefaults any
		if fo, ok := defaults.(repeaterFieldOrder); ok {
			itemDefaults = fo
		}
		for i, e := range x {
			l[i] = orderedValue(el, path+"/"+strconv.Itoa(i), e, itemDefaults)
		}
		return l
	}
	return v
}

// objectKeys returns the top-level keys of a JSON object in document order.
func objectKeys(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		k, _ := t.(string)
		if !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// rawSettings returns the element's saved settings in saved order (keys not in the recorded order
// follow alphabetically).
func rawSettings(el *Element) *omap {
	o := newOmap()
	for _, k := range el.settingsKeys {
		if v, ok := el.Settings[k]; ok {
			o.Set(k, v)
		}
	}
	if len(o.keys) != len(el.Settings) {
		keys := make([]string, 0, len(el.Settings))
		for k := range el.Settings {
			if !o.Has(k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			o.Set(k, el.Settings[k])
		}
	}
	return o
}

func settingsOrder(doc *Document) []string {
	if doc == nil {
		return nil
	}
	return sortedPlainKeys(doc.Settings)
}

// repeaterFieldOrder is a repeater's field names in control order (Control_Repeater::get_value adds
// a saved item's missing fields in that order).
type repeaterFieldOrder []string
