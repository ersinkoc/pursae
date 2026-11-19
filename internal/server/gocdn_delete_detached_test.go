package server

import (
	"strings"
	"testing"
)

// Rows detached from a deleted attachment do not cascade away, so an object that cannot be
// reached is queued for a later delete and its row is still removed.
func TestGocdnDeleteItemsQueuesUnreachableDetachedObjects(t *testing.T) {
	ts, client, srv := newTestServer(t, false)
	installAndLogin(t, ts, client)
	a, err := srv.insertAttachmentFile(t.Context(), "detached.mp4", strings.NewReader("bytes"), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	// No storage endpoint is configured, so no client can be built for the item.
	if err := srv.gocdnSaveItem(t.Context(), &gocdnItem{
		MediaID: a.ID, Path: a.FilePath, Provider: "minio", Region: "auto", Bucket: "media", Key: "detached.mp4",
		Size: a.FileSize, Mime: a.Mime,
	}); err != nil {
		t.Fatal(err)
	}
	items, err := srv.detachOffloadsAndDeleteAttachment(t.Context(), a.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("detach: %d items, %v", len(items), err)
	}
	srv.gocdnDeleteItems(t.Context(), items)
	if srv.gocdnItemByPath(t.Context(), a.FilePath) != nil {
		t.Fatal("the detached mapping was left behind")
	}
	pending := srv.gocdnPendingDeletes()
	if len(pending) != 1 || pending[0].Key != "detached.mp4" {
		t.Fatalf("pending deletes = %+v, want the unreachable object", pending)
	}
}
