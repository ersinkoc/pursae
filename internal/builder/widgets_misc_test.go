package builder

import (
	"strings"
	"testing"
)

func TestMiscRenderWidgetEditing(t *testing.T) {
	cases := []struct {
		el   *Element
		want []string
	}{
		{&Element{ID: "abc1234", ElType: "widget", WidgetType: "alert", Settings: map[string]any{}},
			[]string{`class="elementor-alert-title elementor-inline-editing" data-elementor-setting-key="alert_title" data-elementor-inline-editing-toolbar="none"`, `elementor-alert-description elementor-inline-editing`}},
		{&Element{ID: "abc1235", ElType: "widget", WidgetType: "counter", Settings: map[string]any{}},
			[]string{`<div class="elementor-counter-title elementor-inline-editing" data-elementor-setting-key="counter-title">Cool Number</div>`, `data-delimiter=","`}},
		{&Element{ID: "abc1236", ElType: "widget", WidgetType: "progress", Settings: map[string]any{}},
			[]string{`id="elementor-progress-bar-abc1236"`, `aria-valuetext="50% (Web Designer)"`}},
		{&Element{ID: "abc1237", ElType: "widget", WidgetType: "video", Settings: map[string]any{"video_type": "vimeo"}},
			[]string{`src="https://player.vimeo.com/video/235215203?color&amp;autopause=0&amp;loop=0&amp;muted=0&amp;title=1&amp;portrait=1&amp;byline=1#t="`}},
		{&Element{ID: "abc1238", ElType: "widget", WidgetType: "menu-anchor", Settings: map[string]any{"anchor": "a b#c"}},
			[]string{`id="abc"`}},
	}
	for _, c := range cases {
		got, err := RenderWidget(c.el, &Document{ID: 1}, &RenderContext{})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: missing %q in\n%s", c.el.WidgetType, w, got)
			}
		}
	}
}

func TestProcessMoreTag(t *testing.T) {
	got := processMoreTag("<div>a <!--more Go--> b</div>", 7)
	if got != `<div>a<div id="more-7"></div>b</div>` {
		t.Errorf("got %q", got)
	}
	if got := processMoreTag("x<!--more-->", 7); got != "x<!--more-->" {
		t.Errorf("no extended: %q", got)
	}
}
