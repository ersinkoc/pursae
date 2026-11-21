package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// A small static site whose header menu, news pages, news list and contact form a site import binds
// to Pursae content (docs/developer/web-builder-code-sections.md, "Binding to content").
func bindTestPage(cur, title, body string) string {
	up := ""
	if strings.Contains(cur, "/") {
		up = "../"
	}
	link := func(href, label string) string {
		if href == cur {
			return `<li><a href="` + up + href + `" class="nav on" aria-current="page">` + label + `</a></li>`
		}
		return `<li><a href="` + up + href + `" class="nav off">` + label + `</a></li>`
	}
	return `<!doctype html><html lang="tr"><head><meta charset="utf-8"><title>` + title + ` | Demo</title>
<link rel="stylesheet" href="` + up + `css/site.css"></head><body>
<header id="top"><nav><ul class="menu">
` + link("index.html", "Home") + `
` + link("haberler.html", "News") + `
` + link("iletisim.html", "Contact") + `
</ul></nav></header>
<main id="c">
` + body + `
</main>
<footer class="foot"><p>© 2026</p></footer>
<script src="` + up + `js/main.js" defer></script>
</body></html>`
}

func bindNews(slug, title, paras string) string {
	return bindTestPage("haber/"+slug+".html", title, `<section class="hero"><p class="crumb"><a href="../haberler.html">News</a> / <span>`+title+`</span></p><h1 class="t">`+title+`</h1></section>
<section class="body"><div class="prose">`+paras+`</div></section>`)
}

func TestMCPImportSiteBindings(t *testing.T) {
	ts, c, srv := newTestServer(t, true)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, call := mcpTestAgent(t, base, c, "Bind agent", nil)
	if r := call("activate_theme", map[string]any{"slug": wbHelloSlug}); r["isError"] == true {
		t.Fatalf("theme: %v", mcpText(r))
	}
	mcpStructured(t, "web_builder_kit_upload", call("web_builder_kit_upload", map[string]any{"kit": "demo", "files": []any{
		map[string]any{"path": "css/site.css", "text": ".nav.on{color:red}"},
		map[string]any{"path": "js/main.js", "text": "1"},
		map[string]any{"path": "img/a.jpg", "data": "/9j/4AAQ"},
		map[string]any{"path": "img/b.jpg", "data": "/9j/4AAQ"},
		map[string]any{"path": "img/c.jpg", "data": "/9j/4AAQ"},
	}}))
	card := func(slug, title, sum string) string {
		return `<li class="card"><a href="haber/` + slug + `.html" class="go"><img src="img/` + slug + `.jpg" alt="` + title + `"><h3>` + title + `</h3><p class="sum">` + sum + `</p></a></li>`
	}
	pages := []any{
		map[string]any{"path": "index.html", "html": bindTestPage("index.html", "Home", `<section><h1>Welcome</h1></section>`)},
		map[string]any{"path": "haberler.html", "html": bindTestPage("haberler.html", "News", `<section><h1>News</h1><ul class="cards">
`+card("a", "Alpha news", "The first summary, long enough to be the excerpt of the page.")+`
`+card("b", "Beta news", "The second summary, long enough to be the excerpt of the page.")+`
`+card("c", "Gamma news", "The third summary, long enough to be the excerpt of the page.")+`
</ul></section>`)},
		map[string]any{"path": "haber/a.html", "html": bindNews("a", "Alpha news", `<p>Alpha one, a paragraph of the news text.</p><p>Alpha two, with more of the story.</p>`)},
		map[string]any{"path": "haber/b.html", "html": bindNews("b", "Beta news", `<p>Beta one, a paragraph of the news text that tells the whole story of the second piece of news.</p>`)},
		map[string]any{"path": "haber/c.html", "html": bindNews("c", "Gamma news", `<p>Gamma one, a paragraph that opens the third piece of news at some length.</p><ul><li>Gamma list item</li></ul><p>Gamma end of the story.</p>`)},
		map[string]any{"path": "iletisim.html", "html": bindTestPage("iletisim.html", "Contact", `<section><div id="cf-wrap"><form id="contact-form" novalidate data-mail="x@example.test">
<h2>Write to us</h2><label for="cf-name">Name *</label><input id="cf-name" name="name"><label for="cf-mail">E-mail</label><input id="cf-mail" name="email" type="email" required>
<label for="cf-company">Company</label><input id="cf-company" name="company"><select name="subject"><option value="">Pick</option><option value="Health">Health</option></select>
<label>City <input name="city"></label><input type="text" name="full-name">
<label><input type="radio" name="pref" value="mail"> Mail</label><label><input type="radio" name="pref" value="phone"> Phone</label>
<textarea name="message"></textarea><button type="submit">Send</button></form><div id="cf-done" class="hidden">Thanks!</div></div></section>`)},
	}
	out := mcpStructured(t, "web_builder_import_site", call("web_builder_import_site", map[string]any{"kit": "demo", "pages": pages, "collections": []any{"haber"}}))
	b, _ := out["bindings"].(map[string]any)
	if b == nil || b["menu"] == nil || len(asList(b["groups"])) != 1 || len(asList(b["listings"])) != 1 {
		t.Fatalf("bindings: %v", out["bindings"])
	}
	get := func(path string) string {
		t.Helper()
		resp, err := c.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d", path, resp.StatusCode)
		}
		return string(body)
	}

	// The news pages are content under one single template, at their URLs.
	page := get("/haber/c/")
	for _, want := range []string{`<h1 class="t" data-wb-slot="post.title-text">Gamma news</h1>`, `Gamma list item`, `<span data-wb-slot="post.title-text">Gamma news</span>`} {
		if !strings.Contains(page, want) {
			t.Errorf("/haber/c/ lacks %s", want)
		}
	}
	if strings.Contains(page, "Alpha one") {
		t.Errorf("/haber/c/ shows another page's content")
	}
	// The header menu: the design's markup, the current page marked by the import's rule.
	news := get("/haberler/")
	if !strings.Contains(news, `data-wb-menu-loc="primary"`) || !regexpFind(`<a [^>]*class="nav on"[^>]*>News</a>`, news) {
		t.Errorf("menu: %s", between(news, `<ul class="menu"`, `</ul>`))
	}
	// The list is a loop over the news pages, in the design's order.
	if i, j, k := strings.Index(news, "Alpha news</h3>"), strings.Index(news, "Beta news</h3>"), strings.Index(news, "Gamma news</h3>"); i < 0 || !(i < j && j < k) {
		t.Errorf("cards: %s", between(news, `<ul class="cards"`, `</ul>`))
	}
	if !strings.Contains(news, `The second summary, long enough to be the excerpt of the page.`) {
		t.Errorf("excerpt missing")
	}

	// Content editors manage it: a renamed page, a new page under the folder, a new menu item.
	groups := asList(b["groups"])
	var betaID, parentID float64
	for _, p := range asList(out["pages"]) {
		m := p.(map[string]any)
		if m["path"] == "haberler.html" {
			_ = m
		}
	}
	var found []map[string]any
	_ = json.Unmarshal([]byte(get("/wp-json/wp/v2/pages?search=Beta&_fields=id,parent")), &found)
	for _, m := range found {
		if p := m["parent"].(float64); p > 0 {
			betaID, parentID = m["id"].(float64), p
		}
	}
	if betaID == 0 || parentID == 0 || len(groups) != 1 {
		t.Fatalf("beta page: %v", found)
	}
	if r := call("update_page", map[string]any{"id": betaID, "title": "Beta renamed"}); r["isError"] == true {
		t.Fatalf("rename: %v", mcpText(r))
	}
	if r := call("create_page", map[string]any{"title": "Delta news", "status": "publish", "parent": parentID, "content": "<p>Delta body.</p>", "menu_order": -1}); r["isError"] == true {
		t.Fatalf("new page: %v", mcpText(r))
	}
	menu := b["menu"].(map[string]any)
	if r := call("create_menu_item", map[string]any{"menus": menu["id"], "title": "Blog", "type": "custom", "url": base + "/blog/"}); r["isError"] == true {
		t.Fatalf("menu item: %v", mcpText(r))
	}
	news = get("/haberler/")
	if !strings.Contains(news, "Beta renamed</h3>") || !strings.Contains(news, "Delta news</h3>") || strings.Contains(news, "Gamma news</h3>") {
		t.Errorf("the loop does not follow the pages: %s", between(news, `<ul class="cards"`, `</ul>`))
	}
	if !regexpFind(`<li[^>]*><a class="nav off"[^>]*href="[^"]*/blog/">Blog</a></li>`, news) {
		t.Errorf("new menu item: %s", between(news, `<ul class="menu"`, `</nav>`))
	}
	if page := get("/haber/delta-news/"); !strings.Contains(page, `data-wb-slot="post.title-text">Delta news</h1>`) || !strings.Contains(page, "Delta body.") {
		t.Errorf("a new page under the folder is not drawn by the template: %s", between(page, "<main", "</main>"))
	}

	// The contact form bound to the form endpoint: its submission is stored.
	var contactID float64
	for _, p := range asList(out["pages"]) {
		if m := p.(map[string]any); m["path"] == "iletisim.html" {
			contactID = m["post_id"].(float64)
		}
	}
	bound := mcpStructured(t, "web_builder_bind", call("web_builder_bind", map[string]any{"post_id": contactID, "html_id": "contact-form",
		"bind": map[string]any{"kind": "form", "done": "", "required": []any{"name", "full-name"}}}))
	key, _ := bound["form"].(string)
	if key == "" {
		t.Fatalf("bind: %v", bound)
	}
	contact := get("/iletisim/")
	for _, want := range []string{`data-wb-code-form="` + key + `"`, `/wp-json/web-builder/v2/forms/`, `name="wb_nonce"`, `id="wb-code-form"`, `data-mail="x@example.test"`} {
		if !strings.Contains(contact, want) {
			t.Errorf("/iletisim/ lacks %s: %s", want, between(contact, "<form", "</footer>"))
		}
	}
	nonce := regexpGroup(`name="wb_nonce" value="([^"]+)"`, contact)
	action := regexpGroup(`action="([^"]*/forms/[^"]+)"`, contact)
	form := url.Values{"name": {"Ada"}, "email": {"ada@example.test"}, "company": {"ACME"}, "subject": {"Health"}, "message": {"Hello"},
		"city": {"<b>Izmir</b>"}, "full-name": {"Ada Lovelace"}, "pref": {"phone"}, "wb_nonce": {nonce}, "wb_t": {"5000"}}
	req, _ := http.NewRequest(http.MethodPost, strings.ReplaceAll(action, "&amp;", "&"), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", base)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var res map[string]any
	_ = json.Unmarshal(body, &res)
	if resp.StatusCode != 200 || res["ok"] != true {
		t.Fatalf("submit: %d %s", resp.StatusCode, body)
	}
	var fields string
	if err := srv.store.Pool.QueryRow(t.Context(), `SELECT fields::text FROM web_builder_form_submissions ORDER BY id DESC LIMIT 1`).Scan(&fields); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fields, `"company": "ACME"`) && !strings.Contains(fields, `"company":"ACME"`) {
		t.Errorf("stored fields: %s", fields)
	}
	// Settings › Web Builder lists the code form's own fields too, escaped.
	screen := get(wbSettingsURL)
	for _, want := range []string{"ACME", "Health", "&lt;b&gt;Izmir&lt;/b&gt;", "<strong>city:</strong>", "Ada Lovelace", "<strong>pref:</strong> phone"} {
		if !strings.Contains(screen, want) {
			t.Errorf("submissions screen lacks %s", want)
		}
	}
	if strings.Contains(screen, "<b>Izmir</b>") {
		t.Error("a submitted value is printed unescaped")
	}
	refused := func(name, value string) string {
		t.Helper()
		f := url.Values{}
		for k, v := range form {
			f[k] = v
		}
		f.Set(name, value)
		req, _ := http.NewRequest(http.MethodPost, strings.ReplaceAll(action, "&amp;", "&"), strings.NewReader(f.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", base)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 422 {
			t.Errorf("%s=%q: %d %s", name, value, resp.StatusCode, body)
		}
		return string(body)
	}
	// A required field the design left empty is refused with the field named, by the design's
	// own input name (the form script marks [name="full-name"], not field_full_name).
	if body := refused("name", ""); !strings.Contains(body, `"name":"required"`) {
		t.Errorf("empty name: %s", body)
	}
	if body := refused("full-name", ""); !strings.Contains(body, `"full-name":"required"`) || strings.Contains(body, "field_full_name") {
		t.Errorf("empty full-name: %s", body)
	}
	// A radio value the markup does not declare is refused like a select's.
	if body := refused("pref", "fax"); !strings.Contains(body, `"pref":"invalid"`) {
		t.Errorf("undeclared radio value: %s", body)
	}
}

func regexpFind(re, s string) bool { return regexp.MustCompile(re).MatchString(s) }

func regexpGroup(re, s string) string {
	if m := regexp.MustCompile(re).FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func TestWBImportMenuKeepsActiveItemsAfterReplacementFailure(t *testing.T) {
	srv, _, _ := wbrStart(t)
	menu := &webbuilder.SiteMenu{Items: []webbuilder.SiteMenuItem{
		{Label: "Existing parent", URL: "/existing/", Children: []webbuilder.SiteMenuItem{
			{Label: "Existing child", URL: "/existing/child/"},
		}},
	}}
	_, ids, err := srv.wbSiteMenu(wbsGin(), "menu-rollback", menu, nil)
	if err != nil || len(ids) != 2 {
		t.Fatalf("save existing menu: %v, %v", ids, err)
	}
	if _, err := srv.store.Pool.Exec(t.Context(),
		`ALTER TABLE menu_items ADD CONSTRAINT rejected_import_item CHECK (label <> 'Blocked replacement')`); err != nil {
		t.Fatal(err)
	}
	menu.Items[0].Label = "New parent"
	menu.Items[0].Children[0].Label = "Blocked replacement"
	if _, _, err := srv.wbSiteMenu(wbsGin(), "menu-rollback", menu, nil); err == nil {
		t.Fatal("replacement should fail")
	}
	active, err := srv.store.MenuAt(t.Context(), webbuilder.MenuLocationPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != ids[0] || active[0].Label != "Existing parent" ||
		len(active[0].Children) != 1 || active[0].Children[0].ID != ids[1] || active[0].Children[0].Label != "Existing child" {
		t.Fatalf("replacement changed the active menu: %+v", active)
	}
}
