package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wpgo/internal/config"
)

func TestMediaDeletionRollbackAcrossLibraryAndCLI(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "serve-from-s3": {"1"}})
	body := []byte("keep this attachment")
	att := s.upload("referenced-media.txt", body)
	id := itoa64(int64(att["id"].(float64)))
	rel := gocdnRelOfURL(att["source_url"].(string))
	item := s.srv.gocdnItemByPath(t.Context(), rel)
	if item == nil {
		t.Fatal("offload mapping missing")
	}
	if _, err := s.srv.store.Pool.Exec(t.Context(), `CREATE TABLE referenced_media (id bigint REFERENCES media(id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.store.Pool.Exec(t.Context(), `INSERT INTO referenced_media VALUES ($1)`, item.MediaID); err != nil {
		t.Fatal(err)
	}
	_, list, _ := get(t, s.c, s.ts.URL+"/wp-admin/upload?mode=list")
	form := url.Values{"mode": {"list"}, "action": {"delete"}, "_wpnonce": {formNonce(t, list)}, "media[]": {id}}
	code, _, next := get(t, s.c, s.ts.URL+"/wp-admin/upload?"+form.Encode())
	if code != http.StatusFound || !strings.Contains(next, "deleted=0&delete-failed=1") {
		t.Fatalf("bulk deletion outcome: %d %q", code, next)
	}
	assertKept := func() {
		t.Helper()
		data, err := os.ReadFile(s.srv.gocdnLocalPath(rel))
		remote := s.fake.Object(item.Bucket, item.Key)
		if err != nil || string(data) != string(body) || remote == nil || string(remote.Body) != string(body) {
			t.Fatalf("failed deletion lost bytes: local=%q err=%v remote=%+v", data, err, remote)
		}
	}
	assertKept()
	cfg := s.srv.cfg
	cfg.DatabaseURL = config.Env("TEST_DB")
	cfg.HubURL = "off"
	out, errors, code := mediaCLIRun(t, cfg, "media", "delete", id, "0"+id, "--yes")
	if code == 0 || !strings.Contains(out+errors, "failed deleting 1 of 1") {
		t.Fatalf("CLI rejection: %d %q %q", code, out, errors)
	}
	assertKept()
	if _, err := s.srv.store.Pool.Exec(t.Context(), `DROP TABLE referenced_media`); err != nil {
		t.Fatal(err)
	}
	out, errors, code = mediaCLIRun(t, cfg, "media", "delete", id, id, "--yes")
	if code != 0 || !strings.Contains(out, "Deleted 1 media item(s).") {
		t.Fatalf("CLI retry: %d %q %q", code, out, errors)
	}
	if _, err := os.Stat(s.srv.gocdnLocalPath(rel)); !os.IsNotExist(err) || s.fake.Object(item.Bucket, item.Key) != nil {
		t.Fatalf("successful retry left a copy: %v", err)
	}
}

func TestGoCDNHousekeepingResumesDetachedDeletesWithoutRepeatingInFlightWork(t *testing.T) {
	s := gocdnSite(t)
	const name = "cleanup-in-progress.txt"
	reached, release := make(chan struct{}, 1), make(chan struct{})
	var calls atomic.Int32
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/"+name) && calls.Add(1) == 1 {
			reached <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		s.fake.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(gateway.Close)
	s.configure(url.Values{"endpoint": {gateway.URL}})
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "serve-from-s3": {"1"}})
	detach := func(name string) *gocdnItem {
		t.Helper()
		att := s.upload(name, []byte("removed media"))
		items, err := s.srv.detachOffloadsAndDeleteAttachment(t.Context(), int64(att["id"].(float64)))
		if err != nil || len(items) != 1 {
			t.Fatalf("detach %s: %v %v", name, items, err)
		}
		return items[0]
	}
	running := detach(name)
	interrupted := detach("cleanup-interrupted.txt")
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.srv.gocdnDeleteItems(ctx, []*gocdnItem{running})
		close(done)
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("storage delete did not start")
	}
	s.srv.gocdnHousekeepingOnce(ctx)
	if calls.Load() != 1 || s.fake.Object(running.Bucket, running.Key) == nil {
		t.Fatal("housekeeping repeated an in-flight deletion")
	}
	if s.fake.Object(interrupted.Bucket, interrupted.Key) != nil || s.srv.gocdnItemByPath(ctx, interrupted.Path) != nil {
		t.Fatal("housekeeping missed a committed detachment")
	}
	unblock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("storage delete did not complete")
	}
	if s.fake.Object(running.Bucket, running.Key) != nil || s.srv.gocdnItemByPath(ctx, running.Path) != nil {
		t.Fatal("foreground cleanup left the object or mapping")
	}
}
