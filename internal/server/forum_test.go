package server

import (
	"context"
	"html"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// forumParsePath maps every forum URL, with and without page/N, and refuses the rest.
func TestForumParsePath(t *testing.T) {
	ok := map[string]forumRoute{
		"forums":                         {View: forumViewIndex, Page: 1},
		"forums/f/general":               {View: forumViewForum, Slug: "general", Page: 1},
		"forums/f/general/page/3":        {View: forumViewForum, Slug: "general", Page: 3},
		"forums/f/general/new":           {View: forumViewNewTopic, Slug: "general", Page: 1},
		"forums/t/12":                    {View: forumViewTopic, ID: 12, Page: 1},
		"forums/t/12/hello-world":        {View: forumViewTopic, ID: 12, Slug: "hello-world", Page: 1},
		"forums/t/12/hello-world/page/2": {View: forumViewTopic, ID: 12, Slug: "hello-world", Page: 2},
		"forums/t/12/page/2":             {View: forumViewTopic, ID: 12, Page: 2},
		"forums/r/40":                    {View: forumViewReply, ID: 40, Page: 1},
		"forums/edit/40":                 {View: forumViewEdit, ID: 40, Page: 1},
		"forums/search":                  {View: forumViewSearch, Page: 1},
		"forums/search/page/4":           {View: forumViewSearch, Page: 4},
		"forums/u/mara":                  {View: forumViewProfile, Slug: "mara", Page: 1},
		"forums/u/mara.lind/page/2":      {View: forumViewProfile, Slug: "mara.lind", Page: 2},
		"forums/login":                   {View: forumViewLogin, Page: 1},
		"forums/register":                {View: forumViewRegister, Page: 1},
		"forums/verify":                  {View: forumViewVerify, Page: 1},
		"forums/account":                 {View: forumViewAccount, Page: 1},
		"forums/f/çay-ocağı":             {View: forumViewForum, Slug: "çay-ocağı", Page: 1},
	}
	for path, want := range ok {
		got, found := forumParsePath("forums", path)
		if !found || got != want {
			t.Errorf("%s: %+v %v, want %+v", path, got, found, want)
		}
	}
	for _, path := range []string{
		"", "forum", "forumsx", "forums-x/f/a", "blog/forums", "forums/page/2", "forums/f", "forums/f/Bad Slug", "forums/f/UPPER",
		"forums/f/a/b", "forums/f/a/new/page/2", "forums/t/abc", "forums/t/0", "forums/t/012", "forums/t/-1", "forums/t/1/a/b",
		"forums/r/1/page/2", "forums/edit/x", "forums/login/page/2", "forums/f/a/page/0", "forums/f/a/page/x", "forums/f/a/page/01",
		"forums/unknown", "forums/u", "forums/f//x", "forums/t/1/<script>",
	} {
		if got, found := forumParsePath("forums", path); found {
			t.Errorf("%q parsed as %+v", path, got)
		}
	}
	// Every slug forumSlugOf makes parses back, spacing combining marks of Indic scripts included.
	for _, title := range []string{"नमस्ते दुनिया", "বাংলা ফোরাম", "தமிழ் மன்றம்", "Çay ocağı", "ไทย ฟอรั่ม"} {
		slug := forumSlugOf(title)
		if rt, found := forumParsePath("forums", "forums/t/12/"+slug); !found || rt.Slug != slug {
			t.Errorf("topic slug %q of %q does not parse", slug, title)
		}
		if rt, found := forumParsePath("forums", "forums/f/"+slug); !found || rt.Slug != slug {
			t.Errorf("forum slug %q of %q does not parse", slug, title)
		}
	}
	// Another base: the old one is nobody's.
	if rt, found := forumParsePath("community", "community/t/5/x"); !found || rt.View != forumViewTopic || rt.ID != 5 {
		t.Errorf("base community: %+v %v", rt, found)
	}
	if _, found := forumParsePath("community", "forums/t/5/x"); found {
		t.Error("the default base still parses after a base change")
	}
}

// forumSanitize keeps the base a safe path segment and clamps the numbers and the default role.
func TestForumSanitize(t *testing.T) {
	st := forumSanitize(forumSettings{Base: " Community ", TopicsPerPage: 1, RepliesPerPage: 500, DefaultRole: "administrator", EditWindow: -5, FloodSeconds: 99999})
	if st.Base != "community" || st.TopicsPerPage != 5 || st.RepliesPerPage != 100 || st.DefaultRole != forumRoleParticipant ||
		st.EditWindow != 0 || st.FloodSeconds != 3600 || st.Title != "Forums" {
		t.Errorf("sanitize: %+v", st)
	}
	for _, bad := range []string{"wp-admin", "wp-anything", "wp-json", "feed", "forum-auth", "a/b", "../x", "with space", strings.Repeat("a", 41), ""} {
		if got := forumSanitize(forumSettings{Base: bad}).Base; got != "forums" {
			t.Errorf("base %q kept as %q", bad, got)
		}
	}
	if got := forumSanitize(forumSettings{DefaultRole: "subscriber"}).DefaultRole; got != "subscriber" {
		t.Errorf("subscriber role: %q", got)
	}
	if d := forumSanitize(forumSettings{}); d.TopicsPerPage != 20 || d.RepliesPerPage != 20 {
		t.Errorf("zero values take the defaults: %+v", d)
	}
}

// Content is sanitized on the way in and again on the way out.
func TestForumFormatClean(t *testing.T) {
	raw := `<p>Hi <a href="https://example.org">there</a></p><script>alert(1)</script><img src=x onerror=alert(1)>`
	member := &store.User{ID: 5}
	clean := forumClean(member, raw)
	if strings.Contains(clean, "<script") || strings.Contains(clean, "onerror") || !strings.Contains(clean, "there") {
		t.Errorf("forumClean: %s", clean)
	}
	out := string(forumFormat(raw))
	if strings.Contains(out, "<script") || strings.Contains(out, "onerror") || !strings.Contains(out, `rel="nofollow ugc"`) {
		t.Errorf("forumFormat: %s", out)
	}
	if got := forumClean(nil, "  plain  "); got != "plain" {
		t.Errorf("forumClean trims: %q", got)
	}
}

// Members without unfiltered_html can post code, quotes, lists and links, never active content.
func TestForumCleanMemberCode(t *testing.T) {
	member := &store.User{ID: 5}
	raw := "<p>Try this:</p>\n<pre><code class=\"language-go\">fmt.Println(&quot;hi&quot;)\nx := a &lt; b</code></pre>\n" +
		`<blockquote cite="https://example.org/q">Quoted <strong>bold</strong> <em>it</em></blockquote>` +
		`<ul><li>one</li><li>two</li></ul><ol start="3"><li>three</li></ol>` +
		`<a href="https://example.org/doc" onclick="steal()">docs</a> <a href="javascript:alert(1)">bad</a>` +
		`<code class="evil language-go">x</code><code class="language-<b>">y</code>` +
		`<script>alert(1)</script><style>p{}</style><iframe src="https://evil.example"></iframe>` +
		`<p style="color:red" id="x" class="hero" onmouseover="x()">styled</p><img src=x onerror=alert(1)><form><input name=a></form>`
	clean := forumClean(member, raw)
	for _, want := range []string{
		`<pre><code class="language-go">fmt.Println(&#34;hi&#34;)` + "\n" + `x := a &lt; b</code></pre>`,
		`<blockquote cite="https://example.org/q">Quoted <strong>bold</strong> <em>it</em></blockquote>`,
		`<ul><li>one</li><li>two</li></ul>`, `<ol start="3"><li>three</li></ol>`,
		`<a href="https://example.org/doc" rel="nofollow ugc">docs</a>`,
		`<code>x</code>`, `<code>y</code>`, `<p>styled</p>`,
	} {
		if !strings.Contains(clean, want) {
			t.Errorf("forumClean lost %s\n%s", want, clean)
		}
	}
	for _, bad := range []string{"<script", "alert(1)</script", "<style", "<iframe", "onclick", "onmouseover", "onerror", "javascript:",
		"style=", `id="x"`, "hero", "evil", "<img", "<form", "<input"} {
		if strings.Contains(clean, bad) {
			t.Errorf("forumClean kept %q\n%s", bad, clean)
		}
	}
	// Displayed again through forumFormat: the code block survives autop untouched.
	if out := string(forumFormat(clean)); !strings.Contains(out, `<pre><code class="language-go">`) || strings.Contains(out, "<pre><br") {
		t.Errorf("forumFormat code block: %s", out)
	}
	// Admins (unfiltered_html) keep the post tag set.
	admin := &store.User{ID: 1, Caps: map[string]bool{"unfiltered_html": true}}
	if got := forumClean(admin, `<figure class="wp-block-image">x</figure>`); !strings.Contains(got, "<figure") {
		t.Errorf("admin forumClean: %s", got)
	}
}

// The plugin's templates parse with the functions they get at run time, and every view the
// contract names exists.
func TestForumTemplatesParse(t *testing.T) {
	s := &Server{}
	tp := &templates{s: s}
	funcs := tp.coreFuncs(s)
	for k, f := range s.forumTemplateFuncs() {
		funcs[k] = f
	}
	set, err := parseTree(os.DirFS("../../web"), "forum/templates", funcs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index", "forum", "topic", "new-topic", "edit", "search", "profile", "login", "register", "verify",
		"account", "shortcode-index", "shortcode-recent", "part-head", "part-notice", "part-pagination", "part-userbar"} {
		if set.Lookup("forum/"+name) == nil {
			t.Errorf("forum/%s is missing", name)
		}
	}
	// Themes see the forum_* functions.
	all := tp.funcs()
	for _, fn := range []string{"forum_on", "forum_url", "forum_user_menu", "forum_recent_topics", "forum_stats", "forum_is_view"} {
		if all[fn] == nil {
			t.Errorf("theme functions lack %s", fn)
		}
	}
}

// forumActivateLink follows the Plugins screen's Activate link of Pursae Forum.
func forumActivateLink(t *testing.T, c *http.Client, base string) {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(body, "Pursae Forum") {
		t.Fatal("no Pursae Forum row on the Plugins screen")
	}
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=activate&amp;plugin=pursae-forum[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Activate link for Pursae Forum")
	}
	if code, _, loc := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound || !strings.Contains(loc, "activate=true") {
		t.Fatalf("activate Pursae Forum: %d %s", code, loc)
	}
}

// forumRenderTest renders a forum view through the synthetic page path without a front end.
func forumRenderTest(t *testing.T, srv *Server, view string) (int, string, http.Header) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/forums/", nil)
	p := srv.forumNewPage(c, view, "Forums")
	srv.forumRender(c, p, view, srv.forumURL(c.Request.Context()), 1)
	return w.Code, w.Body.String(), w.Header()
}

// Off: every forum URL is a 404. On (from the Plugins screen): the roles exist, the menu shows, the
// index placeholder answers 200 over HTTP, the other stub views 404, and the synthetic render path renders a view inside the theme.
func TestForumFoundation(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	base := ts.URL
	ctx := context.Background()
	anon := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for _, u := range []string{"/forums/", "/forum-assets/forum.css", "/forum-auth/google", "/wp-admin/forum"} {
		if code, _, _ := get(t, c, base+u); code != http.StatusNotFound {
			t.Errorf("%s while off: %d", u, code)
		}
	}
	if code, _, _ := post(t, anon, base+"/forums/", map[string][]string{"forum_action": {"login"}}); code != http.StatusNotFound {
		t.Errorf("forum POST while off: %d", code)
	}

	forumActivateLink(t, c, base)
	if !srv.forumOn() {
		t.Fatal("not on after the Activate link")
	}
	var roles int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE slug IN ('forum_participant','forum_moderator')`).Scan(&roles)
	if roles != 2 {
		t.Fatalf("forum roles: %d", roles)
	}
	var adminCaps, subCaps string
	_ = srv.store.Pool.QueryRow(ctx, `SELECT capabilities::text FROM roles WHERE slug = 'administrator'`).Scan(&adminCaps)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT capabilities::text FROM roles WHERE slug = 'subscriber'`).Scan(&subCaps)
	if !strings.Contains(adminCaps, `"forum_moderate": true`) || !strings.Contains(subCaps, `"forum_participate": true`) || strings.Contains(subCaps, "forum_moderate") {
		t.Errorf("role caps: admin %s / subscriber %s", adminCaps, subCaps)
	}
	if err := srv.forumActivate(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	if st := srv.forumLoad(); st.Base != "forums" || !st.Registration {
		t.Errorf("default settings: %+v", st)
	}
	if code, css, _ := get(t, anon, base+"/forum-assets/forum.css"); code != http.StatusOK || !strings.Contains(css, "--pf-accent") {
		t.Errorf("forum.css: %d", code)
	}
	_, dash, _ := get(t, c, base+"/wp-admin/")
	if !strings.Contains(dash, "toplevel_page_pursae-forum") || !strings.Contains(dash, "dashicons-format-chat") {
		t.Error("no Forums menu")
	}

	// The index placeholder over HTTP: 200 inside Twenty Twenty-Five with the forum body class.
	if code, body, _ := get(t, anon, base+"/forums/"); code != http.StatusOK || !strings.Contains(body, "pursae-forum pf-view-index") ||
		!strings.Contains(body, `data-view="index"`) || !strings.Contains(body, "wp-theme-twentytwentyfive") || strings.Contains(body, "[pursae_forum_render]") {
		t.Errorf("GET /forums/: %d", code)
	}
	// The login view is a real page: the form, its styles from forum.css, no template error.
	if code, body, _ := get(t, anon, base+"/forums/login/"); code != http.StatusOK || !strings.Contains(body, `data-view="login"`) ||
		!strings.Contains(body, `name="forum_action" value="login"`) ||
		strings.Contains(body, "Template error") || strings.Contains(body, "forum template error") || strings.Contains(body, "pf-auth-css") {
		t.Errorf("GET /forums/login/: %d", code)
	}
	// A missing topic and an unknown path: 404 inside the theme (the forum owns its base); unknown actions are 400.
	for _, u := range []string{"/forums/t/999999/x/", "/forums/nope/"} {
		if code, body, _ := get(t, anon, base+u); code != http.StatusNotFound || strings.Contains(body, "Template error") {
			t.Errorf("missing %s: %d", u, code)
		}
	}
	if code, _, _ := get(t, anon, base+"/forumsx/"); code != http.StatusNotFound {
		t.Errorf("/forumsx/: %d", code)
	}
	if code, _, _ := post(t, anon, base+"/forums/", map[string][]string{"forum_action": {"nope"}}); code != http.StatusBadRequest {
		t.Errorf("unknown action: %d", code)
	}
	if code, _, _ := post(t, anon, base+"/forums/", map[string][]string{"other": {"x"}}); code != http.StatusNotFound {
		t.Errorf("POST without forum_action: %d", code)
	}

	// The synthetic page: the placeholder body inside Twenty Twenty-Five, the forum canonical, no shortlink.
	code, body, hdr := forumRenderTest(t, srv, forumViewIndex)
	if code != http.StatusOK || !strings.Contains(body, `data-view="index"`) || !strings.Contains(body, "pursae-forum pf-view-index") {
		t.Fatalf("render: %d %s", code, body)
	}
	if !strings.Contains(body, `rel="canonical" href="`+srv.forumURL(ctx)+`"`) || strings.Contains(body, "page_id=0") || !strings.Contains(body, "forum.css?ver=") {
		t.Error("forum head: canonical, shortlink or stylesheet")
	}
	if strings.Contains(body, "oembed") {
		t.Error("forum head: oEmbed discovery links point at the synthetic page")
	}
	if !strings.Contains(hdr.Get("X-WPGo-Cache-Tags"), forumCacheTag) {
		t.Errorf("cache tags: %q", hdr.Get("X-WPGo-Cache-Tags"))
	}
	// A private view: no-store and noindex.
	_, body, hdr = forumRenderTest(t, srv, forumViewLogin)
	if hdr.Get("Cache-Control") != "no-store" || hdr.Get("X-Robots-Tag") != "noindex" || !strings.Contains(body, "noindex, follow") || strings.Contains(body, `rel="canonical"`) {
		t.Errorf("private view headers: %v", hdr)
	}
	// Inside Hello Pursae (blank canvas): the placeholder still renders.
	if err := srv.switchTheme(ctx, wbHelloSlug); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := forumRenderTest(t, srv, forumViewIndex); code != http.StatusOK || !strings.Contains(body, `data-view="index"`) || !strings.Contains(body, "wp-theme-"+wbHelloSlug) {
		t.Errorf("Hello Pursae render: %d", code)
	}

	// A theme override: the active theme's "forum/<view>" (templates/forum/<view>.html) wins; a broken
	// one falls back to the plugin's template.
	srv.cfg.WebDir = "" // keep the injected set (development mode reloads every set per request)
	set := template.Must(template.New("").Funcs(srv.tmpl.funcs()).New("forum/index").Parse(`<div class="pf my-override">{{.T "Forums"}} {{.Title}}</div>`))
	template.Must(set.New("forum/topic").Parse(`{{.Missing.Field}}`))
	srv.tmpl.mu.Lock()
	srv.tmpl.themes[wbHelloSlug] = set
	srv.tmpl.mu.Unlock()
	{
		w := httptest.NewRecorder()
		gc, _ := gin.CreateTestContext(w)
		gc.Request = httptest.NewRequest(http.MethodGet, "/forums/", nil)
		p := srv.forumNewPage(gc, forumViewIndex, "Board")
		if out := srv.forumExec(gc, "index", p); !strings.Contains(out, "my-override") || !strings.Contains(out, "Board") {
			t.Errorf("override not used: %s", out)
		}
		if out := srv.forumExec(gc, "topic", p); !strings.Contains(out, `data-view="topic"`) {
			t.Errorf("broken override did not fall back: %s", out)
		}
		if out := srv.forumExec(gc, "forum", p); !strings.Contains(out, `data-view="forum"`) {
			t.Errorf("plugin template: %s", out)
		}
	}

	// Deactivated: the assets go, the data and roles stay.
	if err := srv.forumDeactivate(ctx); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := get(t, anon, base+"/forum-assets/forum.css"); code != http.StatusNotFound {
		t.Errorf("forum.css after deactivation: %d", code)
	}
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE slug LIKE 'forum_%'`).Scan(&n)
	if n != 2 {
		t.Error("roles removed on deactivation")
	}
}

// The Google subject is unique across accounts.
func TestForumGoogleSubUnique(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	id2, err := srv.store.CreateUser(ctx, store.NewUser{Login: "mara", Email: "mara@example.test", PasswordHash: "x", Role: "subscriber"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, 1, map[string]any{forumUMetaGoogleSub: "g-123", forumUMetaGoogleEmail: "admin@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, id2, map[string]any{forumUMetaGoogleSub: "g-123"}); err == nil {
		t.Error("a second account took the same Google subject")
	}
	u, err := srv.forumUserByGoogleSub(ctx, "g-123")
	if err != nil || u.ID != 1 {
		t.Fatalf("by sub: %v %v", u, err)
	}
	if _, err := srv.forumUserByGoogleSub(ctx, "nobody"); err != errForumNotFound {
		t.Errorf("unknown sub: %v", err)
	}
	if err := srv.forumSetUserMeta(ctx, 1, nil, forumUMetaGoogleSub, forumUMetaGoogleEmail); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, id2, map[string]any{forumUMetaGoogleSub: "g-123"}); err != nil {
		t.Errorf("sub free again after unlink: %v", err)
	}
}

// The pager keeps the query, links neighbours and elides far pages.
func TestForumPagination(t *testing.T) {
	s := &Server{}
	if s.forumPagination(nil, "/forums/f/a/", 1, 1) != "" {
		t.Error("one page has a pager")
	}
	out := string(s.forumPagination(nil, "http://x.test/forums/search/?q=go", 5, 12))
	for _, want := range []string{`class="pf-pagination"`, `href="http://x.test/forums/search/page/4/?q=go"`, `rel="next" href="http://x.test/forums/search/page/6/?q=go"`,
		`href="http://x.test/forums/search/?q=go">1<`, `aria-current="page">5<`, `page/12/?q=go">12<`, "&hellip;"} {
		if !strings.Contains(out, want) {
			t.Errorf("pager lacks %s: %s", want, out)
		}
	}
	if strings.Contains(out, "page/9/") {
		t.Error("far page not elided")
	}
}
