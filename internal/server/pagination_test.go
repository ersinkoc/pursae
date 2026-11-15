package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// hugePaged overflowed (page-1)*20 when it was not capped: the OFFSET came out negative, which
// Postgres rejects ("OFFSET must not be negative").
const hugePaged = "9223372036854775807"

// The admin ?paged= value is capped like store.MaxPage however large (or malformed) it is.
func TestGooPagedCapped(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	for q, want := range map[string]int{hugePaged: store.MaxPage, "99999999999999999999": store.MaxPage, "-5": 1, "": 1, "3": 3} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?paged="+q, nil)
		got := gooPaged(c)
		if got != want || (got-1)*20 < 0 {
			t.Errorf("paged=%q: got %d, want %d", q, got, want)
		}
	}
}

// A crafted ?paged= on the coupon list is an empty page, not a 500 carrying the database error.
func TestGooHugePagedCoupons(t *testing.T) {
	base, c, srv := gooSite(t)
	ctx := context.Background()
	if err := srv.gooSaveCoupon(ctx, &gooCoupon{Code: "paged10", DiscountType: "percent", Amount: 10}); err != nil {
		t.Fatal(err)
	}
	list, total, err := srv.gooQueryCoupons(ctx, "", "", 1<<62, 20)
	if err != nil || total != 1 || len(list) != 0 {
		t.Fatalf("gooQueryCoupons past the end: %d rows, total %d, err %v", len(list), total, err)
	}
	body := gooStoreGet(t, c, base+"/wp-admin/goocommerce?page=coupons&paged="+hugePaged)
	if strings.Contains(body, "<code>paged10</code>") || strings.Contains(body, "OFFSET") {
		t.Error("a page past the end must be empty")
	}
}
