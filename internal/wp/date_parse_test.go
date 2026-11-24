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

func TestParseDateRejectsCalendarRollover(t *testing.T) {
	for _, date := range []string{"2023-02-29", "2024-04-31"} {
		if got, ok := ParseDate(date, "Y-m-d"); ok {
			t.Errorf("accepted %s as %v", date, got)
		}
	}
	if _, ok := ParseDate("2024-02-29", "Y-m-d"); !ok {
		t.Error("rejected leap day")
	}
}

func TestParseDateTwelveHourBounds(t *testing.T) {
	for _, text := range []string{"0:15 AM", "13:15 PM", "99:15 AM"} {
		if got, ok := ParseDate(text, "g:i A"); ok {
			t.Errorf("accepted %s as %v", text, got)
		}
	}
	for text, hour := range map[string]int{"12:15 AM": 0, "12:15 PM": 12, "1:15 PM": 13} {
		if got, ok := ParseDate(text, "g:i A"); !ok || got.Hour() != hour {
			t.Errorf("%s: %v, %v", text, got, ok)
		}
	}
}

func TestHumanTimeDiffLongIntervalIsSymmetric(t *testing.T) {
	a := time.Date(1000, 1, 1, 0, 0, 0, 0, time.UTC)
	b := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if forward, reverse := HumanTimeDiff(a, b), HumanTimeDiff(b, a); forward != reverse {
		t.Fatalf("forward=%q reverse=%q", forward, reverse)
	}
}

func TestParseDateUnicodeLiterals(t *testing.T) {
	format := "Y年m月d日"
	date := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	got, ok := ParseDate(FormatDate(date, format), format)
	if !ok || !got.Equal(date) {
		t.Fatalf("got %v, valid=%v", got, ok)
	}
}

func TestParseDateValidatesWeekday(t *testing.T) {
	for _, tc := range []struct {
		text, format string
		valid        bool
	}{
		{"Monday, October 5, 2026", "l, F j, Y", true},
		{"Mon, Oct 5, 2026", "D, M j, Y", true},
		{"Tuesday, October 5, 2026", "l, F j, Y", false},
		{"Sun, Oct 5, 2026", "D, M j, Y", false},
	} {
		if _, got := ParseDate(tc.text, tc.format); got != tc.valid {
			t.Errorf("ParseDate(%q, %q) valid=%v, want %v", tc.text, tc.format, got, tc.valid)
		}
	}
}

func TestParseDateRejectsMismatchedOrdinal(t *testing.T) {
	if _, ok := ParseDate("22th October 2026", "jS F Y"); ok {
		t.Fatal("accepted an ordinal suffix that disagrees with the day")
	}
	if _, ok := ParseDate("22nd October 2026", "jS F Y"); !ok {
		t.Fatal("rejected a matching ordinal suffix")
	}
}

func TestDateSignedAndExpandedYear(t *testing.T) {
	for _, tc := range []struct {
		year int
		want string
	}{
		{-55, "-0055-10-22"},
		{0, "0000-10-22"},
		{10191, "10191-10-22"},
	} {
		date := time.Date(tc.year, 10, 22, 0, 0, 0, 0, time.UTC)
		if got := FormatDate(date, "Y-m-d"); got != tc.want {
			t.Errorf("year %d formats as %q, want %q", tc.year, got, tc.want)
		}
		if got, ok := ParseDate(tc.want, "Y-m-d"); !ok || !got.Equal(date) {
			t.Errorf("ParseDate(%q) = %s, %v", tc.want, got, ok)
		}
	}
}

func TestLocationPrefersExplicitOffset(t *testing.T) {
	for _, tc := range []struct {
		zone string
		want int
	}{
		{"+05:45", 20700},
		{"-03:30", -12600},
		{"UTC", 0},
		{"+05:75", -14400},
	} {
		_, got := time.Date(2026, 1, 1, 0, 0, 0, 0, Location(tc.zone, -4)).Zone()
		if got != tc.want {
			t.Errorf("Location(%q, -4) gave offset %d, want %d", tc.zone, got, tc.want)
		}
	}
}
