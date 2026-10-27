package server

import (
	"html/template"
	"strings"

	"github.com/gin-gonic/gin"
)

// Twenty Sixteen 3.9: functions.php parts that need code. Templates live in web/themes/twentysixteen.

func init() {
	registerTheme(&Theme{
		Slug: "twentysixteen", Name: "Twenty Sixteen", Version: "3.9",
		Description: "Twenty Sixteen is a modernized take on an ever-popular WordPress layout — the horizontal masthead with an optional right sidebar that works perfectly for blogs and websites. It has custom color options with beautiful default color schemes, a harmonious fluid grid using a mobile-first approach, and impeccable polish in every detail. Twenty Sixteen will make your WordPress look beautiful everywhere.",
		Author:      "the WordPress team", AuthorURI: "https://wordpress.org/", ThemeURI: "https://wordpress.org/themes/twentysixteen/",
		Tags:       []string{"Blog", "Two Columns", "Right Sidebar", "Accessibility Ready", "Custom Colors", "Custom Menu", "Custom Logo", "Editor Style", "Featured Images", "Flexible Header", "Microformats", "Post Formats", "RTL Language Support", "Sticky Post", "Threaded Comments", "Translation Ready", "Block Styles", "Wide Blocks"},
		Screenshot: "screenshot.png",
		WidgetAreas: []WidgetArea{
			{ID: "sidebar-1", Name: "Sidebar", Description: "Add widgets here to appear in your sidebar."},
			{ID: "sidebar-2", Name: "Content Bottom 1", Description: "Appears at the bottom of the content on posts and pages."},
			{ID: "sidebar-3", Name: "Content Bottom 2", Description: "Appears at the bottom of the content on posts and pages."},
		},
		MenuLocations:     []MenuLocation{{"primary", "Primary Menu"}, {"social", "Social Links Menu"}},
		EditorStyles:      []string{"css/editor-style.css", "fonts/merriweather-plus-montserrat-plus-inconsolata.css"},
		EditorBlockStyles: []string{"css/editor-blocks.css", "genericons/genericons.css"},
		Palette:           twentySixteenPalette,
		HeadTop:           "<script>(function(html){html.className = html.className.replace(/\\bno-js\\b/,'js')})(document.documentElement);</script>\n",
		PreloadFonts:      []string{"fonts/merriweather/merriweather-latin-400-normal.woff2?ver=30", "fonts/montserrat/montserrat-latin-700-normal.woff2?ver=30"},
		Head: func(s *Server, c *gin.Context, v *siteView) string {
			u := v.ThemeURI
			return "<link rel='stylesheet' id='twentysixteen-fonts-css' href='" + u + "/fonts/merriweather-plus-montserrat-plus-inconsolata.css?ver=20230328' media='all' />\n" +
				"<link rel='stylesheet' id='genericons-css' href='" + u + "/genericons/genericons.css?ver=20251101' media='all' />\n" +
				"<link rel='stylesheet' id='twentysixteen-style-css' href='" + u + "/style.css?ver=20260819' media='all' />\n" +
				tt16ColorCSS(v) +
				"<link rel='stylesheet' id='twentysixteen-block-style-css' href='" + u + "/css/blocks.css?ver=20260105' media='all' />\n" +
				"<script src=\"/wp-includes/js/jquery/jquery.min.js?ver=3.7.1\" id=\"jquery-core-js\"></script>\n" +
				"<script src=\"/wp-includes/js/jquery/jquery-migrate.min.js?ver=3.4.1\" id=\"jquery-migrate-js\"></script>\n" +
				"<script id=\"twentysixteen-script-js-extra\">\nvar screenReaderText = {\"expand\":\"expand child menu\",\"collapse\":\"collapse child menu\"};\n</script>\n" +
				"<script src=\"" + u + "/js/functions.js?ver=20230629\" id=\"twentysixteen-script-js\" defer data-wp-strategy=\"defer\"></script>\n"
		},
		Prepare: func(s *Server, c *gin.Context, v *siteView) {
			// sidebar-content-bottom.php
			if v.Areas["sidebar-2"]+v.Areas["sidebar-3"] == "" {
				return
			}
			var sb strings.Builder
			sb.WriteString(`<aside id="content-bottom-widgets" class="content-bottom-widgets">`)
			for _, area := range []string{"sidebar-2", "sidebar-3"} {
				if v.Areas[area] != "" {
					sb.WriteString("\n\t\t<div class=\"widget-area\">\n" + string(v.Areas[area]) + "\t\t</div><!-- .widget-area -->\n")
				}
			}
			sb.WriteString("</aside><!-- .content-bottom-widgets -->")
			v.ContentBottom = template.HTML(sb.String())
		},
		BodyClasses: func(s *Server, v *siteView) []string {
			var out []string
			if multi, _ := s.multiAuthor(v.ctx); multi {
				out = append(out, "group-blog")
			}
			if v.Areas["sidebar-1"] == "" {
				out = append(out, "no-sidebar")
			}
			if v.Template != "single" && v.Template != "page" {
				out = append(out, "hfeed")
			}
			return out
		},
	})
}
