package atomic

// Front-end assets of atomic elements: the inline CSS the atomic widgets module (and the atomic
// form) add to the frontend stylesheet, and the scripts elements depend on.

// FrontendInlineCSS is added after the frontend stylesheet on pages with atomic elements
// (Module::add_inline_styles plus the atomic form's textarea/submit rules).
const FrontendInlineCSS = `
.e-heading-base a, .e-paragraph-base a { all: unset; cursor: pointer; }form[data-element_type="e-form"].form-state-success [data-element_type="e-form-success-message"],form[data-element_type="e-form"].form-state-error [data-element_type="e-form-error-message"]{ display: block; }.elementor-edit-mode [data-element_type="e-form-success-message"]:is(.elementor-element-editable, :has(.elementor-element-editable), :has(> .elementor-empty-view)),.elementor-edit-mode [data-element_type="e-form-error-message"]:is(.elementor-element-editable, :has(.elementor-element-editable), :has(> .elementor-empty-view)){ display: block; }.e-background-video { position: relative; overflow: hidden; }.e-background-video__media { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; pointer-events: none; z-index: 0; }.e-background-video__content { position: relative; z-index: 1; flex: 1 1 auto; }.elementor-edit-mode .e-background-video__content.e-atomic-element:has(> .elementor-empty-view) { display: flex; flex-direction: column; flex: 1 1 auto; min-height: 0; }.elementor-edit-mode .e-background-video__content.e-atomic-element:has(> .elementor-empty-view) > .elementor-empty-view.elementor-empty-view { position: relative; flex: 1 1 auto; min-height: 120px; height: auto; width: 100%; }.elementor-edit-mode .e-background-video__content.e-atomic-element:has(> .elementor-empty-view) > .elementor-empty-view > .elementor-first-add { position: absolute; inset: 0; width: 100%; height: 100%; }.e-background-video__controls { z-index: 2; }.e-background-video__play, .e-background-video__pause { appearance: none; -webkit-appearance: none; }.e-background-video.e-background-video--playing .e-background-video__play { display: none; }.e-background-video.e-background-video--paused .e-background-video__pause { display: none; }.e-background-video:not(.e-background-video--playing):not(.e-background-video--paused) .e-background-video__controls,.e-background-video:not(.e-background-video--playing):not(.e-background-video--paused) .e-background-video__play,.e-background-video:not(.e-background-video--playing):not(.e-background-video--paused) .e-background-video__pause { display: none; }.e-accordion-item-header-base { list-style: none; }.e-accordion-item-header-base::-webkit-details-marker { display: none; }.e-accordion-item-base { interpolate-size: allow-keywords; }.e-accordion-item-base::details-content {block-size: 0; overflow: hidden;transition: block-size .3s ease, content-visibility .3s ease allow-discrete;}.e-accordion-item-base[open]::details-content { block-size: auto; }.e-accordion-item-icon-base.e-accordion-item-icon-base .e-svg-base { width: auto; height: 100%; max-width: 100%; }.e-accordion-item-icon-base.e-accordion-item-icon-base .e-svg-base svg { width: auto !important; }.e-accordion-item-icon-base svg { transition: transform .3s ease; }.e-accordion-item-base[open] > summary .e-accordion-item-icon-base svg { transform: rotate(180deg); }
.e-form-textarea-base:not([data-resizable]) { resize: none; } .e-form-submit-button-base { cursor: pointer; box-sizing: border-box; }`

// scriptDeps lists each script handle's dependencies (wp_register_script).
var scriptDeps = map[string][]string{
	"elementor-youtube-handler":                  {"elementor-v2-frontend-handlers"},
	"elementor-tabs-handler":                     {"elementor-v2-frontend-handlers", "elementor-v2-alpinejs"},
	"elementor-tabs-preview-handler":             {"elementor-v2-frontend-handlers", "elementor-v2-alpinejs"},
	"elementor-background-video-handler":         {"elementor-v2-frontend-handlers", "elementor-v2-alpinejs"},
	"elementor-background-video-preview-handler": {"elementor-v2-frontend-handlers", "elementor-v2-alpinejs"},
	"elementor-accordion-preview-handler":        {"elementor-v2-frontend-handlers"},
	"elementor-v2-action-link-handlers":          {"elementor-v2-frontend-handlers"},
	"elementor-v2-form-handlers":                 {"elementor-v2-frontend-handlers", "elementor-v2-alpinejs"},
}

// ScriptFiles maps the atomic script handles to their files under the editor assets folder.
var ScriptFiles = map[string]string{
	"elementor-v2-frontend-handlers":             "js/packages/frontend-handlers/frontend-handlers.min.js",
	"elementor-v2-alpinejs":                      "js/packages/alpinejs/alpinejs.min.js",
	"elementor-youtube-handler":                  "js/youtube-handler.min.js",
	"elementor-tabs-handler":                     "js/tabs-handler.min.js",
	"elementor-tabs-preview-handler":             "js/tabs-preview-handler.min.js",
	"elementor-background-video-handler":         "js/background-video-handler.min.js",
	"elementor-background-video-preview-handler": "js/background-video-preview-handler.min.js",
	"elementor-accordion-preview-handler":        "js/accordion-preview-handler.min.js",
	"elementor-v2-action-link-handlers":          "js/atomic-widgets-action-link-handler.min.js",
	"elementor-v2-form-handlers":                 "js/atomic-widgets-form-handler.min.js",
}

// elementScripts mirrors get_script_depends of an atomic element.
func elementScripts(n *Node, preview bool) []string {
	var out []string
	if hasActionLink(n) {
		out = append(out, "elementor-v2-action-link-handlers")
	}
	out = append(out, loopScripts(n)...) // gap-loops: the Loop's pagination handler (loop.go)
	switch n.Type() {
	case "e-form":
		out = append(out, "elementor-v2-form-handlers")
	case "e-youtube":
		out = append(out, "elementor-youtube-handler")
	case "e-tabs":
		out = append(out, "elementor-tabs-handler")
		if preview {
			out = append(out, "elementor-tabs-preview-handler")
		}
	case "e-background-video":
		out = append(out, "elementor-background-video-handler")
		if preview {
			out = append(out, "elementor-background-video-preview-handler")
		}
	case "e-accordion":
		if preview {
			out = append(out, "elementor-accordion-preview-handler")
		}
	}
	return out
}

func hasActionLink(n *Node) bool {
	link, _, ok := transformable(n.Settings.Get("link"))
	if !ok {
		return false
	}
	v := asMap(link.Get("value"))
	return tagValue(v.Get("tag")) == "button" && !phpEmpty(get(v.Get("destination"), "value"))
}

// Scripts returns the script handles a tree needs, dependencies first, in enqueue order.
func Scripts(nodes []*Node, preview bool) []string {
	var out []string
	seen := map[string]bool{}
	var add func(h string)
	add = func(h string) {
		if seen[h] {
			return
		}
		seen[h] = true
		for _, d := range scriptDeps[h] {
			add(d)
		}
		out = append(out, h)
	}
	var walk func([]*Node)
	walk = func(list []*Node) {
		for _, n := range list {
			if Lookup(n.Type()) != nil {
				for _, h := range elementScripts(n, preview) {
					add(h)
				}
			}
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

// HasAtomic reports whether a tree contains atomic elements.
func HasAtomic(nodes []*Node) bool {
	for _, n := range nodes {
		if Lookup(n.Type()) != nil || HasAtomic(n.Children) {
			return true
		}
	}
	return false
}
