package server

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

func testGinCtx() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c
}

// TestACFGoSaveScreenBranches covers acfgoSaveScreen's user, comment and Add User branches
// (accepted, wrong nonce, unauthorised, refused by the core) plus the conditional-logic markup.
func TestACFGoSaveScreenBranches(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)
	ctx := context.Background()

	acfgoSaveGroupForm(t, c, base, 0, map[string]any{"title": "Screens", "show_in_rest": 1, "location": []any{
		[]any{map[string]any{"param": "user_form", "operator": "==", "value": "all"}},
		[]any{map[string]any{"param": "comment", "operator": "==", "value": "all"}}},
		"fields": []any{
			map[string]any{"key": "field_toggle", "label": "Toggle", "name": "toggle", "type": "true_false"},
			map[string]any{"key": "field_note", "label": "Note", "name": "note", "type": "text",
				"conditional_logic": []any{[]any{map[string]any{"field": "field_toggle", "operator": "==", "value": "1"}}}},
		}})

	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "target", Email: "target@example.test", PasswordHash: "x", Role: "author"})
	if err != nil {
		t.Fatal(err)
	}
	val := func(kind string, id int64) any { return srv.acfGetField(ctx, "note", kind+"_"+itoa64(id), true) }

	// Conditional logic reaches the form as data-conditions.
	code, body, _ := get(t, c, base+"/wp-admin/user-edit?user_id="+itoa64(uid))
	if code != http.StatusOK || !strings.Contains(body, `name="acf[field_note]"`) || !strings.Contains(body, `data-conditions="`) ||
		!strings.Contains(body, "field_toggle") {
		t.Fatalf("user-edit form without conditional logic markup: %d", code)
	}
	nonce := formNonce(t, body)

	// user-edit: a wrong nonce stores nothing, the right one does.
	form := func(n string) url.Values {
		return url.Values{"_wpnonce": {n}, "user_id": {itoa64(uid)}, "action": {"update"}, "email": {"target@example.test"},
			"acf[field_toggle]": {"1"}, "acf[field_note]": {"saved"}}
	}
	post(t, c, base+"/wp-admin/user-edit?user_id="+itoa64(uid), form("bogus"))
	if v := val("user", uid); v != nil && v != "" {
		t.Errorf("user-edit stored with a wrong nonce: %v", v)
	}
	post(t, c, base+"/wp-admin/user-edit?user_id="+itoa64(uid), form(nonce))
	if v := val("user", uid); v != "saved" {
		t.Errorf("user-edit value = %v", v)
	}

	// user-new: a refused add (duplicate login) leaves the existing user's fields alone; a real add saves them.
	_, body, _ = get(t, c, base+"/wp-admin/user-new")
	nn := formNonce(t, body)
	add := func(login, mail, note string) (int, string) {
		code, b, _ := post(t, c, base+"/wp-admin/user-new", url.Values{"_wpnonce": {nn}, "action": {"createuser"}, "user_login": {login},
			"email": {mail}, "pass1": {"Correct-horse-battery-9!"}, "pass2": {"Correct-horse-battery-9!"}, "role": {"author"},
			"acf[field_note]": {note}})
		return code, b
	}
	add("target", "other@example.test", "hijack")
	if v := val("user", uid); v != "saved" {
		t.Errorf("a refused Add User overwrote the existing user's field: %v", v)
	}
	if code, b := add("freshuser", "fresh@example.test", "fresh note"); code != http.StatusFound {
		t.Fatalf("Add User: %d %s", code, b)
	}
	var fid int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT id FROM users WHERE login = 'freshuser'`).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	if v := val("user", fid); v != "fresh note" {
		t.Errorf("Add User value = %v", v)
	}
	post(t, c, base+"/wp-admin/user-new", url.Values{"_wpnonce": {"bogus"}, "action": {"createuser"}, "user_login": {"nonce_bad"},
		"email": {"nb@example.test"}, "pass1": {"Correct-horse-battery-9!"}, "pass2": {"Correct-horse-battery-9!"}, "acf[field_note]": {"x"}})
	var n int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE login = 'nonce_bad'`).Scan(&n)
	if n != 0 {
		t.Error("Add User with a wrong nonce created a user")
	}

	// comment: wrong nonce stores nothing, the right one does.
	p := &store.Post{Type: "post", AuthorID: 1, Status: "publish", Title: "Commented", Content: "x"}
	if err := srv.store.InsertPost(ctx, p); err != nil {
		t.Fatal(err)
	}
	cm := &store.Comment{PostID: p.ID, AuthorName: "A", AuthorEmail: "a@example.test", Content: "hi", Status: "approved"}
	if err := srv.store.InsertComment(ctx, cm); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, c, base+"/wp-admin/comment?action=editcomment&c="+itoa64(cm.ID))
	cn := formNonce(t, body)
	cform := func(n string) url.Values {
		return url.Values{"_wpnonce": {n}, "action": {"editedcomment"}, "comment_ID": {itoa64(cm.ID)}, "newcomment_author": {"A"},
			"newcomment_author_email": {"a@example.test"}, "content": {"hi"}, "comment_status": {"approved"}, "acf[field_note]": {"cnote"}}
	}
	post(t, c, base+"/wp-admin/comment", cform("bogus"))
	if v := val("comment", cm.ID); v != nil && v != "" {
		t.Errorf("comment stored with a wrong nonce: %v", v)
	}
	post(t, c, base+"/wp-admin/comment", cform(cn))
	if v := val("comment", cm.ID); v != "cnote" {
		t.Errorf("comment value = %v", v)
	}

}

// TestACFGoLocalJSONSync writes a group's JSON on save and syncs an edited file back into the database.
func TestACFGoLocalJSONSync(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, base)
	ctx := context.Background()

	dir, _ := srv.acfgoJSONDirs()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	gid := acfgoSaveGroupForm(t, c, base, 0, acfgoTestGroup())
	g := srv.acfgoGroupByID(ctx, gid)
	if g == nil {
		t.Fatal("group not saved")
	}
	file := filepath.Join(dir, g.Key+".json")
	if b, err := os.ReadFile(file); err != nil || !strings.Contains(string(b), `"Product Details"`) {
		t.Fatalf("local JSON not written on save: %v", err)
	}

	// Edit the file on disk: the list offers Sync and syncing updates the database group.
	b, _ := os.ReadFile(file)
	edited := strings.Replace(string(b), `"Product Details"`, `"Synced Title"`, 1)
	var em map[string]any
	if err := json.Unmarshal([]byte(edited), &em); err != nil {
		t.Fatal(err)
	}
	em["modified"] = 4102444800 // far future: newer than the database copy
	eb, _ := json.Marshal(em)
	edited = string(eb)
	if err := os.WriteFile(file, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.acfgoFlush()
	_, body, _ := get(t, c, base+"/wp-admin/acf-field-groups?post_status=sync")
	m := regexp.MustCompile(`href="(/wp-admin/acf-field-groups\?action=sync[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no Sync link for a changed local JSON file")
	}
	link := html.UnescapeString(m[1])
	// A wrong nonce does not sync.
	bad := regexp.MustCompile(`_wpnonce=[^&]+`).ReplaceAllString(link, "_wpnonce=bogus")
	get(t, c, base+bad)
	if g2 := srv.acfgoGroupByID(ctx, gid); g2 == nil || g2.Title != "Product Details" {
		t.Fatalf("sync with a wrong nonce changed the group: %+v", g2)
	}
	get(t, c, base+link)
	if g2 := srv.acfgoGroupByID(ctx, gid); g2 == nil || g2.Title != "Synced Title" {
		t.Fatalf("sync did not update the group: %+v", g2)
	}
	if err := srv.acfgoSyncGroup(testGinCtx(), "group_missing"); err == nil {
		t.Error("syncing an unknown key succeeded")
	}
}
