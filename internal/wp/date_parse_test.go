package wp

import (
	"testing"
	"time"
)

func TestFormatDateNamesAndParse(t *testing.T) {
	d := time.Date(2026, 10, 5, 15, 4, 0, 0, time.UTC) // Monday
	n := EnglishDateNames()
	n.Months[9], n.Weekdays[1], n.PM = "Ekim", "Pazartesi", "ös"
	if got := FormatDateNames(d, "l, j F Y g:i a", n); got != "Pazartesi, 5 Ekim 2026 3:04 ös" {
		t.Errorf("got %q", got)
	}
	if got := FormatDateNames(d, "F j, Y", nil); got != "October 5, 2026" || got != FormatDate(d, "F j, Y") {
		t.Errorf("nil names: %q", got)
	}
	for _, f := range []string{"F j, Y", "j F Y", "d/m/Y", "l, F jS, Y", "Y-m-d H:i", "M j, Y g:i A"} {
		text := FormatDate(d, f)
		got, ok := ParseDate(text, f)
		if !ok || got.Year() != 2026 || got.Month() != 10 || got.Day() != 5 {
			t.Errorf("%q (%q): %v %v", f, text, got, ok)
		}
	}
	if got, _ := ParseDate("Oct 5, 2026 3:04 PM", "M j, Y g:i A"); got.Hour() != 15 || got.Minute() != 4 {
		t.Errorf("time: %v", got)
	}
	if _, ok := ParseDate("hello", "F j, Y"); ok {
		t.Error("garbage parsed")
	}
	if _, ok := ParseDate("2026", "Y z"); ok {
		t.Error("unsupported token parsed")
	}
}
