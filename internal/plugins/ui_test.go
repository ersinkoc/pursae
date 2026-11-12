package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUIRegistrationValidation(t *testing.T) {
	p := &Plugin{manifest: &Manifest{Slug: "demo", Name: "Demo"}}
	ctx := context.Background()
	reg := func(fn func(context.Context, *Plugin, []byte) (any, error), v any) error {
		raw, _ := json.Marshal(v)
		_, err := fn(ctx, p, raw)
		return err
	}
	bad := []struct {
		name string
		fn   func(context.Context, *Plugin, []byte) (any, error)
		v    any
	}{
		{"page bad parent", hostRegisterAdminPage, AdminPage{Parent: "nowhere", Callback: "cb"}},
		{"page self main", hostRegisterAdminPage, AdminPage{Parent: "self", Callback: "cb"}},
		{"page bad icon", hostRegisterAdminPage, AdminPage{Icon: "javascript:x", Callback: "cb"}},
		{"page bad cap", hostRegisterAdminPage, AdminPage{Capability: "Manage Options", Callback: "cb"}},
		{"page no callback", hostRegisterAdminPage, AdminPage{}},
		{"widget bad id", hostRegisterDashboardWidget, DashboardWidget{ID: "A B", Title: "x", Callback: "cb"}},
		{"box empty", hostRegisterMetaBox, MetaBox{ID: "b", Title: "B"}},
		{"box bad context", hostRegisterMetaBox, MetaBox{ID: "b", Title: "B", Context: "top", Callback: "cb"}},
		{"box bad field type", hostRegisterMetaBox, MetaBox{ID: "b", Title: "B", Fields: []SettingsField{{ID: "f", Type: "file"}}}},
		{"radio without options", hostRegisterMetaBox, MetaBox{ID: "b", Title: "B", Fields: []SettingsField{{ID: "f", Type: "radio"}}}},
		{"block of another plugin", hostRegisterBlock, BlockType{Name: "other/x", Title: "X", Callback: "cb"}},
		{"block bad category", hostRegisterBlock, BlockType{Name: "x", Title: "X", Category: "nope", Callback: "cb"}},
		{"widget underscore", hostRegisterBuilderWidget, BuilderWidget{ID: "a_b", Title: "X", Callback: "cb"}},
	}
	for _, c := range bad {
		if err := reg(c.fn, c.v); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	if err := reg(hostRegisterAdminPage, AdminPage{Callback: "cb"}); err != nil {
		t.Fatal(err)
	}
	if err := reg(hostRegisterAdminPage, AdminPage{Slug: "sub", Parent: "self", Callback: "cb2"}); err != nil {
		t.Fatal(err)
	}
	if err := reg(hostRegisterAdminPage, AdminPage{Slug: "sub", Parent: "tools", Callback: "cb3"}); err != nil { // re-register replaces
		t.Fatal(err)
	}
	if pages := p.AdminPages(); len(pages) != 2 || pages[0].MenuTitle != "Demo" || pages[0].Capability != "manage_options" || pages[1].Parent != "tools" {
		t.Fatalf("pages = %+v", pages)
	}
	if err := reg(hostRegisterBlock, BlockType{Name: "card", Title: "Card", Callback: "cb"}); err != nil {
		t.Fatal(err)
	}
	if b := p.BlockTypes(); len(b) != 1 || b[0].Name != "demo/card" || b[0].Category != "widgets" {
		t.Fatalf("blocks = %+v", b)
	}
	if err := reg(hostRegisterMetaBox, MetaBox{ID: "b", Title: "B", Fields: []SettingsField{{ID: "c", Type: "color"}}}); err != nil {
		t.Fatal(err)
	}
	if m := p.MetaBoxes(); len(m) != 1 || m[0].Context != "normal" || !m[0].ForType("page") || m[0].ForType("product") {
		t.Fatalf("boxes = %+v", m)
	}
}

func TestFieldDefaults(t *testing.T) {
	fields := []SettingsField{{ID: "a", Default: "x"}, {ID: "b"}}
	got := FieldDefaults(fields, map[string]any{"b": 2.0, "evil": "y", "className": "c"}, "className")
	if got["a"] != "x" || got["b"] != 2.0 || got["className"] != "c" || got["evil"] != nil {
		t.Fatalf("FieldDefaults = %v", got)
	}
}

func TestUIDemoPlugin(t *testing.T) {
	rt := newTestRuntime(t, Config{})
	host := NewMemoryHost()
	p := loadPlugin(t, rt, "examples/plugins/ui-demo", host)
	defer dumpLogs(t, p)
	ctx := context.Background()

	a, ok := rt.FindAdminPage("ui-demo")
	if !ok || a.Page.Parent != "tools" || a.Page.MenuTitle != "Gopher Board" {
		t.Fatalf("admin page = %+v, %v", a, ok)
	}
	resp, err := p.RenderAdminPage(ctx, "", AdminRequest{Method: "POST", Form: map[string]string{"message": "Hi <script>alert(1)</script>"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Notices) != 1 || resp.Notices[0].Type != "success" || !strings.Contains(resp.HTML, `<input name="message"`) || strings.Contains(resp.HTML, "<script") {
		t.Fatalf("admin page = %+v", resp)
	}
	out, err := p.RenderDashboardWidget(ctx, "board", nil)
	if err != nil || !strings.Contains(out, "Hi &lt;script&gt;") {
		t.Fatalf("dashboard widget = %q, %v", out, err)
	}
	if boxes := rt.MetaBoxes("post"); len(boxes) != 1 || boxes[0].Box.Context != "side" || len(boxes[0].Box.Fields) != 3 {
		t.Fatalf("meta boxes = %+v", boxes)
	}
	if out, err := p.RenderMetaBox(ctx, "notes", 7, nil, nil); err != nil || !strings.Contains(out, "post meta") {
		t.Fatalf("meta box = %q, %v", out, err)
	}
	html, ok := rt.RenderBlock(ctx, "ui-demo/badge", map[string]any{"text": "Go", "onclick": "x"}, "")
	if !ok || html != `<span class="ui-demo-badge ui-demo-badge--solid">Go</span>` {
		t.Fatalf("block = %q, %v", html, ok)
	}
	if _, ok := rt.RenderBlock(ctx, "ui-demo/missing", nil, ""); ok {
		t.Fatal("unknown block rendered")
	}
	html, ok = rt.RenderBuilderWidget(ctx, "ui-demo/badge", map[string]any{"style": "outline", "loud": true})
	if !ok || !strings.Contains(html, "ui-demo-badge--outline") || !strings.Contains(html, "<strong>") {
		t.Fatalf("widget = %q, %v", html, ok)
	}

	// Without the permissions nothing is registered.
	rt2 := newTestRuntime(t, Config{})
	p2 := loadPlugin(t, rt2, "examples/plugins/ui-demo", NewMemoryHost(), PermStorage)
	if len(p2.AdminPages())+len(p2.BlockTypes())+len(p2.BuilderWidgets())+len(p2.MetaBoxes())+len(p2.DashboardWidgets()) != 0 {
		t.Fatal("UI registered without permissions")
	}
}
