package server

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var postNavLinkRe = regexp.MustCompile(`<a href="([^"]+)" rel="(prev|next)"`)

// postNavLinks returns the previous/next links of a single post page (rel → href).
func postNavLinks(t *testing.T, body string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range postNavLinkRe.FindAllStringSubmatch(body, -1) {
		out[m[2]] = m[1]
	}
	return out
}

// TestGolylangPostNavLanguage: the previous/next post links (the_post_navigation) keep to the
// post's language like Polylang's get_adjacent_post() join, and core's post navigation and comment
// form strings follow the page's language (Turkish from Pursae's own catalog, recorded under the
// core source on Languages › Translations).
func TestGolylangPostNavLanguage(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	installLibraryTheme(t, c, base, "byte-notes", true) // single.html prints {{.PostNav}} and the comment form
	golylangToggle(t, c, base, "activate")
	pllAddLanguage(t, c, base, "English", "en_US", "en", "us")
	pllAddLanguage(t, c, base, "Türkçe", "tr_TR", "tr", "tr")
	if code, obj, _ := pllREST(t, c, base, http.MethodDelete, "/wp-json/wp/v2/posts/1?force=true", nil); code != http.StatusOK {
		t.Fatalf("delete Hello world: %d %v", code, obj)
	}

	// One English post and its Turkish translation: nothing to navigate to in either language.
	enA, enALink := pllCreatePost(t, c, base, "", "PHP to Go")
	_, trALink := pllCreatePost(t, c, base, "?lang=tr&translations[en]="+strconv.FormatInt(enA, 10), "PHP uygulamalari Go gecis")
	if !strings.Contains(trALink, "/tr/") {
		t.Fatalf("Turkish permalink %s", trALink)
	}
	for _, link := range []string{enALink, trALink} {
		code, body, _ := get(t, c, link)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", link, code)
		}
		if l := postNavLinks(t, body); len(l) != 0 {
			t.Errorf("%s links across languages: %v", link, l)
		}
	}

	// A second post in each language: each post links to the other one of its own language.
	enB, enBLink := pllCreatePost(t, c, base, "", "Go modules")
	_, trBLink := pllCreatePost(t, c, base, "?lang=tr&translations[en]="+strconv.FormatInt(enB, 10), "Go modulleri")
	want := map[string]map[string]string{
		enALink: {"next": enBLink},
		enBLink: {"prev": enALink},
		trALink: {"next": trBLink},
		trBLink: {"prev": trALink},
	}
	pages := map[string]string{}
	for link, w := range want {
		_, body, _ := get(t, c, link)
		pages[link] = body
		got := postNavLinks(t, body)
		if len(got) != len(w) || got["prev"] != w["prev"] || got["next"] != w["next"] {
			t.Errorf("%s: links %v, want %v", link, got, w)
		}
	}

	// Turkish pages speak Turkish: the navigation (an admin) and the comment form (an admin and a visitor).
	for _, s := range []string{"Sonraki</span>", "Sonraki yazı:", `aria-label="Yazılar"`, "Yazılar arasında gezinme",
		"Bir yanıt yazın", "Yanıtı iptal et", "admin olarak giriş yapıldı", "Çıkış yapılsın mı?",
		`Gerekli alanlar <span class="required">*</span> ile işaretlenmişlerdir`, `>Yorum <span class="required">*</span></label>`, `value="Yorum gönder"`} {
		if !strings.Contains(pages[trALink], s) {
			t.Errorf("the Turkish post lacks %q", s)
		}
	}
	if !strings.Contains(pages[trBLink], "Önceki</span>") || !strings.Contains(pages[trBLink], "Önceki yazı:") {
		t.Error("the Turkish post lacks Önceki / Önceki yazı:")
	}
	for _, s := range []string{"Leave a Reply", "Post Comment", "Post navigation", "Next post:", "Logged in as"} {
		if strings.Contains(pages[trALink], s) {
			t.Errorf("the Turkish post still shows %q", s)
		}
	}
	anon := bodyOf(anonGet(t, trALink))
	for _, s := range []string{"E-posta adresiniz yayınlanmayacak.", `<label for="url">İnternet sitesi</label>`, "Bir yanıt yazın", `value="Yorum gönder"`} {
		if !strings.Contains(anon, s) {
			t.Errorf("the Turkish comment form for visitors lacks %q", s)
		}
	}
	// English stays English.
	for _, s := range []string{"Next</span>", "Next post:", "Post navigation", "Leave a Reply", `value="Post Comment"`, "Logged in as admin"} {
		if !strings.Contains(pages[enALink], s) {
			t.Errorf("the English post lacks %q", s)
		}
	}

	// The strings are core's: Languages › Translations lists them so a language without a pack can
	// translate them there.
	srv.pllCaptureFlush(context.Background())
	for _, s := range []string{"Leave a Reply", "Previous post:", "Required fields are marked %s"} {
		if src, dom, _, _ := pllSourceRow(t, srv, s); src != "core" || dom != "default" {
			t.Errorf("%q: source %q domain %q, want core/default", s, src, dom)
		}
	}
	if _, body, _ := get(t, c, base+"/wp-admin/admin?page=mlang_strings&lang=tr"); !strings.Contains(body, "Leave a Reply") {
		t.Error("Languages › Translations does not list Leave a Reply")
	}
}
