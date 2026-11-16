package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// The product logo. Every screen (admin bar, login, favicons, dashboard welcome, the builder's
// loading screen and menu) points at /wp-includes/images/brand/: the defaults ship in
// web/wp-includes/images/brand (swap those files to change the logo in a release), and the Hub can
// replace them on every site by announcing brand.logo_url in /api/v1/index. The Hub's logo is
// downloaded into uploads/brand and served in place of every default size.
//
// The Hub may also announce brand.logo_dark_url: a light mark for dark backgrounds. It is served in
// place of the files in brandDarkFiles (the admin bar's mark); without it those show the Hub's
// logo_url too, as before.

const (
	brandDir      = "/images/brand/" // under /wp-includes
	brandLogoPath = "/wp-includes/images/brand/logo-256.png"
	brandMaxLogo  = 1 << 20
)

// brandDarkFiles are the shipped brand files drawn on dark backgrounds.
var brandDarkFiles = map[string]bool{brandDir + "pursae-mark-white.svg": true, brandDir + "logo-dark.svg": true}

// The names the Hub's logos are stored under in uploads/brand.
const (
	brandLight = "logo"
	brandDark  = "logo-dark"
)

// hubBrand is the optional brand block of the Hub index.
type hubBrand struct {
	LogoURL         string `json:"logo_url"`          // absolute, or relative to the Hub; empty = the shipped logo
	LogoVersion     string `json:"logo_version"`      // changes when the logo does
	LogoDarkURL     string `json:"logo_dark_url"`     // the logo for dark backgrounds; empty = logo_url
	LogoDarkVersion string `json:"logo_dark_version"` // changes when the dark logo does
}

// brandLogoTypes are the image types a Hub logo may be, with the extension it is stored under.
var brandLogoTypes = map[string]string{"image/png": ".png", "image/webp": ".webp", "image/svg+xml": ".svg"}

func (s *Server) brandOverrideDir() string { return filepath.Join(s.cfg.UploadsDir, "brand") }

// brandOverride is the Hub's light-mode logo on disk, if there is one.
func (s *Server) brandOverride() (path, ctype string) { return s.brandOverrideFile(brandLight) }

// brandOverrideFile is one of the Hub's logos on disk (brandLight or brandDark), if there is one.
func (s *Server) brandOverrideFile(name string) (path, ctype string) {
	for ct, ext := range brandLogoTypes {
		p := filepath.Join(s.brandOverrideDir(), name+ext)
		if _, err := os.Stat(p); err == nil {
			return p, ct
		}
	}
	return "", ""
}

// serveBrandOverride answers a /wp-includes/images/brand/ request with the Hub's logo: its dark-mode
// logo for the files drawn on dark backgrounds when it has one, its light-mode logo otherwise.
func (s *Server) serveBrandOverride(c *gin.Context, file string) bool {
	if !strings.HasPrefix(file, brandDir) {
		return false
	}
	var p, ct string
	if brandDarkFiles[file] {
		p, ct = s.brandOverrideFile(brandDark)
	}
	if p == "" {
		p, ct = s.brandOverride()
	}
	if p == "" {
		return false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	c.Header("Cache-Control", "public, max-age=3600")
	brandImageHeaders(c)
	c.Data(http.StatusOK, ct, b)
	return true
}

// brandImageHeaders keep a logo from the Hub (which may be an SVG, a document that can carry script)
// from ever running as a page of the site's origin: a sandbox CSP and no MIME sniffing. Every
// response that may serve it sends them (brand files, /favicon.ico).
func brandImageHeaders(c *gin.Context) {
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	c.Header("X-Content-Type-Options", "nosniff")
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

// hubSyncBrand downloads the Hub's logos when their version changed, or drops them when the Hub no
// longer announces them.
func (s *Server) hubSyncBrand(ctx context.Context, b *hubBrand) {
	if b == nil || b.LogoURL == "" {
		if s.settings.String("wpgo_brand_logo_version") != "" || s.settings.String("wpgo_brand_logo_dark_version") != "" {
			_ = os.RemoveAll(s.brandOverrideDir())
			_ = s.settings.Set(ctx, map[string]any{"wpgo_brand_logo_version": "", "wpgo_brand_logo_dark_version": ""})
		}
		return
	}
	s.hubSyncLogo(ctx, brandLight, "wpgo_brand_logo_version", b.LogoURL, b.LogoVersion)
	s.hubSyncLogo(ctx, brandDark, "wpgo_brand_logo_dark_version", b.LogoDarkURL, b.LogoDarkVersion)
}

// hubSyncLogo keeps one of the Hub's logos (name) in step with the index; versionKey is the setting
// that remembers the version on disk.
func (s *Server) hubSyncLogo(ctx context.Context, name, versionKey, logoURL, version string) {
	if logoURL == "" {
		if s.settings.String(versionKey) != "" {
			s.brandRemove(name)
			_ = s.settings.Set(ctx, map[string]any{versionKey: ""})
		}
		return
	}
	if version == "" {
		version = logoURL
	}
	if version == s.settings.String(versionKey) {
		if p, _ := s.brandOverrideFile(name); p != "" {
			return
		}
	}
	if err := s.hubFetchLogo(ctx, logoURL, name); err != nil {
		slog.Warn("hub: brand logo", "name", name, "err", err)
		return
	}
	_ = s.settings.Set(ctx, map[string]any{versionKey: version})
}

// brandRemove deletes one of the Hub's logos from disk.
func (s *Server) brandRemove(name string) {
	for _, e := range brandLogoTypes {
		_ = os.Remove(filepath.Join(s.brandOverrideDir(), name+e))
	}
}

// brandLogoClient fetches a logo hosted elsewhere than the Hub: https only and never a private or
// loopback address (checked at connect time, after DNS, on every redirect).
var brandLogoClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 10 * time.Second,
		Control: publicOnly(nil, "the logo URL points at a private network")}).DialContext, TLSHandshakeTimeout: 10 * time.Second},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" {
			return errors.New("the logo redirect is not an https URL")
		}
		return nil
	},
}

// hubFetchLogo downloads logoURL (resolved against the Hub) into uploads/brand/<name>.<ext>. A logo
// on the Hub itself is fetched like every Hub request; a logo elsewhere must be https on a public
// address.
func (s *Server) hubFetchLogo(ctx context.Context, logoURL, name string) error {
	base, err := url.Parse(s.hubBase() + "/")
	if err != nil {
		return err
	}
	u, err := base.Parse(logoURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("bad logo URL %q", logoURL)
	}
	client := s.hubClient()
	if !strings.EqualFold(u.Host, base.Host) || u.Scheme != base.Scheme {
		if u.Scheme != "https" {
			return fmt.Errorf("the logo URL %q is not https", logoURL)
		}
		client = brandLogoClient
	}
	req, err := s.hubNewRequest(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
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
	tmp := filepath.Join(dir, "."+name+ext)
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	for _, e := range brandLogoTypes {
		if e != ext {
			_ = os.Remove(filepath.Join(dir, name+e))
		}
	}
	return os.Rename(tmp, filepath.Join(dir, name+ext))
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
