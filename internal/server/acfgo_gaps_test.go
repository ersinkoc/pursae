package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestACFGoUnsupportedLocation(t *testing.T) {
	g := func(param string) *acfGroup {
		return &acfGroup{Location: [][]acfRule{{{Param: "post_type", Operator: "==", Value: "post"}}, {{Param: param, Operator: "==", Value: "all"}}}}
	}
	for _, p := range []string{"widget", "nav_menu", "nav_menu_item"} {
		if !acfgoUnsupportedLocation(g(p)) {
			t.Errorf("%s not flagged", p)
		}
	}
	if acfgoUnsupportedLocation(g("taxonomy")) {
		t.Error("taxonomy flagged")
	}
}

func TestACFGoRepeaterPageAttrs(t *testing.T) {
	if got := acfgoRepeaterPageAttrs(acfField{"pagination": 1, "rows_per_page": 5}); got != ` data-pagination="1" data-per-page="5"` {
		t.Errorf("attrs = %q", got)
	}
	if got := acfgoRepeaterPageAttrs(acfField{"pagination": 1}); !strings.Contains(got, `data-per-page="20"`) {
		t.Errorf("default per page: %q", got)
	}
	if acfgoRepeaterPageAttrs(acfField{}) != "" {
		t.Error("attrs without pagination")
	}
}

func TestACFGoIDSet(t *testing.T) {
	got := acfgoIDSet([]any{"3", float64(4), "x", ""})
	if len(got) != 2 || !got[3] || !got[4] {
		t.Errorf("acfgoIDSet = %v", got)
	}
}

// Integration: the Tools setting, the list badge, REST writes on terms, users, comments and media,
// bidirectional relationships, the ajax select and repeater pagination markup.
func TestACFGoGapFixes(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)

	// Empty cache: the second call is served from the cache (no reload).
	srv.acfgoFlush()
	srv.acfgoAllGroups(ctx, false)
	e := srv.acfgoCache()
	e.mu.Lock()
	loaded, groups := e.loaded, e.groups
	e.mu.Unlock()
	if !loaded || len(groups) != 0 {
		t.Fatalf("empty result not cached: loaded=%v groups=%d", loaded, len(groups))
	}

	// "Show admin menu" in ACF > Tools.
	_, body, _ := get(t, c, base+"/wp-admin/acf-tools")
	if !strings.Contains(body, `name="acfgo_show_admin"`) {
		t.Fatal("no Show admin menu setting on the Tools screen")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/acf-tools", url.Values{"_wpnonce": {formNonce(t, body)}, "tool": {"settings"}}); code != http.StatusFound {
		t.Fatalf("save settings: %d", code)
	}
	if srv.settings.String("acfgo_show_admin") != "0" {
		t.Errorf("acfgo_show_admin = %q after unchecking", srv.settings.String("acfgo_show_admin"))
	}
	_, body, _ = get(t, c, base+"/wp-admin/")
	if strings.Contains(body, `href="/wp-admin/acf-field-groups"`) {
		t.Error("ACF menu still shown")
	}
	_, body, _ = get(t, c, base+"/wp-admin/acf-tools")
	post(t, c, base+"/wp-admin/acf-tools", url.Values{"_wpnonce": {formNonce(t, body)}, "tool": {"settings"}, "acfgo_show_admin": {"1"}})
	if srv.settings.String("acfgo_show_admin") != "1" {
		t.Errorf("acfgo_show_admin = %q after checking", srv.settings.String("acfgo_show_admin"))
	}

	// A group on a menu item is listed with the badge.
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Menu icons", "location": []any{[]any{map[string]any{"param": "nav_menu_item", "operator": "==", "value": "all"}}},
		"fields": []any{map[string]any{"key": "field_icon", "label": "Icon", "name": "icon", "type": "text"}}})
	_, body, _ = get(t, c, base+"/wp-admin/acf-field-groups")
	if !strings.Contains(body, "Not available in Pursae") {
		t.Error("group with an unsupported location has no badge")
	}

	// REST writes: term, user, comment, media.
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "REST objects", "show_in_rest": 1, "location": []any{
		[]any{map[string]any{"param": "taxonomy", "operator": "==", "value": "category"}},
		[]any{map[string]any{"param": "user_form", "operator": "==", "value": "all"}},
		[]any{map[string]any{"param": "comment", "operator": "==", "value": "all"}},
		[]any{map[string]any{"param": "attachment", "operator": "==", "value": "all"}}},
		"fields": []any{map[string]any{"key": "field_note", "label": "Note", "name": "note", "type": "text", "required": 0, "maxlength": 10}}})
	term, _ := srv.store.TermBySlug(ctx, "category", "uncategorized")
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories/"+itoa64(term.ID), `{"acf":{"note":"term note"}}`); code != http.StatusOK || !strings.Contains(b, `"acf":{"note":"term note"}`) {
		t.Errorf("term write: %d %s", code, b)
	}
	if v := srv.acfGetField(ctx, "note", "term_"+itoa64(term.ID), true); v != "term note" {
		t.Errorf("term stored %v", v)
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories/"+itoa64(term.ID), `{"acf":{"note":"this is far too long"}}`); code != http.StatusBadRequest {
		t.Errorf("term validation: %d", code)
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories/"+itoa64(term.ID), `{"acf":{"nope":"x"}}`); code != http.StatusBadRequest {
		t.Errorf("term unknown field: %d", code)
	}
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/categories", `{"name":"Fresh","acf":{"note":"new term"}}`); code != http.StatusCreated || !strings.Contains(b, `"note":"new term"`) {
		t.Errorf("term create: %d %s", code, b)
	}

	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/users/me", `{"acf":{"note":"user note"}}`); code != http.StatusOK || !strings.Contains(b, `"note":"user note"`) {
		t.Errorf("user write: %d %s", code, b)
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/users/me", `{"acf":{"note":"this is far too long"}}`); code != http.StatusBadRequest {
		t.Errorf("user validation: %d", code)
	}
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/users", `{"username":"newbie","email":"newbie@example.test","password":"Str0ng-Pass-123!x","acf":{"note":"hi"}}`); code != http.StatusCreated || !strings.Contains(b, `"note":"hi"`) {
		t.Errorf("user create: %d %s", code, b)
	}

	p := &store.Post{Type: "post", Status: "publish", Title: "Hello", Slug: "hello", AuthorID: 1, CommentStatus: "open"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/comments", `{"post":`+itoa64(p.ID)+`,"content":"first","acf":{"note":"c note"}}`)
	var cm map[string]any
	_ = json.Unmarshal([]byte(b), &cm)
	if code != http.StatusCreated || !strings.Contains(b, `"note":"c note"`) {
		t.Fatalf("comment create: %d %s", code, b)
	}
	cid := itoa64(int64(cm["id"].(float64)))
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/comments/"+cid, `{"acf":{"note":"edited"}}`); code != http.StatusOK || !strings.Contains(b, `"note":"edited"`) {
		t.Errorf("comment update: %d %s", code, b)
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/comments/"+cid, `{"acf":{"note":"this is far too long"}}`); code != http.StatusBadRequest {
		t.Errorf("comment validation: %d", code)
	}
	if _, b := restGet(t, c, base, "/wp-json/wp/v2/comments/"+cid); !strings.Contains(b, `"note":"edited"`) {
		t.Errorf("comment read: %s", b)
	}

	a := &store.Attachment{UploaderID: 1, Slug: "logo", FilePath: "2026/11/logo.png", Mime: "image/png", Title: "Logo"}
	if err := srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/media/"+itoa64(a.ID), `{"acf":{"note":"m note"}}`); code != http.StatusOK || !strings.Contains(b, `"note":"m note"`) {
		t.Errorf("media write: %d %s", code, b)
	}
	if code, _ := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/media/"+itoa64(a.ID), `{"acf":{"note":"this is far too long"}}`); code != http.StatusBadRequest {
		t.Errorf("media validation: %d", code)
	}
	if _, b := restGet(t, c, base, "/wp-json/wp/v2/media/"+itoa64(a.ID)); !strings.Contains(b, `"note":"m note"`) {
		t.Errorf("media read: %s", b)
	}

	// Bidirectional relationship: post <-> post (relationship field mirrored into itself).
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Related", "show_in_rest": 1, "location": []any{[]any{map[string]any{"param": "post_type", "operator": "==", "value": "post"}}},
		"fields": []any{map[string]any{"key": "field_rel", "label": "Related", "name": "related", "type": "relationship", "post_type": []any{"post"}, "return_format": "id",
			"bidirectional": 1, "bidirectional_target": []any{"field_rel"}}}})
	b1 := &store.Post{Type: "post", Status: "publish", Title: "B1", Slug: "b1", AuthorID: 1}
	b2 := &store.Post{Type: "post", Status: "publish", Title: "B2", Slug: "b2", AuthorID: 1}
	for _, x := range []*store.Post{b1, b2} {
		if err := srv.store.InsertPost(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	if code, b := restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(p.ID), `{"acf":{"related":[`+itoa64(b1.ID)+`,`+itoa64(b2.ID)+`]}}`); code != http.StatusOK {
		t.Fatalf("relationship write: %d %s", code, b)
	}
	if v := srv.acfGetField(ctx, "related", b1.ID, false); !strings.Contains(strings.Join(acfgoStrList(v), ","), itoa64(p.ID)) {
		t.Errorf("B1 related = %v, want it to hold %d", v, p.ID)
	}
	restSend(t, c, base, http.MethodPost, "/wp-json/wp/v2/posts/"+itoa64(p.ID), `{"acf":{"related":[`+itoa64(b2.ID)+`]}}`)
	if v := srv.acfGetField(ctx, "related", b1.ID, false); strings.Contains(strings.Join(acfgoStrList(v), ","), itoa64(p.ID)) {
		t.Errorf("B1 related = %v, want %d removed", v, p.ID)
	}
	if v := srv.acfGetField(ctx, "related", b2.ID, false); !strings.Contains(strings.Join(acfgoStrList(v), ","), itoa64(p.ID)) {
		t.Errorf("B2 related = %v, want it to keep %d", v, p.ID)
	}

	// Ajax select and repeater pagination markup on the post screen.
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Lazy", "location": []any{[]any{map[string]any{"param": "post_type", "operator": "==", "value": "page"}}},
		"fields": []any{
			map[string]any{"key": "field_pick", "label": "Pick", "name": "pick", "type": "post_object", "post_type": []any{"post"}, "ajax": 1},
			map[string]any{"key": "field_rows", "label": "Rows", "name": "rows", "type": "repeater", "pagination": 1, "rows_per_page": 3,
				"sub_fields": []any{map[string]any{"key": "field_row_t", "label": "T", "name": "t", "type": "text"}}}}})
	pg := &store.Post{Type: "page", Status: "publish", Title: "Pg", Slug: "pg", AuthorID: 1, Meta: map[string]any{"pick": itoa64(b2.ID), "_pick": "field_pick"}}
	if err := srv.store.InsertPost(ctx, pg); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, c, base+"/wp-admin/post?post="+itoa64(pg.ID)+"&action=edit")
	if !strings.Contains(body, `data-ajax="1"`) || !strings.Contains(body, `acf-select-ajax-search`) {
		t.Error("ajax select markup missing")
	}
	if strings.Contains(body, `>B1</option>`) || !strings.Contains(body, `>B2</option>`) {
		t.Error("ajax select must render only the chosen value")
	}
	if !strings.Contains(body, `data-pagination="1" data-per-page="3"`) || !strings.Contains(body, "acf-repeater-pagination") {
		t.Error("repeater pagination markup missing")
	}
	_, body = acfgoAjaxGet(t, c, base, "acfgo/query_posts&field_key=field_pick&s=B1")
	if !strings.Contains(body, `"text":"B1"`) || strings.Contains(body, `"text":"B2"`) {
		t.Errorf("ajax search: %s", body)
	}
}

func acfgoAjaxGet(t *testing.T, c *http.Client, base, q string) (int, string) {
	t.Helper()
	code, body, _ := get(t, c, base+"/wp-admin/admin-ajax?action="+q)
	return code, body
}
