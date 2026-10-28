package server

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"log"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// The theme library (docs/theme-package.md, "Theme library"): complete theme package directories
// shipped in web/theme-library/<slug>/ and embedded in the binary. Appearance › Add Themes › Library
// lists them; Install zips the directory in memory and hands it to installThemePackage, so a library
// theme is validated, extracted to <data>/themes/<slug> and updated exactly like an uploaded package.

// themeCategories are the library categories, in the order of the filter links.
var themeCategories = []string{"blog", "magazine", "portfolio", "photography", "business", "agency", "restaurant", "food",
	"travel", "fashion", "technology", "education", "health", "fitness", "nonprofit", "music", "wedding", "real-estate",
	"personal", "minimal", "home", "shop", "legal", "construction", "finance", "architecture", "gaming", "entertainment",
	"podcast", "beauty", "kids", "pets", "automotive", "hotel", "startup", "science", "religion", "environment", "books",
	"sports"}

var themeCategoryLabels = map[string]string{"blog": "Blog", "magazine": "Magazine", "portfolio": "Portfolio",
	"photography": "Photography", "business": "Business", "agency": "Agency", "restaurant": "Restaurant", "food": "Food",
	"travel": "Travel", "fashion": "Fashion", "technology": "Technology", "education": "Education", "health": "Health",
	"fitness": "Fitness", "nonprofit": "Nonprofit", "music": "Music", "wedding": "Wedding", "real-estate": "Real Estate",
	"personal": "Personal", "minimal": "Minimal", "home": "Home & Interior", "shop": "Shop", "legal": "Legal",
	"construction": "Construction", "finance": "Finance", "architecture": "Architecture", "gaming": "Gaming",
	"entertainment": "Film & TV", "podcast": "Podcast", "beauty": "Beauty", "kids": "Kids", "pets": "Pets",
	"automotive": "Automotive", "hotel": "Hotel", "startup": "Startup", "science": "Science", "religion": "Faith",
	"environment": "Environment", "books": "Books", "sports": "Sports & Outdoors"}

// themeCategoryLabel is the display name of a category ("" for an unknown one).
func themeCategoryLabel(slug string) string { return themeCategoryLabels[slug] }

// libraryTheme is one directory of the library.
type libraryTheme struct {
	Manifest *themeManifest
	Dir      string // inside the library FS
}

// libraryThemes lists the valid library themes, sorted by name. A directory whose theme.json does
// not validate, whose name differs from its slug or whose slug is a bundled theme is logged and left out.
func (s *Server) libraryThemes() []libraryTheme {
	if s.themeLibrary == nil {
		return nil
	}
	entries, err := fs.ReadDir(s.themeLibrary, ".")
	if err != nil {
		return nil
	}
	var out []libraryTheme
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := fs.ReadFile(s.themeLibrary, e.Name()+"/theme.json")
		if err != nil {
			continue
		}
		m, err := parseThemeManifest(b)
		if err != nil {
			log.Printf("theme library %s skipped: %v", e.Name(), err)
			continue
		}
		if m.Slug != e.Name() {
			log.Printf("theme library %s skipped: slug %q does not match the directory name", e.Name(), m.Slug)
			continue
		}
		if t := themeBySlug(m.Slug); t != nil && t.Package == nil {
			log.Printf("theme library %s skipped: the slug belongs to a bundled theme", e.Name())
			continue
		}
		out = append(out, libraryTheme{Manifest: m, Dir: e.Name()})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Manifest.Name) < strings.ToLower(out[j].Manifest.Name)
	})
	return out
}

// libraryTheme returns the library entry of a slug.
func (s *Server) libraryTheme(slug string) *libraryTheme {
	for _, lt := range s.libraryThemes() {
		if lt.Manifest.Slug == slug {
			return &lt
		}
	}
	return nil
}

// zip packages the library directory the way tools/build-themes.sh does (files at the zip root).
func (lt *libraryTheme) zip(fsys fs.FS) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := fs.WalkDir(fsys, lt.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasPrefix(path.Base(p), ".") {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		w, err := zw.Create(strings.TrimPrefix(p, lt.Dir+"/"))
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// installLibraryTheme installs (or updates) a library theme through the upload installer.
func (s *Server) installLibraryTheme(c *gin.Context, lt *libraryTheme) (*Theme, error) {
	data, err := lt.zip(s.themeLibrary)
	if err != nil {
		return nil, err
	}
	return s.installThemePackage(c, data)
}

// serveLibraryScreenshot serves a library theme's screenshot (?action=library-screenshot&theme=slug):
// the theme is not installed, so its assets have no URL yet.
func (s *Server) serveLibraryScreenshot(c *gin.Context) {
	lt := s.libraryTheme(c.Query("theme"))
	if lt == nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	http.ServeFileFS(c.Writer, c.Request, s.themeLibrary, lt.Dir+"/"+lt.Manifest.Screenshot)
}
