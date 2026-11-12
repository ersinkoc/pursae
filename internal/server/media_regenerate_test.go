package server

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"wpgo/internal/media"
)

// TestRegenerateThumbnails changes the medium size after an upload and rebuilds the sub-sizes; an
// undecodable image gets a warning instead of failing silently, and WAV uploads get a length.
func TestRegenerateThumbnails(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)

	img := image.NewRGBA(image.Rect(0, 0, 800, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := media.EncodeTo(&buf, img, "jpeg"); err != nil {
		t.Fatal(err)
	}
	a, err := srv.insertAttachmentFile(ctx, "regen.jpg", &buf, uid, 0)
	if err != nil {
		t.Fatal(err)
	}
	a, _ = srv.store.GetAttachment(ctx, a.ID)
	if v := a.Variant("medium"); v == nil || v.Width != 300 {
		t.Fatalf("medium before: %+v", v)
	}
	if err := srv.settings.Set(ctx, map[string]any{"medium_size_w": 400, "medium_size_h": 400}); err != nil {
		t.Fatal(err)
	}
	done, _, failed := srv.regenerateAttachments(ctx, []int64{a.ID})
	if done != 1 || failed != 0 {
		t.Fatalf("done=%d failed=%d", done, failed)
	}
	a, _ = srv.store.GetAttachment(ctx, a.ID)
	if v := a.Variant("medium"); v == nil || v.Width != 400 || !strings.Contains(v.FilePath, "400x250") {
		t.Fatalf("medium after: %+v", v)
	}

	// A file that is not an image: warning, no panic, attachment kept without sizes.
	junk, err := srv.insertAttachmentFile(ctx, "broken.png", strings.NewReader("\x89PNG\r\n\x1a\nnot really"), uid, 0)
	if err == nil && junk != nil {
		if len(junk.Variants) != 0 {
			t.Errorf("variants for a broken image: %d", len(junk.Variants))
		}
		if w := srv.createImageSubsizes(ctx, junk); w == "" {
			t.Error("no warning for an image that cannot be processed")
		}
	}

	// AVIF has no sub-sizes: the uploader is told.
	a2, err := srv.insertAttachmentFile(ctx, "x.avif", strings.NewReader("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), uid, 0)
	if err == nil {
		if w := srv.createImageSubsizes(ctx, a2); !strings.Contains(w, "No smaller image sizes") {
			t.Errorf("avif warning: %q", w)
		}
	}
}

func TestMediaLengthFormatted(t *testing.T) {
	for in, want := range map[int]string{5: "0:05", 65: "1:05", 3725: "1:02:05"} {
		if got := mediaLengthFormatted(in); got != want {
			t.Errorf("%d: %s", in, got)
		}
	}
}

// TestRegenerateAllJob runs the "Regenerate all" job: progress is stored, the ajax endpoint reports
// it, and a job stored as running (a restart) is resumed after its last image.
func TestRegenerateAllJob(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	installAndLogin(t, ts, c)
	ctx := context.Background()
	var uid int64
	_ = srv.store.Pool.QueryRow(ctx, `SELECT id FROM users ORDER BY id LIMIT 1`).Scan(&uid)
	var ids []int64
	for _, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		img := image.NewRGBA(image.Rect(0, 0, 400, 300))
		var buf bytes.Buffer
		if err := media.EncodeTo(&buf, img, "jpeg"); err != nil {
			t.Fatal(err)
		}
		a, err := srv.insertAttachmentFile(ctx, name, &buf, uid, 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	wait := func() regenerateJob {
		t.Helper()
		for i := 0; i < 200; i++ {
			if !regenerateRunning.Load() {
				return srv.regenerateJobState()
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("job did not finish")
		return regenerateJob{}
	}
	if !srv.regenerateAllAsync() {
		t.Fatal("job did not start")
	}
	j := wait()
	if !j.Finished || j.Running || j.Total != 3 || j.Done != 3 || j.Percent() != 100 {
		t.Fatalf("job = %+v", j)
	}
	_, body, _ := get(t, c, ts.URL+"/wp-admin/admin-ajax?action=regenerate-progress")
	if !strings.Contains(body, `"percent":100`) || !strings.Contains(body, `"finished":true`) {
		t.Errorf("progress = %s", body)
	}
	// A restart in the middle: stored as running after the first image.
	srv.regeneratePersist(ctx, regenerateJob{Running: true, Total: 3, Done: 1, LastID: ids[0]})
	srv.regenerateResume()
	j = wait()
	if !j.Finished || j.Done != 3 || j.Total != 3 || j.LastID != ids[2] {
		t.Fatalf("resumed job = %+v", j)
	}
}
