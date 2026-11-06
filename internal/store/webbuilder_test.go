package store

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/db"
)

func TestWebBuilderContentHash(t *testing.T) {
	// sha256("") and sha256("abc"), lower-case hex.
	if got := WebBuilderContentHash(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("empty: %s", got)
	}
	if got := WebBuilderContentHash("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("abc: %s", got)
	}
}

func TestRevConflictErrorIs(t *testing.T) {
	var err error = &RevConflictError{Rev: 3}
	if !errors.Is(err, ErrRevConflict) {
		t.Fatal("RevConflictError must match ErrRevConflict")
	}
	var rc *RevConflictError
	if !errors.As(err, &rc) || rc.Rev != 3 {
		t.Fatalf("errors.As: %+v", rc)
	}
}

// wbTestStore recreates the WPGO_TEST_DB database, migrates it and returns a store on it, a post
// and two users. Skipped without WPGO_TEST_ADMIN_DB / WPGO_TEST_DB.
func wbTestStore(t *testing.T) (*Store, int64, int64, int64) {
	t.Helper()
	adminURL, testURL := os.Getenv("WPGO_TEST_ADMIN_DB"), os.Getenv("WPGO_TEST_DB")
	if adminURL == "" || testURL == "" {
		t.Skip("WPGO_TEST_ADMIN_DB / WPGO_TEST_DB not set")
	}
	ctx := context.Background()
	u, err := url.Parse(testURL)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP DATABASE IF EXISTS ` + pgx.Identifier{name}.Sanitize() + ` WITH (FORCE)`,
		`CREATE DATABASE ` + pgx.Identifier{name}.Sanitize()} {
		if _, err := admin.Exec(ctx, q); err != nil {
			admin.Close(ctx)
			t.Fatal(err)
		}
	}
	admin.Close(ctx)
	pool, err := db.Connect(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := New(pool)
	u1, err := s.CreateUser(ctx, NewUser{Login: "wb-one", Email: "one@example.test", Role: "administrator"})
	if err != nil {
		t.Fatal(err)
	}
	u2, err := s.CreateUser(ctx, NewUser{Login: "wb-two", Email: "two@example.test", Role: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	p := &Post{Type: "page", AuthorID: u1, Title: "Home", Content: "<p>old</p>", Status: "draft"}
	if err := s.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	return s, p.ID, u1, u2
}

func jsonEq(t *testing.T, what string, got json.RawMessage, want string) {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatalf("%s: %v (%s)", what, err, got)
	}
	_ = json.Unmarshal([]byte(want), &b)
	ga, _ := json.Marshal(a)
	gb, _ := json.Marshal(b)
	if string(ga) != string(gb) {
		t.Errorf("%s = %s, want %s", what, ga, gb)
	}
}

func TestWebBuilderDocs(t *testing.T) {
	s, postID, u1, _ := wbTestStore(t)
	ctx := context.Background()
	wb := s.WebBuilder()

	if _, err := wb.Doc(ctx, postID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Doc before publish: %v", err)
	}
	if rev, err := wb.DocRev(ctx, postID); err != nil || rev != 0 {
		t.Fatalf("DocRev before publish = %d, %v", rev, err)
	}
	if err := wb.PublishDoc(ctx, &WebBuilderDoc{PostID: postID}, 0); err == nil {
		t.Fatal("a doc without JSON must be refused")
	}

	// A first publish names base rev 0; one that names another rev conflicts.
	if err := wb.PublishDoc(ctx, &WebBuilderDoc{PostID: postID, Doc: json.RawMessage(`{"v":0}`)}, 4); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("publish with base rev 4 on a new page: %v", err)
	}
	content := "<h1>Home</h1>"
	d := &WebBuilderDoc{PostID: postID, Doc: json.RawMessage(`{"sections":[],"v":1}`), HTML: "<section>1</section>",
		CSS: ".a{}", Features: []string{"motion"}, Renderer: 2, ContentHash: WebBuilderContentHash(content),
		SnapshotUser: u1, SnapshotUnfiltered: true}
	if err := wb.PublishDoc(ctx, d, 0); err != nil {
		t.Fatal(err)
	}
	if d.Rev != 1 || d.CreatedAt.IsZero() {
		t.Fatalf("after first publish: rev %d created %v", d.Rev, d.CreatedAt)
	}
	got, err := wb.Doc(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "doc", got.Doc, `{"sections":[],"v":1}`)
	jsonEq(t, "page default", got.Page, `{}`)
	jsonEq(t, "fonts default", got.Fonts, `[]`)
	if got.Rev != 1 || got.HTML != d.HTML || got.CSS != ".a{}" || got.Renderer != 2 || got.SnapshotUser != u1 ||
		!got.SnapshotUnfiltered || len(got.Features) != 1 || got.Features[0] != "motion" || got.Detached {
		t.Fatalf("stored doc: %+v", got)
	}

	// A second first publish (stale base rev 0) loses and learns the stored rev.
	err = wb.PublishDoc(ctx, &WebBuilderDoc{PostID: postID, Doc: json.RawMessage(`{"v":"stale"}`)}, 0)
	var rc *RevConflictError
	if !errors.As(err, &rc) || rc.Rev != 1 || rc.SnapshotUser != u1 || rc.UpdatedAt.IsZero() {
		t.Fatalf("stale first publish: %v %+v", err, rc)
	}

	// Drift: a matching outside save keeps the doc attached, a different one detaches it, and
	// putting the old content back does not reattach it.
	if det, err := wb.MarkDetachedIfDrifted(ctx, postID, content); err != nil || det {
		t.Fatalf("same content: detached=%v %v", det, err)
	}
	if det, err := wb.MarkDetachedIfDrifted(ctx, postID, "<p>edited</p>"); err != nil || !det {
		t.Fatalf("edited content: detached=%v %v", det, err)
	}
	if det, err := wb.MarkDetachedIfDrifted(ctx, postID, content); err != nil || !det {
		t.Fatalf("drift must stick until a reattaching publish: detached=%v %v", det, err)
	}
	if det, err := wb.MarkDetachedIfDrifted(ctx, postID+999, "x"); err != nil || det {
		t.Fatalf("post without doc: %v %v", det, err)
	}

	// Next publish from rev 1 succeeds, bumps the rev and clears detached; replaying it conflicts.
	d2 := &WebBuilderDoc{PostID: postID, Doc: json.RawMessage(`{"v":2}`), Page: json.RawMessage(`{"title":"Home"}`),
		Fonts: json.RawMessage(`[{"family":"Inter"}]`)}
	if err := wb.PublishDoc(ctx, d2, 1); err != nil {
		t.Fatal(err)
	}
	if d2.Rev != 2 {
		t.Fatalf("rev = %d, want 2", d2.Rev)
	}
	if err := wb.PublishDoc(ctx, &WebBuilderDoc{PostID: postID, Doc: json.RawMessage(`{"v":"replay"}`)}, 1); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("replayed publish: %v", err)
	}
	got, _ = wb.Doc(ctx, postID)
	jsonEq(t, "doc v2", got.Doc, `{"v":2}`)
	jsonEq(t, "page v2", got.Page, `{"title":"Home"}`)
	jsonEq(t, "fonts v2", got.Fonts, `[{"family":"Inter"}]`)
	if got.Detached || got.SnapshotUser != 0 || got.Rev != 2 {
		t.Fatalf("after reattaching publish: %+v", got)
	}

	if err := wb.SetDetached(ctx, postID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ = wb.Doc(ctx, postID); !got.Detached {
		t.Fatal("SetDetached(true) did not stick")
	}

	// Inside a transaction: lock, check, publish; a rollback leaves the stored doc alone.
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		txwb := WebBuilderIn(tx)
		cur, err := txwb.DocForUpdate(ctx, postID)
		if err != nil {
			return err
		}
		if err := txwb.PublishDoc(ctx, &WebBuilderDoc{PostID: postID, Doc: json.RawMessage(`{"v":"tx"}`)}, cur.Rev); err != nil {
			return err
		}
		return errors.New("roll back")
	})
	if err == nil || err.Error() != "roll back" {
		t.Fatalf("tx: %v", err)
	}
	if rev, _ := wb.DocRev(ctx, postID); rev != 2 {
		t.Fatalf("rolled back publish changed rev to %d", rev)
	}

	// Concurrent publishes from the same base rev: exactly one wins.
	var wg sync.WaitGroup
	results := make([]error, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = wb.PublishDoc(ctx, &WebBuilderDoc{PostID: postID, Doc: json.RawMessage(`{"v":"race"}`)}, 2)
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range results {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ErrRevConflict):
			t.Fatalf("race: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent publishes won, want 1", wins)
	}

	// Leaving the builder drops the doc and the drafts.
	if err := wb.SaveDraft(ctx, &WebBuilderDraft{PostID: postID, UserID: u1, Doc: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := wb.DeleteDoc(ctx, postID); err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Doc(ctx, postID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("doc after DeleteDoc: %v", err)
	}
	if _, err := wb.Draft(ctx, postID, u1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft after DeleteDoc: %v", err)
	}
	if err := wb.DeleteDoc(ctx, postID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second DeleteDoc: %v", err)
	}
}

func TestWebBuilderDrafts(t *testing.T) {
	s, postID, u1, u2 := wbTestStore(t)
	ctx := context.Background()
	wb := s.WebBuilder()

	if _, err := wb.Draft(ctx, postID, u1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no draft yet: %v", err)
	}
	if err := wb.SaveDraft(ctx, &WebBuilderDraft{PostID: postID, UserID: u1}); err == nil {
		t.Fatal("a draft without a doc must be refused")
	}
	d1 := &WebBuilderDraft{PostID: postID, UserID: u1, Doc: json.RawMessage(`{"who":1}`), BaseRev: 0}
	if err := wb.SaveDraft(ctx, d1); err != nil {
		t.Fatal(err)
	}
	first := d1.UpdatedAt
	d1.Doc, d1.Page, d1.BaseRev = json.RawMessage(`{"who":1,"n":2}`), json.RawMessage(`{"title":"T"}`), 3
	if err := wb.SaveDraft(ctx, d1); err != nil {
		t.Fatal(err)
	}
	if d1.UpdatedAt.Before(first) {
		t.Fatal("updated_at went back")
	}
	if err := wb.SaveDraft(ctx, &WebBuilderDraft{PostID: postID, UserID: u2, Doc: json.RawMessage(`{"who":2}`)}); err != nil {
		t.Fatal(err)
	}

	got, err := wb.Draft(ctx, postID, u1)
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "u1 doc", got.Doc, `{"who":1,"n":2}`)
	jsonEq(t, "u1 page", got.Page, `{"title":"T"}`)
	if got.BaseRev != 3 {
		t.Errorf("base rev %d", got.BaseRev)
	}
	got2, err := wb.Draft(ctx, postID, u2)
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "u2 doc", got2.Doc, `{"who":2}`)

	if err := wb.DeleteDraft(ctx, postID, u1); err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Draft(ctx, postID, u1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("u1 draft after delete: %v", err)
	}
	if _, err := wb.Draft(ctx, postID, u2); err != nil {
		t.Fatalf("u2 draft must survive u1's delete: %v", err)
	}
	if err := wb.DeleteDraft(ctx, postID, u1); err != nil {
		t.Fatalf("deleting a missing draft: %v", err)
	}

	// Deleting the post removes its drafts.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, postID); err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Draft(ctx, postID, u2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft after post delete: %v", err)
	}
}

func TestWebBuilderRevisions(t *testing.T) {
	s, postID, u1, _ := wbTestStore(t)
	ctx := context.Background()
	wb := s.WebBuilder()

	if id, err := wb.LatestPostRevisionID(ctx, postID); err != nil || id != 0 {
		t.Fatalf("no revisions yet: %d %v", id, err)
	}
	if err := wb.AddRevision(ctx, &WebBuilderRevision{RevisionID: 999999, PostID: postID, Doc: json.RawMessage(`{}`)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown core revision: %v", err)
	}

	// Each core update records a post_revisions row; link a doc to each.
	p, err := s.GetPost(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i, title := range []string{"One", "Two", "Three"} {
		p.Title, p.Content = title, "<p>"+title+"</p>"
		if err := s.UpdatePost(ctx, p, u1); err != nil {
			t.Fatal(err)
		}
		id, err := wb.LatestPostRevisionID(ctx, postID)
		if err != nil || id == 0 {
			t.Fatalf("latest revision: %d %v", id, err)
		}
		ids = append(ids, id)
		r := &WebBuilderRevision{RevisionID: id, PostID: postID, Rev: i + 1, Doc: json.RawMessage(`{"t":"` + title + `"}`), Renderer: 2}
		if err := wb.AddRevision(ctx, r); err != nil {
			t.Fatal(err)
		}
		if r.CreatedAt.IsZero() {
			t.Fatal("created_at not filled in")
		}
	}

	list, err := wb.Revisions(ctx, postID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Rev != 3 || list[0].Title != "Three" || list[0].AuthorID != u1 ||
		list[0].RevisionID != ids[2] || list[0].Renderer != 2 || list[0].Date.IsZero() || list[2].Rev != 1 {
		t.Fatalf("list: %+v", list)
	}
	page, err := wb.Revisions(ctx, postID, 1, 1)
	if err != nil || len(page) != 1 || page[0].Rev != 2 {
		t.Fatalf("paged list: %+v %v", page, err)
	}
	if n, err := wb.CountRevisions(ctx, postID); err != nil || n != 3 {
		t.Fatalf("count: %d %v", n, err)
	}

	r, err := wb.Revision(ctx, postID, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "rev 1 doc", r.Doc, `{"t":"One"}`)
	jsonEq(t, "rev 1 page", r.Page, `{}`)

	// A revision is only found through its own post.
	other := &Post{Type: "page", AuthorID: u1, Title: "Other", Status: "draft"}
	if err := s.InsertPost(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Revision(ctx, other.ID, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revision through another post: %v", err)
	}
	if err := wb.AddRevision(ctx, &WebBuilderRevision{RevisionID: ids[0], PostID: other.ID, Doc: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("linking another post's core revision must fail")
	}

	// Deleting the core revision removes the link.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM post_revisions WHERE id = $1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := wb.Revision(ctx, postID, ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revision after core delete: %v", err)
	}
}

func TestWebBuilderLibrary(t *testing.T) {
	s, _, u1, u2 := wbTestStore(t)
	ctx := context.Background()
	wb := s.WebBuilder()

	if items, err := wb.LibraryItems(ctx); err != nil || len(items) != 0 {
		t.Fatalf("empty library: %v %v", items, err)
	}
	if err := wb.AddLibraryItem(ctx, &WebBuilderLibraryItem{Name: "empty"}, 0); err == nil {
		t.Fatal("an item without data must be refused")
	}
	a := &WebBuilderLibraryItem{Name: "Hero", Data: json.RawMessage(`{"type":"hero"}`), AuthorID: u1}
	b := &WebBuilderLibraryItem{Name: "FAQ", Data: json.RawMessage(`{"type":"faq"}`), AuthorID: u2}
	for _, it := range []*WebBuilderLibraryItem{a, b} {
		if err := wb.AddLibraryItem(ctx, it, 2); err != nil {
			t.Fatal(err)
		}
		if it.ID == 0 || it.CreatedAt.IsZero() {
			t.Fatalf("not filled in: %+v", it)
		}
	}
	if err := wb.AddLibraryItem(ctx, &WebBuilderLibraryItem{Name: "Third", Data: json.RawMessage(`{}`)}, 2); !errors.Is(err, ErrLibraryFull) {
		t.Fatalf("third item with max 2: %v", err)
	}
	if n, err := wb.CountLibraryItems(ctx); err != nil || n != 2 {
		t.Fatalf("count: %d %v", n, err)
	}

	items, err := wb.LibraryItems(ctx)
	if err != nil || len(items) != 2 || items[0].ID != b.ID || items[1].ID != a.ID {
		t.Fatalf("list (newest first): %+v %v", items, err)
	}

	a.Name, a.Data = "Hero 2", json.RawMessage(`{"type":"hero","v":2}`)
	if err := wb.UpdateLibraryItem(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := wb.LibraryItem(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, "updated data", got.Data, `{"type":"hero","v":2}`)
	if got.Name != "Hero 2" || got.AuthorID != u1 {
		t.Fatalf("updated item: %+v", got)
	}
	if err := wb.UpdateLibraryItem(ctx, &WebBuilderLibraryItem{ID: 987654, Name: "x", Data: json.RawMessage(`{}`)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	// A deleted author leaves the item with author 0.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u2); err != nil {
		t.Fatal(err)
	}
	if got, err = wb.LibraryItem(ctx, b.ID); err != nil || got.AuthorID != 0 {
		t.Fatalf("item of deleted author: %+v %v", got, err)
	}

	if err := wb.DeleteLibraryItem(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := wb.LibraryItem(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := wb.DeleteLibraryItem(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}

	// Concurrent adds never pass the limit.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = wb.AddLibraryItem(ctx, &WebBuilderLibraryItem{Name: "c", Data: json.RawMessage(`{}`)}, 5)
		}()
	}
	wg.Wait()
	if n, _ := wb.CountLibraryItems(ctx); n != 5 {
		t.Fatalf("concurrent adds left %d items, want 5", n)
	}
}
