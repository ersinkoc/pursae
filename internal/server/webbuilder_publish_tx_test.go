package server

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A publish that fails after the post mapping rolls the post back with the doc: the post keeps its
// title and status, no core revision is left behind and the user's draft survives.
func TestWebBuilderRESTPublishAtomic(t *testing.T) {
	srv, _, admin := wbrStart(t)
	ctx := context.Background()
	page := wbrPost(t, srv, "page", "Old Title", "draft", 1)
	doc, render := wbrFixture(t, "initial")
	if code, v, _ := admin.do(http.MethodPut, wbrDocPath(page.ID, "/autosave"), map[string]any{"doc": doc, "page": map[string]any{"title": "Draft title"}, "baseRev": 0}, nil); code != http.StatusOK {
		t.Fatalf("autosave: %d %v", code, v)
	}
	var revsBefore int
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM post_revisions WHERE post_id = $1`, page.ID).Scan(&revsBefore); err != nil {
		t.Fatal(err)
	}
	// Make the last write of the publish (the web builder revision) fail.
	if _, err := srv.store.Pool.Exec(ctx, `CREATE FUNCTION wb_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'forced failure'; END $$;
		CREATE TRIGGER wb_test_fail BEFORE INSERT ON web_builder_revisions FOR EACH ROW EXECUTE FUNCTION wb_test_fail();`); err != nil {
		t.Fatal(err)
	}
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 0, "publish", nil), nil); code != http.StatusInternalServerError {
		t.Fatalf("publish with a failing revision insert: %d %v, want 500", code, v)
	}
	p, err := srv.store.GetPost(ctx, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Old Title" || p.Status != "draft" || p.Content != "" {
		t.Errorf("post after a failed publish: title %q status %q content %.60q, want it unchanged", p.Title, p.Status, p.Content)
	}
	var revsAfter, drafts int
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM post_revisions WHERE post_id = $1`, page.ID).Scan(&revsAfter); err != nil {
		t.Fatal(err)
	}
	if revsAfter != revsBefore {
		t.Errorf("post revisions %d → %d after a failed publish", revsBefore, revsAfter)
	}
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM web_builder_drafts WHERE post_id = $1`, page.ID).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 1 {
		t.Errorf("drafts after a failed publish = %d, want the draft kept", drafts)
	}
}

// Publish holds one pooled connection for its transaction and must not wait for a second one:
// on a one-connection pool it used to block forever in GetPost / UpdatePost.
func TestWebBuilderRESTPublishOneConnection(t *testing.T) {
	srv, _, admin := wbrStart(t)
	page := wbrPost(t, srv, "page", "Tiny Pool", "draft", 1)
	doc, render := wbrFixture(t, "initial")

	cfg, err := pgxpool.ParseConfig(os.Getenv("WPGO_TEST_DB"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	tiny, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := srv.store.Pool
	srv.store.Pool = tiny
	t.Cleanup(func() { srv.store.Pool = old; tiny.Close() })

	hc := *admin.c
	hc.Timeout = 20 * time.Second
	cl := *admin
	cl.c = &hc
	if code, v, _ := cl.do(http.MethodPost, wbrDocPath(page.ID, "/publish"), wbrPublishBody(doc, render, 0, "publish", nil), nil); code != http.StatusOK {
		t.Fatalf("publish on a one-connection pool: %d %v", code, v)
	}
}
