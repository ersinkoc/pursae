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
	_, body, _ := get(t, c, base+"/wp-admin/nav-menus?menu="+itoa64(menu))
	if !regexp.MustCompile(`<li id="menu-item-`+id+`" class="[^"]*\bpending\b`).MatchString(body) ||
		!strings.Contains(body, `name="menu-item-db-id[`+id+`]"`) {
		t.Fatal("the pending item is missing from the Menus screen")
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
