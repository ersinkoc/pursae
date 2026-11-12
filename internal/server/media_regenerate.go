package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	// The sizes were written again, mostly under the same names: their bucket copies and cached
	// image-service variants are stale (the source file itself did not change).
	var changed []string
	for _, v := range oldVariants {
		changed = append(changed, v.FilePath)
	}
	for _, v := range a.Variants {
		changed = append(changed, v.FilePath)
	}
	if oldMain != src {
		changed = append(changed, oldMain)
	}
	s.gocdnFilesChanged(a, changed)
	s.gocdnDropVariants(changed)
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

// The "Regenerate all" job. Progress is kept in the option media_regenerate_job after every image, so
// a run interrupted by a restart continues from the last image done (regenerateResume at start-up);
// the Media Library polls /wp-admin/admin-ajax?action=regenerate-progress.

const regenerateJobOption = "media_regenerate_job"

type regenerateJob struct {
	Running  bool      `json:"running"`
	Finished bool      `json:"finished"`
	Stopped  bool      `json:"stopped"`
	Total    int       `json:"total"`
	Done     int       `json:"done"`
	Skipped  int       `json:"skipped"`
	Failed   int       `json:"failed"`
	LastID   int64     `json:"last_id"`
	Started  time.Time `json:"started"`
	Updated  time.Time `json:"updated"`
}

// Percent is the share of the library already processed.
func (j regenerateJob) Percent() int {
	if j.Total <= 0 {
		if j.Finished {
			return 100
		}
		return 0
	}
	return min(100, (j.Done+j.Skipped+j.Failed)*100/j.Total)
}

var (
	regenerateRunning atomic.Bool
	regenerateStop    atomic.Bool
	regenerateMu      sync.Mutex
	regenerateLive    *regenerateJob
)

// regenerateJobState is the running job, else the stored state of the last one.
func (s *Server) regenerateJobState() regenerateJob {
	regenerateMu.Lock()
	if regenerateLive != nil {
		j := *regenerateLive
		regenerateMu.Unlock()
		return j
	}
	regenerateMu.Unlock()
	var j regenerateJob
	if v := s.settings.Get(regenerateJobOption); v != nil {
		raw, _ := json.Marshal(v)
		_ = json.Unmarshal(raw, &j)
	}
	return j
}

func (s *Server) regeneratePersist(ctx context.Context, j regenerateJob) {
	_ = s.settings.Set(ctx, map[string]any{regenerateJobOption: j})
}

// regenerateAllAsync starts one background run over the whole library (continuing an interrupted
// one when resume is set); false when one is running.
func (s *Server) regenerateAllAsync(resume ...bool) bool {
	if !regenerateRunning.CompareAndSwap(false, true) {
		return false
	}
	regenerateStop.Store(false)
	ctx := context.Background()
	j := regenerateJob{Running: true, Started: time.Now(), Updated: time.Now()}
	if len(resume) > 0 && resume[0] {
		if prev := s.regenerateJobState(); prev.Running && !prev.Finished {
			j.LastID, j.Done, j.Skipped, j.Failed, j.Started = prev.LastID, prev.Done, prev.Skipped, prev.Failed, prev.Started
		}
	}
	ids, err := s.store.ImageAttachmentIDs(ctx)
	if err != nil {
		regenerateRunning.Store(false)
		return false
	}
	var todo []int64
	for _, id := range ids {
		if id > j.LastID {
			todo = append(todo, id)
		}
	}
	j.Total = j.Done + j.Skipped + j.Failed + len(todo)
	regenerateMu.Lock()
	regenerateLive = &j
	regenerateMu.Unlock()
	s.regeneratePersist(ctx, j)
	go func() {
		defer regenerateRunning.Store(false)
		for _, id := range todo {
			if regenerateStop.Load() {
				break
			}
			var rerr error
			a, err := s.store.GetAttachment(ctx, id)
			if err == nil {
				rerr = s.regenerateAttachment(ctx, a)
			}
			regenerateMu.Lock()
			switch {
			case err != nil:
				j.Skipped++ // deleted while the job ran
			case rerr == nil:
				j.Done++
			case errors.Is(rerr, errRegenerateSkipped):
				j.Skipped++
			default:
				j.Failed++
			}
			j.LastID, j.Updated = id, time.Now()
			snap := j
			regenerateMu.Unlock()
			s.regeneratePersist(ctx, snap)
		}
		regenerateMu.Lock()
		j.Running, j.Stopped, j.Finished, j.Updated = false, regenerateStop.Load(), !regenerateStop.Load(), time.Now()
		snap := j
		regenerateLive = nil
		regenerateMu.Unlock()
		s.regeneratePersist(ctx, snap)
		slog.Info("regenerate thumbnails finished", "done", snap.Done, "skipped", snap.Skipped, "failed", snap.Failed, "stopped", snap.Stopped)
	}()
	return true
}

// regenerateResume continues a "Regenerate all" run that a restart interrupted (stored as running).
func (s *Server) regenerateResume() {
	if j := s.regenerateJobState(); j.Running && !j.Finished && !regenerateRunning.Load() {
		slog.Info("resuming regenerate thumbnails", "done", j.Done, "last_id", j.LastID)
		s.regenerateAllAsync(true)
	}
}
