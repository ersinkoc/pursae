package server

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// The Language switcher menu item becomes one item per language, each with its own id and class
// (Polylang: "menu-item-<id>-<slug>"), never one id repeated.
func TestGolylangSwitcherMenuItemIDs(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Français", "fr_FR", "fr", "fr")
	ctx := context.Background()
	if err := srv.wbSetActive(ctx, true); err != nil { // Hello Pursae prints the classic menu markup
		t.Fatal(err)
	}
	menu, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	it := &store.EditItem{MenuID: menu, Kind: "custom", Object: "custom", URL: "#pll_switcher", Label: "Languages", Status: "publish"}
	if err := srv.store.AddMenuItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetMenuLocations(ctx, map[string]int64{"primary": menu}); err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	_, body, _ := get(t, c, base+"/")
	id := itoa64(it.ID)
	for _, want := range []string{`id="menu-item-` + id + `-en"`, `id="menu-item-` + id + `-fr"`, ` menu-item-` + id + `-fr"`} {
		if !strings.Contains(body, want) {
			t.Errorf("switcher markup lacks %s", want)
		}
	}
	// Each id once per menu rendering (the menu may sit in several locations, so compare with the en item).
	en := strings.Count(body, `id="menu-item-`+id+`-en"`)
	if n := len(regexp.MustCompile(`id="menu-item-`+id+`"`).FindAllString(body, -1)); n != 0 || en == 0 {
		t.Errorf("bare id menu-item-%s printed %d times (en item %d times)", id, n, en)
	}
}
