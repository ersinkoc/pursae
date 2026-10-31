package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"wpgo/internal/gocdn/s3fake"
)

// TestGoCDNEnvBucketChangeIsRemembered changes the bucket (another endpoint and credentials) only
// through WPGO_CDN_SETTINGS: the old connection is remembered, sealed, and files offloaded to the
// old bucket are still served from it with the old credentials.
func TestGoCDNEnvBucketChangeIsRemembered(t *testing.T) {
	s := gocdnSite(t)
	env := func(endpoint, bucket, key, secret string) string {
		b, _ := json.Marshal(map[string]any{"provider": "minio", "endpoint": endpoint, "region": "auto", "bucket": bucket, "access-key-id": key,
			"secret-access-key": secret, "use-path-style": true, "local-delivery": "proxy", "remove-local-file": true, "serve-from-s3": true})
		return string(b)
	}
	t.Setenv(gocdnEnv, env(s.fake.URL, "media", gocdnTestKey, gocdnTestSecret))
	if st := s.srv.gocdnLoad(); st.Bucket != "media" || len(st.Previous) != 0 {
		t.Fatalf("first load: %+v", st.Previous)
	}
	att := s.upload("env-old.png", gocdnTestImage(300, 200, "png"))
	id := int64(att["id"].(float64))
	rel := gocdnRelOfURL(att["source_url"].(string))
	oldKey := "wp-content/uploads/" + rel
	if s.fake.Object("media", oldKey) == nil {
		t.Fatal("not offloaded to the first bucket")
	}
	s.srv.gocdnRemoveLocal(context.Background(), id)

	// The deployment points WPGO_CDN_SETTINGS at a new bucket on another endpoint with other keys.
	fake2 := s3fake.New("AKNEWKEY", "new-secret/value", "auto", "media2")
	t.Cleanup(fake2.Close)
	t.Setenv(gocdnEnv, env(fake2.URL, "media2", "AKNEWKEY", "new-secret/value"))
	st := s.srv.gocdnLoad()
	if st.Bucket != "media2" || len(st.Previous) != 1 || st.Previous[0].Bucket != "media" || st.Previous[0].Endpoint != s.fake.URL {
		t.Fatalf("old connection not remembered: bucket %q, previous %+v", st.Bucket, st.Previous)
	}
	// The removed file still comes from the old bucket (old endpoint and credentials).
	resp := doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	body := readAll(resp)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, s.fake.Object("media", oldKey).Body) {
		t.Fatalf("file of the old bucket: %d", resp.StatusCode)
	}
	if len(s.fake.Errors) != 0 {
		t.Fatalf("signature problems at the old bucket: %v", s.fake.Errors)
	}

	// It is stored (secrets sealed) so that it survives a restart.
	deadline := time.Now().Add(5 * time.Second)
	var stored map[string]any
	for time.Now().Before(deadline) {
		stored, _ = s.srv.settings.Get(gocdnOption).(map[string]any)
		if p, _ := stored["previous-connections"].([]any); len(p) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	prev, _ := stored["previous-connections"].([]any)
	if len(prev) != 1 {
		t.Fatalf("previous connection not stored: %v", stored["previous-connections"])
	}
	raw, _ := json.Marshal([]any{prev, s.srv.settings.Get(gocdnSeenOption)})
	if strings.Contains(string(raw), gocdnTestSecret) || strings.Contains(string(raw), "new-secret/value") || !strings.Contains(string(raw), gocdnSealPrefix) {
		t.Fatalf("stored connections are not sealed: %s", raw)
	}
	gocdnRuntimes.Delete(s.srv) // a restart
	st = s.srv.gocdnLoad()
	if len(st.Previous) != 1 || st.Previous[0].Bucket != "media" || s.srv.gocdnReveal(st.Previous[0].SecretKey) != gocdnTestSecret {
		t.Fatalf("after a restart: %+v", st.Previous)
	}
	resp = doGet(t, gocdnAnon(), s.ts.URL+"/wp-content/uploads/"+rel, nil)
	readAll(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("file of the old bucket after a restart: %d", resp.StatusCode)
	}

	// Moving the file to the new bucket drops the remembered connection.
	if err := s.srv.gocdnStartJobWith(context.Background(), "move", false, gocdnJobOpts{}); err != nil {
		t.Fatal(err)
	}
	if j := s.waitJob(); j["failed"].(float64) != 0 || j["done"].(float64) != 1 {
		t.Fatalf("move job %v", j)
	}
	if fake2.Object("media2", "wp-content/uploads/"+rel) == nil {
		t.Fatal("not moved to the new bucket")
	}
	if got := s.srv.gocdnLoad().Previous; len(got) != 0 {
		t.Errorf("unused previous connection kept: %+v", got)
	}
}

// A connection change that leaves no items behind remembers nothing.
func TestGoCDNEnvBucketChangeWithoutItems(t *testing.T) {
	s := gocdnSite(t)
	set := func(bucket string) {
		b, _ := json.Marshal(map[string]any{"provider": "minio", "endpoint": s.fake.URL, "region": "auto", "bucket": bucket, "access-key-id": gocdnTestKey,
			"secret-access-key": gocdnTestSecret, "use-path-style": true})
		t.Setenv(gocdnEnv, string(b))
	}
	s.fake.AddBucket("other")
	set("media")
	s.srv.gocdnLoad()
	set("other")
	s.srv.gocdnLoad()
	time.Sleep(300 * time.Millisecond)
	stored, _ := s.srv.settings.Get(gocdnOption).(map[string]any)
	if p, _ := stored["previous-connections"].([]any); len(p) != 0 {
		t.Errorf("remembered a bucket without items: %v", p)
	}
}
