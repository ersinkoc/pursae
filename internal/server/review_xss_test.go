package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

// layoutXSS is the exploit from the 2026-11-08 review: a layout type that closes the class attribute
// and adds an event handler that fires without a click.
const layoutXSS = `x" autofocus tabindex="0" onfocus="alert(document.domain)`

func TestBlockLayoutTypeWhitelist(t *testing.T) {
	cases := []struct {
		attrs   string
		inherit bool
		want    string
	}{
		{`{"layout":{"type":"` + layoutXSS + `"}}`, true, "flow"},
		{`{"layout":{"type":"constrained"}}`, true, "constrained"},
		{`{"layout":{"type":"flex"}}`, false, "flex"},
		{`{"layout":{"type":"grid"}}`, false, "grid"},
		{`{"layout":{"type":"default"}}`, true, "flow"},
		{`{"layout":{"inherit":true}}`, true, "constrained"},
		{`{"layout":{"inherit":true}}`, false, "flow"},
		{`{}`, true, "flow"},
	}
	for _, tc := range cases {
		b := parseOneBlock(t, `<!-- wp:group `+tc.attrs+` --><div class="wp-block-group"></div><!-- /wp:group -->`)
		if got := blockLayoutType(b, tc.inherit); got != tc.want {
			t.Errorf("%s (inherit %v) = %q, want %q", tc.attrs, tc.inherit, got, tc.want)
		}
	}
}

func TestSocialLinkRefusesScriptURLs(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "JavaScript:alert(1)", " javascript:alert(1)", "java&#09;script:alert(1)", "data:text/html,x", "vbscript:x"} {
		raw, _ := json.Marshal(map[string]any{"url": u, "service": "chain"})
		got := renderSocialLink(nil, parseOneBlock(t, `<!-- wp:social-link `+string(raw)+` /-->`), false, false)
		// Either refused (empty href) or, without a scheme of its own, turned into an https:// URL.
		href := html.UnescapeString(regexp.MustCompile(`href="([^"]*)"`).FindStringSubmatch(got)[1])
		if href != "" && !strings.HasPrefix(href, "https://") {
			t.Errorf("%q: unsafe href kept: %s", u, got)
		}
	}
	got := renderSocialLink(nil, parseOneBlock(t, `<!-- wp:social-link {"url":"https://example.test/?a=1&b=2","service":"chain"} /-->`), false, false)
	if !strings.Contains(got, `href="https://example.test/?a=1&amp;b=2"`) {
		t.Errorf("https link: %s", got)
	}
}

func TestFilterFootnotesMeta(t *testing.T) {
	in := `[{"id":"Ab-1\" onmouseover=x","content":"Note <img src=x onerror=alert(1)><script>alert(2)</script><em>ok</em>"},{"id":"","content":"no id"},{"id":"b","content":""}]`
	var out []map[string]string
	if err := json.Unmarshal([]byte(filterFootnotesMeta(in)), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("notes = %v", out)
	}
	if out[0]["id"] != "ab-1onmouseoverx" {
		t.Errorf("id = %q", out[0]["id"])
	}
	if c := out[0]["content"]; strings.Contains(c, "onerror") || strings.Contains(c, "<script") || !strings.Contains(c, "<em>ok</em>") {
		t.Errorf("content = %q", c)
	}
	if got := filterFootnotesMeta("not json"); got != "" {
		t.Errorf("invalid JSON = %q", got)
	}
	if got := sanitizePostMeta(nil, "footnotes", in); strings.Contains(got, "onerror") {
		t.Errorf("sanitizePostMeta kept the handler: %s", got)
	}
	if got := sanitizePostMeta(nil, "other", "<b onclick=x>"); got != "<b onclick=x>" {
		t.Errorf("other keys are stored as written: %s", got)
	}
}

func TestPublicIPSpecialRanges(t *testing.T) {
	for _, s := range []string{
		"198.18.0.1", "198.19.255.255", "192.0.0.8", "240.0.0.1", "250.1.2.3", "192.0.2.1", "203.0.113.5",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::127.0.0.1", "64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::1",
		"2002:a9fe:a9fe::", "64:ff9b:1::1", "2001:0:4136:e378::1", "2001:db8::1", "fec0::1",
	} {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("%s must not be public", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "::ffff:8.8.8.8", "64:ff9b::808:808", "2002:808:808::1", "2606:4700:4700::1111", "198.20.0.1"} {
		if !publicIP(net.ParseIP(s)) {
			t.Errorf("%s should be public", s)
		}
	}
}

func TestOEmbedCacheIsBounded(t *testing.T) {
	c := newTTLCache[int](3)
	for i := 0; i < 5; i++ {
		c.Set(fmt.Sprint(i), i, time.Hour)
	}
	if c.Len() != 3 {
		t.Fatalf("len = %d", c.Len())
	}
	if _, ok := c.Get("0"); ok {
		t.Error("the oldest entry survived")
	}
	c.Get("2") // most recently used now
	c.Set("5", 5, time.Hour)
	if _, ok := c.Get("2"); !ok {
		t.Error("a recently used entry was evicted")
	}
	if _, ok := c.Get("3"); ok {
		t.Error("the least recently used entry survived")
	}
	c.Set("old", 1, -time.Second)
	if _, ok := c.Get("old"); ok {
		t.Error("an expired entry was returned")
	}
	if oembedCache.max != 1000 {
		t.Errorf("oEmbed cache bound = %d", oembedCache.max)
	}
}

// Block attributes, footnotes meta and stored excerpts reach the front end escaped or kses-filtered.
func TestFrontBlockAttrXSS(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	adminID := installFrontSite(t, base, c, srv)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	lb := srv.links(ctx)

	// An excerpt saved before excerpts were sanitized.
	mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "Legacy", Content: "<p>legacy body</p>",
		Excerpt: `<script>alert("excerpt")</script><em>kept</em><img src=x onerror=alert("excerpt2")>`})

	content := `<!-- wp:group {"layout":{"type":"` + layoutXSS + `"}} --><div class="wp-block-group"><p>g</p></div><!-- /wp:group -->
<!-- wp:columns {"layout":{"type":"` + layoutXSS + `"}} --><div class="wp-block-columns"><!-- wp:column {"layout":{"type":"` + layoutXSS + `"}} --><div class="wp-block-column"></div><!-- /wp:column --></div><!-- /wp:columns -->
<!-- wp:cover {"layout":{"type":"` + layoutXSS + `"}} --><div class="wp-block-cover"><div class="wp-block-cover__inner-container"><p>c</p></div></div><!-- /wp:cover -->
<!-- wp:social-links --><ul class="wp-block-social-links"><!-- wp:social-link {"url":"javascript:alert("social")","service":"chain"} /--></ul><!-- /wp:social-links -->
<!-- wp:latest-posts {"displayPostContent":true} /-->
<!-- wp:footnotes /-->`
	p := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Title: "Blocks", Content: content,
		Meta: map[string]any{"footnotes": `[{"id":"n1","content":"Note <img src=x onerror=alert(\"fn\")><script>alert(\"fn2\")</script><strong>ok</strong>"}]`}})
	code, body, _ := get(t, anon, base+pathOf(t, base, lb.Permalink(p)))
	if code != http.StatusOK {
		t.Fatalf("post: %d", code)
	}
	for _, bad := range []string{"onfocus", "autofocus", "alert(document.domain)", "javascript:", `alert("excerpt")`, `alert(&#34;excerpt`, "onerror", `alert("fn2")`} {
		if strings.Contains(body, bad) {
			t.Errorf("rendered post contains %q", bad)
		}
	}
	for _, want := range []string{`is-layout-flow wp-block-group-is-layout-flow`, `wp-block-cover__inner-container is-layout-flow wp-block-cover-is-layout-flow`,
		`<em>kept</em>`, `<strong>ok</strong>`, `<li id="n1">`} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered post misses %q", want)
		}
	}
	if t.Failed() {
		t.Logf("body: %s", body)
	}
	if _, home, _ := get(t, anon, base+"/"); strings.Contains(home, `alert("excerpt")`) || strings.Contains(home, "onerror") {
		t.Error("the home page prints the stored excerpt unfiltered")
	}
}

// Users without unfiltered_html get excerpts kses-filtered on save and comments limited to $allowedtags.
func TestExcerptAndCommentSanitizedOnSave(t *testing.T) {
	s := secStart(t)
	_, author := s.user("ann", "author")
	code, out, _, _ := author.do("POST", "/wp/v2/posts", map[string]any{"title": "Ann", "content": "body",
		"excerpt": `<script>alert(1)</script><b onclick="x()">bold</b>`, "status": "publish"})
	if code != http.StatusCreated {
		t.Fatalf("author post: %d %v", code, out)
	}
	id, _ := out["id"].(float64)
	p, err := s.srv.store.GetPost(context.Background(), int64(id))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Excerpt, "<script") || strings.Contains(p.Excerpt, "onclick") || !strings.Contains(p.Excerpt, "<b>bold</b>") {
		t.Errorf("stored excerpt = %q", p.Excerpt)
	}

	anon := asNewClient()
	comment := `<div class="screen-reader-text" style="position:fixed;inset:0">x</div><a href="javascript:alert(1)">j</a><a href="https://e.test" style="color:red">l</a><img src="https://e.test/i.png"><strong>s</strong>`
	post(t, anon, s.base+"/wp-comments-post", url.Values{"comment_post_ID": {fmt.Sprint(int(id))}, "author": {"Vis"}, "email": {"vis@example.test"}, "comment": {comment}})
	var stored string
	if err := s.srv.store.Pool.QueryRow(context.Background(), `SELECT content FROM comments WHERE post_id = $1 ORDER BY id DESC LIMIT 1`, int64(id)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<div", "style=", "class=", "javascript:", "<img"} {
		if strings.Contains(stored, bad) {
			t.Errorf("stored comment keeps %q: %s", bad, stored)
		}
	}
	if !strings.Contains(stored, "<strong>s</strong>") || !strings.Contains(stored, `href="https://e.test"`) {
		t.Errorf("stored comment lost allowed markup: %s", stored)
	}
}
