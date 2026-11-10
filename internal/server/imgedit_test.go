package server

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"testing"

	"wpgo/internal/auth"
	"wpgo/internal/media"
	"wpgo/internal/store"
)

// The history image-edit.js sends: crop width/height are strings, the rest numbers.
func TestParseHistoryLooseNumbers(t *testing.T) {
	ops := parseHistory(`[{"c":{"x":40,"y":30,"w":"201","h":"132","r":1}},{"r":90},{"f":1}]`)
	if len(ops) != 3 {
		t.Fatalf("ops = %d, want 3", len(ops))
	}
	c := ops[0]
	if !c.Crop || c.X != 40 || c.Y != 30 || c.W != 201 || c.H != 132 || c.Ratio != 1 {
		t.Errorf("crop op = %+v", c)
	}
	if ops[1].Rotate != 90 || ops[2].Flip != 1 {
		t.Errorf("rotate/flip = %+v %+v", ops[1], ops[2])
	}

	img := media.ApplyOps(image.NewRGBA(image.Rect(0, 0, 447, 447)), ops[:1])
	if b := img.Bounds(); b.Dx() != 201 || b.Dy() != 132 {
		t.Errorf("cropped size = %dx%d, want 201x132", b.Dx(), b.Dy())
	}
}

// imgeditEnv creates a 600x400 PNG attachment (with sub-sizes) owned by the admin and returns what
// the image-editor ajax calls need.
func imgeditEnv(t *testing.T) (s *secSite, a *store.Attachment, call func(form url.Values) map[string]any) {
	t.Helper()
	s = secStart(t)
	ctx := context.Background()
	admin, _ := s.srv.store.UserByLoginOrEmail(ctx, "admin")
	src := image.NewRGBA(image.Rect(0, 0, 600, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 600; x++ {
			src.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 90, 255})
		}
	}
	rel := "2026/11/pic.png"
	if err := os.MkdirAll(filepath.Join(s.srv.cfg.UploadsDir, "2026/11"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := media.WriteFile(s.srv.cfg.UploadsDir, rel, src, "png"); err != nil {
		t.Fatal(err)
	}
	info, err := media.ProcessImage(s.srv.cfg.UploadsDir, rel, s.srv.imageSizes())
	if err != nil {
		t.Fatal(err)
	}
	a = &store.Attachment{UploaderID: admin.ID, Slug: "pic", FilePath: info.RelPath, Mime: "image/png", FileSize: info.FileSize,
		Width: info.Width, Height: info.Height, Title: "pic", Meta: map[string]any{}}
	for _, v := range info.Variants {
		a.Variants = append(a.Variants, store.AttachmentVariant{Name: v.Name, FilePath: path.Join(path.Dir(info.RelPath), v.File), Width: v.Width, Height: v.Height, Mime: v.Mime, FileSize: v.Size})
	}
	if err := s.srv.store.InsertAttachment(ctx, a); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(s.base)
	var sess *auth.Session
	for _, ck := range s.admin.c.Jar.Cookies(u) {
		if ck.Name == auth.CookieName {
			sess, _ = s.srv.auth.Lookup(ctx, ck.Value)
		}
	}
	if sess == nil {
		t.Fatal("no session")
	}
	n := s.srv.auth.Nonce("image_editor-"+itoa64(a.ID), sess.UserID, sess.Token)
	call = func(form url.Values) map[string]any {
		t.Helper()
		form.Set("action", "image-editor")
		form.Set("postid", itoa64(a.ID))
		form.Set("_ajax_nonce", n)
		code, body, _ := post(t, s.admin.c, s.base+"/wp-admin/admin-ajax", form)
		if code != http.StatusOK {
			t.Fatalf("image-editor %v: %d %s", form, code, body)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("response %q: %v", body, err)
		}
		return out
	}
	return s, a, call
}

func imgeditExists(t *testing.T, s *secSite, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(s.srv.cfg.UploadsDir, rel))
	return err == nil
}

// Crop, save, restore: the edit writes a new main file and sizes, keeps the originals on disk, and
// Restore points the attachment back at them.
func TestImgeditSaveRestore(t *testing.T) {
	s, a, call := imgeditEnv(t)
	ctx := context.Background()
	origPath, origVars := a.FilePath, append([]store.AttachmentVariant(nil), a.Variants...)

	out := call(url.Values{"do": {"save"}, "target": {"all"}, "history": {`[{"c":{"x":10,"y":20,"w":"300","h":"200","r":0}}]`}})
	if out["success"] != true {
		t.Fatalf("save: %v", out)
	}
	got, _ := s.srv.store.GetAttachment(ctx, a.ID)
	if got.FilePath == origPath || got.Width != 300 || got.Height != 200 {
		t.Fatalf("after save: %s %dx%d", got.FilePath, got.Width, got.Height)
	}
	if !imgeditExists(t, s, got.FilePath) || !imgeditExists(t, s, origPath) {
		t.Error("edited or original main file missing on disk")
	}
	if got.Variant("thumbnail") == nil || !imgeditExists(t, s, got.Variant("thumbnail").FilePath) {
		t.Error("edited thumbnail missing")
	}
	for _, v := range origVars {
		if !imgeditExists(t, s, v.FilePath) {
			t.Errorf("original size %s deleted by the edit", v.FilePath)
		}
	}
	if got.Meta["backup_file"] != origPath {
		t.Errorf("backup_file = %v, want %s", got.Meta["backup_file"], origPath)
	}

	// Nothing to save is refused.
	if out := call(url.Values{"do": {"save"}, "history": {`[]`}}); out["success"] != false {
		t.Errorf("empty history saved: %v", out)
	}

	out = call(url.Values{"do": {"restore"}})
	if out["success"] != true {
		t.Fatalf("restore: %v", out)
	}
	got, _ = s.srv.store.GetAttachment(ctx, a.ID)
	if got.FilePath != origPath || got.Width != 600 || got.Height != 400 || len(got.Variants) != len(origVars) {
		t.Errorf("after restore: %s %dx%d variants=%d", got.FilePath, got.Width, got.Height, len(got.Variants))
	}
	if _, ok := got.Meta["backup_file"]; ok {
		t.Error("backup_file kept after restore")
	}
	if !imgeditExists(t, s, origPath) {
		t.Error("original deleted by restore")
	}
	// A second restore has nothing to restore.
	if out := call(url.Values{"do": {"restore"}}); out["success"] != false {
		t.Errorf("second restore: %v", out)
	}
}

// "Thumbnail only" swaps just the thumbnail; scale refuses to enlarge and otherwise resizes.
func TestImgeditThumbnailOnlyAndScale(t *testing.T) {
	s, a, call := imgeditEnv(t)
	ctx := context.Background()
	origPath := a.FilePath
	origThumb := a.Variant("thumbnail").FilePath

	out := call(url.Values{"do": {"save"}, "target": {"thumbnail"}, "history": {`[{"c":{"x":0,"y":0,"w":"200","h":"200","r":0}}]`}})
	if out["success"] != true {
		t.Fatalf("thumbnail save: %v", out)
	}
	got, _ := s.srv.store.GetAttachment(ctx, a.ID)
	if got.FilePath != origPath || got.Width != 600 || got.Height != 400 {
		t.Errorf("main file changed by a thumbnail-only save: %s %dx%d", got.FilePath, got.Width, got.Height)
	}
	if th := got.Variant("thumbnail"); th == nil || th.FilePath == origThumb || !imgeditExists(t, s, th.FilePath) {
		t.Errorf("thumbnail not replaced: %+v", th)
	}
	if !imgeditExists(t, s, origThumb) {
		t.Error("previous thumbnail deleted")
	}

	if out := call(url.Values{"do": {"scale"}, "fwidth": {"900"}, "fheight": {"600"}}); out["success"] != false {
		t.Errorf("scaling up accepted: %v", out)
	}
	out = call(url.Values{"do": {"scale"}, "fwidth": {"300"}, "fheight": {"200"}})
	if out["success"] != true {
		t.Fatalf("scale: %v", out)
	}
	got, _ = s.srv.store.GetAttachment(ctx, a.ID)
	if got.Width != 300 || got.Height != 200 || !imgeditExists(t, s, got.FilePath) {
		t.Errorf("after scale: %s %dx%d", got.FilePath, got.Width, got.Height)
	}
}
