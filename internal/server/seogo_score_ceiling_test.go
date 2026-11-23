package server

import (
	"strings"
	"testing"
)

// A post that passes every SEO check must score 100: a full pass is worth 9 points for every check,
// so no check may cap a passing post below the maximum.
func TestSEOGoPerfectPostScores100(t *testing.T) {
	para := "<p>Search ranking matters for every site. Moreover, short sentences help readers. " +
		"Writers keep paragraphs brief. Therefore, search engines understand the page. " +
		"Teams measure results often. Also, clear headings guide each visitor.</p>"
	var b strings.Builder
	b.WriteString("<p>Golang SEO starts with a clear goal. Then the page explains it well.</p>")
	b.WriteString(`<p>Read the <a href="https://example.com/guide/">site guide</a> and the <a href="https://go.dev/doc/">Go documentation</a>.</p>`)
	b.WriteString(`<figure><img src="https://example.com/a.jpg" alt="Golang SEO checklist"></figure>`)
	heads := []string{"Why golang SEO matters", "Plan the content", "Golang SEO in practice", "Measure the results"}
	for i, h := range heads {
		b.WriteString("<h2>" + h + "</h2>")
		for j := 0; j < 2; j++ {
			b.WriteString(para)
		}
		if i%2 == 0 {
			b.WriteString("<p>Good golang SEO keeps the work simple. Finally, it rewards patience.</p>")
		}
	}
	in := seogoAnalysisInput{
		SEOTitle:    "Golang SEO: 7 Proven Tips for Faster Pages",
		PostTitle:   "Golang SEO: 7 Proven Tips for Faster Pages",
		Description: "Golang SEO explained step by step: clear headings, short paragraphs, useful links and images, and the measurements that show what works.",
		Slug:        "golang-seo",
		URL:         "https://example.com/golang-seo/",
		Content:     b.String(),
		Keywords:    []string{"golang seo"},
		SiteHost:    "example.com",
	}
	got := seogoAnalyzeSEO(in)
	for _, r := range got.Results {
		if r.Group == "" && r.Score != 9 {
			t.Errorf("%s scored %d: %s", r.ID, r.Score, r.Text)
		}
	}
	if got.Score != 100 {
		t.Errorf("perfect post scored %d, want 100", got.Score)
	}
}
