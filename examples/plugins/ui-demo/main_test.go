package main

import (
	"strings"
	"testing"

	"wpgo/sdk/go/wpgo"
	"wpgo/sdk/go/wpgo/wpgotest"
)

func TestBoardAndBadge(t *testing.T) {
	h := wpgotest.New(t)
	wpgo.Init()
	resp, err := h.RenderAdminPage("", wpgo.AdminRequest{Method: "POST", Form: map[string]string{"message": "Hello <b>"}})
	if err != nil || len(resp.Notices) != 1 || resp.Notices[0].Type != "success" || !strings.Contains(resp.HTML, "Hello &lt;b&gt;") {
		t.Fatalf("board = %+v, %v", resp, err)
	}
	if out, _ := h.RenderDashboardWidget("board", wpgo.AdminRequest{}); !strings.Contains(out, "Hello &lt;b&gt;") {
		t.Fatalf("widget = %q", out)
	}
	if out, _ := h.RenderBlock("badge", map[string]any{"text": "Go", "style": "outline", "loud": true}, ""); out != `<strong><span class="ui-demo-badge ui-demo-badge--outline">Go</span></strong>` {
		t.Fatalf("badge = %q", out)
	}
	if len(h.Registered("register_meta_box")) != 1 || len(h.Registered("register_builder_widget")) != 1 {
		t.Fatal("meta box or widget not registered")
	}
}
