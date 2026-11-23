package server

import (
	"context"
	"fmt"
	"html"
	"strings"

	"wpgo/internal/blocks"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// A WordPress site with a block theme keeps its Site Editor work as posts of its own types. Pursae
// has no Site Editor (site-wide design is done in Pursae Web Builder's Theme Builder), so the
// importer counts these items instead of logging each one as an invalid post type, and the report
// points at the Theme Builder. Block theme navigation menus (wp_navigation) are the exception:
// they hold the site's links, which become ordinary menus that the Theme Builder's Menu widget and
// Hello Pursae's header use.

// wxrSiteEditorTypes are the Site Editor post types that are counted, not imported.
var wxrSiteEditorTypes = map[string]bool{
	"wp_template": true, "wp_template_part": true, "wp_global_styles": true,
	"wp_font_family": true, "wp_font_face": true,
}

// wxrNavLink is one link of a block navigation menu.
type wxrNavLink struct {
	Label, URL, Kind, Type string
	ID                     int64
	Children               []wxrNavLink
}

// wxrNavLinks reads the links of a wp_navigation post's block content: navigation links,
// submenus (with their links as sub-items) and the home link; other blocks (page lists, search,
// social icons, spacers) carry no fixed link and are left out.
func wxrNavLinks(content string) []wxrNavLink {
	var walk func(list []*blocks.Block) []wxrNavLink
	walk = func(list []*blocks.Block) []wxrNavLink {
		var out []wxrNavLink
		for _, b := range list {
			switch b.Name() {
			case "core/navigation-link", "core/navigation-submenu", "core/home-link":
				l := wxrNavLink{Label: wxrNavText(b.Attrs["label"]), URL: wxrNavText(b.Attrs["url"]),
					Kind: wxrNavText(b.Attrs["kind"]), Type: wxrNavText(b.Attrs["type"])}
				if f, ok := b.Attrs["id"].(float64); ok && f > 0 {
					l.ID = int64(f)
				}
				if b.Name() == "core/home-link" {
					l.Kind, l.Type, l.ID = "custom", "", 0
					if l.URL == "" {
						l.URL = "/"
					}
					if l.Label == "" {
						l.Label = "Home"
					}
				}
				l.Children = walk(b.InnerBlocks)
				if l.Label != "" && (l.URL != "" || l.ID != 0 || len(l.Children) > 0) {
					out = append(out, l)
				}
			default:
				// A group or a navigation block nested in the menu: its links count as the menu's own.
				out = append(out, walk(b.InnerBlocks)...)
			}
		}
		return out
	}
	return walk(blocks.Parse(content))
}

// wxrNavSafeURL keeps a link of an imported file only when esc_url() would print it (an allowed
// protocol or a relative URL); javascript:, data: and the like are stored as "#", so no consumer of
// the menu (REST, the Web Builder's menu slots) is handed a script URL by an untrusted file.
func wxrNavSafeURL(u string) string {
	if strings.TrimSpace(u) != "" && wp.CleanURL(u) == "" {
		return "#"
	}
	return u
}

// wxrNavText is a block attribute as plain text (labels may hold inline markup).
func wxrNavText(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(html.UnescapeString(wp.StripTags(s)))
}

// wxrImportNavigations turns the published wp_navigation posts of the file into menus. Links to
// imported posts and terms point at them here; any other link keeps its URL. A menu whose name a
// menu here already has is left alone, so a second run adds nothing. The first menu goes to the
// Primary Menu location when no menu is assigned there yet. It returns the number of menus made.
func (s *Server) wxrImportNavigations(ctx context.Context, navs []*wxrItem, idMap, oldTerms map[int64]int64, rep *importReport) int {
	if len(navs) == 0 {
		return 0
	}
	existing := map[string]bool{}
	primaryTaken := false
	if all, err := s.store.Menus(ctx); err == nil {
		for _, m := range all {
			existing[m.Slug] = true
			if m.HasLocation("primary") {
				primaryTaken = true
			}
		}
	}
	made := 0
	for _, it := range navs {
		if ctx.Err() != nil {
			break
		}
		name := strings.TrimSpace(it.Title)
		if name == "" {
			name = "Navigation"
		}
		links := wxrNavLinks(it.Content)
		if len(links) == 0 {
			rep.SkippedSiteEditor++
			continue
		}
		if existing[wp.SanitizeTitle(name)] {
			rep.Log = append(rep.Log, fmt.Sprintf("Menu “%s” already exists.", name))
			rep.Skipped++
			continue
		}
		menuID, err := s.store.CreateMenu(ctx, name)
		if err != nil {
			rep.Log = append(rep.Log, fmt.Sprintf("Failed to import the navigation menu “%s”: %v", name, err))
			rep.Skipped++
			continue
		}
		existing[wp.SanitizeTitle(name)] = true
		var add func(list []wxrNavLink, parent int64)
		add = func(list []wxrNavLink, parent int64) {
			for i, l := range list {
				mi := &store.EditItem{MenuID: menuID, ParentID: parent, Kind: "custom", URL: wxrNavSafeURL(l.URL), Label: l.Label, Order: i + 1, Status: "publish"}
				switch {
				case l.Kind == "post-type" && idMap[l.ID] != 0:
					mi.Kind, mi.Object, mi.TargetID, mi.URL = "post_type", defaultStr(l.Type, "page"), idMap[l.ID], ""
				case l.Kind == "taxonomy" && oldTerms[l.ID] != 0:
					obj := l.Type
					if obj == "tag" {
						obj = "post_tag"
					}
					mi.Kind, mi.Object, mi.TargetID, mi.URL = "taxonomy", defaultStr(obj, "category"), oldTerms[l.ID], ""
				case l.URL == "":
					mi.URL = "#" // a submenu heading without a link of its own
				}
				if err := s.store.AddMenuItem(ctx, mi); err != nil {
					rep.Log = append(rep.Log, fmt.Sprintf("Failed to import menu item “%s”: %v", l.Label, err))
					continue
				}
				rep.MenuItems++
				add(l.Children, mi.ID)
			}
		}
		add(links, 0)
		made++
		if !primaryTaken {
			if err := s.store.SetMenuLocations(ctx, map[string]int64{"primary": menuID}); err == nil {
				primaryTaken = true
				rep.Log = append(rep.Log, fmt.Sprintf("Menu “%s” was assigned to the Primary Menu location.", name))
			}
		}
	}
	return made
}

// wxrSiteEditorLog is the report line for the Site Editor items of the file, or "" when it had none.
func wxrSiteEditorLog(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("Skipped %d Site Editor items (block theme templates, template parts, global styles and fonts): design the header, footer and templates in the Theme Builder of Pursae Web Builder.", n)
}
