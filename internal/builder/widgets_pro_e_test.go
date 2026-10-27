package builder

import (
	"strings"
	"testing"
)

// GoWebEditor Pro widgets of widgets_pro_e.go: the lab menu ("Lab Menu", id 21, made with WP-CLI in
// the lab) for the nav-menu fixtures, and the lab's login URL.

func init() {
	ProELoginPath = "/wp-login.php"
	prev := proTestRC
	proTestRC = func(rc *RenderContext) {
		if prev != nil {
			prev(rc)
		}
		rc.Theme.Menu = func(loc string, id int64) []*MenuItem {
			if id != 21 && loc != "primary" {
				return nil
			}
			return []*MenuItem{
				{ID: 207, Label: "Home", URL: "http://localhost:8095/", Kind: "custom", Object: "custom"},
				{ID: 212, Label: "Sample Page", URL: "http://localhost:8095/sample-page/", Kind: "post_type", Object: "page",
					Children: []*MenuItem{{ID: 216, Label: "Team", URL: "http://localhost:8095/team/", Kind: "custom", Object: "custom"}}},
				{ID: 217, Label: "Anchor", URL: "#contact", Kind: "custom", Object: "custom"},
			}
		}
	}
}

func TestProEWidgets(t *testing.T) {
	menu := func(loc string, id int64) []*MenuItem {
		if loc == "footer" || id == 7 {
			return []*MenuItem{{ID: 1, Label: "Home", URL: "http://x.test/", Kind: "custom", Object: "custom", Current: true}}
		}
		return nil
	}
	rc := func() *RenderContext {
		return &RenderContext{HomeURL: "http://x.test", Theme: &ThemeData{HomeURL: "http://x.test/", Menu: menu},
			Pro: &ProData{User: &UserInfo{ID: 3, DisplayName: "Ada"}, CurrentURL: "http://x.test/page/?a=1"}}
	}
	render := func(widget string, settings map[string]any) string {
		el := &Element{ID: "w1", ElType: "widget", WidgetType: widget, Settings: settings}
		res, err := Render(&Document{ID: 5, Elements: []*Element{el}}, rc())
		if err != nil {
			t.Fatal(err)
		}
		return res.HTML
	}
	// Site widget settings keep working: the menu location, and the menu id under the Pro "menu" setting.
	if out := render("nav-menu", map[string]any{"menu_source": "location", "menu_location": "footer"}); !strings.Contains(out, `menu-item-home menu-item-1`) ||
		!strings.Contains(out, `aria-current="page" class="elementor-item elementor-item-active"`) {
		t.Errorf("location menu: %s", out)
	}
	if out := render("nav-menu", map[string]any{"menu": "7"}); !strings.Contains(out, `id="menu-1-w1"`) {
		t.Errorf("menu by id: %s", out)
	}
	if out := render("nav-menu", map[string]any{"menu": "main-menu"}); strings.Contains(out, "elementor-widget-nav-menu") {
		t.Errorf("slug menu rendered: %s", out)
	}
	// Logged-in visitors get the message instead of the form.
	if out := render("login", map[string]any{}); !strings.Contains(out, `You are Logged in as Ada (<a href="http://x.test/wp-login.php?action=logout&#038;redirect_to=%2Fpage%2F%3Fa%3D1" target="_blank">Logout</a>)`) {
		t.Errorf("logged in: %s", out)
	}
	// reCAPTCHA with a site key.
	ProERecaptchaSiteKey = func(v3 bool) string { return "k3y" }
	defer func() { ProERecaptchaSiteKey = nil }()
	out := render("form", map[string]any{"form_fields": []any{map[string]any{"_id": "r", "custom_id": "cap", "field_type": "recaptcha"}}})
	if !strings.Contains(out, `<div class="elementor-g-recaptcha" data-sitekey="k3y" data-type="v2_checkbox" data-theme="light" data-size="normal"></div>`) {
		t.Errorf("recaptcha: %s", out)
	}
}
