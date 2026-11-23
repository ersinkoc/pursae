package server

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// wxrScan sees exactly what decoding the whole file into a wxrDoc sees, item for item.
func TestWxrScanMatchesDecode(t *testing.T) {
	raw, err := os.ReadFile("testdata/wxr-woocommerce.xml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := wxrParseDoc(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var items []wxrItem
	hdr := wxrHeader{}
	err = wxrScan(bytes.NewReader(raw), func(v any) {
		switch v := v.(type) {
		case *wxrAuthor:
			hdr.Authors = append(hdr.Authors, *v)
		case *wxrCategory:
			hdr.Categories = append(hdr.Categories, *v)
		case *wxrTag:
			hdr.Tags = append(hdr.Tags, *v)
		case *wxrTerm:
			hdr.Terms = append(hdr.Terms, *v)
		}
	}, func(it *wxrItem) error { items = append(items, *it); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || len(hdr.Terms) == 0 {
		t.Fatalf("the fixture has %d items and %d terms: nothing to compare", len(items), len(hdr.Terms))
	}
	if !reflect.DeepEqual(items, doc.Channel.Items) {
		t.Error("the items differ from a whole-document decode")
	}
	if !reflect.DeepEqual(hdr.Authors, doc.Channel.Authors) || !reflect.DeepEqual(hdr.Categories, doc.Channel.Categories) ||
		!reflect.DeepEqual(hdr.Tags, doc.Channel.Tags) || !reflect.DeepEqual(hdr.Terms, doc.Channel.Terms) {
		t.Error("the header differs from a whole-document decode")
	}
	h2, n, err := wxrReadHeader(bytes.NewReader(raw))
	if err != nil || n != len(items) || !reflect.DeepEqual(*h2, hdr) {
		t.Errorf("wxrReadHeader: %d items (%d), %v", n, len(items), err)
	}
}

// Files that are not WXR, or are damaged, fail with the importer's message, from the first pass.
func TestWxrScanErrors(t *testing.T) {
	for name, in := range map[string]string{
		"empty":     "",
		"text":      "hello",
		"cut off":   `<rss><channel><item><title>a</title><wp:post_id>1</wp:post_id></item><item><title>b`,
		"bad id":    `<rss xmlns:wp="http://wordpress.org/export/1.2/"><channel><item><wp:post_id>x</wp:post_id></item></channel></rss>`,
		"no close":  `<rss><channel><item><title>a</title></item>`,
		"cut tag":   `<rss><channel><item><title>a</title></item></chan`,
		"only decl": `<?xml version="1.0"?>`,
	} {
		_, _, err := wxrReadHeader(strings.NewReader(in))
		if err == nil || !strings.Contains(err.Error(), "does not appear to be a WXR file") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A well-formed file with no channel is an empty import, as it always was.
	if _, n, err := wxrReadHeader(strings.NewReader(`<rss></rss>`)); err != nil || n != 0 {
		t.Errorf("no channel: %d %v", n, err)
	}
}

// A WXR of a few hundred megabytes is read with a heap that stays small: items are handled one at a
// time. (A whole-document decode of the same file holds several times its size.)
func TestWxrScanStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("generates a 250 MB file")
	}
	const items, bodySize = 30000, 8 << 10
	body := strings.Repeat("lorem ipsum dolor sit amet ", bodySize/27)
	pr, pw := io.Pipe()
	go func() {
		bw := newErrWriter(pw)
		fmt.Fprint(bw, `<?xml version="1.0"?><rss xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><wp:wxr_version>1.2</wp:wxr_version>`)
		fmt.Fprint(bw, `<wp:author><wp:author_id>1</wp:author_id><wp:author_login>a</wp:author_login></wp:author>`)
		for i := 1; i <= items; i++ {
			fmt.Fprintf(bw, `<item><title>Post %d</title><content:encoded><![CDATA[%s]]></content:encoded><wp:post_id>%d</wp:post_id><wp:post_type>post</wp:post_type><wp:postmeta><wp:meta_key>k</wp:meta_key><wp:meta_value>v</wp:meta_value></wp:postmeta></item>`, i, body, i)
		}
		fmt.Fprint(bw, `</channel></rss>`)
		pw.CloseWithError(bw.err)
	}()
	// Other tests of the package leave heap behind: measure what this read adds.
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var count int
	var size int64
	var peak uint64
	var last int64
	err := wxrScan(pr, nil, func(it *wxrItem) error {
		count++
		size += int64(len(it.Content))
		last = it.PostID
		if count%2000 == 0 {
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			peak = max(peak, ms.HeapAlloc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != items || last != items || size < int64(items)*int64(bodySize)/2 {
		t.Fatalf("%d items (last %d), %d bytes of content", count, last, size)
	}
	if peak > base.HeapAlloc+96<<20 {
		t.Errorf("heap grew by %d MB while reading a %d MB file", (peak-base.HeapAlloc)>>20, size>>20)
	}
}

type errWriter struct {
	w   io.Writer
	err error
}

func newErrWriter(w io.Writer) *errWriter { return &errWriter{w: w} }

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}
	n, err := e.w.Write(p)
	e.err = err
	return n, err
}

// wxrParseDoc decodes a whole file like the importer did before it streamed (the reference).
func wxrParseDoc(r io.Reader) (*wxrDoc, error) {
	var doc wxrDoc
	dec := xml.NewDecoder(r)
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

const wxrOutOfOrderFixture = `<?xml version="1.0"?>
<rss version="2.0" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
<item><title>Streamed post</title><dc:creator>writer</dc:creator><content:encoded><![CDATA[<p>Hello</p>]]></content:encoded>
<wp:post_id>10</wp:post_id><wp:post_date>2024-01-02 10:00:00</wp:post_date><wp:post_name>streamed-post</wp:post_name><wp:status>publish</wp:status><wp:post_type>post</wp:post_type>
<category domain="category" nicename="late-cat"><![CDATA[Late Category]]></category></item>
<wp:author><wp:author_id>2</wp:author_id><wp:author_login>writer</wp:author_login><wp:author_email>writer@example.test</wp:author_email><wp:author_display_name>Writer</wp:author_display_name></wp:author>
<wp:category><wp:term_id>5</wp:term_id><wp:category_nicename>late-cat</wp:category_nicename><wp:category_parent></wp:category_parent><wp:cat_name><![CDATA[Late Category]]></wp:cat_name></wp:category>
%s
</channel></rss>`

// The authors and terms of a file are used for its items wherever they are in the file, and a file
// that is cut off imports nothing (the first pass reads it all before the second imports).
func TestWXRStreamImport(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	opens := 0
	opener := func(data string) func() (io.ReadCloser, error) {
		return func() (io.ReadCloser, error) { opens++; return io.NopCloser(strings.NewReader(data)), nil }
	}
	good := fmt.Sprintf(wxrOutOfOrderFixture, "")
	broken := good[:len(good)-40]
	var posts int
	count := func() int {
		_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE type = 'post' AND slug = 'streamed-post'`).Scan(&posts)
		return posts
	}
	if _, err := srv.importWXRStream(ctx, admin, opener(broken), wxrImportOptions{}); err == nil || !strings.Contains(err.Error(), "does not appear to be a WXR file") {
		t.Fatalf("a cut-off file: %v", err)
	}
	if count() != 0 {
		t.Fatal("a damaged file imported something")
	}
	opens = 0
	oldTerms := map[int64]int64{}
	rep, err := srv.importWXRStream(ctx, admin, opener(good), wxrImportOptions{TermMap: oldTerms})
	if err != nil {
		t.Fatal(err)
	}
	if opens != 2 || rep.Posts != 1 || count() != 1 {
		t.Fatalf("opens %d, report %+v, posts %d", opens, rep, posts)
	}
	// The category in the header (after the item) was created and the post has it, by the author that
	// the header (after the item) names.
	var cat, author string
	if err := srv.store.Pool.QueryRow(ctx, `SELECT t.slug, u.login FROM posts p JOIN post_terms r ON r.post_id = p.id JOIN terms t ON t.id = r.term_id AND t.taxonomy = 'category'
		JOIN users u ON u.id = p.author_id WHERE p.slug = 'streamed-post' AND t.slug = 'late-cat'`).Scan(&cat, &author); err != nil {
		t.Fatal(err)
	}
	if cat != "late-cat" || author != "admin" || oldTerms[5] == 0 {
		t.Errorf("category %q author %q terms %v", cat, author, oldTerms)
	}
	// Again: nothing new.
	rep, err = srv.importWXRStream(ctx, admin, opener(good), wxrImportOptions{})
	if err != nil || rep.Posts != 0 || count() != 1 {
		t.Errorf("second import: %+v %v (%d posts)", rep, err, posts)
	}
}

// A media file larger than the upload limit (64 MB) comes through a package: streamed to the uploads
// directory, up to PURSAE_WP2P_MAX_MEDIA_MB.
func TestWP2PLargeMedia(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 70 MB file")
	}
	ts, c, srv := testServerBuilderOff(t)
	installUsersSite(t, c, ts.URL)
	ctx := context.Background()
	big := filepath.Join(t.TempDir(), "keynote.mp4")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxUploadBytes + 6<<20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	// Over the limit set for packages: refused, with the way out in the message.
	t.Setenv("PURSAE_WP2P_MAX_MEDIA_MB", "1")
	if _, err := srv.sideloadLocal(ctx, big, "keynote.mp4", 0); err == nil || !strings.Contains(err.Error(), "PURSAE_WP2P_MAX_MEDIA_MB") {
		t.Fatalf("a file over the package limit: %v", err)
	}
	t.Setenv("PURSAE_WP2P_MAX_MEDIA_MB", "")
	if got := wp2pMaxMedia(); got != 4096<<20 {
		t.Fatalf("default limit %d", got)
	}
	a, err := srv.sideloadLocal(ctx, big, "keynote.mp4", 0)
	if err != nil {
		t.Fatalf("a file over the upload limit: %v", err)
	}
	if a.FileSize != maxUploadBytes+6<<20 {
		t.Errorf("size %d", a.FileSize)
	}
	st, err := os.Stat(filepath.Join(srv.cfg.UploadsDir, filepath.FromSlash(a.FilePath)))
	if err != nil || st.Size() != a.FileSize {
		t.Errorf("stored file: %v %v", st, err)
	}
	// The upload limit itself stays for uploads.
	if _, err := srv.insertAttachmentFile(ctx, "again.mp4", io.LimitReader(zeroReader{}, maxUploadBytes+1), 0, 0); err == nil {
		t.Error("an upload over the upload limit was accepted")
	}

	// End to end through a package: media.jsonl, media/<id> of 65 MB and the attachment in content.xml.
	body, err := os.ReadFile(big)
	if err != nil {
		t.Fatal(err)
	}
	wxr := `<?xml version="1.0"?><rss xmlns:wp="http://wordpress.org/export/1.2/" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel>
<item><title>Annual keynote</title><wp:post_id>77</wp:post_id><wp:post_date>2024-02-03 04:05:06</wp:post_date><wp:post_name>annual-keynote</wp:post_name><wp:status>inherit</wp:status><wp:post_type>attachment</wp:post_type><wp:attachment_url>https://old.example.test/wp-content/uploads/keynote.mp4</wp:attachment_url></item>
</channel></rss>`
	key := wp2pTokenKey("pmt_large_media")
	pkg := wp2pBuildPackage(t, key, []wp2pTestEntry{
		{"manifest.json", []byte(`{"format":1,"home_url":"https://old.example.test","counts":{}}`)},
		{"content.xml", []byte(wxr)},
		{"media.jsonl", wp2pJSONL(t, map[string]any{"id": 77, "name": "keynote-pkg.mp4", "size": len(body)})},
		{"media/77", body},
	})
	pkgPath := filepath.Join(t.TempDir(), "big.wp2p")
	if err := os.WriteFile(pkgPath, pkg, 0o600); err != nil {
		t.Fatal(err)
	}
	admin, _ := srv.store.UserByLoginOrEmail(ctx, "admin")
	id := wp2pNewID()
	if _, err := srv.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, created_by, state) VALUES ($1, $2, 'running')`, id, admin.ID); err != nil {
		t.Fatal(err)
	}
	r := &wp2pRun{s: srv, id: id, dir: filepath.Join(t.TempDir(), "work"), pkg: pkgPath, keepPackage: true, key: &key, u: admin}
	if err := r.run(ctx); err != nil {
		t.Fatal(err)
	}
	var size int64
	if err := srv.store.Pool.QueryRow(ctx, `SELECT file_size FROM media WHERE title = 'Annual keynote'`).Scan(&size); err != nil || size != int64(len(body)) {
		t.Errorf("media of the package: size %d, %v (report %+v)", size, err, r.rep)
	}
	if r.rep.Counts["media"] != 1 {
		t.Errorf("counts %v", r.rep.Counts)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// A running import touches its row while it works, and stops when it is over; a command-line run
// whose process was killed is found by the silence, closed, and its extracted files deleted.
func TestWP2PHeartbeatAndStaleCLI(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	ctx := context.Background()
	pool := srv.store.Pool
	old := wp2pHeartbeat
	wp2pHeartbeat = 15 * time.Millisecond
	t.Cleanup(func() { wp2pHeartbeat = old })

	newRow := func(state string, cli bool, age string) string {
		id := wp2pNewID()
		if _, err := pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, state, cli, updated_at) VALUES ($1, $2, $3, now() - $4::interval)`, id, state, cli, age); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(srv.wp2pWorkDir(id), "media"), 0o700); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(srv.wp2pWorkDir(id), "users.jsonl"), []byte("{}"), 0o600)
		return id
	}
	age := func(id string) float64 {
		var s float64
		if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM now() - updated_at)::float8 FROM wp2p_migrations WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	live := newRow("running", true, "2 hours")
	r := &wp2pRun{s: srv, id: live}
	stop := r.heartbeat()
	time.Sleep(200 * time.Millisecond)
	if a := age(live); a > 5 {
		t.Errorf("the row was not touched while the run was alive: %.0f s old", a)
	}
	stop()
	// After the run, nothing touches the row any more.
	if _, err := pool.Exec(ctx, `UPDATE wp2p_migrations SET updated_at = now() - interval '2 hours' WHERE id = $1`, live); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if a := age(live); a < 3600 {
		t.Errorf("the heartbeat went on after the run ended: %.0f s old", a)
	}

	killed := newRow("running", true, "45 minutes")
	fresh := newRow("running", true, "5 minutes")
	job := newRow("running", false, "5 hours") // a background import: the job queue resumes it
	done := newRow("done", true, "5 hours")
	ids, err := srv.wp2pFailStaleCLI(ctx)
	if err != nil {
		t.Fatal(err)
	}
	has := func(id string) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	if !has(killed) || !has(live) || has(fresh) || has(job) || has(done) {
		t.Errorf("closed %v (killed %s, fresh %s)", ids, killed, fresh)
	}
	state := func(id string) string {
		var s string
		_ = pool.QueryRow(ctx, `SELECT state FROM wp2p_migrations WHERE id = $1`, id).Scan(&s)
		return s
	}
	for id, want := range map[string]string{killed: "failed", fresh: "running", job: "running", done: "done"} {
		if got := state(id); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
	for id, gone := range map[string]bool{killed: true, fresh: false, job: false, done: false} {
		_, err := os.Stat(srv.wp2pWorkDir(id))
		if (err != nil) != gone {
			t.Errorf("%s: extracted files gone = %v, want %v", id, err != nil, gone)
		}
	}
	var msg string
	_ = pool.QueryRow(ctx, `SELECT error FROM wp2p_migrations WHERE id = $1`, killed).Scan(&msg)
	if !strings.Contains(msg, "run it again") {
		t.Errorf("message %q", msg)
	}
}
