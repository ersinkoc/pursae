package server

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestGooikeMoreServices(t *testing.T) {
	for key, id := range map[string]string{"matomo": "analytics.example.com/stats/3", "intercom": "ab12cd34", "hubspot": "1234567", "stripe": ""} {
		sv := gooikeServiceByKey(key)
		if sv == nil || !sv.validID(id) {
			t.Fatalf("service %s missing or rejects %q", key, id)
		}
		code := gooikeScript{Service: key, ServiceID: id}.code()
		found := false
		for _, d := range sv.Detect {
			found = found || strings.Contains(strings.ToLower(code), d)
		}
		if code == "" || !found || len(sv.Cookies) == 0 {
			t.Errorf("%s: snippet without a detect signature or cookies: %s", key, code)
		}
	}
	// IDs end up inside JavaScript strings: nothing that could break out may pass.
	for key, bad := range map[string][]string{
		"matomo":   {`a.example.com/1";alert(1)//`, "example.com", "example.com/x", "a b.com/1", "<x>.com/1", "example.com/1/"},
		"intercom": {`ab12"cd`, "AB12CD34", "a"},
		"hubspot":  {"12a45678", ""},
	} {
		for _, id := range bad {
			if gooikeServiceByKey(key).validID(id) || (gooikeScript{Service: key, ServiceID: id}).code() != "" {
				t.Errorf("%s accepts %q", key, id)
			}
		}
	}
	// A self-hosted Matomo brings its own host into the CSP.
	st := gooikeDefaults()
	st.Scripts = []gooikeScript{{Service: "matomo", ServiceID: "analytics.example.com/stats/3", Enabled: true, Category: "statistics"}}
	src := gooikeCSPSources(&st)
	if !gooikeContains(src["script-src"], "https://analytics.example.com") || !gooikeContains(src["connect-src"], "https://analytics.example.com") {
		t.Errorf("Matomo host not in the CSP: %v", src)
	}
	if c := st.Scripts[0].code(); !strings.Contains(c, `u="https://analytics.example.com/stats/"`) || !strings.Contains(c, `'setSiteId','3'`) {
		t.Errorf("Matomo snippet: %s", c)
	}
	// Stripe is necessary: its script is never held back.
	if got := gooikeBlockScripts(&st, `<script src="https://js.stripe.com/v3/"></script>`); strings.Contains(got, "text/plain") {
		t.Errorf("Stripe.js blocked: %s", got)
	}
	if got := gooikeBlockScripts(&st, `<script src="https://cdn.example.com/matomo.js"></script>`); !strings.Contains(got, `data-gooike-category="statistics"`) {
		t.Errorf("a Matomo script outside the script manager was not held back: %s", got)
	}
}

func TestGooikeMoreEmbeds(t *testing.T) {
	st := gooikeDefaults()
	for src, want := range map[string]string{
		"https://open.spotify.com/embed/track/abc":                "spotify",
		"https://w.soundcloud.com/player/?url=x":                  "soundcloud",
		"https://www.instagram.com/p/abc/embed":                   "instagram",
		"https://platform.twitter.com/embed/Tweet.html?id=1":      "x",
		"https://www.facebook.com/plugins/post.php?href=x":        "facebook",
		"https://www.tiktok.com/embed/v2/123":                     "tiktok",
		"https://www.dailymotion.com/embed/video/x1":              "dailymotion",
		"https://player.twitch.tv/?channel=x&parent=example.com":  "twitch",
		"https://www.facebook.com/somepage":                       "",
		"https://www.tiktok.com/@user":                            "",
		"https://notspotify.com/embed":                            "",
		"https://open.spotify.com.evil.test/embed":                "",
		"https://example.com/?u=https://open.spotify.com/embed/x": "",
	} {
		if got, _ := gooikeEmbedFor(&st, src); got != want {
			t.Errorf("gooikeEmbedFor(%q) = %q, want %q", src, got, want)
		}
	}
	// Loader scripts of blocked embeds are held back in the embed category.
	for _, tag := range []string{`<script async src="//www.instagram.com/embed.js"></script>`, `<script async src="https://platform.twitter.com/widgets.js" charset="utf-8"></script>`, `<script async src="https://www.tiktok.com/embed.js"></script>`} {
		if got := gooikeBlockScripts(&st, tag); !strings.Contains(got, `type="text/plain"`) || !strings.Contains(got, `data-gooike-category="marketing"`) {
			t.Errorf("embed loader not held back: %s", got)
		}
	}
	// Switched off per provider.
	st.EmbedsOff = []string{"spotify", "x"}
	if got, _ := gooikeEmbedFor(&st, "https://open.spotify.com/embed/track/abc"); got != "" {
		t.Error("Spotify blocked although switched off")
	}
	if got := gooikeBlockScripts(&st, `<script async src="https://platform.twitter.com/widgets.js"></script>`); strings.Contains(got, "text/plain") {
		t.Error("X loader blocked although switched off")
	}
	if got, _ := gooikeEmbedFor(&st, "https://w.soundcloud.com/player/?url=x"); got != "soundcloud" {
		t.Error("SoundCloud no longer blocked")
	}
}

func TestGooikeTextPresetsComplete(t *testing.T) {
	en := gooikePresetIdx().texts["en"]
	for _, k := range gooikeTextPresetKeys() {
		p := gooikeTextPresets[k]
		m := gooikePresetIdx().texts[k]
		for field := range en {
			if m[field] == "" {
				t.Errorf("preset %s lacks %s", k, field)
			}
		}
		if strings.Count(p.Texts().EmbedText, "%s") != 2 {
			t.Errorf("preset %s: embed text needs two %%s", k)
		}
		for _, c := range gooikeDefaultCategories() {
			if v := gooikePresetIdx().cats[k][c.Key]; v[0] == "" || v[1] == "" {
				t.Errorf("preset %s lacks category %s", k, c.Key)
			}
		}
	}
}

// TestGooikeAutomaticLanguage: untouched texts follow the site language, rewritten ones stay.
func TestGooikeAutomaticLanguage(t *testing.T) {
	base, c, srv := gooikeSite(t)
	ctx := context.Background()
	front := func() string {
		t.Helper()
		_, body, _ := get(t, c, base+"/")
		return body
	}
	if b := front(); !strings.Contains(b, "Accept all") || strings.Contains(b, "Tümünü kabul et") {
		t.Fatal("English site does not show the English banner")
	}
	if err := srv.settings.Set(ctx, map[string]any{"WPLANG": "tr_TR"}); err != nil {
		t.Fatal(err)
	}
	b := front()
	for _, want := range []string{"Tümünü kabul et", "Gizliliğinize değer veriyoruz", "Pazarlama", "Zorunlu"} {
		if !strings.Contains(b, want) {
			t.Fatalf("Turkish site: banner lacks %q", want)
		}
	}
	if strings.Contains(b, "Accept all") {
		t.Fatal("Turkish site still shows English texts")
	}
	// A text the admin rewrote is kept; the rest still follows the language.
	st := srv.gooikeLoad()
	st.Texts.Accept = "OK, fine by me"
	if err := srv.gooikeSave(ctx, st); err != nil {
		t.Fatal(err)
	}
	if b = front(); !strings.Contains(b, "OK, fine by me") || !strings.Contains(b, "Tümünü reddet") {
		t.Fatal("a rewritten text was replaced, or the others stopped following the language")
	}
	// The stored settings are not rewritten by serving a page.
	if got := srv.gooikeLoad(); got.Texts.Reject != "Reject all" || got.Categories[0].Label != "Necessary" {
		t.Fatalf("stored texts changed: %q %q", got.Texts.Reject, got.Categories[0].Label)
	}
	// A language without a built-in set, and the switch off, fall back to the stored texts.
	_ = srv.settings.Set(ctx, map[string]any{"WPLANG": "ja"})
	if b = front(); !strings.Contains(b, "Reject all") {
		t.Fatal("no fallback for a language without a set")
	}
	_ = srv.settings.Set(ctx, map[string]any{"WPLANG": "de_DE"})
	gooikePost(t, c, base, url.Values{"page": {"banner"}, "tab": {"texts"}, "do": {"texts"}})
	if srv.gooikeLoad().TextsAuto {
		t.Fatal("Automatic language not switched off")
	}
	if b = front(); !strings.Contains(b, "Reject all") || strings.Contains(b, "Alle ablehnen") {
		t.Fatal("Automatic language off still localises")
	}
}
