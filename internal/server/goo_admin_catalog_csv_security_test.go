package server

import (
	"context"
	"strings"
	"testing"

	"wpgo/internal/store"
)

// A CSV import by a user without unfiltered_html must KSES description and purchase_note, as the
// product editor does (content_save_pre); a user with unfiltered_html keeps raw HTML.
func TestGooCSVImportKsesWithoutUnfilteredHTML(t *testing.T) {
	_, _, srv := gooSite(t)
	ctx := context.Background()
	uid, err := srv.store.CreateUser(ctx, store.NewUser{Login: "csvmgr", Email: "csvmgr@example.test", PasswordHash: "x", Role: "shop_manager"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetUserCapExceptions(ctx, uid, map[string]string{"unfiltered_html": "deny"}); err != nil {
		t.Fatal(err)
	}
	admin, err := srv.store.UserByLoginOrEmail(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	mapping := []string{"type", "sku", "name", "description", "purchase_note"}
	row := func(sku string) []string {
		return []string{"simple", sku, "X " + sku, `<p>ok</p><script>alert(1)</script>`, `<b>n</b><script>alert(2)</script>`}
	}
	srv.gooImportRows(ctx, [][]string{row("K-DENY")}, mapping, false, uid)
	srv.gooImportRows(ctx, [][]string{row("K-ADMIN")}, mapping, false, admin.ID)
	get := func(sku string) *gooProduct {
		p, err := srv.gooGetProduct(ctx, srv.gooRefID(ctx, sku, nil))
		if err != nil {
			t.Fatalf("%s: %v", sku, err)
		}
		return p
	}
	p := get("K-DENY")
	if strings.Contains(p.Post.Content, "<script") || strings.Contains(p.PurchaseNote, "<script") || !strings.Contains(p.Post.Content, "<p>ok</p>") {
		t.Errorf("denied user stored unfiltered HTML: %q / %q", p.Post.Content, p.PurchaseNote)
	}
	if a := get("K-ADMIN"); !strings.Contains(a.Post.Content, "<script") {
		t.Errorf("unfiltered_html user lost raw HTML: %q", a.Post.Content)
	}
}
