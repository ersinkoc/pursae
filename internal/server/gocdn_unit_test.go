package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGoCDNBufferWriter(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		gocdnBuffer(c, func(body []byte, ct string) []byte {
			return []byte(strings.ReplaceAll(string(body), "old", "brand-new"))
		})
	})
	r.GET("/html", func(c *gin.Context) { c.Data(http.StatusTeapot, "text/html; charset=utf-8", []byte("<p>old</p>")) })
	r.GET("/json", func(c *gin.Context) {
		c.Header("Content-Length", "13")
		c.JSON(http.StatusOK, gin.H{"v": "old"})
	})
	r.GET("/bin", func(c *gin.Context) { c.Data(http.StatusOK, "image/png", []byte("old")) })
	r.GET("/empty", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/notmod", func(c *gin.Context) { c.Header("Content-Type", "text/html"); c.Status(http.StatusNotModified) })
	r.GET("/stream", func(c *gin.Context) {
		c.Header("Content-Type", "text/plain")
		c.Writer.WriteHeader(http.StatusAccepted)
		_, _ = c.Writer.WriteString("old ")
		c.Writer.Flush()
		_, _ = c.Writer.WriteString("old")
	})
	r.NoRoute(func(c *gin.Context) { c.Data(http.StatusNotFound, "text/html", []byte("old 404")) })
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/html", "<p>brand-new</p>", http.StatusTeapot},
		{"/json", `{"v":"brand-new"}`, http.StatusOK},
		{"/bin", "old", http.StatusOK},
		{"/empty", "", http.StatusNoContent},
		{"/notmod", "", http.StatusNotModified},
		{"/stream", "old old", http.StatusAccepted},
		{"/missing", "brand-new 404", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		b, _ := io.ReadAll(rec.Body)
		if rec.Code != tc.status || string(b) != tc.body {
			t.Errorf("%s: %d %q, want %d %q", tc.path, rec.Code, b, tc.status, tc.body)
		}
		if cl := rec.Header().Get("Content-Length"); cl != "" && cl != itoa64(int64(len(b))) {
			t.Errorf("%s: Content-Length %s for %d bytes", tc.path, cl, len(b))
		}
	}
}

func TestGoCDNUploadsMatcher(t *testing.T) {
	body := []byte(`<img src="/wp-content/uploads/2026/10/a.jpg" srcset="http://site.test/wp-content/uploads/2026/10/a-300x200.jpg 300w, //site.test/wp-content/uploads/2026/10/a-1024x683.jpg 1024w">` +
		`<a href="https://evil.test/wp-content/uploads/2026/10/a.jpg">x</a> style="background:url(/wp-content/uploads/b.png)" foo/wp-content/uploads/c.jpg ` +
		`{"source_url":"http://site.test/wp-content/uploads/2026/10/a%20b.jpg?ver=2"}`)
	var got []string
	for _, m := range gocdnUploadsURL.FindAllSubmatchIndex(body, -1) {
		host := ""
		if m[2] >= 0 {
			host = string(body[m[2]:m[3]])
		}
		got = append(got, map[bool]string{true: "+", false: "-"}[gocdnBoundary(body, m[0])]+host+"|"+string(body[m[4]:m[5]]))
	}
	want := []string{"+|2026/10/a.jpg", "+site.test|2026/10/a-300x200.jpg", "+site.test|2026/10/a-1024x683.jpg", "+evil.test|2026/10/a.jpg",
		"+|b.png", "-|c.jpg", "+site.test|2026/10/a%20b.jpg"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("matches\n got %v\nwant %v", got, want)
	}
	for _, c := range []struct {
		path string
		ok   bool
	}{{"/", true}, {"/hello-world/", true}, {"/wp-admin/upload", false}, {"/wp-login", false}, {"/wp-includes/x.js", false},
		{"/wp-json/wp/v2/media/1", true}, {"/wp-json/wp/v2/media/1?context=edit", false}, {"/cdn-img/a.jpg", false}} {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		if gocdnRewritable(req) != c.ok {
			t.Errorf("gocdnRewritable(%s) = %v", c.path, !c.ok)
		}
	}
	if gocdnRewritable(httptest.NewRequest(http.MethodPost, "/wp-json/wp/v2/posts", nil)) {
		t.Error("a REST write is rewritten")
	}
}
