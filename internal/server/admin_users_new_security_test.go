package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Add User (user-new.php) must apply the same role guard as the bulk role change, user-edit.php and
// REST (actorMayGiveRole): a user who holds create_users and promote_users but is not an
// administrator must not create an administrator. Integration test.
func TestUserNewRoleGuardNonAdmin(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installUsersSite(t, c, base)
	ctx := context.Background()
	_ = srv.settings.Set(ctx, map[string]any{"default_role": "subscriber"})
	hrID := asCreateUser(t, srv, "hrmgr", "subscriber", asStrongPass)
	if err := srv.store.SetUserCapExceptions(ctx, hrID, map[string]string{"create_users": "allow", "promote_users": "allow", "list_users": "allow"}); err != nil {
		t.Fatal(err)
	}
	hr := asNewClient()
	if code, _, _ := asLogin(t, hr, base, "/wp-login", "hrmgr", asStrongPass); code != http.StatusFound {
		t.Fatalf("hr login: %d", code)
	}
	code, page, _ := get(t, hr, base+"/wp-admin/user-new")
	if code != http.StatusOK || !strings.Contains(page, `name="user_login"`) {
		t.Fatalf("user-new as hr: %d", code)
	}
	form := url.Values{"_wpnonce": {formNonce(t, page)}, "action": {"createuser"}, "user_login": {"rogueadmin"}, "email": {"rogueadmin@example.test"},
		"pass1": {asStrongPass}, "role": {"administrator"}}
	if strings.Contains(page, `<option value="administrator"`) || strings.Contains(page, `<option value="editor"`) {
		t.Error("Add User role dropdown offers roles the actor may not give")
	}
	if !strings.Contains(page, `<option value="subscriber"`) {
		t.Error("Add User role dropdown misses the subscriber role the actor may give")
	}
	roleOf := func(login string) string {
		var r string
		_ = getPool(t).QueryRow(ctx, `SELECT r.slug FROM users u JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id WHERE u.login = $1`, login).Scan(&r)
		return r
	}
	code, body, _ := post(t, hr, base+"/wp-admin/user-new", form)
	if r := roleOf("rogueadmin"); r != "" {
		t.Errorf("a non-administrator with create_users+promote_users created a user with role %q via /wp-admin/user-new (status %d)", r, code)
	}
	if !strings.Contains(body, "not allowed to give users that role") {
		t.Errorf("refused role gave no error (status %d)", code)
	}
	// A role within the actor's own level still works.
	form.Set("role", "subscriber")
	form.Set("user_login", "plainsub")
	form.Set("email", "plainsub@example.test")
	if code, _, _ := post(t, hr, base+"/wp-admin/user-new", form); code != http.StatusFound || roleOf("plainsub") != "subscriber" {
		t.Errorf("create subscriber as hr: %d %q", code, roleOf("plainsub"))
	}
}
