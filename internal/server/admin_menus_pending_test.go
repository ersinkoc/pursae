package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// Items added on the Menus screen stay pending until the menu is saved. Like WordPress
// (wp_get_nav_menu_to_edit asks for every status), the screen lists them after a reload, so the next
// save publishes them instead of deleting them.
func TestNavMenusListsPendingItems(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	menu, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	it := &store.EditItem{MenuID: menu, Kind: "custom", Object: "custom", URL: "https://example.org/", Label: "Example", Status: "draft"}
	if err := srv.store.AddMenuItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	id := itoa64(it.ID)
	// A draft abandoned more than a day ago is deleted when the screen loads, not listed.
	old := &store.EditItem{MenuID: menu, Kind: "custom", Object: "custom", URL: "https://example.org/old", Label: "Abandoned", Status: "draft"}
	if err := srv.store.AddMenuItem(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE menu_items SET created_at = now() - interval '2 days' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}
	_, body, _ := get(t, c, base+"/wp-admin/nav-menus?menu="+itoa64(menu))
	if !regexp.MustCompile(`<li id="menu-item-`+id+`" class="[^"]*\bpending\b`).MatchString(body) ||
		!strings.Contains(body, `name="menu-item-db-id[`+id+`]"`) {
		t.Fatal("the pending item is missing from the Menus screen")
	}
	if strings.Contains(body, `menu-item-`+itoa64(old.ID)+`"`) {
		t.Error("the abandoned draft is still listed")
	}
	var n int
	if err := srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM menu_items WHERE id = $1`, old.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("the abandoned draft was not deleted: count %d (%v)", n, err)
	}
	nonce := regexp.MustCompile(`name="update-nav-menu-nonce" value="([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no update-nav-menu-nonce")
	}
	code, _, _ := post(t, c, base+"/wp-admin/nav-menus", url.Values{"action": {"update"}, "menu": {itoa64(menu)}, "menu-name": {"Main"},
		"update-nav-menu-nonce": {nonce[1]}, "menu-item-db-id[" + id + "]": {id}, "menu-item-position[" + id + "]": {"1"},
		"menu-item-title[" + id + "]": {"Example"}, "menu-item-url[" + id + "]": {"https://example.org/"}})
	if code != http.StatusFound {
		t.Fatalf("save: %d", code)
	}
	var status string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT status FROM menu_items WHERE id = $1`, it.ID).Scan(&status); err != nil || status != "publish" {
		t.Errorf("after saving: status %q (%v), want publish", status, err)
	}
}

// Saving a menu applies the posted hierarchy and order, and assigns the checked display locations.
func TestNavMenusSaveHierarchyAndLocation(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	ctx := context.Background()
	menu, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range []string{"A", "B", "C"} {
		it := &store.EditItem{MenuID: menu, Kind: "custom", Object: "custom", URL: "https://example.org/" + l, Label: l, Status: "publish"}
		if err := srv.store.AddMenuItem(ctx, it); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, itoa64(it.ID))
	}
	_, body, _ := get(t, c, base+"/wp-admin/nav-menus?menu="+itoa64(menu))
	nonce := regexp.MustCompile(`name="update-nav-menu-nonce" value="([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no update-nav-menu-nonce")
	}
	// New order C, A, B; B becomes a child of C.
	form := url.Values{"action": {"update"}, "menu": {itoa64(menu)}, "menu-name": {"Renamed"}, "update-nav-menu-nonce": {nonce[1]}}
	order := map[string]string{ids[2]: "1", ids[0]: "2", ids[1]: "3"}
	for id, pos := range order {
		form.Set("menu-item-db-id["+id+"]", id)
		form.Set("menu-item-position["+id+"]", pos)
		form.Set("menu-item-title["+id+"]", "T"+id)
		form.Set("menu-item-url["+id+"]", "https://example.org/")
	}
	form.Set("menu-item-parent-id["+ids[1]+"]", ids[2])
	locs := srv.activeTheme().MenuLocations
	if len(locs) > 0 {
		form.Set("menu-locations["+locs[0].Slug+"]", "1")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/nav-menus", form); code != http.StatusFound {
		t.Fatalf("save: %d", code)
	}
	items, err := srv.store.MenuItems(ctx, menu)
	if err != nil || len(items) != 3 {
		t.Fatalf("items %d (%v)", len(items), err)
	}
	got := map[string]*store.EditItem{}
	for _, it := range items {
		got[itoa64(it.ID)] = it
	}
	if got[ids[1]].ParentID != got[ids[2]].ID {
		t.Errorf("B parent = %d, want %d", got[ids[1]].ParentID, got[ids[2]].ID)
	}
	if got[ids[0]].ParentID != 0 || got[ids[2]].ParentID != 0 {
		t.Error("top-level items gained a parent")
	}
	if !(got[ids[2]].Order < got[ids[0]].Order && got[ids[0]].Order < got[ids[1]].Order) {
		t.Errorf("order not applied: C=%d A=%d B=%d", got[ids[2]].Order, got[ids[0]].Order, got[ids[1]].Order)
	}
	if len(locs) > 0 {
		menus, _ := srv.store.Menus(ctx)
		ok := false
		for _, m := range menus {
			if m.ID == menu && m.HasLocation(locs[0].Slug) && m.Name == "Renamed" {
				ok = true
			}
		}
		if !ok {
			t.Error("location not assigned or menu not renamed")
		}
	}
}
