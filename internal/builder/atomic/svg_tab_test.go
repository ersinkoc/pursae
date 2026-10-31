package atomic

import (
	"strings"
	"testing"
)

// The inline SVG path must also drop a script link hidden by a tab or newline character reference.
func TestSanitizeSVGDropsScriptLinksHiddenByControlCharacters(t *testing.T) {
	for _, v := range []string{"java&#9;script:alert(1)", "jav&#10;ascript:alert(1)", "&#13;javascript:alert(1)", "vb&#9;script:x"} {
		in := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><a href="` + v + `"><text>x</text></a><a xlink:href="` + v + `"><text>y</text></a></svg>`
		out := sanitizeSVG(in)
		flat := strings.ToLower(strings.NewReplacer("&#9;", "", "&#x9;", "", "&#10;", "", "&#13;", "", "\t", "", "\n", "", "\r", "").Replace(out))
		if strings.Contains(flat, `href="javascript:`) || strings.Contains(flat, `href="vbscript:`) {
			t.Errorf("%q survived: %s", v, out)
		}
	}
}

func TestSanitizeSVGKeepsOrdinaryLinks(t *testing.T) {
	out := sanitizeSVG(`<svg xmlns="http://www.w3.org/2000/svg"><a href="https://example.com/a"><text>x</text></a></svg>`)
	if !strings.Contains(out, `href="https://example.com/a"`) {
		t.Fatalf("ordinary link lost: %s", out)
	}
}
