package server

import (
	"context"
	"strings"
	"testing"
)

// hgGuardWrite lets a change through for an entry that does not exist yet, but refuses one when the
// entry could not be read: otherwise a database error would skip the edit-lock check.
func TestHeadlessGoGuardWriteLookupError(t *testing.T) {
	_, _, srv := hgSite(t)
	hgMemo(t, srv, false)
	sc := &hgSchema{UID: "memo"}
	if err := srv.hgGuardWrite(context.Background(), sc, "no-such-doc", "", 1, ""); err != nil {
		t.Fatalf("missing entry: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := srv.hgGuardWrite(cancelled, sc, "no-such-doc", "", 1, "")
	if err == nil {
		t.Fatal("a failed entry lookup let the change through")
	}
	if strings.Contains(err.Error(), "context") {
		t.Errorf("the raw database error is shown to the admin: %v", err)
	}
}
