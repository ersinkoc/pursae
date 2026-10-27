package server

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/config"
	"wpgo/internal/db"
)

// testServer starts the app against a freshly created database. It needs
// WPGO_TEST_ADMIN_DB (a connection to an existing database used to recreate the
// test database) and WPGO_TEST_DB (the test database URL); docker compose's test
// profile sets both. Without them the integration tests are skipped.
func testServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	adminURL, testURL := os.Getenv("WPGO_TEST_ADMIN_DB"), os.Getenv("WPGO_TEST_DB")
	if adminURL == "" || testURL == "" {
		t.Skip("WPGO_TEST_ADMIN_DB / WPGO_TEST_DB not set")
	}
	ctx := context.Background()
	u, err := url.Parse(testURL)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	admin.Close(ctx)

	pool, err := db.Connect(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{SiteURL: "http://example.test", UploadsDir: t.TempDir(), WebDir: "../../web"}
	ts := httptest.NewUnstartedServer(nil)
	cfg.SiteURL = "http://" + ts.Listener.Addr().String()
	srv, err := New(ctx, cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return ts, client
}

func get(t *testing.T, c *http.Client, u string) (int, string, string) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Location")
}

func post(t *testing.T, c *http.Client, u string, form url.Values) (int, string, string) {
	t.Helper()
	resp, err := c.PostForm(u, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Location")
}

var nonceRe = regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`)

func formNonce(t *testing.T, body string) string {
	t.Helper()
	m := nonceRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no _wpnonce in form")
	}
	return m[1]
}

func TestInstallLoginPublishAndView(t *testing.T) {
	ts, c := testServer(t)
	base := ts.URL

	if code, _, loc := get(t, c, base+"/"); code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/install") {
		t.Fatalf("uninstalled site should redirect to the installer, got %d %s", code, loc)
	}
	code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Test Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	})
	if code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install failed: %d %s", code, body)
	}

	// Fresh install defaults: Hello world!, its comment, Sample Page and the sidebar widgets.
	code, body, _ = get(t, c, base+"/")
	for _, want := range []string{"Hello world!", `class="site-title"`, "widget_search", "Recent Posts", "A WordPress Go Commenter", "Proudly powered by WordPress"} {
		if !strings.Contains(body, want) {
			t.Errorf("home page misses %q", want)
		}
	}
	if code != http.StatusOK {
		t.Fatalf("home %d", code)
	}

	// Wrong password, then the right one.
	_, body, _ = post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"nope"}})
	if !strings.Contains(body, "is incorrect") {
		t.Error("wrong password not rejected")
	}
	code, _, loc := post(t, c, base+"/wp-login", url.Values{"log": {"admin"}, "pwd": {"correct horse"}})
	if code != http.StatusFound || !strings.HasSuffix(loc, "/wp-admin/") {
		t.Fatalf("login failed: %d %s", code, loc)
	}

	for _, screen := range []string{"/wp-admin/", "/wp-admin/edit", "/wp-admin/edit?post_type=page",
		"/wp-admin/edit-tags?taxonomy=category", "/wp-admin/options-general", "/wp-admin/options-discussion",
		"/wp-admin/options-permalink", "/wp-admin/profile"} {
		if code, body, _ := get(t, c, base+screen); code != http.StatusOK || strings.Contains(body, "Template error") {
			t.Errorf("%s: %d", screen, code)
		}
	}

	// New posts open in Gotenberg, WordPress' block editor, preloaded with the REST data it asks for first.
	_, body, _ = get(t, c, base+"/wp-admin/post-new")
	if !strings.Contains(body, `id="editor" class="block-editor__container`) || !strings.Contains(body, "wp.editPost.initializeEditor") ||
		!strings.Contains(body, "createPreloadingMiddleware") {
		t.Fatalf("post-new is not the block editor")
	}

	// Switch the default editor to the built-in Classic Editor (Settings > Writing) and publish through it.
	_, body, _ = get(t, c, base+"/wp-admin/options-writing")
	defCat := regexp.MustCompile(`value="(\d+)" selected="selected"`).FindStringSubmatch(body)
	if defCat == nil {
		t.Fatal("writing settings: no default category")
	}
	if code, _, _ := post(t, c, base+"/wp-admin/options", url.Values{"option_page": {"writing"}, "_wpnonce": {formNonce(t, body)},
		"default_category": {defCat[1]}, "default_post_format": {"0"}, "classic-editor-replace": {"classic"}, "classic-editor-allow-users": {"disallow"}}); code != http.StatusFound {
		t.Fatalf("writing settings: %d", code)
	}
	_, body, _ = get(t, c, base+"/wp-admin/post-new")
	if !strings.Contains(body, "tinyMCEPreInit") || !strings.Contains(body, `id="wp-content-wrap"`) {
		t.Fatalf("classic editor not shown")
	}
	id := regexp.MustCompile(`name="post_ID" value="(\d+)"`).FindStringSubmatch(body)[1]
	code, _, loc = post(t, c, base+"/wp-admin/post", url.Values{
		"_wpnonce": {formNonce(t, body)}, "post_ID": {id}, "publish": {"Publish"}, "visibility": {"public"},
		"comment_status": {"open"}, "post_title": {"Go publishes"}, "content": {"First paragraph.\n\nSecond paragraph."},
		"tax_input[post_tag]": {"golang"},
	})
	if code != http.StatusFound || !strings.Contains(loc, "message=6") {
		t.Fatalf("publish: %d %s", code, loc)
	}
	code, body, _ = get(t, c, base+"/")
	if !strings.Contains(body, "Go publishes") || !strings.Contains(body, "<p>Second paragraph.</p>") {
		t.Errorf("published post not on the home page")
	}
	_, body, _ = get(t, c, base+"/tag/golang/")
	if !strings.Contains(body, "Tag: <span>golang</span>") {
		t.Errorf("tag archive missing title")
	}

	// A first-time anonymous commenter is held for moderation.
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	code, _, _ = post(t, anon, base+"/wp-comments-post", url.Values{
		"comment_post_ID": {id}, "comment": {"Nice one"}, "author": {"Visitor"}, "email": {"v@example.test"},
	})
	if code != http.StatusFound {
		t.Fatalf("comment post: %d", code)
	}
	var status string
	_ = getPool(t).QueryRow(context.Background(), `SELECT status FROM comments WHERE content = 'Nice one'`).Scan(&status)
	if status != "pending" {
		t.Errorf("comment status = %q, want pending", status)
	}

	// WordPress-style .php URLs still work: GET redirects to the clean path, POST is served in place.
	if code, _, loc := get(t, c, base+"/wp-admin/edit.php?post_type=page"); code != http.StatusMovedPermanently || loc != "/wp-admin/edit?post_type=page" {
		t.Errorf("legacy GET: %d %s", code, loc)
	}
	if code, _, loc := get(t, c, base+"/wp-admin/index.php"); code != http.StatusMovedPermanently || loc != "/wp-admin/" {
		t.Errorf("legacy dashboard: %d %s", code, loc)
	}
	if code, body, _ := post(t, c, base+"/wp-login.php", url.Values{"log": {"admin"}, "pwd": {"nope"}}); code != http.StatusOK || !strings.Contains(body, "is incorrect") {
		t.Errorf("legacy POST: %d", code)
	}

	// Settings require a valid nonce.
	if code, _, _ := post(t, c, base+"/wp-admin/options", url.Values{"option_page": {"general"}, "_wpnonce": {"bad"}, "blogname": {"x"}}); code != http.StatusForbidden {
		t.Errorf("bad nonce accepted: %d", code)
	}
}

func getPool(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := pgx.Connect(context.Background(), os.Getenv("WPGO_TEST_DB"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })
	return conn
}
