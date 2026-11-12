package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// TestFrontRenderRound2 covers the round-2 front-end fixes: escaped term names in the Categories
// dropdown, protected discussions kept out of the Latest Comments block, replytocom limited to
// approved comments, Atom titles without markup, noindex search results, paging dropped from single
// posts, comment permalinks with "newest" as the default page, and Memberships refusals honoured by
// the embed template, the oEmbed endpoint and the SEO head endpoint.
func TestFrontRenderRound2(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	adminID := installFrontSite(t, base, c, srv)
	ctx := context.Background()
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	lb := srv.links(ctx)

	// Categories block as a dropdown over tags: a tag any author may create carries markup.
	evil := &store.Term{Taxonomy: "post_tag", Name: `<img src=x onerror=alert(1)>`}
	if err := srv.store.InsertTerm(ctx, evil); err != nil {
		t.Fatal(err)
	}
	cats := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Slug: "tag-list", Title: "Tag list",
		Content: `<!-- wp:categories {"taxonomy":"post_tag","displayAsDropdown":true,"showEmpty":true} /-->`})
	_, body, _ := getPage(t, anon, lb.Permalink(cats))
	if strings.Contains(body, `<img src=x onerror=alert(1)>`) || !strings.Contains(body, `&lt;img src=x onerror=alert(1)&gt;</option>`) {
		t.Errorf("categories dropdown must escape term names: %s", body)
	}

	// Latest Comments: a comment on a password-protected post is not listed.
	locked := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Slug: "locked", Title: "Locked", Content: "<p>Secret</p>", Password: "pw", CommentStatus: "open"})
	open := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Slug: "open-post", Title: "Open post", Content: "<p>Hi</p>", CommentStatus: "open"})
	for _, cm := range []*store.Comment{
		{PostID: locked.ID, AuthorName: "Insider", AuthorEmail: "in@example.test", Content: "Protected discussion text", Status: "approved"},
		{PostID: open.ID, AuthorName: "Reader", AuthorEmail: "r@example.test", Content: "Public discussion text", Status: "approved"},
	} {
		if err := srv.store.InsertComment(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	latest := mustInsertPost(t, srv, &store.Post{Type: "page", AuthorID: adminID, Slug: "latest-comments", Title: "Latest comments",
		Content: `<!-- wp:latest-comments {"commentsToShow":5} /-->`})
	_, body, _ = getPage(t, anon, lb.Permalink(latest))
	if strings.Contains(body, "Protected discussion text") || !strings.Contains(body, "Public discussion text") {
		t.Errorf("latest comments must hide protected discussions: %s", body)
	}

	// replytocom: a pending comment's author is never named in the reply title.
	pending := &store.Comment{PostID: open.ID, AuthorName: "Held Back Author", AuthorEmail: "h@example.test", Content: "Waiting", Status: "pending"}
	if err := srv.store.InsertComment(ctx, pending); err != nil {
		t.Fatal(err)
	}
	link := lb.Permalink(open)
	_, body, _ = get(t, anon, link+previewSep(link)+"replytocom="+itoa64(pending.ID))
	if strings.Contains(body, "Held Back Author") || !strings.Contains(body, "name='comment_parent' id='comment_parent' value='0'") {
		t.Errorf("replytocom must ignore unapproved comments: %s", body)
	}

	// Atom: entry titles are text, never markup for the feed reader.
	mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Slug: "markup-title", Title: `Bold <script>alert(1)</script><b>move</b>`, Content: "<p>x</p>"})
	_, body, _ = get(t, anon, base+"/feed/atom/")
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, "<b>move</b>") || !strings.Contains(body, "<![CDATA[Bold move]]>") {
		t.Errorf("atom title: %s", body)
	}

	// Search results are noindex (wp_robots_noindex_search()).
	_, body, _ = get(t, anon, base+"/?s=open")
	if !strings.Contains(body, "<meta name='robots' content='noindex, follow, max-image-preview:large' />") {
		t.Errorf("search robots: %s", body)
	}

	// /<post>/page/2/ is the post: redirect_canonical() drops the paging of is_single().
	if lb.Pretty() {
		code, _, loc := get(t, anon, strings.TrimSuffix(link, "/")+"/page/2/")
		if code != http.StatusMovedPermanently || loc != link {
			t.Errorf("single paged: %d %s", code, loc)
		}
	}

	// Comment permalinks on page 1 carry comment-page-1 when "newest" is the default page.
	if err := srv.settings.Set(ctx, map[string]any{"page_comments": true, "comments_per_page": 1, "default_comments_page": "newest"}); err != nil {
		t.Fatal(err)
	}
	second := &store.Comment{PostID: open.ID, AuthorName: "Second", AuthorEmail: "s@example.test", Content: "Second text", Status: "approved"}
	if err := srv.store.InsertComment(ctx, second); err != nil {
		t.Fatal(err)
	}
	srv.purgeTags("post:" + itoa64(open.ID))
	if lb.Pretty() {
		page1 := strings.TrimSuffix(link, "/") + "/comment-page-1/"
		_, body, _ = get(t, anon, page1)
		if !strings.Contains(body, "Public discussion text") || !strings.Contains(body, `<a href="`+page1+`#comment-`) {
			t.Errorf("comment permalinks on page 1 must carry comment-page-1: %s", body)
		}
	}

	// wp_allow_comment(): the post's author is not held for moderation on their own post; others are.
	if err := srv.settings.Set(ctx, map[string]any{"comment_moderation": true}); err != nil {
		t.Fatal(err)
	}
	writerID := asCreateUser(t, srv, "writer", "author", asStrongPass)
	own := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: writerID, Slug: "writers-post", Title: "Writer's post", Content: "<p>x</p>", CommentStatus: "open"})
	jar, _ := cookiejar.New(nil)
	writer := &http.Client{Jar: jar, CheckRedirect: c.CheckRedirect}
	if code, _, _ := post(t, writer, base+"/wp-login", url.Values{"log": {"writer"}, "pwd": {asStrongPass}}); code != http.StatusFound {
		t.Fatalf("writer login: %d", code)
	}
	for _, target := range []*store.Post{own, open} {
		if code, _, _ := post(t, writer, base+"/wp-comments-post", url.Values{"comment_post_ID": {itoa64(target.ID)}, "comment": {"Reply by writer on " + target.Slug}}); code != http.StatusFound {
			t.Fatalf("writer comment: %d", code)
		}
		var status string
		if err := srv.store.Pool.QueryRow(ctx, `SELECT status FROM comments WHERE post_id = $1 AND user_id = $2`, target.ID, writerID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if want := map[bool]string{true: "approved", false: "pending"}[target.ID == own.ID]; status != want {
			t.Errorf("writer's comment on %s: %s, want %s", target.Slug, status, want)
		}
		if _, err := srv.store.Pool.Exec(ctx, `UPDATE comments SET created_at = created_at - interval '1 minute' WHERE user_id = $1`, writerID); err != nil {
			t.Fatal(err) // past the 15-second flood window
		}
	}

	// Memberships ("hide"): the embed template, oEmbed and the SEO head refuse what the page refuses.
	members := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: adminID, Slug: "members-only", Title: "Members only", Content: "<p>Members text</p>"})
	if err := srv.settings.Set(ctx, map[string]any{gooSetting: true, seogoSetting: true}); err != nil {
		t.Fatal(err)
	}
	saved := gooSingleAccessHooks
	gooSingleAccessHooks = append(append([]gooSingleAccessHook{}, saved...), func(_ *Server, _ *gin.Context, id int64, _ string) string {
		if id == members.ID {
			return "404"
		}
		return ""
	})
	t.Cleanup(func() { gooSingleAccessHooks = saved })
	mlink := lb.Permalink(members)
	if code, _, _ := get(t, anon, mlink); code != http.StatusNotFound {
		t.Fatalf("restricted post: %d", code)
	}
	if code, body, _ := get(t, anon, strings.TrimSuffix(mlink, "/")+"/embed/"); code != http.StatusNotFound || strings.Contains(body, "Members only") {
		t.Errorf("embed of a restricted post: %d %s", code, body)
	}
	if code, body, _ := get(t, anon, base+"/wp-json/oembed/1.0/embed?url="+url.QueryEscape(mlink)); code != http.StatusNotFound {
		t.Errorf("oembed of a restricted post: %d %s", code, body)
	}
	if _, body, _ := get(t, anon, base+"/wp-json/yoast/v1/get_head?url="+url.QueryEscape(mlink)); !strings.Contains(body, `"status":404`) || strings.Contains(body, "Members text") {
		t.Errorf("SEO head of a restricted post: %s", body)
	}
	if code, _, _ := get(t, anon, lb.Permalink(open)); code != http.StatusOK {
		t.Errorf("unrestricted post: %d", code)
	}
}

// TestRenderMenuEscapes: menu item classes and URLs are free text; they cannot leave their attributes
// or carry a javascript: link.
func TestRenderMenuEscapes(t *testing.T) {
	out := string(renderMenu([]*store.MenuItem{{ID: 1, Kind: "custom", Object: "custom", Label: "Home",
		URL: "javascript:alert(1)", Classes: `x"onmouseover="alert(1)`}}, "c", "m", "", ""))
	if strings.Contains(out, `"onmouseover="`) || strings.Contains(out, "javascript:") {
		t.Errorf("renderMenu: %s", out)
	}
	if got := titleRSS(`A &amp; <em>B</em>`); got != "A &amp;amp; B" {
		t.Errorf("titleRSS = %q", got)
	}
}
