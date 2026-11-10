package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Pursae Security and Pursae Commerce together: the My Account register, login (with the second
// factor) and password-change forms follow the same policy as the core screens.
func TestGooAdminSecAccountFlows(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	asToggle(t, c, base, "activate")
	if !srv.adminsecOn() {
		t.Fatal("Pursae Security not active")
	}
	_ = srv.gooSetOpts(ctx, map[string]any{"enable_myaccount_registration": true, "registration_generate_username": false, "registration_generate_password": false})
	v := gooFrontClient()

	// Registration with a weak password is refused and creates no account.
	_, body, _ := get(t, v, base+"/my-account/")
	post(t, v, base+"/my-account/", url.Values{"email": {"weak@example.test"}, "username": {"weakling"}, "password": {"monkey"},
		"woocommerce-register-nonce": {gooHidden(t, body, "woocommerce-register-nonce")}, "register": {"Register"}})
	if n := asCount(t, `SELECT count(*) FROM users WHERE login = 'weakling'`); n != 0 {
		t.Error("weak password accepted at My Account registration")
	}
	// A strong one works.
	_, body, _ = get(t, v, base+"/my-account/")
	post(t, v, base+"/my-account/", url.Values{"email": {"strong@example.test"}, "username": {"strongone"}, "password": {asStrongPass},
		"woocommerce-register-nonce": {gooHidden(t, body, "woocommerce-register-nonce")}, "register": {"Register"}})
	if n := asCount(t, `SELECT count(*) FROM users WHERE login = 'strongone'`); n != 1 {
		t.Error("strong password refused at My Account registration")
	}

	// Password change from Account details follows the policy.
	_, body, _ = get(t, v, base+"/my-account/edit-account/")
	n := gooHidden(t, body, "save-account-details-nonce")
	acc := url.Values{"account_first_name": {"A"}, "account_last_name": {"B"}, "account_display_name": {"A B"}, "account_email": {"strong@example.test"},
		"password_current": {asStrongPass}, "password_1": {"monkey"}, "password_2": {"monkey"}, "save-account-details-nonce": {n}, "save_account_details": {"1"}}
	post(t, v, base+"/my-account/edit-account/", acc)
	u, _ := srv.store.UserByLoginOrEmail(ctx, "strongone")
	if u == nil || u.DisplayName == "A B" {
		t.Error("weak password change was not refused")
	}

	// A customer with an e-mailed second factor is challenged at My Account login.
	id := asCreateUser(t, srv, "twofa", "customer", asStrongPass)
	_, _ = getPool(t).Exec(ctx, `INSERT INTO adminsec_factors (user_id, email_enabled, email_address) VALUES ($1, true, 'twofa@example.test')`, id)
	lv := gooFrontClient()
	_, body, _ = get(t, lv, base+"/my-account/")
	code, body, _ := post(t, lv, base+"/my-account/", url.Values{"username": {"twofa"}, "password": {asStrongPass},
		"woocommerce-login-nonce": {gooHidden(t, body, "woocommerce-login-nonce")}, "login": {"Log in"}})
	if code != http.StatusOK || !strings.Contains(body, "6-digit code") {
		t.Fatalf("no second-factor challenge at My Account login: %d %.300s", code, body)
	}
}
