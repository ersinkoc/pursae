// Command upgradetest is the helper of tools/upgrade-test.sh, which proves that a site created by an
// older release keeps its data when the current code takes it over. It talks to the running server
// over HTTP only (the installer, /wp-login, admin-ajax and the REST API), so it seeds a site exactly
// the way the old release supports, whatever its schema looked like:
//
//	upgradetest resetdb  -admin DSN -db NAME [-drop]    drop and create (or only drop) a database
//	upgradetest counts   -dsn DSN -out FILE             row count of every table
//	upgradetest counts   -dsn DSN -compare FILE         fail when a table vanished or lost rows
//	upgradetest seed     -url URL -out FILE [-token T] [-wb-fixture PREFIX]
//	upgradetest verify   -url URL -snapshot FILE
//
// seed installs the site, creates users, terms, media, posts, pages, a custom field, comments, a
// menu, settings and (with -wb-fixture, a doc/render fixture pair of that release's own
// internal/webbuilder/testdata/render) a Pursae Web Builder page, then writes a snapshot: for every
// object the REST URL it lives at and the values of the fields that must survive. verify logs every
// seeded user in with the old password, reads every snapshot URL again with the Application Password
// created by the old release, and compares field by field; it also fetches the uploaded file, the
// front page and each permalink.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "resetdb":
		err = cmdResetDB(os.Args[2:])
	case "counts":
		err = cmdCounts(os.Args[2:])
	case "seed":
		err = cmdSeed(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgradetest:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: upgradetest resetdb|counts|seed|verify [flags]")
	os.Exit(2)
}

// --- database ----------------------------------------------------------------------------------

func cmdResetDB(args []string) error {
	fs := flag.NewFlagSet("resetdb", flag.ExitOnError)
	admin := fs.String("admin", "", "DSN of a database to connect to (not the one reset)")
	name := fs.String("db", "", "database to drop and create")
	dropOnly := fs.Bool("drop", false, "only drop it")
	_ = fs.Parse(args) // ExitOnError: Parse exits on a bad flag
	if *admin == "" || !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(*name) {
		return errors.New("resetdb needs -admin and a -db of [a-z0-9_]")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *admin)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+*name+` WITH (FORCE)`); err != nil {
		return err
	}
	if *dropOnly {
		return nil
	}
	_, err = conn.Exec(ctx, `CREATE DATABASE `+*name)
	return err
}

func tableCounts(ctx context.Context, dsn string) (map[string]int64, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, n := range names {
		var c int64
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{n}.Sanitize()).Scan(&c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, nil
}

// countsIgnore are tables whose rows may legitimately go away in a migration (none so far).
var countsIgnore = map[string]bool{}

func cmdCounts(args []string) error {
	fs := flag.NewFlagSet("counts", flag.ExitOnError)
	dsn := fs.String("dsn", "", "database")
	out := fs.String("out", "", "write the counts here")
	compare := fs.String("compare", "", "compare with the counts in this file")
	_ = fs.Parse(args) // ExitOnError: Parse exits on a bad flag
	got, err := tableCounts(context.Background(), *dsn)
	if err != nil {
		return err
	}
	if *out != "" {
		b, _ := json.MarshalIndent(got, "", "  ")
		return os.WriteFile(*out, b, 0o644)
	}
	b, err := os.ReadFile(*compare)
	if err != nil {
		return err
	}
	var before map[string]int64
	if err := json.Unmarshal(b, &before); err != nil {
		return err
	}
	var bad []string
	rows := 0
	for _, t := range sortedKeys(before) {
		n, ok := got[t]
		switch {
		case countsIgnore[t]:
		case !ok:
			bad = append(bad, fmt.Sprintf("table %s (%d rows) is gone", t, before[t]))
		case n < before[t]:
			bad = append(bad, fmt.Sprintf("table %s: %d rows before the upgrade, %d after", t, before[t], n))
		}
		rows += int(before[t])
	}
	if len(bad) > 0 {
		return errors.New("rows lost in migration:\n  " + strings.Join(bad, "\n  "))
	}
	fmt.Printf("counts: %d tables, %d rows before the upgrade, none lost\n", len(before), rows)
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- HTTP client --------------------------------------------------------------------------------

type client struct {
	base  string
	hc    *http.Client
	nonce string // X-WP-Nonce for cookie-authenticated REST calls
	basic [2]string
}

func newClient(base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{base: strings.TrimRight(base, "/"), hc: &http.Client{
		Jar: jar, Timeout: 60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *client) do(method, path string, body io.Reader, hdr map[string]string) (int, http.Header, []byte, error) {
	u := path
	if !strings.HasPrefix(u, "http") {
		u = c.base + path
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if c.basic[0] != "" {
		req.SetBasicAuth(c.basic[0], c.basic[1])
	} else if c.nonce != "" && strings.Contains(u, "/wp-json/") {
		req.Header.Set("X-WP-Nonce", c.nonce)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b, err
}

func (c *client) form(path string, v url.Values) (int, http.Header, []byte, error) {
	return c.do(http.MethodPost, path, strings.NewReader(v.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
}

// rest calls /wp-json<route> with a JSON body and decodes a JSON answer, failing unless the status is want.
func (c *client) rest(method, route string, body any, want int, hdr map[string]string) (map[string]any, error) {
	var rd io.Reader
	h := map[string]string{}
	for k, v := range hdr {
		h[k] = v
	}
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
		h["Content-Type"] = "application/json"
	}
	code, _, b, err := c.do(method, "/wp-json"+route, rd, h)
	if err != nil {
		return nil, err
	}
	if code != want {
		return nil, fmt.Errorf("%s %s: status %d (want %d): %s", method, route, code, want, clip(b))
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s %s: %w: %s", method, route, err, clip(b))
	}
	return out, nil
}

func clip(b []byte) string {
	s := string(b)
	if len(s) > 600 {
		s = s[:600] + "…"
	}
	return s
}

// login posts the login form and expects the redirect to wp-admin that a good password earns.
func (c *client) login(user, pass string) error {
	_, _, _, _ = c.do(http.MethodGet, "/wp-login", nil, nil) // the test cookie, like a browser
	code, h, b, err := c.form("/wp-login", url.Values{"log": {user}, "pwd": {pass}, "rememberme": {"forever"}, "redirect_to": {"/wp-admin/"}, "testcookie": {"1"}})
	if err != nil {
		return err
	}
	if code != http.StatusFound || !strings.Contains(h.Get("Location"), "/wp-admin") {
		return fmt.Errorf("login %s: status %d, location %q: %s", user, code, h.Get("Location"), clip(b))
	}
	code, _, b, err = c.do(http.MethodGet, "/wp-admin/admin-ajax?action=rest-nonce", nil, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK || len(bytes.TrimSpace(b)) == 0 || len(b) > 64 {
		return fmt.Errorf("rest nonce for %s: status %d: %s", user, code, clip(b))
	}
	c.nonce = strings.TrimSpace(string(b))
	return nil
}

// --- snapshot -----------------------------------------------------------------------------------

// entry is one seeded object: where to read it and the values that must survive the upgrade.
type entry struct {
	Name   string         `json:"name"`
	Path   string         `json:"path"`             // REST route, read with ?context=edit added
	Fields map[string]any `json:"fields"`           // dotted path -> value
	Page   string         `json:"page,omitempty"`   // a public URL that must answer 200 and contain Expect
	Expect string         `json:"expect,omitempty"` // text expected on Page
	Cookie bool           `json:"cookie,omitempty"` // read with the administrator's login, not the Application Password
}

type snapshot struct {
	Version     string            `json:"version"` // the release that seeded the site
	Users       map[string]string `json:"users"`   // login -> password
	AppUser     string            `json:"appUser"`
	AppPassword string            `json:"appPassword"`
	Entries     []entry           `json:"entries"`
	Media       map[string]string `json:"media"` // file URL -> sha256
	CustomField [3]string         `json:"customField"`
	// CustomFieldPost is the post the custom field (key, value) belongs to.
	CustomFieldPost int64 `json:"customFieldPost"`
}

// lookup follows a dotted path ("title.raw", "meta.footnotes") into a decoded JSON object.
func lookup(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, p := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func withContext(route string) string {
	if strings.Contains(route, "?") {
		return route + "&context=edit"
	}
	return route + "?context=edit"
}

// record reads route back and keeps the fields this release reports (a field an older release does
// not have is simply not compared).
func (c *client) record(s *snapshot, name, route string, fields []string, page, expect string) (map[string]any, error) {
	obj, err := c.rest(http.MethodGet, withContext(route), nil, http.StatusOK, nil)
	if err != nil {
		return nil, err
	}
	e := entry{Name: name, Path: route, Fields: map[string]any{}, Page: page, Expect: expect}
	for _, f := range fields {
		if v, ok := lookup(obj, f); ok {
			e.Fields[f] = v
		}
	}
	if len(e.Fields) == 0 {
		return nil, fmt.Errorf("%s: none of %v in %s", name, fields, clip(mustJSON(obj)))
	}
	s.Entries = append(s.Entries, e)
	return obj, nil
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func id(obj map[string]any) int64 {
	f, _ := obj["id"].(float64)
	return int64(f)
}

var (
	postFields = []string{"id", "date_gmt", "slug", "status", "type", "link", "title.raw", "content.raw", "excerpt.raw",
		"author", "featured_media", "comment_status", "ping_status", "sticky", "format", "categories", "tags", "parent",
		"menu_order", "template", "meta.footnotes"}
	termFields    = []string{"id", "name", "slug", "description", "parent", "taxonomy", "link"}
	userFields    = []string{"id", "username", "name", "first_name", "last_name", "email", "url", "description", "slug", "roles", "nickname", "locale"}
	mediaFields   = []string{"id", "date_gmt", "slug", "status", "title.raw", "author", "alt_text", "caption.raw", "description.raw", "media_type", "mime_type", "source_url", "post", "media_details.width", "media_details.height", "media_details.file"}
	commentFields = []string{"id", "post", "parent", "author", "author_name", "author_email", "author_url", "date_gmt", "content.raw", "status", "type"}
	menuFields    = []string{"id", "name", "slug", "description", "locations", "auto_add"}
	itemFields    = []string{"id", "title.raw", "status", "url", "attr_title", "description", "type", "type_label", "object", "object_id", "parent", "menu_order", "target", "classes", "xfn", "menus"}
)

// --- seed ---------------------------------------------------------------------------------------

const (
	adminUser, adminPass   = "upgadmin", "Upgrade-Admin-Pass-1!"
	editorUser, editorPass = "upgeditor", "Upgrade-Editor-Pass-2!"
)

func cmdSeed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	base := fs.String("url", "", "site URL")
	out := fs.String("out", "", "snapshot file to write")
	token := fs.String("token", "", "setup token (releases with a setup token)")
	version := fs.String("version", "", "release being seeded (for the report)")
	wbFixture := fs.String("wb-fixture", "", "Web Builder fixture prefix (<prefix>.doc.json and <prefix>.render.json); empty skips the builder page")
	_ = fs.Parse(args) // ExitOnError: Parse exits on a bad flag
	s := &snapshot{Version: *version, Users: map[string]string{}, Media: map[string]string{}}
	c := newClient(*base)
	if err := install(c, *token); err != nil {
		return err
	}
	if err := c.login(adminUser, adminPass); err != nil {
		return err
	}
	if err := seed(c, s, *wbFixture); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("seed: %d objects recorded\n", len(s.Entries))
	return nil
}

func install(c *client, token string) error {
	q := "?language=en_US"
	if token != "" {
		q += "&token=" + url.QueryEscape(token)
	}
	code, h, b, err := c.do(http.MethodGet, "/wp-admin/install"+q, nil, nil)
	if err != nil {
		return err
	}
	if code == http.StatusSeeOther || code == http.StatusFound { // a release with a setup token: it moves into a cookie
		if code, _, b, err = c.do(http.MethodGet, h.Get("Location"), nil, nil); err != nil {
			return err
		}
	}
	if code != http.StatusOK {
		return fmt.Errorf("install form: status %d: %s", code, clip(b))
	}
	v := url.Values{
		"weblog_title": {"Upgrade Test Site"}, "user_name": {adminUser}, "admin_password": {adminPass},
		"admin_password2": {adminPass}, "pw_weak": {"on"}, "admin_email": {"admin@upgrade.example.com"},
		"blog_public": {"1"}, "language": {"en_US"},
	}
	if token != "" {
		v.Set("token", token)
	}
	path := "/wp-admin/install"
	if token != "" {
		path += "?token=" + url.QueryEscape(token)
	}
	code, _, b, err = c.form(path, v)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusFound {
		return fmt.Errorf("install: status %d: %s", code, clip(b))
	}
	// Installed: the installer now sends everyone to the login screen.
	code, h, _, err = c.do(http.MethodGet, "/wp-admin/install", nil, nil)
	if err != nil {
		return err
	}
	if code != http.StatusFound || !strings.Contains(h.Get("Location"), "wp-login") {
		return fmt.Errorf("site not installed after the install form (status %d): %s", code, clip(b))
	}
	return nil
}

func testPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 48, 32))
	for x := 0; x < 48; x++ {
		for y := 0; y < 32; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 5), uint8(y * 7), 160, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err) // in-memory encode of a fixed image
	}
	return buf.Bytes()
}

func seed(c *client, s *snapshot, wbFixture string) error {
	s.Users[adminUser] = adminPass
	s.Users[editorUser] = editorPass

	// Options: through the settings endpoint, as the Settings screens do.
	settings := map[string]any{"title": "Upgrade Test Site", "description": "Data that must survive upgrades",
		"posts_per_page": 7, "date_format": "Y-m-d", "time_format": "H:i", "default_comment_status": "open", "start_of_week": 1}
	if _, err := c.rest(http.MethodPost, "/wp/v2/settings", settings, http.StatusOK, nil); err != nil {
		return err
	}
	sf := make([]string, 0, len(settings))
	for k := range settings {
		sf = append(sf, k)
	}
	sort.Strings(sf)
	if _, err := c.record(s, "settings", "/wp/v2/settings", sf, "", ""); err != nil {
		return err
	}

	// Users.
	if _, err := c.record(s, "user "+adminUser, "/wp/v2/users/1", userFields, "", ""); err != nil {
		return err
	}
	ed, err := c.rest(http.MethodPost, "/wp/v2/users", map[string]any{"username": editorUser, "email": "editor@upgrade.example.com",
		"password": editorPass, "roles": []string{"editor"}, "first_name": "Edith", "last_name": "Upgrade",
		"description": "Second user, seeded by the old release.", "url": "https://editor.example.com"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	edID := id(ed)
	if _, err := c.record(s, "user "+editorUser, fmt.Sprintf("/wp/v2/users/%d", edID), userFields, "", ""); err != nil {
		return err
	}

	// Application Password: verify reads everything back with it, so it needs every scope. Passwords
	// without scopes had full access until scopes defaulted to content and media (migration 0098
	// turned the old empty lists into full_access); the REST API takes full_access in some releases
	// only, so the first form the release accepts wins.
	var ap map[string]any
	for _, scopes := range [][]string{{"full_access"}, {"content", "media", "themes", "plugins", "cache", "seo", "settings", "commerce", "users"}, nil} {
		body := map[string]any{"name": "upgrade-test"}
		if scopes != nil {
			body["scopes"] = scopes
		}
		if ap, err = c.rest(http.MethodPost, "/wp/v2/users/me/application-passwords", body, http.StatusCreated, nil); err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	s.AppUser = adminUser
	s.AppPassword, _ = ap["password"].(string)
	// Its scopes are not compared: 0098 rewrites the empty list of old releases (full access) as full_access.
	if _, err := c.record(s, "application password", fmt.Sprintf("/wp/v2/users/me/application-passwords/%s", ap["uuid"]), []string{"uuid", "name"}, "", ""); err != nil {
		return err
	}
	s.Entries[len(s.Entries)-1].Cookie = true // a scoped password may not read the passwords routes
	if s.AppPassword == "" {
		return fmt.Errorf("application password: no password in %s", clip(mustJSON(ap)))
	}

	// Terms.
	news, err := c.rest(http.MethodPost, "/wp/v2/categories", map[string]any{"name": "Upgrade News", "description": "Parent category"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	local, err := c.rest(http.MethodPost, "/wp/v2/categories", map[string]any{"name": "Upgrade Local", "slug": "upgrade-local", "parent": id(news)}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	tagA, err := c.rest(http.MethodPost, "/wp/v2/tags", map[string]any{"name": "Durable", "description": "A tag"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	tagB, err := c.rest(http.MethodPost, "/wp/v2/tags", map[string]any{"name": "Çağrı ışık", "slug": "cagri-isik"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}

	// Media: an uploaded PNG with its texts.
	pngBytes := testPNG()
	media, err := func() (map[string]any, error) {
		code, _, b, err := c.do(http.MethodPost, "/wp-json/wp/v2/media", bytes.NewReader(pngBytes), map[string]string{
			"Content-Type": "image/png", "Content-Disposition": `attachment; filename="upgrade-test.png"`})
		if err != nil {
			return nil, err
		}
		if code != http.StatusCreated {
			return nil, fmt.Errorf("media upload: status %d: %s", code, clip(b))
		}
		var m map[string]any
		return m, json.Unmarshal(b, &m)
	}()
	if err != nil {
		return err
	}
	mediaID := id(media)
	if _, err := c.rest(http.MethodPost, fmt.Sprintf("/wp/v2/media/%d", mediaID), map[string]any{"title": "Upgrade image",
		"alt_text": "Alt text that must survive", "caption": "A caption", "description": "A description"}, http.StatusOK, nil); err != nil {
		return err
	}
	mo, err := c.record(s, "media", fmt.Sprintf("/wp/v2/media/%d", mediaID), mediaFields, "", "")
	if err != nil {
		return err
	}
	src, _ := mo["source_url"].(string)
	sum := sha256.Sum256(pngBytes)
	s.Media[src] = hex.EncodeToString(sum[:])

	// Posts.
	footnotes := `[{"id":"fn-upgrade-1","content":"A footnote that must survive."}]`
	p1, err := c.rest(http.MethodPost, "/wp/v2/posts", map[string]any{"title": "Upgrade post one", "slug": "upgrade-post-one", "status": "publish",
		"content": "<!-- wp:paragraph -->\n<p>First post body with <strong>markup</strong> and ünïcödé.</p>\n<!-- /wp:paragraph -->",
		"excerpt": "First excerpt", "categories": []int64{id(news), id(local)}, "tags": []int64{id(tagA), id(tagB)},
		"featured_media": mediaID, "comment_status": "open", "meta": map[string]any{"footnotes": footnotes}}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	p2, err := c.rest(http.MethodPost, "/wp/v2/posts", map[string]any{"title": "Upgrade post two by the editor", "status": "publish",
		"content": "<p>Second post.</p>", "author": edID, "categories": []int64{id(local)}, "tags": []int64{id(tagA)}, "sticky": true}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	draft, err := c.rest(http.MethodPost, "/wp/v2/posts", map[string]any{"title": "Upgrade draft", "status": "draft", "content": "<p>Not published.</p>"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}

	// A custom field, added the way the classic editor's Custom Fields box does (admin-ajax add-meta).
	s.CustomField = [3]string{"upgrade_custom_key", "custom value that must survive", ""}
	s.CustomFieldPost = id(p1)
	if err := c.useClassicEditor(s); err != nil {
		return err
	}
	if err := c.addCustomField(id(p1), s.CustomField[0], s.CustomField[1]); err != nil {
		return err
	}

	// Pages.
	about, err := c.rest(http.MethodPost, "/wp/v2/pages", map[string]any{"title": "Upgrade About", "slug": "upgrade-about", "status": "publish",
		"content": "<p>About page.</p>", "menu_order": 3}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	team, err := c.rest(http.MethodPost, "/wp/v2/pages", map[string]any{"title": "Upgrade Team", "slug": "upgrade-team", "status": "publish",
		"content": "<p>Child page.</p>", "parent": id(about)}, http.StatusCreated, nil)
	if err != nil {
		return err
	}

	// Comments: one approved with a reply, one held for moderation.
	cm, err := c.rest(http.MethodPost, "/wp/v2/comments", map[string]any{"post": id(p1), "content": "A comment that must survive.",
		"author_name": "Visitor", "author_email": "visitor@upgrade.example.com", "status": "approved"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	reply, err := c.rest(http.MethodPost, "/wp/v2/comments", map[string]any{"post": id(p1), "parent": id(cm), "content": "A reply by the admin."}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	held, err := c.rest(http.MethodPost, "/wp/v2/comments", map[string]any{"post": id(p2), "content": "A held comment.",
		"author_name": "Someone", "author_email": "someone@upgrade.example.com", "status": "hold"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}

	// Menu with a custom link, a page and a post, in a theme location when the theme has one.
	menuBody := map[string]any{"name": "Upgrade Menu", "description": "Primary navigation"}
	if locs, err := c.rest(http.MethodGet, "/wp/v2/menu-locations", nil, http.StatusOK, nil); err == nil && len(locs) > 0 {
		menuBody["locations"] = []string{sortedKeys(locs)[0]}
	}
	menu, err := c.rest(http.MethodPost, "/wp/v2/menus", menuBody, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	var items []map[string]any
	for _, it := range []map[string]any{
		{"title": "Home link", "url": "https://upgrade.example.com/", "type": "custom", "menu_order": 1},
		{"title": "About", "type": "post_type", "object": "page", "object_id": id(about), "menu_order": 2},
		{"type": "post_type", "object": "post", "object_id": id(p1), "menu_order": 3, "target": "_blank"},
	} {
		it["menus"] = id(menu)
		it["status"] = "publish"
		o, err := c.rest(http.MethodPost, "/wp/v2/menu-items", it, http.StatusCreated, nil)
		if err != nil {
			return err
		}
		items = append(items, o)
	}
	child, err := c.rest(http.MethodPost, "/wp/v2/menu-items", map[string]any{"title": "Team", "type": "post_type", "object": "page",
		"object_id": id(team), "parent": id(items[1]), "menu_order": 4, "menus": id(menu), "status": "publish"}, http.StatusCreated, nil)
	if err != nil {
		return err
	}
	items = append(items, child)

	// Pursae Web Builder: a page published from the editor, with that release's own fixture.
	var wbPage int64
	if wbFixture != "" {
		if wbPage, err = c.seedWebBuilder(s, wbFixture); err != nil {
			return err
		}
	}

	// Record everything (after all writes, so term counts and links are final).
	type rec struct {
		name, route string
		fields      []string
		public      bool
		expect      string
	}
	recs := []rec{
		{"category news", fmt.Sprintf("/wp/v2/categories/%d", id(news)), termFields, true, ""},
		{"category local", fmt.Sprintf("/wp/v2/categories/%d", id(local)), termFields, true, ""},
		{"tag durable", fmt.Sprintf("/wp/v2/tags/%d", id(tagA)), termFields, true, ""},
		{"tag unicode", fmt.Sprintf("/wp/v2/tags/%d", id(tagB)), termFields, false, ""},
		{"post one", fmt.Sprintf("/wp/v2/posts/%d", id(p1)), postFields, true, "Upgrade post one"},
		{"post two", fmt.Sprintf("/wp/v2/posts/%d", id(p2)), postFields, true, "Upgrade post two by the editor"},
		{"draft", fmt.Sprintf("/wp/v2/posts/%d", id(draft)), postFields, false, ""},
		{"page about", fmt.Sprintf("/wp/v2/pages/%d", id(about)), postFields, true, "Upgrade About"},
		{"page team", fmt.Sprintf("/wp/v2/pages/%d", id(team)), postFields, true, "Upgrade Team"},
		{"comment", fmt.Sprintf("/wp/v2/comments/%d", id(cm)), commentFields, false, ""},
		{"reply", fmt.Sprintf("/wp/v2/comments/%d", id(reply)), commentFields, false, ""},
		{"held comment", fmt.Sprintf("/wp/v2/comments/%d", id(held)), commentFields, false, ""},
		{"menu", fmt.Sprintf("/wp/v2/menus/%d", id(menu)), menuFields, false, ""},
	}
	for i, it := range items {
		recs = append(recs, rec{fmt.Sprintf("menu item %d", i+1), fmt.Sprintf("/wp/v2/menu-items/%d", id(it)), itemFields, false, ""})
	}
	if wbPage != 0 {
		recs = append(recs, rec{"web builder page", fmt.Sprintf("/wp/v2/pages/%d", wbPage), postFields, true, ""})
	}
	for _, r := range recs {
		o, err := c.record(s, r.name, r.route, r.fields, "", "")
		if err != nil {
			return err
		}
		if r.public {
			e := &s.Entries[len(s.Entries)-1]
			e.Page, _ = o["link"].(string)
			e.Expect = r.expect
			if e.Page == "" {
				return fmt.Errorf("%s: no link", r.name)
			}
			if code, _, b, err := c.do(http.MethodGet, e.Page, nil, nil); err != nil || code != http.StatusOK || !bytes.Contains(b, []byte(e.Expect)) {
				return fmt.Errorf("%s: %s answers %d (err %w) on the old release", r.name, e.Page, code, err)
			}
		}
	}
	return nil
}

var wpnonceRe = regexp.MustCompile(`name="_wpnonce" value="([0-9a-f]+)"`)

// useClassicEditor activates the Classic Editor plugin and makes it the default editor on Settings ›
// Writing, so posts open on the screen with the Custom Fields box.
func (c *client) useClassicEditor(s *snapshot) error {
	if _, err := c.rest(http.MethodPost, "/pursae/v1/plugins/classic-editor/activate", nil, http.StatusOK, nil); err != nil {
		return err
	}
	if _, err := c.record(s, "plugin classic-editor", "/pursae/v1/plugins/classic-editor", []string{"active", "slug"}, "", ""); err != nil {
		return err
	}
	code, _, b, err := c.do(http.MethodGet, "/wp-admin/options-writing", nil, nil)
	if err != nil {
		return err
	}
	m := wpnonceRe.FindSubmatch(b)
	if code != http.StatusOK || m == nil {
		return fmt.Errorf("options-writing: status %d, nonce found %v", code, m != nil)
	}
	code, h, b, err := c.form("/wp-admin/options", url.Values{"option_page": {"writing"}, "action": {"update"}, "_wpnonce": {string(m[1])},
		"_wp_http_referer": {"/wp-admin/options-writing"}, "default_category": {"1"}, "default_post_format": {"0"},
		"classic-editor-replace": {"classic"}, "classic-editor-allow-users": {"disallow"}})
	if err != nil {
		return err
	}
	if code != http.StatusFound || !strings.Contains(h.Get("Location"), "settings-updated") {
		return fmt.Errorf("save options-writing: status %d, location %q: %s", code, h.Get("Location"), clip(b))
	}
	return nil
}

var addMetaNonceRe = regexp.MustCompile(`_ajax_nonce-add-meta['"]?\s+value=['"]([0-9a-f]+)['"]|_ajax_nonce-add-meta=([0-9a-f]+)`)

func (c *client) editScreen(postID int64) ([]byte, error) {
	code, _, b, err := c.do(http.MethodGet, fmt.Sprintf("/wp-admin/post?post=%d&action=edit", postID), nil, nil)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("edit screen of post %d: status %d", postID, code)
	}
	return b, nil
}

func (c *client) addCustomField(postID int64, key, value string) error {
	b, err := c.editScreen(postID)
	if err != nil {
		return err
	}
	m := addMetaNonceRe.FindSubmatch(b)
	if m == nil {
		return fmt.Errorf("no add-meta nonce on the edit screen of post %d", postID)
	}
	nonce := string(m[1]) + string(m[2])
	code, _, rb, err := c.form("/wp-admin/admin-ajax", url.Values{"action": {"add-meta"}, "post_id": {strconv.FormatInt(postID, 10)},
		"metakeyinput": {key}, "metavalue": {value}, "_ajax_nonce-add-meta": {nonce}})
	if err != nil {
		return err
	}
	if code != http.StatusOK || !bytes.Contains(rb, []byte(key)) {
		return fmt.Errorf("add-meta: status %d: %s", code, clip(rb))
	}
	return nil
}

// seedWebBuilder creates a page and publishes a builder document on it from <prefix>.doc.json and
// <prefix>.render.json, the way the editor does.
func (c *client) seedWebBuilder(s *snapshot, prefix string) (int64, error) {
	read := func(f string) (map[string]any, error) {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, json.Unmarshal(b, &m)
	}
	doc, err := read(prefix + ".doc.json")
	if err != nil {
		return 0, err
	}
	render, err := read(prefix + ".render.json")
	if err != nil {
		return 0, err
	}
	// The plugin's slug kept the old builder's name (webbuilder.PluginSlug).
	if _, err := c.rest(http.MethodPost, "/pursae/v1/plugins/golementor/activate", nil, http.StatusOK, nil); err != nil {
		return 0, err
	}
	if _, err := c.record(s, "plugin web builder", "/pursae/v1/plugins/golementor", []string{"active", "slug"}, "", ""); err != nil {
		return 0, err
	}
	page, err := c.rest(http.MethodPost, "/wp/v2/pages", map[string]any{"title": "Upgrade Builder Page", "status": "draft"}, http.StatusCreated, nil)
	if err != nil {
		return 0, err
	}
	pid := id(page)
	route := fmt.Sprintf("/web-builder/v2/documents/%d", pid)
	h := map[string]string{"X-WB-Api": wbAPIVersion(c, route)}
	cur, err := c.rest(http.MethodGet, route, nil, http.StatusOK, h)
	if err != nil {
		return 0, err
	}
	baseRev, _ := cur["rev"].(float64)
	if _, err := c.rest(http.MethodPost, route+"/publish", map[string]any{"baseRev": int(baseRev), "status": "publish", "doc": doc,
		"page":   map[string]any{"title": "Upgrade Builder Page", "slug": "upgrade-builder-page", "description": "Built with the Web Builder", "indexable": true},
		"render": render, "utilityCss": ".p-4{padding:1rem}"}, http.StatusOK, h); err != nil {
		return 0, err
	}
	if _, err := c.rest(http.MethodPut, "/web-builder/v2/site-settings", map[string]any{"custom_css": "/* upgrade-test */ body{--upgrade:1}"}, http.StatusOK, h); err != nil {
		return 0, err
	}
	got, err := c.rest(http.MethodGet, route, nil, http.StatusOK, h)
	if err != nil {
		return 0, err
	}
	e := entry{Name: "web builder document", Path: route, Fields: map[string]any{}}
	if d, ok := got["doc"].(map[string]any); ok {
		e.Fields["sectionIds"] = sectionIDs(d)
	}
	if len(e.Fields) == 0 || len(e.Fields["sectionIds"].([]any)) == 0 {
		return 0, fmt.Errorf("web builder document %d: no sections after publish: %s", pid, clip(mustJSON(got)))
	}
	s.Entries = append(s.Entries, e)
	ss, err := c.rest(http.MethodGet, "/web-builder/v2/site-settings", nil, http.StatusOK, h)
	if err != nil {
		return 0, err
	}
	css, ok := lookup(ss, "custom_css")
	if !ok {
		return 0, fmt.Errorf("web builder site settings: no custom_css in %s", clip(mustJSON(ss)))
	}
	s.Entries = append(s.Entries, entry{Name: "web builder site settings", Path: "/web-builder/v2/site-settings", Fields: map[string]any{"custom_css": css}})
	return pid, nil
}

// wbAPIVersion asks the server which X-WB-Api it speaks (the 409 mismatch answer carries it).
func wbAPIVersion(c *client, route string) string {
	code, _, b, err := c.do(http.MethodGet, "/wp-json"+route, nil, map[string]string{"X-WB-Api": "0"})
	if err == nil && code == http.StatusConflict {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			if v, ok := lookup(m, "data.apiVersion"); ok {
				return fmt.Sprint(v)
			}
		}
	}
	return "2"
}

func sectionIDs(doc map[string]any) []any {
	out := []any{}
	secs, _ := doc["sections"].([]any)
	for _, s := range secs {
		if m, ok := s.(map[string]any); ok {
			out = append(out, m["id"])
		}
	}
	return out
}

// --- verify -------------------------------------------------------------------------------------

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	base := fs.String("url", "", "site URL")
	snapFile := fs.String("snapshot", "", "snapshot written by seed")
	_ = fs.Parse(args) // ExitOnError: Parse exits on a bad flag
	b, err := os.ReadFile(*snapFile)
	if err != nil {
		return err
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	var fails []string
	fail := func(f string, a ...any) { fails = append(fails, fmt.Sprintf(f, a...)) }

	for _, p := range []string{"/healthz", "/readyz"} {
		c := newClient(*base)
		if code, _, body, err := c.do(http.MethodGet, p, nil, nil); err != nil || code != http.StatusOK {
			fail("%s: status %d (err %v): %s", p, code, err, clip(body))
		}
	}
	// Every seeded user logs in with the password set on the old release.
	var admin *client
	for _, u := range sortedKeys(s.Users) {
		c := newClient(*base)
		if err := c.login(u, s.Users[u]); err != nil {
			fail("%v", err)
			continue
		}
		if u == adminUser {
			admin = c
		}
	}
	// The Application Password created by the old release still authenticates, and reads everything back.
	api := newClient(*base)
	api.basic = [2]string{adminUser, s.AppPassword}
	if _, err := api.rest(http.MethodGet, "/wp/v2/users/me", nil, http.StatusOK, nil); err != nil {
		fail("application password: %v", err)
		if admin == nil {
			return report(fails, &s)
		}
		api = admin
	}
	compared := 0
	for _, e := range s.Entries {
		hdr := map[string]string{}
		if strings.HasPrefix(e.Path, "/web-builder/") {
			hdr["X-WB-Api"] = wbAPIVersion(api, e.Path)
		}
		reader := api
		if e.Cookie && admin != nil {
			reader = admin
		}
		obj, err := reader.rest(http.MethodGet, withContext(e.Path), nil, http.StatusOK, hdr)
		if err != nil {
			fail("%s: %v", e.Name, err)
			continue
		}
		for _, f := range sortedKeys(e.Fields) {
			want := e.Fields[f]
			var got any
			var ok bool
			if f == "sectionIds" {
				d, _ := obj["doc"].(map[string]any)
				got, ok = sectionIDs(d), d != nil
			} else {
				got, ok = lookup(obj, f)
			}
			compared++
			if !ok {
				fail("%s: field %s is missing (was %s)", e.Name, f, mustJSON(want))
			} else if !equalJSON(want, got) {
				fail("%s: field %s changed: %s -> %s", e.Name, f, mustJSON(want), mustJSON(got))
			}
		}
		if e.Page != "" {
			c := newClient(*base)
			code, _, body, err := c.do(http.MethodGet, e.Page, nil, nil)
			if err != nil || code != http.StatusOK {
				fail("%s: %s answers %d (err %v)", e.Name, e.Page, code, err)
			} else if e.Expect != "" && !bytes.Contains(body, []byte(e.Expect)) {
				fail("%s: %s does not show %q", e.Name, e.Page, e.Expect)
			}
		}
	}
	// Uploaded files are still served, byte for byte.
	for src, sum := range s.Media {
		c := newClient(*base)
		code, _, body, err := c.do(http.MethodGet, src, nil, nil)
		got := sha256.Sum256(body)
		if err != nil || code != http.StatusOK || hex.EncodeToString(got[:]) != sum {
			fail("media file %s: status %d (err %v), sha256 %x, want %s", src, code, err, got, sum)
		}
	}
	// The custom field shows on the post's edit screen.
	if admin != nil && s.CustomFieldPost != 0 {
		body, err := admin.editScreen(s.CustomFieldPost)
		switch {
		case err != nil:
			fail("custom field: %v", err)
		case !bytes.Contains(body, []byte(s.CustomField[0])) || !bytes.Contains(body, []byte(s.CustomField[1])):
			fail("custom field %s=%q is not on the edit screen of post %d", s.CustomField[0], s.CustomField[1], s.CustomFieldPost)
		}
	}
	// The front page.
	if code, _, _, err := newClient(*base).do(http.MethodGet, "/", nil, nil); err != nil || code != http.StatusOK {
		fail("front page: status %d (err %v)", code, err)
	}
	if len(fails) == 0 {
		fmt.Printf("verify: %s -> HEAD: %d users logged in, %d objects, %d fields, %d media files unchanged\n",
			s.Version, len(s.Users), len(s.Entries), compared, len(s.Media))
	}
	return report(fails, &s)
}

func report(fails []string, s *snapshot) error {
	if len(fails) == 0 {
		return nil
	}
	return fmt.Errorf("upgrade from %s lost or changed data:\n  %s", s.Version, strings.Join(fails, "\n  "))
}

// equalJSON compares two decoded JSON values (numbers as float64, so 7 == 7.0).
func equalJSON(a, b any) bool {
	return bytes.Equal(mustJSON(a), mustJSON(b))
}
