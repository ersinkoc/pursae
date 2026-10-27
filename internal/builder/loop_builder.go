package builder

// GoWebEditor Pro loop builder: the loop item list of the Loop Grid / Loop Carousel with
// alternate templates (Alternate_Templates_Trait: every Nth item, once or repeating, and
// "static" items that take a slot without consuming a post), the per-item dynamic CSS and the
// loop template CSS printed once per request (Loop_Css_Trait).

import (
	"strconv"
	"strings"
)

// loopTemplate is the template one slot renders (get_template_data_by_index).
type loopTemplate struct {
	ID        int64
	Alternate bool
	Static    bool
	RepeatID  string // the alternate template's repeater _id
}

// loopAlternates is init_alternate_template_settings: the alternate templates, last added first,
// static ones before the others; entries without a position are skipped.
type loopAlternates struct {
	list      []loopAltDef
	hasStatic bool
}

type loopAltDef struct {
	tpl      int64
	repeat   int
	once     bool
	static   bool
	repeatID string
}

func (e *elemCtx) loopAlternates() *loopAlternates {
	items := asList(e.get("alternate_templates"))
	if len(items) == 0 || e.s("template_id") == "" {
		return nil
	}
	a := &loopAlternates{}
	var statics, standard []loopAltDef
	for i := len(items) - 1; i >= 0; i-- {
		m := asMap(items[i])
		if m == nil || phpString(m["repeat_template"]) == "" {
			continue
		}
		d := loopAltDef{tpl: toInt64(m["template_id"]), repeat: toInt(m["repeat_template"]), once: phpString(m["show_once"]) == "yes",
			static: truthy(m["static_position"]), repeatID: phpString(m["_id"])}
		if d.static {
			statics = append(statics, d)
			a.hasStatic = true
		} else {
			standard = append(standard, d)
		}
	}
	a.list = append(statics, standard...)
	if len(a.list) == 0 {
		return nil
	}
	return a
}

// byIndex mirrors get_template_data_by_index.
func (e *elemCtx) loopTemplateAt(a *loopAlternates, index int) loopTemplate {
	def := loopTemplate{ID: toInt64(e.s("template_id"))}
	if a == nil {
		return def
	}
	for _, d := range a.list {
		var found bool
		if d.once {
			found = d.repeat == index+1
		} else {
			found = d.repeat != 0 && (index+1)%d.repeat == 0
		}
		if found {
			return loopTemplate{ID: d.tpl, Alternate: true, Static: d.static, RepeatID: d.repeatID}
		}
	}
	return def
}

// loopStatic is the static alternate templates' query data (init_static_alternate_template_query_data).
type loopStatic struct {
	templates []loopTemplate
	offset    map[int]int // page -> query offset
	start     map[int]int // page -> first slot index
}

// loopPosts runs a loop widget's query, adjusted for static alternate templates
// (query_posts_for_alternate_templates).
func (e *elemCtx) loopPosts(a *loopAlternates, perPage, page int) (*PostList, *loopStatic) {
	if a == nil || !a.hasStatic || perPage <= 0 {
		return e.proDRun(e.proDQuery("post_query_", perPage, page)), nil
	}
	probe := e.loopRunQuiet(e.proDQuery("post_query_", 1, 1))
	required := probe.Total
	st := &loopStatic{offset: map[int]int{}, start: map[int]int{}}
	statics := 0
	for i := 0; i < required; i++ {
		t := e.loopTemplateAt(a, i)
		st.templates = append(st.templates, t)
		p := (i+perPage-1)/perPage + 1
		st.offset[p] = i - statics
		st.start[p] = i
		if t.Static {
			statics++
			required++
		}
	}
	if statics == 0 {
		return e.proDRun(e.proDQuery("post_query_", perPage, page)), nil
	}
	q := e.proDQuery("post_query_", perPage, 1)
	if off, ok := st.offset[page]; ok {
		q.Offset = off
	} else {
		q.Offset = len(st.templates)
	}
	list := e.proDRun(q)
	list.Total = len(st.templates)
	list.Pages = (list.Total + perPage - 1) / perPage
	list.CurrentPage = page
	return list, st
}

// loopRunQuiet runs a query without adding to the avoid list (the static templates' probe).
func (e *elemCtx) loopRunQuiet(q *PostQuery) *PostList {
	st := e.r.loopState()
	saved := append([]int64(nil), st.Displayed...)
	l := e.loopRun(q, true)
	st.Displayed = saved
	return l
}

// loopItems prints the loop items of a grid or carousel (Skin_Loop_Base::render_post for every
// post, with alternate templates).
func (e *elemCtx) loopItems(list *PostList, a *loopAlternates, static *loopStatic, perPage, page int, slide bool) string {
	var b strings.Builder
	rendered := map[int64]bool{}
	slot := 0
	for i, p := range list.Posts {
		for {
			if a != nil && perPage > 0 && slot >= perPage {
				break
			}
			var t loopTemplate
			switch {
			case a == nil:
				t = e.loopTemplateAt(nil, i)
			case static != nil:
				idx := static.start[page] + slot
				if idx < len(static.templates) {
					t = static.templates[idx]
				} else {
					t = e.loopTemplateAt(nil, 0)
				}
			default:
				t = e.loopTemplateAt(a, i)
			}
			extra := ""
			if t.Alternate {
				extra = " elementor-repeater-item-" + t.RepeatID
				if e.editing() && !rendered[t.ID] {
					extra += " e-loop-alternate-template"
				}
			}
			b.WriteString(e.loopDynamicCSS(t.ID, p))
			b.WriteString(e.proDLoopItem(t.ID, p, e.loopCSSOnce(t.ID), slide, extra))
			rendered[t.ID] = true
			slot++
			if !t.Static || a == nil {
				break
			}
		}
	}
	return b.String()
}

// loopCSSOnce reports whether the template's CSS still has to be printed (once per request on
// the front end; once per widget in the editor).
func (e *elemCtx) loopCSSOnce(tplID int64) bool {
	st := e.r.loopState()
	key := strconv.FormatInt(tplID, 10)
	if e.editing() {
		key = e.el.ID + ":" + key
	}
	if st.printed == nil {
		st.printed = map[string]bool{}
	}
	if st.printed[key] {
		return false
	}
	st.printed[key] = true
	return true
}
