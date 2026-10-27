package builder

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Group B fixtures (pro-b-*): the lab's library templates 234 (a section) and 237 (a global
// widget) are testdata/golden/pro-b-tpl-<id>.data.json.

var proBTestGlobalIDs = map[string]int64{"b300002": 237}

func init() {
	prev := proTestRC
	proTestRC = func(rc *RenderContext) {
		if prev != nil {
			prev(rc)
		}
		if rc.Pro == nil {
			return
		}
		rc.Pro.Template = func(id int64) *Document {
			raw, err := os.ReadFile("testdata/golden/pro-b-tpl-" + itoa64(id) + ".data.json")
			if err != nil {
				return nil
			}
			els, err := ParseElements(raw)
			if err != nil {
				return nil
			}
			typ := "section"
			if id == 237 {
				typ = "widget"
			}
			return &Document{ID: id, Type: typ, Elements: els, Settings: map[string]any{}}
		}
	}
	prevGlobal := ProBGlobalTemplateID
	ProBGlobalTemplateID = func(el *Element) int64 {
		if id, ok := proBTestGlobalIDs[el.ID]; ok {
			return id
		}
		return prevGlobal(el)
	}
}

var (
	proBPostTypeRe = regexp.MustCompile(` data-elementor-post-type="page"`)
	proBStyleRe    = regexp.MustCompile(`<style id="elementor-post-\d+">.*?</style>`)
	proBNonceRe    = regexp.MustCompile(`(name="stripe_form_submit_nonce" value=")[^"]*"`)
	proBSettingsRe = regexp.MustCompile(`data-settings="([^"]*)"`)
)

// proBNormalize removes the differences documented in the pro-b-*.knowndiff files: the
// data-elementor-post-type root attribute (needs a renderDocument hook), the nested template's
// inline CSS (Golementor inlines it on the front end too), nonces, and the key order of
// repeater items inside data-settings (compared as JSON).
func proBNormalize(h string) string {
	h = proBPostTypeRe.ReplaceAllString(h, "")
	h = proBStyleRe.ReplaceAllString(h, "")
	h = proBNonceRe.ReplaceAllString(h, `${1}"`)
	return proBSettingsRe.ReplaceAllStringFunc(h, func(m string) string {
		raw := proBSettingsRe.FindStringSubmatch(m)[1]
		js := strings.NewReplacer("&quot;", `"`, "&lt;", "<", "&gt;", ">", "&#039;", "'", "&amp;", "&").Replace(raw)
		var v any
		if json.Unmarshal([]byte(js), &v) != nil {
			return m
		}
		b, _ := json.Marshal(proBSortKeys(v))
		return `data-settings="` + string(b) + `"`
	})
}

func proBSortKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := map[string]any{}
		for _, k := range keys {
			out[k] = proBSortKeys(x[k])
		}
		return out
	case []any:
		for i := range x {
			x[i] = proBSortKeys(x[i])
		}
	}
	return v
}

// TestProBWidgets compares the group-B fixtures exactly, apart from the documented differences.
func TestProBWidgets(t *testing.T) {
	rc := testRC(t)
	n := 0
	for _, f := range loadFixtures(t) {
		if !strings.HasPrefix(f.name, "pro-b-") || f.wantHTML == "" {
			continue
		}
		if proFixtureCurrent != nil {
			proFixtureCurrent(f.doc)
		}
		res, err := Render(f.doc, rc)
		if err != nil {
			t.Fatal(err)
		}
		got, want := proBNormalize(normalizeHTML(res.HTML)), proBNormalize(normalizeHTML(f.wantHTML))
		if f.name == "pro-b-3" {
			// global widget: data-widget_type stays "global.default" (the wrapper type comes from
			// the element; see pro-b-3.knowndiff)
			got = strings.Replace(got, `data-widget_type="global.default"`, `data-widget_type="heading.default"`, 1)
		}
		if got != want {
			t.Errorf("%s: HTML differs%s", f.name, firstDiff(got, want))
		}
		n++
	}
	if n < 4 {
		t.Errorf("only %d group-B fixtures", n)
	}
}

// TestProBDump writes the rendered HTML of the group-B fixtures (PROB_DUMP=dir) for diffing.
func TestProBDump(t *testing.T) {
	dir := os.Getenv("PROB_DUMP")
	if dir == "" {
		t.Skip("PROB_DUMP not set")
	}
	rc := testRC(t)
	for _, f := range loadFixtures(t) {
		if !strings.HasPrefix(f.name, "pro-b-") {
			continue
		}
		if proFixtureCurrent != nil {
			proFixtureCurrent(f.doc)
		}
		res, err := Render(f.doc, rc)
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(dir+"/"+f.name+".html", []byte(proBNormalize(normalizeHTML(res.HTML))), 0o644)
		_ = os.WriteFile(dir+"/"+f.name+".want.html", []byte(proBNormalize(normalizeHTML(f.wantHTML))), 0o644)
		_ = os.WriteFile(dir+"/"+f.name+".css", []byte(res.CSS), 0o644)
		_ = os.WriteFile(dir+"/"+f.name+".assets", []byte(strings.Join(res.Styles, " ")+"\n"+strings.Join(res.Scripts, " ")), 0o644)
	}
}

// TestProBDefaults renders every group-B widget with its defaults, in the editor and on the
// front end, without a Pro context.
func TestProBDefaults(t *testing.T) {
	repanic = true
	defer func() { repanic = false }()
	for _, name := range []string{"share-buttons", "hotspot", "table-of-contents", "video-playlist", "slides", "paypal-button", "stripe-button", "template", "global"} {
		el := &Element{ID: "b9" + "00001", ElType: "widget", WidgetType: name, Settings: map[string]any{}}
		if _, err := RenderWidget(el, &Document{ID: 1}, &RenderContext{}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		res, err := Render(&Document{ID: 1, Elements: []*Element{el}}, &RenderContext{})
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if strings.Contains(res.HTML, "unsupported element") && name != "global" {
			t.Errorf("%s: not registered", name)
		}
	}
}
