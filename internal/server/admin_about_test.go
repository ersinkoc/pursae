package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAboutTeam checks the Team section of About Pursae: four cards with the photos served from the
// admin's own assets (never hotlinked from pursae.com), the social links opening in a new tab, and the
// Turkish strings from web/languages/wpgo-tr_TR.json with the names left untranslated.
func TestAboutTeam(t *testing.T) {
	ts, c, srv := newTestServer(t, false)
	installAndLogin(t, ts, c)
	code, body, _ := get(t, c, ts.URL+"/wp-admin/about")
	if code != http.StatusOK {
		t.Fatalf("about: %d", code)
	}
	for _, want := range []string{
		`<h2 id="pursae-team-title">Our team</h2>`, "The small team that builds, tests and designs Pursae.",
		`pursae-team-card--bora`, `pursae-team-card--aleyna`, `pursae-team-card--davut`, `pursae-team-card--open`,
		`<h3 translate="no">Bora Ata Türkoğlu</h3><p>Co-Founder, Engineering</p>`,
		`<h3 translate="no">Aleyna Özyaman</h3><p>Co-Founder, Design &amp; Strategy</p>`,
		`<h3 translate="no">Davut İlbak</h3><p>AI System Architect</p>`,
		`<h3>Could it be you?</h3><p>Frontend Developer wanted</p>`,
		`href="https://www.linkedin.com/in/boracomet/" target="_blank" rel="noopener" aria-label="Bora Ata Türkoğlu · LinkedIn"`,
		`href="https://github.com/boracomet" target="_blank" rel="noopener"`,
		`href="https://www.instagram.com/bora4dev/"`, `href="https://x.com/ataturkoglubora"`,
		`href="https://www.linkedin.com/in/aleynaozyaman/"`, `href="https://www.instagram.com/aleynaozyaman/"`, `href="https://byumbral.studio/"`,
		`href="https://www.linkedin.com/in/davutilbak/"`, `href="https://davutilbak.com/"`,
		`href="https://pursae.com/forum/register/" target="_blank" rel="noopener">Apply →</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("about page lacks %q", want)
		}
	}
	for _, slug := range []string{"bora", "aleyna", "davut", "open"} {
		for _, size := range []string{"480", "720"} {
			p := "/wp-admin/assets/images/team/" + slug + "-" + size + ".webp"
			if !strings.Contains(body, p) {
				t.Errorf("about page lacks %s", p)
			}
			code, img, _ := get(t, c, ts.URL+p)
			if code != http.StatusOK || !strings.HasPrefix(img, "RIFF") {
				t.Errorf("%s: %d", p, code)
			}
		}
	}
	if strings.Contains(body, "pursae.com/wp-content") {
		t.Error("the about page hotlinks pursae.com images")
	}
	if i, j := strings.Index(body, `<section class="px-sec pursae-team"`), strings.Index(body, `<p class="px-foot">`); i < 0 || j < i {
		t.Error("no team section")
	} else if strings.Contains(body[i:j], `style=`) || strings.Contains(body[i:j], ` on`+`click=`) {
		t.Error("the team section has an inline style or handler (the admin CSP)")
	}

	// Turkish: the section's strings are translated, the names are not.
	if err := os.MkdirAll(srv.lang.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srv.lang.Dir, "admin-tr_TR.mo"), i18nMO(map[string]string{"": "Plural-Forms: nplurals=2; plural=(n > 1);\n"}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := srv.settings.Set(context.Background(), map[string]any{"WPLANG": "tr_TR"}); err != nil {
		t.Fatal(err)
	}
	srv.lang.Reset()
	_, body, _ = get(t, c, ts.URL+"/wp-admin/about")
	for _, want := range []string{
		">Ekibimiz</h2>", "Pursae'yi geliştiren, test eden ve tasarlayan küçük ekip.", "Kurucu Ortak, Mühendislik",
		"Kurucu Ortak, Tasarım ve Strateji", "Yapay Zekâ Sistem Mimarı", "Bu sen olabilir misin?", "Frontend Geliştirici aranıyor",
		">Başvur →</a>", `<h3 translate="no">Bora Ata Türkoğlu</h3>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Turkish about page lacks %q", want)
		}
	}
}
