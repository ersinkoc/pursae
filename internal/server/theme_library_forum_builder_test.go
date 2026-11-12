package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"image"
	_ "image/png"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
	"wpgo/internal/wp"
)

// Theme library, the Pursae Forum themes that also ship Pursae Web Builder parts (Guild Hall and
// Study Circle): each package validates, works as a plain theme (native templates with the forum
// wrapper and the forum user menu), carries a forum demo whose references resolve, and ships a
// parts file whose header links the forum and whose front-page tiles link the demo forums.

var forumBuilderThemeSlugs = []string{"guild-hall", "study-circle"}

// forumThemeDemo is the demo's "forum" section as the themes write it (design §7); the importer
// (forum_demo.go) owns the real schema, this only checks that the content is consistent.
type forumThemeDemo struct {
	Title   string `json:"title"`
	Members []struct {
		Ref         string `json:"ref"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
	} `json:"members"`
	Forums []struct {
		Ref    string `json:"ref"`
		Title  string `json:"title"`
		Slug   string `json:"slug"`
		Kind   string `json:"kind"`
		Parent string `json:"parent"`
	} `json:"forums"`
	Topics []struct {
		Ref      string `json:"ref"`
		Forum    string `json:"forum"`
		Author   string `json:"author"`
		Title    string `json:"title"`
		Content  string `json:"content"`
		HoursAgo int    `json:"hours_ago"`
		Replies  []struct {
			Author   string `json:"author"`
			Content  string `json:"content"`
			HoursAgo int    `json:"hours_ago"`
		} `json:"replies"`
	} `json:"topics"`
}

func TestForumBuilderThemePackages(t *testing.T) {
	funcs := (&templates{s: &Server{}}).funcs()
	srv := &Server{themeLibrary: os.DirFS(themeLibraryDir)}
	library := map[string]libraryTheme{}
	for _, lt := range srv.dirLibraryThemes() {
		library[lt.Manifest.Slug] = lt
	}
	for _, slug := range forumBuilderThemeSlugs {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join(themeLibraryDir, slug)
			files, m, err := unpackThemeZip(zipDir(t, dir))
			if err != nil {
				t.Fatalf("package: %v", err)
			}
			if m.Slug != slug || m.Author != "Pursae" {
				t.Fatalf("manifest identity: %s %s", m.Slug, m.Author)
			}
			th, err := loadThemePackage(dir, slug)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if _, err := parsePackageTemplates(os.DirFS(dir), "templates", funcs); err != nil {
				t.Fatalf("templates: %v", err)
			}
			for _, f := range []string{"readme.md", "assets/style.css", "assets/editor-style.css", "assets/screenshot.png",
				"templates/header.html", "templates/footer.html", "templates/index.html", "templates/page.html", "templates/single.html",
				"templates/404.html", "templates/searchform.html", "templates/content-none.html", "templates/forum.html",
				"templates/front-page.html", "web-builder/parts.json"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("%s missing", f)
				}
			}

			// Native theme: the forum user menu in the header, the forum wrapper with the body, the CSS contract.
			header, _ := os.ReadFile(filepath.Join(dir, "templates", "header.html"))
			if !strings.Contains(string(header), "{{forum_user_menu .}}") {
				t.Error("the header has no forum user menu (forum_user_menu)")
			}
			wrapper, _ := os.ReadFile(filepath.Join(dir, "templates", "forum.html"))
			for _, want := range []string{".Content", "forum_stats", "forum_recent_topics", `theme_mod . "layout"`} {
				if !strings.Contains(string(wrapper), want) {
					t.Errorf("templates/forum.html does not use %s", want)
				}
			}
			css, _ := os.ReadFile(filepath.Join(dir, "assets", "style.css"))
			for _, sel := range []string{".pf-topic-row", ".pf-post", ".pf-usermenu", ".pf-recent", ".pf-stats", "--pf-accent:", "body .pf"} {
				if !strings.Contains(string(css), sel) {
					t.Errorf("style.css does not style %s", sel)
				}
			}
			if strings.Contains(string(css), "<script") {
				t.Error("style.css carries markup")
			}

			// Library metadata.
			if m.Supports["pursae-forum"] != true {
				t.Error(`supports["pursae-forum"] is not true`)
			}
			if !slices.Contains(themeCategories, m.Category) || m.Style == "" || len(m.Style) > 80 {
				t.Errorf("library metadata: category %q style %q", m.Category, m.Style)
			}
			if m.Demo != "demo/demo.json" || m.WebBuilder != "web-builder/parts.json" {
				t.Errorf("demo %q, web_builder %q", m.Demo, m.WebBuilder)
			}
			var accent, layout bool
			for _, sec := range m.Customizer.Sections {
				for _, st := range sec.Settings {
					accent = accent || (st.ID == "accent" && st.Type == "color" && st.CSSVar == "--pf-accent")
					layout = layout || (st.ID == "layout" && st.Type == "select" && strings.Contains(st.BodyClass, "{value}") && len(st.Choices) == 2)
				}
			}
			if !accent || !layout {
				t.Errorf("customizer: accent %v, layout %v", accent, layout)
			}

			// Screenshot 1200x900.
			shot, err := os.ReadFile(filepath.Join(dir, "assets", "screenshot.png"))
			if err != nil {
				t.Fatal(err)
			}
			if cfg, format, err := image.DecodeConfig(bytes.NewReader(shot)); err != nil || format != "png" || cfg.Width != 1200 || cfg.Height != 900 {
				t.Errorf("screenshot: %s %dx%d %v", format, cfg.Width, cfg.Height, err)
			}

			// Demo: images, core sections, the forum section.
			inPkg := map[string]int{}
			for _, f := range files {
				inPkg[f.Path] = len(f.Data)
				if strings.HasPrefix(f.Path, "demo/images/") {
					if !strings.HasPrefix(path.Base(f.Path), slug+"-") {
						t.Errorf("%s: demo images are prefixed with the theme slug", f.Path)
					}
					if len(f.Data) > 1<<20 {
						t.Errorf("%s is %d KB (max 1 MiB)", f.Path, len(f.Data)>>10)
					}
				}
			}
			d, err := themeDemoOf(th)
			if err != nil {
				t.Fatalf("demo: %v", err)
			}
			media := map[string]bool{}
			for _, md := range d.Media {
				media[md.ID] = true
				if _, ok := inPkg[md.File]; !ok {
					t.Errorf("media %s: %s not in the package", md.ID, md.File)
				}
			}
			if !media["hero"] || !media["about"] {
				t.Errorf("the demo has no hero / about image (the parts use both): %v", media)
			}
			if errs := forumValidateDemo(d.Forum, d.Media); len(errs) > 0 {
				t.Errorf("forum demo: %v", errs)
			}
			pages := map[string]bool{}
			front := false
			for _, p := range d.Pages {
				pages[p.Slug] = true
				front = front || (p.FrontPage && p.Slug == "home")
				if strings.TrimSpace(p.Content) == "" {
					t.Errorf("page %s has no content", p.Slug)
				}
			}
			if !front || !pages["guidelines"] || !pages["about"] {
				t.Errorf("demo pages: %v (front page home: %v)", pages, front)
			}
			forumsLink := false
			for _, mn := range d.Menus {
				if mn.Location != "primary" {
					continue
				}
				for _, it := range mn.Items {
					forumsLink = forumsLink || it.URL == "/forums/"
				}
			}
			if !forumsLink {
				t.Error("the primary menu has no Forums (/forums/) item")
			}
			fd := checkForumThemeDemo(t, d.Forum)

			// Web Builder parts: valid, the header links the forum and carries the tokens, the tiles link the demo forums.
			if !strings.Contains(string(forumBuilderRead(t, filepath.Join(dir, "web-builder", "parts.json"))), `"format": "web-builder-theme"`) {
				t.Error(`parts.json: format is not "web-builder-theme"`)
			}
			lt, ok := library[slug]
			if !ok {
				t.Fatal("not in the library listing")
			}
			pack, _, err := srv.wbThemePackOf(context.Background(), &lt)
			if err != nil || pack == nil {
				t.Fatalf("parts: %v", err)
			}
			if pack.Format != "web-builder-theme" {
				t.Errorf("parts format %q", pack.Format)
			}
			docs := map[string]string{}
			for _, tp := range pack.Templates {
				docs[tp.Type] = string(tp.Doc)
				res, perr := webbuilder.ParseDoc(wbThemeTokens(tp.Doc, map[string]string{"hero": "/x.jpg", "about": "/y.jpg"}), webbuilder.DocOptions{UnfilteredHTML: true})
				if perr != nil || !res.Valid() {
					t.Errorf("%s: %v %v", tp.Key, perr, res.Errors)
				}
			}
			for _, want := range []string{"header", "footer", "front-page", "single-post", "single-page", "archive", "search-results", "error-404"} {
				if docs[want] == "" {
					t.Errorf("no %s template", want)
				}
			}
			for _, want := range []string{`"/forums/login/"`, `"/forums/register/"`, `"menu":"primary"`, "--pf-accent:"} {
				if !strings.Contains(forumBuilderCompactJSON(docs["header"]), want) {
					t.Errorf("the header part has no %s", want)
				}
			}
			// The header's custom CSS (the forum tokens under Hello Pursae) survives the sanitizer whole:
			// a dropped rule would silently leave the forum in the default colours.
			var headerDoc struct {
				Sections []struct {
					Style struct {
						CSS string `json:"css"`
					} `json:"style"`
				} `json:"sections"`
			}
			if err := json.Unmarshal([]byte(docs["header"]), &headerDoc); err != nil {
				t.Fatalf("header doc: %v", err)
			}
			tokens := false
			for _, sec := range headerDoc.Sections {
				if sec.Style.CSS == "" {
					continue
				}
				tokens = tokens || strings.Contains(sec.Style.CSS, "body .pf{")
				if _, changed, err := webbuilder.SanitizeCSS(sec.Style.CSS, webbuilder.CSSOptions{}); err != nil || changed {
					t.Errorf("header CSS is changed by the sanitizer (%v): %s", err, sec.Style.CSS)
				}
			}
			if !tokens {
				t.Error("the header CSS sets no forum tokens on body .pf")
			}
			kinds := map[string]string{}
			for _, f := range fd.Forums {
				kinds[f.Slug] = f.Kind
			}
			tiles := regexp.MustCompile(`"href":"/forums/f/([^/"]+)/"`).FindAllStringSubmatch(forumBuilderCompactJSON(docs["front-page"]), -1)
			if len(tiles) < 4 {
				t.Errorf("%d forum tiles on the front page, want at least 4", len(tiles))
			}
			for _, tm := range tiles {
				if kinds[tm[1]] != "forum" {
					t.Errorf("tile /forums/f/%s/ links no demo forum", tm[1])
				}
			}
			var slugs []string
			for _, pg := range pack.Pages {
				slugs = append(slugs, pg.Slug)
			}
			slices.Sort(slugs)
			if !slices.Equal(slugs, []string{"about", "guidelines"}) {
				t.Errorf("parts pages: %v", slugs)
			}
		})
	}
}

// checkForumThemeDemo checks the forum section against the design limits (unique refs, two levels,
// topics in forums, known authors) and that every forum carries an explicit slug (equal to its ref and
// to its title's slug), so the tiles' /forums/f/<slug>/ links resolve after the import.
func checkForumThemeDemo(t *testing.T, raw json.RawMessage) *forumThemeDemo {
	t.Helper()
	var fd forumThemeDemo
	if err := json.Unmarshal(raw, &fd); err != nil {
		t.Fatalf("forum section: %v", err)
	}
	if fd.Title == "" || len(fd.Members) == 0 || len(fd.Members) > 12 || len(fd.Forums) == 0 || len(fd.Forums) > 30 ||
		len(fd.Topics) < 8 || len(fd.Topics) > 50 {
		t.Fatalf("forum section sizes: title %q, %d members, %d forums, %d topics", fd.Title, len(fd.Members), len(fd.Forums), len(fd.Topics))
	}
	authors := map[string]bool{"admin": true}
	moderators := 0
	for _, m := range fd.Members {
		if authors[m.Ref] || m.Ref == "" || m.DisplayName == "" {
			t.Errorf("member %+v: empty or duplicate", m)
		}
		authors[m.Ref] = true
		if m.Role != "forum_participant" && m.Role != "forum_moderator" {
			t.Errorf("member %s: role %q", m.Ref, m.Role)
		}
		if m.Role == "forum_moderator" {
			moderators++
		}
	}
	if moderators == 0 {
		t.Error("the demo has no moderator")
	}
	kind := map[string]string{}
	for _, f := range fd.Forums {
		if kind[f.Ref] != "" {
			t.Errorf("forum ref %s used twice", f.Ref)
		}
		k := defaultStr(f.Kind, "forum")
		kind[f.Ref] = k
		if f.Slug == "" || f.Slug != f.Ref || f.Slug != wp.SanitizeTitle(f.Title) {
			t.Errorf("forum %q: slug %q, ref %q, title slug %q (all three must agree)", f.Title, f.Slug, f.Ref, wp.SanitizeTitle(f.Title))
		}
	}
	for _, f := range fd.Forums {
		switch {
		case f.Kind == "category" && f.Parent != "":
			t.Errorf("category %s has a parent", f.Ref)
		case f.Kind != "category" && f.Parent != "" && kind[f.Parent] != "category":
			t.Errorf("forum %s: parent %q is not a category", f.Ref, f.Parent)
		}
	}
	replies, seen := 0, map[string]bool{}
	for _, tp := range fd.Topics {
		if seen[tp.Ref] || tp.Ref == "" {
			t.Errorf("topic ref %q empty or used twice", tp.Ref)
		}
		seen[tp.Ref] = true
		if kind[tp.Forum] != "forum" {
			t.Errorf("topic %s: forum %q is not a forum", tp.Ref, tp.Forum)
		}
		if !authors[tp.Author] || tp.Title == "" || tp.Content == "" || len(tp.Content) > 20<<10 {
			t.Errorf("topic %s: author %q, title %q, %d bytes", tp.Ref, tp.Author, tp.Title, len(tp.Content))
		}
		for _, r := range tp.Replies {
			replies++
			if !authors[r.Author] || r.Content == "" || len(r.Content) > 20<<10 || r.HoursAgo > tp.HoursAgo {
				t.Errorf("topic %s: reply by %q (%dh after a %dh topic)", tp.Ref, r.Author, r.HoursAgo, tp.HoursAgo)
			}
		}
	}
	if replies == 0 || replies > 300 {
		t.Errorf("%d replies", replies)
	}
	return &fd
}

func forumBuilderRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// forumBuilderCompactJSON drops the insignificant whitespace of a JSON document (for substring checks).
func forumBuilderCompactJSON(s string) string {
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

// TestForumBuilderThemesInstall installs, activates and imports each theme as a plain theme, then
// renders the home page, pages, posts, the forum index, every demo forum and topic, search, a
// member profile, log in and join inside it (anonymous, and a topic as a logged-in moderator with
// the reply form), checks the Customizer layout and accent, and removes the demo again.
func TestForumBuilderThemesInstall(t *testing.T) {
	t.Setenv("WPGO_THEMES_DIR", t.TempDir())
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Forum themes"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK {
		t.Fatalf("install: %d %s", code, body)
	}
	if code, _, _ := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}}); code != http.StatusFound {
		t.Fatalf("login: %d", code)
	}
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)
	ctx := context.Background()
	// A post with a comment, so single.html prints the comment list (the demo import trashes the
	// install's own sample post).
	now := time.Now()
	commented := &store.Post{Type: "post", AuthorID: 1, Status: "publish", Title: "Patch notes digest", Slug: "patch-notes-digest",
		Content: "<p>What changed this week.</p>", CommentStatus: "open", PublishedAt: &now}
	if err := srv.store.InsertPost(ctx, commented); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.InsertComment(ctx, &store.Comment{PostID: commented.ID, AuthorName: "Visitor", AuthorEmail: "v@example.test",
		Content: "A first comment.", Status: "approved"}); err != nil {
		t.Fatal(err)
	}
	postPath := "/?p=" + strconv.FormatInt(commented.ID, 10)
	if r := anonGet(t, base+postPath); r.StatusCode/100 == 3 {
		postPath = strings.TrimPrefix(r.Header.Get("Location"), base)
	}
	rel := func(u string) string {
		pu, err := url.Parse(u)
		if err != nil {
			t.Fatalf("bad URL %q", u)
		}
		return pu.RequestURI()
	}
	stray := regexp.MustCompile(`>\s*(true|false|&lt;no value&gt;|<no value>|&lt;nil&gt;|<nil>)\s*<`) // a bool or nil printed by a template
	for _, slug := range forumBuilderThemeSlugs {
		t.Run(slug, func(t *testing.T) {
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", zipDir(t, filepath.Join(themeLibraryDir, slug)))
			if code != http.StatusFound || loc != "/wp-admin/themes?installed="+slug {
				t.Fatalf("upload: %d %q %s", code, loc, body)
			}
			t.Cleanup(func() { unregisterTheme(slug) })
			_, body, _ = get(t, c, base+"/wp-admin/themes")
			m := regexp.MustCompile(`href="(/wp-admin/themes\?action=activate&amp;stylesheet=` + slug + `&amp;_wpnonce=[^"]+)"`).FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("no activate link for %s", slug)
			}
			if code, _, _ := get(t, c, base+html.UnescapeString(m[1])); code != http.StatusFound {
				t.Fatalf("activate: %d", code)
			}
			if _, err := srv.importThemeDemo(ctx, 1, themeBySlug(slug)); err != nil {
				t.Fatalf("import: %v", err)
			}
			if !srv.forumOn() {
				t.Fatal("the demo import did not switch Pursae Forum on")
			}
			prefix := map[string]string{"guild-hall": "gh", "study-circle": "sc"}[slug]
			checkPage := func(what string, resp *http.Response, status int, wants ...string) string {
				t.Helper()
				page := bodyOf(resp)
				if resp.StatusCode != status {
					t.Errorf("%s: status %d, want %d", what, resp.StatusCode, status)
				}
				if strings.Contains(strings.ToLower(page), "template error") {
					t.Errorf("%s: template error in the page", what)
				}
				if m := stray.FindString(page); m != "" {
					t.Errorf("%s: stray template value %q", what, m)
				}
				for _, w := range append([]string{"wp-theme-" + slug}, wants...) {
					if !strings.Contains(page, w) {
						t.Errorf("%s: no %q", what, w)
					}
				}
				return page
			}
			check := func(u string, wants ...string) string {
				t.Helper()
				return checkPage(u, anonGet(t, base+u), http.StatusOK, wants...)
			}
			// The forum wrapper (templates/forum.html) with the sidebar of the default "sidebar" layout.
			wrapper := []string{`class="` + prefix + `-wrap ` + prefix + `-forum ` + prefix + `-forum--sidebar"`, prefix + "-forum__side", "pursae-forum",
				"/forum-assets/forum.css", `class="pf-usermenu"`, "pf-usermenu-login", prefix + "-layout-sidebar"}
			check("/", `class="pf-usermenu"`, prefix+"-hero", "/forums/f/", prefix+"-live", `class="pf-stats"`, `class="pf-recent"`)
			check("/guidelines/", prefix+"-entry__title")
			check(postPath, prefix+"-entry__title", `<ol class="comment-list">`, "A first comment.", `id="commentform"`)
			check("/?s=patch", prefix+"-cards", "Patch notes digest")
			check("/?s=zzzqqq-nothing", prefix+"-empty")
			check("/author/admin/", prefix+"-page-title", prefix+"-card", "Patch notes digest")
			checkPage("/no-such-page-here/", anonGet(t, base+"/no-such-page-here/"), http.StatusNotFound, prefix+"-404", `role="search"`)

			// The forum: the index, every demo forum and every demo topic render inside the theme.
			var fd forumThemeDemo
			d, _ := themeDemoOf(themeBySlug(slug))
			if err := json.Unmarshal(d.Forum, &fd); err != nil {
				t.Fatal(err)
			}
			idx := check("/forums/", append([]string{`class="pf alignwide" data-view="index"`, `<h1 class="pf-title">` + html.EscapeString(fd.Title) + `</h1>`,
				`class="pf-stats"`, `class="pf-recent"`}, wrapper...)...)
			if strings.Count(idx, "<h1") != 1 {
				t.Errorf("/forums/: %d h1 elements", strings.Count(idx, "<h1"))
			}
			for _, f := range fd.Forums {
				ff, err := srv.forumBySlug(ctx, f.Slug)
				if err != nil {
					t.Errorf("demo forum %s: %v", f.Slug, err)
					continue
				}
				if !strings.Contains(idx, `/forums/f/`+ff.Slug+`/"`) {
					t.Errorf("/forums/ does not link the forum %s", ff.Slug)
				}
				check("/forums/f/"+ff.Slug+"/", append([]string{`data-view="forum"`, html.EscapeString(ff.Title)}, wrapper...)...)
			}
			topics, total, err := srv.forumTopics(ctx, forumTopicQuery{PerPage: 50})
			if err != nil || total != len(fd.Topics) || len(topics) != len(fd.Topics) {
				t.Fatalf("demo topics: %d of %d (%d in the demo), %v", len(topics), total, len(fd.Topics), err)
			}
			profile := ""
			for _, tp := range topics {
				page := check(rel(tp.URL), append([]string{`data-view="topic"`, `<h1 class="pf-title">` + html.EscapeString(tp.Title) + `</h1>`,
					"pf-post--lead", "pf-join"}, wrapper...)...)
				if tp.Author != nil && tp.Author.ProfileURL != "" && profile == "" && strings.Contains(page, tp.Author.ProfileURL) {
					profile = rel(tp.Author.ProfileURL)
				}
			}
			if profile == "" {
				t.Error("no topic links its author's profile")
			} else {
				check(profile, append([]string{`data-view="profile"`, "pf-profile"}, wrapper...)...)
			}
			check("/forums/search/?q="+url.QueryEscape(strings.Fields(topics[0].Title)[0]), `data-view="search"`, "pf-results", prefix+"-forum")
			// Log in and join: the card alone (the theme CSS hides the sidebar on form views), the body class it keys on.
			check("/forums/login/", `data-view="login"`, "pf-view-login", "pf-auth", `name="log"`, prefix+"-forum")
			check("/forums/register/", `data-view="register"`, "pf-view-register", "pf-auth", `name="user_login"`, prefix+"-forum")
			// A moderator on a topic: the reply form and the moderation bar, still inside the theme.
			code, page, _ := get(t, c, base+rel(topics[0].URL))
			if code != http.StatusOK || strings.Contains(strings.ToLower(page), "template error") ||
				!strings.Contains(page, "wp-theme-"+slug) || !strings.Contains(page, "pf-reply") || !strings.Contains(page, "pf-modbar") ||
				!strings.Contains(page, "pf-usermenu-account") {
				t.Errorf("logged-in topic: %d, reply form %v, modbar %v, account menu %v", code,
					strings.Contains(page, "pf-reply"), strings.Contains(page, "pf-modbar"), strings.Contains(page, "pf-usermenu-account"))
			}

			// The Customizer: full width drops the sidebar from the page, the accent reaches the forum tokens.
			if err := srv.settings.Set(ctx, map[string]any{"theme_mods_" + slug: map[string]any{"layout": "full", "accent": "#336699"}}); err != nil {
				t.Fatal(err)
			}
			srv.purgeAll(ctx)
			full := check("/forums/", `class="`+prefix+`-wrap `+prefix+`-forum"`, prefix+"-layout-full", "--pf-accent:#336699")
			if strings.Contains(full, prefix+"-forum__side") {
				t.Error("the full width layout still prints the forum sidebar")
			}
			if err := srv.settings.Set(ctx, map[string]any{"theme_mods_" + slug: map[string]any{}}); err != nil {
				t.Fatal(err)
			}
			srv.purgeAll(ctx)

			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatal(err)
			}
			if left, _, _ := srv.forumTopics(ctx, forumTopicQuery{PerPage: 50}); len(left) != 0 {
				t.Errorf("removing the demo left %d topics", len(left))
			}
		})
	}
}
