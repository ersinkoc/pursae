package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"

	"wpgo/internal/webbuilder"
)

func TestWBTemplateMetaDecoders(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want []string
	}{
		{``, []string{}},
		{`null`, []string{}},
		{`"[\"include/general\",\"exclude/404\"]"`, []string{"include/general", "exclude/404"}}, // as written
		{`["include/archive"]`, []string{"include/archive"}},                                    // native array
		{`"not json"`, []string{}},
		{`[1,"include/404",null]`, []string{"include/404"}},
		{`{"a":1}`, []string{}},
	} {
		if got := wbConditionsMeta([]byte(c.raw)); !slices.Equal(got, c.want) || got == nil {
			t.Errorf("conditions %s = %#v, want %#v", c.raw, got, c.want)
		}
	}
	for _, c := range []struct {
		raw  string
		want int
	}{
		{``, 0}, {`null`, 0}, {`"7"`, 7}, {`-3`, -3}, {`" 12 "`, 12}, {`"500"`, 100}, {`-1e9`, -100},
		{`"abc"`, 0}, {`"NaN"`, 0}, {`2.9`, 2}, {`true`, 0},
	} {
		if got := wbPriorityMeta([]byte(c.raw)); got != c.want {
			t.Errorf("priority %s = %d, want %d", c.raw, got, c.want)
		}
	}
	if wbTemplateType("header") != webbuilder.TemplateHeader || wbTemplateType("gwe") != "" || wbTemplateType("") != "" {
		t.Error("wbTemplateType")
	}
	if got := likeEscape(`50%_a\b`); got != `50\%\_a\\b` {
		t.Errorf("likeEscape = %s", got)
	}
}

func TestWebBuilderTemplates(t *testing.T) {
	s, pageID, u1, u2 := wbTestStore(t)
	ctx := context.Background()
	wb := s.WebBuilder()

	// The migration registered the content type (posts.type references it) and the index.
	var public bool
	if err := s.Pool.QueryRow(ctx, `SELECT public FROM content_types WHERE slug = 'wb_template'`).Scan(&public); err != nil || public {
		t.Fatalf("content type: public=%v err=%v", public, err)
	}
	var idx int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'posts_wb_templates'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatalf("index: %d %v", idx, err)
	}
	// Idempotent: running the migration again changes nothing and does not fail.
	sql, err := os.ReadFile("../db/migrations/0029_web_builder_templates.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// Create: defaults for title, conditions, starter; meta stored as WordPress-style strings.
	if _, err := wb.CreateTemplate(ctx, NewWBTemplate{Type: "bogus"}); !errors.Is(err, ErrTemplateType) {
		t.Fatalf("unknown type: %v", err)
	}
	if _, err := wb.CreateTemplate(ctx, NewWBTemplate{Type: webbuilder.TemplateSingle}); !errors.Is(err, ErrTemplateType) {
		t.Fatalf("single without conditions: %v", err)
	}
	hdr, err := wb.CreateTemplate(ctx, NewWBTemplate{Type: webbuilder.TemplateHeader, AuthorID: u1})
	if err != nil {
		t.Fatal(err)
	}
	if hdr.ID == 0 || hdr.Title != "Header" || hdr.Status != "draft" || hdr.Starter != webbuilder.StarterHeader ||
		!slices.Equal(hdr.Conditions, []string{"include/general"}) || hdr.Priority != 0 || hdr.AuthorName == "" || hdr.CreatedAt.IsZero() {
		t.Fatalf("created header: %+v", hdr)
	}
	// The default conditions slice is not shared with the contract table.
	hdr.Conditions[0] = "mutated"
	if webbuilder.DefaultConditions[webbuilder.TemplateHeader][0] != "include/general" {
		t.Fatal("CreateTemplate aliased DefaultConditions")
	}
	p, err := s.GetPost(ctx, hdr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Type != webbuilder.TemplatePostType || p.Meta[webbuilder.MetaConditions] != `["include/general"]` ||
		p.Meta[webbuilder.MetaPriority] != "0" || p.Meta[webbuilder.MetaTemplateType] != "header" ||
		p.Meta[webbuilder.MetaStarter] != "header" || p.Meta[webbuilder.MetaPreview] != "" || p.CommentStatus != "closed" {
		t.Fatalf("stored post: %+v meta %v", p, p.Meta)
	}

	single, err := wb.CreateTemplate(ctx, NewWBTemplate{Type: webbuilder.TemplateSingle, Title: "  Books  ", AuthorID: u2,
		Conditions: []string{"include/singular/book"}, Priority: 250, Starter: webbuilder.StarterBlank, Preview: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	if single.Title != "Books" || single.Priority != 100 || single.Starter != webbuilder.StarterBlank || single.Preview != "latest" {
		t.Fatalf("created single: %+v", single)
	}
	ftr, err := wb.CreateTemplate(ctx, NewWBTemplate{Type: webbuilder.TemplateFooter, Title: "Site 100%_footer"})
	if err != nil {
		t.Fatal(err)
	}
	if ftr.AuthorID != 0 || ftr.AuthorName != "" {
		t.Fatalf("footer without author: %+v", ftr)
	}

	// Get.
	got, err := wb.Template(ctx, single.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != webbuilder.TemplateSingle || got.AuthorID != u2 || got.Priority != 100 ||
		!slices.Equal(got.Conditions, []string{"include/singular/book"}) || got.Preview != "latest" {
		t.Fatalf("Template: %+v", got)
	}
	if _, err := wb.Template(ctx, pageID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Template(page): %v", err)
	}
	if _, err := wb.Template(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Template(missing): %v", err)
	}

	// Conditions, priority, preview.
	if err := wb.SetTemplateConditions(ctx, hdr.ID, []string{"include/general", "exclude/singular/page/" + wbtID(pageID)}, -300); err != nil {
		t.Fatal(err)
	}
	if err := wb.SetTemplatePreview(ctx, hdr.ID, "post:"+wbtID(pageID)); err != nil {
		t.Fatal(err)
	}
	got, _ = wb.Template(ctx, hdr.ID)
	if got.Priority != -100 || len(got.Conditions) != 2 || got.Conditions[1] != "exclude/singular/page/"+wbtID(pageID) ||
		got.Preview != "post:"+wbtID(pageID) || got.Starter != webbuilder.StarterHeader {
		t.Fatalf("after setters: %+v", got)
	}
	if err := wb.SetTemplateConditions(ctx, hdr.ID, nil, 5); err != nil {
		t.Fatal(err)
	}
	got, _ = wb.Template(ctx, hdr.ID)
	if got.Conditions == nil || len(got.Conditions) != 0 || got.Priority != 5 {
		t.Fatalf("after clearing: %+v", got)
	}
	p, _ = s.GetPost(ctx, hdr.ID)
	if p.Meta[webbuilder.MetaConditions] != "[]" {
		t.Fatalf("meta after clearing: %v", p.Meta)
	}
	for _, err := range []error{
		wb.SetTemplateConditions(ctx, pageID, []string{"include/general"}, 0),
		wb.SetTemplatePreview(ctx, pageID, "front"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("setter on a page: %v", err)
		}
	}
	// A native jsonb array and number (an importer's or older writer's form) read the same.
	if _, err := s.Pool.Exec(ctx, `UPDATE posts SET meta = meta || '{"_wb_conditions":["include/404"],"_wb_priority":9}' WHERE id = $1`, ftr.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = wb.Template(ctx, ftr.ID)
	if !slices.Equal(got.Conditions, []string{"include/404"}) || got.Priority != 9 {
		t.Fatalf("native meta: %+v", got)
	}

	// List: newest first, type, status and search filters, trash excluded by default.
	if _, err := s.Pool.Exec(ctx, `UPDATE posts SET modified_at = now() - make_interval(mins => $2) WHERE id = $1`, hdr.ID, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE posts SET modified_at = now() - make_interval(mins => $2) WHERE id = $1`, single.ID, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE posts SET modified_at = now() - make_interval(mins => $2) WHERE id = $1`, ftr.ID, 10); err != nil {
		t.Fatal(err)
	}
	ids := func(f WBTemplateFilter) []int64 {
		t.Helper()
		list, err := wb.Templates(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, it := range list {
			out = append(out, it.ID)
		}
		return out
	}
	if got := ids(WBTemplateFilter{}); !slices.Equal(got, []int64{ftr.ID, single.ID, hdr.ID}) {
		t.Fatalf("all: %v", got)
	}
	if got := ids(WBTemplateFilter{Type: webbuilder.TemplateHeader}); !slices.Equal(got, []int64{hdr.ID}) {
		t.Fatalf("headers: %v", got)
	}
	if got := ids(WBTemplateFilter{Search: "100%_"}); !slices.Equal(got, []int64{ftr.ID}) {
		t.Fatalf("search: %v", got)
	}
	if got := ids(WBTemplateFilter{Search: "%"}); len(got) != 1 {
		t.Fatalf("a %% search must be literal: %v", got)
	}
	if got := ids(WBTemplateFilter{Limit: 1, Offset: 1}); !slices.Equal(got, []int64{single.ID}) {
		t.Fatalf("page 2: %v", got)
	}
	if err := s.TrashPost(ctx, single.ID); err != nil {
		t.Fatal(err)
	}
	if got := ids(WBTemplateFilter{}); !slices.Equal(got, []int64{ftr.ID, hdr.ID}) {
		t.Fatalf("without trash: %v", got)
	}
	if got := ids(WBTemplateFilter{Statuses: []string{"trash"}}); !slices.Equal(got, []int64{single.ID}) {
		t.Fatalf("trash: %v", got)
	}
	if got, err := wb.Template(ctx, single.ID); err != nil || got.Status != "trash" {
		t.Fatalf("trashed template: %+v %v", got, err)
	}
	// An auto-draft template never lists or counts.
	if _, err := s.CreateAutoDraft(ctx, webbuilder.TemplatePostType, u1); err != nil {
		t.Fatal(err)
	}
	if got := ids(WBTemplateFilter{}); len(got) != 2 {
		t.Fatalf("auto-draft listed: %v", got)
	}

	c, err := wb.TemplateCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c.All != 2 || c.Trash != 1 || c.ByType[webbuilder.TemplateHeader] != 1 || c.ByType[webbuilder.TemplateFooter] != 1 ||
		c.ByType[webbuilder.TemplateSingle] != 0 {
		t.Fatalf("counts: %+v", c)
	}

	// Index: published only, unknown types dropped, HasHTML from the doc.
	idxRows, err := wb.PublishedTemplates(ctx)
	if err != nil || len(idxRows) != 0 {
		t.Fatalf("index with drafts only: %v %v", idxRows, err)
	}
	for _, id := range []int64{hdr.ID, ftr.ID} {
		p, _ := s.GetPost(ctx, id)
		p.Status = "publish"
		if err := s.UpdatePost(ctx, p, u1); err != nil {
			t.Fatal(err)
		}
	}
	if err := wb.PublishDoc(ctx, &WebBuilderDoc{PostID: hdr.ID, Doc: json.RawMessage(`{"sections":[]}`), HTML: "<section class=\"sf\">h</section>"}, 0); err != nil {
		t.Fatal(err)
	}
	odd := &Post{Type: webbuilder.TemplatePostType, Title: "Odd", Status: "publish", Meta: map[string]any{webbuilder.MetaTemplateType: "gwe-header"}}
	if err := s.InsertPost(ctx, odd); err != nil {
		t.Fatal(err)
	}
	// A plain post with template meta is not a template.
	if err := s.InsertPost(ctx, &Post{Type: "post", Title: "Fake", Status: "publish", Meta: map[string]any{webbuilder.MetaTemplateType: "header"}}); err != nil {
		t.Fatal(err)
	}
	idxRows, err = wb.PublishedTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(idxRows) != 2 || idxRows[0].ID != hdr.ID || idxRows[1].ID != ftr.ID {
		t.Fatalf("index: %+v", idxRows)
	}
	h, f := idxRows[0], idxRows[1]
	if h.Type != webbuilder.TemplateHeader || !h.HasHTML || h.Priority != 5 || len(h.Conditions) != 0 || h.ModifiedAt.IsZero() {
		t.Fatalf("index header: %+v", h)
	}
	if f.Type != webbuilder.TemplateFooter || f.HasHTML || !slices.Equal(f.Conditions, []string{"include/404"}) || f.Priority != 9 {
		t.Fatalf("index footer: %+v", f)
	}
	// Publishing gave the template a slug; it stays a template (meta survives UpdatePost).
	if got, _ := wb.Template(ctx, hdr.ID); got.Status != "publish" || got.Type != webbuilder.TemplateHeader {
		t.Fatalf("published header: %+v", got)
	}

	// Inside a transaction.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in, err := WebBuilderIn(tx).CreateTemplate(ctx, NewWBTemplate{Type: webbuilder.Template404})
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if _, err := wb.Template(ctx, in.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled back template exists: %v", err)
	}
}

func wbtID(n int64) string { return strconv.FormatInt(n, 10) }
