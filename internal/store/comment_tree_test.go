package store

import "testing"

func TestCommentTreeCapsDeepRepliesInAnyOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		flat := []*Comment{{ID: 1}, {ID: 2, ParentID: 1}, {ID: 3, ParentID: 2}, {ID: 4, ParentID: 3}}
		if reverse {
			for i, j := 0, len(flat)-1; i < j; i, j = i+1, j-1 {
				flat[i], flat[j] = flat[j], flat[i]
			}
		}
		roots := CommentTree(flat, 2)
		if len(roots) != 1 || roots[0].ID != 1 || len(roots[0].Children) != 3 {
			t.Fatalf("reverse=%v roots=%+v", reverse, roots)
		}
		for _, c := range roots[0].Children {
			if c.Depth != 2 || len(c.Children) != 0 {
				t.Fatalf("reverse=%v child=%+v", reverse, c)
			}
		}
	}
}

func TestCommentTreeBreaksCycles(t *testing.T) {
	flat := []*Comment{{ID: 1, ParentID: 2}, {ID: 2, ParentID: 1}}
	roots := CommentTree(flat, 5)
	if len(roots) != 1 || len(roots[0].Children) != 1 {
		t.Fatalf("roots=%+v", roots)
	}
}
