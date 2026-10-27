package builder

// WordPress widgets (wp-widget-<id_base>): upstream runs WP_Widget::widget() with
// before_widget/after_widget empty and <h5> titles. WordPress widgets are not part of this
// package; the server can plug them in with WPWidgetRenderer.

import "strings"

// WPWidgetRenderer renders a classic WordPress widget (idBase without the "wp-widget-" prefix,
// instance = the element's "wp" setting, args = before_widget/after_widget/before_title/
// after_title/widget_id). When nil, WordPress widgets render as an HTML comment.
var WPWidgetRenderer func(idBase string, instance map[string]any, args map[string]string) string

func init() {
	for name, w := range loadSchema().widgets {
		if strings.HasPrefix(name, "wp-widget-") && w != nil {
			registerWidget(name, &widgetDef{render: renderWPWidget, styles: static("e-swiper"), scripts: []string{"swiper"}})
		}
	}
}

func renderWPWidget(w *elemCtx) string {
	if WPWidgetRenderer == nil {
		return unsupportedComment(w.el)
	}
	args := map[string]string{
		"widget_id":     w.el.WidgetType,
		"before_widget": "",
		"after_widget":  "",
		"before_title":  "<h5>",
		"after_title":   "</h5>",
	}
	inst := asMap(w.raw("wp"))
	if inst == nil {
		inst = map[string]any{}
	}
	return WPWidgetRenderer(strings.TrimPrefix(w.el.WidgetType, "wp-widget-"), inst, args)
}
