package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wizUndoConfirm(t *testing.T, w *wizClient, id string) string {
	t.Helper()
	_, undo, _ := w.get(wbWizURL + "?action=undo&import=" + id)
	un := wizHiddenRE.FindStringSubmatch(undo)
	if un == nil {
		t.Fatalf("no undo nonce: %s", wbSnippet(undo, "notice"))
	}
	_, done, _ := w.postForm(wbWizURL+"?action=undo&import="+id, url.Values{"_wpnonce": {un[1]}, "confirm": {"1"}})
	return done
}

// A publish retried after its report failed to save sets the homepage a second time. The record
// must keep the site's original homepage, so Undo restores it rather than the deleted import page.
func TestWBImportWizardRepublishKeepsPreviousHomepage(t *testing.T) {
	srv, _, _, w := wizStart(t)
	ctx := context.Background()
	mine := wbrPost(t, srv, "page", "Mine", "publish", 1).ID
	if err := srv.settings.Set(ctx, map[string]any{"show_on_front": "page", "page_on_front": mine}); err != nil {
		t.Fatal(err)
	}
	_, _, loc := w.upload(wizSite(t), "site.zip", "w3")
	id := wizIDRE.FindStringSubmatch(loc)[1]
	_, analysis, _ := w.get(loc)
	at := wbWizURL + "?import=" + id
	code, _, result := w.postForm(at+"&action=import", url.Values{
		"_wpnonce": {wizHiddenRE.FindStringSubmatch(analysis)[1]}, "kit": {"w3"}, "status": {"draft"}, "front": {"1"}})
	if code != http.StatusSeeOther {
		t.Fatalf("draft import: %d", code)
	}
	_, report, _ := w.get(result)
	form := url.Values{"_wpnonce": {wizHiddenRE.FindStringSubmatch(report)[1]}, "front": {"1"}}
	metaPath := filepath.Join(srv.wbWizDir(id), "meta.json")
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if code, body, _ := w.postForm(at+"&action=publish", form); code != http.StatusOK || strings.Contains(body, "Publish the pages") {
		t.Fatalf("publish: %d %s", code, wbSnippet(body, "notice"))
	}
	importedFront := int64(srv.settings.Int("page_on_front"))
	if importedFront == mine || importedFront == 0 {
		t.Fatalf("homepage was not set by publish: %d", importedFront)
	}
	if rec := srv.wbWizRecords()[id]; rec == nil || rec.PrevFront != mine {
		t.Fatalf("after first publish PrevFront = %+v, want %d", rec, mine)
	}
	// The report save failed: meta.json still holds the unpublished draft report.
	if err := os.WriteFile(metaPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := w.postForm(at+"&action=publish", form); code != http.StatusOK {
		t.Fatalf("retry publish: %d %s", code, wbSnippet(body, "notice"))
	}
	if rec := srv.wbWizRecords()[id]; rec.PrevFront != mine || rec.PrevShow != "page" {
		t.Errorf("after retry PrevFront = %d (prevShow %q), want %d (page)", rec.PrevFront, rec.PrevShow, mine)
	}
	wizUndoConfirm(t, w, id)
	if got := int64(srv.settings.Int("page_on_front")); got != mine {
		t.Errorf("after undo page_on_front = %d (imported page %d was deleted), want %d", got, importedFront, mine)
	}
	if got := srv.settings.String("show_on_front"); got != "page" {
		t.Errorf("after undo show_on_front = %q, want page", got)
	}
}

// The owner moved the homepage (and the posts page) to other pages of the same import. Undo
// deletes them, so it must restore the previous reading settings instead of leaving them pointing
// at deleted pages.
func TestWBImportWizardUndoHomepageMovedWithinImport(t *testing.T) {
	srv, _, _, w := wizStart(t)
	ctx := context.Background()
	_, _, loc := w.upload(wizSite(t), "site.zip", "w4")
	id := wizIDRE.FindStringSubmatch(loc)[1]
	_, page, _ := w.get(loc)
	if code, _, _ := w.postForm(wbWizURL+"?action=import&import="+id, url.Values{
		"_wpnonce": {wizHiddenRE.FindStringSubmatch(page)[1]}, "kit": {"w4"}, "front": {"1"}, "status": {"publish"}}); code != http.StatusSeeOther {
		t.Fatalf("import: %d", code)
	}
	rec := srv.wbWizRecords()[id]
	if rec == nil || !rec.FrontSet || rec.PrevShow != "posts" {
		t.Fatalf("record: %+v", rec)
	}
	var others []int64
	for _, p := range rec.Pages {
		if p != rec.FrontPage {
			if post, err := srv.store.GetPost(ctx, p); err == nil && post.Status == "publish" {
				others = append(others, p)
			}
		}
	}
	if len(others) == 0 {
		t.Fatal("no other published imported page")
	}
	other := others[0]
	values := map[string]any{"show_on_front": "page", "page_on_front": other}
	var posts int64
	if len(others) > 1 {
		posts = others[1]
		values["page_for_posts"] = posts
	}
	if err := srv.settings.Set(ctx, values); err != nil {
		t.Fatal(err)
	}
	wizUndoConfirm(t, w, id)
	if _, err := srv.store.GetPost(ctx, other); err == nil {
		t.Fatalf("page %d was not deleted", other)
	}
	show, front := srv.settings.String("show_on_front"), srv.settings.Int("page_on_front")
	if show != "posts" || front != 0 {
		t.Errorf("after undo show_on_front=%q page_on_front=%d (deleted page %d), want posts and 0", show, front, other)
	}
	if posts > 0 {
		if got := int64(srv.settings.Int("page_for_posts")); got != 0 {
			t.Errorf("after undo page_for_posts = %d (deleted page %d), want 0", got, posts)
		}
	}
	if _, ok := srv.wbWizRecords()[id]; ok {
		t.Error("the import record was kept after undo")
	}
}
