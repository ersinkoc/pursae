package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// A user without unfiltered_html who saves a page with an administrator's code and html sections and
// custom CSS keeps them: autosave and publish store the stored sections (at the positions the editor
// sends), reordering, hiding and removing work, edits of the privileged parts are ignored, and the
// published snapshot holds the stored code markup. The administrator's saves are unchanged.
func TestWBUnprivilegedKeepsCode(t *testing.T) {
	srv, base, admin := wbrStart(t)
	ctx := context.Background()
	author := wbrUser(t, srv, base, "keepau", "author")
	au, _ := srv.store.UserByLoginOrEmail(ctx, "keepau")
	post := wbrPost(t, srv, "post", "Keep Code", "draft", au.ID)
	doc, _ := wbrFixture(t, "initial")
	_, render := wbrFixture(t, "initial")
	style := func() map[string]any {
		return map[string]any{"palette": "light", "align": "left", "padding": 0, "titleSize": 40, "hideOn": []any{}}
	}
	codeSec := map[string]any{"id": "s_code1", "kind": "code", "name": "Kit", "hidden": false, "items": []any{}, "style": style(),
		"content": map[string]any{"html": `<section id="kit"><h2 data-wbc="1">Kit</h2><script>window.kit=1</script></section>`, "head": `<style>#kit{color:teal}</style>`}}
	raw := map[string]any{"id": "s_html1", "kind": "html", "name": "Raw", "hidden": false, "items": []any{}, "style": style(),
		"content": map[string]any{"html": `<p>raw</p><script>window.raw=1</script>`}}
	secs := doc["sections"].([]any)
	wbrObj(wbrObj(secs[1])["style"])["css"] = "#hero-x{color:red}"
	doc["sections"] = append([]any{secs[0], codeSec, secs[1], raw}, secs[2:]...)
	pub := wbrPublishBody(doc, render, 0, "publish", nil)
	pub["serverRender"] = true
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), pub, nil); code != http.StatusOK {
		t.Fatalf("admin publish: %d %v", code, v)
	}
	row, _ := srv.store.WebBuilder().Doc(ctx, post.ID)
	if !strings.Contains(row.HTML, "window.kit=1") || !row.SnapshotUnfiltered {
		t.Fatalf("admin snapshot lacks the code section: %.400s", row.HTML)
	}

	// The author edits the page as the editor loads it.
	_, v, _ := author.do(http.MethodGet, wbrDocPath(post.ID, ""), nil, nil)
	loaded := wbrObj(wbrObj(v)["doc"])
	if loaded == nil {
		t.Fatalf("document: %v", v)
	}
	b, _ := json.Marshal(loaded)
	edited := func(mut func(secs []any) []any) map[string]any {
		var d map[string]any
		_ = json.Unmarshal(b, &d)
		d["sections"] = mut(d["sections"].([]any))
		return d
	}
	find := func(secs []any, id string) map[string]any {
		for _, s := range secs {
			if wbrObj(s)["id"] == id {
				return wbrObj(s)
			}
		}
		return nil
	}
	order := func(raw json.RawMessage) string {
		var d struct {
			Sections []struct {
				ID     string `json:"id"`
				Hidden bool   `json:"hidden"`
			} `json:"sections"`
		}
		_ = json.Unmarshal(raw, &d)
		var ids []string
		for _, s := range d.Sections {
			id := s.ID
			if s.Hidden {
				id += "(hidden)"
			}
			ids = append(ids, id)
		}
		return strings.Join(ids, ",")
	}
	// Edits of the code, the html section and the custom CSS; the code section moved to the end and
	// the html section hidden.
	hacked := edited(func(secs []any) []any {
		c := find(secs, "s_code1")
		wbrObj(c["content"])["html"] = `<section id="kit"><h2 data-wbc="1">Hacked</h2><script>evil()</script></section>`
		r := find(secs, "s_html1")
		wbrObj(r["content"])["html"] = `<script>evil()</script>`
		r["hidden"] = true
		wbrObj(find(secs, "s_kbkx266")["style"])["css"] = "body{background:url(https://evil.test/x)}"
		var out []any
		for _, s := range secs {
			if wbrObj(s)["id"] != "s_code1" {
				out = append(out, s)
			}
		}
		return append(out, c)
	})
	code, v, _ := author.do(http.MethodPut, wbrDocPath(post.ID, "/autosave"), map[string]any{"doc": hacked, "baseRev": 1}, nil)
	if code != http.StatusOK {
		t.Fatalf("author autosave: %d %v", code, v)
	}
	dr, err := srv.store.WebBuilder().Draft(ctx, post.ID, au.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft := string(dr.Doc)
	if strings.Contains(draft, "evil") || !strings.Contains(draft, "window.kit=1") || !strings.Contains(draft, "window.raw=1") || !strings.Contains(draft, "#hero-x{color:red}") {
		t.Fatalf("autosave lost or took privileged parts: %s", draft)
	}
	if o := order(dr.Doc); !strings.HasPrefix(o, "s_5o1spjs,s_kbkx266,s_html1(hidden),") || !strings.HasSuffix(o, ",s_code1") {
		t.Errorf("autosave order: %s", o)
	}

	// Publish the same: the stored code markup is published, never the request's.
	if code, v, _ := author.do(http.MethodPost, wbrDocPath(post.ID, "/lock"), map[string]any{"take": true}, nil); code != http.StatusOK {
		t.Fatalf("author lock: %d %v", code, v)
	}
	code, v, _ = author.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), wbrPublishBody(hacked, render, 1, "publish", nil), nil)
	if code != http.StatusOK {
		t.Fatalf("author publish: %d %v", code, v)
	}
	if s := mustJSON(wbrObj(v)["stripped"]); !strings.Contains(s, webbuilder.StrippedHTMLSections) || !strings.Contains(s, webbuilder.StrippedCustomCSS) {
		t.Errorf("the ignored edits are not reported: %s", s)
	}
	row, _ = srv.store.WebBuilder().Doc(ctx, post.ID)
	if strings.Contains(string(row.Doc), "evil") || !strings.Contains(string(row.Doc), "window.kit=1") || !strings.Contains(string(row.Doc), "#hero-x{color:red}") {
		t.Fatalf("published doc: %s", row.Doc)
	}
	if o := order(row.Doc); !strings.HasSuffix(o, ",s_code1") || !strings.Contains(o, "s_html1(hidden)") {
		t.Errorf("published order: %s", o)
	}
	if strings.Contains(row.HTML, "evil") || !strings.Contains(row.HTML, "window.kit=1") || !strings.Contains(row.HTML, "#kit{color:teal}") {
		t.Errorf("published snapshot: %.600s", row.HTML)
	}
	if !strings.Contains(row.CSS, "red") || strings.Contains(row.CSS, "evil") {
		t.Errorf("published css: %s", row.CSS)
	}
	if !row.SnapshotUnfiltered || row.SnapshotUser != au.ID {
		t.Errorf("snapshot user %d unfiltered %v", row.SnapshotUser, row.SnapshotUnfiltered)
	}
	resp := anonGet(t, base+"/?p="+itoa64(post.ID))
	if loc := resp.Header.Get("Location"); loc != "" {
		resp = anonGet(t, loc)
	}
	if body := bodyOf(resp); !strings.Contains(body, "window.kit=1") || strings.Contains(body, "evil") {
		t.Errorf("published page lacks the code section")
	}

	// Removing the code section is allowed.
	removed := edited(func(secs []any) []any {
		var out []any
		for _, s := range secs {
			if wbrObj(s)["id"] != "s_code1" {
				out = append(out, s)
			}
		}
		return out
	})
	if code, v, _ := author.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), wbrPublishBody(removed, render, 2, "publish", nil), nil); code != http.StatusOK {
		t.Fatalf("author publish without the code section: %d %v", code, v)
	}
	row, _ = srv.store.WebBuilder().Doc(ctx, post.ID)
	if strings.Contains(string(row.Doc), "s_code1") || strings.Contains(row.HTML, "window.kit=1") || !strings.Contains(string(row.Doc), "window.raw=1") {
		t.Errorf("removal: %s", order(row.Doc))
	}

	// The administrator's edits are taken as sent.
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(post.ID, "/lock"), map[string]any{"take": true}, nil); code != http.StatusOK {
		t.Fatalf("admin lock: %d %v", code, v)
	}
	adminEdit := edited(func(secs []any) []any {
		wbrObj(find(secs, "s_code1")["content"])["html"] = `<section id="kit"><h2 data-wbc="1">New</h2><script>window.kit=2</script></section>`
		return secs
	})
	pub = wbrPublishBody(adminEdit, render, 3, "publish", nil)
	pub["serverRender"] = true
	if code, v, _ := admin.do(http.MethodPost, wbrDocPath(post.ID, "/publish"), pub, nil); code != http.StatusOK {
		t.Fatalf("admin publish: %d %v", code, v)
	}
	row, _ = srv.store.WebBuilder().Doc(ctx, post.ID)
	if !strings.Contains(string(row.Doc), "window.kit=2") || !strings.Contains(row.HTML, "window.kit=2") {
		t.Errorf("admin edit not published: %.300s", row.HTML)
	}

	// A revision restored by the author keeps its privileged parts in the draft.
	_, v, _ = author.do(http.MethodGet, wbrDocPath(post.ID, "/revisions"), nil, nil)
	revs, _ := v.([]any)
	var first string
	for _, r := range revs {
		if wbrObj(r)["rev"] == float64(1) {
			first = itoa64(int64(wbrObj(r)["id"].(float64)))
		}
	}
	if first == "" {
		t.Fatalf("revisions: %v", v)
	}
	if code, v, _ := author.do(http.MethodPost, wbrDocPath(post.ID, "/revisions/"+first+"/restore"), map[string]any{"baseRev": 4}, nil); code != http.StatusOK {
		t.Fatalf("restore: %d %v", code, v)
	}
	if dr, err := srv.store.WebBuilder().Draft(ctx, post.ID, au.ID); err != nil || !strings.Contains(string(dr.Doc), "window.kit=1") || !strings.Contains(string(dr.Doc), "window.raw=1") {
		t.Errorf("restored draft lacks the revision's privileged parts")
	}
}
