package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// Media › Optimization (off by default). With it on, every JPEG, PNG and WebP image uploaded is
// written as WebP at the chosen quality, its sizes included. The upload is converted only when that
// makes it smaller (an image over the big-image threshold is scaled down anyway); otherwise it stays
// as uploaded and only its sizes are re-encoded. The uploaded file is kept as the original image
// (WordPress' original_image, what "Regenerate thumbnails" starts from), unless the screen says to
// keep only the optimized file, in which case it is deleted once the optimized one is in place.
//
// "Optimize existing images" runs the same over the library in the background: each image is made
// again from its original (or main) file, URLs of files that went away (photo-300x200.jpg →
// photo-300x200.webp) answer with a redirect to their replacement (media_moved_files, as after
// Replace media), and Pursae CDN and the page cache are told. Edited images (image editor backups),
// SVG, GIF, AVIF/HEIC and protected files are left alone. Each image is marked in its meta
// (pursae_optimized) with its size before and after, or why it was skipped, so it is done once.

const (
	mediaOptimizeOption         = "pursae_media_optimize"                  // "1": optimize uploads
	mediaOptimizeDiscardOption  = "pursae_media_optimize_discard_original" // "1": keep only the optimized file
	mediaOptimizeQualityOption  = "pursae_media_optimize_quality"          // 40–95, default 80
	mediaOptimizeJobOption      = "pursae_media_optimize_job"
	mediaOptimizedMeta          = "pursae_optimized"
	mediaOptimizeDefaultQuality = 80
)

var errOptimizeSkipped = errors.New("the image cannot be optimized")

func (s *Server) mediaOptimizeOn() bool { return s.settings.Bool(mediaOptimizeOption) }

func (s *Server) mediaOptimizeDiscard() bool { return s.settings.Bool(mediaOptimizeDiscardOption) }

func (s *Server) mediaOptimizeQuality() int {
	q := s.settings.Int(mediaOptimizeQualityOption)
	if q == 0 {
		return mediaOptimizeDefaultQuality
	}
	return min(95, max(40, q))
}

// mediaOptimizable reports whether an attachment's files are optimized: JPEG, PNG and WebP images
// outside the protected folder (a downloadable product is delivered byte for byte).
func mediaOptimizable(rel, mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/webp":
		return !mediaIsPrivateRel(rel)
	}
	return false
}

// optimizeOptions turns the image editor's options into the optimizing ones: WebP at the chosen
// quality, converted only when it is smaller (unless Settings › Media already asks for WebP always).
func (s *Server) optimizeOptions(opts *media.Options) {
	if opts.OutputFormat == nil {
		opts.OutputFormat = map[string]string{}
	}
	for _, m := range []string{"image/jpeg", "image/png", "image/webp"} {
		opts.OutputFormat[m] = "image/webp"
	}
	opts.Quality = s.mediaOptimizeQuality()
	opts.OnlyIfSmaller = s.settings.String("image_output_format") != "webp-all"
}

// optimizedMark is the pursae_optimized meta of an optimized image.
func optimizedMark(before, after int64, quality int) map[string]any {
	return map[string]any{"before": before, "after": after, "quality": quality, "at": time.Now().Unix()}
}

func skippedMark(reason string) map[string]any {
	return map[string]any{"skipped": reason, "at": time.Now().Unix()}
}

// discardOriginal deletes the kept upload of a freshly optimized image when only the optimized file
// is to be kept (the main file then stands for it). It reports whether it did.
func (s *Server) discardOriginal(a *store.Attachment) bool {
	orig, _ := a.Meta["original_image"].(string)
	if orig == "" || !s.mediaOptimizeDiscard() {
		return false
	}
	if _, edited := a.Meta["backup_file"]; edited {
		return false
	}
	p := path.Join(path.Dir(a.FilePath), orig)
	if p == a.FilePath {
		return false
	}
	delete(a.Meta, "original_image")
	_ = os.Remove(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(p)))
	return true
}

// optimizeAttachment optimizes one image of the library (Optimize existing images). It returns
// errOptimizeSkipped (and marks the image) when there is nothing it can do.
func (s *Server) optimizeAttachment(ctx context.Context, a *store.Attachment) error {
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	if _, done := a.Meta[mediaOptimizedMeta]; done {
		return errOptimizeSkipped
	}
	skip := func(reason string) error {
		a.Meta[mediaOptimizedMeta] = skippedMark(reason)
		if raw, err := json.Marshal(a.Meta); err == nil {
			_, _ = s.store.Pool.Exec(ctx, `UPDATE media SET meta = $2 WHERE id = $1`, a.ID, raw)
		}
		return errOptimizeSkipped
	}
	dir := path.Dir(a.FilePath)
	src := a.FilePath
	if orig, _ := a.Meta["original_image"].(string); orig != "" {
		src = path.Join(dir, orig)
	}
	mime := media.MimeFor(src)
	if mime == "" {
		mime = a.Mime
	}
	if !mediaOptimizable(a.FilePath, a.Mime) || !mediaOptimizable(src, mime) {
		return skip("format")
	}
	if _, edited := a.Meta["backup_file"]; edited {
		return skip("edited")
	}
	st, err := os.Stat(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(src)))
	if err != nil {
		return skip("not on this server")
	}
	before := st.Size()
	old := *a
	old.Meta = make(map[string]any, len(a.Meta))
	for k, v := range a.Meta {
		old.Meta[k] = v
	}
	old.Variants = append([]store.AttachmentVariant(nil), a.Variants...)
	oldFiles := gocdnAttachmentFiles(&old)

	opts := s.imageEditorOptions(ctx, path.Base(src), mime)
	s.optimizeOptions(&opts)
	opts.Reserved = s.uploadReserved(ctx)(strings.TrimPrefix(dir, "."))
	opts.Focal = attachmentFocal(a)
	info, err := media.ProcessImageWith(s.cfg.UploadsDir, src, s.imageSizes(), opts)
	if err != nil {
		slog.Warn("optimize: image could not be processed", "id", a.ID, "file", src, "err", err)
		return err
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
	if s.mediaOptimizeDiscard() && info.OriginalFile != "" {
		delete(a.Meta, "original_image") // the file goes below, with the other files it no longer has
	}
	a.Meta[mediaOptimizedMeta] = optimizedMark(before, info.FileSize, opts.Quality)
	a.Meta[mediaVersionMeta] = newMediaVersion() // sizes rewritten under their own names
	newFiles := gocdnAttachmentFiles(a)
	if err := s.store.ReplaceAttachmentFiles(ctx, a, mediaMoves(&old, oldFiles, a), newFiles); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, p := range newFiles {
		keep[p] = true
	}
	for _, p := range oldFiles {
		if !keep[p] {
			_ = os.Remove(filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(p)))
		}
	}
	changed := append(append([]string(nil), oldFiles...), newFiles...)
	s.refreshMediaIndex(ctx, a.ID)
	s.gocdnFilesChanged(a, changed)
	s.gocdnDropVariants(changed)
	s.gocdnOffloadAsync(a)
	return nil
}

// The "Optimize existing images" job: like "Regenerate all" (media_regenerate.go), its progress is
// kept in an option after every image, so a restart continues where it stopped.

var (
	optimizeRunning atomic.Bool
	optimizeStop    atomic.Bool
	optimizeMu      sync.Mutex
	optimizeLive    *regenerateJob
)

func (s *Server) optimizeJobState() regenerateJob {
	optimizeMu.Lock()
	if optimizeLive != nil {
		j := *optimizeLive
		optimizeMu.Unlock()
		return j
	}
	optimizeMu.Unlock()
	var j regenerateJob
	if v := s.settings.Get(mediaOptimizeJobOption); v != nil {
		raw, _ := json.Marshal(v)
		_ = json.Unmarshal(raw, &j)
	}
	return j
}

func (s *Server) optimizePersist(ctx context.Context, j regenerateJob) {
	_ = s.settings.Set(ctx, map[string]any{mediaOptimizeJobOption: j})
}

const mediaOptimizableWhere = `mime_type IN ('image/jpeg', 'image/png', 'image/webp') AND lower(file_path) NOT LIKE 'private/%'`

// optimizePendingIDs are the library images not optimized (or skipped) yet.
func (s *Server) optimizePendingIDs(ctx context.Context, after int64) ([]int64, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT id FROM media WHERE `+mediaOptimizableWhere+`
		AND NOT (coalesce(meta, '{}'::jsonb) ? '`+mediaOptimizedMeta+`') AND id > $1 ORDER BY id`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// mediaOptimizeStats is the library summary on Media › Optimization.
type mediaOptimizeStats struct {
	Images, Optimized, Skipped, Pending int
	Before, After                       int64
}

func (st mediaOptimizeStats) Saved() string {
	if st.Before <= st.After {
		return media.HumanSize(0)
	}
	return media.HumanSize(st.Before - st.After)
}

func (st mediaOptimizeStats) SavedPercent() int {
	if st.Before <= 0 || st.Before <= st.After {
		return 0
	}
	return int((st.Before - st.After) * 100 / st.Before)
}

func (s *Server) mediaOptimizeStats(ctx context.Context) mediaOptimizeStats {
	var st mediaOptimizeStats
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE m ? 'after'),
		count(*) FILTER (WHERE m ? 'skipped'),
		count(*) FILTER (WHERE m IS NULL),
		coalesce(sum((m->>'before')::bigint) FILTER (WHERE m ? 'after'), 0),
		coalesce(sum((m->>'after')::bigint) FILTER (WHERE m ? 'after'), 0)
		FROM (SELECT coalesce(meta, '{}'::jsonb)->'`+mediaOptimizedMeta+`' AS m FROM media
			WHERE `+mediaOptimizableWhere+` OR coalesce(meta, '{}'::jsonb) ? '`+mediaOptimizedMeta+`') t`).
		Scan(&st.Images, &st.Optimized, &st.Skipped, &st.Pending, &st.Before, &st.After)
	return st
}

// optimizeAllAsync starts one background run over the images not optimized yet (continuing an
// interrupted one when resume is set); false when one is running or nothing is to do.
func (s *Server) optimizeAllAsync(resume bool) bool {
	if !optimizeRunning.CompareAndSwap(false, true) {
		return false
	}
	optimizeStop.Store(false)
	ctx := context.Background()
	j := regenerateJob{Running: true, Started: time.Now(), Updated: time.Now()}
	if resume {
		if prev := s.optimizeJobState(); prev.Running && !prev.Finished {
			j.Done, j.Skipped, j.Failed, j.Started = prev.Done, prev.Skipped, prev.Failed, prev.Started
		}
	}
	todo, err := s.optimizePendingIDs(ctx, 0)
	if err != nil || len(todo) == 0 {
		optimizeRunning.Store(false)
		return false
	}
	j.Total = j.Done + j.Skipped + j.Failed + len(todo)
	optimizeMu.Lock()
	optimizeLive = &j
	optimizeMu.Unlock()
	s.optimizePersist(ctx, j)
	safeGo("media_optimize.optimizeAllAsync", func() {
		defer optimizeRunning.Store(false)
		for _, id := range todo {
			if optimizeStop.Load() {
				break
			}
			var oerr error
			a, err := s.store.GetAttachment(ctx, id)
			if err == nil {
				oerr = s.optimizeAttachment(ctx, a)
			}
			optimizeMu.Lock()
			switch {
			case err != nil:
				j.Skipped++ // deleted while the job ran
			case oerr == nil:
				j.Done++
			case errors.Is(oerr, errOptimizeSkipped):
				j.Skipped++
			default:
				j.Failed++
			}
			j.LastID, j.Updated = id, time.Now()
			snap := j
			optimizeMu.Unlock()
			s.optimizePersist(ctx, snap)
		}
		optimizeMu.Lock()
		j.Running, j.Stopped, j.Finished, j.Updated = false, optimizeStop.Load(), !optimizeStop.Load(), time.Now()
		snap := j
		optimizeLive = nil
		optimizeMu.Unlock()
		s.optimizePersist(ctx, snap)
		s.purgeAll(ctx) // pages show the new image URLs
		s.announceMediaChange(ctx)
		slog.Info("optimize images finished", "done", snap.Done, "skipped", snap.Skipped, "failed", snap.Failed, "stopped", snap.Stopped)
	})
	return true
}

// optimizeResume continues an "Optimize existing images" run that a restart interrupted.
func (s *Server) optimizeResume() {
	if j := s.optimizeJobState(); j.Running && !j.Finished && !optimizeRunning.Load() && s.mediaOptimizeOn() {
		slog.Info("resuming image optimization", "done", j.Done)
		s.optimizeAllAsync(true)
	}
}

// mediaOptimizationPage is Media › Optimization: the settings, the library summary and the
// "Optimize existing images" job.
func (s *Server) mediaOptimizationPage(c *gin.Context) {
	if !currentUser(c).Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to manage options for this site.")
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "media-optimization") {
			s.denyNonce(c)
			return
		}
		switch c.PostForm("action") {
		case "optimize-all":
			msg := "started"
			if !s.mediaOptimizeOn() {
				msg = "off"
			} else if !s.optimizeAllAsync(false) {
				msg = "nothing"
			}
			c.Redirect(http.StatusFound, "/wp-admin/media-optimization?optimize="+msg)
			return
		case "optimize-stop":
			optimizeStop.Store(true)
			c.Redirect(http.StatusFound, "/wp-admin/media-optimization?optimize=stopping")
			return
		}
		q, _ := strconv.Atoi(c.PostForm("quality"))
		if q == 0 {
			q = mediaOptimizeDefaultQuality
		}
		vals := map[string]any{
			mediaOptimizeOption:        boolOption(c.PostForm("enabled") == "1"),
			mediaOptimizeDiscardOption: boolOption(c.PostForm("original") == "discard"),
			mediaOptimizeQualityOption: min(95, max(40, q)),
		}
		if err := s.settings.Set(c, vals); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/media-optimization?error=1")
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/media-optimization?settings-updated=1")
		return
	}
	var notices []notice
	if c.Query("settings-updated") != "" {
		notices = append(notices, notice{Type: "success", Message: "Settings saved.", Dismissible: true})
	}
	if c.Query("error") != "" {
		notices = append(notices, notice{Type: "error", Message: "The settings could not be saved."})
	}
	switch c.Query("optimize") {
	case "started":
		notices = append(notices, notice{Type: "info", Message: "Optimizing the existing images in the background. You can leave this page.", Dismissible: true})
	case "nothing":
		notices = append(notices, notice{Type: "info", Message: "There are no images left to optimize, or a run is already going.", Dismissible: true})
	case "off":
		notices = append(notices, notice{Type: "warning", Message: "Turn on automatic optimization and save first.", Dismissible: true})
	case "stopping":
		notices = append(notices, notice{Type: "info", Message: "The run stops after the image it is working on.", Dismissible: true})
	}
	d := map[string]any{
		"Nonce":   s.nonce(c, "media-optimization"),
		"On":      s.mediaOptimizeOn(),
		"Discard": s.mediaOptimizeDiscard(),
		"Quality": s.mediaOptimizeQuality(),
		"WebPAll": s.settings.String("image_output_format") == "webp-all",
		"Stats":   s.mediaOptimizeStats(c),
		"Job":     s.optimizeJobState(),
		"Running": optimizeRunning.Load(),
	}
	s.adminPage(c, "media-optimization", "Media Optimization", "media-optimization-php", "admin/media-optimization", d, notices...)
}

func boolOption(on bool) string {
	if on {
		return "1"
	}
	return ""
}
