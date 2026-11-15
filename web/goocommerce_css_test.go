package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestGooCommerceCSSFocusVisible guards keyboard accessibility: goocommerce.css copies
// WooCommerce's `outline: 0` on cart/checkout fields, so every selector in such a block must
// have a matching :focus-visible rule that restores a visible focus ring.
func TestGooCommerceCSSFocusVisible(t *testing.T) {
	blockRE := regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	outlineZero := regexp.MustCompile(`(?m)^\s*outline\s*:\s*(0|none)\s*;`)
	comments := regexp.MustCompile(`(?s)/\*.*?\*/`)
	for _, name := range []string{
		"goocommerce/assets/css/goocommerce.css",
		"goocommerce/assets/css/goocommerce-rtl.css",
	} {
		raw, err := fs.ReadFile(embedded, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		css := comments.ReplaceAllString(string(raw), "")
		for _, want := range []string{
			".form-row input.input-text:focus-visible",
			".coupon .input-text:focus-visible",
		} {
			if !strings.Contains(css, want) {
				t.Errorf("%s: missing %q focus rule", name, want)
			}
		}
		for _, m := range blockRE.FindAllStringSubmatch(css, -1) {
			if !outlineZero.MatchString(m[2]) {
				continue
			}
			for _, sel := range strings.Split(m[1], ",") {
				sel = strings.TrimSpace(sel)
				if sel == "" || strings.Contains(sel, ":focus") || strings.HasPrefix(sel, "@") {
					continue
				}
				if !strings.Contains(css, sel+":focus-visible") {
					t.Errorf("%s: %q sets outline: 0 without a :focus-visible replacement", name, sel)
				}
			}
		}
	}
}
