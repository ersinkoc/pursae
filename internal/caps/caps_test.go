package caps

import "testing"

type fakePost struct {
	author int64
	status string
	typ    string
}

func (p fakePost) PostAuthorID() int64 { return p.author }
func (p fakePost) PostStatus() string  { return p.status }
func (p fakePost) PostType() string    { return p.typ }

func TestResolveUnionAndDenyWins(t *testing.T) {
	author := map[string]bool{"edit_posts": true, "publish_posts": true, "upload_files": true}
	editor := map[string]bool{"edit_posts": true, "edit_others_posts": true, "moderate_comments": true}

	g := Resolve([]map[string]bool{author, editor}, map[string]string{
		"upload_files":    "deny",
		"manage_options":  "allow",
		"publish_posts":   "deny",
		"edit_theme_opts": "allow",
	})
	for _, c := range []string{"edit_posts", "edit_others_posts", "moderate_comments", "manage_options"} {
		if !g[c] {
			t.Errorf("expected %s granted", c)
		}
	}
	for _, c := range []string{"upload_files", "publish_posts"} {
		if g[c] {
			t.Errorf("expected %s denied", c)
		}
	}
}

func TestMetaCaps(t *testing.T) {
	author := Grants{"edit_posts": true, "edit_published_posts": true, "publish_posts": true, "read": true, "delete_posts": true}
	contributor := Grants{"edit_posts": true, "read": true, "delete_posts": true}

	ownDraft := fakePost{author: 7, status: "draft", typ: "post"}
	ownPublished := fakePost{author: 7, status: "publish", typ: "post"}
	othersDraft := fakePost{author: 9, status: "draft", typ: "post"}

	cases := []struct {
		name string
		g    Grants
		cap  string
		post Post
		want bool
	}{
		{"author edits own draft", author, "edit_post", ownDraft, true},
		{"author edits own published", author, "edit_post", ownPublished, true},
		{"author cannot edit others", author, "edit_post", othersDraft, false},
		{"contributor cannot edit own published", contributor, "edit_post", ownPublished, false},
		{"contributor cannot publish", contributor, "publish_post", ownDraft, false},
		{"contributor reads own draft", contributor, "read_post", ownDraft, true},
		{"meta cap without post is denied", author, "edit_post", nil, false},
	}
	for _, c := range cases {
		if got := Can(7, c.g, c.cap, c.post); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
