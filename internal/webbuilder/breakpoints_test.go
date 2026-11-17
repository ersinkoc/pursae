package webbuilder

import "testing"

func TestSitePlanURLs(t *testing.T) {
	plan := PlanSite([]string{"index.html", "404.html", "hakkimizda.html", "kurumsal.html", "kurumsal/kadro.html", "hizmet/1.html", "haber/index.html", "haber/a.html"})
	got := plan.URLs()
	want := map[string]string{"index.html": "/", "hakkimizda.html": "/hakkimizda/", "kurumsal.html": "/kurumsal/", "kurumsal/kadro.html": "/kurumsal/kadro/",
		"hizmet/1.html": "/hizmet/1/", "haber/index.html": "/haber/", "haber/a.html": "/haber/a/"}
	for p, u := range want {
		if got[p] != u {
			t.Errorf("%s: %q, want %q", p, got[p], u)
		}
	}
	if _, ok := got["404.html"]; ok {
		t.Errorf("404 has a URL: %v", got)
	}
}

func TestSuggestBreakpoints(t *testing.T) {
	css := []string{`@media (min-width: 768px){a{}} @media screen and (min-width:768px) and (max-width: 1023.98px){b{}}
@media (min-width: 64em){c{}} @media (prefers-reduced-motion: reduce){d{}} @media (max-width: 10px){e{}}`, `@media(max-width:640px){f{}}`}
	got := SuggestBreakpoints(css, 0)
	if len(got) != 3 || got[0] != (Breakpoint{768, 2}) {
		t.Fatalf("%v", got)
	}
	m := map[int]int{}
	for _, b := range got {
		m[b.Px] = b.Count
	}
	if m[1024] != 2 || m[640] != 1 {
		t.Errorf("%v", m)
	}
	if len(SuggestBreakpoints(css, 2)) != 2 {
		t.Errorf("limit")
	}
}
