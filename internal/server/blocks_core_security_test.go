package server

import (
	"context"
	"strings"
	"testing"

	"wpgo/internal/blocks"
	"wpgo/internal/store"
)

func insertSyncedPattern(t *testing.T, srv *Server, slug string) *store.Post {
	t.Helper()
	p := &store.Post{Type: "wp_block", Status: "publish", Title: slug, Slug: slug, AuthorID: 1}
	if err := srv.store.InsertPost(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func setPostContent(t *testing.T, srv *Server, id int64, content string) {
	t.Helper()
	if _, err := srv.store.Pool.Exec(context.Background(), `UPDATE posts SET content = $1 WHERE id = $2`, content, id); err != nil {
		t.Fatal(err)
	}
}

func patternRef(id int64) string { return `<!-- wp:block {"ref":` + itoa64(id) + `} /-->` }

// Synced patterns that reference themselves (directly or through each other) render once, like
// WordPress' render_block_core_block() $seen_refs guard; a depth counter alone let one stored
// pattern with k self-references expand into 1+k+…+k^5 copies (and as many GetPost queries), and a
// chain of distinct patterns still multiplies, so the expansions per render are bounded too.
func TestBlocksSyncedPatternRecursionBounded(t *testing.T) {
	ts, c, srv := newTestServer(t, false)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	render := func(content string) string {
		return blocks.Render(withRender(ctx, &renderCtx{s: srv}), content)
	}

	self := insertSyncedPattern(t, srv, "loop")
	setPostContent(t, srv, self.ID, `<p>selfmarker</p>`+strings.Repeat(patternRef(self.ID), 3))
	if n := strings.Count(render(patternRef(self.ID)), "selfmarker"); n != 1 {
		t.Fatalf("self-referencing synced pattern rendered %d copies, want 1", n)
	}
	// The same pattern twice side by side is not a recursion: both render.
	if n := strings.Count(render(patternRef(self.ID)+patternRef(self.ID)), "selfmarker"); n != 2 {
		t.Fatalf("two sibling references rendered %d copies, want 2", n)
	}

	a, b := insertSyncedPattern(t, srv, "pa"), insertSyncedPattern(t, srv, "pb")
	setPostContent(t, srv, a.ID, `<p>amarker</p>`+strings.Repeat(patternRef(b.ID), 3))
	setPostContent(t, srv, b.ID, `<p>bmarker</p>`+strings.Repeat(patternRef(a.ID), 3))
	out := render(patternRef(a.ID))
	if na, nb := strings.Count(out, "amarker"), strings.Count(out, "bmarker"); na != 1 || nb != 3 {
		t.Fatalf("mutually referencing patterns rendered a=%d b=%d copies, want 1 and 3", na, nb)
	}

	// A chain of distinct patterns, ten references to the next one per level: 10^5 renders unbounded.
	next := int64(0)
	for i := 0; i < 6; i++ {
		p := insertSyncedPattern(t, srv, "chain"+itoa64(int64(i)))
		body := `<p>chainmarker</p>`
		if next != 0 {
			body += strings.Repeat(patternRef(next), 10)
		}
		setPostContent(t, srv, p.ID, body)
		next = p.ID
	}
	if n := strings.Count(render(patternRef(next)), "chainmarker"); n > maxNestedRenders {
		t.Fatalf("a chain of synced patterns expanded into %d copies (bound %d)", n, maxNestedRenders)
	}
}

// core/post-content renders a post that is already rendering as empty (WordPress' $seen_ids).
func TestBlocksPostContentSelfReference(t *testing.T) {
	ts, c, srv := newTestServer(t, false)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	p := &store.Post{Type: "post", Status: "publish", Title: "Self", Slug: "self", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	body := `<p>pcmarker</p>` + strings.Repeat(`<!-- wp:post-content /-->`, 3)
	setPostContent(t, srv, p.ID, body)
	out := blocks.Render(withRender(ctx, &renderCtx{s: srv, postID: p.ID}), `<!-- wp:post-content /-->`)
	if n := strings.Count(out, "pcmarker"); n != 1 {
		t.Fatalf("self-referencing post content rendered %d copies, want 1", n)
	}
}
