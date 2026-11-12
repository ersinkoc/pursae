package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
)

// A future post whose time has come is published by the scheduler: status, term counts and the
// save side effects (onPostSaved) all follow.
func TestPublishDuePostsRunsSaveSideEffects(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installClassic(t, c, ts.URL)
	ctx := context.Background()
	term := &store.Term{Taxonomy: "category", Name: "Scheduled cat"}
	if err := srv.store.InsertTerm(ctx, term); err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(time.Hour)
	p := &store.Post{Type: "post", Title: "Due post", Slug: "due-post", Status: "future", PublishedAt: &soon, AuthorID: 1}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetPostTerms(ctx, p.ID, "category", []int64{term.ID}); err != nil {
		t.Fatal(err)
	}
	var saved []int64
	srv.onPostSaved = func(_ context.Context, sp *store.Post) { saved = append(saved, sp.ID) }
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE posts SET published_at = now() - interval '1 minute' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	srv.publishDuePosts(ctx)
	got, err := srv.store.GetPost(ctx, p.ID)
	if err != nil || got.Status != "publish" {
		t.Fatalf("status = %v (%v), want publish", got, err)
	}
	if len(saved) != 1 || saved[0] != p.ID {
		t.Errorf("onPostSaved calls = %v, want [%d]", saved, p.ID)
	}
	if tm, err := srv.store.GetTerm(ctx, term.ID); err != nil || tm.Count != 1 {
		t.Errorf("term count = %+v (%v), want 1", tm, err)
	}
}

// The comment form carries a honeypot and a signed timestamp; filling the honeypot, forging the
// timestamp or posting at once is refused, and a half-formed email address is not accepted.
func TestCommentSpamTrapsAndEmail(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installClassic(t, c, base)
	pool := getPool(t)
	ctx := context.Background()
	var id string
	_ = pool.QueryRow(ctx, `SELECT id::text FROM posts WHERE type = 'post' AND status = 'publish' ORDER BY id LIMIT 1`).Scan(&id)
	pid := int64(0)
	for _, r := range id {
		pid = pid*10 + int64(r-'0')
	}
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	resp, err := http.Get(base + "/?p=" + id)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	_, _ = io.Copy(&sb, resp.Body)
	resp.Body.Close()
	if body := sb.String(); !strings.Contains(body, `name="`+commentHoneypot+`"`) || !strings.Contains(body, `name="`+commentTimeField+`"`) {
		t.Error("comment form misses the honeypot or the timestamp")
	}
	form := func(extra url.Values, text string) url.Values {
		v := url.Values{"comment_post_ID": {id}, "comment": {text}, "author": {"V"}, "email": {"v@example.test"}}
		for k, vals := range extra {
			v[k] = vals
		}
		return v
	}
	count := func(text string) int {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM comments WHERE content = $1`, text).Scan(&n)
		return n
	}
	old := srv.commentTimeToken(pid, time.Now().Add(-time.Minute))
	fresh := srv.commentTimeToken(pid, time.Now())
	cases := []struct {
		name  string
		extra url.Values
		want  int
	}{
		{"honeypot", url.Values{commentHoneypot: {"http://spam"}, commentTimeField: {old}}, 0},
		{"forged", url.Values{commentTimeField: {"1.deadbeef"}}, 0},
		{"too fast", url.Values{commentTimeField: {fresh}}, 0},
		{"bad email", url.Values{commentTimeField: {old}, "email": {"a@"}}, 0},
		{"ok", url.Values{commentTimeField: {old}}, 1},
	}
	for _, tc := range cases {
		text := "trap " + tc.name
		post(t, anon, base+"/wp-comments-post", form(tc.extra, text))
		if got := count(text); got != tc.want {
			t.Errorf("%s: %d comments stored, want %d", tc.name, got, tc.want)
		}
	}
	// No timestamp (a custom form or a direct post): stored, but held for moderation even when the
	// site would approve it.
	_ = srv.settings.Set(ctx, map[string]any{"comment_moderation": false, "comment_previously_approved": false})
	backdate := func() { _, _ = pool.Exec(ctx, `UPDATE comments SET created_at = created_at - interval '2 hours'`) } // past the flood interval
	backdate()
	post(t, anon, base+"/wp-comments-post", form(nil, "trap no token"))
	backdate()
	post(t, anon, base+"/wp-comments-post", form(url.Values{commentTimeField: {old}}, "trap with token"))
	var held, approved string
	_ = pool.QueryRow(ctx, `SELECT status FROM comments WHERE content LIKE '%trap no token%'`).Scan(&held)
	_ = pool.QueryRow(ctx, `SELECT status FROM comments WHERE content LIKE '%trap with token%'`).Scan(&approved)
	if held != "pending" || approved != "approved" {
		t.Errorf("no-token comment %q, signed comment %q", held, approved)
	}
}
