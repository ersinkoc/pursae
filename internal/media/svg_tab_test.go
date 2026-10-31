package media

import (
	"encoding/xml"
	"strings"
	"testing"
)

// Browsers delete tab, CR and LF from a URL before reading its scheme, so a link such as
// java&#9;script:alert(1) is still a javascript: link. The sanitizer must treat it as one.
func TestSanitizeSVGDropsScriptLinksHiddenByControlCharacters(t *testing.T) {
	for _, v := range []string{"java&#9;script:alert(1)", "jav&#10;ascript:alert(1)", "&#13;javascript:alert(1)", "java&#x9;script&#x3a;alert(1)"} {
		in := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><a href="` + v + `"><text>x</text></a><a xlink:href="` + v + `"><text>y</text></a></svg>`
		out, err := SanitizeSVG([]byte(in))
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		dec := xml.NewDecoder(strings.NewReader(string(out)))
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok {
				for _, a := range se.Attr {
					flat := strings.ToLower(strings.NewReplacer("\t", "", "\n", "", "\r", "", " ", "").Replace(a.Value))
					if (a.Name.Local == "href" || a.Name.Local == "src") && strings.HasPrefix(flat, "javascript:") {
						t.Errorf("%q survived as %s=%q", v, a.Name.Local, a.Value)
					}
				}
			}
		}
	}
}

func TestSanitizeSVGKeepsOrdinaryLinks(t *testing.T) {
	in := `<svg xmlns="http://www.w3.org/2000/svg"><a href="https://example.com/a"><text>x</text></a><a href="#frag"><text>y</text></a></svg>`
	out, err := SanitizeSVG([]byte(in))
	if err != nil || !strings.Contains(string(out), `href="https://example.com/a"`) || !strings.Contains(string(out), `href="#frag"`) {
		t.Fatalf("ordinary links were changed: %q %v", out, err)
	}
}
