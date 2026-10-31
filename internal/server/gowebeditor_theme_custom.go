package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Saved Golementor themes ("Save as theme"). A saved theme is a row of the themes table with
// custom = true and a JSON snapshot in data: the kit settings (Site Settings, custom CSS), every
// theme part with its element tree, page settings and display conditions, and the menu locations.
// The snapshot is fixed: it only changes when the user saves the theme again (as a new entry).
// Alongside the row the theme owns one kit post tagged _gwe_theme = slug, so that Live Preview
// can render the snapshot with its own global styles.
//
// The Themes screen lists saved themes in their own section. Activating one loads its JSON into
// the working copy (the site's parts and kit, theme_hellogowebeditor.go) and makes Hello
// Golementor the active theme. Exports and imports carry the snapshot as manifest.json.

const gweThemeFormat = "gowebeditor-theme"

// gweSnapshot is a custom theme's data column (and the manifest.json of an exported theme).
type gweSnapshot struct {
	Format      string         `json:"format"`
	Version     int            `json:"version"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Author      string         `json:"author"`
	KitID       int64          `json:"kit_id,omitempty"`
	Kit         map[string]any `json:"kit"`
	Parts       []*gwePart     `json:"parts"`
	Menus       []MenuLocation `json:"menu_locations"`
	// CustomCSS is the Customizer's Additional CSS of the working copy (Hello Golementor's).
	CustomCSS string    `json:"custom_css,omitempty"`
	Saved     time.Time `json:"saved"`
	// Media lists the attachments an export carries (file name in the zip -> original id and URL).
	Media []gweMediaRef `json:"media,omitempty"`
}

type gweMediaRef struct {
	ID   int64  `json:"id"`
	URL  string `json:"url"`
	File string `json:"file"`
}

// gweMenuLocations are the menu locations of every saved theme (those of Hello Golementor).
var gweMenuLocations = helloMenuLocations

var (
	gweSnapMu sync.RWMutex
	gweSnaps  = map[string]*gweSnapshot{}
)

func gweSnapshotFor(slug string) *gweSnapshot {
	gweSnapMu.RLock()
	defer gweSnapMu.RUnlock()
	return gweSnaps[slug]
}

// gweThemeRow is a custom theme as stored.
type gweThemeRow struct {
	Slug, Name, Description, Author, Version string
	ScreenshotID                             int64
	Data                                     *gweSnapshot
}

// gweRegisterCustom adds (or refreshes) a custom theme in the theme registry.
func (s *Server) gweRegisterCustom(ctx context.Context, row *gweThemeRow) {
	if row.Data == nil {
		row.Data = &gweSnapshot{Format: gweThemeFormat, Version: 1}
	}
	gweSnapMu.Lock()
	gweSnaps[row.Slug] = row.Data
	gweSnapMu.Unlock()
	shot := s.links(ctx).HomeURL("/wp-content/web-builder/theme-screenshot/" + row.Slug + ".svg?ver=" + strconv.FormatInt(row.Data.Saved.Unix(), 10))
	if row.ScreenshotID > 0 {
		if a, err := s.store.GetAttachment(ctx, row.ScreenshotID); err == nil {
			shot = s.uploadURL(ctx, a.FilePath)
			if v := a.Variant("medium_large"); v != nil {
				shot = s.uploadURL(ctx, v.FilePath)
			}
		}
	}
	desc := row.Description
	if desc == "" {
		desc = "A design saved from the Pursae Web Builder Theme Builder."
	}
	author := row.Author
	if author == "" {
		author = "Pursae Web Builder"
	}
	registerTheme(&Theme{
		Slug: row.Slug, Name: row.Name, Version: row.Version, Description: desc, Author: author,
		Tags: []string{"Pursae Web Builder", "Custom Logo", "Custom Menu", "Full Width Template"}, Custom: true, ScreenshotURL: shot,
		MenuLocations: gweMenuLocations,
		WidgetAreas:   []WidgetArea{{ID: "sidebar-1", Name: "Sidebar", Description: "Widgets for the Pursae Web Builder Sidebar widget."}},
		Supports: map[string]any{"custom-logo": map[string]any{"height": 100, "width": 350, "flex-height": true, "flex-width": true},
			"title-tag": true, "post-thumbnails": true, "menus": true, "html5": true, "align-wide": true, "responsive-embeds": true},
	})
}

// gweUnloadCustomThemes drops every custom theme from the registry (they stay in the database).
func (s *Server) gweUnloadCustomThemes() {
	themesMu.Lock()
	for slug, t := range themes {
		if t.Custom {
			delete(themes, slug)
		}
	}
	themesMu.Unlock()
	gweSnapMu.Lock()
	gweSnaps = map[string]*gweSnapshot{}
	gweSnapMu.Unlock()
}

// gweLoadCustomThemes registers the custom themes stored in this site's database (startup).
func (s *Server) gweLoadCustomThemes(ctx context.Context) {
	s.gweUnloadCustomThemes()
	rows, err := s.gweThemeRows(ctx, "")
	if err != nil {
		log.Printf("gowebeditor: custom themes: %v", err)
		return
	}
	for _, r := range rows {
		s.gweRegisterCustom(ctx, r)
	}
}

func (s *Server) gweThemeRows(ctx context.Context, slug string) ([]*gweThemeRow, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT slug, name, description, author, version, coalesce(screenshot_id,0), data FROM themes
		WHERE custom AND ($1 = '' OR slug = $1) ORDER BY lower(name), slug`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*gweThemeRow
	for rows.Next() {
		r := &gweThemeRow{}
		var data []byte
		if err := rows.Scan(&r.Slug, &r.Name, &r.Description, &r.Author, &r.Version, &r.ScreenshotID, &data); err != nil {
			return nil, err
		}
		r.Data = &gweSnapshot{}
		_ = json.Unmarshal(data, r.Data)
		if r.Name == "" {
			r.Name = r.Slug
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var gweSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// gweUniqueSlug makes a theme slug from a name: gwe-<name>, unique among installed themes.
func (s *Server) gweUniqueSlug(ctx context.Context, name string) string {
	base := strings.Trim(gweSlugRe.ReplaceAllString(strings.ToLower(wp.SanitizeTitle(name)), "-"), "-")
	if base == "" {
		base = "theme"
	}
	base = "gwe-" + base
	slug := base
	for n := 2; ; n++ {
		var exists bool
		_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM themes WHERE slug = $1)`, slug).Scan(&exists)
		if !exists && themeBySlug(slug) == nil {
			return slug
		}
		slug = base + "-" + strconv.Itoa(n)
	}
}

// gweCopyPart duplicates a part post into theme (a new post tagged _gwe_theme = theme).
func (s *Server) gweCopyPart(ctx context.Context, src *gwePart, theme string, authorID int64) (*store.Post, error) {
	conds := make([]any, 0, len(src.Conditions))
	for _, c := range src.Conditions {
		conds = append(conds, c)
	}
	settings := src.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	status := src.Status
	if status == "" {
		status = "publish"
	}
	p := &store.Post{Type: gwePartType, Title: src.Title, Status: status, AuthorID: authorID, CommentStatus: "closed", PingStatus: "closed",
		Meta: map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": src.Type, "_elementor_version": gweVersion,
			"_elementor_data": src.Elements, "_elementor_page_settings": settings, "_gwe_conditions": conds, "_gwe_theme": theme}}
	if p.Meta["_elementor_data"] == "" {
		p.Meta["_elementor_data"] = "[]"
	}
	if err := s.store.InsertPost(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// gweSaveThemeInput is the "Save as theme" form.
type gweSaveThemeInput struct {
	Name, Description string
	ScreenshotID      int64
	Activate          bool
}

// gweSaveAsTheme snapshots the working copy (the site's kit and theme parts) into a new saved
// theme. With Activate, Hello Golementor becomes the active theme (the working copy is already
// the saved design).
func (s *Server) gweSaveAsTheme(c *gin.Context, in gweSaveThemeInput) (string, error) {
	u := currentUser(c)
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "", errors.New("Please enter a theme name.")
	}
	posts, err := s.gwePartPosts(c, "")
	if err != nil {
		return "", err
	}
	gweSortParts(posts)
	kit, err := s.gweKit(c)
	if err != nil {
		return "", err
	}
	slug := s.gweUniqueSlug(c, in.Name)
	parts := make([]*gwePart, 0, len(posts))
	for _, p := range posts {
		parts = append(parts, gwePartFromPost(p))
	}
	author, authorID := "", int64(0)
	if u != nil {
		author, authorID = u.DisplayName, u.ID
	}
	if err := s.gweCreateTheme(c, authorID, slug, in, author, gwePageSettings(kit), parts, s.gweWorkingCSS()); err != nil {
		return "", err
	}
	if err := s.settings.Set(c, map[string]any{"gwe_loaded_theme": slug}); err != nil {
		return "", err
	}
	if in.Activate && s.activeTheme().Slug != helloThemeSlug {
		return slug, s.switchTheme(c, helloThemeSlug)
	}
	return slug, nil
}

// gweCreateTheme stores a saved theme: its snapshot row and a kit post for live previews. It
// does not activate anything.
func (s *Server) gweCreateTheme(c context.Context, authorID int64, slug string, in gweSaveThemeInput, author string, kitSettings map[string]any, parts []*gwePart, customCSS string) error {
	if err := s.gweEnsureLibraryType(c); err != nil {
		return err
	}
	kitSettings = gweCloneSettings(kitSettings)
	kit := &store.Post{Type: gweLibrary, Title: in.Name + " Kit", Slug: slug + "-kit", Status: "publish", AuthorID: authorID,
		CommentStatus: "closed", PingStatus: "closed",
		Meta: map[string]any{"_elementor_edit_mode": "builder", "_elementor_template_type": "kit", "_elementor_version": gweVersion,
			"_elementor_data": "[]", "_elementor_page_settings": kitSettings, "_gwe_theme": slug}}
	if err := s.store.InsertPost(c, kit); err != nil {
		return err
	}
	snap := &gweSnapshot{Format: gweThemeFormat, Version: 1, Name: in.Name, Description: in.Description, Author: author,
		KitID: kit.ID, Kit: kitSettings, Parts: parts, Menus: gweMenuLocations, CustomCSS: customCSS, Saved: time.Now().UTC()}
	if snap.Parts == nil {
		snap.Parts = []*gwePart{}
	}
	data, _ := json.Marshal(snap)
	if _, err := s.store.Pool.Exec(c, `INSERT INTO themes (slug, version, status, custom, name, description, author, screenshot_id, data)
		VALUES ($1, '1.0.0', 'inactive', true, $2, $3, $4, $5, $6::jsonb)`,
		slug, in.Name, in.Description, author, nullable64(in.ScreenshotID), string(data)); err != nil {
		return err
	}
	s.gweRegisterCustom(c, &gweThemeRow{Slug: slug, Name: in.Name, Description: in.Description, Author: author, Version: "1.0.0",
		ScreenshotID: in.ScreenshotID, Data: snap})
	return nil
}

// gweMigrateActiveSavedTheme handles databases from before Hello Golementor, where a saved
// theme could be the active theme itself: its design is loaded into the working copy and Hello
// Golementor takes over (startup, once).
func (s *Server) gweMigrateActiveSavedTheme(ctx context.Context) {
	t := themeBySlug(s.settings.String("stylesheet"))
	if t == nil || !t.Custom {
		return
	}
	kitID := int64(s.settings.Int("gwe_site_kit"))
	if kitID > 0 {
		if p, err := s.store.GetPost(ctx, kitID); err != nil || p.Status == "trash" {
			kitID = 0
		}
	}
	if err := s.settings.Set(ctx, map[string]any{"elementor_active_kit": kitID}); err != nil {
		log.Printf("gowebeditor: migrate active theme: %v", err)
		return
	}
	if err := s.switchTheme(ctx, t.Slug); err != nil {
		log.Printf("gowebeditor: migrate active theme %s: %v", t.Slug, err)
	}
}

// gweDeleteTheme removes a saved theme with its kit post (and the part copies of a pre-Hello
// theme). The working copy is not touched.
func (s *Server) gweDeleteTheme(ctx context.Context, slug string) error {
	t := themeBySlug(slug)
	if t == nil || !t.Custom {
		return errors.New("That theme is not a saved Pursae Web Builder theme.")
	}
	if s.activeTheme().Slug == slug {
		return errors.New("The active theme cannot be deleted. Activate another theme first.")
	}
	if s.settings.String("gwe_loaded_theme") == slug {
		if err := s.settings.Set(ctx, map[string]any{"gwe_loaded_theme": ""}); err != nil {
			return err
		}
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM posts WHERE meta->>'_gwe_theme' = $1 AND type = ANY($2)`, slug, []string{gwePartType, gweLibrary})
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := s.store.DeletePost(ctx, id); err != nil {
			return err
		}
		s.gweDropCSS(id)
	}
	if _, err := s.store.Pool.Exec(ctx, `DELETE FROM themes WHERE slug = $1 AND custom`, slug); err != nil {
		return err
	}
	themesMu.Lock()
	delete(themes, slug)
	themesMu.Unlock()
	gweSnapMu.Lock()
	delete(gweSnaps, slug)
	gweSnapMu.Unlock()
	return nil
}

// gweThemeScreenshot serves the generated screenshot of a saved theme without an uploaded one:
// the theme name over the kit's colors.
func (s *Server) gweThemeScreenshot(c *gin.Context) {
	slug := strings.TrimSuffix(c.Param("slug"), ".svg")
	t := themeBySlug(slug)
	if t == nil || !t.Custom {
		c.Status(http.StatusNotFound)
		return
	}
	colors := map[string]string{"primary": "#6EC1E4", "secondary": "#54595F", "text": "#7A7A7A", "accent": "#61CE70"}
	parts := map[string]bool{}
	if snap := gweSnapshotFor(slug); snap != nil {
		if list, ok := snap.Kit["system_colors"].([]any); ok {
			for _, it := range list {
				m, _ := it.(map[string]any)
				id, _ := m["_id"].(string)
				col, _ := m["color"].(string)
				if id != "" && gweHexColorRe.MatchString(col) {
					colors[id] = col
				}
			}
		}
		for _, p := range snap.Parts {
			parts[p.Type] = true
		}
	}
	esc := html.EscapeString
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="1200" height="900" viewBox="0 0 1200 900">`)
	fmt.Fprintf(&sb, `<rect width="1200" height="900" fill="#f6f7f8"/>`)
	fmt.Fprintf(&sb, `<rect width="1200" height="110" fill="%s"/>`, esc(colors["secondary"]))
	fmt.Fprintf(&sb, `<rect x="80" y="38" width="160" height="34" rx="6" fill="%s"/>`, esc(colors["primary"]))
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&sb, `<rect x="%d" y="48" width="90" height="14" rx="7" fill="#ffffff" opacity=".75"/>`, 640+i*120)
	}
	fmt.Fprintf(&sb, `<rect x="80" y="170" width="1040" height="330" rx="10" fill="%s" opacity=".18"/>`, esc(colors["primary"]))
	fmt.Fprintf(&sb, `<text x="600" y="320" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" font-size="72" font-weight="700" fill="%s">%s</text>`,
		esc(colors["secondary"]), esc(t.Name))
	fmt.Fprintf(&sb, `<text x="600" y="390" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" font-size="28" fill="%s">Built with Pursae Web Builder</text>`, esc(colors["text"]))
	fmt.Fprintf(&sb, `<rect x="510" y="425" width="180" height="46" rx="23" fill="%s"/>`, esc(colors["accent"]))
	for i := 0; i < 3; i++ {
		x := 80 + i*360
		fmt.Fprintf(&sb, `<rect x="%d" y="550" width="320" height="170" rx="8" fill="#e3e6ea"/><rect x="%d" y="740" width="240" height="16" rx="8" fill="#cfd4d9"/><rect x="%d" y="770" width="280" height="12" rx="6" fill="#e3e6ea"/>`, x, x, x)
	}
	fmt.Fprintf(&sb, `<rect y="830" width="1200" height="70" fill="%s"/>`, esc(colors["secondary"]))
	var labels []string
	for _, pt := range gwePartTypes {
		if parts[pt.Type] {
			labels = append(labels, pt.Label)
		}
	}
	if len(labels) > 0 {
		fmt.Fprintf(&sb, `<text x="600" y="873" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" font-size="22" fill="#ffffff" opacity=".85">%s</text>`, esc(strings.Join(labels, " · ")))
	}
	sb.WriteString(`</svg>`)
	c.Header("Cache-Control", "public, max-age=300")
	c.Data(http.StatusOK, "image/svg+xml", []byte(sb.String()))
}

// --- export / import ---------------------------------------------------------------------------

// gweMediaIDRe finds attachment references in element settings ({"url": ..., "id": 12}); element
// ids are always strings, attachment ids numbers.
var gweMediaIDRe = regexp.MustCompile(`"id":\s*(\d+)`)

var gweHexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{3,8}$`)

// gweExportTheme writes a saved theme as a zip: manifest.json (the snapshot) and media/ with the
// attachments its parts, kit and screenshot use.
func (s *Server) gweExportTheme(c *gin.Context, slug string) ([]byte, error) {
	rows, err := s.gweThemeRows(c, slug)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("Theme not found.")
	}
	row := rows[0]
	snap := &gweSnapshot{}
	if b, err := json.Marshal(row.Data); err == nil {
		_ = json.Unmarshal(b, snap)
	}
	snap.Format, snap.Version = gweThemeFormat, 1
	snap.Name, snap.Description, snap.Author = row.Name, row.Description, row.Author
	snap.KitID, snap.Media = 0, nil
	if snap.Menus == nil {
		snap.Menus = gweMenuLocations
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	ids := map[int64]bool{}
	scan := func(text string) {
		for _, m := range gweMediaIDRe.FindAllStringSubmatch(text, -1) {
			if id, _ := strconv.ParseInt(m[1], 10, 64); id > 0 {
				ids[id] = true
			}
		}
	}
	for _, p := range snap.Parts {
		scan(p.Elements)
		b, _ := json.Marshal(p.Settings)
		scan(string(b))
	}
	kb, _ := json.Marshal(snap.Kit)
	scan(string(kb))
	screenshot := ""
	for id := range ids {
		a, err := s.store.GetAttachment(c, id)
		if err != nil {
			continue
		}
		f, err := os.Open(s.gocdnFetchBack(c, filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(a.FilePath))))
		if err != nil {
			continue
		}
		name := "media/" + strconv.FormatInt(id, 10) + "-" + path.Base(a.FilePath)
		w, err := zw.Create(name)
		if err == nil {
			_, err = io.Copy(w, f)
		}
		f.Close()
		if err != nil {
			return nil, err
		}
		snap.Media = append(snap.Media, gweMediaRef{ID: id, URL: s.uploadURL(c, a.FilePath), File: name})
	}
	if row.ScreenshotID > 0 {
		if a, err := s.store.GetAttachment(c, row.ScreenshotID); err == nil {
			if b, err := os.ReadFile(s.gocdnFetchBack(c, filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(a.FilePath)))); err == nil {
				screenshot = "screenshot" + path.Ext(a.FilePath)
				if w, err := zw.Create(screenshot); err == nil {
					_, _ = w.Write(b)
				}
			}
		}
	}
	manifest := map[string]any{"theme": snap, "screenshot": screenshot, "generator": "Pursae " + Version}
	mb, _ := json.MarshalIndent(manifest, "", "\t")
	w, err := zw.Create("manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(mb); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// gweImportTheme creates a saved theme from an exported zip; media files become new attachments
// and the parts' references are rewritten to them. With activate, the imported design is loaded
// into the working copy and Hello Golementor activated.
func (s *Server) gweImportTheme(c *gin.Context, data []byte, activate bool) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("The file is not a theme export (.zip).")
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	mf := files["manifest.json"]
	if mf == nil {
		return "", errors.New("The archive has no manifest.json.")
	}
	read := func(f *zip.File) ([]byte, error) {
		if f.UncompressedSize64 > uint64(maxUploadBytes) {
			return nil, errors.New("A file in the archive is too large.")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, int64(maxUploadBytes)+1))
	}
	mb, err := read(mf)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Theme      *gweSnapshot `json:"theme"`
		Screenshot string       `json:"screenshot"`
	}
	if err := json.Unmarshal(mb, &manifest); err != nil || manifest.Theme == nil || manifest.Theme.Format != gweThemeFormat {
		return "", errors.New("The archive is not a Pursae Web Builder theme.")
	}
	snap := manifest.Theme
	// Media: new attachments, then rewrite ids and URLs in the parts and the kit.
	idMap := map[string]string{}
	var urlPairs []string
	for _, m := range snap.Media {
		f := files[m.File]
		if f == nil || strings.Contains(m.File, "..") {
			continue
		}
		b, err := read(f)
		if err != nil {
			return "", err
		}
		a, err := s.gweImportMedia(c, path.Base(m.File), b)
		if err != nil {
			continue
		}
		idMap[strconv.FormatInt(m.ID, 10)] = strconv.FormatInt(a.ID, 10)
		if m.URL != "" {
			urlPairs = append(urlPairs, m.URL, s.uploadURL(c, a.FilePath), strings.ReplaceAll(m.URL, "/", `\/`), strings.ReplaceAll(s.uploadURL(c, a.FilePath), "/", `\/`))
		}
	}
	rewrite := func(text string) string {
		if len(urlPairs) > 0 {
			text = strings.NewReplacer(urlPairs...).Replace(text)
		}
		return gweMediaIDRe.ReplaceAllStringFunc(text, func(m string) string {
			sub := gweMediaIDRe.FindStringSubmatch(m)
			if nid, ok := idMap[sub[1]]; ok {
				return strings.Replace(m, sub[1], nid, 1)
			}
			return m
		})
	}
	for _, p := range snap.Parts {
		if gwePartLabel(p.Type) == "" {
			return "", fmt.Errorf("Unknown theme part type %q.", p.Type)
		}
		if _, err := json.Marshal(json.RawMessage(p.Elements)); err != nil || !strings.HasPrefix(strings.TrimSpace(p.Elements), "[") {
			p.Elements = "[]"
		}
		p.Elements = rewrite(p.Elements)
		if b, err := json.Marshal(p.Settings); err == nil {
			_ = json.Unmarshal([]byte(rewrite(string(b))), &p.Settings)
		}
	}
	kit := map[string]any{}
	if b, err := json.Marshal(snap.Kit); err == nil {
		_ = json.Unmarshal([]byte(rewrite(string(b))), &kit)
	}
	var shotID int64
	if f := files[manifest.Screenshot]; manifest.Screenshot != "" && f != nil {
		if b, err := read(f); err == nil {
			if a, err := s.gweImportMedia(c, snap.Name+path.Ext(manifest.Screenshot), b); err == nil {
				shotID = a.ID
			}
		}
	}
	name := strings.TrimSpace(snap.Name)
	if name == "" {
		name = "Imported Theme"
	}
	slug := s.gweUniqueSlug(c, name)
	var importer int64
	if u := currentUser(c); u != nil {
		importer = u.ID
	}
	if err := s.gweCreateTheme(c, importer, slug, gweSaveThemeInput{Name: name, Description: snap.Description, ScreenshotID: shotID},
		snap.Author, kit, snap.Parts, snap.CustomCSS); err != nil {
		return "", err
	}
	if activate {
		return slug, s.switchTheme(c, slug)
	}
	return slug, nil
}

// gweImportMedia stores an imported file as an attachment (like an upload).
func (s *Server) gweImportMedia(c *gin.Context, name string, data []byte) (*store.Attachment, error) {
	u := currentUser(c)
	st, err := media.Save(s.cfg.UploadsDir, s.settings.Bool("uploads_use_yearmonth_folders"), time.Now(), name, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	title := strings.TrimSuffix(filepath.Base(st.Original), filepath.Ext(st.Original))
	a := &store.Attachment{UploaderID: u.ID, FilePath: st.RelPath, Mime: st.Mime, FileSize: st.Size,
		Title: title, Slug: wp.SanitizeTitle(title), Meta: map[string]any{"filesize": st.Size}}
	if strings.HasPrefix(st.Mime, "image/") && st.Mime != "image/svg+xml" {
		if info, err := media.ProcessImage(s.cfg.UploadsDir, st.RelPath, s.imageSizes()); err == nil {
			a.Width, a.Height, a.FilePath, a.FileSize = info.Width, info.Height, info.RelPath, info.FileSize
			dir := path.Dir(info.RelPath)
			for _, v := range info.Variants {
				a.Variants = append(a.Variants, store.AttachmentVariant{Name: v.Name, FilePath: path.Join(dir, v.File),
					Width: v.Width, Height: v.Height, Mime: v.Mime, FileSize: v.Size})
			}
		}
	}
	if err := s.store.InsertAttachment(c, a); err != nil {
		return nil, err
	}
	return a, nil
}

// gweAbsURL makes a site-relative URL absolute.
func gweAbsURL(home, u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return strings.TrimRight(home, "/") + u
}
