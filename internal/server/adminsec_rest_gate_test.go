package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// A role requirement past its grace period, or a forced password change, must close cookie REST as
// well as the HTML admin: otherwise a session holding only the password takes a REST nonce (heartbeat,
// or one it had before), mints an Application Password and keeps API access for good.
func TestAdminsecBlockedSessionHasNoREST(t *testing.T) {
	for _, mode := range []string{"expired-requirement", "forced-password"} {
		t.Run(mode, func(t *testing.T) {
			base, c, srv := asStart(t)
			pool := getPool(t)
			ctx := context.Background()
			srv.cfg.SMTPHost = "smtp.example.test" // e-mail requirements are enforced only when mail can be sent

			// A nonce taken before the block, and a positive control that it works.
			rc := newRESTClient(t, c, base)
			if code, _, _, _ := rc.do("GET", "/wp/v2/users/me?context=edit", nil); code != http.StatusOK {
				t.Fatalf("control: users/me before the block -> %d", code)
			}

			fixURL := asProfileGateURL
			if mode == "expired-requirement" {
				fixURL = asAccountGateURL
				asConfigure(t, srv, func(st *asSettings) {
					st.RequireRoles = map[string]string{"administrator": "2fa"}
					st.GraceDays = 7
				})
				if _, err := pool.Exec(ctx, `UPDATE users SET meta = meta || '{"adminsec_grace_start": 1000}' WHERE login = 'admin'`); err != nil {
					t.Fatal(err)
				}
			} else if _, err := pool.Exec(ctx, `UPDATE users SET meta = meta || '{"adminsec_force_password": true}' WHERE login = 'admin'`); err != nil {
				t.Fatal(err)
			}

			if code, _, loc := get(t, c, base+"/wp-admin/edit"); code != http.StatusFound || loc != fixURL {
				t.Fatalf("HTML admin not gated: %d %s", code, loc)
			}
			// The screen that fixes it stays open.
			fixPage, _ := url.Parse(fixURL)
			fixPage.Fragment = ""
			if code, _, loc := get(t, c, base+fixPage.String()); code != http.StatusOK {
				t.Fatalf("fix screen %s: %d %s", fixPage, code, loc)
			}

			// The heartbeat (exempt from the gate so the auth check keeps working) hands out no REST nonce.
			_, body, _ := post(t, c, base+"/wp-admin/admin-ajax", url.Values{"action": {"heartbeat"}, "data[wp-refresh-nonces]": {"1"}})
			var hb map[string]any
			if err := json.Unmarshal([]byte(body), &hb); err != nil {
				t.Fatalf("heartbeat: %v %q", err, body)
			}
			if hb["wp-auth-check"] != true {
				t.Errorf("heartbeat auth check: %v", hb)
			}
			if n, _ := hb["rest_nonce"].(string); n != "" {
				t.Errorf("heartbeat issued a REST nonce to a blocked session")
			}
			if code, _, _ := get(t, c, base+"/wp-admin/admin-ajax?action=rest-nonce"); code == http.StatusOK {
				t.Errorf("admin-ajax rest-nonce answered a blocked session")
			}

			// A nonce the session already had does not open REST either.
			code, me, _, _ := rc.do("GET", "/wp/v2/users/me?context=edit", nil)
			if code != http.StatusForbidden || me["code"] != "adminsec_account_action_required" {
				t.Errorf("blocked session REST: users/me -> %d %v", code, me)
			}
			code, ap, _, _ := rc.do("POST", "/wp/v2/users/me/application-passwords", map[string]any{"name": "persist"})
			if code == http.StatusCreated || ap["password"] != nil {
				t.Errorf("blocked session minted an application password: %d", code)
			}
			var n int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM app_passwords`).Scan(&n); err != nil || n != 0 {
				t.Errorf("application passwords stored: %d %v", n, err)
			}

			// Once the account is fixed, REST works again with the same session.
			if mode == "expired-requirement" {
				asConfigure(t, srv, func(st *asSettings) { st.RequireRoles = nil })
			} else if _, err := pool.Exec(ctx, `UPDATE users SET meta = meta - 'adminsec_force_password' WHERE login = 'admin'`); err != nil {
				t.Fatal(err)
			}
			if code, _, _, _ := rc.do("GET", "/wp/v2/users/me?context=edit", nil); code != http.StatusOK {
				t.Errorf("after the fix: users/me -> %d", code)
			}
		})
	}
}
