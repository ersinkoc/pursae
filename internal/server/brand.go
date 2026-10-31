package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// The product logo. Every screen (admin bar, login, favicons, dashboard welcome, the builder's
// loading screen and menu) points at /wp-includes/images/brand/: the defaults ship in
// web/wp-includes/images/brand (swap those files to change the logo in a release), and the Hub can
// replace them on every site by announcing brand.logo_url in /api/v1/index. The Hub's logo is
// downloaded into uploads/brand and served in place of every default size.

const (
	brandDir      = "/images/brand/" // under /wp-includes
	brandLogoPath = "/wp-includes/images/brand/logo-256.png"
	brandMaxLogo  = 1 << 20
)

// hubBrand is the optional brand block of the Hub index.
type hubBrand struct {
	LogoURL     string `json:"logo_url"`     // absolute, or relative to the Hub; empty = the shipped logo
	LogoVersion string `json:"logo_version"` // changes when the logo does
}

// brandLogoTypes are the image types a Hub logo may be, with the extension it is stored under.
var brandLogoTypes = map[string]string{"image/png": ".png", "image/webp": ".webp", "image/svg+xml": ".svg"}

func (s *Server) brandOverrideDir() string { return filepath.Join(s.cfg.UploadsDir, "brand") }

// brandOverride is the Hub's logo on disk, if there is one.
func (s *Server) brandOverride() (path, ctype string) {
	for ct, ext := range brandLogoTypes {
		p := filepath.Join(s.brandOverrideDir(), "logo"+ext)
		if _, err := os.Stat(p); err == nil {
			return p, ct
		}
	}
	return "", ""
}

// serveBrandOverride answers a /wp-includes/images/brand/ request with the Hub's logo.
func (s *Server) serveBrandOverride(c *gin.Context, file string) bool {
	if !strings.HasPrefix(file, brandDir) {
		return false
	}
	p, ct := s.brandOverride()
	if p == "" {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	c.Data(http.StatusOK, ct, b)
	return true
}

// brandFavicon is the logo for /favicon.ico.
func (s *Server) brandFavicon() ([]byte, string, error) {
	if p, ct := s.brandOverride(); p != "" {
		b, err := os.ReadFile(p)
		return b, ct, err
	}
	b, err := fs.ReadFile(s.web, "wp-includes/images/brand/logo-32.png")
	return b, "image/png", err
}

// hubSyncBrand downloads the Hub's logo when its version changed, or drops it when the Hub no longer
// announces one.
func (s *Server) hubSyncBrand(ctx context.Context, b *hubBrand) {
	if b == nil || b.LogoURL == "" {
		if s.settings.String("wpgo_brand_logo_version") != "" {
			_ = os.RemoveAll(s.brandOverrideDir())
			_ = s.settings.Set(ctx, map[string]any{"wpgo_brand_logo_version": ""})
		}
		return
	}
	version := b.LogoVersion
	if version == "" {
		version = b.LogoURL
	}
	if version == s.settings.String("wpgo_brand_logo_version") {
		if p, _ := s.brandOverride(); p != "" {
			return
		}
	}
	if err := s.hubFetchLogo(ctx, b.LogoURL); err != nil {
		slog.Warn("hub: brand logo", "err", err)
		return
	}
	_ = s.settings.Set(ctx, map[string]any{"wpgo_brand_logo_version": version})
}

// hubFetchLogo downloads logoURL (resolved against the Hub) into uploads/brand.
func (s *Server) hubFetchLogo(ctx context.Context, logoURL string) error {
	base, err := url.Parse(s.hubBase() + "/")
	if err != nil {
		return err
	}
	u, err := base.Parse(logoURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("bad logo URL %q", logoURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", hubUserAgent)
	resp, err := s.hubClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, brandMaxLogo+1))
	if err != nil {
		return err
	}
	if len(body) > brandMaxLogo {
		return fmt.Errorf("%s: larger than %d bytes", u, brandMaxLogo)
	}
	ct := brandSniff(body)
	ext, ok := brandLogoTypes[ct]
	if !ok {
		return fmt.Errorf("%s: not a PNG, WebP or SVG image", u)
	}
	dir := s.brandOverrideDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".logo"+ext)
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	for _, e := range brandLogoTypes {
		if e != ext {
			_ = os.Remove(filepath.Join(dir, "logo"+e))
		}
	}
	return os.Rename(tmp, filepath.Join(dir, "logo"+ext))
}

// brandSniff tells PNG, WebP and SVG apart (http.DetectContentType calls SVG text/xml).
func brandSniff(b []byte) string {
	if ct := http.DetectContentType(b); ct == "image/png" || ct == "image/webp" {
		return ct
	}
	head := bytes.ToLower(b[:min(len(b), 1024)])
	if bytes.Contains(head, []byte("<svg")) {
		return "image/svg+xml"
	}
	return ""
}
