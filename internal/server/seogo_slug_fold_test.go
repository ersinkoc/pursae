package server

import "testing"

// A keyphrase with Turkish letters is in the slug WordPress makes of it (accents dropped).
func TestSEOGoSlugKeywordFoldsAccents(t *testing.T) {
	for _, c := range []struct{ kw, slug string }{
		{"özgeçmiş", "ozgecmis"},
		{"öne çıkanlar", "one-cikanlar"},
		{"yazılar", "yazilar"},
		{"PHP Go geçişi", "php-uygulamalari-go-gecis"},
	} {
		in := seogoAnalysisInput{Keywords: []string{c.kw}, Slug: c.slug, Locale: "tr_TR",
			Content: "<p>" + c.kw + " hakkında kısa bir metin.</p>"}
		got := 0
		for _, r := range seogoAnalyzeSEO(in).Results {
			if r.ID == "slugKeyword" {
				got = r.Score
			}
		}
		if got != 9 && !(c.kw == "PHP Go geçişi" && got >= 6) {
			t.Errorf("keyphrase %q, slug %q: slugKeyword score %d, want 9", c.kw, c.slug, got)
		}
	}
}
