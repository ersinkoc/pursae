package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// Appearance › Themes › Delete, for every inactive theme, as in WordPress (delete_theme()):
//   - an uploaded package: its directory under <data>/themes goes;
//   - a theme bundled with WordPress Go: its edited files (the Theme File Editor's copies under
//     <data>/themes/<slug>) go and the theme leaves the site's theme list. The originals are part of
//     the binary, so Appearance › Add Themes › Bundled lists it again for reinstalling, the way
//     WordPress.org's directory offers a deleted default theme.
// Theme mods stay in the database, as WordPress keeps them.

const deletedThemesSetting = "wpgo_deleted_themes"

func (s *Server) deletedThemes() []string {
	raw, _ := s.settings.Get(deletedThemesSetting).([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if slug, ok := v.(string); ok {
			out = append(out, slug)
		}
	}
	return out
}

func (s *Server) themeDeleted(slug string) bool { return slices.Contains(s.deletedThemes(), slug) }

// deleteTheme removes an inactive theme from the site.
func (s *Server) deleteTheme(ctx context.Context, slug string) (err error) {
	t := s.installedTheme(slug)
	if t == nil {
		return errors.New("The requested theme does not exist.")
	}
	defer func() {
		if err == nil {
			s.hubEvent("theme_delete", "theme", slug, t.Version) // statistics (hub_telemetry.go)
		}
	}()
	if s.activeTheme().Slug == slug {
		return errors.New("You cannot delete a theme while it is active on the site.")
	}
	if t.Package != nil {
		return s.deleteThemePackage(ctx, slug)
	}
	themeSaveMu.Lock()
	err = os.RemoveAll(filepath.Join(themeOverrideDir(s.cfg.UploadsDir), slug))
	themeSaveMu.Unlock()
	if err != nil {
		return err
	}
	list := append(s.deletedThemes(), slug)
	if err := s.settings.Set(ctx, map[string]any{deletedThemesSetting: list}); err != nil {
		return err
	}
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM themes WHERE slug = $1`, slug)
	s.purgeAll(ctx)
	return nil
}

// restoreTheme puts a deleted bundled theme back (Add Themes › Install). A no-op for others.
func (s *Server) restoreTheme(ctx context.Context, slug string) error {
	list := s.deletedThemes()
	i := slices.Index(list, slug)
	if i < 0 {
		return nil
	}
	if err := s.settings.Set(ctx, map[string]any{deletedThemesSetting: slices.Delete(list, i, i+1)}); err != nil {
		return err
	}
	s.hubEvent("theme_install", "theme", slug, s.themeVersion(slug)) // statistics (hub_telemetry.go)
	return nil
}
