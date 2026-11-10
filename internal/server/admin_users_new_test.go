package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Add User: without promote_users the posted role is ignored (edit_user()), and the role must exist.
// Integration test.
func TestUserNewRoleNeedsPromoteUsers(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installUsersSite(t, c, base)
	ctx := context.Background()
	_ = srv.settings.Set(ctx, map[string]any{"default_role": "subscriber"})
	hrID := asCreateUser(t, srv, "hr", "subscriber", asStrongPass)
	if err := srv.store.SetUserCapExceptions(ctx, hrID, map[string]string{"create_users": "allow", "list_users": "allow"}); err != nil {
		t.Fatal(err)
	}
	hr := asNewClient()
	if code, _, _ := asLogin(t, hr, base, "/wp-login", "hr", asStrongPass); code != http.StatusFound {
		t.Fatalf("hr login: %d", code)
	}
	code, page, _ := get(t, hr, base+"/wp-admin/user-new")
	if code != http.StatusOK || !strings.Contains(page, `name="user_login"`) {
		t.Fatalf("user-new as hr: %d", code)
	}
	if strings.Contains(page, `name="role"`) {
		t.Error("role field shown without promote_users")
	}
	roleOf := func(login string) string {
		var r string
		_ = getPool(t).QueryRow(ctx, `SELECT r.slug FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id WHERE u.login = $1`, login).Scan(&r)
		return r
	}
	form := url.Values{"_wpnonce": {formNonce(t, page)}, "action": {"createuser"}, "user_login": {"sneaky"}, "email": {"sneaky@example.test"},
		"pass1": {asStrongPass}, "role": {"editor"}}
	if code, _, loc := post(t, hr, base+"/wp-admin/user-new", form); code != http.StatusFound {
		t.Fatalf("create as hr: %d %s", code, loc)
	}
	if r := roleOf("sneaky"); r != "subscriber" {
		t.Errorf("hr created a user with role %q, want the default subscriber", r)
	}
	// The admin may choose a role, but only an existing one; the login must be valid.
	_, page, _ = get(t, c, base+"/wp-admin/user-new")
	form = url.Values{"_wpnonce": {formNonce(t, page)}, "action": {"createuser"}, "user_login": {"ghost"}, "email": {"ghost@example.test"},
		"pass1": {asStrongPass}, "role": {"no-such-role"}}
	if _, body, _ := post(t, c, base+"/wp-admin/user-new", form); !strings.Contains(body, "not allowed to give users that role") {
		t.Error("unknown role accepted")
	}
	form.Set("role", "editor")
	form.Set("user_login", "bad<login>")
	if _, body, _ := post(t, c, base+"/wp-admin/user-new", form); !strings.Contains(body, "illegal characters") {
		t.Error("invalid username accepted")
	}
	form.Set("user_login", "ghost")
	if code, _, _ := post(t, c, base+"/wp-admin/user-new", form); code != http.StatusFound || roleOf("ghost") != "editor" {
		t.Errorf("admin create editor: %d %q", code, roleOf("ghost"))
	}
}
