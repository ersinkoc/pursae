package builder

import "strings"

// Store widgets (Goocommerce): the editor's "Store" and "Store Single" panel categories, with the
// widget names of upstream's WooCommerce widgets so imported designs keep working. The markup is the
// storefront's own (products loop, single product parts, cart, checkout, my account): each widget
// asks the site for it through ThemeData.Store, so the renderer stays free of store code. While no
// store is active, or no product is being shown, the editor gets a placeholder and the page nothing.

// storeWidget is the site callback's answer for one widget ("" = nothing to show).
func (w *elemCtx) storeHTML(placeholder string) string {
	t := w.theme()
	if t == nil || t.Store == nil {
		return w.placeholder("Activate Goocommerce to show " + placeholder + ".")
	}
	out := t.Store(w.name, w.disp.m, w.editing())
	if strings.TrimSpace(out) == "" {
		return w.placeholder(placeholder + " will appear here.")
	}
	return out + "\n"
}

func storeRender(placeholder string) func(*elemCtx) string {
	return func(w *elemCtx) string { return w.storeHTML(placeholder) }
}

// storeButtons adds the colors, typography and radius of the store's buttons under selector.
func (l *ctlList) storeButtons(section, label, selector string) {
	l.start(section, label, "style")
	l.color(section+"_text_color", "Text Color", selector, "color", "")
	l.color(section+"_bg_color", "Background Color", selector, "background-color", "accent")
	l.color(section+"_hover_bg_color", "Hover Background", selector+":hover", "background-color", "")
	l.typography(section+"_typography", selector, "accent")
	l.slider(section+"_radius", "Border Radius", selector, "border-radius: {{SIZE}}{{UNIT}};", []string{"px", "em"},
		map[string]any{"px": map[string]any{"max": 100}}, nil)
}

// storeText adds a text color and typography pair.
func (l *ctlList) storeText(prefix, label, selector, colorGlobal, typoGlobal string) {
	l.color(prefix+"_color", label+" Color", selector, "color", colorGlobal)
	l.typography(prefix+"_typography", selector, typoGlobal)
}

var storeColumns = Ordered{{"1", "1"}, {"2", "2"}, {"3", "3"}, {"4", "4"}, {"5", "5"}, {"6", "6"}}

func storeWidgetDefs() {
	cat := []string{"woocommerce-elements"}
	single := []string{"woocommerce-elements-single"}
	reg := func(name, title, icon string, cats []string, kw []string, l *ctlList, render func(*elemCtx) string) {
		registerThemeWidget(&ThemeWidget{Name: name, Title: title, Icon: icon, Keywords: append(kw, "store", "shop", "woocommerce"),
			Categories: cats, Controls: l.out}, &widgetDef{render: render})
	}
	loopStyle := func(l *ctlList) {
		l.start("section_products_style", "Products", "style")
		l.slider("column_gap", "Columns Gap", "{{WRAPPER}} ul.products", "column-gap: {{SIZE}}{{UNIT}};", []string{"px", "em"},
			map[string]any{"px": map[string]any{"max": 100}}, nil)
		l.slider("row_gap", "Rows Gap", "{{WRAPPER}} ul.products", "row-gap: {{SIZE}}{{UNIT}};", []string{"px", "em"},
			map[string]any{"px": map[string]any{"max": 100}}, nil)
		l.align("align", "{{WRAPPER}} ul.products li.product")
		l.slider("image_radius", "Image Border Radius", "{{WRAPPER}} ul.products li.product img", "border-radius: {{SIZE}}{{UNIT}};",
			[]string{"px", "%"}, map[string]any{"px": map[string]any{"max": 200}}, nil)
		l.storeText("title", "Title", "{{WRAPPER}} ul.products li.product .woocommerce-loop-product__title", "primary", "primary")
		l.storeText("price", "Price", "{{WRAPPER}} ul.products li.product .price", "secondary", "text")
		l.storeButtons("button", "Button", "{{WRAPPER}} ul.products li.product .button")
		l.start("section_sale_style", "Sale Flash", "style")
		l.color("onsale_text_color", "Text Color", "{{WRAPPER}} ul.products li.product span.onsale", "color", "")
		l.color("onsale_bg_color", "Background Color", "{{WRAPPER}} ul.products li.product span.onsale", "background-color", "accent")
	}

	// Products --------------------------------------------------------------------------------
	l := &ctlList{}
	l.start("section_content", "Content", "content")
	l.add("columns", "select", "Columns", "options", storeColumns, "default", "4")
	l.add("rows", "number", "Rows", "default", 2, "min", 1, "max", 20)
	l.add("paginate", "switcher", "Pagination", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "")
	l.start("section_query", "Query", "content")
	l.add("query_post_type", "select", "Source", "options", Ordered{{"recent", "Latest Products"}, {"featured", "Featured"},
		{"sale", "On Sale"}, {"best_selling", "Best Selling"}, {"top_rated", "Top Rated"}, {"category", "Product Category"}}, "default", "recent")
	l.add("query_category", "text", "Category Slugs", "placeholder", "shirts, hats", "condition", map[string]any{"query_post_type": "category"},
		"description", "Product category slugs, separated by commas.")
	l.add("orderby", "select", "Order By", "options", Ordered{{"date", "Date"}, {"title", "Title"}, {"price", "Price"},
		{"popularity", "Popularity"}, {"rating", "Rating"}, {"rand", "Random"}, {"menu_order", "Menu Order"}}, "default", "date")
	l.add("order", "select", "Order", "options", Ordered{{"desc", "DESC"}, {"asc", "ASC"}}, "default", "desc")
	loopStyle(l)
	reg("woocommerce-products", "Products", "eicon-products", cat, []string{"products", "grid", "loop"}, l, storeRender("Products"))

	// Archive products ------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_content", "Content", "content")
	l.add("show_result_count", "switcher", "Result Count", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("allow_order", "switcher", "Allow Order", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("nothing_found_message", "textarea", "Nothing Found Message", "default", "No products were found matching your selection.")
	loopStyle(l)
	reg("wc-archive-products", "Archive Products", "eicon-products", cat, []string{"archive", "products", "catalog"}, l, storeRender("The products of the shop or category"))

	// Product categories ----------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_layout", "Layout", "content")
	l.add("columns", "select", "Columns", "options", storeColumns, "default", "4")
	l.add("number", "number", "Categories Count", "default", 4, "min", 1, "max", 24)
	l.add("hide_empty", "switcher", "Hide Empty", "label_on", "Yes", "label_off", "No", "return_value", "yes", "default", "yes")
	l.start("section_categories_style", "Categories", "style")
	l.slider("image_radius", "Image Border Radius", "{{WRAPPER}} li.product-category img", "border-radius: {{SIZE}}{{UNIT}};",
		[]string{"px", "%"}, map[string]any{"px": map[string]any{"max": 200}}, nil)
	l.storeText("title", "Title", "{{WRAPPER}} .woocommerce-loop-category__title", "primary", "primary")
	reg("wc-categories", "Product Categories", "eicon-product-categories", cat, []string{"categories", "taxonomy"}, l, storeRender("Product categories"))

	// Breadcrumb ------------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_breadcrumb_style", "Breadcrumb", "style")
	l.align("align", "{{WRAPPER}} .woocommerce-breadcrumb")
	l.storeText("text", "Text", "{{WRAPPER}} .woocommerce-breadcrumb", "text", "text")
	l.color("link_color", "Link Color", "{{WRAPPER}} .woocommerce-breadcrumb > a", "color", "")
	reg("woocommerce-breadcrumb", "Store Breadcrumbs", "eicon-product-breadcrumbs", cat, []string{"breadcrumbs", "path"}, l, storeRender("The breadcrumbs"))

	// Menu cart -------------------------------------------------------------------------------
	l = &ctlList{}
	l.start("section_menu_icon_content", "Menu Icon", "content")
	l.add("icon", "select", "Icon", "options", Ordered{{"cart", "Cart"}, {"bag", "Bag"}, {"basket", "Basket"}}, "default", "bag")
	l.add("show_subtotal", "switcher", "Subtotal", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("hide_empty_indicator", "switcher", "Hide Empty", "label_on", "Yes", "label_off", "No", "return_value", "hide", "default", "")
	l.start("section_toggle_style", "Menu Icon", "style")
	l.color("toggle_icon_color", "Icon Color", "{{WRAPPER}} .goo-menu-cart__toggle, {{WRAPPER}} .goo-menu-cart__subtotal a.cart-contents", "color", "text")
	l.color("items_indicator_text_color", "Count Text Color", "{{WRAPPER}} .goo-menu-cart__count", "color", "")
	l.color("items_indicator_background_color", "Count Background", "{{WRAPPER}} .goo-menu-cart__count", "background-color", "accent")
	l.typography("toggle_typography", "{{WRAPPER}} .goo-menu-cart__subtotal", "")
	l.start("section_cart_style", "Mini Cart", "style")
	l.color("cart_background_color", "Background Color", "{{WRAPPER}} .goo-menu-cart__panel", "background-color", "")
	l.color("cart_text_color", "Text Color", "{{WRAPPER}} .goo-menu-cart__panel", "color", "")
	reg("woocommerce-menu-cart", "Menu Cart", "eicon-cart", cat, []string{"cart", "mini cart", "basket", "bag"}, l, storeRender("The cart"))

	// Cart, checkout, my account, notices -----------------------------------------------------
	for _, p := range []struct{ name, title, icon, what string }{
		{"woocommerce-cart", "Cart", "eicon-woo-cart", "The cart"},
		{"woocommerce-checkout-page", "Checkout", "eicon-checkout", "The checkout"},
		{"woocommerce-my-account", "My Account", "eicon-my-account", "The customer account"},
	} {
		l = &ctlList{}
		l.start("section_sections_style", "Sections", "style")
		l.color("sections_background_color", "Background Color", "{{WRAPPER}} .woocommerce", "--goo-section-bg", "")
		l.slider("sections_radius", "Border Radius", "{{WRAPPER}} .woocommerce", "--goo-section-radius: {{SIZE}}{{UNIT}};", []string{"px"},
			map[string]any{"px": map[string]any{"max": 50}}, nil)
		l.start("section_typography_style", "Typography", "style")
		l.storeText("headings", "Headings", "{{WRAPPER}} .woocommerce h2, {{WRAPPER}} .woocommerce h3", "primary", "primary")
		l.storeText("body", "Text", "{{WRAPPER}} .woocommerce", "text", "text")
		l.storeButtons("buttons", "Buttons", "{{WRAPPER}} .woocommerce .button, {{WRAPPER}} .woocommerce button.button")
		reg(p.name, p.title, p.icon, cat, []string{strings.ToLower(p.title)}, l, storeRender(p.what))
	}
	l = &ctlList{}
	reg("woocommerce-notices", "Store Notices", "eicon-woocommerce-notices", cat, []string{"notices", "messages"}, l, storeRender("Store notices"))

	// Single product parts --------------------------------------------------------------------
	l = &ctlList{}
	l.heading("section_title", "Product Title", "h1", "", "", "primary", "primary")
	registerThemeWidget(&ThemeWidget{Name: "woocommerce-product-title", Title: "Product Title", Icon: "eicon-product-title",
		Keywords: []string{"store", "product", "title", "heading"}, Categories: single, Controls: l.out},
		&widgetDef{render: renderStoreProductTitle, styles: static("widget-heading"),
			wrapperClass: "elementor-widget-woocommerce-product-title elementor-page-title elementor-widget-heading"})

	l = &ctlList{}
	l.start("section_product_gallery_style", "Style", "style")
	l.add("sale_flash", "switcher", "Sale Flash", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes",
		"selectors", sel("{{WRAPPER}} span.onsale", "display: {{VALUE}}"), "selectors_dictionary", map[string]any{"yes": "block", "": "none"})
	l.slider("image_radius", "Border Radius", "{{WRAPPER}} .woocommerce-product-gallery__image img, {{WRAPPER}} .flex-control-thumbs img",
		"border-radius: {{SIZE}}{{UNIT}};", []string{"px", "%"}, map[string]any{"px": map[string]any{"max": 200}}, nil)
	l.slider("spacing_thumbs", "Thumbnails Spacing", "{{WRAPPER}} .flex-control-thumbs", "gap: {{SIZE}}{{UNIT}};", []string{"px"},
		map[string]any{"px": map[string]any{"max": 50}}, nil)
	reg("woocommerce-product-images", "Product Images", "eicon-product-images", single, []string{"images", "gallery", "image"}, l, storeRender("The product images"))

	l = &ctlList{}
	l.start("section_price_style", "Price", "style")
	l.align("text_align", "{{WRAPPER}}")
	l.storeText("price", "Price", "{{WRAPPER}} .price", "primary", "primary")
	l.color("sale_price_color", "Sale Price Color", "{{WRAPPER}} .price ins", "color", "accent")
	reg("woocommerce-product-price", "Product Price", "eicon-product-price", single, []string{"price", "sale"}, l, storeRender("The price"))

	l = &ctlList{}
	l.start("section_layout", "Layout", "content")
	l.add("show_quantity", "switcher", "Quantity", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes",
		"selectors", sel("{{WRAPPER}} .quantity", "display: {{VALUE}}"), "selectors_dictionary", map[string]any{"yes": "inline-flex", "": "none"})
	l.storeButtons("button", "Button", "{{WRAPPER}} .cart button.button, {{WRAPPER}} .cart .single_add_to_cart_button")
	l.start("section_quantity_style", "Quantity", "style")
	l.color("quantity_border_color", "Border Color", "{{WRAPPER}} .quantity .qty", "border-color", "")
	l.slider("quantity_radius", "Border Radius", "{{WRAPPER}} .quantity .qty", "border-radius: {{SIZE}}{{UNIT}};", []string{"px"},
		map[string]any{"px": map[string]any{"max": 50}}, nil)
	reg("woocommerce-product-add-to-cart", "Add To Cart", "eicon-product-add-to-cart", single, []string{"cart", "add to cart", "variations", "quantity", "button"}, l,
		storeRender("The add to cart form"))

	l = &ctlList{}
	l.start("section_rating_style", "Style", "style")
	l.color("star_color", "Star Color", "{{WRAPPER}} .star-rating", "color", "accent")
	l.color("empty_star_color", "Empty Star Color", "{{WRAPPER}} .star-rating::before", "color", "")
	l.storeText("link", "Link", "{{WRAPPER}} .woocommerce-review-link", "text", "text")
	reg("woocommerce-product-rating", "Product Rating", "eicon-product-rating", single, []string{"rating", "reviews", "stars"}, l, storeRender("The rating"))

	l = &ctlList{}
	l.start("section_stock_style", "Style", "style")
	l.storeText("text", "Text", "{{WRAPPER}} .stock", "", "text")
	reg("woocommerce-product-stock", "Product Stock", "eicon-product-stock", single, []string{"stock", "quantity", "availability"}, l, storeRender("The stock status"))

	l = &ctlList{}
	l.start("section_meta_style", "Style", "style")
	l.storeText("text", "Text", "{{WRAPPER}} .product_meta", "text", "text")
	l.color("link_color", "Link Color", "{{WRAPPER}} .product_meta a", "color", "accent")
	reg("woocommerce-product-meta", "Product Meta", "eicon-product-meta", single, []string{"meta", "sku", "categories", "tags"}, l, storeRender("The SKU, categories and tags"))

	l = &ctlList{}
	l.start("section_style", "Style", "style")
	l.align("text_align", "{{WRAPPER}}")
	l.storeText("text", "Text", "{{WRAPPER}} .woocommerce-product-details__short-description", "text", "text")
	reg("woocommerce-product-short-description", "Short Description", "eicon-product-description", single, []string{"description", "excerpt", "short"}, l,
		storeRender("The short description"))

	l = &ctlList{}
	l.start("section_product_tabs_style", "Tabs", "style")
	l.color("tab_text_color", "Text Color", "{{WRAPPER}} .woocommerce-tabs ul.wc-tabs li a", "color", "")
	l.color("tab_active_text_color", "Active Color", "{{WRAPPER}} .woocommerce-tabs ul.wc-tabs li.active a", "color", "accent")
	l.color("tab_bg_color", "Background Color", "{{WRAPPER}} .woocommerce-tabs ul.wc-tabs li", "background-color", "")
	l.typography("tab_typography", "{{WRAPPER}} .woocommerce-tabs ul.wc-tabs li a", "accent")
	l.start("section_product_panel_style", "Panel", "style")
	l.storeText("panel_text", "Text", "{{WRAPPER}} .woocommerce-Tabs-panel", "text", "text")
	reg("woocommerce-product-data-tabs", "Product Data Tabs", "eicon-product-tabs", single, []string{"tabs", "description", "reviews", "information"}, l,
		storeRender("The description, information and reviews tabs"))

	for _, p := range []struct{ name, title, icon, what string }{
		{"woocommerce-product-related", "Product Related", "eicon-product-related", "Related products"},
		{"woocommerce-product-upsell", "Upsells", "eicon-product-upsell", "Upsells"},
	} {
		l = &ctlList{}
		l.start("section_related_products_content", p.title, "content")
		l.add("columns", "select", "Columns", "options", storeColumns, "default", "4")
		l.add("posts_per_page", "number", "Products Per Page", "default", 4, "min", 1, "max", 12)
		l.start("section_heading_style", "Heading", "style")
		l.add("show_heading", "switcher", "Heading", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes",
			"selectors", sel("{{WRAPPER}} section > h2", "display: {{VALUE}}"), "selectors_dictionary", map[string]any{"yes": "block", "": "none"})
		l.storeText("heading", "Heading", "{{WRAPPER}} section > h2", "primary", "primary")
		loopStyle(l)
		reg(p.name, p.title, p.icon, single, []string{"related", "similar", "upsell"}, l, storeRender(p.what))
	}

	// Language switcher (Golylang) ------------------------------------------------------------
	l = &ctlList{}
	l.start("section_switcher", "Language Switcher", "content")
	l.add("layout", "select", "Layout", "options", Ordered{{"horizontal", "Horizontal"}, {"dropdown", "Dropdown"}}, "default", "horizontal")
	l.add("show_flags", "switcher", "Flags", "label_on", "Show", "label_off", "Hide", "return_value", "yes", "default", "yes")
	l.add("display", "select", "Names", "options", Ordered{{"code", "Language code"}, {"name", "Language name"}, {"none", "None"}}, "default", "code")
	l.add("hide_current", "switcher", "Hide the Current Language", "label_on", "Yes", "label_off", "No", "return_value", "yes", "default", "")
	l.start("section_switcher_style", "Language Switcher", "style")
	l.align("align", "{{WRAPPER}}")
	l.color("text_color", "Text Color", "{{WRAPPER}} .goo-lang-switcher a", "color", "text")
	l.color("active_color", "Current Language Color", "{{WRAPPER}} .goo-lang-switcher .current-lang a", "color", "accent")
	l.typography("typography", "{{WRAPPER}} .goo-lang-switcher a", "")
	registerThemeWidget(&ThemeWidget{Name: "golylang-language-switcher", Title: "Language Switcher", Icon: "eicon-globe",
		Keywords: []string{"language", "switcher", "translation", "multilingual", "golylang"}, Controls: l.out},
		&widgetDef{render: storeRender("The language switcher (Golylang)")})
}

func renderStoreProductTitle(w *elemCtx) string {
	t := w.theme()
	if t != nil && t.Store != nil {
		if name := t.Store(w.name, w.disp.m, w.editing()); name != "" {
			return w.headingMarkup(name, "")
		}
	}
	if p := w.currentPost(); p != nil {
		return w.headingMarkup(p.Title, "")
	}
	return w.placeholder("The product title will appear here.")
}

func init() { storeWidgetDefs() }
