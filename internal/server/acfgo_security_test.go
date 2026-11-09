package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Custom field values of an attachment are saved with the attachment screen's form: only for the
// uploader or users with edit_others_posts (attachmentSave's rule); an author could change the fields
// of anyone's media.
func TestACFGoAttachmentFieldsNeedOwnership(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Media credits", "position": "normal",
		"location": []any{[]any{map[string]any{"param": "attachment", "operator": "==", "value": "all"}}},
		"fields":   []any{map[string]any{"key": "field_credit", "label": "Credit", "name": "credit", "type": "text"}}})
	a := &store.Attachment{UploaderID: 1, Slug: "logo", FilePath: "2026/11/logo.png", Mime: "image/png", Title: "Logo"}
	if err := srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	asCreateUser(t, srv, "writer", "author", asStrongPass)
	ac := asNewClient()
	if code, _, _ := asLogin(t, ac, base, "/wp-login", "writer", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	send := func(cl *http.Client, value string) {
		t.Helper()
		_, body, _ := get(t, cl, base+"/wp-admin/post?post="+itoa64(a.ID)+"&action=edit")
		m := nonceRe.FindStringSubmatch(body)
		if m == nil {
			t.Fatal("no nonce on the attachment screen")
		}
		post(t, cl, base+"/wp-admin/post", url.Values{"_wpnonce": {m[1]}, "post_ID": {itoa64(a.ID)}, "post_type": {"attachment"}, "acf[field_credit]": {value}})
	}
	credit := func() string {
		got, err := srv.store.GetAttachment(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := got.Meta["credit"].(string)
		return v
	}
	send(c, "Studio")
	if credit() != "Studio" {
		t.Fatalf("administrator's own save: %q", credit())
	}
	send(ac, "Hijacked")
	if credit() == "Hijacked" {
		t.Fatal("an author changed custom fields of the administrator's attachment")
	}
}

// The relationship field's search matched titles and content of every post: a contributor could read
// other users' draft and private titles (and probe their content word by word).
func TestACFGoRelationshipSearchHidesUnreadablePosts(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)
	for _, p := range []*store.Post{
		{Type: "post", Title: "Zircon merger draft", Slug: "zircon-draft", Status: "draft", AuthorID: 1, Content: "zircon", Meta: map[string]any{}},
		{Type: "post", Title: "Zircon private memo", Slug: "zircon-private", Status: "private", AuthorID: 1, Content: "zircon", Meta: map[string]any{}},
		{Type: "post", Title: "Zircon public news", Slug: "zircon-public", Status: "publish", AuthorID: 1, Content: "zircon", Meta: map[string]any{}},
	} {
		if err := srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	asCreateUser(t, srv, "contrib", "contributor", asStrongPass)
	cc := asNewClient()
	if code, _, _ := asLogin(t, cc, base, "/wp-login", "contrib", asStrongPass); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	_, body, _ := get(t, cc, base+"/wp-admin/admin-ajax?action=acfgo/query_posts&s=zircon")
	if strings.Contains(body, "merger draft") || strings.Contains(body, "private memo") || !strings.Contains(body, "public news") {
		t.Errorf("contributor search: %s", body)
	}
	if _, body, _ := get(t, cc, base+"/wp-admin/admin-ajax?action=acfgo/query_posts&s=&post_type=wp_block"); strings.Contains(body, `"id"`) {
		t.Errorf("post type outside the field's types: %s", body)
	}
	if _, body, _ := get(t, c, base+"/wp-admin/admin-ajax?action=acfgo/query_posts&s=zircon"); !strings.Contains(body, "merger draft") {
		t.Errorf("administrator search lost drafts: %s", body)
	}
}

// ?acf_format=standard is open to anyone: related posts the requester cannot read and authors' e-mail
// addresses must not be expanded into a public REST response (get_field in a theme is unchanged).
func TestACFGoRESTStandardFormatHidesPrivateData(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	ctx := context.Background()
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)
	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Links", "show_in_rest": 1,
		"location": []any{[]any{map[string]any{"param": "post_type", "operator": "==", "value": "post"}}},
		"fields": []any{
			map[string]any{"key": "field_rel", "label": "Related", "name": "related", "type": "relationship", "return_format": "object"},
			map[string]any{"key": "field_owner", "label": "Owner", "name": "owner", "type": "user", "return_format": "array"},
		}})
	draft := &store.Post{Type: "post", Title: "Secret draft", Slug: "secret-draft", Status: "draft", AuthorID: 1, Content: "draft-body-xyz", Meta: map[string]any{}}
	locked := &store.Post{Type: "post", Title: "Locked", Slug: "locked", Status: "publish", AuthorID: 1, Content: "locked-body-xyz", Password: "pw", Meta: map[string]any{}}
	pub := &store.Post{Type: "post", Title: "Host", Slug: "host", Status: "publish", AuthorID: 1, Content: "host", Meta: map[string]any{}}
	for _, p := range []*store.Post{draft, locked, pub} {
		if err := srv.store.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.acfUpdateField(ctx, "related", []any{itoa64(draft.ID), itoa64(locked.ID)}, pub.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.acfUpdateField(ctx, "owner", "1", pub.ID); err != nil {
		t.Fatal(err)
	}
	admin, _ := srv.store.UserByID(ctx, 1)
	body := bodyOf(anonGet(t, base+"/wp-json/wp/v2/posts/"+itoa64(pub.ID)+"?acf_format=standard"))
	if !strings.Contains(body, `"acf"`) || !strings.Contains(body, `"Locked"`) {
		t.Fatalf("anonymous standard format: %s", body)
	}
	for _, leak := range []string{"Secret draft", "draft-body-xyz", "locked-body-xyz", admin.Email} {
		if strings.Contains(body, leak) {
			t.Errorf("anonymous standard format leaks %q", leak)
		}
	}
	// The administrator still gets everything.
	if _, body := restGet(t, c, base, "/wp-json/wp/v2/posts/"+itoa64(pub.ID)+"?acf_format=standard"); !strings.Contains(body, "draft-body-xyz") || !strings.Contains(body, admin.Email) {
		t.Errorf("administrator standard format lost data: %s", body)
	}
	// Theme output keeps ACF's get_field behaviour.
	if v, _ := srv.acfGetField(ctx, "related", pub.ID, true).([]any); len(v) != 2 {
		t.Errorf("get_field related = %v", v)
	}
}
