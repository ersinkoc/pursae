package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"wpgo/internal/cli"
	"wpgo/internal/textfold"
)

// The theme commands (cli_extension.go has the shared plumbing). They work on the theme registry of
// the CLI process (bundled themes plus loadThemePackages); a running server keeps its own registry,
// so a package installed, updated or deleted here is seen there only after a restart, while a
// switch between themes it knows is live (template/stylesheet settings, NOTIFY).

// themeExtServer is extServer with the package themes registered.
func (e *cliEnv) themeExtServer(ctx context.Context) (*Server, error) {
	s, err := e.extServer(ctx)
	if err != nil {
		return nil, err
	}
	s.loadThemePackages()
	return s, nil
}

// extTemplates parses the templates theme installs validate against (checkThemeDir, afterThemeChange).
func (s *Server) extTemplates() error {
	if s.tmpl != nil {
		return nil
	}
	t, err := newTemplates(s)
	if err != nil {
		return cli.Errorf("templates: %v", err)
	}
	s.tmpl = t
	return nil
}

// themeSource is where a theme comes from: bundled, upload, hub, library or package (unknown).
func themeSource(t *Theme) string {
	if t.Package == nil {
		return "bundled"
	}
	if src := themePackageSource(t); src != "" {
		return src
	}
	return "package"
}

// themeCLIRow is themeJSON with WP-CLI field names: name is the slug, title the display name.
func (s *Server) themeCLIRow(t *Theme, active string) cli.Row {
	row := cli.Row(s.themeJSON(t))
	row["title"] = row["name"]
	row["name"] = t.Slug
	status := "inactive"
	if t.Slug == active {
		status = "active"
	}
	row["status"] = status
	row["source"] = themeSource(t)
	row["description"] = extPlainText(t.Description)
	row["author_uri"], row["theme_uri"] = t.AuthorURI, t.ThemeURI
	tags := append([]string{}, t.Tags...)
	row["tags"] = tags
	return row
}

// matchLibraryTheme reports whether a library theme matches a search term (name, slug,
// description, category and tags; case- and accent-insensitive). An empty term matches all.
func matchLibraryTheme(m *themeManifest, term string) bool {
	term = strings.TrimSpace(term)
	if term == "" {
		return true
	}
	hay := strings.Join(append([]string{m.Slug, m.Name, m.Description, m.Category, m.Style, m.Author}, m.Tags...), " ")
	return textfold.Contains(hay, term)
}

// themeStatusSummary is `theme status` without a slug.
func themeStatusSummary(rows []cli.Row) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d installed themes:\n", len(rows))
	width := 0
	for _, r := range rows {
		width = max(width, len(cli.Stringify(r["name"])))
	}
	for _, r := range rows {
		letter := "I"
		if r["status"] == "active" {
			letter = "A"
		}
		fmt.Fprintf(&b, "  %s %-*s %s\n", letter, width, cli.Stringify(r["name"]), cli.Stringify(r["version"]))
	}
	b.WriteString("\nLegend: A = Active, I = Inactive")
	return b.String()
}

// themeNotInstalled explains a slug that is not an installed theme.
func (s *Server) themeNotInstalled(slug string) error {
	if s.themeDeleted(slug) {
		return cli.Errorf("the '%s' theme was deleted; run 'theme install %s' to restore it", slug, slug)
	}
	return cli.Errorf("the '%s' theme could not be found", slug)
}

func registerThemeCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("theme", "Manage themes: bundled themes and installed theme packages.")
	themeFields := []string{"name", "status", "version", "source"}
	listRows := func(c *cli.Ctx, s *Server) []cli.Row {
		var ups map[string]hubItem
		if extFieldRequested(c, "update") || extFieldRequested(c, "update_version") {
			ups = s.hubThemeUpdates(c.Context)
		}
		active := s.activeTheme().Slug
		var out []cli.Row
		for _, t := range s.installedThemes() {
			if t.Custom {
				continue
			}
			row := s.themeCLIRow(t, active)
			row["update"], row["update_version"] = "none", ""
			if it, ok := ups[t.Slug]; ok {
				row["update"], row["update_version"] = "available", it.Version
			}
			out = append(out, row)
		}
		return out
	}
	r.Add(cli.Command{
		Path:  "theme list",
		Short: "List installed themes.",
		Long: "Fields: name (slug), title, status (active|inactive), version, source (bundled|upload|hub|library),\n" +
			"package, author, author_uri, theme_uri, description, tags, update, update_version. update and\n" +
			"update_version ask Pursae Manager and are only computed when requested with --fields or --field.",
		Flags:    []cli.Flag{{Name: "status", Usage: "Only themes with this status.", Values: []string{"active", "inactive"}}},
		Examples: []string{"theme list", "theme list --status=active --field=name", "theme list --fields=name,version,update,update_version"},
		Run: func(c *cli.Ctx) error {
			s, err := e.themeExtServer(c.Context)
			if err != nil {
				return err
			}
			var out []cli.Row
			for _, row := range listRows(c, s) {
				if st := c.Flag("status"); st != "" && row["status"] != st {
					continue
				}
				out = append(out, row)
			}
			return c.Print(out, cli.View{Fields: themeFields, ID: "name"})
		},
	})
	r.Add(cli.Command{
		Path:     "theme get",
		Short:    "Get details about a theme.",
		Args:     []cli.Arg{{Name: "theme", Usage: "Theme slug."}},
		Examples: []string{"theme get twentytwentyfive", "theme get my-theme --field=version"},
		Run: func(c *cli.Ctx) error {
			s, err := e.themeExtServer(c.Context)
			if err != nil {
				return err
			}
			t := s.installedTheme(c.Arg(0))
			if t == nil || t.Custom {
				return s.themeNotInstalled(c.Arg(0))
			}
			row := s.themeCLIRow(t, s.activeTheme().Slug)
			row["path"] = ""
			if t.Package != nil {
				row["path"] = t.Package.Dir
			}
			return c.PrintItem(row, []string{"name", "title", "version", "status", "source", "author", "author_uri", "theme_uri", "description", "tags", "path"})
		},
	})
	r.Add(cli.Command{
		Path:     "theme status",
		Short:    "Reveal the status of one or all themes.",
		Args:     []cli.Arg{{Name: "theme", Usage: "Theme slug.", Optional: true}},
		Examples: []string{"theme status", "theme status twentytwentyfive"},
		Run: func(c *cli.Ctx) error {
			s, err := e.themeExtServer(c.Context)
			if err != nil {
				return err
			}
			if slug := c.Arg(0); slug != "" {
				t := s.installedTheme(slug)
				if t == nil || t.Custom {
					return s.themeNotInstalled(slug)
				}
				status := "Inactive"
				if s.activeTheme().Slug == t.Slug {
					status = "Active"
				}
				c.Println(strings.Join([]string{
					"Theme " + t.Slug + " details:",
					"    Name: " + t.Name,
					"    Status: " + status,
					"    Version: " + t.Version,
					"    Source: " + themeSource(t),
					"    Author: " + t.Author,
					"    Description: " + extPlainText(t.Description),
				}, "\n"))
				return nil
			}
			c.Println(themeStatusSummary(listRows(c, s)))
			return nil
		},
	})
	r.Add(cli.Command{
		Path:  "theme activate",
		Short: "Activate a theme.",
		Long: "switch_theme(): stores the new theme, carries widgets over and purges the page cache. Running\n" +
			"servers switch live; a theme package installed after a server started needs a restart there.",
		Args:     []cli.Arg{{Name: "theme", Usage: "Theme slug."}},
		Examples: []string{"theme activate twentytwentyfive"},
		Run: func(c *cli.Ctx) error {
			s, err := e.themeExtServer(c.Context)
			if err != nil {
				return err
			}
			defer s.extFlushTelemetry(c.Context)
			return e.themeActivate(c, s, c.Arg(0))
		},
	})
	r.Add(cli.Command{
		Path:  "theme install",
		Short: "Install themes from a zip file or the theme library.",
		Long: "A file (or - for standard input) is validated and installed like an upload (Appearance > Add\n" +
			"Themes > Upload); an installed package is replaced only by a higher version. A slug restores a\n" +
			"deleted bundled theme, or installs the theme from the library (Pursae Manager, or\n" +
			"PURSAE_THEME_LIBRARY_DIR). Demo content is not imported. Running servers see new or updated\n" +
			"packages after a restart.",
		Args:     []cli.Arg{{Name: "theme", Usage: "Zip file, - or slug.", Repeat: true}},
		Flags:    []cli.Flag{{Name: "activate", Bool: true, Usage: "Activate the (last) installed theme."}},
		Examples: []string{"theme install ./my-theme.zip --activate", "theme install aurora", "theme search blog"},
		Run:      e.themeInstall,
	})
	r.Add(cli.Command{
		Path:  "theme delete",
		Short: "Delete themes.",
		Long: "A theme package's directory is removed; a bundled theme is hidden (and its edited files\n" +
			"removed) until 'theme install <slug>' restores it. The active theme cannot be deleted. Asks for\n" +
			"confirmation (--yes).",
		Args:     []cli.Arg{{Name: "theme", Usage: "Theme slug.", Repeat: true}},
		Examples: []string{"theme delete my-old-theme --yes"},
		Run: func(c *cli.Ctx) error {
			s, err := e.themeExtServer(c.Context)
			if err != nil {
				return err
			}
			defer s.extFlushTelemetry(c.Context)
			active := s.activeTheme().Slug
			for _, slug := range c.Args {
				if t := s.installedTheme(slug); t == nil || t.Custom {
					return s.themeNotInstalled(slug)
				}
				if slug == active {
					return cli.Errorf("you cannot delete the '%s' theme while it is active on the site", slug)
				}
			}
			if err := c.Confirm("Delete %d theme(s)?", len(c.Args)); err != nil {
				return err
			}
			if err := s.extTemplates(); err != nil { // a package's removal re-parses the templates (afterThemeChange)
				return err
			}
			ctx, err := e.extActionContext(c, s)
			if err != nil {
				return err
			}
			failed := 0
			for _, slug := range c.Args {
				var derr error
				s.extKeepCache(func() { derr = s.deleteTheme(ctx, slug) })
				if derr != nil {
					c.Warning("%s: %v", slug, derr)
					failed++
					continue
				}
				c.Success("Deleted '%s' theme.", slug)
			}
			c.Warning("running servers keep showing deleted theme packages until they restart")
			if failed > 0 {
				return cli.Errorf("only %d of %d themes deleted", len(c.Args)-failed, len(c.Args))
			}
			return nil
		},
	})
	r.Add(cli.Command{
		Path:  "theme update",
		Short: "Update themes installed from Pursae Manager.",
		Long:  "Uploaded theme packages are updated by installing the new zip. Running servers use the new\nversion after a restart.",
		Args:  []cli.Arg{{Name: "theme", Usage: "Theme slug.", Optional: true, Repeat: true}},
		Flags: []cli.Flag{
			{Name: "all", Bool: true, Usage: "Every theme with an update."},
			{Name: "dry-run", Bool: true, Usage: "Only list the available updates."},
		},
		Examples: []string{"theme update --all --dry-run", "theme update aurora"},
		Run:      e.themeUpdate,
	})
	r.Add(cli.Command{
		Path:     "theme search",
		Short:    "Search the theme library.",
		Long:     "Lists the library themes (Pursae Manager, or PURSAE_THEME_LIBRARY_DIR) whose name, slug,\ndescription, category or tags match. Without a term every library theme is listed.",
		Args:     []cli.Arg{{Name: "search", Usage: "Search term.", Optional: true}},
		Examples: []string{"theme search", "theme search portfolio --fields=name,version,category"},
		Run: func(c *cli.Ctx) error {
			s, err := e.themeExtServer(c.Context)
			if err != nil {
				return err
			}
			list, err := s.libraryThemesErr(c.Context)
			if errors.Is(err, errLibraryOff) {
				return cli.Errorf("no theme library is configured (Pursae Manager or PURSAE_THEME_LIBRARY_DIR)")
			}
			if err != nil {
				return cli.Errorf("theme library: %v", err)
			}
			var out []cli.Row
			for _, lt := range list {
				m := lt.Manifest
				if !matchLibraryTheme(m, c.Arg(0)) {
					continue
				}
				installed := ""
				if t := s.installedTheme(m.Slug); t != nil {
					installed = t.Version
				}
				out = append(out, cli.Row{"name": m.Slug, "title": m.Name, "version": m.Version, "author": m.Author, "category": m.Category,
					"style": m.Style, "tags": append([]string{}, m.Tags...), "description": m.Description, "installed": installed})
			}
			return c.Print(out, cli.View{Fields: []string{"name", "title", "version", "category", "installed"}, ID: "name"})
		},
	})
}

func (e *cliEnv) themeActivate(c *cli.Ctx, s *Server, slug string) error {
	t := s.installedTheme(slug)
	if t == nil || t.Custom {
		return s.themeNotInstalled(slug)
	}
	if s.activeTheme().Slug == t.Slug {
		c.Warning("the '%s' theme is already active.", t.Slug)
		return nil
	}
	ctx, err := e.extActionContext(c, s)
	if err != nil {
		return err
	}
	s.extKeepCache(func() { err = s.switchTheme(ctx, t.Slug) })
	if err != nil {
		return cli.Errorf("%v", err)
	}
	c.Success("Switched to '%s' theme.", t.Name)
	if t.Package != nil {
		c.Log("If the package was installed after the server started, restart the server to serve it.")
	}
	return nil
}

func (e *cliEnv) themeInstall(c *cli.Ctx) error {
	s, err := e.themeExtServer(c.Context)
	if err != nil {
		return err
	}
	defer s.extFlushTelemetry(c.Context)
	if err := s.extTemplates(); err != nil {
		return err
	}
	ctx, err := e.extActionContext(c, s)
	if err != nil {
		return err
	}
	var last string
	done, failed := 0, 0
	for _, a := range c.Args {
		var t *Theme
		var ierr error
		switch {
		case isPackagePath(a, extRegularFile):
			data, err := readPackageArg(c, a, themeZipMaxBytes)
			if err != nil {
				return err
			}
			s.extKeepCache(func() { t, ierr = s.installThemePackage(ctx, data) })
		case s.themeDeleted(a) && themeBySlug(a) != nil && themeBySlug(a).Package == nil:
			if ierr = s.restoreTheme(ctx, a); ierr == nil {
				t = s.installedTheme(a)
				c.Success("Restored the bundled '%s' theme.", a)
				last = a
				done++
				continue
			}
		default:
			if it := s.installedTheme(a); it != nil && it.Package == nil {
				c.Warning("'%s' is a bundled theme and already installed.", a)
				last = a
				done++
				continue
			}
			lt := s.libraryTheme(c.Context, a)
			if lt == nil {
				ierr = errors.New("no such theme in the theme library (see 'theme search')")
				break
			}
			s.extKeepCache(func() { t, ierr = s.installLibraryTheme(ctx, lt) })
		}
		if ierr != nil {
			c.Warning("%s: installation failed: %v", a, ierr)
			failed++
			continue
		}
		c.Success("Installed the '%s' theme (%s %s).", t.Slug, t.Name, t.Version)
		last = t.Slug
		done++
	}
	if done > 0 {
		c.Warning("running servers see new or updated theme packages after a restart")
	}
	if c.Bool("activate") && last != "" {
		if err := e.themeActivate(c, s, last); err != nil {
			return err
		}
	}
	if failed > 0 {
		return cli.Errorf("only %d of %d themes installed", done, done+failed)
	}
	return nil
}

func (e *cliEnv) themeUpdate(c *cli.Ctx) error {
	s, err := e.themeExtServer(c.Context)
	if err != nil {
		return err
	}
	defer s.extFlushTelemetry(c.Context)
	all := c.Bool("all")
	if all && len(c.Args) > 0 {
		return cli.Usagef("pass theme slugs or --all, not both")
	}
	if !all && len(c.Args) == 0 {
		return cli.Usagef("please specify one or more themes, or use --all")
	}
	if s.hubBase() == "" {
		return cli.Errorf("Pursae Manager is off (PURSAE_HUB=off); update theme packages with 'theme install <zip>'")
	}
	ups := s.hubThemeUpdates(c.Context)
	targets := c.Args
	if all {
		targets = sortedKeys(ups)
	}
	for _, slug := range targets {
		if t := s.installedTheme(slug); t == nil || t.Custom {
			return s.themeNotInstalled(slug)
		}
	}
	if c.Bool("dry-run") {
		var out []cli.Row
		for _, slug := range targets {
			if it, ok := ups[slug]; ok {
				out = append(out, cli.Row{"name": slug, "version": s.installedTheme(slug).Version, "update_version": it.Version})
			}
		}
		if len(out) == 0 && !c.Quiet && c.Format == "table" {
			c.Println("No theme updates available.")
			return nil
		}
		return c.Print(out, cli.View{Fields: []string{"name", "version", "update_version"}, ID: "name"})
	}
	if len(targets) == 0 {
		c.Success("Themes already updated.")
		return nil
	}
	if err := s.extTemplates(); err != nil {
		return err
	}
	done, failed := 0, 0
	for _, slug := range targets {
		before := s.installedTheme(slug).Version
		var t *Theme
		var uerr error
		s.extKeepCache(func() { t, uerr = s.hubUpdateTheme(c.Context, slug) })
		if uerr != nil {
			c.Warning("%s: %v", slug, uerr)
			failed++
			continue
		}
		c.Success("Updated %s %s -> %s.", slug, before, t.Version)
		done++
	}
	if done > 0 {
		c.Warning("running servers use updated theme packages after a restart")
	}
	if failed > 0 {
		return cli.Errorf("only %d of %d themes updated", done, done+failed)
	}
	return nil
}
