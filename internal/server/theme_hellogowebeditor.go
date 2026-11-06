package server

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Hello Golementor: the blank, builder-first base theme (in the spirit of Hello).
// Templates live in web/themes/hello-web-builder. Its header, footer and content are the
// theme-builder parts of the site's working copy (gowebeditor_theme.go); without a matching part
// a page shows the minimal defaults from the templates.
//
// The site's current design (parts with their display conditions, the kit = Site Settings
// including custom CSS) is the theme's working copy. "Save as theme" snapshots it as JSON into
// the saved-themes list (gowebeditor_theme_custom.go); activating a saved theme loads that JSON
// back into the working copy and makes Hello Golementor the active theme, while the snapshot
// itself stays as saved.

const helloThemeSlug = "hello-web-builder"

const helloThemeVersion = "1.0.2"

// helloMenuLocations are the theme's menu locations (and those of every saved theme).
var helloMenuLocations = []MenuLocation{{"primary", "Primary Menu"}, {"footer", "Footer Menu"}}

func init() {
	registerTheme(&Theme{
		Slug: helloThemeSlug, Name: "Hello Pursae Web Builder", Version: helloThemeVersion,
		Description: "A plain-vanilla, lightweight theme for Pursae Web Builder: a blank canvas whose header, footer, single, archive, search and 404 templates you build with the Theme Builder. The site's design lives inside this theme as JSON; Save as theme keeps copies of it in the saved themes list, and activating a saved theme loads it back.",
		Author:      "Pursae", AuthorURI: "https://github.com/pursae", ThemeURI: "https://github.com/pursae",
		Tags:       []string{"Pursae Web Builder", "Flexible Header", "Custom Colors", "Custom Menu", "Custom Logo", "Featured Images", "Full Width Template", "Accessibility Ready", "Theme Builder"},
		Screenshot: "screenshot.png",
		WidgetAreas: []WidgetArea{
			{ID: "sidebar-1", Name: "Sidebar", Description: "Widgets for the Pursae Web Builder Sidebar widget. The theme itself shows no sidebar."},
		},
		MenuLocations: helloMenuLocations,
		EditorStyles:  []string{"editor-style.css"},
		Supports: map[string]any{
			"custom-logo":          map[string]any{"height": 100, "width": 350, "flex-height": true, "flex-width": true},
			"title-tag":            true,
			"post-thumbnails":      true,
			"menus":                true,
			"html5":                []string{"search-form", "comment-form", "comment-list", "gallery", "caption", "script", "style"},
			"align-wide":           true,
			"responsive-embeds":    true,
			"editor-styles":        true,
			"wp-block-styles":      true,
			"automatic-feed-links": true,
		},
		Head: func(s *Server, c *gin.Context, v *siteView) string {
			return "<link rel='stylesheet' id='hello-web-builder-css' href='" + v.ThemeURI + "/style.css?ver=" + helloThemeVersion + "' media='all' />\n"
		},
	})
}

func helloTheme() *Theme { return themeBySlug(helloThemeSlug) }

// helloHead is the theme's stylesheet; it is printed whenever a page renders through the Hello
// Golementor templates, also when the active theme is another bundled theme whose layout a
// theme part replaced.
func (s *Server) helloHead(c *gin.Context, v *siteView) string {
	t := helloTheme()
	if t == nil || t.Head == nil {
		return ""
	}
	hv := *v
	hv.ThemeURI = t.URI()
	return t.Head(s, c, &hv)
}

// gweLoadSavedTheme loads a saved theme's JSON into the working copy: the site's current parts go
// to the trash, the snapshot's parts become the site's parts and the kit takes the snapshot's
// settings. The snapshot is not changed. The caller then activates Hello Golementor.
func (s *Server) gweLoadSavedTheme(ctx context.Context, slug string) error {
	snap := gweSnapshotFor(slug)
	if snap == nil {
		return errors.New("That saved theme has no data.")
	}
	return s.gweLoadSnapshot(ctx, snap, slug)
}

// gweLoadSnapshot loads a design into the working copy; key is remembered as the loaded design (a saved
// theme's slug, or a Golementor design package's: goo_gwe.go).
func (s *Server) gweLoadSnapshot(ctx context.Context, snap *gweSnapshot, slug string) error {
	var authorID int64
	if u, ok := ctx.Value(ctxUser).(*store.User); ok && u != nil {
		authorID = u.ID
	}
	if authorID == 0 {
		_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(min(user_id),0) FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.slug = 'administrator'`).Scan(&authorID)
	}
	if err := s.gweEnsurePartType(ctx); err != nil {
		return err
	}
	old, err := s.gwePartPosts(ctx, "")
	if err != nil {
		return err
	}
	// Loading replaces the working copy: keep unsaved design changes as a saved theme first.
	if s.gweWorkingCopyDirty(ctx) {
		if err := s.gweBackupWorkingCopy(ctx, authorID, snap.Name); err != nil {
			return err
		}
	}
	for _, p := range old {
		if err := s.store.TrashPost(ctx, p.ID); err != nil {
			return err
		}
		s.gweDropCSS(p.ID)
	}
	for _, p := range snap.Parts {
		if _, err := s.gweCopyPart(ctx, p, "", authorID); err != nil {
			return err
		}
	}
	kit, err := s.gweKit(ctx)
	if err != nil {
		return err
	}
	kit.Meta["_elementor_page_settings"] = gweCloneSettings(snap.Kit)
	if err := s.store.UpdatePost(ctx, kit, authorID); err != nil {
		return err
	}
	s.gweDropCSS(kit.ID)
	mods := s.themeMods(helloThemeSlug)
	if snap.CustomCSS != "" {
		mods["custom_css"] = snap.CustomCSS
	} else {
		delete(mods, "custom_css")
	}
	if err := s.settings.Set(ctx, map[string]any{"gwe_loaded_theme": slug, "theme_mods_" + helloThemeSlug: mods}); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// gweSavedThemeCards lists the saved Golementor themes for the Themes screen and the Theme
// Builder. Loaded marks the snapshot the working copy was last loaded from (while Hello
// Golementor is active and nothing was saved since).
func (s *Server) gweSavedThemeCards(c *gin.Context) []gweTBTheme {
	rows, _ := s.gweThemeRows(c, "")
	nonce := s.nonce(c, "gwe-theme-builder")
	loaded := ""
	if s.activeTheme().Slug == helloThemeSlug && !s.gweWorkingCopyDirty(c) {
		loaded = s.settings.String("gwe_loaded_theme")
	}
	var out []gweTBTheme
	for _, r := range rows {
		t := themeBySlug(r.Slug)
		card := gweTBTheme{Slug: r.Slug, Name: r.Name, Description: r.Description, Loaded: r.Slug == loaded,
			ExportURL:   gweThemeBuilderPath + "?action=export&theme=" + url.QueryEscape(r.Slug) + "&_wpnonce=" + nonce,
			PreviewURL:  "/?theme=" + url.QueryEscape(r.Slug),
			ActivateURL: "/wp-admin/themes?action=activate&stylesheet=" + url.QueryEscape(r.Slug) + "&_wpnonce=" + s.nonce(c, "switch-theme_"+r.Slug)}
		if t != nil {
			card.Screenshot = t.ScreenshotSrc()
		}
		if r.Data != nil {
			card.Parts = len(r.Data.Parts)
			if !r.Data.Saved.IsZero() {
				card.Saved = r.Data.Saved.In(s.loc()).Format("2006/01/02 3:04 pm")
			}
		}
		out = append(out, card)
	}
	return out
}

// gweCloneSettings deep-copies kit or page settings (through JSON, like the snapshot itself).
func gweCloneSettings(in map[string]any) map[string]any {
	out := map[string]any{}
	if b, err := json.Marshal(in); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// gweWorkingCSS is the working copy's Additional CSS (Hello Golementor's Customizer CSS).
func (s *Server) gweWorkingCSS() string {
	return modString(s.themeMods(helloThemeSlug), "custom_css", "")
}

// gweDesignKey is a comparable form of a design: kit settings, parts (without ids and dates) and
// Additional CSS.
func gweDesignKey(kit map[string]any, parts []*gwePart, css string) string {
	type part struct {
		Type, Title string
		Elements    any
		Settings    any
		Conditions  []string
	}
	ps := make([]part, 0, len(parts))
	for _, p := range parts {
		var el any
		_ = json.Unmarshal([]byte(p.Elements), &el)
		ps = append(ps, part{p.Type, p.Title, el, p.Settings, p.Conditions})
	}
	sort.SliceStable(ps, func(i, j int) bool {
		if ps[i].Type != ps[j].Type {
			return ps[i].Type < ps[j].Type
		}
		return ps[i].Title < ps[j].Title
	})
	b, _ := json.Marshal(struct {
		Kit   map[string]any
		Parts []part
		CSS   string
	}{gweCloneSettings(kit), ps, strings.TrimSpace(css)})
	return string(b)
}

// gweWorkingCopyDirty reports whether the working copy has design changes that no saved theme
// holds: it differs from the snapshot it was loaded from, or (nothing loaded yet) it has parts or
// Additional CSS of its own.
func (s *Server) gweWorkingCopyDirty(ctx context.Context) bool {
	posts, err := s.gwePartPosts(ctx, "")
	if err != nil {
		return false
	}
	parts := make([]*gwePart, 0, len(posts))
	for _, p := range posts {
		parts = append(parts, gwePartFromPost(p))
	}
	css := s.gweWorkingCSS()
	loaded := s.settings.String("gwe_loaded_theme")
	snap := gweSnapshotFor(loaded)
	if snap == nil { // a Golementor design package (goo_gwe.go)
		if t := themeBySlug(loaded); gweDesignTheme(t) {
			snap, _ = gweDesignSnapshot(t)
		}
	}
	if snap == nil {
		return len(parts) > 0 || strings.TrimSpace(css) != ""
	}
	kit, err := s.gweKit(ctx)
	if err != nil {
		return false
	}
	return gweDesignKey(gwePageSettings(kit), parts, css) != gweDesignKey(snap.Kit, snap.Parts, snap.CustomCSS)
}

// gweBackupWorkingCopy saves the working copy as a saved theme before another one is loaded over it.
func (s *Server) gweBackupWorkingCopy(ctx context.Context, authorID int64, loading string) error {
	posts, err := s.gwePartPosts(ctx, "")
	if err != nil {
		return err
	}
	gweSortParts(posts)
	parts := make([]*gwePart, 0, len(posts))
	for _, p := range posts {
		parts = append(parts, gwePartFromPost(p))
	}
	kit, err := s.gweKit(ctx)
	if err != nil {
		return err
	}
	name := "Backup " + time.Now().In(s.loc()).Format("2006-01-02 15:04")
	in := gweSaveThemeInput{Name: name, Description: "Design saved automatically before \"" + loading + "\" was loaded."}
	return s.gweCreateTheme(ctx, authorID, s.gweUniqueSlug(ctx, name), in, "", gwePageSettings(kit), parts, s.gweWorkingCSS())
}

// gweShell is the template set Golementor theme parts and saved themes render through: Hello
// Golementor's (theme_hellogowebeditor.go).
func (t *templates) gweShell() *template.Template {
	return t.getTheme(true, helloThemeSlug)
}
