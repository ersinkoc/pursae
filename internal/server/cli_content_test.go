package server

import (
	"bytes"
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"wpgo/internal/config"
	"wpgo/internal/store"
)

func TestCLIContentMergeTermIDs(t *testing.T) {
	cases := []struct {
		verb     string
		cur, ids []int64
		all      bool
		want     []int64
	}{
		{"set", []int64{1, 2}, []int64{3, 3, 4}, false, []int64{3, 4}},
		{"add", []int64{1, 2}, []int64{2, 5}, false, []int64{1, 2, 5}},
		{"remove", []int64{1, 2, 3}, []int64{2}, false, []int64{1, 3}},
		{"remove", []int64{1, 2, 3}, nil, true, []int64{}},
		{"set", []int64{1}, nil, false, []int64{}},
	}
	for _, tc := range cases {
		if got := contentMergeTermIDs(tc.verb, tc.cur, tc.ids, tc.all); !slices.Equal(got, tc.want) {
			t.Errorf("%s %v %v: %v, want %v", tc.verb, tc.cur, tc.ids, got, tc.want)
		}
	}
}

func TestCLIContentCommentStatus(t *testing.T) {
	for in, want := range map[string]string{"1": "approved", "approve": "approved", "0": "pending", "hold": "pending",
		"spam": "spam", "TRASH": "trash", "": "all", "all": "all"} {
		if got, ok := contentCommentStatus(in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	if _, ok := contentCommentStatus("bogus"); ok {
		t.Error("bogus accepted")
	}
	if contentCommentApproved("approved") != "1" || contentCommentApproved("pending") != "0" || contentCommentApproved("spam") != "spam" {
		t.Error("comment_approved mapping")
	}
	row := contentCommentCountRow(map[string]int{"approved": 3, "pending": 2, "spam": 1})
	if row["all"] != 5 || row["total_comments"] != 6 || row["moderated"] != 2 || row["trash"] != 0 {
		t.Errorf("count row: %v", row)
	}
}

func TestCLIContentHelpers(t *testing.T) {
	if v, err := contentParseMetaValue("42", "plaintext"); err != nil || v != "42" {
		t.Errorf("plaintext: %v %v", v, err)
	}
	if v, err := contentParseMetaValue(`{"a":[1,true]}`, "json"); err != nil {
		t.Error(err)
	} else if m, ok := v.(map[string]any); !ok || len(m["a"].([]any)) != 2 {
		t.Errorf("json: %#v", v)
	}
	if _, err := contentParseMetaValue("{", "json"); err == nil {
		t.Error("bad JSON accepted")
	}
	if _, err := contentParseMetaInput(`[1]`); err == nil {
		t.Error("meta_input array accepted")
	}
	if m, err := contentParseMetaInput(`{"k":"v"}`); err != nil || m["k"] != "v" {
		t.Errorf("meta_input: %v %v", m, err)
	}
	loc := time.FixedZone("X", 3*3600)
	for in, want := range map[string]string{
		"2024-05-06":                "2024-05-06T00:00:00+03:00",
		"2024-05-06 07:08":          "2024-05-06T07:08:00+03:00",
		"2024-05-06 07:08:09":       "2024-05-06T07:08:09+03:00",
		"2024-05-06T07:08:09Z":      "2024-05-06T07:08:09Z",
		"2024-05-06T07:08:09+01:00": "2024-05-06T07:08:09+01:00",
	} {
		got, ok := contentParsePostDate(in, loc)
		if !ok || got.Format(time.RFC3339) != want {
			t.Errorf("%q: %v %v, want %s", in, got, ok, want)
		}
	}
	if _, ok := contentParsePostDate("yesterday", loc); ok {
		t.Error("bad date accepted")
	}
	if types, err := contentPostTypes("any"); err != nil || types != nil {
		t.Errorf("any: %v %v", types, err)
	}
	if _, err := contentPostTypes("post,attachment"); err == nil {
		t.Error("attachment accepted")
	}
	if st := contentPostStatuses("publish,draft"); !slices.Equal(st, []string{"publish", "draft"}) {
		t.Errorf("statuses: %v", st)
	}
	if contentPostStatuses("any") != nil {
		t.Error("any status")
	}
	if got := contentPage([]int{1, 2, 3, 4, 5}, 2, 2); !slices.Equal(got, []int{3, 4}) {
		t.Errorf("page: %v", got)
	}
	if got := contentPage([]int{1, 2, 3}, 2, 5); len(got) != 0 {
		t.Errorf("page past end: %v", got)
	}
	if got := contentPage([]int{1, 2, 3}, 0, 9); len(got) != 3 {
		t.Errorf("all: %v", got)
	}
	if contentTermBy("12", "") != "id" || contentTermBy("news", "") != "auto" || contentTermBy("12", "slug") != "slug" {
		t.Error("term by")
	}
	if got := contentDecodeObjectTypes([]byte(`["post","page"]`)); !slices.Equal(got, []string{"post", "page"}) {
		t.Errorf("object types: %v", got)
	}
	if got := contentDecodeObjectTypes([]byte(`{}`)); got == nil || len(got) != 0 {
		t.Errorf("object types of an object: %v", got)
	}
}

func TestCLIContentHelp(t *testing.T) {
	r, closeEnv := NewCLI(config.Config{}, "pursae", nil)
	defer closeEnv()
	for _, p := range []string{"post list", "post get", "post exists", "post create", "post update", "post delete", "post untrash",
		"post empty-trash", "post duplicate", "post meta list", "post meta get", "post meta add", "post meta update", "post meta delete",
		"post term list", "post term set", "post term add", "post term remove", "post revision list", "post revision restore",
		"post revision prune", "post-type list", "taxonomy list", "term list", "term get", "term create", "term update", "term delete",
		"term recount", "comment list", "comment get", "comment count", "comment create", "comment update", "comment approve",
		"comment unapprove", "comment spam", "comment unspam", "comment trash", "comment untrash", "comment delete", "comment recount"} {
		if r.Lookup(p) == nil {
			t.Errorf("%s is not registered", p)
		}
	}
	var out, errb bytes.Buffer
	if code := CLI(context.Background(), config.Config{}, "pursae", []string{"help", "post", "create"}, strings.NewReader(""), &out, &errb); code != 0 ||
		!strings.Contains(out.String(), "--post_title") {
		t.Errorf("help post create: %d %q %q", code, out.String(), errb.String())
	}
	errb.Reset()
	if code := CLI(context.Background(), config.Config{}, "pursae", []string{"post", "get"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Errorf("missing argument: exit %d", code)
	}
	if code := CLI(context.Background(), config.Config{}, "pursae", []string{"post", "list", "--post_type=attachment"}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Error("attachment list accepted")
	}
}

// TestCLIContentIntegration runs the content commands against a fresh database (WPGO_TEST_DB).
func TestCLIContentIntegration(t *testing.T) {
	_, _, srv := newTestServerSetup(t, false, "")
	cliTestInstalled(t, srv)
	cfg := srv.cfg
	cfg.DatabaseURL = os.Getenv("WPGO_TEST_DB")
	ctx := context.Background()
	if _, err := srv.store.CreateUser(ctx, store.NewUser{Login: "admin", Email: "admin@example.com", PasswordHash: "x", Role: "administrator"}); err != nil {
		t.Fatal(err)
	}
	run := func(want int, stdin string, args ...string) string {
		t.Helper()
		var out, errb bytes.Buffer
		code := CLI(ctx, cfg, "pursae", args, strings.NewReader(stdin), &out, &errb)
		if code != want {
			t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out.String(), errb.String())
		}
		return strings.TrimSpace(out.String())
	}
	st := srv.store

	// Posts.
	id := run(0, "", "post", "create", "--post_title=Hello CLI", "--post_status=publish", "--tags_input=go, cms", `--meta_input={"color":"blue"}`, "--porcelain")
	pid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		t.Fatalf("porcelain id %q", id)
	}
	p, err := st.GetPost(ctx, pid)
	if err != nil || p.Status != "publish" || p.Slug != "hello-cli" || p.Meta["color"] != "blue" {
		t.Fatalf("created post: %+v %v", p, err)
	}
	if got := run(0, "", "post", "get", id, "--field=categories"); got != "uncategorized" {
		t.Errorf("default category: %q", got)
	}
	if got := run(0, "", "post", "get", id, "--field=tags"); got != "cms,go" {
		t.Errorf("tags: %q", got)
	}
	page := run(0, "<p>About</p>", "post", "create", "-", "--post_type=page", "--post_title=About", "--porcelain")
	if got := run(0, "", "post", "get", page, "--field=post_content"); got != "<p>About</p>" {
		t.Errorf("content from stdin: %q", got)
	}
	run(1, "", "post", "create", "--post_type=nope", "--post_title=x")
	if got := run(0, "", "post", "list", "--post_type=any", "--format=count"); got != "2" {
		t.Errorf("count: %q", got)
	}
	if got := run(0, "", "post", "list", "--post_type=page", "--format=ids"); got != page {
		t.Errorf("ids: %q", got)
	}
	if got := run(0, "", "post", "list", "--tag=go", "--fields=ID,post_title", "--format=csv"); got != "ID,post_title\n"+id+",Hello CLI" {
		t.Errorf("by tag: %q", got)
	}
	run(0, "", "post", "exists", id)
	run(1, "", "post", "exists", "999999")

	run(0, "", "post", "update", id, "--post_title=Hello again", "--post_category=uncategorized")
	if revs, _ := st.ListRevisions(ctx, pid, false); len(revs) != 1 {
		t.Errorf("revisions after update: %d", len(revs))
	}
	run(2, "", "post", "update", id)
	// One stdin body for several posts: read once, every post gets it.
	run(0, "<p>Shared</p>", "post", "update", id, page, "-")
	for _, p := range []string{id, page} {
		if got := run(0, "", "post", "get", p, "--field=post_content"); got != "<p>Shared</p>" {
			t.Errorf("post %s content from shared stdin: %q", p, got)
		}
	}

	// Meta.
	run(0, "", "post", "meta", "update", id, "layout", `{"w":2}`, "--format=json")
	if got := run(0, "", "post", "meta", "get", id, "layout"); got != `{"w":2}` {
		t.Errorf("meta get: %q", got)
	}
	run(1, "", "post", "meta", "add", id, "color", "red")
	run(1, "", "post", "meta", "get", id, "missing")
	run(1, "", "post", "meta", "delete", id, "color") // needs --yes
	run(0, "", "post", "meta", "delete", id, "color", "--yes")
	if got := run(0, "", "post", "meta", "list", id, "--format=ids"); got != "layout" {
		t.Errorf("meta list: %q", got)
	}

	// Post terms.
	run(0, "", "post", "term", "add", id, "post_tag", "rust", "--by=name")
	if got := run(0, "", "post", "term", "list", id, "post_tag", "--field=name"); got != "cms\ngo\nrust" {
		t.Errorf("term add: %q", got)
	}
	run(0, "", "post", "term", "remove", id, "post_tag", "go")
	run(0, "", "post", "term", "set", id, "post_tag", "cms")
	if got := run(0, "", "post", "term", "list", id, "post_tag", "--field=slug"); got != "cms" {
		t.Errorf("term set: %q", got)
	}

	// Terms.
	cat := run(0, "", "term", "create", "category", "News", "--porcelain")
	run(1, "", "term", "create", "category", "news")
	run(0, "", "term", "create", "category", "World", "--parent="+cat)
	run(2, "", "term", "create", "post_tag", "x", "--parent="+cat)
	run(0, "", "term", "update", "category", "news", "--by=slug", "--description=Daily")
	if got := run(0, "", "term", "get", "category", cat, "--field=description"); got != "Daily" {
		t.Errorf("term update: %q", got)
	}
	run(0, "", "post", "term", "set", id, "category", "news")
	run(1, "", "term", "delete", "category", "1", "--yes") // the default category
	run(0, "", "term", "delete", "category", cat, "--yes")
	if got := run(0, "", "post", "get", id, "--field=categories"); got != "uncategorized" {
		t.Errorf("orphaned post: %q", got)
	}
	run(0, "", "term", "recount", "category", "post_tag")
	run(1, "", "term", "list", "nope")
	if got := run(0, "", "taxonomy", "list", "--object_type=post", "--field=name"); !strings.Contains(got, "category") {
		t.Errorf("taxonomy list: %q", got)
	}
	if got := run(0, "", "post-type", "list", "--format=ids"); !strings.Contains(got, "page") {
		t.Errorf("post-type list: %q", got)
	}

	// Comments.
	cid := run(0, "", "comment", "create", "--comment_post_ID="+id, "--comment_content=Nice", "--comment_author=Ada", "--porcelain")
	run(0, "", "comment", "unapprove", cid)
	if got := run(0, "", "comment", "list", "--status=hold", "--format=ids"); got != cid {
		t.Errorf("held: %q", got)
	}
	run(0, "", "comment", "approve", cid)
	if got := run(0, "", "comment", "count", id, "--field=approved"); got != "1" {
		t.Errorf("count: %q", got)
	}
	run(0, "", "comment", "update", cid, "--comment_content=Edited")
	if got := run(0, "", "comment", "get", cid, "--field=comment_content"); got != "Edited" {
		t.Errorf("comment update: %q", got)
	}
	run(0, "", "comment", "delete", cid)
	run(1, "", "comment", "delete", cid) // in the trash: permanent, needs --yes
	run(0, "", "comment", "delete", cid, "--yes")
	run(1, "", "comment", "get", cid)
	run(0, "", "comment", "recount", id)

	// Duplicate, revisions, trash.
	dup := run(0, "", "post", "duplicate", id, "--porcelain")
	if got := run(0, "", "post", "get", dup, "--field=post_title"); got != "Hello again (Copy)" {
		t.Errorf("duplicate: %q", got)
	}
	revs, _ := st.ListRevisions(ctx, pid, false)
	if len(revs) > 0 {
		run(0, "", "post", "revision", "restore", itoa64(revs[len(revs)-1].ID))
	}
	run(0, "", "post", "revision", "prune", "--keep=1", "--yes")
	run(0, "", "post", "delete", dup)
	run(0, "", "post", "untrash", dup)
	if got := run(0, "", "post", "get", dup, "--field=post_status"); got != "draft" {
		t.Errorf("untrash: %q", got)
	}
	run(0, "", "post", "delete", dup)
	run(1, "", "post", "empty-trash")
	run(0, "", "post", "empty-trash", "--yes")
	run(1, "", "post", "exists", dup)
	run(1, "", "post", "delete", page, "--force")
	run(0, "", "post", "delete", page, "--force", "--yes")
}
