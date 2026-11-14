package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Colour values: pastel keys, #rgb / #rrggbb in any case, nothing else.
func TestForumColorValue(t *testing.T) {
	for in, want := range map[string]string{"sky": "sky", " MINT ": "mint", "#ABC": "#aabbcc", "#1d4ed8": "#1d4ed8", "#1D4ED8": "#1d4ed8",
		"red": "", "#12345": "", "1d4ed8": "", "url(x)": "", "": ""} {
		if got := forumColorValue(in); got != want {
			t.Errorf("forumColorValue(%q) = %q, want %q", in, got, want)
		}
	}
	key, hex, ink, style := forumColorParts("mint")
	if key != "mint" || hex != "" || ink != "" || style != "" {
		t.Errorf("pastel parts: %q %q %q %q", key, hex, ink, style)
	}
	key, hex, ink, style = forumColorParts("#FDE68A")
	if key != "custom" || hex != "#fde68a" || ink == "" || string(style) != "--pf-c:#fde68a;--pf-ci:"+ink {
		t.Errorf("custom parts: %q %q %q %q", key, hex, ink, style)
	}
}

// The computed ink always reads at 4.5:1, keeps the hue on coloured backgrounds, and is dark on light
// colours and light on dark ones.
func TestForumInkContrast(t *testing.T) {
	for _, bg := range []string{"#ffffff", "#000000", "#fde68a", "#ffff00", "#e3f0fb", "#1d4ed8", "#0f172a", "#ef4444", "#22c55e",
		"#808080", "#7c3aed", "#f97316", "#00ffff", "#ff00ff", "#14b8a6", "#9ca3af", "#fafafa", "#111111"} {
		ink := forumInkFor(bg)
		if c := forumContrast(ink, bg); c < 4.5 {
			t.Errorf("%s: ink %s contrast %.2f", bg, ink, c)
		}
		lightBg := forumLuminance(forumRGB(bg)) > 0.18
		if lightBg != (forumLuminance(forumRGB(ink)) < forumLuminance(forumRGB(bg))) {
			t.Errorf("%s: ink %s goes the wrong way", bg, ink)
		}
	}
	// A blue background gets a blue ink, not black.
	h1, _, _ := forumHSL(forumRGB("#bfdbfe"))
	h2, _, _ := forumHSL(forumRGB(forumInkFor("#bfdbfe")))
	if d := h1 - h2; d > 8 || d < -8 {
		t.Errorf("hue drift: %.0f vs %.0f (%s)", h1, h2, forumInkFor("#bfdbfe"))
	}
}

// The admin screen: the swatches and the icon grid render as radios; a custom colour saves as hex
// and reaches the views, REST and the demo format.
func TestForumAdminColorIconPicker(t *testing.T) {
	base, c, srv := forumAdminSite(t)
	ctx := context.Background()
	nonce := forumAdminNonceOf(t, c, base, "forums")
	_, body, _ := get(t, c, base+"/wp-admin/forum?tab=forums")
	if n := strings.Count(body, `name="icon" value="`); n != len(forumIconNames())+1 {
		t.Errorf("icon radios on the add form: %d", n)
	}
	for _, want := range []string{`role="radiogroup"`, `name="color" value="sky"`, `name="color" value="custom"`, `type="color"`, `name="color_hex"`, `pf-icon--rocket`} {
		if !strings.Contains(body, want) {
			t.Errorf("add form lacks %s", want)
		}
	}
	add := func(v url.Values) (int, string) {
		v.Set("_wpnonce", nonce)
		code, _, loc := post(t, c, base+"/wp-admin/forum?tab=forums", v)
		return code, loc
	}
	if code, loc := add(url.Values{"title": {"Custom One"}, "color": {"custom"}, "color_hex": {"1D4ED8"}, "color_custom": {"#000000"}, "icon": {"rocket"}}); code != http.StatusFound {
		t.Fatalf("add: %d %s", code, loc)
	}
	f, err := srv.forumBySlug(ctx, "custom-one")
	if err != nil || f.Color != "custom" || f.ColorHex != "#1d4ed8" || f.ColorInk == "" || f.Icon != "rocket" ||
		!strings.Contains(string(f.ColorStyle), "--pf-c:#1d4ed8") {
		t.Fatalf("saved forum: %v %+v", err, f)
	}
	// The native input is used when the hex field is empty; a custom choice without a colour is refused.
	if code, _ := add(url.Values{"title": {"Picked"}, "color": {"custom"}, "color_custom": {"#fde68a"}}); code != http.StatusFound {
		t.Fatalf("add picked: %d", code)
	}
	if p, _ := srv.forumBySlug(ctx, "picked"); p == nil || p.ColorHex != "#fde68a" {
		t.Errorf("picked colour: %+v", p)
	}
	if code, _ := add(url.Values{"title": {"No Colour"}, "color": {"custom"}, "color_hex": {"nope"}, "color_custom": {"sky"}}); code == http.StatusFound {
		t.Error("a custom choice without a colour was saved")
	}
	// Edit form: the custom swatch checked, the hex and the ink shown.
	_, body, _ = get(t, c, base+"/wp-admin/forum?tab=forums&action=edit&id="+strconv.FormatInt(f.ID, 10))
	if !regexp.MustCompile(`value="custom" id="pf-color-custom" checked`).MatchString(body) || !strings.Contains(body, `value="#1d4ed8" class="code"`) ||
		!regexp.MustCompile(`name="icon" value="rocket" checked`).MatchString(body) || !strings.Contains(body, "Text "+f.ColorInk) {
		t.Error("edit form does not show the custom colour or the icon")
	}
	// Back to a pastel.
	enonce := forumAdminNonceOf(t, c, base, "forums")
	if code, _, _ := post(t, c, base+"/wp-admin/forum?tab=forums&action=edit&id="+strconv.FormatInt(f.ID, 10), url.Values{"_wpnonce": {enonce},
		"id": {strconv.FormatInt(f.ID, 10)}, "title": {"Custom One"}, "color": {"peach"}, "color_hex": {"#1d4ed8"}}); code != http.StatusFound {
		t.Fatalf("edit: %d", code)
	}
	if f2, _ := srv.forumByID(ctx, f.ID); f2.Color != "peach" || f2.ColorHex != "" {
		t.Errorf("back to pastel: %+v", f2)
	}
	// REST and the demo format.
	_, _ = srv.forumSaveForum(ctx, forumForumInput{ID: f.ID, Title: "Custom One", Design: &forumDesignInput{Color: "#14B8A6"}}, 1)
	_, body, _ = get(t, c, base+"/wp-json/forum/v1/forums")
	var list []map[string]any
	_ = json.Unmarshal([]byte(body), &list)
	found := false
	for _, x := range list {
		if x["slug"] == "custom-one" {
			found = x["color"] == "custom" && x["color_hex"] == "#14b8a6" && x["color_ink"] != ""
		}
	}
	if !found {
		t.Errorf("REST color_hex: %s", body)
	}
	if p := forumValidateDemo(json.RawMessage(`{"forums":[{"ref":"a","title":"A","color":"#1d4ed8"},{"ref":"b","title":"B","color":"#zzz"}]}`), nil); len(p) != 1 {
		t.Errorf("demo colours: %v", p)
	}
}
