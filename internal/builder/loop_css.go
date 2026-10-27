package builder

// Golementor Pro loop items: the per-item dynamic CSS (Loop_Css_Trait::print_dynamic_css with
// Loop_Dynamic_CSS / Dynamic_CSS): style controls whose values come from dynamic tags render for
// each item's post, scoped to .e-loop-item-<post id>.

import (
	"strconv"
	"strings"
)

// loopRendererFor is a renderer for a loop item document with post as the current post.
func (e *elemCtx) loopRendererFor(doc *Document, post *PostData) (*renderer, *RenderContext) {
	rc := *e.r.rc
	th := ThemeData{}
	if rc.Theme != nil {
		th = *rc.Theme
	}
	th.Post = func() *PostData { return post }
	th.Archive = func() *ArchiveData { return nil }
	rc.Theme = &th
	return newRenderer(doc, &rc), &rc
}

// loopDynamicCSS returns the <style id="loop-dynamic-<template>"> block printed before an item
// ("" when the template has no dynamic style values).
func (e *elemCtx) loopDynamicCSS(tplID int64, post *PostData) string {
	pd := e.r.pro()
	if pd.Template == nil || post == nil {
		return ""
	}
	doc := pd.Template(tplID)
	if doc == nil {
		return ""
	}
	r2, rc := e.loopRendererFor(doc, post)
	g := newCSSGen(rc)
	var walk func(list []*Element)
	walk = func(list []*Element) {
		for _, el := range list {
			if el == nil {
				continue
			}
			if dyn := asMap(el.Settings["__dynamic__"]); len(dyn) > 0 {
				g.loopDynamicStyles(r2, el, dyn, post.ID)
			}
			walk(el.Elements)
		}
	}
	walk(doc.Elements)
	css := sanitizeInlineCSS(g.sheet.String())
	if css == "" {
		return ""
	}
	id := strconv.FormatInt(post.ID, 10)
	css = strings.ReplaceAll(css, ".elementor-"+id+" ", ".e-loop-item-"+id+" ")
	return `<style id="loop-dynamic-` + strconv.FormatInt(tplID, 10) + `">` + css + "</style>"
}

// loopDynamicStyles mirrors Dynamic_CSS::add_controls_stack_style_rules for one element: only the
// dynamic controls, with the tags' values for the current post.
func (g *cssGen) loopDynamicStyles(r *renderer, el *Element, dyn map[string]any, postID int64) {
	_, st := elementStack(el, g.bps)
	if st == nil {
		return
	}
	settings := initSettings(rawSettings(el), st.all)
	values := make(map[string]any, len(settings.m))
	for k, v := range settings.m {
		values[k] = v
	}
	r.resolveDynamicMap(values, dyn, st.byName)
	wrapper := ".elementor-" + strconv.FormatInt(postID, 10) + " .elementor-element.elementor-element-" + el.ID
	for _, c := range st.all {
		if _, ok := dyn[c.Name]; !ok || len(c.Selectors) == 0 {
			continue
		}
		g.addControlRules(c, st.byName, func(x *ctrl) any { return g.styleControlValue(x, values) },
			[]string{"{{ID}}", "{{WRAPPER}}"}, []string{el.ID, wrapper}, values)
	}
}
