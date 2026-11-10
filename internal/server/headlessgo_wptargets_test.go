package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestHGWPTargetHelpers(t *testing.T) {
	for target, want := range map[string]string{"wp:post": "post", "wp:page": "page", "wp:type:product": "product", "wp:type:my_cpt": "my_cpt"} {
		if got, ok := hgWPPostType(target); !ok || got != want {
			t.Errorf("hgWPPostType(%q) = %q, %v", target, got, ok)
		}
	}
	for _, bad := range []string{"wp:type:", "wp:type:A b", "wp:user", "wp:term:category", "wp:type:x'; DROP"} {
		if _, ok := hgWPPostType(bad); ok {
			t.Errorf("hgWPPostType(%q) accepted", bad)
		}
	}
	if tax, ok := hgWPTaxonomy("wp:term:product_cat"); !ok || tax != "product_cat" {
		t.Errorf("hgWPTaxonomy: %q %v", tax, ok)
	}
	if _, ok := hgWPTaxonomy("wp:term:"); ok {
		t.Error("empty taxonomy accepted")
	}
	for _, ok := range []string{"wp:post", "wp:page", "wp:user", "wp:type:product", "wp:term:category"} {
		if !hgWPTargetKnown(ok) {
			t.Errorf("%q not known", ok)
		}
	}
	if hgWPTargetKnown("wp:comment") || hgWPTargetKnown("wp:term:") {
		t.Error("unknown target accepted")
	}
	// The post condition follows the target's type, never an injected one.
	if c := hgWPPostCond("p", "wp:type:product"); !strings.Contains(c, "p.type = 'product'") {
		t.Errorf("cond: %s", c)
	}
}

func TestHGWPTargetSQL(t *testing.T) {
	reg := &hgRegistry{all: map[string]*hgSchema{}}
	sc := &hgSchema{UID: "shop", Kind: hgKindCollection, SingularName: "shop", PluralName: "shops", Fields: []hgField{
		{Name: "cat", Type: "relation", Relation: "manyToOne", Target: "wp:term:product_cat"},
		{Name: "item", Type: "relation", Relation: "manyToOne", Target: "wp:type:product"}}}
	reg.all[sc.UID] = sc
	reg.order = append(reg.order, sc)
	q := &hgQuery{reg: reg, sql: &hgSQL{}, status: "published", locale: "en"}
	order, err := q.orderBy(sc, []hgSortKey{{Field: "cat.name"}, {Field: "item.title"}}, "e")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(order, "FROM terms w") || !strings.Contains(order, ".taxonomy = $") || !strings.Contains(order, "FROM posts w") || !strings.Contains(order, ".type = 'product'") {
		t.Fatalf("order by: %s", order)
	}
}

// Relations to the terms of a taxonomy and to posts of another post type: saved, validated,
// populated, filtered, searched in the picker.
func TestHeadlessGoTermAndTypeRelations(t *testing.T) {
	base, c, srv := hgSite(t)
	ctx := context.Background()
	hgMustSchema(t, srv, &hgSchema{Kind: hgKindCollection, DisplayName: "Showcase", SingularName: "showcase", PluralName: "showcases", DraftAndPublish: true, Fields: []hgField{
		{Name: "title", Type: "text"},
		{Name: "cats", Type: "relation", Relation: "manyToMany", Target: "wp:term:category"},
		{Name: "landing", Type: "relation", Relation: "manyToOne", Target: "wp:type:page"}}})
	// An unregistered taxonomy or post type is refused.
	for _, target := range []string{"wp:term:nope", "wp:type:nope"} {
		err := srv.hgSaveSchema(ctx, &hgSchema{Kind: hgKindCollection, DisplayName: "Bad", SingularName: "bad", PluralName: "bads", Fields: []hgField{
			{Name: "x", Type: "relation", Relation: "manyToOne", Target: target}}})
		if err == nil {
			t.Errorf("%s accepted", target)
		}
	}
	term := &store.Term{Taxonomy: "category", Slug: "showroom", Name: "Showroom"}
	if err := srv.store.InsertTerm(ctx, term); err != nil {
		t.Fatal(err)
	}
	other := &store.Term{Taxonomy: "post_tag", Slug: "tagged", Name: "Tagged"}
	if err := srv.store.InsertTerm(ctx, other); err != nil {
		t.Fatal(err)
	}
	page := &store.Post{Type: "page", Title: "Landing page", Slug: "landing", Status: "publish", AuthorID: 1}
	if err := srv.store.InsertPost(ctx, page); err != nil {
		t.Fatal(err)
	}
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	if r := hgCall(t, base, "POST", "/showcases", full, map[string]any{"data": map[string]any{"title": "Bad", "cats": []any{other.ID}}}); r.Code != 400 {
		t.Errorf("a term of another taxonomy: %d %s", r.Code, r.Raw)
	}
	r := hgCall(t, base, "POST", "/showcases?status=published", full, map[string]any{"data": map[string]any{"title": "Good", "cats": []any{term.ID}, "landing": page.ID}})
	if r.Code != 201 {
		t.Fatalf("create: %d %s", r.Code, r.Raw)
	}
	doc := r.data()["documentId"].(string)
	r = hgCall(t, base, "GET", "/showcases/"+doc+"?populate=cats,landing", full, nil)
	cats, _ := r.data()["cats"].([]any)
	land, _ := r.data()["landing"].(map[string]any)
	if len(cats) != 1 || cats[0].(map[string]any)["slug"] != "showroom" || cats[0].(map[string]any)["taxonomy"] != "category" || land == nil || land["slug"] != "landing" {
		t.Errorf("populate: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/showcases?filters[cats][slug][$eq]=showroom", full, nil); len(r.list()) != 1 {
		t.Errorf("filter through a term relation: %s", r.Raw)
	}
	if r := hgCall(t, base, "GET", "/showcases?filters[cats][slug][$eq]=tagged", full, nil); len(r.list()) != 0 {
		t.Errorf("filter matched another term: %s", r.Raw)
	}
	_, body, _ := get(t, c, base+"/wp-admin/headlessgo/search?target=wp:term:category&q=Show")
	if !strings.Contains(body, "Showroom") || strings.Contains(body, "Tagged") {
		t.Errorf("term picker: %s", body)
	}
	_, body, _ = get(t, c, base+"/wp-admin/headlessgo/search?target=wp:type:page&q=Landing")
	if !strings.Contains(body, "Landing page") {
		t.Errorf("post type picker: %s", body)
	}
	gql := hgCall(t, base, "POST", "/graphql", full, map[string]any{"query": `{ showcases { title cats { slug } } }`})
	if !strings.Contains(gql.Raw, "showroom") {
		t.Errorf("graphql: %s", gql.Raw)
	}
}

// The admin write actions by role: contributors and authors against other users' entries and
// against their own, for save, publish, delete and bulk actions.
func TestHeadlessGoAdminRoleMatrix(t *testing.T) {
	base, admin, srv := hgSite(t)
	ctx := context.Background()
	hgMemo(t, srv, false)
	full := hgNewToken(t, srv, &hgToken{Name: "Full", Kind: "full-access"})
	clients := map[string]*http.Client{
		"contributor": hgSecondUser(t, srv, base, "carl", "contributor"),
		"author":      hgSecondUser(t, srv, base, "ann", "author"),
		"editor":      hgSecondUser(t, srv, base, "eddie", "editor"),
	}
	refused := func(l string) bool { return strings.Contains(l, "hg-error") }
	exists := func(doc string) bool { _, err := srv.hgGetEntry(ctx, "memo", doc, ""); return err == nil }
	published := func(doc string) bool {
		e, err := srv.hgGetEntry(ctx, "memo", doc, "")
		return err == nil && e.Status() == "published"
	}
	// Another user's entry (created through the API): the low roles may do nothing with it.
	for _, role := range []string{"contributor", "author"} {
		cl := clients[role]
		doc := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "Theirs"}}))
		for _, do := range []string{"save_entry", "publish_entry", "delete_entry"} {
			_, _, l := hgEntryForm(t, cl, base, "memo", doc, do, url.Values{"payload": {`{"title":"Mine now"}`}})
			if !refused(l) {
				t.Errorf("%s: %s on another user's entry allowed: %s", role, do, l)
			}
		}
		_, _, _ = hgEntryForm(t, cl, base, "memo", doc, "bulk_entries", url.Values{"bulk_action": {"publish"}, "docs[]": {doc}})
		_, _, _ = hgEntryForm(t, cl, base, "memo", doc, "bulk_entries", url.Values{"bulk_action": {"delete"}, "docs[]": {doc}})
		if !exists(doc) || published(doc) {
			t.Errorf("%s changed another user's entry through a bulk action", role)
		}
	}
	// The editor may publish and delete other users' entries.
	doc := hgDocOf(t, hgCall(t, base, "POST", "/memos", full, map[string]any{"data": map[string]any{"title": "For the editor"}}))
	if _, _, l := hgEntryForm(t, clients["editor"], base, "memo", doc, "publish_entry", nil); refused(l) || !published(doc) {
		t.Errorf("editor publish: %s", l)
	}
	if _, _, l := hgEntryForm(t, clients["editor"], base, "memo", doc, "delete_entry", nil); refused(l) || exists(doc) {
		t.Errorf("editor delete: %s", l)
	}
	// An entry the user created: a contributor may save and delete it, but never publish it;
	// an author may publish it too.
	for role, canPublish := range map[string]bool{"contributor": false, "author": true} {
		cl := clients[role]
		if _, _, l := hgEntryForm(t, cl, base, "memo", "", "save_entry", url.Values{"payload": {`{"title":"Own ` + role + `"}`}}); refused(l) {
			t.Fatalf("%s create: %s", role, l)
		}
		var own string
		if err := srv.store.Pool.QueryRow(ctx, `SELECT e.document_id FROM hg_entries e JOIN users u ON u.id = e.created_by WHERE u.login = $1 AND e.type_uid = 'memo' LIMIT 1`,
			map[string]string{"contributor": "carl", "author": "ann"}[role]).Scan(&own); err != nil {
			t.Fatalf("%s: no own entry: %v", role, err)
		}
		_, _, l := hgEntryForm(t, cl, base, "memo", own, "publish_entry", nil)
		if refused(l) == canPublish || published(own) != canPublish {
			t.Errorf("%s publish own entry: refused=%v published=%v (%s)", role, refused(l), published(own), l)
		}
		if _, _, l := hgEntryForm(t, cl, base, "memo", own, "delete_entry", nil); refused(l) || exists(own) {
			t.Errorf("%s delete own entry: %s", role, l)
		}
	}
	_ = admin
}

// The public menu endpoint needs no token (it serves published navigation only); the draft
// items stay out and a write method is refused.
func TestHeadlessGoMenusPublicAccess(t *testing.T) {
	base, _, _ := hgSite(t)
	if r := hgCall(t, base, "GET", "/wp/menus", "", nil); r.Code != http.StatusOK {
		t.Errorf("menus without a token: %d %s", r.Code, r.Raw)
	}
	if r := hgCall(t, base, "POST", "/wp/menus", "", map[string]any{}); r.Code < 400 {
		t.Errorf("menus write without a token: %d", r.Code)
	}
}
