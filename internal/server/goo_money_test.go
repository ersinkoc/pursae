package server

import "testing"

func TestGooParseAmountSeparators(t *testing.T) {
	cases := []struct {
		in, dec, ts string
		want        float64
		ok          bool
	}{
		{"12.50", ".", ",", 12.5, true},
		{"12,50", ".", ",", 12.5, true}, // was 1250: the comma was dropped
		{"12,5", ".", ",", 12.5, true},
		{"1,234.50", ".", ",", 1234.5, true},
		{"1,234", ".", ",", 1234, true},
		{"1,234,567.89", ".", ",", 1234567.89, true},
		{"-3,25", ".", ",", -3.25, true},
		{"12,50", ".", "", 12.5, true},
		{" 1 234.50 ", ".", " ", 1234.5, true},
		{".5", ".", ",", 0.5, true},
		{"12,50", ",", ".", 12.5, true},
		{"12.50", ",", ".", 12.5, true}, // was 1250: the dot was taken for the thousands separator
		{"1.234,50", ",", ".", 1234.5, true},
		{"1.234", ",", ".", 1234, true},
		{"1234,5", ",", "", 1234.5, true},
		{"", ".", ",", 0, false},
		{"abc", ".", ",", 0, false},
		{"$12", ".", ",", 0, false},
		{"1,2,3", ".", ",", 0, false},
		{"1.2.3", ".", ",", 0, false},
		{"12,50.1", ".", ",", 0, false},
	}
	for _, c := range cases {
		got := gooParseAmountSep(c.in, c.dec, c.ts)
		if (got != nil) != c.ok || (got != nil && *got != c.want) {
			g := "nil"
			if got != nil {
				g = gooFormatDecimal(*got, -1)
			}
			t.Errorf("parse %q (dec %q, thousands %q) = %s, want %v ok=%v", c.in, c.dec, c.ts, g, c.want, c.ok)
		}
	}
}

// gooRound matches PHP's round(): the scaled value is pre-rounded to 15 significant digits, so
// amounts just under the half in binary round up the way they are written.
func TestGooRoundLikePHP(t *testing.T) {
	cases := []struct {
		v    float64
		dp   int
		want float64
	}{
		{1.005, 2, 1.01}, // math.Round(100.49999999999999) would give 1.00
		{0.285, 2, 0.29},
		{1.255, 2, 1.26},
		{2.675, 2, 2.68},
		{-1.005, 2, -1.01},
		{1.004, 2, 1.0},
		{0.5, 0, 1},
		{-0.5, 0, -1},
		{1234.5678, 2, 1234.57},
		{-0.001, 2, 0},
		{19.99 * 3, 2, 59.97},
	}
	for _, c := range cases {
		if got := gooRound(c.v, c.dp); got != c.want {
			t.Errorf("gooRound(%v, %d) = %v, want %v", c.v, c.dp, got, c.want)
		}
	}
}
