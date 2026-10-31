package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/gocdn"
	"wpgo/internal/media"
	"wpgo/internal/store"
)

// Media offload: every file of an attachment (the main file, its generated sizes, the original of
// a "-scaled" image and the image editor's backup) is copied to the bucket when it is uploaded,
// tracked in gocdn_items, optionally deleted from the server, fetched back when the server needs it
// (the image editor) and deleted from the bucket with the attachment.

// gocdnItem is a row of gocdn_items.
type gocdnItem struct {
	ID           int64
	MediaID      int64
	Path         string // relative to the uploads directory
	Provider     string
	Region       string
	Bucket       string
	Key          string
	Size         int64
	Mime         string
	LocalRemoved bool
	Endpoint     string // the S3 endpoint it was written to ("" for rows older than 0023_gocdn_more)
}

const gocdnItemCols = `id, coalesce(media_id, 0), path, provider, region, bucket, object_key, size, mime_type, local_removed, endpoint`

func scanGocdnItems(rows pgx.Rows) ([]*gocdnItem, error) {
	defer rows.Close()
	var out []*gocdnItem
	for rows.Next() {
		it := &gocdnItem{}
		if err := rows.Scan(&it.ID, &it.MediaID, &it.Path, &it.Provider, &it.Region, &it.Bucket, &it.Key, &it.Size, &it.Mime, &it.LocalRemoved, &it.Endpoint); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// gocdnItemsByPaths looks up offloaded files by their uploads path.
func (s *Server) gocdnItemsByPaths(ctx context.Context, paths []string) (map[string]*gocdnItem, error) {
	out := map[string]*gocdnItem{}
	if len(paths) == 0 {
		return out, nil
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT `+gocdnItemCols+` FROM gocdn_items WHERE path = ANY($1)`, paths)
	if err != nil {
		return nil, err
	}
	list, err := scanGocdnItems(rows)
	for _, it := range list {
		out[it.Path] = it
	}
	return out, err
}

func (s *Server) gocdnItemByPath(ctx context.Context, p string) *gocdnItem {
	m, err := s.gocdnItemsByPaths(ctx, []string{p})
	if err != nil {
		return nil
	}
	return m[p]
}

func (s *Server) gocdnItemsForMedia(ctx context.Context, id int64) ([]*gocdnItem, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+gocdnItemCols+` FROM gocdn_items WHERE media_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	return scanGocdnItems(rows)
}

func (s *Server) gocdnSaveItem(ctx context.Context, it *gocdnItem) error {
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO gocdn_items (media_id, path, provider, region, bucket, object_key, size, mime_type, local_removed, endpoint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (path) DO UPDATE SET media_id = EXCLUDED.media_id, provider = EXCLUDED.provider, region = EXCLUDED.region,
			bucket = EXCLUDED.bucket, object_key = EXCLUDED.object_key, size = EXCLUDED.size, mime_type = EXCLUDED.mime_type,
			local_removed = EXCLUDED.local_removed, endpoint = EXCLUDED.endpoint`,
		nullable64(it.MediaID), it.Path, it.Provider, it.Region, it.Bucket, it.Key, it.Size, it.Mime, it.LocalRemoved, it.Endpoint)
	return err
}

// gocdnItemURL is the delivery URL of an offloaded file. Items of another bucket than the
// configured one (the storage settings were changed and the move job has not run) use the
// connection they were written with (gocdnConnFor): its delivery domain, or its public bucket URL.
func gocdnItemURL(st gocdnSettings, it *gocdnItem) string {
	if !gocdnItemOnCurrent(st, it) {
		return gocdnObjectURL(gocdnConnFor(st, it), it.Key)
	}
	return gocdnObjectURL(st, it.Key)
}

// gocdnLocalPath is the file of an uploads path, or "" when the path tries to leave the folder.
func (s *Server) gocdnLocalPath(rel string) string {
	clean := path.Clean("/" + strings.ReplaceAll(rel, "\\", "/"))
	if clean == "/" || strings.Contains(clean, "/../") || strings.HasPrefix(clean, "/..") {
		return ""
	}
	return filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
}

// gocdnAttachmentFiles are the uploads paths of an attachment.
func gocdnAttachmentFiles(a *store.Attachment) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(a.FilePath)
	for _, v := range a.Variants {
		add(v.FilePath)
	}
	if orig, ok := a.Meta["original_image"].(string); ok && orig != "" {
		add(path.Join(path.Dir(a.FilePath), orig))
	}
	if b, ok := a.Meta["backup_file"].(string); ok {
		add(b)
	}
	if raw, ok := a.Meta["backup_variants"].(string); ok && raw != "" {
		var vs []store.AttachmentVariant
		if json.Unmarshal([]byte(raw), &vs) == nil {
			for _, v := range vs {
				add(v.FilePath)
			}
		}
	}
	return out
}

func gocdnMime(rel string) string {
	if m := media.MimeFor(rel); m != "" {
		return m
	}
	if m := mime.TypeByExtension(path.Ext(rel)); m != "" {
		return m
	}
	return "application/octet-stream"
}

// gocdnUploadFile copies one local file to the bucket and records it.
func (s *Server) gocdnUploadFile(ctx context.Context, st gocdnSettings, cl *gocdn.Client, mediaID int64, rel string) error {
	local := s.gocdnLocalPath(rel)
	if local == "" {
		return fmt.Errorf("%s: invalid path", rel)
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	key := gocdnObjectKey(st, rel)
	opts := gocdnUploadOptions(st, rel)
	if _, err := cl.Upload(ctx, key, f, fi.Size(), opts); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	return s.gocdnSaveItem(ctx, &gocdnItem{MediaID: mediaID, Path: rel, Provider: st.Provider, Region: gocdnRegion(st), Bucket: st.Bucket,
		Key: key, Size: fi.Size(), Mime: opts.ContentType, Endpoint: gocdnEndpoint(st)})
}

// gocdnOffloadAttachment copies the files of an attachment that are not in the bucket yet and,
// with "Remove files from the server", deletes the local copies once all of them are there.
func (s *Server) gocdnOffloadAttachment(ctx context.Context, st gocdnSettings, cl *gocdn.Client, a *store.Attachment) (int, error) {
	files := gocdnAttachmentFiles(a)
	have, err := s.gocdnItemsByPaths(ctx, files)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, rel := range files {
		if it := have[rel]; it != nil && gocdnItemOnCurrent(st, it) {
			if it.MediaID == 0 { // a remote import that became an attachment
				_, _ = s.store.Pool.Exec(ctx, `UPDATE gocdn_items SET media_id = $2 WHERE id = $1`, it.ID, a.ID)
			}
			continue
		}
		if _, err := os.Stat(s.gocdnLocalPath(rel)); errors.Is(err, os.ErrNotExist) && rel != a.FilePath {
			continue // an editor backup or size that is gone
		}
		if err := s.gocdnUploadFile(ctx, st, cl, a.ID, rel); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	if err := errors.Join(errs...); err != nil {
		return n, err
	}
	if st.RemoveLocal {
		s.gocdnRemoveLocal(ctx, a.ID)
	}
	return n, nil
}

// gocdnRemoveLocal deletes the local copies of an attachment's offloaded files.
func (s *Server) gocdnRemoveLocal(ctx context.Context, mediaID int64) {
	items, err := s.gocdnItemsForMedia(ctx, mediaID)
	if err != nil {
		return
	}
	for _, it := range items {
		if it.LocalRemoved {
			continue
		}
		if p := s.gocdnLocalPath(it.Path); p != "" {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
		_, _ = s.store.Pool.Exec(ctx, `UPDATE gocdn_items SET local_removed = true WHERE id = $1`, it.ID)
	}
}

// gocdnAfterUpload is called after an attachment's files were written (upload, sideload, image
// editor): with "Copy files to the bucket" on, they are offloaded right away. Failures are logged;
// the bulk offload picks the attachment up later.
func (s *Server) gocdnAfterUpload(ctx context.Context, a *store.Attachment) {
	if a == nil || !s.gocdnOn() {
		return
	}
	st := s.gocdnLoad()
	if !st.CopyToBucket {
		return
	}
	cl, err := s.gocdnClient(st)
	if err != nil {
		return
	}
	if _, err := s.gocdnOffloadAttachment(ctx, st, cl, a); err != nil {
		slog.Warn("gocdn: offloading an upload", "media", a.ID, "err", err)
	}
}

// gocdnAfterUploadID is gocdnAfterUpload for an attachment ID (after new sizes were added).
func (s *Server) gocdnAfterUploadID(ctx context.Context, id int64) {
	if !s.gocdnOn() {
		return
	}
	if a, err := s.store.GetAttachment(ctx, id); err == nil {
		s.gocdnAfterUpload(ctx, a)
	}
}

// gocdnBeforeDelete is called by deleteAttachmentFiles: the attachment's objects leave the bucket
// (the rows go with the media row). The CDN is told to forget their URLs (automatic purge).
func (s *Server) gocdnBeforeDelete(a *store.Attachment) {
	if a == nil || !s.gocdnOn() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	items, err := s.gocdnItemsForMedia(ctx, a.ID)
	if err != nil || len(items) == 0 {
		return
	}
	st := s.gocdnLoad()
	var urls []string
	for _, it := range items {
		cl, err := s.gocdnItemClient(st, it)
		if err != nil {
			continue
		}
		if err := cl.Delete(ctx, it.Key); err != nil {
			slog.Warn("gocdn: deleting an object", "key", it.Key, "err", err)
			continue
		}
		urls = append(urls, gocdnItemURL(st, it))
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM gocdn_items WHERE id = $1`, it.ID)
	}
	if len(gocdnPurgeTargets(st)) > 0 && st.CFAuto && len(urls) > 0 {
		go s.gocdnCDNPurgeLogged(st, urls)
	}
}

// gocdnItemClient is the client for the bucket an item lives in: the configured one, or the
// connection it was written with after the storage settings changed (gocdnConnFor).
func (s *Server) gocdnItemClient(st gocdnSettings, it *gocdnItem) (*gocdn.Client, error) {
	if gocdnItemOnCurrent(st, it) {
		return s.gocdnClient(st)
	}
	return s.gocdnClient(gocdnConnFor(st, it))
}

// gocdnDownload writes an item's object to its local path.
func (s *Server) gocdnDownload(ctx context.Context, st gocdnSettings, it *gocdnItem) error {
	local := s.gocdnLocalPath(it.Path)
	if local == "" {
		return errors.New("invalid path")
	}
	cl, err := s.gocdnItemClient(st, it)
	if err != nil {
		return err
	}
	rc, _, err := cl.Get(ctx, it.Key)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return err
	}
	tmp := local + ".gocdn-tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(rc, 4<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, local)
}

// gocdnEnsureAttachmentLocal brings every removed file of an attachment back.
func (s *Server) gocdnEnsureAttachmentLocal(ctx context.Context, a *store.Attachment) {
	if a == nil || !s.gocdnTracking() {
		return
	}
	for _, rel := range gocdnAttachmentFiles(a) {
		_ = s.gocdnEnsureLocal(ctx, rel)
	}
}

// gocdnServeUpload answers /wp-content/uploads/<rel> for a file that is only in the bucket:
// a redirect to its delivery URL, a presigned URL (private buckets) or the file proxied
// through the site. Files on disk are left to the normal handler.
func (s *Server) gocdnServeUpload(w http.ResponseWriter, r *http.Request, st gocdnSettings, rel string) bool {
	local := s.gocdnLocalPath(rel)
	if local == "" {
		return false
	}
	if _, err := os.Stat(local); err == nil {
		return false
	}
	it := s.gocdnItemByPath(r.Context(), path.Clean("/" + rel)[1:])
	if it == nil {
		return false
	}
	switch st.LocalDelivery {
	case "signed":
		if cl, err := s.gocdnItemClient(st, it); err == nil {
			if u, err := cl.PresignGet(it.Key, time.Hour); err == nil {
				w.Header().Set("Cache-Control", "private, max-age=600")
				http.Redirect(w, r, u, http.StatusFound)
				return true
			}
		}
	case "redirect":
		if u := gocdnItemURL(st, it); u != "" && !gocdnItemSSEC(st, it) {
			w.Header().Set("Cache-Control", "public, max-age=3600")
			http.Redirect(w, r, u, http.StatusFound)
			return true
		}
	}
	s.gocdnProxy(w, r, st, it)
	return true
}

// gocdnItemSSEC reports whether an item's object is encrypted with a customer key (so it has no
// usable public URL).
func gocdnItemSSEC(st gocdnSettings, it *gocdnItem) bool {
	if gocdnItemOnCurrent(st, it) {
		return st.SSEType == "customer"
	}
	return gocdnConnFor(st, it).SSEType == "customer"
}

// gocdnProxy streams an object through the site with the headers of a local upload.
func (s *Server) gocdnProxy(w http.ResponseWriter, r *http.Request, st gocdnSettings, it *gocdnItem) {
	cl, err := s.gocdnItemClient(st, it)
	if err != nil {
		http.Error(w, "storage not configured", http.StatusBadGateway)
		return
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == `"gocdn-`+strconv.FormatInt(it.ID, 10)+`-`+strconv.FormatInt(it.Size, 10)+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	rc, obj, err := cl.Get(r.Context(), it.Key)
	if err != nil {
		if gocdn.IsNotFound(err) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "storage error", http.StatusBadGateway)
		return
	}
	defer rc.Close()
	h := w.Header()
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; media-src 'self'")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Content-Type-Options", "nosniff")
	ct := it.Mime
	if ct == "" {
		ct = obj.ContentType
	}
	h.Set("Content-Type", ct)
	h.Set("ETag", `"gocdn-`+strconv.FormatInt(it.ID, 10)+`-`+strconv.FormatInt(it.Size, 10)+`"`)
	if obj.Size > 0 {
		h.Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, rc)
	}
}

// gocdnOffloadStats counts the library for the Media Offload screen.
type gocdnOffloadStats struct {
	Attachments, Offloaded, Files, Removed int
	Bytes                                  int64
}

func (s *Server) gocdnStats(ctx context.Context) gocdnOffloadStats {
	var st gocdnOffloadStats
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE EXISTS (SELECT 1 FROM gocdn_items g WHERE g.media_id = m.id AND g.path = m.file_path))
		FROM media m`).Scan(&st.Attachments, &st.Offloaded)
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE local_removed), coalesce(sum(size), 0) FROM gocdn_items`).
		Scan(&st.Files, &st.Removed, &st.Bytes)
	return st
}
