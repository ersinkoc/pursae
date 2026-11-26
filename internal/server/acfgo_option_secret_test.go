package server

import (
	"context"
	"testing"
)

// An ACF options prefix is any string post_id, and the options live in the site settings with
// auth_secret and the license and payment keys: get_field may read, and an options page may write,
// only settings that are ACF values (they have a "_<prefix>_<name>" field-key reference).
func TestACFGoOptionsNeverReachSiteSettings(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	acfgoActivate(t, c, ts.URL)
	ctx := context.Background()

	secret := srv.settings.String("auth_secret")
	if secret == "" {
		t.Fatal("no auth_secret after install")
	}
	if err := srv.settings.Set(ctx, map[string]any{"pursae_review_key": "sk_live_x", "options_phone": "+90 555", "_options_phone": "field_phone"}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ sel, id string }{{"secret", "auth"}, {"review_key", "pursae"}} {
		if v := srv.acfGetField(ctx, tc.sel, tc.id, false); v != nil {
			t.Errorf("get_field %q %q = %v, want nil", tc.sel, tc.id, v)
		}
		if v := srv.acfGetField(ctx, tc.sel, tc.id, true); v != nil {
			t.Errorf("formatted get_field %q %q = %v, want nil", tc.sel, tc.id, v)
		}
		if v := srv.acfGetField(ctx, "_"+tc.sel, tc.id, false); v != nil {
			t.Errorf("reference get_field %q %q = %v, want nil", "_"+tc.sel, tc.id, v)
		}
	}
	// Through the theme template functions too.
	if v := srv.acfgoTemplateFuncs()["get_field"].(func(string, ...any) any)("secret", "auth"); v != nil {
		t.Errorf("template get_field secret auth = %v", v)
	}

	// ACF's own option values still read.
	if v := srv.acfGetField(ctx, "phone", "option", false); v != "+90 555" {
		t.Errorf("get_field phone option = %v", v)
	}

	// An options page whose post_id makes <prefix>_<name> a core setting cannot overwrite or delete it.
	w := newACFWrite()
	w.put("secret", "field_secret", "attacker")
	if err := srv.acfgoCommit(ctx, &acfObject{Kind: "option", Prefix: "auth"}, w); err == nil {
		t.Error("commit over auth_secret succeeded")
	}
	w = newACFWrite()
	w.del["secret"] = true
	if err := srv.acfgoCommit(ctx, &acfObject{Kind: "option", Prefix: "auth"}, w); err == nil {
		t.Error("delete of auth_secret succeeded")
	}
	if got := srv.settings.String("auth_secret"); got != secret {
		t.Error("auth_secret changed")
	}

	// A new field value and an existing ACF value still save.
	w = newACFWrite()
	w.put("phone", "field_phone", "+90 556")
	w.put("fax", "field_fax", "123")
	if err := srv.acfgoCommit(ctx, &acfObject{Kind: "option", Prefix: "options"}, w); err != nil {
		t.Fatalf("commit ACF options: %v", err)
	}
	if srv.settings.String("options_phone") != "+90 556" || srv.settings.String("options_fax") != "123" || srv.settings.String("_options_fax") != "field_fax" {
		t.Errorf("options after save: %q %q", srv.settings.String("options_phone"), srv.settings.String("options_fax"))
	}
}
