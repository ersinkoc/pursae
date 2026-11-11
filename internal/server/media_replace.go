package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"wpgo/internal/media"
	"wpgo/internal/store"
)

// Media Library: a new file for an existing attachment, and the attachment's focal point.
//
// Replacing a file keeps the attachment (its ID, so featured images, galleries, Custom Fields,
// Web Builder and Commerce references stay) and, when the new file has the same type, every URL:
// the new file is written under the old name, its sizes under the old size names. A new type
// (JPEG → PNG) or a different aspect ratio changes some names; the paths that went away are
// recorded in media_moved_files, redirected (serveUpload) and rewritten in page markup
// (mediaRefreshHTML), so links into the old files keep working.
//
// The focal point (meta focal_point, {x, y} in 0…1 like the block editor's focalPoint) is kept in
// view by the cropped sizes (media.CropRect), by images shown with object-fit (object-position on
// every <img> of the attachment) and by the image service's fit=cover.
//
// Uploads are served with "immutable" caching, so a file that changes under the same URL must get
// a new URL: every change stores a new meta file_version, and the attachment's URLs carry it as
// ?v=<version> (uploadURL, mediaRefreshHTML). Requests without the version revalidate instead.

const (
	mediaVersionMeta = "file_version"
	mediaFocalMeta   = "focal_point"
)

// mediaReplaceMu serialises file replacements and focal re-crops: one at a time is plenty for an
// editor action, and two on one attachment must not interleave.
var mediaReplaceMu sync.Mutex

// mediaTmpPrefix starts the work folders a replacement uses next to the attachment's files; the
// name can't collide with an upload (SanitizeFileName trims leading dots).
const mediaTmpPrefix = ".wpgo-replace-"

// mediaUploadPrefix names the replacement upload while it is checked (SaveIn drops a leading dot).
const mediaUploadPrefix = "wpgo-replace-upload"

// avMetaKeys are the attachment meta readAVMeta writes; a replaced audio/video file is read again.
var avMetaKeys = []string{"length", "length_formatted", "bitrate", "bitrate_mode", "width", "height", "fileformat", "artist", "album", "genre", "title"}

// editorMetaKeys are the image editor's state; a replaced file starts without it.
var editorMetaKeys = []string{"backup_file", "backup_size", "backup_variants", "edit_files"}

// Index -----------------------------------------------------------------------------------------

// mediaFileInfo is what the front end needs to know about one uploads path of an attachment that
// has a file version, a focal point or moved files.
type mediaFileInfo struct {
	ID    int64
	Ver   string       // the attachment's file version ("" none)
	Focal *media.Focal // the attachment's focal point (nil none)
	W, H  int          // the file's dimensions (0 unknown)
	Moved string       // set for a path the attachment no longer has: the path that took its place
}

// mediaIndex holds those paths in memory: uploadURL runs for every image of every page, so the
// lookup must not touch the database. It is loaded at start-up, updated by every change on this
// instance and reloaded when another instance purges the whole page cache (applyRemotePurge).
type mediaIndex struct {
	mu    sync.RWMutex
	files map[string]mediaFileInfo
	byID  map[int64][]string
}

func (x *mediaIndex) lookup(rel string) (mediaFileInfo, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	fi, ok := x.files[rel]
	return fi, ok
}

func (x *mediaIndex) empty() bool {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.files) == 0
}

// version is the file version of an uploads path ("" when it has none).
func (x *mediaIndex) version(rel string) string {
	if fi, ok := x.lookup(rel); ok && fi.Moved == "" {
		return fi.Ver
	}
	return ""
}

// put replaces the entries of one attachment (none: it is dropped).
func (x *mediaIndex) put(id int64, entries map[string]mediaFileInfo) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, p := range x.byID[id] {
		if fi, ok := x.files[p]; ok && fi.ID == id {
			delete(x.files, p)
		}
	}
	delete(x.byID, id)
	if len(entries) == 0 {
		return
	}
	if x.files == nil {
		x.files, x.byID = map[string]mediaFileInfo{}, map[int64][]string{}
	}
	for p, fi := range entries {
		x.files[p] = fi
		x.byID[id] = append(x.byID[id], p)
	}
}

func (x *mediaIndex) replaceAll(files map[string]mediaFileInfo) {
	byID := map[int64][]string{}
	for p, fi := range files {
		byID[fi.ID] = append(byID[fi.ID], p)
	}
	x.mu.Lock()
	x.files, x.byID = files, byID
	x.mu.Unlock()
}

// attachmentFocal is the attachment's focal point (nil when none is set or it is invalid).
func attachmentFocal(a *store.Attachment) *media.Focal {
	if a == nil {
		return nil
	}
	m, ok := a.Meta[mediaFocalMeta].(map[string]any)
	if !ok {
		return nil
	}
	x, xok := m["x"].(float64)
	y, yok := m["y"].(float64)
	f := media.Focal{X: x, Y: y}
	if !xok || !yok || !f.Valid() {
		return nil
	}
	return &f
}

// attachmentFileVersion is the attachment's file version ("" when its files never changed).
func attachmentFileVersion(a *store.Attachment) string {
	v, _ := a.Meta[mediaVersionMeta].(string)
	return v
}

// mediaIndexEntries are the index entries of an attachment and its moved paths.
func mediaIndexEntries(a *store.Attachment, moved []store.MovedFile) map[string]mediaFileInfo {
	ver, focal := attachmentFileVersion(a), attachmentFocal(a)
	if ver == "" && focal == nil && len(moved) == 0 {
		return nil
	}
	out := map[string]mediaFileInfo{}
	for _, p := range gocdnAttachmentFiles(a) {
		out[p] = mediaFileInfo{ID: a.ID, Ver: ver, Focal: focal}
	}
	main := out[a.FilePath]
	main.W, main.H = a.Width, a.Height
	out[a.FilePath] = main
	for _, v := range a.Variants {
		fi := out[v.FilePath]
		fi.W, fi.H = v.Width, v.Height
		out[v.FilePath] = fi
	}
	for _, m := range moved {
		if _, live := out[m.Path]; live {
			continue
		}
		t := out[m.Target]
		out[m.Path] = mediaFileInfo{ID: a.ID, Focal: focal, W: t.W, H: t.H, Moved: m.Target}
	}
	return out
}

// loadMediaIndex reads the whole index from the database.
func (s *Server) loadMediaIndex(ctx context.Context) error {
	if s.store == nil || s.store.Pool == nil {
		return nil // a server without a database (unit tests)
	}
	list, err := s.store.FileStateAttachments(ctx, nil)
	if err != nil {
		return err
	}
	moved, err := s.store.MovedFiles(ctx, nil)
	if err != nil {
		return err
	}
	byMedia := map[int64][]store.MovedFile{}
	for _, m := range moved {
		byMedia[m.MediaID] = append(byMedia[m.MediaID], m)
	}
	files := map[string]mediaFileInfo{}
	seen := map[int64]bool{}
	for _, a := range list {
		seen[a.ID] = true
		for p, fi := range mediaIndexEntries(a, byMedia[a.ID]) {
			files[p] = fi
		}
	}
	// Moved paths of an attachment without a version (cannot happen through a replace, which
	// always sets one, but rows may outlive a meta edit).
	var rest []int64
	for id := range byMedia {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	if len(rest) > 0 {
		if m, err := s.store.GetAttachments(ctx, rest); err == nil {
			for id, a := range m {
				for p, fi := range mediaIndexEntries(a, byMedia[id]) {
					files[p] = fi
				}
			}
		}
	}
	s.mediaIdx.replaceAll(files)
	return nil
}

// refreshMediaIndex reloads one attachment's entries (after a change, or to drop a deleted one).
func (s *Server) refreshMediaIndex(ctx context.Context, id int64) {
	if s.store == nil || s.store.Pool == nil {
		return
	}
	list, err := s.store.FileStateAttachments(ctx, []int64{id})
	if err != nil {
		slog.Warn("media index: reading an attachment", "id", id, "err", err)
		return
	}
	moved, _ := s.store.MovedFiles(ctx, []int64{id})
	if len(list) == 0 {
		if len(moved) == 0 {
			s.mediaIdx.put(id, nil)
			return
		}
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			s.mediaIdx.put(id, nil)
			return
		}
		list = []*store.Attachment{a}
	}
	s.mediaIdx.put(id, mediaIndexEntries(list[0], moved))
}

// uploadURLPlain is the URL of an uploads path without the file version (exports: the file name
// must stay the file name).
func (s *Server) uploadURLPlain(c context.Context, rel string) string {
	return s.links(c).HomeURL("/wp-content/uploads/" + rel)
}

// newMediaVersion is a fresh file version: the time in milliseconds, base 36.
func newMediaVersion() string { return strconv.FormatInt(time.Now().UnixMilli(), 36) }

// Rebuilding an attachment's files -----------------------------------------------------------------

// errMediaReplace* are the replacement's user-facing errors.
var (
	errMediaReplaceType  = errors.New("The new file must be of the same kind as the current one (an image for an image, a video for a video, a document for a document).")
	errMediaReplaceNames = errors.New("No free file name was found next to the current file.")
	errMediaReplaceLocal = errors.New("The current file is not on this server and could not be fetched from remote storage.")
)

// mediaKind is the top-level kind of a MIME type for replacement: image, audio, video, or file.
func mediaKind(mime string) string {
	switch t, _, _ := strings.Cut(mime, "/"); t {
	case "image", "audio", "video":
		return t
	}
	return "file"
}

// replaceSource is the file an attachment's sizes are made from and the name stem its files
// share: the original upload when a "-scaled", "-rotated" or converted main file was made from it,
// otherwise the main file (an edited image keeps its edited file).
func replaceSource(a *store.Attachment) (src, stem string) {
	dir := path.Dir(a.FilePath)
	main := path.Base(a.FilePath)
	stem = strings.TrimSuffix(main, path.Ext(main))
	_, edited := a.Meta["backup_file"]
	orig, _ := a.Meta["original_image"].(string)
	if orig != "" && !edited {
		return path.Join(dir, orig), strings.TrimSuffix(orig, path.Ext(orig))
	}
	if orig != "" || edited {
		for _, suf := range []string{"-scaled", "-rotated"} {
			stem = strings.TrimSuffix(stem, suf)
		}
	}
	return a.FilePath, stem
}

// cloneAttachment copies an attachment deeply enough to keep its old state (meta, sizes).
func cloneAttachment(a *store.Attachment) *store.Attachment {
	c := *a
	c.Variants = append([]store.AttachmentVariant(nil), a.Variants...)
	c.Meta = make(map[string]any, len(a.Meta))
	for k, v := range a.Meta {
		c.Meta[k] = v
	}
	return &c
}

// mediaRebuild describes one rebuild of an attachment's files.
type mediaRebuild struct {
	src     string // absolute path of the new source file
	srcMime string
	replace bool // a new file: the editor's backups, the old sub-sizes and the audio/video data go
}

// rebuildAttachmentFiles makes an attachment's files again from a source file: a replacement
// upload, or (focal point change) its own source. The new files are made in a work folder first;
// only when they all exist are the old ones moved aside and the new ones moved in, and the old
// ones come back when the database update fails. It returns a warning for the editor ("" none).
func (s *Server) rebuildAttachmentFiles(ctx context.Context, a *store.Attachment, rb mediaRebuild) (string, error) {
	mediaReplaceMu.Lock()
	defer mediaReplaceMu.Unlock()
	old := cloneAttachment(a)
	dir := path.Dir(a.FilePath)
	if dir == "." {
		dir = ""
	}
	pdir := path.Dir(path.Join(dir, "x")) // dir as path.Dir reports it for the files ("." for the root)
	dirAbs := filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(dir))
	if s.gocdnLocalPath(path.Join(dir, "x")) == "" {
		return "", errors.New("invalid attachment path")
	}
	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return "", err
	}
	mediaCleanStale(dirAbs)

	oldSrc, stem := replaceSource(a)
	ext := path.Ext(oldSrc)
	if rb.replace && media.MimeFor(oldSrc) != rb.srcMime {
		ext = strings.ToLower(filepath.Ext(rb.src)) // a new type: the stem stays, the extension follows the file
	}
	// The files this rebuild takes over: all of them for a new file; for a re-crop the image
	// editor's backups stay (Restore image keeps working).
	all := gocdnAttachmentFiles(old)
	oldFiles := all
	if !rb.replace {
		oldFiles = []string{old.FilePath}
		if orig, _ := old.Meta["original_image"].(string); orig != "" {
			oldFiles = append(oldFiles, path.Join(path.Dir(old.FilePath), orig))
		}
		for _, v := range old.Variants {
			oldFiles = append(oldFiles, v.FilePath)
		}
	}
	own := map[string]bool{}
	for _, p := range all {
		if path.Dir(p) == pdir {
			own[path.Base(p)] = true
		}
	}
	reserved := map[string]bool{}
	for _, n := range s.uploadReserved(ctx)(dir) {
		if !own[n] {
			reserved[strings.ToLower(n)] = true
		}
	}

	isImage := strings.HasPrefix(rb.srcMime, "image/")
	var opts media.Options
	if isImage {
		opts = s.imageEditorOptions(ctx, stem+ext, rb.srcMime)
		opts.Focal = attachmentFocal(a)
	}
	warning := ""
	var (
		tmpAbs string
		info   *media.ImageInfo
		names  []string
	)
	for try := 0; try < 20 && tmpAbs == ""; try++ {
		cand := stem
		if try > 0 {
			cand = stem + "-" + strconv.Itoa(try)
		}
		work, err := os.MkdirTemp(dirAbs, mediaTmpPrefix)
		if err != nil {
			return "", err
		}
		name := cand + ext
		rel := path.Join(dir, filepath.Base(work), name)
		if err := mediaCopyFile(rb.src, filepath.Join(work, name)); err != nil {
			os.RemoveAll(work)
			return "", err
		}
		info, warning = nil, ""
		if isImage {
			var perr error
			info, perr = media.ProcessImageWith(s.cfg.UploadsDir, rel, s.imageSizes(), opts)
			if perr != nil {
				slog.Warn("media replace: image sizes could not be generated", "id", a.ID, "err", perr)
				info, warning = nil, imageProcessWarning
			} else if len(info.Variants) == 0 && noSubsizeFormat(rb.srcMime) {
				warning = "No smaller image sizes can be generated for this file format, so the full-size file is used everywhere."
			}
		}
		if info == nil {
			info = &media.ImageInfo{RelPath: rel}
			if st, err := os.Stat(filepath.Join(work, name)); err == nil {
				info.FileSize = st.Size()
			}
		}
		entries, err := os.ReadDir(work)
		if err != nil {
			os.RemoveAll(work)
			return "", err
		}
		names = names[:0]
		var paths []string
		clash := false
		for _, e := range entries {
			n := e.Name()
			names = append(names, n)
			paths = append(paths, path.Join(dir, n))
			if own[n] {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dirAbs, n)); err == nil || reserved[strings.ToLower(n)] {
				clash = true
			}
		}
		if !clash {
			if others, err := s.store.MovedPathsOfOthers(ctx, paths, a.ID); err == nil && len(others) > 0 {
				clash = true
			}
		}
		if clash {
			os.RemoveAll(work)
			continue
		}
		tmpAbs = work
	}
	if tmpAbs == "" {
		return "", errMediaReplaceNames
	}
	defer os.RemoveAll(tmpAbs)

	// Swap: the old files move aside, the new ones in.
	backupAbs, err := os.MkdirTemp(dirAbs, mediaTmpPrefix+"old-")
	if err != nil {
		return "", err
	}
	var aside, placed []string
	restore := func() {
		for _, n := range placed {
			_ = os.Remove(filepath.Join(dirAbs, n))
		}
		for _, n := range aside {
			_ = os.Rename(filepath.Join(backupAbs, n), filepath.Join(dirAbs, n))
		}
		_ = os.RemoveAll(backupAbs)
	}
	moveAside := map[string]bool{}
	for _, p := range oldFiles {
		if path.Dir(p) == pdir {
			moveAside[path.Base(p)] = true
		}
	}
	for _, n := range names {
		if own[n] {
			moveAside[n] = true // a re-crop overwrites its own files; keep them to restore
		}
	}
	for n := range moveAside {
		if _, err := os.Lstat(filepath.Join(dirAbs, n)); err != nil {
			continue
		}
		if err := os.Rename(filepath.Join(dirAbs, n), filepath.Join(backupAbs, n)); err != nil {
			restore()
			return "", err
		}
		aside = append(aside, n)
	}
	for _, n := range names {
		if err := os.Rename(filepath.Join(tmpAbs, n), filepath.Join(dirAbs, n)); err != nil {
			restore()
			return "", err
		}
		placed = append(placed, n)
	}

	// The attachment's new state.
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	delete(a.Meta, "original_image")
	if rb.replace {
		for _, k := range editorMetaKeys {
			delete(a.Meta, k)
		}
		for _, k := range avMetaKeys {
			delete(a.Meta, k)
		}
	}
	a.FilePath = path.Join(dir, path.Base(info.RelPath))
	a.Width, a.Height, a.FileSize = info.Width, info.Height, info.FileSize
	a.Mime = rb.srcMime
	if info.OriginalFile != "" {
		a.Meta["original_image"] = info.OriginalFile
		if m := media.MimeFor(info.RelPath); m != "" {
			a.Mime = m // converted (image_editor_output_format)
		}
	}
	a.Meta["filesize"] = info.FileSize
	a.Meta[mediaVersionMeta] = newMediaVersion()
	a.Variants = nil
	for _, v := range info.Variants {
		a.Variants = append(a.Variants, store.AttachmentVariant{Name: v.Name, FilePath: path.Join(dir, v.File),
			Width: v.Width, Height: v.Height, Mime: v.Mime, FileSize: v.Size})
	}
	if rb.replace && (strings.HasPrefix(a.Mime, "audio/") || strings.HasPrefix(a.Mime, "video/")) {
		s.readAVMeta(s.cfg.UploadsDir, a)
	} else if rb.replace && !isImage {
		a.Width, a.Height = 0, 0
	}
	newFiles := gocdnAttachmentFiles(a)
	moves := mediaMoves(old, oldFiles, a)
	if err := s.store.ReplaceAttachmentFiles(ctx, a, moves, newFiles); err != nil {
		restore()
		*a = *old
		return "", err
	}
	_ = os.RemoveAll(backupAbs)
	newSet := map[string]bool{}
	for _, p := range newFiles {
		newSet[p] = true
	}
	for _, p := range oldFiles { // files outside the folder (none are made there, but meta may list them)
		if !newSet[p] && path.Dir(p) != pdir {
			if lp := s.gocdnLocalPath(p); lp != "" {
				_ = os.Remove(lp)
			}
		}
	}
	changed := append(append([]string(nil), oldFiles...), newFiles...)
	s.afterMediaFilesChanged(ctx, a, changed)
	return warning, nil
}

// mediaMoves maps every old path the attachment no longer has to the new path that takes its
// role: the main file to the main file, a size to the size of the same name (or the main file),
// the original upload to the new original (or the main file), editor backups to the main file.
func mediaMoves(old *store.Attachment, oldFiles []string, cur *store.Attachment) map[string]string {
	have := map[string]bool{}
	for _, p := range gocdnAttachmentFiles(cur) {
		have[p] = true
	}
	origOf := func(a *store.Attachment) string {
		if o, _ := a.Meta["original_image"].(string); o != "" {
			return path.Join(path.Dir(a.FilePath), o)
		}
		return ""
	}
	target := map[string]string{old.FilePath: cur.FilePath}
	if o := origOf(old); o != "" {
		target[o] = cur.FilePath
		if n := origOf(cur); n != "" {
			target[o] = n
		}
	}
	for _, v := range old.Variants {
		target[v.FilePath] = cur.FilePath
		if nv := cur.Variant(v.Name); nv != nil {
			target[v.FilePath] = nv.FilePath
		}
	}
	moves := map[string]string{}
	for _, p := range oldFiles {
		if have[p] {
			continue
		}
		t := target[p]
		if t == "" {
			t = cur.FilePath
		}
		if t != p {
			moves[p] = t
		}
	}
	return moves
}

// afterMediaFilesChanged updates everything that holds the attachment's old files: the index (new
// version, moved paths), Pursae CDN (the old objects go, the new files are offloaded, the CDN
// forgets the old URLs), the page cache (pages show the new URLs), and plugins.
func (s *Server) afterMediaFilesChanged(ctx context.Context, a *store.Attachment, changed []string) {
	s.refreshMediaIndex(ctx, a.ID)
	s.gocdnFilesChanged(a, changed)
	s.gocdnOffloadAsync(a)
	s.purgeAll(ctx)
	s.announceMediaChange(ctx)
}

// gocdnFilesChanged drops the bucket copies of an attachment's changed paths (their objects and
// rows) and asks the CDN to forget their URLs; gocdnOffloadAsync then uploads the new files.
func (s *Server) gocdnFilesChanged(a *store.Attachment, changed []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	items, err := s.gocdnItemsForMedia(ctx, a.ID)
	if err != nil || len(items) == 0 {
		return
	}
	want := map[string]bool{}
	for _, p := range changed {
		want[p] = true
	}
	st := s.gocdnLoad()
	var urls []string
	for _, it := range items {
		if !want[it.Path] {
			continue
		}
		urls = append(urls, gocdnItemURL(st, it))
		if cl, err := s.gocdnItemClient(st, it); err == nil {
			if err := cl.Delete(ctx, it.Key); err != nil {
				slog.Warn("gocdn: deleting a replaced object", "key", it.Key, "err", err)
				s.gocdnAddPendingDelete(ctx, it)
			}
		}
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM gocdn_items WHERE id = $1`, it.ID)
	}
	if s.gocdnOn() && len(gocdnPurgeTargets(st)) > 0 && st.CFAuto && len(urls) > 0 {
		s.gocdnQueuePurge(st, urls)
	}
}

// mediaCopyFile copies src to a new file dst.
func mediaCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}

// mediaCleanStale removes work folders a crashed replacement left in dir (older than an hour).
func mediaCleanStale(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), mediaTmpPrefix) && !strings.HasPrefix(e.Name(), mediaUploadPrefix) {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// Replace ----------------------------------------------------------------------------------------

// replaceAttachment stores r (named filename) as the attachment's new file. The upload passes
// the same checks as any upload (allowed type, content matching the extension, SVG sanitizing,
// size limit) and must be of the same kind as the current file.
func (s *Server) replaceAttachment(ctx context.Context, a *store.Attachment, filename string, r io.Reader) (string, error) {
	if media.MimeFor(filename) == "" {
		return "", media.ErrType
	}
	dir := path.Dir(a.FilePath)
	if dir == "." {
		dir = ""
	}
	st, err := media.SaveIn(s.cfg.UploadsDir, dir, mediaUploadPrefix+strings.ToLower(filepath.Ext(filename)), io.LimitReader(r, maxUploadBytes+1), s.uploadReserved(ctx))
	if err != nil {
		return "", err
	}
	upload := filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(st.RelPath))
	defer os.Remove(upload)
	switch {
	case st.Size > maxUploadBytes:
		return "", errUploadTooLarge
	case st.Size == 0:
		return "", errUploadEmpty
	case mediaKind(st.Mime) != mediaKind(a.Mime):
		return "", errMediaReplaceType
	}
	oldURL := s.uploadURLPlain(ctx, a.FilePath)
	warning, err := s.rebuildAttachmentFiles(ctx, a, mediaRebuild{src: upload, srcMime: st.Mime, replace: true})
	if err != nil {
		return "", err
	}
	s.mediaHook(ctx, "pursae_media_replaced", a, map[string]any{"old_url": oldURL, "new_url": s.uploadURLPlain(ctx, a.FilePath),
		"file": a.FilePath, "mime_type": a.Mime, "original_name": filepath.Base(filename)})
	return warning, nil
}

// setAttachmentFocal stores (or with nil clears) an attachment's focal point and, when the
// registered sizes include cropped ones, makes the image's sizes again around it.
func (s *Server) setAttachmentFocal(ctx context.Context, a *store.Attachment, f *media.Focal) error {
	if !strings.HasPrefix(a.Mime, "image/") {
		return nil
	}
	cur := attachmentFocal(a)
	if f != nil && f.Centered() {
		f = nil // the centre is the default: nothing to store
	}
	if (cur == nil) == (f == nil) && (f == nil || *cur == *f) {
		return nil
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	var val any
	if f != nil {
		val = map[string]any{"x": round4(f.X), "y": round4(f.Y)}
		a.Meta[mediaFocalMeta] = val
	} else {
		delete(a.Meta, mediaFocalMeta)
	}
	if err := s.store.SetAttachmentMeta(ctx, a.ID, map[string]any{mediaFocalMeta: val}); err != nil {
		return err
	}
	cropped := false
	for _, sz := range s.imageSizes() {
		cropped = cropped || sz.Crop
	}
	rebuilt := false
	if cropped && a.Mime != "image/svg+xml" && !noSubsizeFormat(a.Mime) {
		src, _ := replaceSource(a)
		if err := s.gocdnEnsureLocal(ctx, src); err != nil {
			slog.Warn("focal point: the source file is not available", "id", a.ID, "file", src, "err", err)
		} else if _, err := s.rebuildAttachmentFiles(ctx, a, mediaRebuild{src: s.gocdnLocalPath(src), srcMime: mediaMimeOf(src, a.Mime)}); err != nil {
			slog.Warn("focal point: sizes could not be made again", "id", a.ID, "err", err)
		} else {
			rebuilt = true
		}
	}
	if !rebuilt {
		// No file changed, but pages carry object-position: the index and the cache follow.
		s.refreshMediaIndex(ctx, a.ID)
		s.purgeAll(ctx)
		s.announceMediaChange(ctx)
	}
	var fv any
	if f != nil {
		fv = map[string]any{"x": round4(f.X), "y": round4(f.Y)}
	}
	s.mediaHook(ctx, "pursae_media_focal_point", a, map[string]any{"focal_point": fv})
	return nil
}

func round4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}

// mediaMimeOf is the MIME type of an uploads path by its extension, else fallback.
func mediaMimeOf(rel, fallback string) string {
	if m := media.MimeFor(rel); m != "" {
		return m
	}
	return fallback
}

// mediaHook fires a media action for plugins: the attachment ID and a details map with its URL.
func (s *Server) mediaHook(ctx context.Context, name string, a *store.Attachment, details map[string]any) {
	if s.plugins == nil || !s.plugins.hooks.HasAction(name) {
		return
	}
	details["id"] = a.ID
	details["url"] = s.uploadURL(ctx, a.FilePath)
	s.plugins.hooks.DoAction(ctx, name, a.ID, details)
}

// parseFocal reads a focal point given as "x,y" (fractions, or percentages with %), or "" / "reset"
// for none. ok is false for anything else.
func parseFocal(v string) (*media.Focal, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "reset" || v == "none" {
		return nil, true
	}
	xs, ys, found := strings.Cut(v, ",")
	if !found {
		return nil, false
	}
	num := func(s string) (float64, bool) {
		s = strings.TrimSpace(s)
		pct := strings.HasSuffix(s, "%")
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil {
			return 0, false
		}
		if pct {
			f /= 100
		}
		return f, true
	}
	x, ok1 := num(xs)
	y, ok2 := num(ys)
	f := media.Focal{X: x, Y: y}
	if !ok1 || !ok2 || !f.Valid() {
		return nil, false
	}
	return &f, true
}

// focalFromJSON reads a focal point from a decoded JSON value: {"x": 0.3, "y": 0.2}, null for
// none, or the "x,y" string form.
func focalFromJSON(v any) (*media.Focal, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case string:
		return parseFocal(t)
	case map[string]any:
		x, xok := t["x"].(float64)
		y, yok := t["y"].(float64)
		f := media.Focal{X: x, Y: y}
		if !xok || !yok || !f.Valid() {
			return nil, false
		}
		return &f, true
	}
	return nil, false
}

// focalJSON is the REST / modal form of an attachment's focal point (nil when none).
func focalJSON(a *store.Attachment) any {
	if f := attachmentFocal(a); f != nil {
		return map[string]any{"x": f.X, "y": f.Y}
	}
	return nil
}

func mediaErrText(err error) string {
	switch {
	case errors.Is(err, errUploadTooLarge):
		return "The uploaded file exceeds the maximum upload size for this site."
	case errors.Is(err, errUploadEmpty):
		return "The uploaded file is empty."
	}
	return fmt.Sprint(err)
}
