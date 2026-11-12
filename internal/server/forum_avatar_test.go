package server

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpgo/internal/media"
)

// forumTestImage encodes a w×h image (opaque unless alpha) in format.
func forumTestImage(t *testing.T, w, h int, format string, alpha bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if alpha && x < w/2 {
				a = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 120, A: a})
		}
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "gif":
		err = gif.Encode(&buf, img, nil)
	case "png":
		err = png.Encode(&buf, img)
	default:
		err = media.EncodeTo(&buf, img, format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// forumBombPNG is a valid PNG header claiming w×h pixels (the pixel data is a 1×1 image's): what a
// decompression bomb looks like to DecodeConfig.
func forumBombPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	b := forumTestImage(t, 1, 1, "png", false)
	// signature (8) + length (4) + "IHDR" (4) + data (13) + crc (4)
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	return b
}

func TestForumAvatarProcess(t *testing.T) {
	decode := func(b []byte) (image.Config, string) {
		cfg, f, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return cfg, f
	}
	// A landscape PNG becomes a centre-cropped square JPEG.
	out, ext, err := forumAvatarProcess(forumTestImage(t, 300, 200, "png", false))
	if err != nil {
		t.Fatal(err)
	}
	if cfg, f := decode(out); ext != "jpg" || f != "jpeg" || cfg.Width != 200 || cfg.Height != 200 {
		t.Errorf("png 300x200 → %s %s %dx%d", ext, f, cfg.Width, cfg.Height)
	}
	// Big photos are scaled down to 512.
	out, _, err = forumAvatarProcess(forumTestImage(t, 900, 1400, "jpeg", false))
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := decode(out); cfg.Width != 512 || cfg.Height != 512 {
		t.Errorf("jpeg 900x1400 → %dx%d", cfg.Width, cfg.Height)
	}
	// Transparency keeps PNG; GIF and WebP are accepted.
	if _, ext, err := forumAvatarProcess(forumTestImage(t, 64, 64, "png", true)); err != nil || ext != "png" {
		t.Errorf("alpha png: %s %v", ext, err)
	}
	if _, _, err := forumAvatarProcess(forumTestImage(t, 40, 30, "gif", false)); err != nil {
		t.Errorf("gif: %v", err)
	}
	if _, _, err := forumAvatarProcess(forumTestImage(t, 80, 80, "webp", false)); err != nil {
		t.Errorf("webp: %v", err)
	}
	// Rejections: not an image, an SVG, a lying header, too small, too big, a bomb.
	bad := map[string]struct {
		data []byte
		want error
	}{
		"text":      {[]byte("hello, this is not an image at all"), errForumAvatarType},
		"svg":       {[]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script></svg>`), errForumAvatarType},
		"html":      {[]byte(`<!DOCTYPE html><html><body>x</body></html>`), errForumAvatarType},
		"truncated": {forumTestImage(t, 50, 50, "png", false)[:40], errForumAvatarBroken},
		"tiny":      {forumTestImage(t, 8, 8, "png", false), errForumAvatarSmall},
		"wide":      {forumBombPNG(t, 7000, 100), errForumAvatarDims},
		"bomb":      {forumBombPNG(t, 60000, 60000), errForumAvatarDims},
	}
	for name, c := range bad {
		if _, _, err := forumAvatarProcess(c.data); err != c.want {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestForumAvatarUpload(t *testing.T) {
	fx := forumFrontStart(t, false)
	srv, ctx := fx.srv, fx.ctx
	_, mid := fx.member(t, "mia", "forum_participant")
	u, err := srv.store.UserByID(ctx, mid)
	if err != nil {
		t.Fatal(err)
	}
	// Too big: rejected before decoding.
	if _, err := srv.forumSaveAvatar(ctx, u, bytes.NewReader(make([]byte, forumAvatarMaxBytes+1))); err != errForumAvatarBytes {
		t.Errorf("3 MB: %v", err)
	}
	if _, err := srv.forumSaveAvatar(ctx, u, strings.NewReader("<?php echo 1; ?>")); err != errForumAvatarType {
		t.Errorf("php: %v", err)
	}
	if _, err := srv.forumSaveAvatar(ctx, u, bytes.NewReader(forumBombPNG(t, 30000, 30000))); err != errForumAvatarDims {
		t.Errorf("bomb: %v", err)
	}

	a1, err := srv.forumSaveAvatar(ctx, u, bytes.NewReader(forumTestImage(t, 640, 480, "png", false)))
	if err != nil {
		t.Fatal(err)
	}
	if a1.UploaderID != mid || a1.Mime != "image/jpeg" || a1.Width != 480 || a1.Height != 480 {
		t.Errorf("attachment: uploader %d mime %s %dx%d", a1.UploaderID, a1.Mime, a1.Width, a1.Height)
	}
	file1 := filepath.Join(srv.cfg.UploadsDir, filepath.FromSlash(a1.FilePath))
	if _, err := os.Stat(file1); err != nil {
		t.Errorf("file: %v", err)
	}
	fresh, _ := srv.store.UserByID(ctx, mid)
	if avatarIDOf(fresh) != a1.ID || forumMetaInt64(fresh.Meta, forumUMetaAvatar) != a1.ID {
		t.Errorf("meta: %v", fresh.Meta)
	}
	authors, _ := srv.forumAuthors(ctx, []int64{mid})
	if a := authors[mid]; !a.HasPhoto || a.AvatarID != a1.ID || !strings.Contains(a.AvatarURL, "/wp-content/uploads/") {
		t.Errorf("author avatar: %+v", a)
	}
	if h := string(forumAvatarHTML(authors[mid], 40)); !strings.HasPrefix(h, "<img") {
		t.Errorf("avatar html: %s", h)
	}

	// A second upload replaces and deletes the first.
	a2, err := srv.forumSaveAvatar(ctx, u, bytes.NewReader(forumTestImage(t, 64, 64, "png", true)))
	if err != nil {
		t.Fatal(err)
	}
	if a2.Mime != "image/png" {
		t.Errorf("alpha upload mime %s", a2.Mime)
	}
	if _, err := srv.store.GetAttachment(ctx, a1.ID); err == nil {
		t.Error("old avatar attachment kept")
	}
	if _, err := os.Stat(file1); !os.IsNotExist(err) {
		t.Errorf("old avatar file kept: %v", err)
	}

	// Remove: meta cleared, the forum's photo deleted, initials again.
	if err := srv.forumRemoveAvatar(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.GetAttachment(ctx, a2.ID); err == nil {
		t.Error("removed avatar attachment kept")
	}
	authors, _ = srv.forumAuthors(ctx, []int64{mid})
	if a := authors[mid]; a.HasPhoto || a.AvatarURL != defaultAvatar || a.Initials != "M" {
		t.Errorf("after remove: %+v", a)
	}

	// An avatar an administrator picked from the library is cleared but not deleted.
	lib, err := srv.insertAttachmentFile(ctx, "library.png", bytes.NewReader(forumTestImage(t, 100, 100, "png", false)), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.forumSetUserMeta(ctx, mid, map[string]any{"avatar_id": lib.ID}); err != nil {
		t.Fatal(err)
	}
	if err := srv.forumRemoveAvatar(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.GetAttachment(ctx, lib.ID); err != nil {
		t.Errorf("library image deleted: %v", err)
	}
	if fresh, _ := srv.store.UserByID(ctx, mid); avatarIDOf(fresh) != 0 {
		t.Error("avatar_id not cleared")
	}
	// A member's upload never deletes someone else's attachment even if forum_avatar_id points at it.
	if err := srv.forumSetUserMeta(ctx, mid, map[string]any{forumUMetaAvatar: lib.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.forumSaveAvatar(ctx, u, bytes.NewReader(forumTestImage(t, 50, 50, "png", false))); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.GetAttachment(ctx, lib.ID); err != nil {
		t.Errorf("foreign attachment deleted: %v", err)
	}
}
