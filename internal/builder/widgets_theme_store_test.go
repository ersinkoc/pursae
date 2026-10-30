package builder

import (
	"strings"
	"testing"
)

func TestStoreWidgetsRegistered(t *testing.T) {
	want := map[string]bool{"woocommerce-products": false, "wc-archive-products": false, "wc-categories": false, "woocommerce-breadcrumb": false,
		"woocommerce-menu-cart": false, "woocommerce-cart": false, "woocommerce-checkout-page": false, "woocommerce-my-account": false,
		"woocommerce-product-title": false, "woocommerce-product-images": false, "woocommerce-product-price": false,
		"woocommerce-product-add-to-cart": false, "woocommerce-product-data-tabs": false, "woocommerce-product-related": false,
		"golylang-language-switcher": false}
	for _, w := range ThemeWidgets() {
		if _, ok := want[w.Name]; ok {
			want[w.Name] = true
			if len(w.Controls) == 0 && w.Name != "woocommerce-notices" {
				t.Errorf("%s has no controls", w.Name)
			}
		}
	}
	for name, ok := range want {
		if !ok || !WidgetKnown(name) {
			t.Errorf("%s is not registered", name)
		}
	}
}

func TestStoreWidgetsRender(t *testing.T) {
	el := func(typ string) []*Element {
		return []*Element{{ID: "c1", ElType: "container", Settings: map[string]any{}, Elements: []*Element{
			{ID: "w1", ElType: "widget", WidgetType: typ, Settings: map[string]any{"columns": "3"}}}}}
	}
	// No store: a placeholder in the editor, nothing on the page.
	res, err := Render(&Document{ID: 9, Type: "wp-page", Elements: el("woocommerce-products")}, &RenderContext{Editing: true, Theme: &ThemeData{}})
	if err != nil || !strings.Contains(res.HTML, "elementor-theme-placeholder") {
		t.Fatalf("editor placeholder: %v %s", err, res.HTML)
	}
	// The site's markup, with the widget's settings.
	var got map[string]any
	td := &ThemeData{Store: func(name string, st map[string]any, editing bool) string {
		got = st
		return `<div class="woocommerce columns-3">` + name + `</div>`
	}}
	res, err = Render(&Document{ID: 9, Type: "wp-page", Elements: el("woocommerce-products")}, &RenderContext{Theme: td})
	if err != nil || !strings.Contains(res.HTML, `<div class="woocommerce columns-3">woocommerce-products</div>`) ||
		!strings.Contains(res.HTML, "elementor-widget-woocommerce-products") {
		t.Fatalf("store markup: %v %s", err, res.HTML)
	}
	if got["columns"] != "3" {
		t.Errorf("settings passed to the site: %v", got)
	}
	// The product title prints a heading.
	td.Store = func(name string, st map[string]any, editing bool) string { return "Linen Shirt" }
	res, _ = Render(&Document{ID: 9, Type: "single-product", Elements: el("woocommerce-product-title")}, &RenderContext{Theme: td})
	if !strings.Contains(res.HTML, `<h1 class="elementor-heading-title elementor-size-default">Linen Shirt</h1>`) {
		t.Errorf("product title: %s", res.HTML)
	}
}
