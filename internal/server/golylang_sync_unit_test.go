package server

import "testing"

func TestPllSEOTranslatable(t *testing.T) {
	for _, k := range []string{"_seogo_title", "_seogo_description", "_seogo_canonical", "_seogo_og_title", "_yoast_wpseo_title",
		"_yoast_wpseo_metadesc", "rank_math_title", "rank_math_canonical_url", "_seogo_seo_score"} {
		if !pllSEOTranslatable(k) {
			t.Errorf("%s keeps syncing", k)
		}
	}
	for _, k := range []string{"_seogo_noindex", "_seogo_robots_adv", "rank_math_robots", "rank_math_advanced_robots", "_seogo_cornerstone",
		"_seogo_primary_category", "_seogo_og_image", "price", "_yoast_wpseo_meta-robots-noindex"} {
		if pllSEOTranslatable(k) {
			t.Errorf("%s no longer syncs", k)
		}
	}
	if !pllSEOCanonical("_seogo_canonical") || !pllSEOCanonical("rank_math_canonical_url") || pllSEOCanonical("_seogo_title") {
		t.Error("pllSEOCanonical")
	}
}
