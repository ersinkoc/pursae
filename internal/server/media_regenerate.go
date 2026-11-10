package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// Regenerate thumbnails: after Settings › Media (sizes, image output format) or the theme's sizes
// change, the sub-sizes of the existing library are made again from each attachment's original
// file. WordPress leaves this to plugins; here it is a Media Library bulk action and a "Regenerate
// all" button.

var errRegenerateSkipped = errors.New("the original file is not on this server")

// regenerateAttachment rebuilds the sub-sizes (and the "-scaled"/converted main file) of one image
// attachment from its original upload. Edited images (image editor backup) keep their edited
// main file as the source. It returns errRegenerateSkipped for files that are not local images.
func (s *Server) regenerateAttachment(ctx context.Context, a *store.Attachment) error {
	if !strings.HasPrefix(a.Mime, "image/") || a.Mime == "image/svg+xml" {
		return errRegenerateSkipped
	}
	dir := path.Dir(a.FilePath)
	src := a.FilePath
	_, edited := a.Meta["backup_file"]
	if orig, _ := a.Meta["original_image"].(string); orig != "" && !edited {
		src = path.Join(dir, orig)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(src))); err != nil {
		return errRegenerateSkipped
	}
	oldMain := a.FilePath
	oldVariants := a.Variants
	mime := media.MimeFor(src)
	if mime == "" {
		mime = a.Mime
	}
	opts := s.imageEditorOptions(ctx, path.Base(src), mime)
	opts.Reserved = s.uploadReserved(ctx)(strings.TrimPrefix(dir, "."))
	// The old sub-sizes go first so their names are free again (and removed sizes do not stay behind).
	for _, v := range oldVariants {
		if v.FilePath != src {
			_ = os.Remove(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(v.FilePath)))
		}
	}
	info, err := media.ProcessImageWith(s.cfg.UploadsDir, src, s.imageSizes(), opts)
	if err != nil {
		slog.Warn("regenerate: image could not be processed", "id", a.ID, "file", src, "err", err)
		return err
	}
	if oldMain != src && oldMain != info.RelPath {
		_ = os.Remove(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(oldMain)))
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	delete(a.Meta, "original_image")
	a.Mime = mime
	a.Width, a.Height = info.Width, info.Height
	a.FilePath, a.FileSize = info.RelPath, info.FileSize
	a.Meta["filesize"] = info.FileSize
	if info.OriginalFile != "" {
		a.Meta["original_image"] = info.OriginalFile
		if m := media.MimeFor(info.RelPath); m != "" {
			a.Mime = m
		}
	}
	a.Variants = nil
	ndir := path.Dir(info.RelPath)
	for _, v := range info.Variants {
		a.Variants = append(a.Variants, store.AttachmentVariant{Name: v.Name, FilePath: path.Join(ndir, v.File),
			Width: v.Width, Height: v.Height, Mime: v.Mime, FileSize: v.Size})
	}
	if err := s.store.ReplaceAttachmentFile(ctx, a); err != nil {
		return err
	}
	s.purgeAttachment(a)
	s.gocdnAfterUpload(ctx, a)
	return nil
}

// regenerateAttachments processes the given attachments (all images when ids is nil) and returns
// how many were rebuilt, skipped and failed.
func (s *Server) regenerateAttachments(ctx context.Context, ids []int64) (done, skipped, failed int) {
	if ids == nil {
		var err error
		if ids, err = s.store.ImageAttachmentIDs(ctx); err != nil {
			return 0, 0, 1
		}
	}
	for _, id := range ids {
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			failed++
			continue
		}
		switch err := s.regenerateAttachment(ctx, a); {
		case err == nil:
			done++
		case errors.Is(err, errRegenerateSkipped):
			skipped++
		default:
			failed++
		}
	}
	return
}

var regenerateRunning atomic.Bool

// regenerateAllAsync starts one background run over the whole library; false when one is running.
func (s *Server) regenerateAllAsync() bool {
	if !regenerateRunning.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer regenerateRunning.Store(false)
		d, sk, f := s.regenerateAttachments(context.Background(), nil)
		slog.Info("regenerate thumbnails finished", "done", d, "skipped", sk, "failed", f)
	}()
	return true
}
