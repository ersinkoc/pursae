package server

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

// Ready-made homepage designs: Golementor's own page templates, listed in the template library's
// Pages tab (source "remote", the tab upstream fills from its cloud) and inserted like any template.
// A new page plus a design plus "Set as homepage" in the Pages panel is a finished front page.
// The designs use the core layout widgets only (container, heading, text editor, button, icon box,
// divider), so they work with every theme and inherit the kit's global colors and fonts.

type gweDesign struct {
	ID, Title string
	Build     func() []any
}

var gweDesigns = []gweDesign{
	{"gwe-home-business", "Homepage – Business", gweDesignBusiness},
	{"gwe-home-personal", "Homepage – Personal blog", gweDesignPersonal},
	{"gwe-home-portfolio", "Homepage – Portfolio", gweDesignPortfolio},
	{"gwe-home-product", "Homepage – Product launch", gweDesignProduct},
}

func gweDesignByID(id string) *gweDesign {
	for i := range gweDesigns {
		if gweDesigns[i].ID == id {
			return &gweDesigns[i]
		}
	}
	return nil
}

// gweDesignItems are the library entries of the designs.
func (s *Server) gweDesignItems(c *gin.Context) []map[string]any {
	lb := s.links(c)
	out := make([]map[string]any, 0, len(gweDesigns))
	for i, d := range gweDesigns {
		out = append(out, map[string]any{
			"template_id": d.ID, "source": "remote", "type": "page", "subtype": "page", "title": d.Title,
			"thumbnail": lb.HomeURL(gweAssetsPath + "images/designs/" + strings.TrimPrefix(d.ID, "gwe-") + ".svg"),
			"date":      1793000000 - i, "human_date": "", "author": "Pursae Web Builder", "tags": []any{"Homepage"},
			"isPro": false, "accessLevel": 0, "accessTier": "free", "popularityIndex": 100 - i, "trendIndex": 100 - i,
			"hasPageSettings": false, "url": "", "favorite": false,
		})
	}
	return out
}

// gweDesignsScript lets the designs insert without an upstream cloud account: the library treats
// "remote" listings as cloud templates that need Connect, and these are served by this site.
const gweDesignsScript = `<script>
(function () {
	function allow() {
		elementor.hooks.addFilter('templates/source/is-remote', function (isRemote, source) {
			return source === 'remote' ? false : isRemote;
		});
	}
	if (window.elementor && elementor.hooks) { allow(); } else { jQuery(window).on('elementor:init', allow); }
})();
</script>
`

// --- a tiny builder for upstream's element JSON -----------------------------------------------

var gweDesignSeq int

func gweElID() string {
	gweDesignSeq++
	return fmt.Sprintf("%07x", 0x6e00000+gweDesignSeq)
}

func px(n int) map[string]any { return map[string]any{"unit": "px", "size": n, "sizes": []any{}} }

func box(top, side int) map[string]any {
	return map[string]any{"unit": "px", "top": fmt.Sprint(top), "right": fmt.Sprint(side), "bottom": fmt.Sprint(top), "left": fmt.Sprint(side), "isLinked": false}
}

// section is a full-width container holding a boxed column of children.
func section(bg string, padTop int, align string, kids ...any) map[string]any {
	st := map[string]any{"content_width": "boxed", "flex_direction": "column", "flex_align_items": align, "padding": box(padTop, 24),
		"flex_gap": map[string]any{"unit": "px", "size": 20, "column": "20", "row": "20"}}
	if bg != "" {
		st["background_background"], st["background_color"] = "classic", bg
	}
	return map[string]any{"id": gweElID(), "elType": "container", "isInner": false, "settings": st, "elements": kids}
}

// row lays children out side by side (wrapping on phones), each a column of width percent.
func row(kids ...any) map[string]any {
	return map[string]any{"id": gweElID(), "elType": "container", "isInner": true, "elements": kids, "settings": map[string]any{
		"content_width": "full", "flex_direction": "row", "flex_direction_mobile": "column", "flex_wrap": "wrap",
		"flex_gap": map[string]any{"unit": "px", "size": 24, "column": "24", "row": "24"}, "padding": box(0, 0)}}
}

func col(width int, bg string, kids ...any) map[string]any {
	st := map[string]any{"content_width": "full", "flex_direction": "column", "width": map[string]any{"unit": "%", "size": width},
		"width_mobile": map[string]any{"unit": "%", "size": 100}, "flex_gap": map[string]any{"unit": "px", "size": 12, "column": "12", "row": "12"}}
	if bg != "" {
		st["background_background"], st["background_color"] = "classic", bg
		st["padding"] = box(28, 28)
		st["border_border"], st["border_width"], st["border_color"] = "solid", map[string]any{"unit": "px", "top": "1", "right": "1", "bottom": "1", "left": "1", "isLinked": true}, "#E5E7EB"
	}
	return map[string]any{"id": gweElID(), "elType": "container", "isInner": true, "settings": st, "elements": kids}
}

func widget(kind string, settings map[string]any) map[string]any {
	return map[string]any{"id": gweElID(), "elType": "widget", "widgetType": kind, "settings": settings, "elements": []any{}}
}

func heading(text, tag string, size int, color, align string) map[string]any {
	st := map[string]any{"title": text, "header_size": tag, "align": align, "typography_typography": "custom",
		"typography_font_size": px(size), "typography_font_weight": "700", "typography_line_height": map[string]any{"unit": "em", "size": 1.15, "sizes": []any{}}}
	if size > 36 {
		st["typography_font_size_mobile"] = px(size * 6 / 10)
	}
	if color != "" {
		st["title_color"] = color
	}
	return widget("heading", st)
}

func text(html, color, align string, size int) map[string]any {
	st := map[string]any{"editor": html, "align": align, "typography_typography": "custom", "typography_font_size": px(size),
		"typography_line_height": map[string]any{"unit": "em", "size": 1.6, "sizes": []any{}}}
	if color != "" {
		st["text_color"] = color
	}
	return widget("text-editor", st)
}

func button(label, bg, fg, align string) map[string]any {
	return widget("button", map[string]any{"text": label, "link": map[string]any{"url": "#", "is_external": "", "nofollow": ""},
		"align": align, "background_color": bg, "button_text_color": fg, "border_radius": map[string]any{"unit": "px", "top": "4", "right": "4", "bottom": "4", "left": "4", "isLinked": true},
		"text_padding": map[string]any{"unit": "px", "top": "14", "right": "26", "bottom": "14", "left": "26", "isLinked": false}})
}

func iconBox(icon, title, desc, accent string) map[string]any {
	return widget("icon-box", map[string]any{"selected_icon": map[string]any{"value": icon, "library": "fa-solid"}, "title_text": title,
		"description_text": desc, "position": "top", "text_align": "left", "primary_color": accent, "title_size": "h3",
		"icon_size": px(28), "title_typography_typography": "custom", "title_typography_font_size": px(20), "title_typography_font_weight": "600"})
}

func divider(color string) map[string]any {
	return widget("divider", map[string]any{"color": color, "weight": px(1), "width": map[string]any{"unit": "%", "size": 100, "sizes": []any{}}})
}

// --- the designs -----------------------------------------------------------------------------

func gweDesignBusiness() []any {
	const ink, accent, light = "#111827", "#2563EB", "#F3F4F6"
	return []any{
		section(ink, 120, "flex-start",
			text(`<p>Trusted by 2,000+ teams</p>`, "#93C5FD", "left", 15),
			heading("We help growing companies do their best work.", "h1", 56, "#FFFFFF", "left"),
			text(`<p>Strategy, design and engineering under one roof. One team from the first workshop to launch day.</p>`, "#D1D5DB", "left", 19),
			row(button("Book a call", accent, "#FFFFFF", "left"), button("See our work", "#1F2937", "#FFFFFF", "left")),
		),
		section("", 96, "stretch",
			heading("What we do", "h2", 36, ink, "left"),
			row(
				col(31, light, iconBox("fas fa-chess", "Strategy", "Research, positioning and a plan the whole company can follow.", accent)),
				col(31, light, iconBox("fas fa-pen-nib", "Design", "Brands, websites and products people enjoy using.", accent)),
				col(31, light, iconBox("fas fa-code", "Engineering", "Fast, accessible software built to last and easy to change.", accent)),
			),
		),
		section(light, 96, "stretch",
			row(
				col(48, "", heading("“They felt like part of our team from day one.”", "h3", 28, ink, "left"),
					text(`<p>— Dana Kim, COO at Northwind</p>`, "#6B7280", "left", 16)),
				col(48, "", heading("98%", "h2", 64, accent, "left"), text(`<p>of clients come back for a second project.</p>`, "#374151", "left", 17)),
			),
		),
		section(accent, 88, "center",
			heading("Ready to start?", "h2", 40, "#FFFFFF", "center"),
			text(`<p>Tell us about your project. We reply within one business day.</p>`, "#DBEAFE", "center", 18),
			button("Get in touch", "#FFFFFF", accent, "center"),
		),
	}
}

func gweDesignPersonal() []any {
	const ink, muted, line = "#1F2937", "#6B7280", "#E5E7EB"
	post := func(date, title, blurb string) map[string]any {
		return col(100, "", text(`<p>`+date+`</p>`, muted, "left", 14), heading(title, "h3", 26, ink, "left"),
			text(`<p>`+blurb+`</p>`, "#4B5563", "left", 17), divider(line))
	}
	return []any{
		section("", 120, "center",
			heading("Hi, I’m Alex.", "h1", 60, ink, "center"),
			text(`<p>I write about building small software, slow travel and the books that changed my mind.</p>`, muted, "center", 20),
			row(button("Read the blog", ink, "#FFFFFF", "center"), button("About me", "#F3F4F6", ink, "center")),
		),
		section("", 72, "stretch",
			heading("Latest writing", "h2", 30, ink, "left"),
			divider(line),
			post("October 2026", "Why I rewrote my blog in Go", "A weekend project that turned into a lesson about doing less."),
			post("September 2026", "Three weeks by train across Anatolia", "Notes, maps and the best breakfast I have ever had."),
			post("August 2026", "Books of the summer", "Five books, one sentence each, and why you should read the third."),
		),
		section("#F9FAFB", 80, "center",
			heading("Get new posts by e-mail", "h2", 30, ink, "center"),
			text(`<p>One e-mail when something new is up. No spam, unsubscribe any time.</p>`, muted, "center", 17),
			button("Subscribe", ink, "#FFFFFF", "center"),
		),
	}
}

func gweDesignPortfolio() []any {
	const ink, accent = "#0F0F0F", "#F97316"
	project := func(name, kind string) map[string]any {
		return col(48, "#F5F5F4", heading(name, "h3", 26, ink, "left"), text(`<p>`+kind+`</p>`, "#57534E", "left", 16),
			button("View case study", ink, "#FFFFFF", "left"))
	}
	return []any{
		section("", 128, "flex-start",
			text(`<p>Product designer · Istanbul</p>`, accent, "left", 16),
			heading("I design calm, useful products for ambitious teams.", "h1", 64, ink, "left"),
			text(`<p>Ten years of interfaces, design systems and the occasional brand. Currently open to new projects.</p>`, "#57534E", "left", 20),
		),
		section("", 64, "stretch",
			heading("Selected work", "h2", 32, ink, "left"),
			row(project("Atlas banking app", "Mobile app · 2026"), project("Kite design system", "Design system · 2025")),
			row(project("Harbor booking", "Web platform · 2025"), project("Orbit brand refresh", "Brand identity · 2024")),
		),
		section(ink, 96, "flex-start",
			heading("Have a project in mind?", "h2", 44, "#FFFFFF", "left"),
			text(`<p>hello@example.com</p>`, "#D6D3D1", "left", 22),
			button("Start a conversation", accent, "#FFFFFF", "left"),
		),
	}
}

func gweDesignProduct() []any {
	const ink, accent, soft = "#0B1220", "#7C3AED", "#F5F3FF"
	plan := func(name, price, note string, featured bool) map[string]any {
		bg, btn, fg := "#FFFFFF", soft, accent
		if featured {
			bg, btn, fg = soft, accent, "#FFFFFF"
		}
		return col(31, bg, heading(name, "h3", 20, ink, "left"), heading(price, "h2", 40, ink, "left"),
			text(`<p>`+note+`</p>`, "#6B7280", "left", 15), button("Choose "+name, btn, fg, "left"))
	}
	return []any{
		section(soft, 120, "center",
			text(`<p>New · Version 2.0 is here</p>`, accent, "center", 15),
			heading("The simplest way to run your team’s week.", "h1", 58, ink, "center"),
			text(`<p>Plans, notes and check-ins in one quiet place. Set up in two minutes, free for small teams.</p>`, "#4B5563", "center", 20),
			row(button("Start free", accent, "#FFFFFF", "center"), button("Watch the demo", "#FFFFFF", ink, "center")),
		),
		section("", 96, "stretch",
			heading("Everything in one place", "h2", 36, ink, "center"),
			row(
				col(48, "", iconBox("fas fa-calendar-check", "Weekly plans", "Agree on priorities every Monday in five minutes.", accent)),
				col(48, "", iconBox("fas fa-comments", "Async check-ins", "Fewer meetings: updates arrive when people are ready.", accent)),
			),
			row(
				col(48, "", iconBox("fas fa-chart-line", "Clear progress", "See what moved this week without asking around.", accent)),
				col(48, "", iconBox("fas fa-lock", "Private by default", "Your data stays yours, encrypted and exportable.", accent)),
			),
		),
		section("#FAFAFA", 96, "stretch",
			heading("Simple pricing", "h2", 36, ink, "center"),
			row(plan("Starter", "Free", "Up to 5 people.", false), plan("Team", "$8", "Per person, per month.", true), plan("Business", "$16", "SSO, audit log and priority support.", false)),
		),
		section(ink, 88, "center",
			heading("Try it with your team this week.", "h2", 40, "#FFFFFF", "center"),
			button("Start free", accent, "#FFFFFF", "center"),
		),
	}
}
