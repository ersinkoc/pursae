package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/config"
	"wpgo/internal/db"
	"wpgo/internal/store"
)

// queryCounter is a pgx tracer that counts the statements sent to the database.
type queryCounter struct {
	n   atomic.Int64
	mu  sync.Mutex
	sql map[string]int // statement texts while logging (WPGO_PERF_SQL=1)
}

func (q *queryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	q.n.Add(1)
	if os.Getenv("WPGO_PERF_SQL") != "" {
		q.mu.Lock()
		if q.sql == nil {
			q.sql = map[string]int{}
		}
		k := strings.Join(strings.Fields(d.SQL), " ")
		if len(k) > 160 {
			k = k[:160]
		}
		q.sql[k]++
		if sub := os.Getenv("WPGO_PERF_STACK"); sub != "" && strings.Contains(k, sub) {
			pcs := make([]uintptr, 30)
			frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
			var fn []string
			for {
				f, more := frames.Next()
				if strings.Contains(f.Function, "internal/server") {
					fn = append(fn, strings.TrimPrefix(f.Function, "wpgo/internal/server.")+":"+strconv.Itoa(f.Line))
				}
				if !more || len(fn) > 4 {
					break
				}
			}
			q.sql["STACK "+strings.Join(fn, " < ")]++
		}
		q.mu.Unlock()
	}
	return ctx
}

// dump logs the statements seen since the last dump, most frequent first.
func (q *queryCounter) dump(t *testing.T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	type kv struct {
		k string
		n int
	}
	var list []kv
	for k, n := range q.sql {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	for _, e := range list {
		t.Logf("%4d %s", e.n, e.k)
	}
	q.sql = nil
}
func (q *queryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// newCountingTestServer is newTestServer (Web Builder off) on a pool whose statements are counted.
func newCountingTestServer(t *testing.T) (*httptest.Server, *http.Client, *Server, *queryCounter) {
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
	cfg, err := pgxpool.ParseConfig(testURL)
	if err != nil {
		t.Fatal(err)
	}
	qc := &queryCounter{}
	cfg.ConnConfig.Tracer = qc
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(nil)
	srv, err := New(ctx, config.Config{SiteURL: "http://" + ts.Listener.Addr().String(), UploadsDir: t.TempDir(), WebDir: "../../web",
		ThemeLibraryDir: "../../library/themes"}, pool)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = srv.Handler()
	ts.Start()
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return ts, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, srv, qc
}

// seedPerfPosts adds n posts with two categories, two tags and a featured image each, by an
// author with a media-library avatar.
func seedPerfPosts(t *testing.T, srv *Server, n int) {
	t.Helper()
	ctx := context.Background()
	var admin int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&admin)
	att := func(name string) *store.Attachment {
		a := &store.Attachment{UploaderID: admin, Slug: name, FilePath: "2026/11/" + name + ".jpg", Mime: "image/jpeg", Width: 1600, Height: 1067, Alt: name,
			Variants: []store.AttachmentVariant{
				{Name: "medium", FilePath: "2026/11/" + name + "-300x200.jpg", Width: 300, Height: 200, Mime: "image/jpeg"},
				{Name: "large", FilePath: "2026/11/" + name + "-1024x683.jpg", Width: 1024, Height: 683, Mime: "image/jpeg"},
				{Name: "post-thumbnail", FilePath: "2026/11/" + name + "-1200x800.jpg", Width: 1200, Height: 800, Mime: "image/jpeg"},
			}}
		if err := srv.store.InsertAttachment(ctx, a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	avatar := att("avatar")
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE users SET meta = meta || jsonb_build_object('avatar_id', $2::bigint),
		profile = profile || '{"description":"Writes about meadows."}' WHERE id = $1`, admin, avatar.ID); err != nil {
		t.Fatal(err)
	}
	var terms []int64
	for _, tm := range []*store.Term{{Taxonomy: "category", Slug: "meadows", Name: "Meadows"}, {Taxonomy: "category", Slug: "rivers", Name: "Rivers"},
		{Taxonomy: "post_tag", Slug: "spring", Name: "Spring"}, {Taxonomy: "post_tag", Slug: "summer", Name: "Summer"}} {
		if err := srv.store.InsertTerm(ctx, tm); err != nil {
			t.Fatal(err)
		}
		terms = append(terms, tm.ID)
	}
	for i := 0; i < n; i++ {
		img := att("photo-" + itoa(i+1))
		p := mustInsertPost(t, srv, &store.Post{Type: "post", AuthorID: admin, Title: "Perf note " + itoa(i+1), FeaturedMediaID: img.ID,
			Content:       "<!-- wp:paragraph -->\n<p>Clover " + itoa(i+1) + ".</p>\n<!-- /wp:paragraph -->",
			CommentStatus: "open"})
		if err := srv.store.SetPostTerms(ctx, p.ID, "category", terms[:2]); err != nil {
			t.Fatal(err)
		}
		if err := srv.store.SetPostTerms(ctx, p.ID, "post_tag", terms[2:]); err != nil {
			t.Fatal(err)
		}
	}
	_ = srv.store.RecountTerms(ctx, terms)
}

var perfVolatile = regexp.MustCompile(`(_wpnonce|nonce|ver)=[0-9a-zA-Z]+|"nonce":"[^"]*"`)

// TestFrontQueryCount measures the statements a 10-post blog home costs on a Pursae Cache miss and
// keeps the loop free of per-post queries (posts × terms, author, avatar, featured image).
func TestFrontQueryCount(t *testing.T) {
	ts, c, srv, qc := newCountingTestServer(t)
	installAndLogin(t, ts, c)
	seedPerfPosts(t, srv, 10)
	anon := &http.Client{CheckRedirect: c.CheckRedirect}
	measure := func(path string) (int64, string) {
		t.Helper()
		before := qc.n.Load()
		qc.mu.Lock()
		qc.sql = nil
		qc.mu.Unlock()
		code, body, _ := get(t, anon, ts.URL+path)
		if code != http.StatusOK {
			t.Fatalf("%s: %d", path, code)
		}
		n := qc.n.Load() - before
		if os.Getenv("WPGO_PERF_SQL") != "" && strings.Contains(path, "count") {
			qc.dump(t)
		}
		return n, body
	}
	measure("/?warm=1") // lazy settings and theme state
	n, body := measure("/?count=1")
	t.Logf("twentytwentyfive home, 10 posts: %d queries", n)
	if strings.Count(body, `class="wp-block-post `) < 10 {
		t.Fatalf("home lists %d posts", strings.Count(body, `class="wp-block-post `))
	}
	if out := os.Getenv("WPGO_PERF_DUMP"); out != "" {
		_ = os.WriteFile(out+"/tt5-home.html", []byte(perfVolatile.ReplaceAllString(body, "$1=X")), 0o644)
	}
	if n > 30 {
		t.Errorf("twentytwentyfive home costs %d queries", n)
	}

	installLibraryTheme(t, c, ts.URL, "byte-notes", true)
	measure("/?warm=2")
	n, body = measure("/?count=2")
	t.Logf("byte-notes home, 10 posts: %d queries", n)
	if out := os.Getenv("WPGO_PERF_DUMP"); out != "" {
		_ = os.WriteFile(out+"/byte-notes-home.html", []byte(perfVolatile.ReplaceAllString(body, "$1=X")), 0o644)
	}
	if n > 40 {
		t.Errorf("byte-notes home costs %d queries", n)
	}
	// A %category%/%author% structure looks the category and author up through the same memo.
	if err := srv.settings.Set(context.Background(), map[string]any{"permalink_structure": "/%category%/%author%/%postname%/"}); err != nil {
		t.Fatal(err)
	}
	measure("/?warm=4")
	n, body = measure("/?count=4")
	t.Logf("byte-notes home, /%%category%%/%%author%%/ links: %d queries", n)
	if !strings.Contains(body, "/meadows/admin/perf-note-10/") {
		t.Error("home misses the category/author permalink")
	}
	if n > 40 {
		t.Errorf("byte-notes home with /%%category%%/ links costs %d queries", n)
	}
	if err := srv.settings.Set(context.Background(), map[string]any{"permalink_structure": "/%year%/%monthnum%/%day%/%postname%/"}); err != nil {
		t.Fatal(err)
	}

	posts, _, _ := srv.store.QueryPosts(context.Background(), store.PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, PerPage: 1})
	single := pathOf(t, ts.URL, srv.links(context.Background()).Permalink(posts[0]))
	measure(single + "?warm=3")
	n, body = measure(single + "?count=3")
	t.Logf("byte-notes single post: %d queries", n)
	if out := os.Getenv("WPGO_PERF_DUMP"); out != "" {
		_ = os.WriteFile(out+"/byte-notes-single.html", []byte(perfVolatile.ReplaceAllString(body, "$1=X")), 0o644)
	}
}
