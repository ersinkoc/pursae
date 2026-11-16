package server

import (
	"fmt"
	"strings"
)

// MCP prompts: the orchestration of a whole site build, served by the site itself so every MCP
// client gets the same playbook (Claude Code, Claude Desktop, Cursor, …). The pursae-site-builder
// skill in the repository (skills/pursae-site-builder) carries the same steps for agents that load
// skills, plus a harness script that talks to this endpoint without an MCP client.

// mcpInstructions is sent with initialize.
const mcpInstructions = `Tools manage this Pursae site (a WordPress-compatible CMS written in Go) end to end: pages built with Pursae Web Builder, content, media, menus, themes, plugins, Pursae Commerce (products, categories, coupons, payments, shipping, orders), Pursae Forum, contact forms, SEO, cache, users, backups and settings.
Rules:
1. Call site_overview first. Plan with the user before large changes (site type, pages, look, language).
2. Built-in plugins (Pursae Web Builder = golementor, Pursae Commerce, Pursae Forum, Pursae SEO, …) may be off. Never turn a plugin on or off without asking the user; when they agree, use activate_plugin with the slug site_overview lists.
3. Build visible pages with Pursae Web Builder (web_builder_catalog, then web_builder_build_page or web_builder_import_html): they publish immediately, rendered on the server, and stay editable by drag and drop in the editor (edit_url). Fill every text field with real copy in the site's language; never leave sample text.
4. An HTML design is imported 1:1 with web_builder_import_html. Default (method code): upload the design's folder (CSS, JS, fonts, images) with web_builder_kit_upload, then import each HTML file with kit and path; the design keeps its own code and stays editable in place. method exact rebuilds it as builder sections: open the import_url it returns in a browser, then call web_builder_import_finish.
5. Use the prompts (build_site, add_page, import_html_page) for the step-by-step playbooks, and the pursae-docs:// resources for themes, templates and plugins.
6. After building: set the front page, menus (menu_build), SEO titles/descriptions, purge the cache, then give the user the URLs and the edit links.
rest_request reaches any other REST route with the same permissions.`

type mcpPrompt struct {
	Name, Title, Desc string
	Args              []mcpPromptArg
	Text              func(args map[string]string) string
}

type mcpPromptArg struct {
	Name, Desc string
	Required   bool
}

var mcpPrompts = []mcpPrompt{
	{Name: "build_site", Title: "Build a whole site", Desc: "Plan and build a complete site (company, store, forum, blog, portfolio or landing page) with Pursae Web Builder.",
		Args: []mcpPromptArg{{"kind", "business | store | forum | blog | portfolio | landing", true}, {"brief", "What the site is for: name, audience, pages, tone, language, colors.", false}},
		Text: mcpBuildSitePrompt},
	{Name: "add_page", Title: "Add a page", Desc: "Design and publish one new Web Builder page and link it in the menu.",
		Args: []mcpPromptArg{{"page", "Which page, e.g. Pricing, About, Contact.", true}, {"brief", "What it must say.", false}},
		Text: func(a map[string]string) string {
			return "Add the page \"" + a["page"] + "\" to this site.\n" + orBlank("Brief: ", a["brief"]) + `
1. site_overview, then web_builder_get_page on the front page to match its look (site settings, palettes, designs in use).
2. web_builder_catalog (kind=… for details) and choose 3–7 sections that fit the page; write all copy in the site's language.
3. web_builder_build_page {title, sections, status: "publish"}; read the warnings and fix any sample text with web_builder_edit_page.
4. menu_build (or create_menu_item) to link it in the primary menu; seo_update_meta for its title and description.
5. Answer with the page URL and its edit_url.`
		}},
	{Name: "import_html_page", Title: "Import an HTML design 1:1", Desc: "Turn an HTML design (a page, or a folder with its CSS, JS, fonts and images) into an editable Web Builder page that is the design 1:1, with its SEO.",
		Args: []mcpPromptArg{{"title", "Page title.", true}},
		Text: func(a map[string]string) string {
			return "Turn an HTML design into the editable Web Builder page \"" + a["title"] + "\", 1:1.\n" + `
1. Check the HTML for SEO before importing: <title>, <meta name="description">, Open Graph tags and lang in the head;
   one h1 and heading levels in order; alt text on every image (ask the user for real copy if there are placeholders).
2. Upload the design's files with web_builder_kit_upload {kit: "<slug>", files: [{path, text|data}]} — every CSS, JS,
   font and image file, with the folder structure the HTML references (several calls with commit: false for a big
   folder, then one with commit: true). Not needed when the HTML loads nothing relative.
3. web_builder_import_html {html, kit, path: "<the file's path in the kit>", title, link_map: {"about.html": "/about/"},
   front_page} — method code (default): every top-level part becomes a code section with the design's own markup,
   CSS and JavaScript (animations and scripts keep working); in the editor every element is a block (select, move, duplicate, delete, style) and texts, links and images are edited in place.
   For a site: templates: true turns the design's header and footer into site-wide templates; import the other pages
   of the design the same way, and map their links to each other with link_map. Read warnings: unmapped links and files
   missing from the kit.
4. Check the result: open the url (and compare with the design at desktop and phone width if you can take screenshots),
   then give the user the url and edit_url.
If the user wants builder sections they can restyle by dragging instead of the design's own code, use method "exact":
it answers import_url — open it in a browser (yours or the user's), then web_builder_import_finish {job}.`
		}},
}

func orBlank(prefix, v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return prefix + v + "\n"
}

func mcpBuildSitePrompt(a map[string]string) string {
	kind := strings.ToLower(strings.TrimSpace(a["kind"]))
	var b strings.Builder
	fmt.Fprintf(&b, "Build a complete %s site on this Pursae installation.\n%s", kindLabel(kind), orBlank("Brief: ", a["brief"]))
	b.WriteString(`
## 0. Understand and plan (do not skip)
- Call site_overview. Note the active plugins, theme, existing pages and menus.
- If the brief leaves out the name, audience, language, main color or the page list, ask the user in one short message, then write a plan: plugins to turn on, pages with their sections, menus, and the look (brand color, fontPair, corners). Get a yes before building.

## 1. Plugins (ask before turning anything on)
- Pursae Web Builder (slug golementor) for every page. `)
	switch kind {
	case "store", "shop", "ecommerce", "e-commerce":
		b.WriteString("Pursae Commerce for the shop. ")
	case "forum", "community":
		b.WriteString("Pursae Forum for the community. ")
	}
	b.WriteString(`Pursae SEO for titles, descriptions and sitemaps. Contact forms come from the builder's form section.
- Turning on the builder switches the theme to Hello Pursae (a blank canvas for Theme Builder templates).

## 2. Look and site frame
- web_builder_catalog: pick designs whose tags match the brief (e.g. Corporate, Minimal, E-commerce, SaaS, Agency).
- Site-wide header and footer: web_builder_create_template {type: "header"} and {type: "footer"}, then web_builder_build_page {post_id: <template id>, sections: [one nav section] / [one footer section]} (new header and footer templates apply to the whole site; narrow them with web_builder_set_template_conditions {conditions: ["include/general", "exclude/singular/page/12"]}). Or start from a ready kit: web_builder_list_theme_kits → web_builder_import_theme_kit, then rewrite its pages.
- Use one site look on every page: the same site {brand, fontPair, corners} in each build call.

## 3. Content first, then pages
`)
	switch kind {
	case "store", "shop", "ecommerce", "e-commerce":
		b.WriteString(`- Store settings: commerce_get_settings {group: general} and commerce_update_settings (currency, store address); commerce_list_payment_gateways → commerce_update_payment_gateway (enable at least one; never invent secret keys — ask the user to enter live keys in the admin); commerce_create_shipping_zone.
- Catalog: commerce_create_category for each category, upload_media for product photos (source_url), commerce_create_product {name, regular_price, description, short_description, categories, images}. Ask before inventing prices.
- Pages: Home (hero + category/feature cards + best sellers or highlights + testimonial + CTA), About, Contact (form section), FAQ / Shipping & Returns. The shop, cart, checkout and my-account pages come from Pursae Commerce; link them in the menu.
`)
	case "forum", "community":
		b.WriteString(`- Forum structure: get_options / update_options {group: "forum", settings: {title, base, registration, require_verify, moderate_first_post, show_members}}; forum_create for categories (kind category) and forums inside them (parent); forum_create_topic for a welcome post and the rules in each main forum.
- Pages: Home (hero with a "Join the community" button to the forum URL, feature cards about the forums, steps "Sign up – Introduce yourself – Ask", FAQ, CTA), Rules / Code of conduct, About, Contact. Link the forum index (forum_list → index URL) in the menu.
`)
	case "blog", "magazine":
		b.WriteString(`- Content: create_term for categories, a few create_post drafts or published posts with featured images (upload_media).
- Pages: Home (hero + latest posts via a Theme Builder archive or a loop section + newsletter CTA), About, Contact. Theme Builder single and archive templates give posts the same look.
`)
	default:
		b.WriteString(`- Pages: Home (nav-free hero + services/features + image-and-text + steps or stats + testimonial + FAQ + CTA), About, Services (or Products), Contact (form section with address/phone/e-mail), plus any page the brief names.
- Upload real images when the user gives them (upload_media); otherwise use neutral stock image URLs and say so.
`)
	}
	b.WriteString(`- Build each page with web_builder_build_page {title, sections, site, status: "publish"}; layout "site" (default) keeps the Theme Builder header/footer around it.
- Copy: real, specific text in the site's language for every field; read the warnings and fix leftovers with web_builder_edit_page.

## 4. Wire it together
- Front page: web_builder_build_page … front_page: true (or update_settings {show_on_front: "page", page_on_front: id}).
- Menus: menu_build {name: "Main", locations: ["primary"], items: [...]} and a footer menu (locations ["footer"]).
- Site identity: update_settings {title, description}; upload a logo and set site_logo / site_icon when the user gives one.
- SEO: seo_update_meta for each page (title ≤ 60 chars, description ≤ 155).
- cache_purge {all: true}.

## 5. Report
- List every page with its URL and edit_url, what plugins you turned on, and what the user still has to do (payment keys, real photos, legal texts). Do not claim anything you did not check: open the URLs with your browser tools when you have them.
`)
	return b.String()
}

func kindLabel(k string) string {
	switch k {
	case "store", "shop", "ecommerce", "e-commerce":
		return "e-commerce"
	case "forum", "community":
		return "forum / community"
	case "", "business", "corporate", "company":
		return "company"
	}
	return k
}

func mcpPromptByName(name string) *mcpPrompt {
	for i := range mcpPrompts {
		if mcpPrompts[i].Name == name {
			return &mcpPrompts[i]
		}
	}
	return nil
}

func mcpPromptsList() []map[string]any {
	out := []map[string]any{}
	for _, p := range mcpPrompts {
		var args []map[string]any
		for _, a := range p.Args {
			args = append(args, map[string]any{"name": a.Name, "description": a.Desc, "required": a.Required})
		}
		out = append(out, map[string]any{"name": p.Name, "title": p.Title, "description": p.Desc, "arguments": args})
	}
	return out
}

// mcpPromptGet answers prompts/get; ok is false for an unknown prompt or a missing argument.
func mcpPromptGet(name string, raw map[string]any) (map[string]any, string) {
	p := mcpPromptByName(name)
	if p == nil {
		return nil, "Unknown prompt: " + name
	}
	args := map[string]string{}
	for k, v := range raw {
		args[k] = fmt.Sprint(v)
	}
	for _, a := range p.Args {
		if a.Required && strings.TrimSpace(args[a.Name]) == "" {
			return nil, "Missing argument: " + a.Name
		}
	}
	return map[string]any{"description": p.Desc, "messages": []map[string]any{{"role": "user", "content": map[string]any{"type": "text", "text": p.Text(args)}}}}, ""
}
