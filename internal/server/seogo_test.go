package server

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// seogoToggle clicks SEO-Go's Activate or Deactivate link on the Plugins screen.
func seogoToggle(t *testing.T, c *http.Client, base, action string) string {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=seo-go[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for SEO-Go on the Plugins screen", action)
	}
	code, _, loc := get(t, c, base+html.UnescapeString(m[1]))
	if code != http.StatusFound {
		t.Fatalf("%s SEO-Go: %d", action, code)
	}
	return loc
}

// seogoForm posts an SEO-Go admin form with the screen's nonce.
func seogoForm(t *testing.T, c *http.Client, base, page string, form url.Values) (int, string) {
	t.Helper()
	q := ""
	if page != "" {
		q = "?page=" + page
	}
	_, body, _ := get(t, c, base+"/wp-admin/seogo"+q)
	m := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no nonce on SEO-Go screen %q", page)
	}
	form.Set("_wpnonce", m[1])
	code, _, loc := post(t, c, base+"/wp-admin/seogo"+q, form)
	return code, loc
}

func seogoJSONLD(t *testing.T, body string) []map[string]any {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/ld\+json" class="seogo-schema-graph">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no SEO-Go JSON-LD in the page")
	}
	var doc struct {
		Context string           `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(m[1]), &doc); err != nil {
		t.Fatalf("JSON-LD: %v\n%s", err, m[1])
	}
	if doc.Context != "https://schema.org" {
		t.Errorf("@context = %q", doc.Context)
	}
	return doc.Graph
}

func graphNode(graph []map[string]any, typ string) map[string]any {
	for _, n := range graph {
		switch v := n["@type"].(type) {
		case string:
			if v == typ {
				return n
			}
		case []any:
			for _, x := range v {
				if x == typ {
					return n
				}
			}
		}
	}
	return nil
}

func TestSEOGoOffByDefaultAndToggle(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	if srv.seogoOn() {
		t.Fatal("SEO-Go is on after a fresh install")
	}
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if row := regexp.MustCompile(`(?s)<tr class="(\w+)" data-slug="seo-go"`).FindStringSubmatch(body); row == nil || row[1] != "inactive" {
		t.Fatalf("SEO-Go is not listed as an inactive plugin: %v", row)
	}
	// Off: nothing of it anywhere.
	_, home, _ := get(t, c, base+"/")
	if strings.Contains(home, "SEO-Go") || strings.Contains(home, "seogo") || !strings.Contains(home, "<meta name='robots' content='max-image-preview:large' />") {
		t.Error("the front page changed while SEO-Go is off")
	}
	if code, _, _ := get(t, c, base+"/wp-sitemap.xml"); code != http.StatusOK {
		t.Errorf("core sitemap while off: %d", code)
	}
	if code, _, _ := get(t, c, base+"/sitemap_index.xml"); code != http.StatusNotFound {
		t.Errorf("sitemap_index.xml while off: %d", code)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/seogo"); code != http.StatusNotFound {
		t.Errorf("admin screen while off: %d", code)
	}
	_, dash, _ := get(t, c, base+"/wp-admin/")
	if strings.Contains(dash, "toplevel_page_seogo") {
		t.Error("SEO-Go menu while off")
	}
	if code, _ := restGet(t, c, base, "/wp-json/yoast/v1/get_head?url="+url.QueryEscape(base+"/")); code != http.StatusNotFound {
		t.Errorf("get_head while off: %d", code)
	}

	// On: the first activation opens the wizard; the menu appears.
	if loc := seogoToggle(t, c, base, "activate"); !strings.Contains(loc, "/wp-admin/seogo?page=wizard") {
		t.Errorf("activation went to %q, not the setup wizard", loc)
	}
	if !srv.seogoOn() || srv.seogoOpts().IndexNowKey == "" {
		t.Fatal("SEO-Go did not switch on")
	}
	code, dash, _ := get(t, c, base+"/wp-admin/seogo")
	if code != http.StatusOK || !strings.Contains(dash, `id="toplevel_page_seogo"`) || !strings.Contains(dash, "Modules") {
		t.Fatalf("dashboard: %d", code)
	}
	for _, page := range []string{"wizard", "general", "titles", "social", "schema", "sitemap", "redirections", "404", "links", "bulk", "tools", "roles"} {
		if code, body, _ := get(t, c, base+"/wp-admin/seogo?page="+page); code != http.StatusOK || strings.Contains(body, "Template error") {
			t.Errorf("screen %s: %d", page, code)
		}
	}
	_, home, _ = get(t, c, base+"/")
	if !strings.Contains(home, "<!-- This site is optimized with SEO-Go") || strings.Contains(home, "max-image-preview:large' />") {
		t.Error("the SEO-Go head block does not replace core's robots meta")
	}

	// Off again: the site is as before.
	seogoToggle(t, c, base, "deactivate")
	_, home, _ = get(t, c, base+"/")
	if strings.Contains(home, "SEO-Go") {
		t.Error("SEO-Go output after deactivation")
	}
	if code, _, _ := get(t, c, base+"/wp-admin/seogo"); code != http.StatusNotFound {
		t.Errorf("admin screen after deactivation: %d", code)
	}
}

func TestSEOGoHeadSchemaSitemapsRedirects(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	if code, loc := seogoForm(t, c, base, "wizard&step=1", url.Values{"step": {"1"}, "kg_type": {"organization"}, "kg_name": {"Acme Inc"}, "kg_logo": {base + "/logo.png"}, "separator": {"|"}}); code != http.StatusFound || !strings.Contains(loc, "step=2") {
		t.Fatalf("wizard step 1: %d %s", code, loc)
	}
	if o := srv.seogoOpts(); o.KGName != "Acme Inc" || o.Separator != "|" {
		t.Fatalf("wizard did not save: %+v", o.KGName)
	}

	// A post with SEO fields, saved through the REST API (as Gotenberg does).
	code, body := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"Golang SEO guide","status":"publish","slug":"golang-seo-guide",
		"content":"<!-- wp:paragraph --><p>Golang SEO is easy. Read <a href=\"/about/\">about us</a>.</p><!-- /wp:paragraph --><!-- wp:seogo/faq {\"questions\":[{\"id\":\"faq-question-1\",\"question\":\"Is it free?\",\"answer\":\"Yes.\"}]} --><div class=\"wp-block-seogo-faq seogo-faq\"><div class=\"seogo-faq-item\" id=\"faq-question-1\"><h3 class=\"seogo-faq-question\">Is it free?</h3><p class=\"seogo-faq-answer\">Yes.</p></div></div><!-- /wp:seogo/faq -->"}`)
	if code != http.StatusCreated {
		t.Fatalf("create post: %d %s", code, body)
	}
	var created struct {
		ID   int64  `json:"id"`
		Link string `json:"link"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	code, body = restSend(t, c, base, http.MethodPost, "/wp-json/seogo/v1/meta/"+itoa64(created.ID), `{"fields":{"focus":"golang seo","title":"%title% %sep% Best %sitename%","description":"Learn golang SEO in minutes.","og_image":"https://cdn.example.test/og.jpg","schema_custom":"{\"@type\":\"Event\",\"name\":\"Launch\"}"}}`)
	if code != http.StatusOK || !strings.Contains(body, `"seo_score"`) {
		t.Fatalf("save meta: %d %s", code, body)
	}

	_, page, _ := get(t, c, created.Link)
	for _, want := range []string{
		"<title>Golang SEO guide | Best Toggle Site</title>",
		`<meta name="description" content="Learn golang SEO in minutes." />`,
		`<meta name="robots" content="index, follow, max-snippet:-1, max-image-preview:large, max-video-preview:-1" />`,
		`<link rel="canonical" href="` + created.Link + `" />`,
		`<meta property="og:type" content="article" />`,
		`<meta property="og:image" content="https://cdn.example.test/og.jpg" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
		`<meta name="twitter:label1" content="Written by" />`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("head lacks %s", want)
		}
	}
	if strings.Count(page, `rel="canonical"`) != 1 {
		t.Error("canonical link printed twice")
	}
	graph := seogoJSONLD(t, page)
	org := graphNode(graph, "Organization")
	if org == nil || org["name"] != "Acme Inc" || org["@id"] != base+"/#organization" {
		t.Errorf("organization node: %v", org)
	}
	web := graphNode(graph, "WebPage")
	if web == nil || web["@id"] != created.Link || graphNode(graph, "FAQPage") == nil {
		t.Errorf("WebPage/FAQPage node: %v", web)
	}
	art := graphNode(graph, "Article")
	if art == nil || art["@id"] != created.Link+"#article" || art["headline"] != "Golang SEO guide" {
		t.Errorf("Article node: %v", art)
	}
	for _, typ := range []string{"WebSite", "Person", "BreadcrumbList", "Question", "Event"} {
		if graphNode(graph, typ) == nil {
			t.Errorf("graph lacks %s", typ)
		}
	}

	// Noindex a page: out of the sitemap, robots meta says so, no canonical.
	code, body = restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages", `{"title":"Thank you","status":"publish","content":"<p>Thanks!</p>"}`)
	var thanks struct {
		ID   int64  `json:"id"`
		Link string `json:"link"`
	}
	_ = json.Unmarshal([]byte(body), &thanks)
	restSend(t, c, base, http.MethodPost, "/wp-json/seogo/v1/meta/"+itoa64(thanks.ID), `{"fields":{"noindex":"noindex"}}`)
	_, page, _ = get(t, c, thanks.Link)
	if !strings.Contains(page, `<meta name="robots" content="noindex, follow" />`) || strings.Contains(page, `rel="canonical"`) {
		t.Error("noindex page head")
	}

	// Sitemaps replace core's.
	code, idx, _ := get(t, c, base+"/sitemap_index.xml")
	if code != http.StatusOK || !strings.Contains(idx, base+"/post-sitemap.xml") || !strings.Contains(idx, base+"/page-sitemap.xml") || !strings.Contains(idx, "main-sitemap.xsl") {
		t.Fatalf("sitemap index: %d %s", code, idx)
	}
	_, ps, _ := get(t, c, base+"/post-sitemap.xml")
	if !strings.Contains(ps, "<loc>"+created.Link+"</loc>") {
		t.Errorf("post sitemap lacks the post: %s", ps)
	}
	_, pg, _ := get(t, c, base+"/page-sitemap.xml")
	if strings.Contains(pg, thanks.Link) {
		t.Error("noindexed page is in the sitemap")
	}
	if code, _, loc := get(t, c, base+"/wp-sitemap.xml"); code != http.StatusMovedPermanently || !strings.HasSuffix(loc, "/sitemap_index.xml") {
		t.Errorf("wp-sitemap.xml: %d %s", code, loc)
	}
	if _, xsl, _ := get(t, c, base+"/main-sitemap.xsl"); !strings.Contains(xsl, "xsl:stylesheet") {
		t.Error("no sitemap stylesheet")
	}
	if _, robots, _ := get(t, c, base+"/robots.txt"); !strings.Contains(robots, "Sitemap: "+base+"/sitemap_index.xml") {
		t.Errorf("robots.txt: %s", robots)
	}
	seogoForm(t, c, base, "tools", url.Values{"action": {"robots"}, "robots_txt": {"User-agent: *\nDisallow: /private/"}})
	if _, robots, _ := get(t, c, base+"/robots.txt"); !strings.Contains(robots, "Disallow: /private/") {
		t.Errorf("custom robots.txt: %s", robots)
	}

	// Redirections: exact, regex with a back-reference, 410.
	for _, f := range []url.Values{
		{"action": {"save"}, "source": {"/old-guide/"}, "target": {created.Link}, "code": {"301"}, "match_type": {"exact"}},
		{"action": {"save"}, "source": {`^/blog/(\d+)/(.*)$`}, "target": {"/archive/$1/$2"}, "code": {"302"}, "match_type": {"regex"}},
		{"action": {"save"}, "source": {"/gone/"}, "code": {"410"}, "match_type": {"exact"}},
	} {
		if code, loc := seogoForm(t, c, base, "redirections", f); code != http.StatusFound || strings.Contains(loc, "seogo-error") {
			t.Fatalf("add redirection: %d %s", code, loc)
		}
	}
	if code, _, loc := get(t, c, base+"/old-guide/"); code != http.StatusMovedPermanently || loc != created.Link {
		t.Errorf("exact redirect: %d %s", code, loc)
	}
	if code, _, loc := get(t, c, base+"/blog/2020/hello"); code != http.StatusFound || loc != base+"/archive/2020/hello" {
		t.Errorf("regex redirect: %d %s", code, loc)
	}
	if code, _, _ := get(t, c, base+"/gone/"); code != http.StatusGone {
		t.Errorf("410: %d", code)
	}

	// Slug change: automatic redirect from the old URL.
	restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(created.ID), `{"slug":"go-seo-guide"}`)
	p, _ := srv.store.GetPost(t.Context(), created.ID)
	newLink := srv.links(t.Context()).Permalink(p)
	if code, _, loc := get(t, c, created.Link); code != http.StatusMovedPermanently || loc != newLink {
		t.Errorf("auto redirect after a slug change: %d %s (want %s)", code, loc, newLink)
	}
	rows, _ := srv.seogoRedirectRows(t.Context(), false)
	auto := false
	for _, r := range rows {
		auto = auto || r.Origin == "auto"
	}
	if !auto {
		t.Error("no automatic redirect row")
	}

	// 404 monitor logs in the background.
	get(t, c, base+"/does-not-exist-xyz/")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if rows := srv.seogo404Rows(t.Context(), "does-not-exist-xyz"); len(rows) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("404 not logged")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Analysis endpoint and the get_head endpoints.
	code, body = restSend(t, c, base, http.MethodPost, "/wp-json/seogo/v1/analyze", `{"post_id":`+itoa64(created.ID)+`,"title":"Golang SEO guide","content":"<p>Golang SEO is easy.</p>","fields":{"focus":"golang seo","title":"","description":""}}`)
	var an struct {
		Title string        `json:"title"`
		SEO   seogoAnalysis `json:"seo"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &an) != nil || an.Title != "Golang SEO guide | Toggle Site" || len(an.SEO.Results) == 0 {
		t.Errorf("analyze: %d %s", code, body)
	}
	code, body = restGet(t, c, base, "/wp-json/yoast/v1/get_head?url="+url.QueryEscape(newLink))
	var head struct {
		HTML   string         `json:"html"`
		JSON   map[string]any `json:"json"`
		Status int            `json:"status"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &head) != nil || head.Status != 200 || !strings.Contains(head.HTML, "<title>Golang SEO guide | Best Toggle Site</title>") || head.JSON["schema"] == nil {
		t.Errorf("yoast get_head: %d %.300s", code, body)
	}
	if code, body = restGet(t, c, base, "/wp-json/rankmath/v1/getHead?url="+url.QueryEscape(newLink)); code != http.StatusOK || !strings.Contains(body, `"success":true`) {
		t.Errorf("rank math getHead: %d", code)
	}

	// Classic editor: the box is on the edit screen and its fields are saved with the post.
	_ = srv.settings.Set(t.Context(), map[string]any{"classic-editor-replace": "classic"})
	_, edit, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(thanks.ID)+"&action=edit")
	if !strings.Contains(edit, `id="seogo-metabox"`) || !strings.Contains(edit, "seogo-editor.js") {
		t.Fatal("no SEO-Go box in the classic editor")
	}
	code, _, _ = post(t, c, base+"/wp-admin/post", url.Values{"_wpnonce": {formNonce(t, edit)}, "action": {"editpost"}, "visibility": {"public"}, "post_ID": {itoa64(thanks.ID)}, "post_type": {"page"},
		"post_title": {"Thank you"}, "content": {"<p>Thanks!</p>"}, "post_name": {"thank-you"}, "save": {"Update"}, "post_status": {"publish"},
		"seogo_present": {"1"}, "seogo[title]": {"Thanks %sep% %sitename%"}, "seogo[noindex]": {""}, "seogo[cornerstone]": {"1"}, "seogo[robots_adv][]": {"noarchive"}})
	if code != http.StatusFound {
		t.Fatalf("classic save: %d", code)
	}
	p, _ = srv.store.GetPost(t.Context(), thanks.ID)
	if seogoMeta(p.Meta, "title") != "Thanks %sep% %sitename%" || seogoMeta(p.Meta, "cornerstone") != "1" || seogoMeta(p.Meta, "noindex") != "" || seogoMeta(p.Meta, "robots_adv") != "noarchive" {
		t.Errorf("classic fields not saved: %v", p.Meta)
	}

	// Gotenberg: the sidebar script is loaded.
	_ = srv.settings.Set(t.Context(), map[string]any{"classic-editor-replace": "block"})
	if _, ed, _ := get(t, c, base+"/wp-admin/post?post="+itoa64(created.ID)+"&action=edit"); !strings.Contains(ed, "seogo-blocks.js") || !strings.Contains(ed, `"mode":"block"`) {
		t.Error("no SEO-Go sidebar in Gotenberg")
	}

	// Breadcrumb shortcode on a page.
	restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/pages/"+itoa64(thanks.ID), `{"content":"<p>[seogo_breadcrumb]</p>"}`)
	_, page, _ = get(t, c, thanks.Link)
	if !strings.Contains(page, `class="seogo-breadcrumb rank-math-breadcrumb"`) || !strings.Contains(page, "breadcrumb_last") {
		t.Error("breadcrumb shortcode not rendered")
	}
}

func TestSEOGoImportAndIndexNow(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")

	// A post imported from WordPress with Yoast and Rank Math meta.
	code, body := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"Imported","status":"publish","content":"<p>Hello</p>"}`)
	if code != http.StatusCreated {
		t.Fatal(body)
	}
	var created struct {
		ID   int64  `json:"id"`
		Link string `json:"link"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	meta := `{"_yoast_wpseo_title":"%%title%% %%sep%% Yoast","_yoast_wpseo_metadesc":"From Yoast","rank_math_robots":"a:1:{i:0;s:7:\"noindex\";}"}`
	if _, err := srv.store.Pool.Exec(t.Context(), `UPDATE posts SET meta = meta || $2::jsonb WHERE id = $1`, created.ID, meta); err != nil {
		t.Fatal(err)
	}
	_, page, _ := get(t, c, created.Link)
	if !strings.Contains(page, "<title>Imported - Yoast</title>") || !strings.Contains(page, `content="From Yoast"`) || !strings.Contains(page, `content="noindex, follow"`) {
		t.Errorf("Yoast/Rank Math meta not honoured")
	}
	if code, loc := seogoForm(t, c, base, "tools", url.Values{"action": {"import_meta"}}); code != http.StatusFound || !strings.Contains(loc, "imported") {
		t.Errorf("import: %d %s", code, loc)
	}
	p, _ := srv.store.GetPost(t.Context(), created.ID)
	if p.Meta["_seogo_title"] != "%title% %sep% Yoast" || p.Meta["_seogo_noindex"] != "noindex" {
		t.Errorf("import did not copy: %v", p.Meta)
	}

	// IndexNow: the module submits a published post to the endpoint with the key.
	var mu sync.Mutex
	var got map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(b, &got)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer fake.Close()
	old := seogoIndexNowEndpoint
	seogoIndexNowEndpoint = fake.URL
	defer func() { seogoIndexNowEndpoint = old }()
	seogoForm(t, c, base, "", url.Values{"module": {"indexnow"}, "state": {"on"}})
	key := srv.seogoOpts().IndexNowKey
	if _, kf, _ := get(t, c, base+"/"+key+".txt"); kf != key {
		t.Errorf("key file: %q", kf)
	}
	restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts", `{"title":"Fresh","status":"publish","content":"<p>New</p>"}`)
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		done := got != nil
		mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("IndexNow was not called")
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["key"] != key || !strings.Contains(strings.Join(func() []string {
		var out []string
		for _, u := range got["urlList"].([]any) {
			out = append(out, u.(string))
		}
		return out
	}(), " "), "fresh") {
		t.Errorf("IndexNow payload: %v", got)
	}
}
