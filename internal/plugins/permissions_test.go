package plugins

import "testing"

func TestPermissionSetIgnoresFalseValues(t *testing.T) {
	p := PermissionSet{PermOptionsRead: false, PermPostsRead: true}
	if got := p.List(); len(got) != 1 || got[0] != PermPostsRead {
		t.Fatalf("list = %v", got)
	}
	if got := p.Intersect(PermissionSet{PermOptionsRead: true, PermPostsRead: true}); got.Has(PermOptionsRead) || !got.Has(PermPostsRead) {
		t.Fatalf("intersection = %v", got)
	}
}
