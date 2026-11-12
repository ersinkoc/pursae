package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// A forged comment_author_email cookie must not reveal another person's pending comment;
// the real author still sees theirs through the signed pending-comments cookie.
func TestPendingCommentPreviewNeedsSignedCookie(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installFrontSite(t, base, c, srv)
	if err := srv.settings.Set(ctx, map[string]any{"comment_moderation": true}); err != nil {
		t.Fatal(err)
	}
	lb := srv.links(ctx)
	hello, _ := srv.store.PostBySlug(ctx, "post", "hello-world")
	helloURL := base + pathOf(t, base, lb.Permalink(hello))

	victim := &store.Comment{PostID: hello.ID, AuthorName: "Victim", AuthorEmail: "victim@example.test", Content: "Victim secret pending text", Status: "pending", AuthorIP: "10.9.9.9"}
	if err := srv.store.InsertComment(ctx, victim); err != nil {
		t.Fatal(err)
	}
	srv.purgeTags("post:" + itoa64(hello.ID))

	req, _ := http.NewRequest("GET", helloURL, nil)
	req.AddCookie(&http.Cookie{Name: "comment_author_email", Value: url.QueryEscape("victim@example.test")})
	req.AddCookie(&http.Cookie{Name: pendingCommentsCookie, Value: itoa64(victim.ID) + ".deadbeef"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Victim secret pending text") {
		t.Fatal("forged comment_author_email cookie revealed another person's pending comment")
	}

	jar, _ := cookiejar.New(nil)
	author := &http.Client{Jar: jar, CheckRedirect: c.CheckRedirect}
	code, _, loc := post(t, author, base+"/wp-comments-post", url.Values{"comment_post_ID": {itoa64(hello.ID)}, "comment": {"My own held comment"}, "author": {"Me"}, "email": {"me@example.test"}})
	if code != http.StatusFound {
		t.Fatalf("submit: %d %s", code, loc)
	}
	_, page, _ := get(t, author, helloURL)
	if !strings.Contains(page, "My own held comment") {
		t.Error("author no longer sees their own pending comment")
	}
	if strings.Contains(page, "Victim secret pending text") {
		t.Error("author sees someone else's pending comment")
	}
}
