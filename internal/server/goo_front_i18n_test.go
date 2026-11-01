package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGooFrontTurkish: with Turkish as the site language the store front speaks Turkish, like
// WooCommerce with its language pack: cart, checkout and shop labels come from the WooCommerce
// pack (here a small one), Pursae Commerce's own strings from web/languages, and the user's
// content (product names, descriptions) stays as written.
func TestGooFrontTurkish(t *testing.T) {
	base, _, srv := gooSite(t)
	ctx := context.Background()
	f := gooFrontCatalog(t, srv)
	_ = srv.gooSetOpts(ctx, map[string]any{"goo_test_settings": map[string]any{"enabled": "yes"}})
	z := &gooZone{Name: "US", Locations: []gooZoneLocation{{Code: "US", Type: "country"}}}
	_ = srv.gooSaveZone(ctx, z)
	_, _ = srv.gooAddMethod(ctx, z.ID, "flat_rate", map[string]any{"cost": "5"})

	dir := srv.lang.Dir
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := "Plural-Forms: nplurals=2; plural=(n > 1);\n"
	if err := os.WriteFile(filepath.Join(dir, "admin-tr_TR.mo"), i18nMO(map[string]string{"": head, "Home": "Ana sayfa"}), 0o644); err != nil {
		t.Fatal(err)
	}
	woo := i18nMO(map[string]string{"": head,
		"Proceed to checkout": "Ödeme sayfasına git", "Cart totals": "Sepet toplamı", "Billing details": "Fatura detayları",
		"Place order": "Siparişi onayla", "Your order": "Siparişiniz", "View cart": "Sepeti görüntüle", "Update cart": "Sepeti güncelle",
		"%s is a required field.": "%s gerekli bir alandır.", "Billing %s": "Fatura %s", "First name": "Ad",
		"Short shirt text": "YANLIŞ: ürün açıklaması çevrildi", // user content must stay as written
	})
	if err := os.WriteFile(filepath.Join(dir, "plugins", "woocommerce-tr_TR.mo"), woo, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(ctx, map[string]any{"WPLANG": "tr_TR"}); err != nil {
		t.Fatal(err)
	}
	srv.lang.Reset()

	v := gooFrontClient()
	get(t, v, base+"/?add-to-cart="+itoa64(f.mug.ID))
	_, cart, _ := get(t, v, base+"/cart/")
	gooMustContain(t, "Turkish cart", cart, "Ödeme sayfasına git", "Sepet toplamı", "Sepeti güncelle", ">Mug</a>", `"i18n_view_cart":"Sepeti görüntüle"`)
	if strings.Contains(cart, ">Proceed to checkout<") || strings.Contains(cart, "Cart totals") {
		t.Error("the cart still shows English labels")
	}

	_, checkout, _ := get(t, v, base+"/checkout/")
	gooMustContain(t, "Turkish checkout", checkout, "Fatura detayları", "Siparişiniz", `value="Siparişi onayla" data-value="Siparişi onayla">Siparişi onayla</button>`,
		"Kupon kodunuz varsa lütfen aşağıda uygulayın.") // Pursae Commerce's own string (web/languages/wpgo-tr_TR.json)
	if strings.Contains(checkout, "Billing details") || strings.Contains(checkout, ">Place order<") {
		t.Error("the checkout still shows English labels")
	}

	// A validation error names the field in Turkish too ("Fatura Ad gerekli bir alandır.").
	bad := gooFrontCheckoutForm(gooHidden(t, checkout, "woocommerce-process-checkout-nonce"))
	bad.Del("billing_first_name")
	post(t, v, base+"/checkout/", bad)
	_, checkout, _ = get(t, v, base+"/checkout/")
	gooMustContain(t, "checkout error", checkout, "<strong>Fatura Ad</strong> gerekli bir alandır.")

	// The product page: the store's labels in Turkish, the description as the shop owner wrote it.
	_, product, _ := get(t, v, base+"/product/shirt/")
	gooMustContain(t, "Turkish product page", product, "Short shirt text", `aria-label="Ekmek kırıntısı"`)
	if strings.Contains(product, "YANLIŞ") {
		t.Error("the product's short description was translated")
	}
}
