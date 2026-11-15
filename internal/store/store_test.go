package store

import "testing"

func TestPrefixed(t *testing.T) {
	got := prefixed("c", "id, coalesce(parent_id,0), author_name")
	want := "c.id, coalesce(c.parent_id,0), c.author_name"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestCommentTreeIndependentOfInputOrder(t *testing.T) {
	root := &Comment{ID: 1}
	child := &Comment{ID: 2, ParentID: 1}
	grandchild := &Comment{ID: 3, ParentID: 2}

	got := CommentTree([]*Comment{grandchild, child, root}, 2)
	if len(got) != 1 || got[0] != root || root.Depth != 1 {
		t.Fatalf("roots = %#v", got)
	}
	if len(root.Children) != 2 || root.Children[0] != child || root.Children[1] != grandchild {
		t.Fatalf("flattened children = %#v", root.Children)
	}
	if child.Depth != 2 || grandchild.Depth != 2 || len(child.Children) != 0 {
		t.Fatalf("depths child=%d grandchild=%d nested=%#v", child.Depth, grandchild.Depth, child.Children)
	}
}
