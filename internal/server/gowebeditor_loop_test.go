package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestGweLoopSearchWords(t *testing.T) {
	for in, want := range map[string]string{
		`gopher race`:          "gopher|race",
		`"quiet weekend" -go`:  "quiet weekend|-go",
		`  a,b+c `:             "a|b|c",
		`"unterminated phrase`: "unterminated|phrase",
	} {
		if got := strings.Join(gweLoopSearchWords(in), "|"); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// TestGweSearchProps: a search submitted by a Search widget (e_search_props) lists only what the
// widget's query allows (here: posts in Guides), on one page; an unknown widget is a 404.
func TestGweSearchProps(t *testing.T) {
	t.Skip("old builder detached from routing, admin and front (R1c/R1d); this file goes in R2")
	ts, c := testServer(t)
	base := ts.URL
	if code, body, _ := post(t, c, base+"/wp-admin/install?step=2", url.Values{
		"weblog_title": {"Search Site"}, "user_name": {"admin"}, "admin_password": {"correct horse"}, "admin_email": {"admin@example.test"},
	}); code != http.StatusOK || !strings.Contains(body, "Success!") {
		t.Fatalf("install: %d", code)
	}
	ctx := context.Background()
	pool := getPool(t)
	var guides int64
	if err := pool.QueryRow(ctx, `INSERT INTO terms (taxonomy, slug, name, post_count) VALUES ('category', 'guides', 'Guides', 2) RETURNING id`).Scan(&guides); err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"Kiwi guide one", "Kiwi news", "Kiwi guide two", "Plain guide"} {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO posts (type, author_id, slug, title, content, status, published_at) VALUES ('post', 1, $1, $2, 'body', 'publish', now() - $3::int * interval '1 hour') RETURNING id`,
			"p"+strconv.Itoa(i), title, i).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(title, "guide") {
			if _, err := pool.Exec(ctx, `INSERT INTO post_terms (post_id, term_id) VALUES ($1, $2)`, id, guides); err != nil {
				t.Fatal(err)
			}
		}
	}
	data := `[{"id":"se00001","elType":"widget","widgetType":"search","settings":{"search_query_post_type":"post","search_query_include":["terms"],"search_query_include_term_ids":["` +
		strconv.FormatInt(guides, 10) + `"]},"elements":[]}]`
	meta, _ := json.Marshal(map[string]any{"_elementor_edit_mode": "builder", "_elementor_data": data, "_elementor_version": "4.3.5"})
	var page int64
	if err := pool.QueryRow(ctx, `INSERT INTO posts (type, author_id, slug, title, status, published_at, meta) VALUES ('page', 1, 'search-page', 'Search page', 'publish', now(), $1) RETURNING id`,
		meta).Scan(&page); err != nil {
		t.Fatal(err)
	}
	titles := func(body string) string {
		var out []string
		for _, m := range regexp.MustCompile(`<h2 class="wp-block-post-title[^"]*"><a [^>]*>([^<]*)</a>`).FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return strings.Join(out, "|")
	}
	code, body, _ := get(t, c, base+"/?s=kiwi")
	if code != http.StatusOK || titles(body) != "Kiwi guide one|Kiwi news|Kiwi guide two" {
		t.Errorf("plain search: %d %q", code, titles(body))
	}
	props := "se00001-" + strconv.FormatInt(page, 10)
	code, body, _ = get(t, c, base+"/?s=kiwi&e_search_props="+props)
	if code != http.StatusOK || titles(body) != "Kiwi guide one|Kiwi guide two" {
		t.Errorf("widget search: %d %q", code, titles(body))
	}
	for _, bad := range []string{"zz00000-" + strconv.FormatInt(page, 10), "se00001-999999", "garbage"} {
		if code, _, _ := get(t, c, base+"/?s=kiwi&e_search_props="+bad); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", bad, code)
		}
	}
}
