package server

// Code Highlight: upstream loads Prism 1.23.0 from cdnjs. Golementor ships Prism (MIT, pinned to
// 1.30.0, the release with the DOM-clobbering fix) under web/gowebeditor/lib/prismjs: the core, the
// autoloader with the components of every language the widget offers (and their dependencies) and
// the normalize-whitespace, line-numbers, line-highlight, toolbar and copy-to-clipboard plugins.
// The handles keep upstream's names and print order (after lottie), so pages load no third-party
// script and the front-end CSP stays 'self'.

const gwePrismBase = "/wp-content/plugins/gowebeditor/assets/lib/prismjs/"

// gwePrismHandles are the Prism script handles in upstream's registration order.
var gwePrismHandles = []string{"prismjs_core", "prismjs_loader", "prismjs_normalize", "prismjs_line_numbers",
	"prismjs_line_highlight", "prismjs_toolbar", "prismjs_copy_to_clipboard"}

var gwePrismFiles = map[string]string{
	"prismjs_core":              "components/prism-core.min.js",
	"prismjs_loader":            "plugins/autoloader/prism-autoloader.min.js",
	"prismjs_normalize":         "plugins/normalize-whitespace/prism-normalize-whitespace.min.js",
	"prismjs_line_numbers":      "plugins/line-numbers/prism-line-numbers.min.js",
	"prismjs_line_highlight":    "plugins/line-highlight/prism-line-highlight.min.js",
	"prismjs_toolbar":           "plugins/toolbar/prism-toolbar.min.js",
	"prismjs_copy_to_clipboard": "plugins/copy-to-clipboard/prism-copy-to-clipboard.min.js",
}

// gwePrismDeps are the wp_register_script dependencies (a plugin needs the scripts before it).
var gwePrismDeps = map[string][]string{
	"prismjs_loader":            {"prismjs_core"},
	"prismjs_normalize":         {"prismjs_core"},
	"prismjs_line_numbers":      {"prismjs_normalize"},
	"prismjs_line_highlight":    {"prismjs_normalize"},
	"prismjs_toolbar":           {"prismjs_normalize"},
	"prismjs_copy_to_clipboard": {"prismjs_toolbar"},
}

func init() {
	for h, f := range gwePrismFiles {
		gweProScriptFiles[h] = gwePrismBase + f
	}
	order := make([]string, 0, len(gweProScriptOrder)+len(gwePrismHandles))
	inserted := false
	for _, h := range gweProScriptOrder {
		order = append(order, h)
		if h == "lottie" {
			order = append(order, gwePrismHandles...)
			inserted = true
		}
	}
	if !inserted {
		order = append(order, gwePrismHandles...)
	}
	gweProScriptOrder = order
}

// gwePrismScripts completes the requested Prism handles with their dependencies; preview loads
// them all (the editor adds Code Highlight widgets at any time).
func gwePrismScripts(scripts map[string]bool, preview bool) {
	if preview {
		for _, h := range gwePrismHandles {
			scripts[h] = true
		}
		return
	}
	for changed := true; changed; {
		changed = false
		for h, deps := range gwePrismDeps {
			if !scripts[h] {
				continue
			}
			for _, d := range deps {
				if !scripts[d] {
					scripts[d], changed = true, true
				}
			}
		}
	}
}
