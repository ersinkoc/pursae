package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/webbuilder"
)

// Site-level MCP tools: what an agent needs to set up a whole site quickly — where the site stands
// (site_overview), a menu in one call (menu_build), Pursae Forum (pursae/v1/forum), more of Pursae
// Commerce (categories, coupons, payments, shipping, store settings) and contact forms. The
// orchestration — which plugins a forum, a shop or a company site needs and in what order to build
// it — is in the MCP prompts (mcp_prompts.go) and the pursae-site-builder skill.

// mcpOverview is what site_overview returns.
func mcpSiteOverview(s *Server, c *gin.Context, _ map[string]any) (any, error) {
	out := map[string]any{"pursae_version": Version}
	var idx map[string]any
	if err := s.mcpREST(c, http.MethodGet, "/", nil, &idx); err == nil {
		out["site"] = map[string]any{"name": idx["name"], "description": idx["description"], "url": idx["home"]}
	}
	var me map[string]any
	if err := s.mcpREST(c, http.MethodGet, "/wp/v2/users/me?context=edit", nil, &me); err == nil {
		out["you"] = map[string]any{"id": me["id"], "name": me["name"], "roles": me["roles"]}
	}
	var st map[string]any
	if err := s.mcpREST(c, http.MethodGet, "/wp/v2/settings", nil, &st); err == nil {
		out["settings"] = map[string]any{"title": st["title"], "description": st["description"], "show_on_front": st["show_on_front"],
			"page_on_front": st["page_on_front"], "page_for_posts": st["page_for_posts"], "site_logo": st["site_logo"], "site_icon": st["site_icon"],
			"language": st["language"], "timezone": st["timezone"]}
	}
	var plugins []map[string]any
	if err := s.mcpREST(c, http.MethodGet, "/pursae/v1/plugins", nil, &plugins); err == nil {
		var active, inactive []string
		for _, p := range plugins {
			slug := fmt.Sprint(p["slug"])
			name := fmt.Sprint(p["name"])
			if b, _ := p["active"].(bool); b {
				active = append(active, slug+" ("+name+")")
			} else {
				inactive = append(inactive, slug+" ("+name+")")
			}
		}
		out["plugins"] = map[string]any{"active": active, "inactive": inactive}
	}
	var themes []map[string]any
	if err := s.mcpREST(c, http.MethodGet, "/pursae/v1/themes", nil, &themes); err == nil {
		for _, t := range themes {
			if b, _ := t["active"].(bool); b {
				out["theme"] = t["slug"]
			}
		}
	}
	var menus []map[string]any
	if err := s.mcpREST(c, http.MethodGet, "/wp/v2/menus?per_page=100", nil, &menus); err == nil {
		var list []map[string]any
		for _, m := range menus {
			list = append(list, map[string]any{"id": m["id"], "name": m["name"], "locations": m["locations"]})
		}
		out["menus"] = list
	}
	var pages []map[string]any
	if err := s.mcpREST(c, http.MethodGet, "/wp/v2/pages?per_page=100&status=publish,draft,private&_fields=id,title,status,link,parent", nil, &pages); err == nil {
		var list []map[string]any
		for _, p := range pages {
			title := p["title"]
			if t, ok := title.(map[string]any); ok {
				title = t["rendered"]
			}
			list = append(list, map[string]any{"id": p["id"], "title": title, "status": p["status"], "link": p["link"]})
		}
		out["pages"] = list
	}
	wb := map[string]any{"active": s.wbOn(), "plugin_slug": webbuilder.PluginSlug}
	if _, err := webbuilder.SharedServerRenderer(s.web); err == nil {
		wb["server_render"] = true
	} else {
		wb["server_render"] = false
	}
	out["web_builder"] = wb
	out["next"] = "Plan the site with the user, then follow the build_site prompt (or the pursae-site-builder skill): turn on only the plugins the site needs (ask first), set the look with web_builder_catalog, build pages with web_builder_build_page, menus with menu_build, and finish with the front page, SEO and a cache purge."
	return out, nil
}

// mcpMenuBuild creates a menu (or refills an existing one) with nested items in one call.
func mcpMenuBuild(s *Server, c *gin.Context, args map[string]any) (any, error) {
	name := argStr(args, "name")
	menuID := argInt(args, "menu_id")
	items := argList(args, "items")
	if len(items) == 0 {
		return nil, mcpErrf("items is required: [{title, page_id | post_id | category_id | url, children?: [...]}]")
	}
	locs := []string{}
	for _, l := range argList(args, "locations") {
		locs = append(locs, fmt.Sprint(l))
	}
	if menuID == 0 {
		if name == "" {
			return nil, mcpErrf("give name (a new menu) or menu_id")
		}
		// a menu of the same name is refilled, so building twice does not make copies
		var menus []map[string]any
		_ = s.mcpREST(c, http.MethodGet, "/wp/v2/menus?per_page=100", nil, &menus)
		for _, m := range menus {
			if strings.EqualFold(fmt.Sprint(m["name"]), name) {
				menuID = argInt(m, "id")
			}
		}
	}
	if menuID == 0 {
		var created map[string]any
		body := map[string]any{"name": name}
		if len(locs) > 0 {
			body["locations"] = locs
		}
		if err := s.mcpREST(c, http.MethodPost, "/wp/v2/menus", body, &created); err != nil {
			return nil, err
		}
		menuID = argInt(created, "id")
	} else {
		if len(locs) > 0 {
			if err := s.mcpREST(c, http.MethodPost, "/wp/v2/menus/"+itoa64(menuID), map[string]any{"locations": locs}, nil); err != nil {
				return nil, err
			}
		}
		if !argBool(args, "keep_items") {
			var old []map[string]any
			_ = s.mcpREST(c, http.MethodGet, "/wp/v2/menu-items?per_page=100&menus="+itoa64(menuID), nil, &old)
			for _, it := range old {
				_ = s.mcpREST(c, http.MethodDelete, "/wp/v2/menu-items/"+itoa64(argInt(it, "id"))+"?force=true", nil, nil)
			}
		}
	}
	order := 0
	var made []map[string]any
	var add func(list []any, parent int64) error
	add = func(list []any, parent int64) error {
		for _, x := range list {
			it, _ := x.(map[string]any)
			if it == nil {
				return mcpErrf("menu items must be objects")
			}
			order++
			body := map[string]any{"menus": menuID, "title": argStr(it, "title"), "menu_order": order, "status": "publish"}
			if parent > 0 {
				body["parent"] = parent
			}
			switch {
			case argInt(it, "page_id") > 0:
				body["type"], body["object"], body["object_id"] = "post_type", "page", argInt(it, "page_id")
			case argInt(it, "post_id") > 0:
				body["type"], body["object"], body["object_id"] = "post_type", "post", argInt(it, "post_id")
			case argInt(it, "category_id") > 0:
				body["type"], body["object"], body["object_id"] = "taxonomy", "category", argInt(it, "category_id")
			case argStr(it, "url") != "":
				body["type"], body["url"] = "custom", argStr(it, "url")
			default:
				return mcpErrf("menu item %q needs page_id, post_id, category_id or url", argStr(it, "title"))
			}
			var created map[string]any
			if err := s.mcpREST(c, http.MethodPost, "/wp/v2/menu-items", body, &created); err != nil {
				return err
			}
			id := argInt(created, "id")
			made = append(made, map[string]any{"id": id, "title": body["title"], "parent": parent})
			if kids := argList(it, "children"); len(kids) > 0 {
				if err := add(kids, id); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := add(items, 0); err != nil {
		return nil, err
	}
	return map[string]any{"menu_id": menuID, "locations": locs, "items": made}, nil
}

// mcpSiteTools are the site-level tools added to mcpTools.
func mcpSiteTools() []*mcpTool {
	idp := map[string]any{"id": num("ID.")}
	with := func(base map[string]any, extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	forumFields := map[string]any{"title": str("Name."), "kind": str("forum (holds topics) or category (holds forums)."), "parent": num("Category ID (forums only)."),
		"slug": str("Slug."), "description": str("Description."), "closed": flag("No new topics."), "order": num("Position.")}
	return []*mcpTool{
		{Name: "site_overview", Desc: "Start here: the site's name, URL, your role, active and inactive plugins (slugs for activate_plugin), theme, front page, menus, pages and whether Pursae Web Builder is on.",
			Path: "/", run: mcpSiteOverview, readOnly: true},
		{Name: "menu_build", Desc: "Create a navigation menu (or refill one with the same name / menu_id) with nested items in one call and assign theme locations (e.g. primary, footer). Items: {title, page_id | post_id | category_id | url, children?}.",
			Path: "/wp/v2/menus", Props: map[string]any{"name": str("Menu name."), "menu_id": num("Existing menu to refill."), "locations": strList("Theme menu locations (list_menu_locations)."),
				"items":      map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "Items in order: {title, page_id | post_id | category_id | url, children?: [...]}."},
				"keep_items": flag("Append instead of replacing the menu's items.")}, Required: []string{"items"}, run: mcpMenuBuild},

		// Pursae Forum (pursae/v1/forum)
		{Name: "forum_list", Desc: "List Pursae Forum categories and forums with topic counts and URLs.", Method: http.MethodGet, Path: "/pursae/v1/forum/forums"},
		{Name: "forum_create", Desc: "Create a forum category or a forum (a forum may sit in a category).", Method: http.MethodPost, Path: "/pursae/v1/forum/forums", Props: forumFields, Required: []string{"title"}},
		{Name: "forum_update", Desc: "Change a forum or category (only the fields given).", Method: http.MethodPost, Path: "/pursae/v1/forum/forums/{id}", Props: with(idp, forumFields), Required: []string{"id"}},
		{Name: "forum_delete", Desc: "Delete a forum or category.", Method: http.MethodDelete, Path: "/pursae/v1/forum/forums/{id}", Props: idp, Required: []string{"id"}},
		{Name: "forum_create_topic", Desc: "Start a topic in a forum as you (e.g. a welcome post or forum rules).", Method: http.MethodPost, Path: "/pursae/v1/forum/topics",
			Props: map[string]any{"forum_id": num("Forum ID."), "title": str("Title."), "content": str("Text (simple HTML allowed).")}, Required: []string{"forum_id", "title", "content"}},
		{Name: "forum_list_templates", Desc: "List the forum views (index, forum, topic, new-topic, login, register, verify, search, profile, members, account, edit) and which have a site template: the site's own markup for a view, e.g. from an imported HTML design.",
			Method: http.MethodGet, Path: "/pursae/v1/forum/templates"},
		{Name: "forum_get_template", Desc: "A forum view's site template and the plugin's default template for it (`default`: shows the data on the dot — .Forums, .Topics, .Topic, .Replies, .URLs, forms and their nonces — and the partials it calls).",
			Method: http.MethodGet, Path: "/pursae/v1/forum/templates/{view}", Props: map[string]any{"view": str("View name.")}, Required: []string{"view"}},
		{Name: "forum_set_template", Desc: "Give a forum view the site's own markup: a Go html/template whose dot is the view's data (copy the data usage and the form fields from forum_get_template's default; keep id=\"post-{{.ID}}\" on posts and every form's forum_action, nonce and traps). main: true makes it print the page's whole <main> (a design imported as code sections: no theme title or wrapper, no forum.css). Parse- and render-checked; rolled back on error. Needs unfiltered_html.",
			Method: http.MethodPost, Path: "/pursae/v1/forum/templates/{view}", Props: map[string]any{"view": str("View name."), "source": str("Template source."), "main": flag("Prints the whole <main> (Hello Pursae with Web Builder).")}, Required: []string{"view", "source"}},
		{Name: "forum_delete_template", Desc: "Remove a forum view's site template (the view goes back to the theme's or the plugin's markup).",
			Method: http.MethodDelete, Path: "/pursae/v1/forum/templates/{view}", Props: map[string]any{"view": str("View name.")}, Required: []string{"view"}},

		// Pursae Commerce (wc/v3)
		{Name: "commerce_list_categories", Desc: "List product categories.", Method: http.MethodGet, Path: "/wc/v3/products/categories", Props: listProps(nil), Open: true},
		{Name: "commerce_create_category", Desc: "Create a product category {name, slug?, parent?, description?, image?: {src}}.", Method: http.MethodPost, Path: "/wc/v3/products/categories",
			Props: map[string]any{"name": str("Name."), "slug": str("Slug."), "parent": num("Parent category ID."), "description": str("Description."), "image": obj("{src: URL} or {id: attachment ID}.")}, Required: []string{"name"}},
		{Name: "commerce_update_category", Desc: "Update a product category.", Method: http.MethodPut, Path: "/wc/v3/products/categories/{id}", Props: idp, Required: []string{"id"}, Open: true},
		{Name: "commerce_list_coupons", Desc: "List coupons.", Method: http.MethodGet, Path: "/wc/v3/coupons", Props: listProps(nil), Open: true},
		{Name: "commerce_create_coupon", Desc: "Create a coupon {code, discount_type: percent | fixed_cart | fixed_product, amount, …}.", Method: http.MethodPost, Path: "/wc/v3/coupons",
			Props: map[string]any{"code": str("Coupon code."), "discount_type": str("percent, fixed_cart or fixed_product."), "amount": str("Amount, e.g. \"10\".")}, Required: []string{"code"}, Open: true},
		{Name: "commerce_get_settings", Desc: "Store settings of a group: general (address, currency, …), products, tax, shipping, checkout, account, email.", Method: http.MethodGet, Path: "/wc/v3/settings/{group}",
			Props: map[string]any{"group": str("Settings group, e.g. general.")}, Required: []string{"group"}},
		{Name: "commerce_update_settings", Desc: "Change store settings of a group: {group, update: [{id: \"woocommerce_currency\", value: \"EUR\"}, …]}.", Method: http.MethodPost, Path: "/wc/v3/settings/{group}/batch",
			Props: map[string]any{"group": str("Settings group."), "update": map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "[{id, value}]"}}, Required: []string{"group", "update"}},
		{Name: "commerce_list_payment_gateways", Desc: "List payment methods with their enabled state and settings.", Method: http.MethodGet, Path: "/wc/v3/payment_gateways"},
		{Name: "commerce_update_payment_gateway", Desc: "Enable/disable or configure a payment method: {id, enabled?, title?, description?, settings?: {key: value}}. Never put real secret keys here unless the user typed them for this purpose.",
			Method: http.MethodPut, Path: "/wc/v3/payment_gateways/{id}", Props: map[string]any{"id": str("Gateway ID, e.g. bacs, cod, cheque, stripe.")}, Required: []string{"id"}, Open: true},
		{Name: "commerce_list_shipping_zones", Desc: "List shipping zones (methods: rest_request GET /wc/v3/shipping/zones/{id}/methods).", Method: http.MethodGet, Path: "/wc/v3/shipping/zones"},
		{Name: "commerce_create_shipping_zone", Desc: "Create a shipping zone {name, order?}; add methods with rest_request POST /wc/v3/shipping/zones/{id}/methods {method_id: flat_rate | free_shipping | local_pickup}.",
			Method: http.MethodPost, Path: "/wc/v3/shipping/zones", Props: map[string]any{"name": str("Zone name."), "order": num("Order.")}, Required: []string{"name"}},

		// Contact forms (Pursae Contact Form, CF7-compatible)
		{Name: "contact_list_forms", Desc: "List Pursae Contact Form forms (shortcode [contact-form-7 id=\"…\"]). On Web Builder pages prefer the builder's own form section.",
			Method: http.MethodGet, Path: "/contact-form-7/v1/contact-forms"},
		{Name: "contact_create_form", Desc: "Create a contact form: {title, form: CF7 form template, e.g. \"<label>Your name [text* your-name]</label> [email* your-email] [textarea your-message] [submit \\\"Send\\\"]\", mail?: {recipient, subject, body}}.",
			Method: http.MethodPost, Path: "/contact-form-7/v1/contact-forms", Props: map[string]any{"title": str("Title."), "form": str("Form template.")}, Required: []string{"title"}, Open: true},
		{Name: "contact_update_form", Desc: "Update a contact form (fields as in contact_create_form).", Method: http.MethodPost, Path: "/contact-form-7/v1/contact-forms/{id}", Props: idp, Required: []string{"id"}, Open: true},
	}
}
