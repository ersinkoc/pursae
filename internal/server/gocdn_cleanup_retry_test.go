package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const gocdnCleanupTestPrefix = "wp-content/uploads/"

func TestGoCDNConcurrentRemoteLinksShareOneAttachment(t *testing.T) {
	s := gocdnSite(t)
	const name = "concurrent-link.txt"
	const key = gocdnCleanupTestPrefix + name
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/"+name) {
			arrived <- struct{}{}
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
	s.fake.PutObject("media", key, []byte("one linked object"), "text/plain")
	st := s.srv.gocdnLoad()
	client, err := s.srv.gocdnClient(st)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  int64
		err error
	}
	results := make(chan result, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	for range 2 {
		go func() {
			id, err := s.srv.gocdnImportLink(ctx, st, client, key, 1)
			results <- result{id, err}
		}()
	}
	// Both requests must pass the initial lookup before either is allowed to publish.
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("both link requests did not reach HEAD")
		}
	}
	unblock()
	left, right := <-results, <-results
	if left.err != nil {
		left, right = right, left
	}
	if left.err != nil || !errors.Is(right.err, errAlreadyInLibrary) || left.id == 0 || left.id != right.id {
		t.Fatalf("overlapping link results: %+v %+v", left, right)
	}
	var rows int
	err = s.srv.store.Pool.QueryRow(t.Context(), `SELECT count(*) FROM media WHERE file_path = $1`, name).Scan(&rows)
	item := s.srv.gocdnItemByPath(t.Context(), name)
	if err != nil || rows != 1 || item == nil || item.MediaID != left.id {
		t.Fatalf("rows=%d mapping=%+v err=%v", rows, item, err)
	}
}

func TestRESTMediaRejectedDeletePreservesFiles(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "serve-from-s3": {"1"}})
	uploaded := s.upload("hold-delete.txt", []byte("keep attachment contents"))
	id := int64(uploaded["id"].(float64))
	rel := gocdnRelOfURL(uploaded["source_url"].(string))
	target := "/wp-json/wp/v2/media/" + itoa64(id) + "?force=true"
	if _, err := s.srv.store.Pool.Exec(t.Context(), `CREATE TABLE hold_media_delete (id bigint REFERENCES media(id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.store.Pool.Exec(t.Context(), `INSERT INTO hold_media_delete VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	if code, body, _ := s.restJSON(http.MethodDelete, target, nil); code != http.StatusInternalServerError || body["code"] != "rest_cannot_delete" {
		t.Fatalf("delete while referenced: %d %v", code, body)
	}
	content, err := os.ReadFile(s.srv.gocdnLocalPath(rel))
	remote := s.fake.Object("media", gocdnCleanupTestPrefix+rel)
	if err != nil || string(content) != "keep attachment contents" || remote == nil || string(remote.Body) != string(content) {
		t.Fatalf("rejected delete changed the local or remote object: %q %v", content, err)
	}
}

func TestGoCDNPendingDeleteLeavesNewlyTrackedObject(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	s.pageForm("offload", url.Values{"copy-to-s3": {"1"}, "serve-from-s3": {"1"}})
	if err := s.srv.settings.Set(t.Context(), map[string]any{"uploads_use_yearmonth_folders": false}); err != nil {
		t.Fatal(err)
	}
	st := s.srv.gocdnLoad()
	const name = "republished.txt"
	const key = gocdnCleanupTestPrefix + name
	s.srv.gocdnAddPendingDelete(t.Context(), &gocdnItem{
		Path: name, Key: key, Bucket: st.Bucket, Provider: st.Provider, Endpoint: gocdnEndpoint(st),
	})
	newUpload := s.upload(name, []byte("replacement contents"))
	if rel := gocdnRelOfURL(newUpload["source_url"].(string)); rel != name {
		t.Fatalf("expected new upload at %s, got %s", name, rel)
	}
	if left := s.srv.gocdnRetryPendingDeletes(t.Context()); left != 0 {
		t.Fatalf("%d obsolete deletions still queued", left)
	}
	if object := s.fake.Object("media", key); object == nil || string(object.Body) != "replacement contents" {
		t.Fatal("the old deletion removed the replacement")
	}
}

func TestGoCDNOrphanRemovalRequiresSuccessfulLookup(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	const name = "owned.txt"
	const key = gocdnCleanupTestPrefix + name
	s.fake.PutObject("media", key, []byte("ordinary library attachment"), "text/plain")
	st := s.srv.gocdnLoad()
	client, err := s.srv.gocdnClient(st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.gocdnImportLink(t.Context(), st, client, key, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.srv.store.Pool.Exec(t.Context(), `ALTER TABLE gocdn_items RENAME TO unavailable_gocdn_items`); err != nil {
		t.Fatal(err)
	}
	n, err := s.srv.gocdnDeleteOrphans(t.Context(), []string{key})
	if err == nil || n != 0 || s.fake.Object("media", key) == nil {
		t.Fatalf("lookup error must preserve the object: removed=%d err=%v", n, err)
	}
}
