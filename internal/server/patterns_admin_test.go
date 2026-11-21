package server

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// patternCategory creates a wp_pattern_category term as rc and returns its id.
func patternCategory(t *testing.T, rc *restClient, name string) float64 {
	t.Helper()
	code, out, _, _ := rc.do("POST", "/wp/v2/wp_pattern_category", map[string]any{"name": name})
	if code != http.StatusCreated || out["taxonomy"] != "wp_pattern_category" {
		t.Fatalf("create pattern category %q: %d %v", name, code, out)
	}
	return out["id"].(float64)
}

// The wp_pattern_category taxonomy: REST CRUD with WordPress's capabilities (a flat taxonomy, so
// edit_posts may create terms; editing and deleting need manage_categories) and the taxonomy object.
func TestRESTPatternCategoryCRUD(t *testing.T) {
	s := secStart(t)
	anon := s.anon()
	_, sub := s.user("subby", "subscriber")
	_, contrib := s.user("conny", "contributor")

	if code, _, list, h := anon.do("GET", "/wp/v2/wp_pattern_category", nil); code != http.StatusOK || len(list) != 0 || h.Get("X-WP-Total") != "0" {
		t.Errorf("empty list: %d %v %q", code, list, h.Get("X-WP-Total"))
	}
	// The taxonomy is listed (core-data builds the taxonomy entity from it) and belongs to wp_block.
	code, tax, _, _ := s.admin.do("GET", "/wp/v2/taxonomies/wp_pattern_category?context=edit", nil)
	if code != http.StatusOK || tax["rest_base"] != "wp_pattern_category" || tax["hierarchical"] != false {
		t.Fatalf("taxonomy object: %d %v", code, tax)
	}
	if caps, _ := tax["capabilities"].(map[string]any); caps["assign_terms"] != "edit_posts" || caps["manage_terms"] != "manage_categories" {
		t.Errorf("taxonomy caps: %v", tax["capabilities"])
	}
	if vis, _ := tax["visibility"].(map[string]any); vis["public"] != false || vis["show_ui"] != true {
		t.Errorf("taxonomy visibility: %v", tax["visibility"])
	}
	if _, all, _, _ := anon.do("GET", "/wp/v2/taxonomies", nil); all["wp_pattern_category"] == nil || all["category"] == nil {
		t.Errorf("taxonomies list: %v", all)
	}
	if _, byType, _, _ := anon.do("GET", "/wp/v2/taxonomies?type=wp_block", nil); len(byType) != 1 || byType["wp_pattern_category"] == nil {
		t.Errorf("taxonomies of wp_block: %v", byType)
	}
	if _, byType, _, _ := anon.do("GET", "/wp/v2/taxonomies?type=post", nil); byType["wp_pattern_category"] != nil || byType["category"] == nil {
		t.Errorf("taxonomies of post: %v", byType)
	}

	// Create: subscribers cannot, contributors can (assign_terms on a flat taxonomy).
	if code, e, _, _ := sub.do("POST", "/wp/v2/wp_pattern_category", map[string]any{"name": "Nope"}); code != http.StatusForbidden || e["code"] != "rest_cannot_create" {
		t.Errorf("subscriber create: %d %v", code, e)
	}
	if code, e, _, _ := anon.do("POST", "/wp/v2/wp_pattern_category", map[string]any{"name": "Nope"}); code < 400 {
		t.Errorf("anonymous create: %d %v", code, e)
	}
	heroes := patternCategory(t, contrib, "Heroes")
	// The editor's category picker relies on term_exists carrying the existing term's id.
	if code, e, _, _ := contrib.do("POST", "/wp/v2/wp_pattern_category", map[string]any{"name": "heroes"}); code != http.StatusBadRequest || e["code"] != "term_exists" ||
		e["data"].(map[string]any)["term_id"] != heroes {
		t.Errorf("duplicate: %d %v", code, e)
	}
	id := itoa64(int64(heroes))
	code, got, _, _ := anon.do("GET", "/wp/v2/wp_pattern_category/"+id, nil)
	if code != http.StatusOK || got["name"] != "Heroes" || got["slug"] != "heroes" || got["count"] != float64(0) {
		t.Errorf("get: %d %v", code, got)
	}
	if !strings.Contains(got["link"].(string), "?taxonomy=wp_pattern_category&term=heroes") {
		t.Errorf("link of a non-public taxonomy term: %v", got["link"])
	}
	if links, _ := got["_links"].(map[string]any); links["wp:post_type"] == nil {
		t.Errorf("links: %v", got["_links"])
	}
	// A pattern category is not a category.
	if code, _, _, _ := s.admin.do("GET", "/wp/v2/categories/"+id, nil); code != http.StatusNotFound {
		t.Errorf("pattern category through /categories: %d", code)
	}

	// Update and delete need manage_categories (edit_terms / delete_terms).
	if code, e, _, _ := contrib.do("POST", "/wp/v2/wp_pattern_category/"+id, map[string]any{"name": "Renamed"}); code != http.StatusForbidden || e["code"] != "rest_cannot_update" {
		t.Errorf("contributor update: %d %v", code, e)
	}
	if code, e, _, _ := contrib.do("DELETE", "/wp/v2/wp_pattern_category/"+id+"?force=true", nil); code != http.StatusForbidden || e["code"] != "rest_cannot_delete" {
		t.Errorf("contributor delete: %d %v", code, e)
	}
	if code, out, _, _ := s.admin.do("POST", "/wp/v2/wp_pattern_category/"+id, map[string]any{"name": "Hero Sections", "description": "Big banners"}); code != http.StatusOK ||
		out["name"] != "Hero Sections" || out["description"] != "Big banners" {
		t.Errorf("admin update: %d %v", code, out)
	}
	if code, _, list, _ := anon.do("GET", "/wp/v2/wp_pattern_category?search=hero", nil); code != http.StatusOK || len(list) != 1 {
		t.Errorf("search: %d %v", code, list)
	}
	if code, out, _, _ := s.admin.do("DELETE", "/wp/v2/wp_pattern_category/"+id+"?force=true", nil); code != http.StatusOK || out["deleted"] != true {
		t.Errorf("admin delete: %d %v", code, out)
	}
	if code, _, _, _ := anon.do("GET", "/wp/v2/wp_pattern_category/"+id, nil); code != http.StatusNotFound {
		t.Errorf("deleted term still served: %d", code)
	}
}

// Patterns (/wp/v2/blocks) carry their categories in wp_pattern_category and their sync status in
// the wp_pattern_sync_status field (written as the field or as meta, as the editor's "Create pattern" sends it).
func TestRESTBlocksPatternCategories(t *testing.T) {
	s := secStart(t)
	_, contrib := s.user("conny", "contributor")
	heroes := patternCategory(t, s.admin, "Heroes")
	footers := patternCategory(t, s.admin, "Footers")

	// What the patterns package's createPattern() sends.
	code, made, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Big hero", "content": "<!-- wp:paragraph --><p>Hi</p><!-- /wp:paragraph -->",
		"status": "publish", "meta": map[string]any{"wp_pattern_sync_status": "unsynced"}, "wp_pattern_category": []any{heroes, footers}})
	if code != http.StatusCreated {
		t.Fatalf("create pattern: %d %v", code, made)
	}
	if _, inMeta := made["meta"].(map[string]any)["wp_pattern_sync_status"]; made["wp_pattern_sync_status"] != "unsynced" || inMeta {
		t.Errorf("sync status: %v %v", made["wp_pattern_sync_status"], made["meta"])
	}
	if cats, _ := made["wp_pattern_category"].([]any); len(cats) != 2 {
		t.Errorf("categories: %v", made["wp_pattern_category"])
	}
	links, _ := made["_links"].(map[string]any)
	for _, rel := range []string{"wp:term", "wp:action-assign-wp_pattern_category", "wp:action-create-wp_pattern_category"} {
		if links[rel] == nil {
			t.Errorf("pattern links miss %s: %v", rel, links)
		}
	}
	pid := itoa64(int64(made["id"].(float64)))
	code, synced, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Plain synced", "content": "<p>s</p>", "status": "publish"})
	if code != http.StatusCreated || synced["wp_pattern_sync_status"] != "" || len(synced["wp_pattern_category"].([]any)) != 0 {
		t.Fatalf("synced pattern: %d %v", code, synced)
	}

	// The term counts and the terms of the pattern (the editor's wp:term link).
	if _, got, _, _ := s.admin.do("GET", "/wp/v2/wp_pattern_category/"+itoa64(int64(heroes)), nil); got["count"] != float64(1) {
		t.Errorf("term count: %v", got["count"])
	}
	if code, _, list, _ := contrib.do("GET", "/wp/v2/wp_pattern_category?post="+pid, nil); code != http.StatusOK || len(list) != 2 {
		t.Errorf("terms of the pattern: %d %v", code, list)
	}
	if code, _, _, _ := s.anon().do("GET", "/wp/v2/wp_pattern_category?post="+pid, nil); code != http.StatusForbidden && code != http.StatusUnauthorized {
		t.Errorf("anonymous terms of a pattern: %d", code)
	}
	// The collection filter.
	if code, _, list, _ := s.admin.do("GET", "/wp/v2/blocks?wp_pattern_category="+itoa64(int64(heroes)), nil); code != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["id"] != made["id"] {
		t.Errorf("blocks by category: %d %v", code, list)
	}
	if code, _, list, _ := s.admin.do("GET", "/wp/v2/blocks?wp_pattern_category_exclude="+itoa64(int64(heroes)), nil); code != http.StatusOK || len(list) != 1 || list[0].(map[string]any)["id"] != synced["id"] {
		t.Errorf("blocks excluding a category: %d %v", code, list)
	}
	// Embedded terms.
	if code, got, _, _ := s.admin.do("GET", "/wp/v2/blocks/"+pid+"?_embed=1", nil); code != http.StatusOK {
		t.Errorf("embed: %d", code)
	} else if terms, _ := got["_embedded"].(map[string]any)["wp:term"].([]any); len(terms) != 1 || len(terms[0].([]any)) != 2 {
		t.Errorf("embedded terms: %v", got["_embedded"])
	}

	// Updating: one category, back to synced (top-level field); an unknown status is refused.
	code, upd, _, _ := s.admin.do("POST", "/wp/v2/blocks/"+pid, map[string]any{"wp_pattern_category": []any{footers}, "wp_pattern_sync_status": ""})
	if code != http.StatusOK || upd["wp_pattern_sync_status"] != "" || len(upd["wp_pattern_category"].([]any)) != 1 || upd["wp_pattern_category"].([]any)[0] != footers {
		t.Errorf("update: %d %v %v", code, upd["wp_pattern_sync_status"], upd["wp_pattern_category"])
	}
	for _, bad := range []any{"sometimes", ""} {
		code, e, _, _ := s.admin.do("POST", "/wp/v2/blocks/"+pid, map[string]any{"meta": map[string]any{"wp_pattern_sync_status": bad}})
		params, _ := e["data"].(map[string]any)["params"].(map[string]any)
		if code != http.StatusBadRequest || e["code"] != "rest_invalid_param" || params["meta"] == nil {
			t.Errorf("invalid sync status %q: %d %v", bad, code, e)
		}
	}
	// meta.wp_pattern_sync_status: null deletes the meta, so the pattern is synced again.
	if code, got, _, _ := s.admin.do("POST", "/wp/v2/blocks/"+pid, map[string]any{"meta": map[string]any{"wp_pattern_sync_status": "partial"}}); code != http.StatusOK || got["wp_pattern_sync_status"] != "partial" {
		t.Errorf("partial: %d %v", code, got["wp_pattern_sync_status"])
	}
	if code, got, _, _ := s.admin.do("POST", "/wp/v2/blocks/"+pid, map[string]any{"meta": map[string]any{"wp_pattern_sync_status": nil}}); code != http.StatusOK || got["wp_pattern_sync_status"] != "" {
		t.Errorf("null sync status: %d %v", code, got["wp_pattern_sync_status"])
	}
	if _, got, _, _ := s.admin.do("GET", "/wp/v2/wp_pattern_category/"+itoa64(int64(heroes)), nil); got["count"] != float64(0) {
		t.Errorf("term count after removal: %v", got["count"])
	}
	// A contributor creates a pattern with a category (assign_terms is edit_posts); it stays a draft.
	code, own, _, _ := contrib.do("POST", "/wp/v2/blocks", map[string]any{"title": "Mine", "content": "<p>m</p>", "status": "publish", "wp_pattern_category": []any{heroes}})
	if code != http.StatusCreated || own["status"] == "publish" || len(own["wp_pattern_category"].([]any)) != 1 {
		t.Errorf("contributor pattern: %d %v %v", code, own["status"], own["wp_pattern_category"])
	}
	// Deleting a category unassigns it.
	if code, _, _, _ := s.admin.do("DELETE", "/wp/v2/wp_pattern_category/"+itoa64(int64(footers))+"?force=true", nil); code != http.StatusOK {
		t.Fatalf("delete category: %d", code)
	}
	if _, got, _, _ := s.admin.do("GET", "/wp/v2/blocks/"+pid+"?context=edit", nil); len(got["wp_pattern_category"].([]any)) != 0 {
		t.Errorf("deleted category still assigned: %v", got["wp_pattern_category"])
	}
}

// The Patterns list (edit.php?post_type=wp_block): columns, row actions, filters and caps; editing a
// pattern opens the block editor on it.
func TestAdminPatternsList(t *testing.T) {
	s := secStart(t)
	subC, _ := s.user("subby", "subscriber")
	contribC, _ := s.user("conny", "contributor")
	heroes := patternCategory(t, s.admin, "Heroes")
	_, unsynced, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Unsynced hero", "content": "<p>u</p>", "status": "publish",
		"meta": map[string]any{"wp_pattern_sync_status": "unsynced"}, "wp_pattern_category": []any{heroes}})
	_, synced, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Synced footer", "content": "<p>s</p>", "status": "publish"})
	uid, sid := itoa64(int64(unsynced["id"].(float64))), itoa64(int64(synced["id"].(float64)))
	c := s.admin.c

	// The editor's links use edit.php; the .php URL redirects to the list.
	if code, _, loc := get(t, c, s.base+"/wp-admin/edit.php?post_type=wp_block"); code != http.StatusMovedPermanently || loc != "/wp-admin/edit?post_type=wp_block" {
		t.Errorf("edit.php?post_type=wp_block: %d %q", code, loc)
	}
	code, body, _ := get(t, c, s.base+"/wp-admin/edit?post_type=wp_block")
	if code != http.StatusOK {
		t.Fatalf("patterns list: %d %s", code, body)
	}
	for _, want := range []string{
		`<h1 class="wp-heading-inline">Patterns</h1>`, `href="/wp-admin/post-new?post_type=wp_block" class="page-title-action"`,
		"Unsynced hero", "Synced footer", ">Not synced</td>", ">Synced</td>", "Pattern Categories",
		`href="/wp-admin/edit?post_type=wp_block&amp;wp_pattern_category=heroes">Heroes</a>`,
		`class="wp-list-reusable-blocks__export button-link" data-id="` + uid + `"`,
		`href="/wp-admin/post?post=` + uid + `&amp;action=edit"`,
		"list-reusable-blocks.min.js", "list-reusable-blocks/style.min.css",
		`<a href="/wp-admin/edit?post_type=wp_block" class=" current" aria-current="page">Patterns</a>`, // Appearance › Patterns
	} {
		if !strings.Contains(body, want) {
			t.Errorf("patterns list misses %q", want)
		}
	}
	// Filter by pattern category.
	_, body, _ = get(t, c, s.base+"/wp-admin/edit?post_type=wp_block&wp_pattern_category=heroes")
	if !strings.Contains(body, "Unsynced hero") || strings.Contains(body, "Synced footer") {
		t.Error("category filter")
	}
	// Subscribers cannot see the list; contributors can (edit_posts).
	if code, _, _ := get(t, subC, s.base+"/wp-admin/edit?post_type=wp_block"); code != http.StatusForbidden {
		t.Errorf("subscriber list: %d", code)
	}
	if code, body, _ := get(t, contribC, s.base+"/wp-admin/edit?post_type=wp_block"); code != http.StatusOK || !strings.Contains(body, "Synced footer") ||
		strings.Contains(body, `<span class="trash"><a href="/wp-admin/post?post=`+sid) {
		t.Errorf("contributor list: %d (trash link on another user's pattern: %v)", code, strings.Contains(body, "action=trash"))
	}

	// Editing a pattern opens the block editor on the wp_block post; a new pattern too.
	code, body, _ = get(t, c, s.base+"/wp-admin/post?post="+uid+"&action=edit")
	if code != http.StatusOK || !strings.Contains(body, `wp.editPost.initializeEditor( 'editor', "wp_block", `+uid) || !strings.Contains(body, "Edit Pattern") {
		t.Errorf("edit pattern: %d", code)
	}
	code, body, _ = get(t, c, s.base+"/wp-admin/post-new?post_type=wp_block")
	if code != http.StatusOK || !strings.Contains(body, `wp.editPost.initializeEditor( 'editor', "wp_block", `) {
		t.Errorf("new pattern: %d", code)
	}
	if code, _, _ := get(t, subC, s.base+"/wp-admin/post-new?post_type=wp_block"); code != http.StatusForbidden {
		t.Errorf("subscriber new pattern: %d", code)
	}

	// Bulk trash, then the Trash view.
	_, body, _ = get(t, c, s.base+"/wp-admin/edit?post_type=wp_block")
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if nonce == nil {
		t.Fatal("no bulk nonce")
	}
	q := url.Values{"post_type": {"wp_block"}, "action": {"trash"}, "bulk_action": {"Apply"}, "post[]": {sid}, "_wpnonce": {nonce[1]}}
	code, _, loc := get(t, c, s.base+"/wp-admin/edit?"+q.Encode())
	if code != http.StatusFound || !strings.HasPrefix(loc, "/wp-admin/edit?post_type=wp_block&trashed=1") {
		t.Fatalf("bulk trash: %d %q", code, loc)
	}
	if p, _ := s.srv.store.GetPost(context.Background(), int64(synced["id"].(float64))); p.Status != "trash" {
		t.Errorf("trashed pattern status: %s", p.Status)
	}
	_, body, _ = get(t, c, s.base+loc)
	if !strings.Contains(body, "1 pattern moved to the Trash.") {
		t.Error("trash notice")
	}
	_, body, _ = get(t, c, s.base+"/wp-admin/edit?post_type=wp_block&post_status=trash")
	if !strings.Contains(body, "Synced footer") || !strings.Contains(body, "Delete Permanently") || strings.Contains(body, "Unsynced hero") {
		t.Error("trash view")
	}

	// The pattern categories screen (show_ui): no View links, the count links to the Patterns list.
	code, body, _ = get(t, c, s.base+"/wp-admin/edit-tags?taxonomy=wp_pattern_category&post_type=wp_block")
	if code != http.StatusOK || !strings.Contains(body, "Heroes") || !strings.Contains(body, `href="/wp-admin/edit?post_type=wp_block&amp;wp_pattern_category=heroes"`) ||
		strings.Contains(body, "taxonomy=wp_pattern_category&amp;term=") {
		t.Errorf("pattern categories screen: %d", code)
	}
}

// Pattern categories travel with their patterns through WXR export and import.
func TestWXRPatternCategories(t *testing.T) {
	s := secStart(t)
	ctx := context.Background()
	heroes := patternCategory(t, s.admin, "Heroes")
	if code, out, _, _ := s.admin.do("POST", "/wp/v2/blocks", map[string]any{"title": "Exported hero", "content": "<p>x</p>", "status": "publish", "wp_pattern_category": []any{heroes}}); code != http.StatusCreated {
		t.Fatalf("pattern: %d %v", code, out)
	}
	c := s.admin.c
	code, xml, _ := get(t, c, s.base+"/wp-admin/export?_wpnonce="+exportNonce(t, c, s.base)+"&download=true&content=all")
	if code != http.StatusOK {
		t.Fatalf("export: %d", code)
	}
	for _, want := range []string{
		`<wp:term_taxonomy><![CDATA[wp_pattern_category]]></wp:term_taxonomy><wp:term_slug><![CDATA[heroes]]></wp:term_slug>`,
		`<category domain="wp_pattern_category" nicename="heroes"><![CDATA[Heroes]]></category>`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("export misses %q", want)
		}
	}
	// Import into an emptied site: the term is recreated and assigned.
	if _, err := s.srv.store.Pool.Exec(ctx, `DELETE FROM posts WHERE type = 'wp_block'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.store.Pool.Exec(ctx, `DELETE FROM terms WHERE taxonomy = 'wp_pattern_category'`); err != nil {
		t.Fatal(err)
	}
	u, err := s.srv.store.UserByLoginOrEmail(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.importWXRWith(ctx, u, []byte(xml), wxrImportOptions{}); err != nil {
		t.Fatal(err)
	}
	_, _, list, _ := s.admin.do("GET", "/wp/v2/blocks?search=Exported", nil)
	if len(list) != 1 {
		t.Fatalf("imported pattern: %v", list)
	}
	cats, _ := list[0].(map[string]any)["wp_pattern_category"].([]any)
	if len(cats) != 1 {
		t.Fatalf("imported pattern categories: %v", cats)
	}
	if _, got, _, _ := s.admin.do("GET", "/wp/v2/wp_pattern_category/"+itoa64(int64(cats[0].(float64))), nil); got["slug"] != "heroes" {
		t.Errorf("imported term: %v", got)
	}
}
