package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bmgrFakeArchive writes a minimal archive with a manifest of the given kind.
func bmgrFakeArchive(t *testing.T, dir, name, kind string, created time.Time) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("manifest.json")
	_ = json.NewEncoder(w).Encode(bmgrManifest{Format: bmgrFormat, Kind: kind, Created: created})
	_ = zw.Close()
	f.Close()
	_ = os.Chtimes(filepath.Join(dir, name), created, created)
}

func TestBmgrPruneSafetyKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WPGO_BACKUPS_DIR", dir)
	s := &Server{}
	now := time.Now()
	for i := 0; i < 5; i++ {
		bmgrFakeArchive(t, dir, fmt.Sprintf("pre-restore-%d.zip", i), "safety", now.Add(-time.Duration(i)*time.Hour))
	}
	bmgrFakeArchive(t, dir, "backup-manual.zip", "manual", now.Add(-48*time.Hour))
	removed := s.bmgrPruneKind("safety", "pre-restore-", bmgrSafetyKeep)
	if len(removed) != 2 {
		t.Fatalf("removed %v, want 2", removed)
	}
	for _, keep := range []string{"pre-restore-0.zip", "pre-restore-1.zip", "pre-restore-2.zip", "backup-manual.zip"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s was removed", keep)
		}
	}
	if s.bmgrUsage() <= 0 {
		t.Error("usage should be positive")
	}
}

func TestBmgrSweepsStaleParts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WPGO_BACKUPS_DIR", dir)
	s := &Server{}
	old, fresh := filepath.Join(dir, "backup-old.zip.part"), filepath.Join(dir, "backup-new.zip.part")
	for _, p := range []string{old, fresh} {
		_ = os.WriteFile(p, []byte("x"), 0o600)
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(old, past, past)
	s.bmgrList()
	if _, err := os.Stat(old); err == nil {
		t.Error("stale .part was not swept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh .part must be kept")
	}
}

func TestBmgrCancelAndWarnings(t *testing.T) {
	s := &Server{}
	j, _ := s.bmgrStart("backup")
	ctx := s.bmgrCancelable(j)
	if !s.bmgrCancel(j.ID) || ctx.Err() == nil {
		t.Fatal("cancel did not stop the context")
	}
	s.bmgrFinish(j, fmt.Errorf("adding files: %w", context.Canceled))
	if got := s.bmgrJobByID(j.ID); got.State != "error" || got.Phase != "Canceled" {
		t.Errorf("job after cancel: %+v", got)
	}
	if s.bmgrCancel(j.ID) {
		t.Error("a finished job cannot be canceled again")
	}
	j2, _ := s.bmgrStart("backup")
	s.bmgrUpdate(j2, func(j *bmgrJob) { j.Warnings = 2 })
	s.bmgrFinish(j2, nil)
	if got := s.bmgrJobByID(j2.ID); got.State != "done" || !strings.Contains(got.Phase, "warnings") {
		t.Errorf("job with warnings: %+v", got)
	}

}

func TestBmgrFreeBytes(t *testing.T) {
	if free, ok := bmgrFreeBytes(t.TempDir()); !ok || free <= 0 {
		t.Errorf("free = %d ok = %v", free, ok)
	}
}

func TestBmgrUnreadableFileIsReported(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read everything")
	}
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	t.Setenv("WPGO_BACKUPS_DIR", t.TempDir())
	bad := filepath.Join(srv.cfg.UploadsDir, "unreadable-test.bin")
	if err := os.MkdirAll(srv.cfg.UploadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("secret"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bad)
	var archive string
	var j *bmgrJob
	bmgrRun(t, srv, "backup", func(jb *bmgrJob) (err error) {
		j = jb
		archive, err = srv.bmgrRunBackup(ctx, jb, bmgrSelection{Categories: []string{"media"}, UserMode: "all"})
		return err
	})
	got := srv.bmgrJobByID(j.ID)
	if got.State != "done" || got.Warnings != 1 || got.Phase != "Done with warnings" {
		t.Errorf("job: state=%s warnings=%d phase=%s", got.State, got.Warnings, got.Phase)
	}
	if !strings.Contains(strings.Join(got.Log, "\n"), "skipped uploads/unreadable-test.bin") {
		t.Errorf("no skip line in the log:\n%s", strings.Join(got.Log, "\n"))
	}
	m, err := bmgrReadManifest(filepath.Join(srv.bmgrDir(), archive))
	if err != nil || m.Skipped != 1 {
		t.Fatalf("manifest skipped = %v err = %v", m, err)
	}
	zr, _ := zip.OpenReader(filepath.Join(srv.bmgrDir(), archive))
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "unreadable-test.bin") {
			t.Error("unreadable file left a (empty) entry in the archive")
		}
	}
}
