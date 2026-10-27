package builder

// GoWebEditor v4: atomic elements (e-heading, e-flexbox, ...) render through internal/builder/atomic
// (their upstream Twig templates and the atomic props resolver). The atomic renderer hands
// non-atomic children back to printElement.

import (
	"strings"

	"wpgo/internal/builder/atomic"
)

// atomicNode converts an element subtree into atomic nodes (children keep a reference to their
// element so non-atomic ones print through the v3 renderer).
func atomicNode(el *Element) *atomic.Node {
	n := atomic.NodeFromJSON(el.Raw)
	if n == nil {
		n = &atomic.Node{ID: el.ID, ElType: el.ElType, WidgetType: el.WidgetType, Settings: atomic.NewMap(), Styles: atomic.NewMap()}
		if v, ok := atomic.FromAny(el.Settings).(*atomic.Map); ok {
			n.Settings = v
		}
	}
	n.Ref = el
	for _, c := range el.Elements {
		if c != nil {
			n.Children = append(n.Children, atomicNode(c))
		}
	}
	return n
}

// atomicEnv returns this render's atomic environment.
func (r *renderer) atomicEnv() *atomic.Env {
	if r.atomic != nil {
		return r.atomic
	}
	env := &atomic.Env{}
	if r.rc.Atomic != nil {
		cp := *r.rc.Atomic
		env = &cp
	}
	env.Editing = r.rc.Editing
	if env.AssetsURL == "" {
		env.AssetsURL = "http://localhost:8095/wp-content/plugins/elementor/assets/" // fixed by the server's URL rewriting
	}
	if env.Image == nil && r.rc.Attachment != nil {
		env.Image = func(id int64, size string) (atomic.Image, bool) {
			a := r.rc.Attachment(id)
			if a == nil {
				return atomic.Image{}, false
			}
			img := atomic.Image{URL: a.URL, Width: a.Width, Height: a.Height, Alt: a.Alt}
			if size != "" && size != "full" {
				for _, s := range a.Sizes {
					if s.Name == size {
						img.URL, img.Width, img.Height = s.URL, s.Width, s.Height
					}
				}
			}
			return img, true
		}
	}
	if env.AttachmentURL == nil && r.rc.MediaURL != nil {
		env.AttachmentURL = func(id int64) string { return r.rc.MediaURL(id, "") }
	}
	if env.SVG == nil && SVGLoader != nil {
		env.SVG = func(id int64, url string) string {
			if id > 0 {
				return SVGLoader(id)
			}
			return ""
		}
	}
	env.RenderChild = func(n *atomic.Node) string {
		if out, ok := r.loopRenderChild(n); ok { // gap-loops2: classic widgets in atomic Loop items (loop_atomic.go)
			return out
		}
		if el, ok := n.Ref.(*Element); ok && el != nil {
			return r.printElement(el)
		}
		els, err := ParseElements(append(append([]byte("["), n.Raw...), ']'))
		if err != nil || len(els) != 1 || els[0] == nil {
			return ""
		}
		return r.printElement(els[0])
	}
	// gap-atomic: display conditions of atomic elements use the Pro evaluator (widgets_pro_common.go).
	env.Visible = func(_ *atomic.Node, conds string) bool {
		disp := newOmap()
		disp.m["e_display_conditions"] = []any{conds}
		return proShouldRender(&elemCtx{r: r, disp: disp})
	}
	env.Loop = r.atomicLoopHooks() // gap-loops: the atomic Loop (loop_atomic.go)
	r.atomic = env
	return env
}

// printAtomic prints an atomic element and records its scripts.
func (r *renderer) printAtomic(el *Element) string {
	n := atomicNode(el)
	env := r.atomicEnv()
	for _, h := range atomic.Scripts([]*atomic.Node{n}, r.rc.Editing) {
		r.scripts = addUnique(r.scripts, h)
	}
	r.hasAtomic = true
	return env.Render(n)
}

// AtomicScripts reports whether a script handle belongs to the atomic elements (served from the
// editor assets folder by the server).
func AtomicScripts(handles []string) []string {
	var out []string
	for _, h := range handles {
		if atomic.ScriptFiles[h] != "" || strings.HasPrefix(h, "elementor-v2-") {
			out = append(out, h)
		}
	}
	return out
}
