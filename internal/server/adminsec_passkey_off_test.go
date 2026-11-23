package server

import (
	"strings"
	"testing"
)

// The passkey button and its endpoints exist only while Pursae Security is on: after it was
// switched on (passkeys enabled) and off again, the login page and its passkey actions are gone.
func TestAdminSecPasskeyButtonGoneWhenOff(t *testing.T) {
	base, c, srv := asStart(t)
	asConfigure(t, srv, func(st *asSettings) { st.Passkeys = true })
	host := strings.Replace(base, "127.0.0.1", "localhost", 1) // passkeys need a domain, not an IP
	if !strings.Contains(asPage(t, asNewClient(), host+"/wp-login"), `id="adminsec-passkey"`) {
		t.Skip("passkeys not usable on the test host; nothing to compare")
	}
	asToggle(t, c, base, "deactivate")
	for _, u := range []string{host + "/wp-login", host + "/wp-login.php", base + "/wp-login"} {
		code, body, _ := get(t, asNewClient(), u)
		if code == 200 && (strings.Contains(body, "adminsec-passkey") || strings.Contains(body, "Log in with a passkey")) {
			t.Errorf("%s: passkey button shown while Pursae Security is off", u)
		}
	}
	if code, body, _ := get(t, asNewClient(), host+"/wp-login?action=passkey_options"); strings.Contains(body, "challenge") {
		t.Errorf("passkey options served while off: %d", code)
	}
	// No stored copy of the login page may bring the button back (back/forward, restored tabs).
	resp, err := asNewClient().Get(host + "/wp-login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("login page Cache-Control %q, want no-store", cc)
	}
	asToggle(t, c, base, "activate") // and back on: the button returns
	if !strings.Contains(asPage(t, asNewClient(), host+"/wp-login"), `id="adminsec-passkey"`) {
		t.Error("passkey button missing after reactivation")
	}
}
