package server

import (
	"regexp"
	"strings"
	"testing"
)

func TestSEOGoMetaFallbacks(t *testing.T) {
	m := map[string]any{
		"_yoast_wpseo_title":                 "%%title%% %%sep%% %%sitename%%",
		"_yoast_wpseo_metadesc":              "Yoast description",
		"_yoast_wpseo_focuskw":               "go seo",
		"_yoast_wpseo_focuskeywords":         `[{"keyword":"golang seo","score":60}]`,
		"_yoast_wpseo_meta-robots-noindex":   "1",
		"_yoast_wpseo_meta-robots-nofollow":  "1",
		"_yoast_wpseo_is_cornerstone":        "1",
		"rank_math_description":              "Rank Math description",
		"rank_math_robots":                   `a:2:{i:0;s:7:"noindex";i:1;s:9:"noarchive";}`,
		"rank_math_advanced_robots":          `a:2:{s:11:"max-snippet";s:2:"-1";s:17:"max-image-preview";s:5:"large";}`,
		"rank_math_pillar_content":           "on",
		"_yoast_wpseo_opengraph-image":       "https://example.test/og.png",
		"rank_math_breadcrumb_title":         "Short",
		"_yoast_wpseo_meta-robots-adv":       "none",
		"_yoast_wpseo_schema_article_type":   "NewsArticle",
		"_yoast_wpseo_primary_category":      "7",
		"_wp_old_slug":                       "x",
		"_seogo_canonical":                   "https://example.test/canonical/",
		"rank_math_facebook_title":           "RM social",
		"_yoast_wpseo_opengraph-description": "",
	}
	cases := map[string]string{
		"title": "%title% %sep% %sitename%", "description": "Yoast description", "focus": "go seo,golang seo",
		"noindex": "noindex", "nofollow": "1", "cornerstone": "1", "og_image": "https://example.test/og.png", "bc_title": "Short",
		"robots_adv": "noarchive", "max_snippet": "-1", "max_image_preview": "large", "schema_article": "NewsArticle",
		"primary_category": "7", "canonical": "https://example.test/canonical/", "og_title": "RM social", "og_description": "",
	}
	for f, want := range cases {
		if got := seogoMeta(m, f); got != want {
			t.Errorf("seogoMeta(%s) = %q, want %q", f, got, want)
		}
	}
	// Yoast's "2" means index; an explicit Pursae SEO value wins over both plugins.
	if got := seogoMeta(map[string]any{"_yoast_wpseo_meta-robots-noindex": "2"}, "noindex"); got != "index" {
		t.Errorf("Yoast index flag: %q", got)
	}
	if got := seogoMeta(map[string]any{"_seogo_title": "Mine", "_yoast_wpseo_title": "Theirs"}, "title"); got != "Mine" {
		t.Errorf("own key does not win: %q", got)
	}
	n := seogoImportMeta(m)
	if n == 0 || m["_seogo_title"] != "%title% %sep% %sitename%" || m["_yoast_wpseo_title"] == nil {
		t.Errorf("import: %d %v", n, m["_seogo_title"])
	}
}

func TestSEOGoTidyAndVars(t *testing.T) {
	if got := tidySep("Hello  - - World -", "-"); got != "Hello - World" {
		t.Errorf("tidySep: %q", got)
	}
	if got := yoastVars("%%title%% %%sep%% %%sitename%%"); got != "%title% %sep% %sitename%" {
		t.Errorf("yoastVars: %q", got)
	}
	if got := phpList(`a:1:{i:0;s:5:"index";}`); len(got) != 1 || got[0] != "index" {
		t.Errorf("phpList: %v", got)
	}
}

func TestSEOGoRedirectMatch(t *testing.T) {
	r := &seogoRedirectRow{Source: "/old-page/", Target: "/new/", MatchType: "exact"}
	if _, ok := r.match("old-page", "old-page"); !ok {
		t.Error("exact match failed")
	}
	if _, ok := r.match("old-page-2", "old-page-2"); ok {
		t.Error("exact matched a longer path")
	}
	re := &seogoRedirectRow{Source: `^/blog/(\d+)/(.*)$`, Target: "/archive/$1/$2", MatchType: "regex"}
	re.re = mustRegexp(t, re.Source)
	if got, ok := re.match("blog/2020/hello", "blog/2020/hello"); !ok || got != "/archive/2020/hello" {
		t.Errorf("regex: %q %v", got, ok)
	}
	for mt, path := range map[string]string{"contains": "x/legacy/y", "start": "legacy/y", "end": "y/legacy"} {
		row := &seogoRedirectRow{Source: "legacy", Target: "/", MatchType: mt}
		if _, ok := row.match(path, path); !ok {
			t.Errorf("%s did not match %s", mt, path)
		}
	}
}

func TestSEOGoAnalysisScoring(t *testing.T) {
	para := "Golang SEO is easy when you know the basics. However, many sites forget the details. Therefore we explain them here. " +
		"First, write a clear title. Then add a short description. Finally, link to related content and to good sources. "
	content := "<p>" + para + "</p><h2>Why golang SEO matters</h2><p>" + strings.Repeat(para, 3) + "</p>" +
		`<h2>Tools</h2><p>See <a href="/about/">our about page</a> and <a href="https://go.dev/">the Go site</a>.</p>` +
		`<p><img src="/a.png" alt="golang seo diagram" /></p><p>` + strings.Repeat(para, 2) + "</p>"
	in := seogoAnalysisInput{SEOTitle: "Golang SEO: 7 easy steps to better rankings", Description: "Golang SEO made easy: learn how to write titles, descriptions and links that rank. A practical guide with examples for every step.",
		Slug: "golang-seo", URL: "https://example.test/golang-seo/", Content: content, Keywords: []string{"golang seo"}, SiteHost: "example.test"}
	seo := seogoAnalyzeSEO(in)
	if seo.Rating != "good" {
		for _, r := range seo.Results {
			t.Logf("%s %d %s", r.Rating, r.Score, r.Text)
		}
		t.Fatalf("expected a good SEO score, got %d (%s)", seo.Score, seo.Rating)
	}
	if seo.Points <= 0 || seo.Points > 100 {
		t.Errorf("Rank Math points out of range: %d", seo.Points)
	}
	byID := map[string]seogoResult{}
	for _, r := range seo.Results {
		byID[r.ID] = r
	}
	for _, id := range []string{"keyphraseInTitle", "introductionKeyword", "slugKeyword", "internalLinks", "externalLinks", "metaDescriptionKeyword", "imageKeyphrase"} {
		if byID[id].Rating != "good" {
			t.Errorf("%s: %s (%s)", id, byID[id].Rating, byID[id].Text)
		}
	}

	// No keyphrase: the score is "not available", like Yoast.
	in2 := in
	in2.Keywords = nil
	if a := seogoAnalyzeSEO(in2); a.Rating != "na" || a.Score != 0 {
		t.Errorf("no keyphrase: %d %s", a.Score, a.Rating)
	}
	// Keyphrase stuffing drives the density assessment negative.
	in3 := in
	in3.Content = "<p>" + strings.Repeat("golang seo golang seo text ", 40) + "</p>"
	a := seogoAnalyzeSEO(in3)
	for _, r := range a.Results {
		if r.ID == "keyphraseDensity" && r.Score >= 0 {
			t.Errorf("stuffed density scored %d", r.Score)
		}
	}
	// Too long meta description, previously used keyphrase.
	in4 := in
	in4.Description = strings.Repeat("long ", 40)
	in4.UsedBy = []string{"Another post", "Third"}
	for _, r := range seogoAnalyzeSEO(in4).Results {
		if r.ID == "metaDescriptionLength" && r.Rating != "ok" {
			t.Errorf("long description rated %s", r.Rating)
		}
		if r.ID == "previouslyUsedKeywords" && r.Rating != "bad" {
			t.Errorf("used keyphrase rated %s", r.Rating)
		}
	}

	read := seogoAnalyzeReadability(in)
	if read.Score != 90 && read.Score != 60 {
		for _, r := range read.Results {
			t.Logf("%s %s", r.Rating, r.Text)
		}
		t.Errorf("readability of plain short sentences: %d", read.Score)
	}
	hard := in
	hard.Content = "<p>" + strings.Repeat("The extraordinarily complicated administrative documentation was comprehensively reviewed by the international organization committee members during the unprecedented institutional reorganization procedures and was subsequently considered acceptable by everybody involved in the process ", 8) + "</p>"
	if r := seogoAnalyzeReadability(hard); r.Score != 30 {
		t.Errorf("hard text readability: %d", r.Score)
	}
}

func TestSEOGoBlockSchemaAndCustom(t *testing.T) {
	content := `<!-- wp:seogo/faq {"questions":[{"id":"faq-question-1","question":"What is Pursae SEO?","answer":"An SEO plugin."}]} -->
<div class="wp-block-seogo-faq seogo-faq"></div>
<!-- /wp:seogo/faq -->
<!-- wp:yoast/how-to-block {"jsonTitle":"Brew tea","steps":[{"id":"s1","jsonName":"Boil","jsonText":"Boil water."}]} -->
<div></div>
<!-- /wp:yoast/how-to-block -->`
	qs, hs := seogoBlockSchema(content, "https://example.test/p/", "en-US")
	if len(qs) != 1 || qs[0]["name"] != "What is Pursae SEO?" || qs[0]["@id"] != "https://example.test/p/#faq-question-1" {
		t.Fatalf("faq: %v", qs)
	}
	if len(hs) != 1 || hs[0]["name"] != "Brew tea" {
		t.Fatalf("howto: %v", hs)
	}
	if n := seogoCustomSchema(`{"@context":"https://schema.org","@graph":[{"@type":"Event","name":"A"},{"@type":"Place"}]}`); len(n) != 2 {
		t.Errorf("custom graph: %v", n)
	}
	if n := seogoCustomSchema(`not json`); n != nil {
		t.Errorf("invalid JSON accepted")
	}
}

func mustRegexp(t *testing.T, s string) *regexp.Regexp {
	t.Helper()
	re, err := regexp.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return re
}
