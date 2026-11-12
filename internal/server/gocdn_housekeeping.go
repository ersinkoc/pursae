package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"wpgo/internal/store"
)

// Background offload of new uploads ------------------------------------------------------------

// gocdnOffloadRetryDelays are the waits before each retry of a failed offload of a new upload
// (tests shorten them).
// gocdnOffloadInline runs the offload in the caller (tests that read the bucket right after an upload).
var gocdnOffloadInline bool

var gocdnOffloadRetryDelays = []time.Duration{0, 5 * time.Second, 30 * time.Second, 2 * time.Minute}

// gocdnOffloadAsync offloads a new attachment after the request returned: the upload handler does
// not wait for the bucket, and a failure is retried with a growing delay instead of being logged
// once and forgotten. What still fails stays for the Media Offload screen's bulk job.
func (s *Server) gocdnOffloadAsync(a *store.Attachment) {
	if a == nil || !s.gocdnOn() || !s.gocdnLoad().CopyToBucket {
		return
	}
	id := a.ID
	run := func(f func()) { go f() }
	if gocdnOffloadInline {
		run = func(f func()) { f() }
	}
	run(func() {
		for i, d := range gocdnOffloadRetryDelays {
			time.Sleep(d)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			st := s.gocdnLoad()
			cur, err := s.store.GetAttachment(ctx, id)
			if err != nil || !s.gocdnOn() || !st.CopyToBucket {
				cancel()
				return // deleted meanwhile, or the plugin was turned off
			}
			cl, err := s.gocdnClient(st)
			if err == nil {
				_, err = s.gocdnOffloadAttachment(ctx, st, cl, cur)
			}
			if err == nil {
				s.gocdnClearFailure(ctx, id)
				cancel()
				return
			}
			slog.Warn("gocdn: offloading an upload", "media", id, "attempt", i+1, "err", err)
			if gocdnOffloadInline || i == len(gocdnOffloadRetryDelays)-1 {
				s.gocdnRecordFailure(ctx, id, err) // kept for the daily pass and the Retry failed button
				cancel()
				return
			}
			cancel()
		}
	})
}

// Failed offloads ---------------------------------------------------------------------------------

func (s *Server) gocdnRecordFailure(ctx context.Context, id int64, err error) {
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if _, e := s.store.Pool.Exec(ctx, `INSERT INTO gocdn_offload_failures(media_id, last_error) VALUES ($1,$2)
		ON CONFLICT (media_id) DO UPDATE SET attempts = gocdn_offload_failures.attempts + 1, last_error = EXCLUDED.last_error, updated_at = now()`, id, msg); e != nil {
		slog.Warn("gocdn: recording a failed offload", "media", id, "err", e)
	}
}

func (s *Server) gocdnClearFailure(ctx context.Context, id int64) {
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM gocdn_offload_failures WHERE media_id = $1`, id)
}

// gocdnFailedCount is the number of uploads waiting for another offload attempt.
func (s *Server) gocdnFailedCount(ctx context.Context) int {
	var n int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gocdn_offload_failures`).Scan(&n)
	return n
}

// gocdnRetryFailedUploads offloads the uploads whose earlier attempts failed (oldest first, up to limit);
// it returns how many succeeded and how many are still failing.
func (s *Server) gocdnRetryFailedUploads(ctx context.Context, limit int) (done, left int) {
	st := s.gocdnLoad()
	if !s.gocdnOn() || !st.CopyToBucket {
		return 0, s.gocdnFailedCount(ctx)
	}
	cl, err := s.gocdnClient(st)
	if err != nil {
		return 0, s.gocdnFailedCount(ctx)
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT media_id FROM gocdn_offload_failures ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return 0, 0
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		a, err := s.store.GetAttachment(ctx, id)
		if err != nil {
			s.gocdnClearFailure(ctx, id)
			continue
		}
		if _, err := s.gocdnOffloadAttachment(ctx, st, cl, a); err != nil {
			s.gocdnRecordFailure(ctx, id, err)
			continue
		}
		s.gocdnClearFailure(ctx, id)
		done++
	}
	return done, s.gocdnFailedCount(ctx)
}

// Objects without a record -----------------------------------------------------------------------

// gocdnOrphanLimit bounds one scan's result (the screen lists them; the next scan finds the rest).
const gocdnOrphanLimit = 500

// gocdnScanOrphans lists the objects under the uploads prefix of the configured bucket that no
// record points at (an upload interrupted before its row was written, a delete whose row went but
// whose object stayed, files put there by hand). The processed image cache is not part of it.
func (s *Server) gocdnScanOrphans(ctx context.Context) ([]string, error) {
	st := s.gocdnLoad()
	cl, err := s.gocdnClient(st)
	if err != nil {
		return nil, err
	}
	var out []string
	token := ""
	for {
		res, err := cl.List(ctx, st.Prefix, "", token, 1000)
		if err != nil {
			return out, err
		}
		var keys []string
		for _, o := range res.Objects {
			if strings.HasPrefix(o.Key, "gocdn-cache/") || strings.HasSuffix(o.Key, "/") {
				continue
			}
			keys = append(keys, o.Key)
		}
		if len(keys) > 0 {
			known := map[string]bool{}
			rows, err := s.store.Pool.Query(ctx, `SELECT object_key FROM gocdn_items WHERE bucket = $1 AND object_key = ANY($2)`, st.Bucket, keys)
			if err != nil {
				return out, err
			}
			for rows.Next() {
				var k string
				if rows.Scan(&k) == nil {
					known[k] = true
				}
			}
			rows.Close()
			for _, it := range s.gocdnPendingDeletes() {
				known[it.Key] = true
			}
			for _, k := range keys {
				if !known[k] {
					out = append(out, k)
					if len(out) >= gocdnOrphanLimit {
						return out, nil
					}
				}
			}
		}
		if res.NextToken == "" {
			return out, nil
		}
		token = res.NextToken
	}
}

// gocdnDeleteOrphans deletes the given keys from the bucket, skipping any that has a record by now.
func (s *Server) gocdnDeleteOrphans(ctx context.Context, keys []string) (int, error) {
	st := s.gocdnLoad()
	cl, err := s.gocdnClient(st)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, k := range keys {
		var have bool
		_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gocdn_items WHERE bucket = $1 AND object_key = $2)`, st.Bucket, k).Scan(&have)
		if have || !strings.HasPrefix(k, st.Prefix) {
			continue
		}
		if err := cl.Delete(ctx, k); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	return n, errors.Join(errs...)
}

// Processed image cache limit -------------------------------------------------------------------

var gocdnPruneBusy atomic.Bool
var gocdnPruneLast atomic.Int64

// gocdnMaybePrune trims the variant cache at most once a minute, in the background.
func (s *Server) gocdnMaybePrune(st gocdnSettings) {
	if st.ImgCacheMaxMB <= 0 {
		return
	}
	now := time.Now().Unix()
	if last := gocdnPruneLast.Load(); now-last < 60 || !gocdnPruneLast.CompareAndSwap(last, now) {
		return
	}
	if !gocdnPruneBusy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer gocdnPruneBusy.Store(false)
		gocdnPruneDir(s.gocdnCacheDir(), int64(st.ImgCacheMaxMB)<<20)
	}()
}

// gocdnPruneDir deletes the oldest files under dir until the total is within limit bytes.
// It returns the number of files removed.
func gocdnPruneDir(dir string, limit int64) int {
	type ent struct {
		path string
		size int64
		mod  time.Time
	}
	var files []ent
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			files = append(files, ent{p, fi.Size(), fi.ModTime()})
			total += fi.Size()
		}
		return nil
	})
	if total <= limit {
		return 0
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	n := 0
	for _, f := range files {
		if total <= limit {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
			n++
		}
	}
	return n
}

// gocdnTouch marks a cached variant as recently used so the prune keeps it (at most hourly).
func gocdnTouch(file string) {
	if fi, err := os.Stat(file); err == nil && time.Since(fi.ModTime()) > time.Hour {
		now := time.Now()
		_ = os.Chtimes(file, now, now)
	}
}

// Page purge on content changes ---------------------------------------------------------------

// gocdnPurgePostPages tells the CDNs to forget the post's page, the home page and the feed (queued
// and coalesced like every automatic purge). A CDN that caches HTML would otherwise serve the old
// page until its TTL ended.
func (s *Server) gocdnPurgePostPages(ctx context.Context, p *store.Post) {
	if p == nil || !s.gocdnOn() {
		return
	}
	st := s.gocdnLoad()
	if !st.CFAuto || len(gocdnPurgeTargets(st)) == 0 {
		return
	}
	lb := s.links(ctx)
	urls := []string{lb.HomeURL("/"), lb.FeedLink("")}
	if u := lb.Permalink(p); u != "" {
		urls = append(urls, u)
	}
	s.gocdnQueuePurge(st, urls)
}

// Housekeeping ------------------------------------------------------------------------------------

const gocdnPendingDeletesOption = "gocdn_pending_deletes"

var gocdnPendingMu sync.Mutex

func (s *Server) gocdnPendingDeletes() []gocdnItem {
	var out []gocdnItem
	if v := s.settings.Get(gocdnPendingDeletesOption); v != nil {
		raw, _ := json.Marshal(v)
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// gocdnAddPendingDelete remembers an object whose delete from the bucket failed although its row is
// going away with the attachment; gocdnRetryPendingDeletes tries again.
func (s *Server) gocdnAddPendingDelete(ctx context.Context, it *gocdnItem) {
	gocdnPendingMu.Lock()
	defer gocdnPendingMu.Unlock()
	list := append(s.gocdnPendingDeletes(), *it)
	if len(list) > 5000 {
		list = list[len(list)-5000:]
	}
	if err := s.settings.Set(ctx, map[string]any{gocdnPendingDeletesOption: list}); err != nil {
		slog.Warn("gocdn: saving a pending delete", "key", it.Key, "err", err)
	}
}

// gocdnRetryPendingDeletes deletes the objects whose earlier delete failed; it returns how many
// are still pending.
func (s *Server) gocdnRetryPendingDeletes(ctx context.Context) int {
	gocdnPendingMu.Lock()
	defer gocdnPendingMu.Unlock()
	list := s.gocdnPendingDeletes()
	if len(list) == 0 {
		return 0
	}
	st := s.gocdnLoad()
	var left []gocdnItem
	for i := range list {
		it := list[i]
		cl, err := s.gocdnItemClient(st, &it)
		if err == nil {
			err = cl.Delete(ctx, it.Key)
		}
		if err != nil {
			left = append(left, it)
		}
	}
	if len(left) != len(list) {
		var v any = left
		if len(left) == 0 {
			v = []gocdnItem{}
		}
		_ = s.settings.Set(ctx, map[string]any{gocdnPendingDeletesOption: v})
	}
	return len(left)
}

// gocdnCleanupLocal deletes files that still sit on disk although their row says they were removed
// from the server (a fetched-back copy whose timer was lost in a restart, a crash between marking
// and deleting). Files touched within the re-remove delay may be in use and are left alone.
func (s *Server) gocdnCleanupLocal(ctx context.Context) int {
	rows, err := s.store.Pool.Query(ctx, `SELECT path FROM gocdn_items WHERE local_removed`)
	if err != nil {
		return 0
	}
	var paths []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			paths = append(paths, p)
		}
	}
	rows.Close()
	n := 0
	for _, rel := range paths {
		p := s.gocdnLocalPath(rel)
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() || time.Since(fi.ModTime()) < gocdnReRemoveDelay {
			continue
		}
		if os.Remove(p) == nil {
			n++
		}
	}
	return n
}

// gocdnHousekeepingOnce is one pass: resume an interrupted job, retry pending deletes, clean up.
func (s *Server) gocdnHousekeepingOnce(ctx context.Context) {
	if !s.gocdnTracking() {
		return
	}
	s.gocdnResumeInterrupted(ctx)
	s.gocdnRetryPendingDeletes(ctx)
	s.gocdnRetryFailedUploads(ctx, 500)
	s.gocdnCleanupLocal(ctx)
}

// gocdnStartHousekeeping runs the pass shortly after start-up and then daily.
func (s *Server) gocdnStartHousekeeping(ctx context.Context) {
	go func() {
		t := time.NewTimer(45 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.gocdnHousekeepingOnce(ctx)
				t.Reset(24 * time.Hour)
			}
		}
	}()
}
