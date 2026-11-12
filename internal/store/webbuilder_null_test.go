package store

import (
	"encoding/json"
	"testing"
)

func TestWebBuilderRawNullWithWhitespace(t *testing.T) {
	for _, s := range []string{"", " \n", "null", " \r\nnull\t"} {
		raw := json.RawMessage(s)
		if got := string(rawOr(raw, "{}")); got != "{}" {
			t.Errorf("rawOr(%q) = %q", s, got)
		}
		if err := requireDoc(raw); err == nil {
			t.Errorf("requireDoc(%q) accepted missing document", s)
		}
	}
}
