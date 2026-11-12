package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"html/template"
	"image"
	_ "image/png"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// Theme library, Pursae Forum native themes (Campfire, Devboard): each package validates, declares
// forum support, calls the forum template functions in its header and forum wrapper, styles the
// forum markup, reuses fonts other library themes already ship, has a 1200x900 screenshot and a demo
// whose forum section follows the demo schema (design §7). Campfire's forum home override renders
// category cards from a forumPage. The install test activates each theme with the forum on and
// renders the home page, the forum index and a page inside it.

var forumNativeThemeSlugs = []string{"campfire", "devboard"}

// forumNativeDemo is the demo "forum" section (design §7), decoded strictly enough to check refs.
type forumNativeDemo struct {
	Title   string `json:"title"`
	Members []struct {
		Ref         string `json:"ref"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Bio         string `json:"bio"`
	} `json:"members"`
	Forums []struct {
		Ref         string `json:"ref"`
		Title       string `json:"title"`
		Kind        string `json:"kind"`
		Parent      string `json:"parent"`
		Description string `json:"description"`
		Order       int    `json:"order"`
		Closed      bool   `json:"closed"`
	} `json:"forums"`
	Topics []struct {
		Ref      string `json:"ref"`
		Forum    string `json:"forum"`
		Author   string `json:"author"`
		Title    string `json:"title"`
		Content  string `json:"content"`
		Sticky   bool   `json:"sticky"`
		Closed   bool   `json:"closed"`
		HoursAgo int    `json:"hours_ago"`
		Replies  []struct {
			Author   string `json:"author"`
			Content  string `json:"content"`
			HoursAgo int    `json:"hours_ago"`
		} `json:"replies"`
	} `json:"topics"`
}

func TestForumNativeThemePackages(t *testing.T) {
	funcs := (&templates{s: &Server{}}).funcs()
	otherFonts := forumNativeLibraryFonts(t)
	for _, slug := range forumNativeThemeSlugs {
		t.Run(slug, func(t *testing.T) {
			dir := filepath.Join(themeLibraryDir, slug)
			if _, err := os.Stat(dir); err != nil {
				t.Fatalf("missing: %v", err)
			}
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
			set, err := parsePackageTemplates(os.DirFS(dir), "templates", funcs)
			if err != nil {
				t.Fatalf("templates: %v", err)
			}
			for _, f := range []string{"readme.md", "theme.json", "assets/style.css", "assets/editor-style.css", "assets/screenshot.png",
				"templates/header.html", "templates/footer.html", "templates/index.html", "templates/page.html", "templates/single.html",
				"templates/404.html", "templates/searchform.html", "templates/content-none.html", "templates/forum.html",
				"templates/front-page.html", "demo/demo.json"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("%s missing", f)
				}
			}
			read := func(f string) string {
				b, _ := os.ReadFile(filepath.Join(dir, f))
				return string(b)
			}
			if !strings.Contains(read("templates/header.html"), "{{with forum_user_menu .}}") {
				t.Error("the header does not print forum_user_menu")
			}
			wrapper := read("templates/forum.html")
			for _, want := range []string{".Post.Content", `theme_mod . "layout"`, `"header"`, `"footer"`} {
				if !strings.Contains(wrapper, want) {
					t.Errorf("templates/forum.html lacks %s", want)
				}
			}
			sidebar := wrapper + read("templates/sidebar.html")
			for _, fn := range []string{"forum_stats .", "forum_recent_topics ."} {
				if !strings.Contains(sidebar, fn) {
					t.Errorf("the forum sidebar does not call %s", fn)
				}
			}
			if set.Lookup("forum") == nil || set.Lookup("front-page") == nil {
				t.Error("forum or front-page template not in the set")
			}
			css := read("assets/style.css")
			for _, sel := range []string{".pf-topic-row", ".pf-post", ".pf-post--lead", ".pf-forum-row", ".pf-usermenu", ".pf-recent", ".pf-stats",
				".pf-button--primary", ".pf-input", ".pf-notice--error", ".pf-badge", ".pf-pagination", ".pf-card", "--pf-accent", "--pf-font-heading",
				// Pursae Forum declares its token defaults on .pf and on the standalone widgets, so the theme
				// re-declares them on the same elements (a :root value alone would be shadowed).
				"body :is(.pf, .pf-usermenu, .pf-recent, .pf-stats)"} {
				if !strings.Contains(css, sel) {
					t.Errorf("style.css does not style %s", sel)
				}
			}
			if strings.Contains(css, "@import") || regexp.MustCompile(`url\(["']?https?:`).MatchString(css) {
				t.Error("style.css loads remote resources (fonts must be self-hosted)")
			}

			// Library metadata and forum support.
			var meta struct {
				Category, Style, Demo string
				Supports              map[string]any
				Scripts               []any
				Customizer            struct {
					Sections []struct {
						Settings []struct {
							ID        string `json:"id"`
							Type      string `json:"type"`
							CSSVar    string `json:"css_var"`
							BodyClass string `json:"body_class"`
							Choices   []struct {
								Value string `json:"value"`
							} `json:"choices"`
						}
					}
				}
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(read("theme.json")), &raw); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal([]byte(read("theme.json")), &meta)
			if meta.Supports["pursae-forum"] != true || th.Supports["pursae-forum"] != true || !forumThemeSupports(th) {
				t.Errorf("supports pursae-forum: %v", meta.Supports)
			}
			if !slices.Contains(themeCategories, meta.Category) || meta.Style == "" || len(meta.Style) > 80 || meta.Demo != "demo/demo.json" {
				t.Errorf("library metadata: category %q style %q demo %q", meta.Category, meta.Style, meta.Demo)
			}
			if len(meta.Scripts) != 0 {
				t.Error("the theme ships scripts; the front end must work without JavaScript")
			}
			settings := map[string]int{}
			for _, sec := range meta.Customizer.Sections {
				for _, st := range sec.Settings {
					switch st.ID {
					case "accent":
						if st.Type == "color" && st.CSSVar == "--pf-accent" {
							settings["accent"]++
						}
					case "layout":
						var vals []string
						for _, ch := range st.Choices {
							vals = append(vals, ch.Value)
						}
						slices.Sort(vals)
						if st.Type == "select" && strings.Contains(st.BodyClass, "{value}") && slices.Equal(vals, []string{"full", "sidebar"}) {
							settings["layout"]++
						}
					}
				}
			}
			if settings["accent"] != 1 || settings["layout"] != 1 {
				t.Errorf("customizer accent/layout: %v", settings)
			}

			// Screenshot: a 1200x900 PNG.
			if sf, err := os.Open(filepath.Join(dir, "assets", "screenshot.png")); err != nil {
				t.Error(err)
			} else {
				cfg, format, err := image.DecodeConfig(sf)
				sf.Close()
				if err != nil || format != "png" || cfg.Width != 1200 || cfg.Height != 900 {
					t.Errorf("screenshot: %v %s %dx%d", err, format, cfg.Width, cfg.Height)
				}
			}

			// Fonts: woff2 only, each one already shipped by another library theme.
			inPkg := map[string]int{}
			for _, f := range files {
				inPkg[f.Path] = len(f.Data)
				if strings.HasPrefix(f.Path, "assets/fonts/") {
					if path.Ext(f.Path) != ".woff2" {
						t.Errorf("%s: fonts are woff2", f.Path)
					}
					if !otherFonts[string(f.Data)] {
						t.Errorf("%s is not a font another library theme ships", f.Path)
					}
				}
				if strings.HasPrefix(f.Path, "demo/images/") {
					if !strings.HasPrefix(path.Base(f.Path), slug+"-") {
						t.Errorf("%s: demo images are prefixed with the theme slug", f.Path)
					}
					if len(f.Data) > 1<<20 {
						t.Errorf("%s is %d KB (max 1 MiB)", f.Path, len(f.Data)>>10)
					}
				}
			}
			for _, pf := range th.PreloadFonts {
				if _, ok := inPkg["assets/"+strings.TrimPrefix(pf, "assets/")]; !ok {
					t.Errorf("preload font %s not in the package", pf)
				}
			}

			// Demo: core sections validate, the forum section follows the schema.
			d, err := themeDemoOf(th)
			if err != nil || d == nil {
				t.Fatalf("demo: %v", err)
			}
			if probs := forumValidateDemo(d.Forum, d.Media); len(probs) > 0 {
				t.Errorf("forumValidateDemo: %v", probs)
			}
			for _, md := range d.Media {
				if _, ok := inPkg[md.File]; !ok {
					t.Errorf("media %s: %s not in the package", md.ID, md.File)
				}
			}
			pages := map[string]bool{}
			front := ""
			for _, p := range d.Pages {
				pages[p.Slug] = true
				if p.FrontPage {
					front = p.Slug
				}
			}
			if front != "home" || !pages["guidelines"] || !pages["about"] {
				t.Errorf("demo pages: %v (front page %q)", pages, front)
			}
			forumsItem := false
			for _, mn := range d.Menus {
				for _, it := range mn.Items {
					if mn.Location == "primary" && it.URL == "/forums/" {
						forumsItem = true
					}
				}
			}
			if !forumsItem {
				t.Error("the primary menu has no Forums (/forums/) item")
			}
			forumNativeCheckDemo(t, d.Forum)

			if slug == "campfire" {
				forumNativeCheckCampfireIndex(t, set)
			}
		})
	}
}

// forumNativeLibraryFonts returns the contents of every woff2 shipped by a library theme other than
// the forum native themes.
func forumNativeLibraryFonts(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(themeLibraryDir, "*", "assets", "fonts", "*.woff2"))
	for _, f := range matches {
		owner := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(f))))
		if slices.Contains(forumNativeThemeSlugs, owner) {
			continue
		}
		if b, err := os.ReadFile(f); err == nil {
			out[string(b)] = true
		}
	}
	return out
}

// forumNativeCheckDemo applies the design §7 rules to a demo's forum section: refs unique per kind,
// a parent is a top-level category, topics live in forums (not categories), authors are "admin" or a
// member ref, replies come after their topic, limits and content size.
func forumNativeCheckDemo(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("the demo has no forum section")
	}
	var fd forumNativeDemo
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&fd); err != nil {
		t.Fatalf("forum section: %v", err)
	}
	if fd.Title == "" {
		t.Error("forum section: no title")
	}
	if len(fd.Members) == 0 || len(fd.Members) > 12 || len(fd.Forums) > 30 || len(fd.Topics) < 6 || len(fd.Topics) > 50 {
		t.Errorf("forum section sizes: %d members, %d forums, %d topics", len(fd.Members), len(fd.Forums), len(fd.Topics))
	}
	refRe := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	members := map[string]bool{}
	for _, m := range fd.Members {
		if !refRe.MatchString(m.Ref) || members[m.Ref] || m.Ref == "admin" {
			t.Errorf("member ref %q", m.Ref)
		}
		members[m.Ref] = true
		if m.DisplayName == "" || (m.Role != forumRoleParticipant && m.Role != forumRoleModerator) {
			t.Errorf("member %s: name %q role %q", m.Ref, m.DisplayName, m.Role)
		}
	}
	kinds := map[string]string{}
	parents := map[string]string{}
	for _, f := range fd.Forums {
		if !refRe.MatchString(f.Ref) || kinds[f.Ref] != "" {
			t.Errorf("forum ref %q", f.Ref)
		}
		kind := f.Kind
		if kind == "" {
			kind = "forum"
		}
		if kind != "forum" && kind != "category" {
			t.Errorf("forum %s: kind %q", f.Ref, f.Kind)
		}
		if f.Title == "" {
			t.Errorf("forum %s: no title", f.Ref)
		}
		kinds[f.Ref], parents[f.Ref] = kind, f.Parent
	}
	categories, open := 0, 0
	for ref, kind := range kinds {
		p := parents[ref]
		switch {
		case kind == "category" && p != "":
			t.Errorf("category %s has a parent", ref)
		case p != "" && (kinds[p] != "category" || parents[p] != ""):
			t.Errorf("forum %s: parent %q is not a top-level category", ref, p)
		}
		if kind == "category" {
			categories++
		}
	}
	topics := map[string]bool{}
	replies := 0
	for _, tp := range fd.Topics {
		if !refRe.MatchString(tp.Ref) || topics[tp.Ref] {
			t.Errorf("topic ref %q", tp.Ref)
		}
		topics[tp.Ref] = true
		if kinds[tp.Forum] != "forum" {
			t.Errorf("topic %s: forum %q is not a forum", tp.Ref, tp.Forum)
		}
		if tp.Author != "admin" && !members[tp.Author] {
			t.Errorf("topic %s: author %q", tp.Ref, tp.Author)
		}
		if n := len([]rune(tp.Title)); n < 3 || n > 120 {
			t.Errorf("topic %s: title length %d", tp.Ref, n)
		}
		if len(tp.Content) < 2 || len(tp.Content) > 20000 || strings.Contains(tp.Content, "<script") {
			t.Errorf("topic %s: content", tp.Ref)
		}
		if tp.HoursAgo <= 0 {
			t.Errorf("topic %s: hours_ago %d", tp.Ref, tp.HoursAgo)
		}
		for i, r := range tp.Replies {
			if r.Author != "admin" && !members[r.Author] {
				t.Errorf("topic %s reply %d: author %q", tp.Ref, i, r.Author)
			}
			if len(r.Content) < 2 || len(r.Content) > 20000 || strings.Contains(r.Content, "<script") {
				t.Errorf("topic %s reply %d: content", tp.Ref, i)
			}
			if r.HoursAgo < 0 || r.HoursAgo >= tp.HoursAgo {
				t.Errorf("topic %s reply %d: hours_ago %d is not after the topic (%d)", tp.Ref, i, r.HoursAgo, tp.HoursAgo)
			}
			if i > 0 && r.HoursAgo > tp.Replies[i-1].HoursAgo {
				t.Errorf("topic %s reply %d is older than the reply before it", tp.Ref, i)
			}
		}
		replies += len(tp.Replies)
		if !tp.Closed {
			open++
		}
	}
	if categories < 2 || replies < 12 || replies > 300 || open == 0 {
		t.Errorf("forum section: %d categories, %d replies, %d open topics", categories, replies, open)
	}
}

// forumNativeCheckCampfireIndex executes Campfire's forum home override with a forumPage holding a
// category tree, then with no forums.
func forumNativeCheckCampfireIndex(t *testing.T, set *template.Template) {
	t.Helper()
	tpl := set.Lookup("forum/index")
	if tpl == nil {
		t.Fatal("campfire does not override forum/index (templates/forum/index.html)")
	}
	now := time.Now()
	wood := &forumForum{ID: 11, ParentID: 10, Slug: "woodworking", Title: "Woodworking", Kind: "forum", URL: "/forums/f/woodworking/",
		Description: "<p>Joinery &amp; finishes.</p>", TopicCount: 12, ReplyCount: 48}
	news := &forumForum{ID: 12, ParentID: 10, Slug: "news", Title: "Announcements", Kind: "forum", Closed: true, URL: "/forums/f/news/"}
	cat := &forumForum{ID: 10, Slug: "workshop", Title: "The Workshop", Kind: "category", URL: "/forums/f/workshop/", TopicCount: 12, ReplyCount: 48,
		LastActive: now.Add(-2 * time.Hour), LastTopicTitle: "Hand-cut <dovetails>", LastTopicURL: "/forums/t/5/dovetails/",
		LastAuthor: &forumAuthor{ID: 2, DisplayName: "Mara Lind"}, Children: []*forumForum{wood, news}}
	lone := &forumForum{ID: 20, Slug: "lounge", Title: "Lounge", Kind: "forum", URL: "/forums/f/lounge/"}
	one := &forumForum{ID: 13, ParentID: 10, Slug: "metal", Title: "Metal", Kind: "forum", URL: "/forums/f/metal/", TopicCount: 1}
	cat.Children = append(cat.Children, one)
	topics := []*forumTopic{
		{ID: 5, Title: "Hand-cut <dovetails>", URL: "/forums/t/5/dovetails/", ForumTitle: "Woodworking", ReplyCount: 1, LastActive: now.Add(-3 * time.Hour), Sticky: true},
		{ID: 6, Title: "Bench tour", URL: "/forums/t/6/bench-tour/", ForumTitle: "Lounge", ReplyCount: 4, LastActive: now.Add(-26 * time.Hour)},
	}
	p := &forumPage{View: forumViewIndex, Title: "Campfire Community", OwnTitle: true, Forums: []*forumForum{cat, lone}, Topics: topics,
		URLs:    forumURLs{Index: "/forums/", Search: "/forums/search/", Account: "/forums/account/", NewTopic: "/forums/new/"},
		CanPost: true, Notice: "Saved.", Form: map[string]string{}, Extra: map[string]any{}}
	var b strings.Builder
	if err := tpl.Execute(&b, p); err != nil {
		t.Fatalf("forum/index: %v", err)
	}
	out := b.String()
	for _, want := range []string{`data-theme-override="campfire"`, `class="pf cf-board"`, `<h1 class="pf-title">Campfire Community</h1>`,
		`cf-card`, `id="cf-card-10"`, `href="/forums/f/woodworking/"`, "<p>Joinery &amp; finishes.</p>", `pf-forum-row is-closed`,
		`>Closed<`, `<strong>12</strong>`, "Hand-cut &lt;dovetails&gt;", "2 hours ago", `action="/forums/search/"`,
		`href="/forums/new/"`, `pf-notice--success`, `cf-card--single`, `href="/forums/f/lounge/"`, "No topics yet",
		// Counts agree with their number; the latest topics list follows the cards.
		`<strong>1</strong> <span class="cf-count-label">topic</span>`, `<strong>0</strong> <span class="cf-count-label">topics</span>`,
		`class="cf-latest"`, `cf-latest__item is-sticky`, `href="/forums/t/6/bench-tour/"`, "Woodworking &middot; 1 reply", "Lounge &middot; 4 replies", "1 day ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("forum/index output lacks %q", want)
		}
	}
	if strings.Contains(out, "<dovetails>") {
		t.Error("a topic title is not escaped")
	}
	// No forums, a logged-out visitor: the empty state, no New topic button.
	b.Reset()
	empty := &forumPage{View: forumViewIndex, Title: "Forums", URLs: forumURLs{Index: "/forums/"}, Form: map[string]string{}, Extra: map[string]any{}}
	if err := tpl.Execute(&b, empty); err != nil {
		t.Fatalf("forum/index (empty): %v", err)
	}
	if out := b.String(); !strings.Contains(out, "cf-board__empty") || strings.Contains(out, "pf-title") || strings.Contains(out, "New topic") || strings.Contains(out, "cf-latest") {
		t.Errorf("empty forum/index: %s", out)
	}
}

func TestForumNativeThemeInstall(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WPGO_THEMES_DIR", dir)
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
	ctx := context.Background()
	if err := srv.forumActivate(ctx); err != nil {
		t.Fatalf("forum activate: %v", err)
	}
	_, body, _ := get(t, c, base+"/wp-admin/theme-install?browse=upload")
	nonce := formNonce(t, body)
	for _, slug := range forumNativeThemeSlugs {
		t.Run(slug, func(t *testing.T) {
			tdir := filepath.Join(themeLibraryDir, slug)
			code, body, loc := postZip(t, c, base+"/wp-admin/theme-install", nonce, "themezip", slug+".zip", zipDir(t, tdir))
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
			for _, u := range []string{"/", "/forums/", "/guidelines/"} {
				resp := anonGet(t, base+u)
				page := bodyOf(resp)
				if resp.StatusCode != http.StatusOK || strings.Contains(strings.ToLower(page), "template error") {
					t.Errorf("%s: %d", u, resp.StatusCode)
				}
				if !strings.Contains(page, "wp-theme-"+slug) {
					t.Errorf("%s is not rendered by the theme", u)
				}
				if !strings.Contains(page, `class="pf-usermenu"`) || !strings.Contains(page, "pf-usermenu-login") {
					t.Errorf("%s: no forum account menu in the header", u)
				}
			}
			home := bodyOf(anonGet(t, base+"/"))
			if !strings.Contains(home, `class="pf-stats"`) || !strings.Contains(home, "/forums/") {
				t.Error("the front page shows no forum stats or forum link")
			}
			idx := bodyOf(anonGet(t, base+"/forums/"))
			if !strings.Contains(idx, "pursae-forum") || !strings.Contains(idx, `data-view="index"`) {
				t.Error("/forums/ is not the forum index")
			}
			switch slug {
			case "campfire":
				if !strings.Contains(idx, `data-theme-override="campfire"`) || !strings.Contains(idx, "cf-forum") {
					t.Error("campfire: the forum home override or the forum wrapper is not used")
				}
				if strings.Contains(idx, "cf-forum__side") {
					t.Error("campfire: the sidebar shows although the layout is full width")
				}
				// The Customizer: the sidebar layout and an accent color.
				if err := srv.settings.Set(ctx, map[string]any{"theme_mods_campfire": map[string]any{"layout": "sidebar", "accent": "#336699"}}); err != nil {
					t.Fatal(err)
				}
				srv.purgeAll(ctx)
				side := bodyOf(anonGet(t, base+"/forums/"))
				if !strings.Contains(side, "cf-forum--sidebar") || !strings.Contains(side, "cf-forum__side") || !strings.Contains(side, `class="pf-stats"`) ||
					!strings.Contains(side, "cf-layout-sidebar") || !strings.Contains(side, "--pf-accent:#336699") {
					t.Error("campfire: the sidebar layout or the accent color is not applied")
				}
			case "devboard":
				if !strings.Contains(idx, "db-forum--sidebar") || !strings.Contains(idx, `class="pf-stats"`) {
					t.Error("devboard: the forum wrapper has no stats sidebar")
				}
			}
			// Every demo topic and its forum render inside the theme, and so do log in and join.
			topics, _, err := srv.forumTopics(ctx, forumTopicQuery{PerPage: 50})
			if err != nil {
				t.Fatal(err)
			}
			if len(topics) < 6 {
				t.Fatalf("the demo import created %d topics", len(topics))
			}
			for _, u := range []string{"/forums/login/", "/forums/register/"} {
				resp := anonGet(t, base+u)
				page := bodyOf(resp)
				if resp.StatusCode != http.StatusOK || strings.Contains(strings.ToLower(page), "template error") ||
					!strings.Contains(page, "wp-theme-"+slug) || !strings.Contains(page, "pf-auth") {
					t.Errorf("%s: %d, not rendered by the theme", u, resp.StatusCode)
				}
			}
			if code, page, _ := get(t, c, base+forumNativeRel(t, topics[0].URL)); code != http.StatusOK || strings.Contains(strings.ToLower(page), "template error") ||
				!strings.Contains(page, "wp-theme-"+slug) || !strings.Contains(page, "pf-reply") || !strings.Contains(page, "pf-modbar") {
				t.Errorf("logged-in topic %s: %d, or no reply form / moderation bar", topics[0].URL, code)
			}
			for _, tp := range topics {
				for _, u := range []string{tp.ForumURL, tp.URL} {
					resp := anonGet(t, base+forumNativeRel(t, u))
					page := bodyOf(resp)
					if resp.StatusCode != http.StatusOK || strings.Contains(strings.ToLower(page), "template error") || !strings.Contains(page, "wp-theme-"+slug) {
						t.Errorf("%s: %d, not rendered by the theme", u, resp.StatusCode)
					}
					if u == tp.URL && !strings.Contains(page, html.EscapeString(tp.Title)) {
						t.Errorf("%s: the topic title is missing", u)
					}
				}
			}
			if err := srv.removeThemeDemo(ctx, slug); err != nil {
				t.Fatal(err)
			}
			if left, _, _ := srv.forumTopics(ctx, forumTopicQuery{PerPage: 50}); len(left) != 0 {
				t.Errorf("removing the demo left %d topics", len(left))
			}
		})
	}
}

// forumNativeRel returns the path and query of an absolute forum URL.
func forumNativeRel(t *testing.T, u string) string {
	t.Helper()
	pu, err := url.Parse(u)
	if err != nil || u == "" {
		t.Fatalf("bad URL %q", u)
	}
	return pu.RequestURI()
}
