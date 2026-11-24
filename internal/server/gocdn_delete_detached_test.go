package server

import (
	"strings"
	"testing"
)

// An unreachable detached object keeps its durable mapping for housekeeping to retry.
func TestGocdnDeleteItemsRetainsUnreachableDetachedObjects(t *testing.T) {
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
	if _, err := srv.store.Pool.Exec(t.Context(), `ALTER TABLE settings ADD CONSTRAINT reject_pending_record CHECK (key <> 'gocdn_pending_deletes')`); err != nil {
		t.Fatal(err)
	}
	srv.gocdnDeleteItems(t.Context(), items)
	if it := srv.gocdnItemByPath(t.Context(), a.FilePath); it == nil || it.MediaID != 0 || it.Key != "detached.mp4" {
		t.Fatalf("the durable cleanup record was lost: %+v", it)
	}
	if pending := srv.gocdnPendingDeletes(); len(pending) != 0 {
		t.Fatalf("unexpected settings-based retries: %+v", pending)
	}
}
