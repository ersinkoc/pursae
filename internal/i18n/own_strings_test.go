package i18n

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	ownPlaceholder = regexp.MustCompile(`%(?:\d+\$)?[sdfu]|%%|%[A-Za-z_]+%`)
	ownTag         = regexp.MustCompile(`<[^>]+>`)
)

func ownSig(re *regexp.Regexp, s string) string {
	m := re.FindAllString(html.UnescapeString(s), -1)
	sort.Strings(m)
	return strings.Join(m, "\x00")
}

// TestOwnStringFiles is the validator for web/languages/wpgo-<locale>.json: wpgo-de_DE.json is the
// reference set of msgids that every language (English variants excepted, they only hold spelling
// differences) must have; wpgo-tr_TR.json, wpgo-sv_SE.json and wpgo-he_IL.json hold more. Each
// translation in every file must keep the placeholders and HTML markup of its msgid (entities may be written as the
// character itself; numbers may be localised, "2FA", "02-Feb").
func TestOwnStringFiles(t *testing.T) {
	dir := filepath.Join("..", "..", "web", "languages")
	load := func(name string) map[string]json.RawMessage {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return m
	}
	ref := load("wpgo-de_DE.json")
	files, _ := filepath.Glob(filepath.Join(dir, "wpgo-*.json"))
	if len(files) < 100 {
		t.Fatalf("only %d language files", len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		locale := strings.TrimSuffix(strings.TrimPrefix(name, "wpgo-"), ".json")
		if strings.HasPrefix(locale, "en_") {
			continue
		}
		got := load(name)
		problems := 0
		report := func(format string, args ...any) {
			if problems++; problems <= 5 {
				t.Errorf(locale+": "+format, args...)
			}
		}
		for msgid := range ref {
			if _, ok := got[msgid]; !ok {
				report("missing %.70q", msgid)
			}
		}
		for msgid, raw := range got {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				continue // plural forms: checked by the catalog loader
			}
			if strings.TrimSpace(s) == "" {
				report("empty translation of %.70q", msgid)
				continue
			}
			for what, re := range map[string]*regexp.Regexp{"placeholders": ownPlaceholder, "markup": ownTag} {
				if ownSig(re, msgid) != ownSig(re, s) {
					report("%s differ: %.70q => %.70q", what, msgid, s)
				}
			}
		}
		if problems > 5 {
			t.Errorf("%s: %d problems in all", locale, problems)
		}
	}
}
