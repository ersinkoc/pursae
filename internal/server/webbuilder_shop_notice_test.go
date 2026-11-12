package server

import "testing"

func TestWBHasShopNotices(t *testing.T) {
	for _, tc := range []struct {
		markup string
		want   bool
	}{
		{gooNoticesMarkup(nil, []string{"added to cart"}, nil), true},
		{gooNoticesMarkup(nil, nil, []string{"information"}), true},
		{gooNoticesMarkup([]string{"error"}, nil, nil), true},
		{gooNoticesMarkup(nil, nil, nil), false},
		{"", false},
	} {
		if got := wbHasShopNotices(tc.markup); got != tc.want {
			t.Errorf("markup %q: got %t, want %t", tc.markup, got, tc.want)
		}
	}
}
