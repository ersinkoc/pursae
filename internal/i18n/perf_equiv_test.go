package i18n

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestCollapseWSMatchesRegexp(t *testing.T) {
	cases := []string{"", " ", "  ", "a", "a b", "a  b", " a", "a ", "\t", "a\tb", "a\n\nb", "a \r\n b", "\f", "a\fb", "\v", "a\vb", "a b", "a   b", "日本  語", "\xff  \xff", "a   ", "   a"}
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b", " ", " ", "\t", "\n", "\r", "\f", "\v", " ", "é", "\xff", " "}
	for i := 0; i < 5000; i++ {
		var b bytes.Buffer
		for n := rng.Intn(12); n > 0; n-- {
			b.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		cases = append(cases, b.String())
	}
	for _, c := range cases {
		if got, want := collapseWS(c), spaceRun.ReplaceAllString(c, " "); got != want {
			t.Errorf("collapseWS(%q) = %q, regexp gives %q", c, got, want)
		}
		if got, want := collapseWS(c), wsRun.ReplaceAllString(c, " "); got != want {
			t.Errorf("collapseWS(%q) = %q, wsRun gives %q", c, got, want)
		}
	}
}

func TestScriptTranslationsCached(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, nil)
	rel := "wp-includes/js/dist/editor.js"
	sum := md5.Sum([]byte(rel))
	file := filepath.Join(dir, "pl_PL-"+hex.EncodeToString(sum[:])+".json")
	js := `{"locale_data":{"messages":{"":{"domain":"messages"},"Publish":["Opublikuj"]}}}`
	if err := os.WriteFile(file, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	first := m.ScriptTranslations("pl_PL", "/wp-includes/js/dist/editor.min.js", "")
	if len(first) == 0 {
		t.Fatal("no translations")
	}
	if err := os.Remove(file); err != nil { // a second call must not touch the disk
		t.Fatal(err)
	}
	if second := m.ScriptTranslations("pl_PL", "/wp-includes/js/dist/editor.min.js", ""); !bytes.Equal(first, second) {
		t.Errorf("cached result differs: %s vs %s", first, second)
	}
	// Negative results are cached too, until Reset.
	if m.ScriptTranslations("pl_PL", "other.js", "") != nil {
		t.Fatal("unexpected data")
	}
	osum := md5.Sum([]byte("other.js"))
	if err := os.WriteFile(filepath.Join(dir, "pl_PL-"+hex.EncodeToString(osum[:])+".json"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	if m.ScriptTranslations("pl_PL", "other.js", "") != nil {
		t.Error("negative result not cached")
	}
	m.Reset()
	if m.ScriptTranslations("pl_PL", "other.js", "") == nil {
		t.Error("Reset did not clear the cache")
	}
	if m.ScriptTranslations("pl_PL", "wp-includes/js/dist/editor.js", "") != nil {
		t.Error("removed file still served after Reset")
	}
}
