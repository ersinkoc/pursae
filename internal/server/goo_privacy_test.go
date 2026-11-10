package server

import (
	"net/url"
	"testing"
)

func TestGooReusableOrder(t *testing.T) {
	prev := &gooOrder{Status: "failed", CartHash: "abc", CustomerID: 7}
	if !gooReusableOrder(prev, &gooOrder{CartHash: "abc", CustomerID: 7}) {
		t.Error("a failed order with the same cart should be reused")
	}
	if gooReusableOrder(prev, &gooOrder{CartHash: "other", CustomerID: 7}) {
		t.Error("a changed cart must create a new order")
	}
	if gooReusableOrder(prev, &gooOrder{CartHash: "abc", CustomerID: 8}) {
		t.Error("another customer's order must not be reused")
	}
	prev.Status = "processing"
	if gooReusableOrder(prev, &gooOrder{CartHash: "abc", CustomerID: 7}) {
		t.Error("a paid order must not be reused")
	}
}

func TestGooAttributionFrom(t *testing.T) {
	a := gooAttributionFrom(url.Values{"utm_source": {"news"}, "utm_medium": {"email"}}, "", "shop.test")
	if a["utm_source"] != "news" || a["utm_medium"] != "email" || a["source_type"] != "utm" {
		t.Errorf("utm: %v", a)
	}
	a = gooAttributionFrom(nil, "https://example.org/post", "shop.test")
	if a["utm_source"] != "example.org" || a["source_type"] != "referral" {
		t.Errorf("referral: %v", a)
	}
	if len(gooAttributionFrom(nil, "https://shop.test/x", "shop.test")) != 0 {
		t.Error("an internal referrer is not attribution")
	}
}
