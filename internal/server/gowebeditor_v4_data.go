package server

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
)

// GoWebEditor v4 (atomic): the editor data captured from the upstream editor with the atomic / "Editor
// V4" experiments on (e_atomic_elements, e_opt_in_v4 and the experiments they gate: classes, variables,
// components, interactions, default styles). The files in internal/server/gowebeditor/v4/ are built by
// scratchpad gwe-tools/gen_data_v4.py the same way gen_data.py builds the v3 set, but keep the v2
// editing panel packages, the atomic element and widget definitions (e-*), the "v4-elements" and
// "atomic-form" panel categories and the editor config.atomic. The v3 files stay embedded as the fallback
// when v4 is disabled (WPGO_GWE_V4=0).

//go:embed gowebeditor/v4/*.gz gowebeditor/v4/preview-styles.json
var gweV4FS embed.FS

// gweV4Enabled turns the atomic editor on; WPGO_GWE_V4=0 falls back to the v3 editor data.
var gweV4Enabled = os.Getenv("WPGO_GWE_V4") != "0"

// gweV4DocTypes are the document configs the v4 capture replaces (plus the component document).
var gweV4DocTypes = []string{"wp-page", "wp-post", "kit", "section", "elementor_component"}

func gweV4Read(name string) []byte {
	b, err := gweV4FS.ReadFile("gowebeditor/v4/" + name)
	if err != nil {
		panic("gowebeditor v4 data: " + err.Error())
	}
	if !strings.HasSuffix(name, ".gz") {
		return b
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		panic("gowebeditor v4 data: " + err.Error())
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		panic("gowebeditor v4 data: " + err.Error())
	}
	return out
}

// gweV4Apply swaps the editor data for the v4 set. It runs once, from gweData, after the Pro layer
// (gweProLoad) has loaded: the v4 shell gets Pro's patch again, Pro's widgets that the v4 capture lacks
// are kept, and Pro's request-time the editor config patch takes the v4 capture's values (the lab had
// Pro active) so it does not put back the element and control lists captured with atomic off.
func gweV4Apply(d *gweStore) {
	if !gweV4Enabled {
		return
	}
	must := func(err error) {
		if err != nil {
			panic("gowebeditor v4 data: " + err.Error())
		}
	}
	shell := string(gweV4Read("editor-shell.html.gz"))
	if gwePro.templates != "" {
		shell = gweProPatchShell(shell, gwePro.templates)
	}
	shell = gweV4PatchShell(shell)
	var editorCfg map[string]json.RawMessage
	must(json.Unmarshal(gweV4Read("editor-config.json.gz"), &editorCfg))
	var commonCfg, frontCfg map[string]any
	must(json.Unmarshal(gweV4Read("common-config.json.gz"), &commonCfg))
	must(json.Unmarshal(gweV4Read("frontend-config.json.gz"), &frontCfg))
	var previewStyles [][2]string
	must(json.Unmarshal(gweV4Read("preview-styles.json"), &previewStyles))
	docs := map[string]map[string]json.RawMessage{}
	for _, t := range gweV4DocTypes {
		m := map[string]json.RawMessage{}
		must(json.Unmarshal(gweV4Read("doc-"+t+".json.gz"), &m))
		docs[t] = m
	}
	docWidgets, err := gweV4MergeObjects(gweV4Read("doc-widgets.json.gz"), d.docWidgets)
	must(err)
	widgetsCfg, err := gweV4MergeObjects(gweV4Read("widgets-config.json.gz"), d.widgetsCfg)
	must(err)

	d.shell = shell
	d.editorCfg = editorCfg
	d.commonCfg = commonCfg
	d.appCfg = gweV4Read("app-config.json.gz")
	d.frontCfg = frontCfg
	if d.docs == nil {
		d.docs = map[string]map[string]json.RawMessage{}
	}
	for t, m := range docs {
		d.docs[t] = m
	}
	d.docWidgets = docWidgets
	d.widgetsCfg = widgetsCfg
	d.i18n = gweV4Read("i18n-rebrand.json.gz")
	d.previewStyles = previewStyles
	for k := range gwePro.configPatch {
		if k == "customIconsURL" || k == "admin_url" {
			continue
		}
		if v, ok := editorCfg[k]; ok {
			gwePro.configPatch[k] = v
		}
	}
}

// gweV4MergeObjects returns the JSON object primary followed by the members of extra whose keys
// primary lacks, keeping both key orders (the panel lists widgets in config order).
func gweV4MergeObjects(primary, extra []byte) (json.RawMessage, error) {
	p, err := gweV4Members(primary)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(extra)) == 0 {
		return primary, nil
	}
	x, err := gweV4Members(extra)
	if err != nil {
		return primary, nil // nothing usable to add
	}
	seen := make(map[string]bool, len(p))
	var buf bytes.Buffer
	buf.WriteByte('{')
	write := func(k string, v json.RawMessage) {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(v)
	}
	for _, m := range p {
		seen[m.k] = true
		write(m.k, m.v)
	}
	for _, m := range x {
		if !seen[m.k] {
			write(m.k, m.v)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

type gweV4Member struct {
	k string
	v json.RawMessage
}

func gweV4Members(raw []byte) ([]gweV4Member, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if dl, ok := t.(json.Delim); !ok || dl != '{' {
		return nil, errors.New("not a JSON object")
	}
	var out []gweV4Member
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, gweV4Member{kt.(string), v})
	}
	return out, nil
}

var (
	gweV4AtomicOnce sync.Once
	gweV4AtomicJSON json.RawMessage
)

// gweV4AtomicElements is the map of every atomic element and widget type (e-*, e-component and the
// atomic form included) to its render definition: {elType: "widget"|"element", title,
// twig_main_template, twig_templates, base_styles, base_styles_dictionary, atomic_props_schema,
// default_html_tag, html_tag_follows_link, initial_attributes, atomic_style_states, default_children}
// (the keys each type has). Asset URLs in prop defaults carry the @@ASSETS@@ placeholder (gweFill).
func gweV4AtomicElements() json.RawMessage {
	gweV4AtomicOnce.Do(func() {
		gweV4AtomicJSON = gweV4Read("atomic-elements.json.gz")
	})
	return gweV4AtomicJSON
}

// gweV4ProPackages are the Pro editor packages (web/gowebeditor/pro/js/packages, copied and stripped of
// promotions by tools/gowebeditor/patch_pro_packages.py; see NOTICE) the atomic editor loads after the core
// v2 packages, in dependency order: the components editor (save as component, properties, instance
// panel), the atomic display-conditions / attributes / custom CSS controls, the form controls.
var gweV4ProPackages = []string{"core-adapter-utils", "editor-canvas-extended", "editor-templates-extended",
	"editor-controls-extended", "editor-editing-panel-extended", "editor-components-extended",
	"editor-collection-loop"} // gap-loops: the atomic Loop element's editor (tools/gowebeditor/patch_loop_package.py)

// gweV4PatchShell adds the atomic editor's Pro scripts to the v4 shell and gives every editor asset
// the per-build cache buster (some upstream packages are patched; ?ver=4.3.5 would keep stale copies).
func gweV4PatchShell(h string) string {
	add := func(h, afterID, tag string) string {
		if strings.Contains(h, tag) {
			return h
		}
		anchor := `id="` + afterID + `"`
		i := strings.Index(h, anchor)
		if i < 0 {
			return h
		}
		end := strings.Index(h[i:], "</script>")
		if end < 0 {
			return h
		}
		pos := i + end + len("</script>")
		return h[:pos] + "\n" + tag + h[pos:]
	}
	after := "elementor-v2-editor-components-js"
	for _, p := range gweV4ProPackages {
		id := "elementor-v2-" + p + "-js"
		if strings.Contains(h, `id="`+id+`"`) {
			after = id
			continue
		}
		h = add(h, after, `<script id="`+id+`" src="@@GWEASSETS@@pro/js/packages/`+p+`/`+p+`.min.js?ver=@@GWEVER@@"></script>`)
		after = id
	}
	h = add(h, "elementor-pro-js", `<script src="@@GWEASSETS@@pro/js/atomic-form-editor.min.js?ver=@@GWEVER@@" id="elementor-pro-atomic-form-editor-js"></script>`)
	for _, old := range []string{"?ver=4.3.5", "?ver=@@VER@@"} {
		h = gweV4AssetVer.ReplaceAllStringFunc(h, func(m string) string { return strings.Replace(m, old, "?ver=@@GWEVER@@", 1) })
	}
	return h
}

// gweV4AssetVer matches the editor's own asset URLs (GoWebEditor assets, upstream or Pro).
var gweV4AssetVer = regexp.MustCompile(`@@(?:GWE)?ASSETS@@[^"'\s]*\?ver=(?:4\.3\.5|@@VER@@)`)
