package server

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Golementor is a native plugin like Classic Editor and WPGo Cache: it is off on a fresh install and
// can be switched on and off on the Plugins screen. Its routes stay registered; while it is off they
// answer 404 and nothing of it reaches the admin menu, the editors, the front end or the theme list.
// The switch is the option native_golementor, read through golementorOn.

const (
	golementorSlug    = "golementor"
	golementorSetting = "native_golementor"
)

// golementorOn reports whether the Golementor plugin is active: one cached option read.
func (s *Server) golementorOn() bool {
	return s.settings.Bool(golementorSetting)
}

// golementorOnly is the middleware of Golementor's own routes: 404 while the plugin is off.
func (s *Server) golementorOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.golementorOn() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

// themeVisible reports whether a theme may be listed, previewed or activated: Hello Golementor and
// the saved Golementor themes belong to the plugin.
func (s *Server) themeVisible(t *Theme) bool {
	return t != nil && ((t.Slug != helloThemeSlug && !t.Custom) || s.golementorOn()) && !s.themeDeleted(t.Slug)
}

// installedThemes is allThemes without the themes of an inactive Golementor.
func (s *Server) installedThemes() []*Theme {
	all := allThemes()
	out := all[:0]
	for _, t := range all {
		if s.themeVisible(t) {
			out = append(out, t)
		}
	}
	return out
}

// installedTheme is themeBySlug for the themes of this site (nil for Golementor's themes while it is off).
func (s *Server) installedTheme(slug string) *Theme {
	if t := themeBySlug(slug); s.themeVisible(t) {
		return t
	}
	return nil
}

// golementorActivate switches the plugin on and does the setup the builder needs at startup: the
// saved themes join the theme list and a saved theme left active by an old install is migrated.
// The kit and the template types are created on first use, as ever; all of it is idempotent.
// The site then moves to Hello Golementor, the blank canvas the builder designs on (unless a
// Golementor theme is already active); the theme it leaves is remembered for deactivation.
func (s *Server) golementorActivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{golementorSetting: true}); err != nil {
		return err
	}
	s.golementorStart(ctx)
	_ = s.restoreTheme(ctx, helloThemeSlug) // the blank canvas comes with the plugin, even after a delete
	if t := themeBySlug(s.settings.String("stylesheet")); t == nil || (t.Slug != helloThemeSlug && !t.Custom) {
		prev := ""
		if t != nil {
			prev = t.Slug
		}
		if err := s.switchTheme(ctx, helloThemeSlug); err != nil {
			log.Printf("gowebeditor: switching to %s: %v", helloThemeSlug, err)
		} else {
			_ = s.settings.Set(ctx, map[string]any{golementorPrevTheme: prev})
		}
	}
	s.purgeAll(ctx)
	return nil
}

// golementorPrevTheme is the theme that was active before Golementor switched to Hello Golementor.
const golementorPrevTheme = "golementor_previous_theme"

// golementorStart is the startup work of an active Golementor (server start and activation).
func (s *Server) golementorStart(ctx context.Context) {
	s.initGoWebEditor(ctx)
	if err := s.gweEnsureLibraryType(ctx); err != nil {
		log.Printf("gowebeditor: template type: %v", err)
	}
	if err := s.gweEnsurePartType(ctx); err != nil {
		log.Printf("gowebeditor: theme part type: %v", err)
	}
	s.gweLoadCustomThemes(ctx)        // Golementor custom themes stored in this site's database
	s.gweMigrateActiveSavedTheme(ctx) // a saved theme active from before Hello Golementor
}

// golementorDeactivate switches the plugin off. When Hello Golementor (or a saved theme) is the
// active theme the site goes back to the default theme first; switched reports that. Saved themes,
// theme parts, the kit and built pages stay in the database.
func (s *Server) golementorDeactivate(ctx context.Context) (switched bool, err error) {
	if t := themeBySlug(s.settings.String("stylesheet")); t != nil && (t.Slug == helloThemeSlug || t.Custom) {
		back := defaultTheme // the theme Golementor's activation left, when it still exists
		if prev := themeBySlug(s.settings.String(golementorPrevTheme)); prev != nil && prev.Slug != helloThemeSlug && !prev.Custom {
			back = prev.Slug
		}
		_ = s.restoreTheme(ctx, back)
		if err = s.switchTheme(ctx, back); err != nil {
			log.Printf("gowebeditor: switching to %s: %v", back, err)
		} else {
			switched = true
		}
	}
	if err = s.settings.Set(ctx, map[string]any{golementorSetting: false}); err != nil {
		return switched, err
	}
	s.gweUnloadCustomThemes()
	s.purgeAll(ctx)
	return switched, nil
}
