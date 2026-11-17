package webbuilder

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shared fixtures (testdata/override) are printed byte for byte by KitOverrideCSS and by the
// editor's overrides.ts.
func TestKitOverrideCSSFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/override/*.json")
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var fx struct {
			Override KitOverride `json:"override"`
		}
		if err := json.Unmarshal(raw, &fx); err != nil {
			t.Fatal(f, err)
		}
		want, err := os.ReadFile(strings.TrimSuffix(f, ".json") + ".css")
		if err != nil {
			t.Fatal(err)
		}
		if got := KitOverrideCSS(&fx.Override); got != string(want) {
			t.Errorf("%s:\n got %q\nwant %q", f, got, want)
		}
	}
}

func TestParseKitOverride(t *testing.T) {
	ok := `{"rules":[{"ctx":["@layer utilities"],"sel":".rounded-2xl","p":{"border-radius":"2rem"}},{"ctx":["@layer theme"],"sel":":root, :host","p":{"--color-brand-600":"#c2410c"}}]}`
	o, err := ParseKitOverride(json.RawMessage(ok))
	if err != nil || len(o.Rules) != 2 {
		t.Fatalf("valid override: %v %+v", err, o)
	}
	if o, err := ParseKitOverride(nil); err != nil || !o.Empty() {
		t.Fatalf("empty: %v", err)
	}
	bad := map[string]string{
		"selector with a block":   `{"rules":[{"sel":".a{color:red}","p":{"color":"red"}}]}`,
		"selector with markup":    `{"rules":[{"sel":".a</style>","p":{"color":"red"}}]}`,
		"at-rule as ctx":          `{"rules":[{"ctx":["@import url(x)"],"sel":".a","p":{"color":"red"}}]}`,
		"ctx with a block":        `{"rules":[{"ctx":["@media x{"],"sel":".a","p":{"color":"red"}}]}`,
		"javascript url":          `{"rules":[{"sel":".a","p":{"background":"url(javascript:alert(1))"}}]}`,
		"expression":              `{"rules":[{"sel":".a","p":{"width":"expression(alert(1))"}}]}`,
		"value with a semicolon":  `{"rules":[{"sel":".a","p":{"color":"red;x:y"}}]}`,
		"bad property":            `{"rules":[{"sel":".a","p":{"co lor":"red"}}]}`,
		"duplicate rule":          `{"rules":[{"sel":".a","p":{"color":"red"}},{"sel":".a","p":{"color":"blue"}}]}`,
		"selector with url":       `{"rules":[{"sel":"a[href=url(x)]","p":{"color":"red"}}]}`,
		"unbalanced selector":     `{"rules":[{"sel":".a:not(.b","p":{"color":"red"}}]}`,
		"not json":                `{"rules":3}`,
		"selector with a comment": `{"rules":[{"sel":".a/**/","p":{"color":"red"}}]}`,
	}
	for name, raw := range bad {
		if _, err := ParseKitOverride(json.RawMessage(raw)); !errors.Is(err, ErrBadOverride) {
			t.Errorf("%s: want ErrBadOverride, got %v", name, err)
		}
	}
	big := `{"rules":[{"sel":".a","p":{"--x":"` + strings.Repeat("a", MaxOverrideBytes) + `"}}]}`
	if _, err := ParseKitOverride(json.RawMessage(big)); !errors.Is(err, ErrTooLargeOverride) {
		t.Errorf("too large: %v", err)
	}
}
