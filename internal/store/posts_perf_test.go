package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDateArchiveRange(t *testing.T) {
	ist := time.FixedZone("UTC+3:00", 3*3600)
	cases := []struct {
		y, m, d  int
		from, to string
		ok       bool
	}{
		{2026, 0, 0, "2026-01-01T00:00:00+03:00", "2027-01-01T00:00:00+03:00", true},
		{2026, 2, 0, "2026-02-01T00:00:00+03:00", "2026-03-01T00:00:00+03:00", true},
		{2026, 12, 0, "2026-12-01T00:00:00+03:00", "2027-01-01T00:00:00+03:00", true},
		{2026, 2, 28, "2026-02-28T00:00:00+03:00", "2026-03-01T00:00:00+03:00", true},
		{2026, 2, 31, "", "", false},
		{2026, 13, 0, "", "", false},
		{0, 1, 0, "", "", false},
	}
	for _, c := range cases {
		from, to, ok := DateArchiveRange(c.y, c.m, c.d, ist)
		if ok != c.ok || (ok && (from.Format(time.RFC3339) != c.from || to.Format(time.RFC3339) != c.to)) {
			t.Errorf("%d-%d-%d: %v %v %v", c.y, c.m, c.d, from, to, ok)
		}
	}
}

func TestLimitOffsetClamp(t *testing.T) {
	if got := limitOffset(10, 0); got != " LIMIT 10 OFFSET 0" {
		t.Errorf("page 0: %q", got)
	}
	if got := limitOffset(10, 3); got != " LIMIT 10 OFFSET 20" {
		t.Errorf("page 3: %q", got)
	}
	// A page number near MaxInt used to overflow (page-1)*perPage into a negative OFFSET (a 500).
	if got := limitOffset(100, int(^uint(0)>>1)); got != " LIMIT 100 OFFSET 99999900" {
		t.Errorf("huge page: %q", got)
	}
}

// TestPostListOrderAndIndexes: published posts always carry published_at (the trigger of migration
// 0032 fills raw inserts), lists order by it with the id as tie-breaker, date archives use the site
// time zone, huge pages are empty instead of failing, and the list query can use posts_list_date.
func TestPostListOrderAndIndexes(t *testing.T) {
	s, _, u1, _ := wbTestStore(t)
	ctx := context.Background()
	ist := time.FixedZone("UTC+3:00", 3*3600)
	// 2026-03-31 22:30 UTC is 1 April 01:30 in UTC+3.
	at := time.Date(2026, 3, 31, 22, 30, 0, 0, time.UTC)
	var ids []int64
	for i := 0; i < 3; i++ {
		p := &Post{Type: "post", AuthorID: u1, Title: "Same time " + string(rune('A'+i)), Status: "publish", PublishedAt: &at}
		if err := s.InsertPost(ctx, p); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	var raw int64
	if err := s.Pool.QueryRow(ctx, `INSERT INTO posts (type, slug, title, status, created_at) VALUES ('post', 'raw', 'Raw', 'publish', $1) RETURNING id`,
		at.Add(-3*time.Hour)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var draft int64
	_ = s.Pool.QueryRow(ctx, `INSERT INTO posts (type, slug, title, status) VALUES ('post', 'd', 'D', 'draft') RETURNING id`).Scan(&draft)
	if _, err := s.Pool.Exec(ctx, `UPDATE posts SET status = 'publish' WHERE id = $1`, draft); err != nil {
		t.Fatal(err)
	}
	var missing int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE status IN ('publish','private','future') AND published_at IS NULL`).Scan(&missing)
	if missing != 0 {
		t.Fatalf("%d published posts without published_at", missing)
	}

	got, total, err := s.QueryPosts(ctx, PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, PerPage: 10, Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, p := range got {
		if p.PublishedAt != nil && p.PublishedAt.Equal(at) {
			order = append(order, p.ID)
		}
	}
	if total != len(got) || len(order) != 3 || order[0] != ids[2] || order[2] != ids[0] {
		t.Errorf("order of equal dates %v (want id DESC of %v), total %d", order, ids, total)
	}
	if got[len(got)-1].ID != raw {
		t.Errorf("the raw insert (oldest created_at) should come last, got %d", got[len(got)-1].ID)
	}

	april, _, _ := s.QueryPosts(ctx, PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, Year: 2026, Month: 4, Day: 1, Loc: ist, PerPage: 10})
	march, _, _ := s.QueryPosts(ctx, PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, Year: 2026, Month: 3, Loc: ist, PerPage: 10})
	if len(april) != 3 {
		t.Errorf("1 April in UTC+3 has %d posts, want 3", len(april))
	}
	for _, p := range march {
		if p.PublishedAt.Equal(at) {
			t.Error("March in UTC+3 lists a post of 1 April local time")
		}
	}
	if days, _ := s.PostDays(ctx, 2026, 4, ist); len(days) != 1 || days[0] != 1 {
		t.Errorf("April days %v", days)
	}

	if huge, total, err := s.QueryPosts(ctx, PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, PerPage: 100, Page: int(^uint(0) >> 1)}); err != nil || len(huge) != 0 || total == 0 {
		t.Errorf("huge page: %d posts, total %d, %v", len(huge), total, err)
	}

	// The planner may use the list index (sequential scans disabled to make the choice visible).
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, _ = tx.Exec(ctx, `SET LOCAL enable_seqscan = off`)
	var plan strings.Builder
	rows, err := tx.Query(ctx, `EXPLAIN SELECT id FROM posts WHERE type = 'post' AND status = 'publish' ORDER BY published_at DESC, id DESC LIMIT 10`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		plan.WriteString(line + "\n")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "posts_list_date") || strings.Contains(plan.String(), "Sort") {
		t.Errorf("list query plan:\n%s", plan.String())
	}
	// Search: both branches of "search @@ … OR title ILIKE …" are index scans (GIN tsvector, trigram).
	var where []any
	q := PostQuery{Search: "meadow"}
	cond := q.where(&where)
	plan.Reset()
	rows, err = tx.Query(ctx, `EXPLAIN SELECT id FROM posts WHERE `+cond, where...)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		plan.WriteString(line + "\n")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "posts_title_trgm") || !strings.Contains(plan.String(), "posts_search") {
		t.Errorf("search plan:\n%s", plan.String())
	}
	for _, idx := range []string{"users_lower_email", "users_lower_login", "comments_parent", "posts_featured_media", "widgets_area", "posts_title_trgm"} {
		var ok bool
		_ = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = $1)`, idx).Scan(&ok)
		if !ok {
			t.Errorf("index %s missing", idx)
		}
	}
}
