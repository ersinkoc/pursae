package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The old builder's admin URLs answer with a 301 to the new screens; core screens without the old
// query parameters, other methods and the old public assets are left alone.
func TestWebBuilderRedirects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Server{}).registerWebBuilderRedirects(r)
	ok := func(c *gin.Context) { c.String(http.StatusOK, "core") }
	for _, p := range []string{"/wp-admin/post", "/wp-admin/edit", "/wp-admin/post-new", "/wp-admin/admin"} {
		r.Match([]string{http.MethodGet, http.MethodPost}, p, ok)
	}
	cases := []struct {
		method, url string
		code        int
		to          string
	}{
		{"GET", "/wp-admin/web-builder", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/web-builder?tab=popups", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/web-builder?tab=settings", 301, wbSettingsURL},
		{"GET", "/wp-admin/gowebeditor-pro?tab=settings", 301, wbSettingsURL},
		{"GET", "/wp-admin/web-builder-icons", 301, wbSettingsURL},
		{"GET", "/wp-admin/web-builder-setup", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/web-builder-setup/preview?kit=x", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/golementor-setup", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/theme-builder?action=conditions&post=4", 301, "/wp-admin/themes"},
		{"GET", "/wp-admin/gowebeditor-theme-builder", 301, "/wp-admin/themes"},
		{"HEAD", "/wp-admin/theme-builder", 301, "/wp-admin/themes"},
		{"GET", "/wp-admin/post?post=12&action=elementor", 301, "/wp-admin/post?post=12&action=web-builder"},
		{"GET", "/wp-admin/post?post=12&action=golementor", 301, "/wp-admin/post?post=12&action=web-builder"},
		{"GET", "/wp-admin/post?post=12&action=gowebeditor", 301, "/wp-admin/post?post=12&action=web-builder"},
		{"GET", "/wp-admin/post?post=abc&action=elementor", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/edit?post_type=elementor_library&tabs_group=library", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/edit?post_type=gwe_template", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/post-new?post_type=elementor_component", 301, "/wp-admin/edit?post_type=page"},
		{"GET", "/wp-admin/edit?action=elementor_new_post&post_type=page&_wpnonce=x", 301, "/wp-admin/post-new?post_type=page"},
		{"GET", "/wp-admin/edit?action=elementor_new_post&post_type=post", 301, "/wp-admin/post-new"},
		{"GET", "/wp-admin/admin?page=elementor-settings", 301, wbSettingsURL},
		{"GET", "/wp-admin/admin?page=e-form-submissions", 301, "/wp-admin/edit?post_type=page"},
		// Untouched.
		{"GET", "/wp-admin/post?post=12&action=edit", 200, ""},
		{"GET", "/wp-admin/post?post=12&action=web-builder", 200, ""},
		{"POST", "/wp-admin/post?post=12&action=elementor", 200, ""},
		{"GET", "/wp-admin/edit?post_type=page", 200, ""},
		{"GET", "/wp-admin/admin?page=wpgo-cache", 200, ""},
		{"POST", "/wp-admin/web-builder", 404, ""},
		{"GET", "/wp-content/plugins/web-builder/assets/js/editor.min.js", 404, ""},
		{"GET", "/wp-content/plugins/gowebeditor/assets/css/frontend.css", 404, ""},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.url, nil))
		if w.Code != tc.code || w.Header().Get("Location") != tc.to {
			t.Errorf("%s %s: %d %q, want %d %q", tc.method, tc.url, w.Code, w.Header().Get("Location"), tc.code, tc.to)
		}
	}
}
