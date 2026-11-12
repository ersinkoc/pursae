package wpgotest_test

import (
	"strings"
	"testing"

	"wpgo/sdk/go/wpgo"
	"wpgo/sdk/go/wpgo/wpgotest"
)

func TestUIRegistrations(t *testing.T) {
	h := wpgotest.New(t)
	wpgo.RegisterAdminPage(wpgo.AdminPage{Slug: "reports", MenuTitle: "Reports", Parent: "tools",
		Render: func(_ wpgo.Context, req wpgo.AdminRequest) wpgo.AdminResponse {
			resp := wpgo.AdminResponse{HTML: "<p>" + req.Method + " " + req.Page + "</p>"}
			if req.Method == "POST" {
				resp.Notices = []wpgo.PageNotice{{Type: "success", Message: "Saved " + req.Form["n"]}}
			}
			return resp
		}})
	wpgo.RegisterDashboardWidget("stats", "Stats", "", func(wpgo.Context, wpgo.AdminRequest) string { return "<b>42</b>" })
	saved := int64(0)
	wpgo.RegisterMetaBox(wpgo.MetaBox{ID: "extra", Title: "Extra", Fields: []wpgo.SettingsField{{ID: "sub", Label: "Subtitle", Type: "text"}},
		Render: func(_ wpgo.Context, req wpgo.AdminRequest) string { return "post " + itoa(req.PostID) },
		OnSave: func(_ wpgo.Context, id int64, v map[string]any) { saved = id }})
	wpgo.RegisterBlock(wpgo.Block{Name: "hello", Title: "Hello", Attributes: []wpgo.SettingsField{{ID: "who", Type: "text", Default: "world"}},
		Render: func(_ wpgo.Context, a map[string]any, content string) string {
			return "Hello " + a["who"].(string) + content
		}})
	wpgo.RegisterBuilderWidget(wpgo.BuilderWidget{ID: "badge", Title: "Badge",
		Render: func(_ wpgo.Context, a map[string]any, _ string) string { return "<span>badge</span>" }})
	wpgo.Init()

	if got := h.Registered("register_admin_page"); len(got) != 1 || got[0]["parent"] != "tools" {
		t.Fatalf("admin pages = %v", got)
	}
	resp, err := h.RenderAdminPage("reports", wpgo.AdminRequest{Method: "POST", Form: map[string]string{"n": "7"}})
	if err != nil || resp.HTML != "<p>POST reports</p>" || len(resp.Notices) != 1 || resp.Notices[0].Message != "Saved 7" {
		t.Fatalf("admin page = %+v, %v", resp, err)
	}
	if out, err := h.RenderDashboardWidget("stats", wpgo.AdminRequest{}); err != nil || out != "<b>42</b>" {
		t.Fatalf("widget = %q, %v", out, err)
	}
	if out, err := h.RenderMetaBox("extra", wpgo.AdminRequest{PostID: 9}); err != nil || out != "post 9" {
		t.Fatalf("meta box = %q, %v", out, err)
	}
	if err := h.SaveMetaBox("extra", 9, map[string]any{"sub": "x"}); err != nil || saved != 9 {
		t.Fatalf("save = %d, %v", saved, err)
	}
	if out, err := h.RenderBlock("hello", map[string]any{"who": "you"}, "!"); err != nil || out != "Hello you!" {
		t.Fatalf("block = %q, %v", out, err)
	}
	if out, err := h.RenderBuilderWidget("badge", nil); err != nil || !strings.Contains(out, "badge") {
		t.Fatalf("builder widget = %q, %v", out, err)
	}
	if _, err := h.RenderAdminPage("nope", wpgo.AdminRequest{}); err == nil {
		t.Fatal("unknown page rendered")
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
