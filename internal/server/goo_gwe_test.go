package server

import (
	"context"
	"testing"

	"wpgo/internal/store"
)

// gooProductID is the id of a product by slug, as a string.
func gooProductID(t *testing.T, srv *Server, slug string) string {
	t.Helper()
	var id int64
	if err := srv.store.Pool.QueryRow(context.Background(), `SELECT id FROM posts WHERE type = 'product' AND slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("product %s: %v", slug, err)
	}
	return itoa64(id)
}

// The store conditions pick the Theme Builder parts of store views.
func TestGooGolementorStoreConditions(t *testing.T) {
	product := &siteQuery{Kind: "page", Post: &store.Post{ID: 42, Type: "page"}, Store: "product"}
	cat := &siteQuery{Kind: "page", Post: &store.Post{ID: 7, Type: "page"}, Store: "product_cat", StoreTerm: 7}
	cart := &siteQuery{Kind: "page", Post: &store.Post{ID: 3, Type: "page"}, Store: "cart"}
	page := &siteQuery{Kind: "page", Post: &store.Post{ID: 5, Type: "page"}}
	for _, tc := range []struct {
		rule string
		q    *siteQuery
		want int
	}{
		{"product", product, 40}, {"product/42", product, 55}, {"product/43", product, 0}, {"product", cat, 0},
		{"product_archive", cat, 40}, {"product_archive/product_cat", cat, 45}, {"product_archive/product_cat/7", cat, 55},
		{"product_archive/product_cat/8", cat, 0}, {"product_archive/shop", cat, 0}, {"product_archive", product, 0},
		{"goocommerce/cart", cart, 55}, {"goocommerce/checkout", cart, 0}, {"goocommerce/cart", page, 0},
		{"singular/page", page, 30},
	} {
		if got := gweCondScore(tc.rule, tc.q); got != tc.want {
			t.Errorf("%s on %s: %d, want %d", tc.rule, tc.q.Store, got, tc.want)
		}
	}
	if types := gweLocationTypes("main", product); types[0] != "single-product" {
		t.Errorf("product location types %v", types)
	}
	if types := gweLocationTypes("main", cat); types[0] != "product-archive" {
		t.Errorf("archive location types %v", types)
	}
	// A specific product part beats the general one, a store part beats All Pages.
	parts := []*gwePart{{ID: 1, Type: "single-page", Conditions: []string{"include/singular/page"}},
		{ID: 2, Type: "single-product", Conditions: []string{"include/product"}},
		{ID: 3, Type: "single-product", Conditions: []string{"include/product/42"}}}
	if p := gwePick(parts, "main", product); p == nil || p.ID != 3 {
		t.Errorf("picked %v for the product", p)
	}
	cartParts := []*gwePart{{ID: 1, Type: "single-page", Conditions: []string{"include/singular/page"}},
		{ID: 2, Type: "single-page", Conditions: []string{"include/goocommerce/cart"}}}
	if p := gwePick(cartParts, "main", cart); p == nil || p.ID != 2 {
		t.Errorf("picked %v for the cart", p)
	}
}
