package server

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestSideloadCleansUpAfterVariantInsertFails(t *testing.T) {
	ts, client, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, client)
	_, err := srv.store.Pool.Exec(t.Context(), `ALTER TABLE media_variants ADD CONSTRAINT reject_test_variants CHECK (file_path NOT LIKE '%rejected-upload%')`)
	if err != nil {
		t.Fatal(err)
	}
	image := gocdnTestImage(1000, 800, "png")
	_, err = srv.insertAttachmentFile(t.Context(), "rejected-upload.png", bytes.NewReader(image), 1, 0)
	if err == nil || !strings.Contains(err.Error(), "reject_test_variants") {
		t.Fatalf("expected a rejected variant insert, got %v", err)
	}
	err = filepath.WalkDir(srv.cfg.UploadsDir, func(_ string, item fs.DirEntry, err error) error {
		if err == nil && !item.IsDir() && strings.HasPrefix(item.Name(), "rejected-upload") {
			t.Errorf("failed registration left %s", item.Name())
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
