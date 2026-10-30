package server

import (
	"context"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

// Demo content sets of a bundled theme (Theme.Demos). A bundled theme can ship several demo.json
// files, each with its own images, under web/themes/<theme>/demos/<set>/demo/ (the same layout as a
// package's demo, docs/theme-package.md "Demo content"). Appearance › Themes › Import Demo Content
// opens a chooser (themes?action=demo-content); one set is on the site at a time: importing another
// set first removes the one imported before (its pages, posts, images, terms and menus go, and the
// options, reading settings and theme mods it changed get their earlier values back), so no front
// page, menu or color scheme of the previous set stays behind.

// themeDemoSet is one demo content set of a bundled theme.
type themeDemoSet struct {
	Slug, Name, Description string
	// Image is the chooser's preview image, relative to the theme URI.
	Image string
}

// demoKey is the set's _wpgo_demo key: <theme>-<set>.
func (ds themeDemoSet) demoKey(t *Theme) string { return t.Slug + "-" + ds.Slug }

func (t *Theme) demoSet(slug string) *themeDemoSet {
	for i := range t.Demos {
		if t.Demos[i].Slug == slug {
			return &t.Demos[i]
		}
	}
	return nil
}

// themeHasDemo reports whether a theme has demo content: a package's demo.json or bundled sets.
func themeHasDemo(t *Theme) bool {
	return t != nil && ((t.Package != nil && t.Package.Manifest.Demo != "") || len(t.Demos) > 0)
}

// bundledDemoManifest is the manifest a bundled set validates against: the theme's menu locations,
// widget areas and Customizer settings.
func bundledDemoManifest(t *Theme) *themeManifest {
	m := &themeManifest{Slug: t.Slug, Name: t.Name, Demo: "demo/demo.json", pageTemplates: t.Templates}
	for _, l := range t.MenuLocations {
		m.MenuLocations = append(m.MenuLocations, themeManifestLocation{ID: l.Slug, Name: l.Name})
	}
	for _, a := range t.WidgetAreas {
		m.WidgetAreas = append(m.WidgetAreas, themeManifestArea{ID: a.ID, Name: a.Name})
	}
	m.Customizer.Sections = []themeCzSection{{ID: "theme", Title: t.Name, Settings: demoThemeSettings(t)}}
	return m
}

// bundledDemoSource reads and validates a bundled set.
func (s *Server) bundledDemoSource(t *Theme, ds *themeDemoSet) (*demoSource, error) {
	fsys, err := fs.Sub(s.web, "themes/"+t.Slug+"/demos/"+ds.Slug)
	if err != nil {
		return nil, err
	}
	d, err := parseThemeDemo(fsys, bundledDemoManifest(t))
	if err != nil {
		return nil, err
	}
	return &demoSource{Key: ds.demoKey(t), FS: fsys, Demo: d}, nil
}

// importedDemoSet is the set of t whose content is on the site (nil when none).
func (s *Server) importedDemoSet(t *Theme) *themeDemoSet {
	for i := range t.Demos {
		if s.demoImported(t.Demos[i].demoKey(t)) {
			return &t.Demos[i]
		}
	}
	return nil
}

// importDemoSet imports set ds of bundled theme t, removing the set imported before.
func (s *Server) importDemoSet(ctx context.Context, uid int64, t *Theme, ds *themeDemoSet) (*demoReport, error) {
	src, err := s.bundledDemoSource(t, ds)
	if err != nil {
		return nil, err
	}
	var samples []int64 // the install samples the previous set put in the trash stay there
	for i := range t.Demos {
		other := t.Demos[i].demoKey(t)
		prev := s.demoRecord(other)
		if other == src.Key || prev == nil {
			continue
		}
		if err := s.removeThemeDemo(ctx, other); err != nil {
			return nil, err
		}
		samples = append(samples, prev.Trashed...)
	}
	if len(samples) > 0 && s.demoRecord(src.Key) == nil {
		rec := &demoRecord{Done: map[string]bool{"samples": true}}
		for _, id := range samples {
			if s.store.TrashPost(ctx, id) == nil {
				rec.Trashed = append(rec.Trashed, id)
			}
		}
		if err := s.saveDemoRecord(ctx, src.Key, rec); err != nil {
			return nil, err
		}
	}
	return s.importDemo(ctx, uid, t, src)
}

// demoChooserURL is the chooser of a bundled theme's demo sets.
func demoChooserURL(slug string) string {
	return "/wp-admin/themes?action=demo-content&stylesheet=" + url.QueryEscape(slug)
}

type demoSetCard struct {
	themeDemoSet
	ImageURL             string
	ImportURL, RemoveURL string
	Imported             bool
}

type demoChooserData struct {
	Theme     *Theme
	Sets      []demoSetCard
	Active    bool
	Imported  string // the name of the set on the site
	ThemesURL string
}

// themeDemoChooser is Appearance › Themes › Import Demo Content for a theme with bundled sets.
func (s *Server) themeDemoChooser(c *gin.Context, t *Theme) {
	nonce := s.nonce(c, "import-demo_"+t.Slug)
	rmNonce := s.nonce(c, "remove-demo_"+t.Slug)
	d := demoChooserData{Theme: t, Active: s.activeTheme().Slug == t.Slug, ThemesURL: "/wp-admin/themes"}
	cur := s.importedDemoSet(t)
	for _, ds := range t.Demos {
		card := demoSetCard{themeDemoSet: ds, ImageURL: t.URI() + "/" + ds.Image + "?ver=" + t.Version}
		q := "&stylesheet=" + url.QueryEscape(t.Slug) + "&demo=" + url.QueryEscape(ds.Slug)
		card.ImportURL = "/wp-admin/themes?action=import-demo" + q + "&_wpnonce=" + nonce
		if cur != nil && cur.Slug == ds.Slug {
			card.Imported = true
			card.RemoveURL = "/wp-admin/themes?action=remove-demo" + q + "&_wpnonce=" + rmNonce
			d.Imported = ds.Name
		}
		d.Sets = append(d.Sets, card)
	}
	var notices []notice
	if msg := c.Query("error"); msg != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(msg)), Dismissible: true})
	}
	if !d.Active {
		notices = append(notices, notice{Type: "warning", Message: template.HTML("Activate " + template.HTMLEscapeString(t.Name) + " before importing its demo content.")})
	}
	s.adminPage(c, "themes", "Import Demo Content", "themes-php wpgo-demo-chooser", "admin/theme-demos", d, notices...)
}

// themeDemoAction runs import-demo and remove-demo for a theme with bundled sets.
func (s *Server) themeDemoAction(c *gin.Context, t *Theme, action string) {
	ds := t.demoSet(c.Query("demo"))
	if ds == nil {
		c.String(http.StatusBadRequest, "Unknown demo content.")
		return
	}
	back := demoChooserURL(t.Slug)
	if action == "remove-demo" {
		if err := s.removeThemeDemo(c.Request.Context(), ds.demoKey(t)); err != nil {
			c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("The demo content could not be removed: "+err.Error()))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/themes?demo=removed")
		return
	}
	if s.activeTheme().Slug != t.Slug {
		c.String(http.StatusBadRequest, "Activate the theme before importing its demo content.")
		return
	}
	rep, err := s.importDemoSet(c.Request.Context(), currentUser(c).ID, t, ds)
	if err != nil {
		var de *demoError
		if errors.As(err, &de) {
			err = errors.New(de.Error())
		}
		c.Redirect(http.StatusFound, back+"&error="+url.QueryEscape("The demo content could not be imported: "+err.Error()))
		return
	}
	c.Redirect(http.StatusFound, "/wp-admin/themes?"+rep.query())
}
