package server

import (
	"html/template"
	"math"
	"strconv"
	"strings"
)

// Money helpers: wc_get_price_decimals(), wc_format_decimal(), wc_price() and wc_round_tax_total().
// Amounts are float64 like WooCommerce's, rounded to the store's decimals where WooCommerce rounds.

// gooRound rounds half away from zero to dp decimals (round() in PHP).
func gooRound(v float64, dp int) float64 {
	p := math.Pow(10, float64(dp))
	r := math.Round(v*p) / p
	if r == 0 {
		return 0 // no negative zero
	}
	return r
}

// gooDecimals is wc_get_price_decimals().
func (s *Server) gooDecimals() int {
	d := s.gooOptInt("price_num_decimals")
	if d < 0 {
		return 0
	}
	return d
}

// gooCurrency is get_woocommerce_currency().
func (s *Server) gooCurrency() string {
	if c := s.gooOptString("currency"); c != "" {
		return c
	}
	return "USD"
}

// gooParseAmount reads a price typed in the admin or a form: the store's decimal separator, "" → nil.
func (s *Server) gooParseAmount(v string) *float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if sep := s.gooOptString("price_decimal_sep"); sep != "" && sep != "." {
		v = strings.ReplaceAll(v, s.gooOptString("price_thousand_sep"), "")
		v = strings.ReplaceAll(v, sep, ".")
	}
	v = strings.ReplaceAll(v, ",", "")
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

// gooFormatDecimal is wc_format_decimal(): a plain "1234.50" string with dp decimals (dp < 0 trims zeros).
func gooFormatDecimal(v float64, dp int) string {
	if dp < 0 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strconv.FormatFloat(gooRound(v, dp), 'f', dp, 64)
}

// gooNumberFormat is number_format() with the store's separators.
func (s *Server) gooNumberFormat(v float64, dp int) string {
	neg := v < 0
	str := strconv.FormatFloat(math.Abs(gooRound(v, dp)), 'f', dp, 64)
	intPart, frac, _ := strings.Cut(str, ".")
	ts := s.gooOptString("price_thousand_sep")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(ts)
		}
		b.WriteRune(r)
	}
	out := b.String()
	if dp > 0 {
		out += s.gooOptString("price_decimal_sep") + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// gooPriceFormat is get_woocommerce_price_format() for the currency position option.
func (s *Server) gooPriceFormat() string {
	switch s.gooOptString("currency_pos") {
	case "right":
		return "%2$s%1$s"
	case "left_space":
		return "%1$s&nbsp;%2$s"
	case "right_space":
		return "%2$s&nbsp;%1$s"
	}
	return "%1$s%2$s"
}

// gooPrice is wc_price(): <span class="woocommerce-Price-amount amount"><bdi>…</bdi></span>.
func (s *Server) gooPrice(v float64) template.HTML {
	return s.gooPriceIn(v, s.gooCurrency())
}

// gooPriceIn is wc_price() with an explicit currency (orders keep theirs).
func (s *Server) gooPriceIn(v float64, currency string) template.HTML {
	neg := v < 0
	num := s.gooNumberFormat(math.Abs(v), s.gooDecimals())
	sym := `<span class="woocommerce-Price-currencySymbol">` + gooCurrencySymbol(currency) + `</span>`
	f := s.gooPriceFormat()
	f = strings.Replace(f, "%1$s", sym, 1)
	f = strings.Replace(f, "%2$s", num, 1)
	if neg {
		f = "-" + f
	}
	return template.HTML(`<span class="woocommerce-Price-amount amount"><bdi>` + f + `</bdi></span>`)
}

// gooPricePlain is the price as text (e-mails' plain parts, CSV exports, REST "formatted" strings).
func (s *Server) gooPricePlain(v float64, currency string) string {
	num := s.gooNumberFormat(math.Abs(v), s.gooDecimals())
	sym := strings.ReplaceAll(gooCurrencySymbol(currency), "&nbsp;", " ")
	f := strings.NewReplacer("&nbsp;", " ").Replace(s.gooPriceFormat())
	f = strings.Replace(f, "%1$s", sym, 1)
	f = strings.Replace(f, "%2$s", num, 1)
	if v < 0 {
		f = "-" + f
	}
	return f
}

// gooMinorUnits converts an amount to integer minor units (the Store API's prices).
func (s *Server) gooMinorUnits(v float64) string {
	return strconv.FormatInt(int64(math.Round(v*math.Pow(10, float64(s.gooDecimals())))), 10)
}
