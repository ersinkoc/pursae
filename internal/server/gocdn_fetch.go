package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Fetch-back and safe deactivation.
//
// With "Remove files from the server", code that reads an upload from disk would find nothing.
// Every such place goes through gocdnFetchBack(ctx, path-on-disk): when the file is missing and
// GoCDN tracks it as removed, it is downloaded back from the bucket first (gocdnEnsureLocal) and
// the path is returned unchanged, so the call site stays `os.ReadFile(s.gocdnFetchBack(ctx, p))`.
// Users: the Customizer's site-icon sizes, theme export, WooCommerce-like downloads and PDF logo,
// contact-form mail attachments, the image editor (gocdnEnsureLocal). The image service and /wp-content/uploads read the bucket directly
// without writing to disk. A copy fetched back is deleted again after gocdnReRemoveDelay while its
// row still says "removed" (the "Download back to the server" job clears that and keeps it).
//
// Deactivation. Deactivating GoCDN while files exist only in the bucket would break their URLs, so:
//   - the Plugins screen's Deactivate link leads to a confirmation screen (page=deactivate) that
//     offers "Download back to the server, then deactivate" (the download job, deactivating when
//     it finished without failures) or "Deactivate and keep serving them from the bucket";
//   - any deactivation that leaves removed files (bulk action, the second choice) turns on the
//     uploads fallback (option gocdn_fallback): while GoCDN is off, /wp-content/uploads answers
//     those files from the bucket (redirect / presigned / proxy as configured) and fetch-back keeps
//     working. Nothing else of GoCDN runs while it is off (no rewriting, image service, screens).
//     The flag clears itself once no removed file is left (download job) or on activation.

const gocdnFallbackOption = "gocdn_fallback"

// gocdnReRemoveDelay is how long a fetched-back copy stays on disk.
var gocdnReRemoveDelay = 10 * time.Minute

// gocdnTracking reports whether removed files are still served/fetched: the plugin is on, or it
// was deactivated with files left only in the bucket.
func (s *Server) gocdnTracking() bool {
	return s.gocdnOn() || s.settings.Bool(gocdnFallbackOption)
}

// gocdnRelOf is the uploads path of a file on disk ("" outside the uploads directory).
func (s *Server) gocdnRelOf(p string) string {
	root, err := filepath.Abs(s.cfg.UploadsDir)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return filepath.ToSlash(rel)
}

// gocdnFetchBack makes sure a file of the uploads directory is on disk before it is read, fetching
// it back from the bucket when GoCDN removed it. It returns p, so it wraps the path at the call.
func (s *Server) gocdnFetchBack(ctx context.Context, p string) string {
	if _, err := os.Stat(p); err == nil || !s.gocdnTracking() {
		return p
	}
	if rel := s.gocdnRelOf(p); rel != "" {
		_ = s.gocdnEnsureLocal(ctx, rel)
	}
	return p
}

// gocdnEnsureLocal fetches a file back from the bucket when the server needs it and its local
// copy was removed; the copy is removed again later (gocdnScheduleReRemove).
func (s *Server) gocdnEnsureLocal(ctx context.Context, rel string) error {
	local := s.gocdnLocalPath(rel)
	if local == "" {
		return errors.New("invalid path")
	}
	if _, err := os.Stat(local); err == nil {
		return nil
	}
	if !s.gocdnTracking() {
		return os.ErrNotExist
	}
	it := s.gocdnItemByPath(ctx, rel)
	if it == nil {
		return os.ErrNotExist
	}
	if err := s.gocdnDownload(ctx, s.gocdnLoad(), it); err != nil {
		return err
	}
	if it.LocalRemoved {
		s.gocdnScheduleReRemove(it.Path)
	}
	return nil
}

// gocdnScheduleReRemove deletes a fetched-back copy after a while if its row still says the
// file was removed from the server.
func (s *Server) gocdnScheduleReRemove(rel string) {
	rt := s.gocdnRT()
	rt.mu.Lock()
	if rt.reRemove == nil {
		rt.reRemove = map[string]bool{}
	}
	if rt.reRemove[rel] {
		rt.mu.Unlock()
		return
	}
	rt.reRemove[rel] = true
	rt.mu.Unlock()
	time.AfterFunc(gocdnReRemoveDelay, func() { s.gocdnReRemove(rel) })
}

func (s *Server) gocdnReRemove(rel string) {
	rt := s.gocdnRT()
	rt.mu.Lock()
	delete(rt.reRemove, rel)
	rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if it := s.gocdnItemByPath(ctx, rel); it != nil && it.LocalRemoved {
		if p := s.gocdnLocalPath(rel); p != "" {
			_ = os.Remove(p)
		}
	}
}

// gocdnRemovedCount is the number of files that exist only in the bucket. Callers treat an error as
// "some": turning the uploads fallback off on a failed count would break every such file.
func (s *Server) gocdnRemovedCount(ctx context.Context) (int, error) {
	var n int
	err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gocdn_items WHERE local_removed`).Scan(&n)
	return n, err
}

// gocdnHasRemoved reports whether files may exist only in the bucket (true when the count fails).
func (s *Server) gocdnHasRemoved(ctx context.Context) bool {
	n, err := s.gocdnRemovedCount(ctx)
	return err != nil || n > 0
}

// gocdnDeactivate turns the plugin off, with the uploads fallback when files are left only in
// the bucket.
func (s *Server) gocdnDeactivate(ctx context.Context) error {
	if err := s.settings.Set(ctx, map[string]any{gocdnSetting: false, gocdnFallbackOption: s.gocdnHasRemoved(ctx)}); err != nil {
		return err
	}
	s.gocdnStopJob()
	s.purgeAll(ctx)
	return nil
}

// gocdnDeactivatePost handles the confirmation screen's buttons.
func (s *Server) gocdnDeactivatePost(c *gin.Context, do string) {
	switch do {
	case "deactivate-download":
		if err := s.gocdnStartJobWith(c, "download", false, gocdnJobOpts{ThenDeactivate: true}); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/gocdn?page=deactivate&gocdn-error="+url.QueryEscape(err.Error()))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/gocdn?page=offload&gocdn-msg="+url.QueryEscape("Downloading the files back to the server. Pursae CDN is deactivated when the job has finished without failures."))
	case "deactivate-keep":
		if err := s.gocdnDeactivate(c); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/gocdn?page=deactivate&gocdn-error="+url.QueryEscape(err.Error()))
			return
		}
		c.Redirect(http.StatusFound, "/wp-admin/plugins?deactivate=true")
	}
}
