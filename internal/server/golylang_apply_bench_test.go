package server

import (
	"fmt"
	"strings"
	"testing"

	"wpgo/internal/i18n"
)

// pllBenchPage is a Twenty Twenty-Five home page of ten posts (about 40 KB): head with styles and
// scripts, header, navigation, the query loop (posts marked as user content), search form, footer.
func pllBenchPage() []byte {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="fr-FR"><head><meta charset="UTF-8" /><title>Mon Site – Juste un autre site</title>`)
	b.WriteString(`<style id="global-styles-inline-css">` + strings.Repeat(`:root{--wp--preset--color--base:#fff;--wp--preset--spacing--60:2rem}`, 60) + `</style>`)
	b.WriteString(`<script type="speculationrules">{"prefetch":[{"source":"document","where":{"and":[{"href_matches":"/*"}]}}]}</script></head>`)
	b.WriteString(`<body class="home blog wp-embed-responsive"><a class="skip-link screen-reader-text" id="wp-skip-link" href="#wp--skip-link--target">Skip to content</a>`)
	b.WriteString(`<div class="wp-site-blocks"><header class="wp-block-template-part"><div class="wp-block-group alignfull"><p class="wp-block-site-title"><a href="/fr/" rel="home">Mon Site</a></p>`)
	b.WriteString(`<nav class="wp-block-navigation" aria-label="Navigation"><button aria-label="Open menu" class="wp-block-navigation__responsive-container-open"></button><ul>`)
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, `<li class="wp-block-navigation-item"><a class="wp-block-navigation-item__content" href="/fr/page-%d/"><span class="wp-block-navigation-item__label">Page %d</span></a></li>`, i, i)
	}
	b.WriteString(`</ul></nav></div></header><main id="wp--skip-link--target"><div class="wp-block-query alignfull"><ul class="wp-block-post-template">`)
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, `<li class="wp-block-post post-%d"><div class="wp-block-group alignfull has-global-padding"><figure class="wp-block-post-featured-image"><img width="1024" height="683" src="/wp-content/uploads/img-%d.jpg" alt="Photo %d" loading="lazy" decoding="async" srcset="/a.jpg 1024w, /b.jpg 300w" /></figure>`, i, i, i)
		fmt.Fprintf(&b, `<h2 class="wp-block-post-title"><a href="/fr/post-%d/" target="_self">%sArticle numéro %d%s</a></h2>`, i, i18n.UCOpen, i, i18n.UCClose)
		fmt.Fprintf(&b, `<div class="entry-content wp-block-post-content">%s%s%s</div>`, i18n.UCOpenPost(int64(i+1)), strings.Repeat(`<p>Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore.</p>`, 12), i18n.UCClose)
		b.WriteString(`<div class="wp-block-post-date has-small-font-size"><time datetime="2026-11-08T10:00:00+00:00">8 novembre 2026</time></div></div></li>`)
	}
	b.WriteString(`</ul><nav class="wp-block-query-pagination" aria-label="Pagination"><a class="wp-block-query-pagination-next" href="/fr/page/2/">Next Page</a></nav></div>`)
	b.WriteString(`<form role="search" method="get" action="/fr/" class="wp-block-search"><label class="wp-block-search__label" for="s1">Search</label><input class="wp-block-search__input" id="s1" placeholder="Search…" value="" type="search" name="s" required /><button aria-label="Search" class="wp-block-search__button" type="submit">Search</button></form></main>`)
	b.WriteString(`<footer class="wp-block-template-part"><p class="has-small-font-size">Twenty Twenty-Five</p><p class="has-small-font-size">Designed with <a href="https://wordpress.org" rel="nofollow">WordPress</a></p></footer></div>`)
	b.WriteString(`<script id="wp-block-navigation-view-js" type="module" src="/wp-includes/js/view.min.js"></script></body></html>`)
	return []byte(b.String())
}

// BenchmarkPllStringPass is the page string pass on a typical page: a table of 300 translations
// (lookups are map hits or misses) and the walk. The anonymous no-translation case never walks.
func BenchmarkPllStringPass(b *testing.B) {
	page := pllBenchPage()
	t := &pllFrontTable{tr: map[string]string{}, rev: map[string]bool{}}
	for i := 0; i < 300; i++ {
		t.tr[fmt.Sprintf("Theme string %d", i)] = fmt.Sprintf("Chaîne %d", i)
	}
	t.tr["Skip to content"], t.tr["Search"], t.tr["Next Page"], t.tr["Designed with"] = "Aller au contenu", "Rechercher", "Page suivante", "Conçu avec"
	lookup := func(k string) (string, bool) { v, ok := t.tr[k]; return v, ok }
	b.SetBytes(int64(len(page)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out := pllUCFinish(i18n.TranslateFront(page, lookup, nil), false)
		if len(out) == 0 {
			b.Fatal("empty page")
		}
	}
}

// TestPllBenchPage checks the benchmark page translates as expected (and keeps user content).
func TestPllBenchPage(t *testing.T) {
	out := string(pllUCFinish(i18n.TranslateFront(pllBenchPage(), func(k string) (string, bool) {
		m := map[string]string{"Skip to content": "Aller au contenu", "Search": "Rechercher", "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore.": "X"}
		v, ok := m[k]
		return v, ok
	}, nil), false))
	if !strings.Contains(out, ">Aller au contenu</a>") || !strings.Contains(out, `aria-label="Rechercher"`) || strings.Contains(out, "<p>X</p>") || strings.Contains(out, "pll:uc") {
		t.Fatalf("bench page: %.400s", out)
	}
}
