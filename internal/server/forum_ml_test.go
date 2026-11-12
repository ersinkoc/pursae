package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// forumSwitcherHref is the href of the language switcher menu item of language slug (item id).
func forumSwitcherHref(t *testing.T, body, item, slug string) string {
	t.Helper()
	m := regexp.MustCompile(`id="menu-item-` + item + `-` + slug + `"[^>]*>\s*<a[^>]*href="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s switcher item on the page", slug)
	}
	return strings.ReplaceAll(m[1], "&amp;", "&")
}

// Pursae Multi Language with two languages (Turkish default without a prefix, English under /en/):
// forum views answer under the prefix and keep it on every link, the switcher points to the same
// forum path in the other language, public views carry hreflang alternates, x-default and a
// prefixed canonical, private views have no hreflang, and the login and posting redirects stay
// in the language.
func TestForumMultiLanguage(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx, base := fx.srv, fx.ctx, fx.base
	golylangToggle(t, fx.admin, base, "activate")
	pllAddLanguage(t, fx.admin, base, "Türkçe", "tr_TR", "tr", "tr")
	pllAddLanguage(t, fx.admin, base, "English", "en_US", "en", "us")
	if d := srv.pllDefault(ctx); d == nil || d.Slug != "tr" || !srv.pllOpts().HideDefault {
		t.Fatalf("default language: %+v %+v", d, srv.pllOpts())
	}
	// The classic header menu with the Language switcher item (Hello Pursae prints it).
	if err := srv.wbSetActive(ctx, true); err != nil {
		t.Fatal(err)
	}
	menu, err := srv.store.CreateMenu(ctx, "Main")
	if err != nil {
		t.Fatal(err)
	}
	it := &store.EditItem{MenuID: menu, Kind: "custom", Object: "custom", URL: "#pll_switcher", Label: "Languages", Status: "publish"}
	if err := srv.store.AddMenuItem(ctx, it); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetMenuLocations(ctx, map[string]int64{"primary": menu}); err != nil {
		t.Fatal(err)
	}
	item := itoa64(it.ID)
	// English pages use their own menu locations (Manage Locations): the same menu.
	o := srv.pllOpts()
	o.NavMenus["hello-pursae"] = map[string]map[string]int64{"primary": {"en": menu}}
	if err := srv.pllSaveOpts(ctx, o); err != nil {
		t.Fatal(err)
	}
	// alice posts a topic (content is shared by every language).
	hash, _ := auth.HashPassword("member password 1")
	alice, err := srv.store.CreateUser(ctx, store.NewUser{Login: "alice", Email: "alice@example.test", PasswordHash: hash, Role: "forum_participant", DisplayName: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	topic, err := srv.forumCreateTopic(ctx, forumNewTopic{ForumID: fx.forum.ID, AuthorID: alice, Title: "Shared topic", Content: "<p>Hello</p>"})
	if err != nil {
		t.Fatal(err)
	}
	srv.purgeAll(ctx)
	trTopic := base + "/forums/t/" + itoa64(topic.ID) + "/" + topic.Slug + "/"
	enTopic := base + "/en/forums/t/" + itoa64(topic.ID) + "/" + topic.Slug + "/"

	// The index under /en/: 200, English, links keep the prefix, hreflang + x-default, canonical.
	code, idx, _ := get(t, fx.anon, base+"/en/forums/")
	if code != http.StatusOK {
		t.Fatalf("/en/forums/: %d", code)
	}
	forumNoTemplateError(t, "en index", idx)
	wbtfWant(t, "en index", idx, `lang="en-US"`, `href="`+base+`/en/forums/f/`+fx.forum.Slug+`/"`, `href="`+enTopic+`"`,
		`<link rel="canonical" href="`+base+`/en/forums/" />`,
		`<link rel="alternate" href="`+base+`/forums/" hreflang="tr" />`,
		`<link rel="alternate" href="`+base+`/en/forums/" hreflang="en" />`,
		`<link rel="alternate" href="`+base+`/forums/" hreflang="x-default" />`,
		`action="`+base+`/en/forums/search/"`, `href="`+base+`/en/forums/members/"`)
	wbtfBad(t, "en index", idx, `href="`+base+`/forums/f/`, "page_id=0")
	if got := forumSwitcherHref(t, idx, item, "tr"); got != base+"/forums/" {
		t.Errorf("index switcher → tr: %s", got)
	}
	if _, trIdx, _ := get(t, fx.anon, base+"/forums/"); forumSwitcherHref(t, trIdx, item, "en") != base+"/en/forums/" {
		t.Errorf("tr index switcher → en: %s", forumSwitcherHref(t, trIdx, item, "en"))
	}

	// The topic in both languages: the switcher targets the same topic in the other language.
	code, en, _ := get(t, fx.anon, enTopic)
	if code != http.StatusOK {
		t.Fatalf("en topic: %d", code)
	}
	wbtfWant(t, "en topic", en, "Shared topic", `href="`+base+`/en/forums/login/?redirect=%2Fen%2Fforums%2Ft%2F`, `<link rel="canonical" href="`+enTopic+`" />`,
		`<link rel="alternate" href="`+trTopic+`" hreflang="tr" />`, `<link rel="alternate" href="`+enTopic+`" hreflang="en" />`,
		`<link rel="alternate" href="`+trTopic+`" hreflang="x-default" />`, `href="`+base+`/en/forums/u/alice/"`)
	if got := forumSwitcherHref(t, en, item, "tr"); got != trTopic {
		t.Errorf("en topic switcher → tr: %s, want %s", got, trTopic)
	}
	code, tr, _ := get(t, fx.anon, trTopic)
	if code != http.StatusOK || !strings.Contains(tr, `lang="tr-TR"`) {
		t.Fatalf("tr topic: %d", code)
	}
	if got := forumSwitcherHref(t, tr, item, "en"); got != enTopic {
		t.Errorf("tr topic switcher → en: %s, want %s", got, enTopic)
	}
	if !strings.Contains(tr, `<link rel="canonical" href="`+trTopic+`" />`) {
		t.Error("tr topic canonical")
	}
	// A wrong slug under /en/ redirects within the language.
	if code, _, loc := get(t, fx.anon, base+"/en/forums/t/"+itoa64(topic.ID)+"/wrong/"); code != http.StatusMovedPermanently || loc != "/en/forums/t/"+itoa64(topic.ID)+"/"+topic.Slug+"/" {
		t.Errorf("en wrong slug: %d %s", code, loc)
	}
	// The member list under /en/.
	if code, body, _ := get(t, fx.anon, base+"/en/forums/members/"); code != http.StatusOK || !strings.Contains(body, `href="`+base+`/en/forums/u/alice/"`) {
		t.Errorf("en members: %d", code)
	}

	// A private view (login): the switcher links the same view, no hreflang, noindex.
	code, login, _ := get(t, fx.anon, base+"/en/forums/login/")
	if code != http.StatusOK {
		t.Fatalf("en login: %d", code)
	}
	if got := forumSwitcherHref(t, login, item, "tr"); got != base+"/forums/login/" {
		t.Errorf("en login switcher → tr: %s", got)
	}
	wbtfBad(t, "en login", login, "hreflang=\"tr\" />", "x-default", `rel="canonical"`)
	wbtfWant(t, "en login", login, `action="`+base+`/en/forums/login/"`, "noindex")
	// Search keeps ?q in the switcher.
	_, search, _ := get(t, fx.anon, base+"/en/forums/search/?q=shared")
	if got := forumSwitcherHref(t, search, item, "tr"); got != base+"/forums/search/?q=shared" {
		t.Errorf("en search switcher → tr: %s", got)
	}

	// A visitor opening the new-topic form under /en/ is sent to the English login, coming back.
	code, _, loc := get(t, fx.anon, base+"/en/forums/f/"+fx.forum.Slug+"/new/")
	wantLogin := base + "/en/forums/login/?redirect=" + url.QueryEscape("/en/forums/f/"+fx.forum.Slug+"/new/")
	if code != http.StatusFound || loc != wantLogin {
		t.Errorf("en new topic as a visitor: %d %s, want %s", code, loc, wantLogin)
	}
	// Logging in through the English form returns to the English page.
	c := forumJarClient()
	_, login, _ = get(t, c, wantLogin)
	code, _, loc = post(t, c, base+"/en/forums/login/", url.Values{"forum_action": {"login"}, "_wpnonce": {forumAuthFormNonce(t, login, "login")},
		"log": {"alice"}, "pwd": {"member password 1"}, "redirect": {"/en/forums/f/" + fx.forum.Slug + "/new/"}})
	if code != http.StatusFound || loc != "/en/forums/f/"+fx.forum.Slug+"/new/" {
		t.Fatalf("en login: %d %s", code, loc)
	}
	// Posting a reply from /en/ lands on the English topic page.
	_, page, _ := get(t, c, enTopic)
	code, _, loc = post(t, c, enTopic, url.Values{"forum_action": {"reply"}, "_wpnonce": {forumFormNonce(t, page, "reply")},
		"content": {"<p>A reply from the English page</p>"}})
	if code != http.StatusFound || !strings.Contains(loc, "/en/forums/t/"+itoa64(topic.ID)+"/") {
		t.Errorf("en reply: %d %s", code, loc)
	}
	// The member's reply form under /en/ posts to the English URL.
	if !strings.Contains(page, `action="`+enTopic+`#reply"`) {
		t.Error("the en reply form does not post to the English topic URL")
	}
	// A visitor's POST under /en/ goes to the English login.
	code, _, loc = post(t, fx.anon, enTopic, url.Values{"forum_action": {"reply"}, "content": {"x"}})
	if code != http.StatusFound || !strings.HasPrefix(loc, base+"/en/forums/login/?redirect=%2Fen%2Fforums%2Ft%2F") {
		t.Errorf("en visitor POST: %d %s", code, loc)
	}
}
