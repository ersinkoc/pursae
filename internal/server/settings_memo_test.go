package server

import (
	"context"
	"reflect"
	"testing"
)

// The settings-derived memos (asLoad, maintLoad, gooikeLoad, pllOpts, cspExtraState, gooExtOn) follow
// a Set at once, and changing a returned value never reaches the next call.
func TestSettingsMemosInvalidateAndIsolate(t *testing.T) {
	_, _, srv := newTestServer(t, false)
	ctx := context.Background()
	set := func(values map[string]any) {
		t.Helper()
		if err := srv.settings.Set(ctx, values); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("asLoad", func(t *testing.T) {
		st := srv.asLoad()
		st.RequireRoles["administrator"] = "2fa"
		st.PwMinLength["administrator"] = 99
		st.PwMinStrength["administrator"] = 0
		again := srv.asLoad()
		if again.RequireRoles["administrator"] != "" || again.PwMinLength["administrator"] == 99 || again.PwMinStrength["administrator"] == 0 {
			t.Fatalf("a caller's change reached the cache: %+v", again)
		}
		set(map[string]any{asOption: map[string]any{"idle_minutes": 77, "require_roles": map[string]any{"editor": "mfa"}}})
		if st := srv.asLoad(); st.IdleMinutes != 77 || st.RequireRoles["editor"] != "mfa" {
			t.Fatalf("asLoad did not follow the Set: idle %d roles %v", st.IdleMinutes, st.RequireRoles)
		}
		set(map[string]any{asOption: map[string]any{"idle_minutes": 5}})
		if st := srv.asLoad(); st.IdleMinutes != 5 || len(st.RequireRoles) != 0 {
			t.Fatalf("asLoad did not follow the second Set: idle %d roles %v", st.IdleMinutes, st.RequireRoles)
		}
	})

	t.Run("maintLoad", func(t *testing.T) {
		st := srv.maintLoad()
		st.Social["facebook"] = "https://example.com/x"
		if len(st.BypassRoles) > 0 {
			st.BypassRoles[0] = "changed"
		}
		again := srv.maintLoad()
		if again.Social["facebook"] != "" || (len(again.BypassRoles) > 0 && again.BypassRoles[0] == "changed") {
			t.Fatalf("a caller's change reached the cache: %v %v", again.Social, again.BypassRoles)
		}
		set(map[string]any{maintOptions: map[string]any{"header_text": "Back at noon"}})
		if h := srv.maintLoad().Heading; h != "Back at noon" {
			t.Fatalf("maintLoad did not follow the Set: %q", h)
		}
	})

	t.Run("gooikeLoad", func(t *testing.T) {
		st := srv.gooikeLoad()
		st.Categories[0].Signals[0] = "changed"
		st.Categories[0].Label = "changed"
		st.Cookies[0].Name = "changed"
		st.Texts.Title = "changed"
		again := srv.gooikeLoad()
		if again.Categories[0].Signals[0] == "changed" || again.Categories[0].Label == "changed" || again.Cookies[0].Name == "changed" || again.Texts.Title == "changed" {
			t.Fatalf("a caller's change reached the cache: %+v", again.Categories[0])
		}
		set(map[string]any{gooikeOptions: map[string]any{"policy_version": 7}})
		if v := srv.gooikeLoad().PolicyVersion; v != 7 {
			t.Fatalf("gooikeLoad did not follow the Set: %d", v)
		}
	})

	t.Run("pllOpts", func(t *testing.T) {
		set(map[string]any{golylangOption: map[string]any{"sync": []any{"taxonomies"}, "domains": map[string]any{"en": "https://en.example.com"},
			"nav_menus": map[string]any{"theme": map[string]any{"primary": map[string]any{"en": 3}}}}})
		o := srv.pllOpts()
		if !o.syncs("taxonomies") || o.Domains["en"] != "https://en.example.com" || o.NavMenus["theme"]["primary"]["en"] != 3 {
			t.Fatalf("pllOpts did not follow the Set: %+v", o)
		}
		o.Sync[0] = "changed"
		o.Domains["en"] = "changed"
		o.NavMenus["theme"]["primary"]["en"] = 9
		again := srv.pllOpts()
		if !again.syncs("taxonomies") || again.Domains["en"] != "https://en.example.com" || again.NavMenus["theme"]["primary"]["en"] != 3 {
			t.Fatalf("a caller's change reached the cache: %+v", again)
		}
		set(map[string]any{golylangOption: map[string]any{"force_lang": 0}})
		if o := srv.pllOpts(); o.ForceLang != 0 || o.syncs("taxonomies") {
			t.Fatalf("pllOpts did not follow the second Set: %+v", o)
		}
	})

	t.Run("cspExtraState", func(t *testing.T) {
		set(map[string]any{cspExtraOption: map[string]any{"connect-src": []any{"https://api.example.com"}}})
		src, hash := srv.cspExtraState()
		if hash == "" || len(src["connect-src"]) != 1 {
			t.Fatalf("cspExtraState did not follow the Set: %v %q", src, hash)
		}
		set(map[string]any{"blogdescription": "unrelated"})
		src2, hash2 := srv.cspExtraState()
		if hash2 != hash || reflect.ValueOf(src2).Pointer() != reflect.ValueOf(src).Pointer() {
			t.Fatal("an unrelated Set revalidated the unchanged sources")
		}
		set(map[string]any{cspExtraOption: map[string]any{}})
		if src, hash := srv.cspExtraState(); hash != "" || len(src) != 0 {
			t.Fatalf("cspExtraState did not follow the clearing Set: %v %q", src, hash)
		}
	})

	t.Run("gooExtOn", func(t *testing.T) {
		set(map[string]any{gooSetting: true, gooOptPrefix + "extensions": map[string]any{"bookings": true, "gift_cards": false}})
		if !srv.gooExtOn("bookings") || srv.gooExtOn("gift_cards") || srv.gooExtOn("missing") {
			t.Fatal("gooExtOn misread the extensions object")
		}
		set(map[string]any{gooOptPrefix + "extensions": "not an object"})
		if srv.gooExtOn("bookings") {
			t.Fatal("gooExtOn read a non-object option as on")
		}
		set(map[string]any{gooOptPrefix + "extensions": map[string]any{"bookings": true}, gooSetting: false})
		if srv.gooExtOn("bookings") {
			t.Fatal("gooExtOn is on while Goocommerce is off")
		}
	})
}
