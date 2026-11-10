package i18n

import "testing"

// Dates in any format get their month names translated; prose that merely contains one stays.
func TestTranslateDateNamesAnyFormat(t *testing.T) {
	c := partsCatalog(t)
	for in, want := range map[string]string{
		"5 October 2026":       "5 październik 2026",
		"October 2026":         "październik 2026",
		"Oct 5, 2026 3:04 pm":  "", // "Oct" is not in this catalog: left alone
		"October 5th, 2026":    "październik 5th, 2026",
		"October":              "", // no digit: not a date
		"October is nice 2026": "",
		"Shop October 5":       "",
	} {
		got, ok := c.translateDateNames(in)
		if want == "" {
			if ok {
				t.Errorf("%q: translated to %q, want untouched", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%q: got %q (%v), want %q", in, got, ok, want)
		}
	}
	n := c.DateNames()
	if n.Months[9] != "październik" || n.Months[0] != "January" {
		t.Errorf("DateNames: %q %q", n.Months[9], n.Months[0])
	}
}
