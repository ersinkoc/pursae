package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
	"wpgo/internal/store"
)

// Golementor Studio setup wizard (docs/golementor-studio.md, section 2): /wp-admin/golementor-setup
// installs a site kit from the library (golementor_library.go) as editable Golementor documents of
// the Hello Golementor working copy: theme parts with display conditions, pages built with
// Golementor, menus, sample posts with featured images, Settings › Reading, the kit's global colors
// and fonts (in the active kit document), site title, tagline and logo; then Hello Golementor is
// activated.
//
// Every post, page, theme part, attachment and term it creates carries the meta _gwe_starter = <kit
// slug> (pages and posts also _gwe_starter_ref = their slug in kit.json). What it changes besides
// is kept in the golementor_starter option: the previous reading settings, title and tagline, the
// kit's previous Site Settings, the theme that was active, the menu locations it took over, the
// menus it made and the posts it moved to the trash (the working copy's theme parts and the
// untouched install samples). Remove starter site deletes exactly the tagged items and restores
// all of that. Running the wizard again offers to replace the starter site (remove, then install)
// or to keep it and only apply the colors, fonts and site details.

const (
	gweSetupPath     = "/wp-admin/golementor-setup"
	gweStarterMeta   = "_gwe_starter"       // kit slug on every created item
	gweStarterRef    = "_gwe_starter_ref"   // the item's slug inside kit.json (pages, posts)
	gweStarterOption = "golementor_starter" // the install record
	gweSetupTimeout  = 3 * time.Minute
	gweSetupNonce    = "golementor-setup"
	// gweSetupPreviewID is the document id base of the live preview (never a real post).
	gweSetupPreviewID int64 = 2_000_000_000_000
)

// gweSetupSystemFonts are font families a kit may use besides the Google Fonts catalogue.
var gweSetupSystemFonts = map[string]bool{"Arial": true, "Helvetica": true, "Georgia": true, "Times New Roman": true,
	"Verdana": true, "Tahoma": true, "Trebuchet MS": true, "Courier New": true, "system-ui": true, "sans-serif": true, "serif": true}

// gweSetupKitReserved are Site Settings a kit's "settings" may not set (globals and wizard choices).
var gweSetupKitReserved = map[string]bool{"system_colors": true, "custom_colors": true, "system_typography": true,
	"custom_typography": true, "site_logo": true, "site_name": true, "site_description": true}

// gweSetupPalette is a color preset: primary, secondary, text, accent.
type gweSetupPalette struct {
	ID, Name string
	Colors   [4]string
}

var gweSetupPalettes = []gweSetupPalette{
	{"ocean", "Ocean", [4]string{"#0B5394", "#0E7490", "#1F2937", "#F59E0B"}},
	{"forest", "Forest", [4]string{"#166534", "#3F6212", "#1C2A1E", "#CA8A04"}},
	{"sunset", "Sunset", [4]string{"#C2410C", "#9A3412", "#292524", "#0EA5E9"}},
	{"berry", "Berry", [4]string{"#9D174D", "#6D28D9", "#27272A", "#DB2777"}},
	{"graphite", "Graphite", [4]string{"#111827", "#374151", "#4B5563", "#2563EB"}},
	{"sand", "Sand", [4]string{"#8B5E34", "#A47148", "#3F3A34", "#2F855A"}},
	{"midnight", "Midnight", [4]string{"#4F46E5", "#1E1B4B", "#334155", "#F43F5E"}},
	{"mint", "Mint", [4]string{"#0F766E", "#134E4A", "#1F2937", "#F97316"}},
}

// gweSetupPairing is a font preset: headings (primary, secondary) and body (text, accent).
type gweSetupPairing struct {
	ID, Name, Heading, Body string
}

var gweSetupPairings = []gweSetupPairing{
	{"modern", "Modern", "Inter", "Inter"},
	{"editorial", "Editorial", "Playfair Display", "Source Sans 3"},
	{"friendly", "Friendly", "Poppins", "Open Sans"},
	{"classic", "Classic", "Libre Baskerville", "Lora"},
	{"tech", "Technical", "Space Grotesk", "DM Sans"},
	{"elegant", "Elegant", "DM Serif Display", "Manrope"},
	{"literary", "Literary", "Merriweather", "Work Sans"},
}

// gweSetupChoices are the wizard's answers.
type gweSetupChoices struct {
	Kit      string
	Palette  string            // "" (the kit's colors), a preset id or "custom"
	Colors   map[string]string // custom: primary, secondary, text, accent
	Fonts    string            // "" (the kit's fonts) or a pairing id
	SiteName string
	Tagline  string
	LogoID   int64
	Pages    []string // kit page slugs to create; nil = every page not marked optional
	Mode     string   // with a starter installed: "replace" (default) or "keep"
}

// gweStarterRecord is the golementor_starter option.
type gweStarterRecord struct {
	Kit         string           `json:"kit"`
	Title       string           `json:"title"`
	InstalledAt string           `json:"installed_at"`
	Options     map[string]any   `json:"options"`      // previous values of the options it set
	KitID       int64            `json:"kit_id"`       // the kit document it changed
	KitSettings map[string]any   `json:"kit_settings"` // that kit's previous Site Settings
	Theme       string           `json:"theme"`        // the theme active before
	Locations   map[string]int64 `json:"locations"`    // menu location -> menu assigned before (0 = none)
	Menus       []int64          `json:"menus"`
	Trashed     []int64          `json:"trashed"`
	Complete    bool             `json:"complete"`
}

func (s *Server) gweStarter() *gweStarterRecord {
	raw := s.settings.Get(gweStarterOption)
	if raw == nil {
		return nil
	}
	b, _ := json.Marshal(raw)
	var r gweStarterRecord
	if json.Unmarshal(b, &r) != nil || r.Kit == "" {
		return nil
	}
	return &r
}

func (s *Server) gweSaveStarter(ctx context.Context, r *gweStarterRecord) error {
	return s.settings.Set(ctx, map[string]any{gweStarterOption: r})
}

// gweSetupValidCondition checks a kit part condition ({{page_id:slug}} stands for a page id).
func gweSetupValidCondition(cd string, pages map[string]bool) bool {
	mode, rule, ok := strings.Cut(cd, "/")
	if !ok || (mode != "include" && mode != "exclude") {
		return false
	}
	valid := true
	rule = gweLibTokenRe.ReplaceAllStringFunc(rule, func(tok string) string {
		m := gweLibTokenRe.FindStringSubmatch(tok)
		if m[1] != "page_id" || !pages[m[2]] {
			valid = false
		}
		return "1"
	})
	return valid && gweValidRule(rule)
}

// --- globals -----------------------------------------------------------------------------------

// gweSetupKitSettings computes the kit document's Site Settings for a kit and the wizard choices,
// starting from base (the current settings, which keep everything the kit does not set).
func (s *Server) gweSetupKitSettings(ctx context.Context, base map[string]any, k *gweLibKit, ch gweSetupChoices) map[string]any {
	out := gweCloneSettings(base)
	for _, extra := range []map[string]any{k.Globals.Settings, k.Settings} { // kit style settings, then extras
		for key, v := range gweCloneSettings(extra) {
			if key == "__globals__" { // merged: the kit's references win, others stay
				merged, _ := out[key].(map[string]any)
				if merged == nil {
					merged = map[string]any{}
				}
				if m, ok := v.(map[string]any); ok {
					for gk, gv := range m {
						merged[gk] = gv
					}
				}
				out[key] = merged
				continue
			}
			out[key] = v
		}
	}
	colors := map[string]string{}
	switch {
	case ch.Palette == "custom":
		colors = ch.Colors
	case ch.Palette != "":
		for _, p := range gweSetupPalettes {
			if p.ID == ch.Palette {
				for i, id := range gweLibGlobalIDs {
					colors[id] = p.Colors[i]
				}
			}
		}
	}
	var pair *gweSetupPairing
	for i := range gweSetupPairings {
		if gweSetupPairings[i].ID == ch.Fonts {
			pair = &gweSetupPairings[i]
		}
	}
	system := map[string]bool{}
	for _, id := range gweLibGlobalIDs {
		system[id] = true
	}
	split := func(list []map[string]any, key string, apply func(m map[string]any)) (sys, custom []any) {
		byID := map[string]map[string]any{}
		for _, m := range list {
			c := gweCloneSettings(m)
			apply(c)
			byID[c["_id"].(string)] = c
		}
		for _, id := range gweLibGlobalIDs {
			if m, ok := byID[id]; ok {
				sys = append(sys, m)
			}
		}
		// The site's own custom globals stay (unless the kit defines the same id), the kit's follow.
		existing, _ := out[key].([]any)
		for _, it := range existing {
			if m, ok := it.(map[string]any); ok {
				if id, _ := m["_id"].(string); id != "" && byID[id] == nil {
					custom = append(custom, m)
				}
			}
		}
		for _, m := range list {
			if id, _ := m["_id"].(string); !system[id] {
				custom = append(custom, byID[id])
			}
		}
		return sys, custom
	}
	sysColors, customColors := split(k.Globals.Colors, "custom_colors", func(m map[string]any) {
		if c := colors[m["_id"].(string)]; gweLibColorRe.MatchString(c) {
			m["color"] = strings.ToUpper(c)
		}
	})
	sysFonts, customFonts := split(k.Globals.Typography, "custom_typography", func(m map[string]any) {
		if _, ok := m["typography_typography"]; !ok {
			m["typography_typography"] = "custom"
		}
		if pair == nil {
			return
		}
		switch m["_id"] {
		case "primary", "secondary":
			m["typography_font_family"] = pair.Heading
		case "text", "accent":
			m["typography_font_family"] = pair.Body
		}
	})
	out["system_colors"], out["custom_colors"] = sysColors, gweNonNil(customColors)
	out["system_typography"], out["custom_typography"] = sysFonts, gweNonNil(customFonts)
	if ch.LogoID > 0 {
		if a, err := s.store.GetAttachment(ctx, ch.LogoID); err == nil && strings.HasPrefix(a.Mime, "image/") {
			out["site_logo"] = map[string]any{"id": a.ID, "url": s.uploadURL(ctx, a.FilePath), "alt": a.Alt, "source": "library"}
		}
	}
	return out
}

func gweNonNil(list []any) []any {
	if list == nil {
		return []any{}
	}
	return list
}

// --- install -----------------------------------------------------------------------------------

// gweSetupResolver resolves the tokens of a kit while it is installed.
type gweSetupResolver struct {
	s     *Server
	ctx   context.Context
	pages map[string]*store.Post
}

func (r *gweSetupResolver) resolve(kind, arg string) string {
	switch kind {
	case "library":
		return r.s.gweLibAssetURL(r.ctx, arg)
	case "page":
		if p := r.pages[arg]; p != nil {
			return r.s.links(r.ctx).Permalink(p)
		}
		return "#"
	case "page_id":
		if p := r.pages[arg]; p != nil {
			return itoa64(p.ID)
		}
		return "0"
	}
	return ""
}

// tree replaces the tokens of an element array and gives it fresh ids.
func (r *gweSetupResolver) tree(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "[]", nil
	}
	out, err := gweLibFreshTree(json.RawMessage(gweLibReplace(string(raw), true, r.resolve)))
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if json.Compact(&buf, out) == nil {
		return buf.String(), nil
	}
	return string(out), nil
}

func (r *gweSetupResolver) text(v string) string { return gweLibReplace(v, false, r.resolve) }

// gweSetupPageSet is the set of kit pages to create.
func gweSetupPageSet(k *gweLibKit, chosen []string) map[string]bool {
	set := map[string]bool{}
	if chosen == nil {
		for _, p := range k.Pages {
			if !p.Optional {
				set[p.Slug] = true
			}
		}
	} else {
		for _, slug := range chosen {
			if k.page(slug) != nil {
				set[slug] = true
			}
		}
	}
	for _, p := range k.Pages {
		if p.FrontPage { // the front page is the point of a starter site
			set[p.Slug] = true
		}
	}
	return set
}

// gweSetupMu serializes installs and removals (a double click must not install twice).
var gweSetupMu sync.Mutex

// gweStarterInstall installs a kit for user uid.
func (s *Server) gweStarterInstall(ctx context.Context, uid int64, ch gweSetupChoices) error {
	gweSetupMu.Lock()
	defer gweSetupMu.Unlock()
	k := s.gweLib().kit(ch.Kit)
	if k == nil {
		return errors.New("That starter site does not exist.")
	}
	ctx, cancel := context.WithTimeout(ctx, gweSetupTimeout)
	defer cancel()
	if old := s.gweStarter(); old != nil {
		if ch.Mode == "keep" {
			return s.gweStarterRestyle(ctx, uid, old, ch)
		}
		if err := s.gweStarterRemoveLocked(ctx); err != nil {
			return fmt.Errorf("removing the current starter site: %v", err)
		}
	}
	if err := s.gweEnsurePartType(ctx); err != nil {
		return err
	}
	kitPost, err := s.gweKit(ctx)
	if err != nil {
		return err
	}
	rec := &gweStarterRecord{Kit: k.Slug, Title: k.Title, Options: map[string]any{}, Locations: map[string]int64{},
		KitID: kitPost.ID, KitSettings: gwePageSettings(kitPost), Theme: s.settings.String("stylesheet")}
	for _, key := range []string{"show_on_front", "page_on_front", "page_for_posts", "blogname", "blogdescription"} {
		rec.Options[key] = s.settings.Get(key)
	}
	save := func() error { return s.gweSaveStarter(ctx, rec) }
	if err := save(); err != nil { // first, so Remove can clean up an install that stops half-way
		return err
	}
	tag := func(ref string) map[string]any {
		m := map[string]any{gweStarterMeta: k.Slug}
		if ref != "" {
			m[gweStarterRef] = ref
		}
		return m
	}

	// The working copy's theme parts and the untouched install samples go to the trash.
	old, err := s.gwePartPosts(ctx, "")
	if err != nil {
		return err
	}
	for _, p := range old {
		if err := s.store.TrashPost(ctx, p.ID); err != nil {
			return err
		}
		s.gweDropCSS(p.ID)
		rec.Trashed = append(rec.Trashed, p.ID)
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM posts WHERE status = 'publish'
		AND ((type = 'post' AND slug = 'hello-world') OR (type = 'page' AND slug = 'sample-page'))
		AND updated_at - created_at < interval '5 seconds' AND coalesce(meta->>$1, '') = '' AND coalesce(meta->>$2, '') = ''`, demoMetaKey, gweStarterMeta)
	if err == nil {
		var ids []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			if s.store.TrashPost(ctx, id) == nil {
				rec.Trashed = append(rec.Trashed, id)
			}
		}
	}
	if err := save(); err != nil {
		return err
	}

	// Pages: created first (permalinks and ids for the tokens), filled once they all exist.
	res := &gweSetupResolver{s: s, ctx: ctx, pages: map[string]*store.Post{}}
	want := gweSetupPageSet(k, ch.Pages)
	for i, pg := range k.Pages {
		if !want[pg.Slug] {
			continue
		}
		p := &store.Post{Type: "page", AuthorID: uid, Title: pg.Title, Slug: pg.Slug, Status: "publish", MenuOrder: i,
			CommentStatus: "closed", PingStatus: "closed", Meta: tag(pg.Slug)}
		if err := s.store.InsertPost(ctx, p); err != nil {
			return fmt.Errorf("page %q: %v", pg.Title, err)
		}
		res.pages[pg.Slug] = p
	}
	for _, pg := range k.Pages {
		p := res.pages[pg.Slug]
		if p == nil || pg.PostsPage {
			continue
		}
		tree, err := res.tree(pg.Content)
		if err != nil {
			return fmt.Errorf("page %q: %v", pg.Title, err)
		}
		tpl := pg.pageTemplate()
		settings := gweCloneSettings(pg.Settings)
		delete(settings, "template") // a post field (_wp_page_template), like save_builder
		p.Meta["_elementor_edit_mode"], p.Meta["_elementor_template_type"], p.Meta["_elementor_version"] = "builder", "wp-page", gweVersion
		p.Meta["_elementor_data"], p.Meta["_elementor_page_settings"] = tree, settings
		if tpl != "default" {
			p.Meta["_wp_page_template"] = tpl
		}
		if r, err := builder.Render(gweDocument(p), s.gweRenderContext(ctx, false)); err == nil && r != nil {
			p.Content = gwePlainText(r.HTML)
		}
		if err := s.store.UpdatePost(ctx, p, uid); err != nil {
			return fmt.Errorf("page %q: %v", pg.Title, err)
		}
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM post_revisions WHERE post_id = $1`, p.ID)
		s.gweDropCSS(p.ID)
	}

	// Sample posts with their categories and featured images.
	media := map[string]int64{}
	for i, kp := range k.Posts {
		var catIDs []int64
		for _, name := range kp.categories() {
			id, err := s.gweSetupTerm(ctx, "category", name, k.Slug)
			if err != nil {
				return err
			}
			catIDs = append(catIDs, id)
		}
		if len(catIDs) == 0 {
			catIDs = []int64{int64(s.settings.Int("default_category"))}
		}
		var imageID int64
		if m := gweLibTokenRe.FindStringSubmatch(kp.Image); m != nil && m[1] == "library" {
			if id, ok := media[m[2]]; ok {
				imageID = id
			} else if imageID, err = s.gweSetupMedia(ctx, uid, k.Slug, m[2]); err != nil {
				return fmt.Errorf("post %q: image: %v", kp.Title, err)
			}
			media[m[2]] = imageID
		}
		date := time.Now().Add(-time.Duration(i+1) * 26 * time.Hour).Truncate(time.Minute)
		p := &store.Post{Type: "post", AuthorID: uid, Title: kp.Title, Slug: kp.Slug, Content: res.text(kp.Content), Excerpt: res.text(kp.Excerpt),
			Status: "publish", CommentStatus: "open", PingStatus: "closed", Format: "standard", FeaturedMediaID: imageID, PublishedAt: &date,
			Meta: tag(defaultStr(kp.Slug, strconv.Itoa(i+1)))}
		if err := s.store.InsertPost(ctx, p); err != nil {
			return fmt.Errorf("post %q: %v", kp.Title, err)
		}
		if err := s.store.SetPostTerms(ctx, p.ID, "category", catIDs); err != nil {
			return err
		}
		if imageID > 0 {
			_, _ = s.store.Pool.Exec(ctx, `UPDATE media SET post_id = $2 WHERE id = $1 AND post_id IS NULL`, imageID, p.ID)
		}
	}
	_ = s.store.RecountTerms(ctx, []int64{int64(s.settings.Int("default_category"))})

	// Theme parts: the new working copy of Hello Golementor.
	for _, part := range k.Parts {
		tree, err := res.tree(part.Content)
		if err != nil {
			return fmt.Errorf("%s part: %v", part.Type, err)
		}
		conds := []any{}
		for _, cd := range part.Conditions {
			conds = append(conds, res.text(cd))
		}
		title := defaultStr(strings.TrimSpace(part.Title), gwePartLabel(part.Type))
		meta := tag("")
		meta["_elementor_edit_mode"], meta["_elementor_template_type"], meta["_elementor_version"] = "builder", part.Type, gweVersion
		meta["_elementor_data"], meta["_elementor_page_settings"] = tree, gweCloneSettings(part.Settings)
		meta["_gwe_conditions"], meta["_gwe_theme"] = conds, ""
		p := &store.Post{Type: gwePartType, AuthorID: uid, Title: title, Status: "publish", CommentStatus: "closed", PingStatus: "closed", Meta: meta}
		if err := s.store.InsertPost(ctx, p); err != nil {
			return fmt.Errorf("%s part: %v", part.Type, err)
		}
	}

	// Menus, assigned to Hello Golementor's locations.
	existing, err := s.store.Menus(ctx)
	if err != nil {
		return err
	}
	for _, mn := range k.Menus {
		menuID, err := s.store.CreateMenu(ctx, mn.Name)
		if err != nil {
			return err
		}
		rec.Menus = append(rec.Menus, menuID)
		if err := save(); err != nil {
			return err
		}
		order := 0
		var add func(items []gweLibKitMenuItem, parent int64) error
		add = func(items []gweLibKitMenuItem, parent int64) error {
			for _, it := range items {
				mi := &store.EditItem{MenuID: menuID, ParentID: parent, Label: it.Title, Status: "publish"}
				if it.Page != "" {
					p := res.pages[it.Page]
					if p == nil {
						continue // a page the user chose not to create
					}
					mi.Kind, mi.Object, mi.TargetID = "post_type", "page", p.ID
					if mi.Label == "" {
						mi.Label = p.Title
					}
				} else {
					mi.Kind, mi.Object, mi.URL = "custom", "custom", res.text(it.URL)
				}
				order++
				mi.Order = order
				if err := s.store.AddMenuItem(ctx, mi); err != nil {
					return err
				}
				if err := add(it.Children, mi.ID); err != nil {
					return err
				}
			}
			return nil
		}
		if err := add(mn.Items, 0); err != nil {
			return err
		}
		if _, ok := rec.Locations[mn.Location]; !ok {
			rec.Locations[mn.Location] = 0
			for _, m := range existing {
				if m.Location == mn.Location {
					rec.Locations[mn.Location] = m.ID
				}
			}
		}
		if err := s.store.SetMenuLocations(ctx, map[string]int64{mn.Location: menuID}); err != nil {
			return err
		}
		if err := save(); err != nil {
			return err
		}
	}

	// Settings › Reading, title and tagline.
	values := map[string]any{}
	for _, pg := range k.Pages {
		if p := res.pages[pg.Slug]; p != nil {
			if pg.FrontPage {
				values["show_on_front"], values["page_on_front"] = "page", p.ID
			}
			if pg.PostsPage {
				values["page_for_posts"] = p.ID
			}
		}
	}
	if _, ok := values["page_for_posts"]; !ok {
		values["page_for_posts"] = 0
	}
	s.gweSetupIdentity(values, k, ch)
	if err := s.settings.Set(ctx, values); err != nil {
		return err
	}

	// The kit's global colors and fonts (with the chosen palette and fonts) and the logo.
	kitPost.Meta["_elementor_page_settings"] = s.gweSetupKitSettings(ctx, rec.KitSettings, k, ch)
	if err := s.store.UpdatePost(ctx, kitPost, uid); err != nil {
		return err
	}
	s.gweDropCSS(kitPost.ID)

	// Hello Golementor shows the parts.
	if s.settings.String("stylesheet") != helloThemeSlug {
		_ = s.restoreTheme(ctx, helloThemeSlug)
		if err := s.switchTheme(ctx, helloThemeSlug); err != nil {
			return err
		}
	}
	_ = s.settings.Set(ctx, map[string]any{"gwe_loaded_theme": ""}) // the working copy is no saved theme's any more
	rec.Complete, rec.InstalledAt = true, time.Now().UTC().Format(time.RFC3339)
	if err := save(); err != nil {
		return err
	}
	s.purgeAll(ctx)
	return nil
}

// gweSetupIdentity adds the site title and tagline of the choices (or the kit's defaults) to values.
func (s *Server) gweSetupIdentity(values map[string]any, k *gweLibKit, ch gweSetupChoices) {
	first := func(list ...string) string {
		for _, v := range list {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
		return ""
	}
	if v := first(ch.SiteName, k.Site.Name, k.Options["blogname"]); v != "" {
		values["blogname"] = v
	}
	if v := first(ch.Tagline, k.Site.Tagline, k.Options["blogdescription"]); v != "" {
		values["blogdescription"] = v
	}
}

// gweStarterRestyle is "keep": the installed starter site stays, the colors, fonts and site details
// of the choices are applied (Remove still restores the state from before the install).
func (s *Server) gweStarterRestyle(ctx context.Context, uid int64, rec *gweStarterRecord, ch gweSetupChoices) error {
	lib := s.gweLib()
	k := lib.kit(rec.Kit)
	if k == nil {
		if k = lib.kit(ch.Kit); k == nil {
			return errors.New("That starter site does not exist.")
		}
	}
	kitPost, err := s.gweKit(ctx)
	if err != nil {
		return err
	}
	kitPost.Meta["_elementor_page_settings"] = s.gweSetupKitSettings(ctx, gwePageSettings(kitPost), k, ch)
	if err := s.store.UpdatePost(ctx, kitPost, uid); err != nil {
		return err
	}
	s.gweDropCSS(kitPost.ID)
	values := map[string]any{}
	s.gweSetupIdentity(values, k, ch)
	if len(values) > 0 {
		if err := s.settings.Set(ctx, values); err != nil {
			return err
		}
	}
	s.purgeAll(ctx)
	return nil
}

// gweSetupTerm returns a category by name, creating it (tagged) when it does not exist.
func (s *Server) gweSetupTerm(ctx context.Context, tax, name, kit string) (int64, error) {
	if t, err := s.store.TermByName(ctx, tax, name); err == nil {
		return t.ID, nil
	}
	t := &store.Term{Taxonomy: tax, Name: name, Meta: map[string]any{gweStarterMeta: kit}}
	if err := s.store.InsertTerm(ctx, t); err != nil {
		return 0, fmt.Errorf("%s %q: %v", tax, name, err)
	}
	return t.ID, nil
}

// gweSetupMedia imports a library image into the media library (tagged).
func (s *Server) gweSetupMedia(ctx context.Context, uid int64, kit, rel string) (int64, error) {
	b, err := fs.ReadFile(s.gweLibFS(), rel)
	if err != nil {
		return 0, err
	}
	// Prefixed: uploads are cached as immutable, a file name must never come back with other bytes.
	a, err := s.insertAttachmentFile(ctx, "golementor-"+kit+"-"+path.Base(rel), bytes.NewReader(b), uid, 0)
	if err != nil {
		return 0, err
	}
	a.Meta[gweStarterMeta] = kit
	if err := s.store.UpdateAttachment(ctx, a); err != nil {
		return 0, err
	}
	return a.ID, nil
}

// --- remove ------------------------------------------------------------------------------------

// gweStarterRemove is Remove starter site.
func (s *Server) gweStarterRemove(ctx context.Context) error {
	gweSetupMu.Lock()
	defer gweSetupMu.Unlock()
	return s.gweStarterRemoveLocked(ctx)
}

func (s *Server) gweStarterRemoveLocked(ctx context.Context) error {
	rec := s.gweStarter()
	if rec == nil {
		rec = &gweStarterRecord{}
	}
	ids := func(q string) ([]int64, error) {
		rows, err := s.store.Pool.Query(ctx, q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
	for _, id := range rec.Menus {
		if err := s.store.DeleteMenu(ctx, id); err != nil {
			return err
		}
	}
	if len(rec.Locations) > 0 {
		menus, _ := s.store.Menus(ctx)
		exists := map[int64]bool{}
		for _, m := range menus {
			exists[m.ID] = true
		}
		locs := map[string]int64{}
		for l, id := range rec.Locations {
			if id > 0 && !exists[id] {
				id = 0
			}
			locs[l] = id
		}
		if err := s.store.SetMenuLocations(ctx, locs); err != nil {
			return err
		}
	}
	posts, err := ids(`SELECT id FROM posts WHERE coalesce(meta->>'` + gweStarterMeta + `','') <> '' ORDER BY id DESC`)
	if err != nil {
		return err
	}
	for _, id := range posts {
		if err := s.store.DeletePost(ctx, id); err != nil {
			return err
		}
		s.gweDropCSS(id)
	}
	for _, id := range rec.Trashed { // the working copy's parts and the samples, back as they were
		_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET status = coalesce(trashed_status, 'publish'), trashed_status = NULL, deleted_at = NULL
			WHERE id = $1 AND status = 'trash'`, id)
	}
	media, err := ids(`SELECT id FROM media WHERE coalesce(meta->>'` + gweStarterMeta + `','') <> ''`)
	if err != nil {
		return err
	}
	for _, id := range media {
		if a, err := s.store.GetAttachment(ctx, id); err == nil {
			s.deleteAttachmentFiles(a)
		}
		if err := s.store.DeleteAttachment(ctx, id); err != nil {
			return err
		}
	}
	terms, err := ids(`SELECT id FROM terms WHERE coalesce(meta->>'` + gweStarterMeta + `','') <> '' ORDER BY id DESC`)
	if err != nil {
		return err
	}
	def := int64(s.settings.Int("default_category"))
	for _, id := range terms {
		if id == def {
			continue
		}
		if tm, err := s.store.GetTerm(ctx, id); err == nil {
			if err := s.store.DeleteTerm(ctx, tm, def); err != nil {
				return err
			}
		}
	}
	_ = s.store.RecountTerms(ctx, []int64{def})
	if rec.KitID > 0 && rec.KitSettings != nil {
		if kit, err := s.store.GetPost(ctx, rec.KitID); err == nil {
			kit.Meta["_elementor_page_settings"] = rec.KitSettings
			if err := s.store.UpdatePost(ctx, kit, kit.AuthorID); err != nil {
				return err
			}
			s.gweDropCSS(kit.ID)
		}
	}
	values := map[string]any{}
	for k, v := range rec.Options {
		values[k] = v
	}
	values[gweStarterOption] = nil
	if err := s.settings.Set(ctx, values); err != nil {
		return err
	}
	if rec.Theme != "" && rec.Theme != s.settings.String("stylesheet") {
		if t := s.installedTheme(rec.Theme); t != nil && !t.Custom {
			if err := s.switchTheme(ctx, rec.Theme); err != nil {
				log.Printf("golementor setup: restoring theme %s: %v", rec.Theme, err)
			}
		}
	}
	s.purgeAll(ctx)
	return nil
}

// --- screen ------------------------------------------------------------------------------------

type gweSetupKitView struct {
	Slug, Title, Category, Description, Screenshot, PreviewURL string
	Colors                                                     []string // primary, secondary, text, accent
	Fonts                                                      [2]string
	Pages                                                      []gweSetupPageView
	Parts, Posts                                               int
}

type gweSetupPageView struct {
	Slug, Title       string
	Checked, Required bool
	Note              string
}

type gweSetupData struct {
	Nonce      string
	Kits       []gweSetupKitView
	Selected   string
	Palettes   []gweSetupPalette
	Pairings   []gweSetupPairing
	SiteName   string
	Tagline    string
	LogoID     int64
	LogoURL    string
	Installed  *gweStarterRecord
	Counts     map[string]int
	Done       bool
	FrontURL   string
	FrontEdit  string
	Problems   []string // invalid library files (for administrators)
	PreviewURL string
}

// gweSetupScreen is the wizard.
func (s *Server) gweSetupScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_theme_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, gweSetupNonce) {
			s.denyNonce(c)
			return
		}
		fail := func(err error) { c.Redirect(http.StatusFound, gweSetupPath+"?error="+url.QueryEscape(err.Error())) }
		switch c.PostForm("action") {
		case "install":
			ch := gweSetupChoices{Kit: c.PostForm("kit"), Palette: c.PostForm("palette"), Fonts: c.PostForm("fonts"),
				SiteName: c.PostForm("blogname"), Tagline: c.PostForm("blogdescription"), LogoID: gweInt(c.PostForm("logo_id")),
				Mode: c.PostForm("mode"), Colors: map[string]string{}}
			for _, id := range gweLibGlobalIDs {
				ch.Colors[id] = c.PostForm("color_" + id)
			}
			if c.PostForm("pages_sent_"+ch.Kit) == "1" {
				ch.Pages = append([]string{}, c.PostFormArray("pages_"+ch.Kit+"[]")...)
			}
			if err := s.gweStarterInstall(c, u.ID, ch); err != nil {
				fail(err)
				return
			}
			c.Redirect(http.StatusFound, gweSetupPath+"?done=1")
		case "remove":
			if err := s.gweStarterRemove(c); err != nil {
				fail(err)
				return
			}
			c.Redirect(http.StatusFound, gweSetupPath+"?removed=1")
		default:
			fail(errors.New("Unknown action."))
		}
		return
	}
	lib := s.gweLib()
	d := gweSetupData{Nonce: s.nonce(c, gweSetupNonce), Palettes: gweSetupPalettes, Pairings: gweSetupPairings,
		SiteName: s.settings.String("blogname"), Tagline: s.settings.String("blogdescription"), Installed: s.gweStarter(),
		Done: c.Query("done") == "1", PreviewURL: gweSetupPath + "/preview"}
	if d.Tagline == "Just another WordPress site" {
		d.Tagline = ""
	}
	if _, kit := s.gweKitSettings(c); kit != nil {
		if m, ok := kit["site_logo"].(map[string]any); ok {
			d.LogoID = gweInt(m["id"])
			d.LogoURL, _ = m["url"].(string)
		}
	}
	for _, k := range lib.Kits {
		kv := gweSetupKitView{Slug: k.Slug, Title: k.Title, Category: k.Category, Description: k.Description,
			Screenshot: s.gweLibAssetURL(c, "kits/"+k.Slug+"/"+k.Screenshot), Parts: len(k.Parts), Posts: len(k.Posts),
			PreviewURL: gweSetupPath + "/preview?kit=" + url.QueryEscape(k.Slug)}
		byID := map[string]string{}
		for _, m := range k.Globals.Colors {
			byID[m["_id"].(string)], _ = m["color"].(string)
		}
		for _, id := range gweLibGlobalIDs {
			kv.Colors = append(kv.Colors, byID[id])
		}
		for _, m := range k.Globals.Typography {
			fam, _ := m["typography_font_family"].(string)
			switch m["_id"] {
			case "primary":
				kv.Fonts[0] = fam
			case "text":
				kv.Fonts[1] = fam
			}
		}
		for _, p := range k.Pages {
			pv := gweSetupPageView{Slug: p.Slug, Title: p.Title, Checked: !p.Optional, Required: p.FrontPage}
			switch {
			case p.FrontPage:
				pv.Note = "front page"
			case p.PostsPage:
				pv.Note = "posts page"
			}
			kv.Pages = append(kv.Pages, pv)
		}
		d.Kits = append(d.Kits, kv)
	}
	d.Selected = c.Query("kit")
	if d.Installed != nil && d.Selected == "" {
		d.Selected = d.Installed.Kit
	}
	if lib.kit(d.Selected) == nil && len(lib.Kits) > 0 {
		d.Selected = lib.Kits[0].Slug
	}
	if d.Installed != nil {
		d.Counts = s.gweStarterCounts(c)
		if id := int64(s.settings.Int("page_on_front")); id > 0 {
			if p, err := s.store.GetPost(c, id); err == nil {
				d.FrontURL, d.FrontEdit = s.links(c).Permalink(p), gweEditURL(p.ID)
			}
		}
	}
	if u.Can("manage_options", nil) {
		d.Problems = lib.Errors
	}
	var notices []notice
	if d.Done {
		notices = append(notices, notice{Type: "success", Message: "Your starter site is installed.", Dismissible: true})
	}
	if c.Query("removed") == "1" {
		notices = append(notices, notice{Type: "success", Message: "The starter site was removed and your previous settings restored.", Dismissible: true})
	}
	if e := c.Query("error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(html.EscapeString(e)), Dismissible: true})
	}
	s.enqueueMedia(c, 0)
	s.adminPage(c, "golementor-setup", "Site Setup", "golementor_page_golementor-setup", "admin/golementor-setup", d, notices...)
}

// gweStarterCounts counts what the installed starter site made.
func (s *Server) gweStarterCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	rows, err := s.store.Pool.Query(ctx, `SELECT type, count(*) FROM posts WHERE coalesce(meta->>'`+gweStarterMeta+`','') <> '' AND status <> 'trash' GROUP BY type`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int
		if rows.Scan(&t, &n) == nil {
			out[t] = n
		}
	}
	out["parts"] = out[gwePartType]
	return out
}

// --- live preview ------------------------------------------------------------------------------

// gweSetupPreview renders a kit page with the kit's header and footer and the chosen colors and
// fonts, without installing anything: ?kit=<slug>&page=<slug>|blog|post-<n>&palette=…&fonts=….
func (s *Server) gweSetupPreview(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_theme_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	k := s.gweLib().kit(c.Query("kit"))
	if k == nil {
		c.String(http.StatusNotFound, "That starter site does not exist.")
		return
	}
	ch := gweSetupChoices{Kit: k.Slug, Palette: c.Query("palette"), Fonts: c.Query("fonts"), Colors: map[string]string{}}
	for _, id := range gweLibGlobalIDs {
		ch.Colors[id] = c.Query("color_" + id)
	}
	pageURL := func(page string) string {
		q := url.Values{"kit": {k.Slug}, "page": {page}}
		for _, key := range []string{"palette", "fonts", "color_primary", "color_secondary", "color_text", "color_accent"} {
			if v := c.Query(key); v != "" {
				q.Set(key, v)
			}
		}
		return gweSetupPath + "/preview?" + q.Encode()
	}
	_, current := s.gweKitSettings(c)
	kitDoc := &builder.Document{ID: gweSetupPreviewID, Type: "kit", Settings: s.gweSetupKitSettings(c, current, k, ch)}
	rc := s.gweRenderContext(c, false)
	rc.Kit, rc.KitID = kitDoc, kitDoc.ID
	rc.Breakpoints = builder.KitBreakpoints(kitDoc)
	resolve := func(kind, arg string) string {
		switch kind {
		case "library":
			return s.gweLibAssetURL(c, arg)
		case "page":
			return pageURL(arg)
		}
		return "0"
	}
	td := s.gweThemeData(c)
	var menuItems func(items []gweLibKitMenuItem) []*builder.MenuItem
	n := int64(0)
	menuItems = func(items []gweLibKitMenuItem) []*builder.MenuItem {
		var out []*builder.MenuItem
		for _, it := range items {
			n++
			mi := &builder.MenuItem{ID: n, Label: it.Title, Kind: "custom", Object: "custom", URL: gweLibReplace(it.URL, false, resolve)}
			if it.Page != "" {
				mi.Kind, mi.Object, mi.URL = "post_type", "page", pageURL(it.Page)
				if p := k.page(it.Page); p != nil && mi.Label == "" {
					mi.Label = p.Title
				}
			}
			mi.Children = menuItems(it.Children)
			out = append(out, mi)
		}
		return out
	}
	td.Menu = func(location string, _ int64) []*builder.MenuItem {
		for _, m := range k.Menus {
			if m.Location == location {
				return menuItems(m.Items)
			}
		}
		return nil
	}
	if strings.TrimSpace(c.Query("blogname")) != "" {
		td.SiteName = c.Query("blogname")
	}
	postData := func(i int) *builder.PostData {
		kp := k.Posts[i]
		pd := &builder.PostData{ID: gweSetupPreviewID + 100 + int64(i), Type: "post", Title: html.EscapeString(kp.Title), URL: pageURL("post-" + strconv.Itoa(i+1)),
			Excerpt: gweLibReplace(kp.Excerpt, false, resolve), AuthorName: u.DisplayName, Class: "post type-post status-publish format-standard hentry"}
		date := time.Now().Add(-time.Duration(i+1) * 26 * time.Hour).In(s.loc())
		pd.Date, pd.DateISO, pd.Time = date.Format("January 2, 2006"), date.Format(time.RFC3339), date.Format("3:04 pm")
		if m := gweLibTokenRe.FindStringSubmatch(kp.Image); m != nil {
			pd.ImageURL = s.gweLibAssetURL(c, m[2])
			pd.Class += " has-post-thumbnail"
		}
		for _, cat := range kp.categories() {
			pd.Categories = append(pd.Categories, builder.TermLink{Name: html.EscapeString(cat), URL: "#"})
		}
		if pd.Excerpt == "" {
			pd.Excerpt = gweExcerptText(gweLibReplace(kp.Content, false, resolve))
		}
		return pd
	}
	part := func(typ string) *gweLibKitPart {
		for i := range k.Parts {
			if k.Parts[i].Type == typ {
				return &k.Parts[i]
			}
		}
		return nil
	}
	page := c.Query("page")
	var front, blog *gweLibKitPage
	for i := range k.Pages {
		if k.Pages[i].FrontPage {
			front = &k.Pages[i]
		}
		if k.Pages[i].PostsPage {
			blog = &k.Pages[i]
		}
	}
	if page == "" && front != nil {
		page = front.Slug
	}
	var mainRaw json.RawMessage
	mainType, title := "wp-page", k.Title
	switch {
	case strings.HasPrefix(page, "post-"):
		i, _ := strconv.Atoi(strings.TrimPrefix(page, "post-"))
		if i < 1 || i > len(k.Posts) {
			c.String(http.StatusNotFound, "No such post.")
			return
		}
		pd := postData(i - 1)
		td.Post = func() *builder.PostData { return pd }
		content := gweLibReplace(k.Posts[i-1].Content, false, resolve)
		td.PostContent = func() string { return content }
		if p := part("single-post"); p != nil {
			mainRaw, mainType = p.Content, "single-post"
		}
		title = k.Posts[i-1].Title
	case (blog != nil && page == blog.Slug) || page == "blog":
		a := &builder.ArchiveData{Title: "Blog", PlainTitle: "Blog", Page: 1, Pages: 1, Home: true, HomeTitle: "Blog"}
		if blog != nil {
			a.Title, a.PlainTitle, a.HomeTitle = html.EscapeString(blog.Title), html.EscapeString(blog.Title), html.EscapeString(blog.Title)
		}
		for i := range k.Posts {
			a.Posts = append(a.Posts, postData(i))
		}
		a.PageURL = func(int) string { return "#" }
		td.Archive = func() *builder.ArchiveData { return a }
		if p := part("archive"); p != nil {
			mainRaw, mainType = p.Content, "archive"
		}
		title = a.PlainTitle
	default:
		pg := k.page(page)
		if pg == nil {
			c.String(http.StatusNotFound, "No such page.")
			return
		}
		mainRaw, title = pg.Content, pg.Title
	}
	rc.Theme = td
	var results []gweRendered
	var css strings.Builder
	css.WriteString(builder.KitCSS(kitDoc, rc))
	render := func(raw json.RawMessage, typ string, id int64, location string) string {
		if len(bytes.TrimSpace(raw)) == 0 {
			return ""
		}
		els, err := builder.ParseElements([]byte(gweLibReplace(string(raw), true, resolve)))
		if err != nil {
			return ""
		}
		res, err := builder.Render(&builder.Document{ID: id, Type: typ, Elements: els}, rc)
		if err != nil || res == nil {
			log.Printf("golementor setup preview: %s: %v", typ, err)
			return ""
		}
		css.WriteString("\n" + res.CSS)
		results = append(results, gweRendered{post: &store.Post{ID: id, Type: "page", Title: title}, res: res})
		out := res.HTML
		if location != "" {
			out = strings.Replace(out, `class="elementor elementor-`+itoa64(id)+`"`, `class="elementor elementor-`+itoa64(id)+` elementor-location-`+location+`"`, 1)
		}
		return out
	}
	var header, footer string
	if p := part("header"); p != nil {
		header = render(p.Content, "header", gweSetupPreviewID+1, "header")
	}
	main := render(mainRaw, mainType, gweSetupPreviewID+2, "")
	if p := part("footer"); p != nil {
		footer = render(p.Content, "footer", gweSetupPreviewID+3, "footer")
	}
	for _, r := range results {
		s.gweRecord(c, r.post, r.res)
	}
	assets := s.links(c).HomeURL(gweAssetsPath)
	ver := "?ver=" + gweAssetVer()
	var head strings.Builder
	if t := helloTheme(); t != nil {
		head.WriteString(gweLink("hello-gowebeditor", t.URI()+"/style.css?ver="+helloThemeVersion))
	}
	head.WriteString(gweLink("elementor-frontend", assets+"css/frontend.min.css"+ver))
	head.WriteString(gweLink("widget-theme-elements", assets+"css/widget-theme-elements.min.css"+ver))
	seen := map[string]bool{}
	for _, r := range results {
		for _, h := range r.res.Styles {
			if !seen[h] {
				seen[h] = true
				if rel := s.gweStyleHref(h); rel != "" {
					head.WriteString(gweLink(h, assets+rel+ver))
				}
			}
		}
	}
	var fams []string
	for _, r := range results {
		fams = append(fams, r.res.Fonts...)
	}
	for _, list := range []string{"system_typography", "custom_typography"} {
		items, _ := kitDoc.Settings[list].([]any)
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				if f, _ := m["typography_font_family"].(string); f != "" {
					fams = append(fams, f)
				}
			}
		}
	}
	sort.Strings(fams)
	head.WriteString(s.gweGoogleFontsHead(c, fams))
	head.WriteString(gweScript("jquery-core", "/wp-includes/js/jquery/jquery.min.js?ver=3.7.1"))
	var b strings.Builder
	esc := html.EscapeString
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en-US\">\n<head>\n<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<meta name=\"robots\" content=\"noindex\">\n<title>" + esc(title+" ‹ Preview of "+k.Title) + "</title>\n")
	b.WriteString(head.String())
	b.WriteString("<style id=\"golementor-setup-preview-css\">\n" + strings.ReplaceAll(css.String(), "</", `<\/`) + "\n")
	b.WriteString(".gwe-setup-previewbar{position:sticky;top:0;z-index:99999;display:flex;gap:12px;align-items:center;justify-content:space-between;padding:8px 16px;background:#1d2327;color:#f0f0f1;font:13px/1.4 -apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif}.gwe-setup-previewbar a{color:#72aee6}.gwe-setup-previewbar strong{color:#fff}\n</style>\n</head>\n")
	b.WriteString("<body class=\"gwe-theme gwe-setup-preview elementor-default elementor-kit-" + itoa64(kitDoc.ID) + "\">\n")
	b.WriteString("<div class=\"gwe-setup-previewbar\"><span>Preview of <strong>" + esc(k.Title) + "</strong> &middot; nothing is installed yet</span><a href=\"" +
		esc(gweSetupPath+"?kit="+url.QueryEscape(k.Slug)) + "\" target=\"_top\">Back to Site Setup</a></div>\n")
	b.WriteString(header + "\n<main id=\"content\" class=\"site-main\">\n" + main + "\n</main>\n" + footer + "\n")
	b.WriteString(string(s.gweFooter(c)))
	b.WriteString("</body>\n</html>\n")
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=UTF-8", []byte(b.String()))
}

var gweSetupTagRe = regexp.MustCompile(`<[^>]*>`)

// gweExcerptText is a short plain-text excerpt of block content.
func gweExcerptText(content string) string {
	words := strings.Fields(html.UnescapeString(gweSetupTagRe.ReplaceAllString(content, " ")))
	if len(words) > 30 {
		words = append(words[:30], "…")
	}
	return html.EscapeString(strings.Join(words, " "))
}
