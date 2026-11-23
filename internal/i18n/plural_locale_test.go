package i18n

import (
	"os"
	"path/filepath"
	"testing"
)

// The embedded JSON packs have no Plural-Forms header. Without a core .mo, a one-form language
// fell back to the English plural and a three-or-more-form language picked the wrong form
// (Russian "5 секунды", Arabic dual for 3, Japanese "5 seconds").
func TestJSONPluralWithoutCorePack(t *testing.T) {
	extra := os.DirFS(filepath.Join("..", "..", "web", "languages"))
	cat := func(locale string) *Catalog { return NewManager("", extra).Catalog(locale) }
	check := func(locale, single, plural string, n int, want string) {
		t.Helper()
		if got := cat(locale).N(single, plural, n); got != want {
			t.Errorf("%s N(%q, %d) = %q, want %q", locale, single, n, got, want)
		}
	}
	check("ja", "%s second", "%s seconds", 5, "%s 秒")
	check("zh_CN", "%s min", "%s mins", 12, "%s 分钟")
	check("ru_RU", "%s second", "%s seconds", 1, "%s секунда")
	check("ru_RU", "%s second", "%s seconds", 2, "%s секунды")
	check("ru_RU", "%s second", "%s seconds", 5, "%s секунд")
	check("ru_RU", "%s second", "%s seconds", 21, "%s секунда")
	check("pl_PL", "%s second", "%s seconds", 1, "%s sekunda")
	check("pl_PL", "%s second", "%s seconds", 2, "%s sekundy")
	check("pl_PL", "%s second", "%s seconds", 5, "%s sekund")
	check("pl_PL", "%s second", "%s seconds", 22, "%s sekundy")
	check("cs_CZ", "%s second", "%s seconds", 5, "%s sekund")
	check("lt_LT", "%s second", "%s seconds", 1, "%s sekundė")
	check("lt_LT", "%s second", "%s seconds", 2, "%s sekundės")
	check("lt_LT", "%s second", "%s seconds", 10, "%s sekundžių")
	check("ro_RO", "%s second", "%s seconds", 1, "%s secundă")
	check("ro_RO", "%s second", "%s seconds", 2, "%s secunde")
	check("ro_RO", "%s second", "%s seconds", 20, "%s de secunde")
	check("lv", "%s year", "%s years", 0, "%s gadu")
	check("lv", "%s year", "%s years", 1, "%s gads")
	check("lv", "%s year", "%s years", 2, "%s gadi")
	check("lv", "%s year", "%s years", 11, "%s gadu")
	check("lv", "%s year", "%s years", 21, "%s gads")
	check("sl_SI", "%s year", "%s years", 1, "%s leto")
	check("sl_SI", "%s year", "%s years", 2, "%s leti")
	check("sl_SI", "%s year", "%s years", 3, "%s leta")
	check("sl_SI", "%s year", "%s years", 5, "%s let")
	check("cy", "%s year", "%s years", 1, "%s flwyddyn")
	check("cy", "%s year", "%s years", 2, "%s flynedd")
	check("cy", "%s year", "%s years", 3, "%s blynedd")
	check("cy", "%s year", "%s years", 8, "%s mlynedd")
	check("gd", "%s year", "%s years", 3, "%s bliadhnaichean")
	check("gd", "%s year", "%s years", 20, "%s bliadhna")
	check("ar", "%s second", "%s seconds", 0, "%s ثانية")
	check("ar", "%s second", "%s seconds", 1, "%s ثانية")
	check("ar", "%s second", "%s seconds", 2, "%s ثانيتان")
	check("ar", "%s second", "%s seconds", 3, "%s ثوانٍ")
	check("ar", "%s second", "%s seconds", 11, "%s ثانية")
	check("fr_FR", "%s second", "%s seconds", 0, "%s seconde")
	check("fr_FR", "%s second", "%s seconds", 2, "%s secondes")
	check("pt_BR", "%s year", "%s years", 0, "%s ano")
	check("pt_BR", "%s year", "%s years", 2, "%s anos")
	// Turkish stays the two-form default, including with no core pack.
	check("tr_TR", "%s min", "%s mins", 20, "%s dk")
	check("tr_TR", "%s second", "%s seconds", 1, "%s sn")
}
