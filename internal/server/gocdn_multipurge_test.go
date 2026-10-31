package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// TestGoCDNSeveralPurgeProviders purges Cloudflare (main), BunnyCDN and Fastly at once, with Fastly
// refusing its token: the others are purged and the failure is named.
func TestGoCDNSeveralPurgeProviders(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	note := func(s string) { mu.Lock(); calls = append(calls, s); mu.Unlock() }
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		note("cloudflare " + r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer cf.Close()
	bunny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		note("bunny " + r.URL.Path + " " + r.Header.Get("AccessKey"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer bunny.Close()
	fastly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		note("fastly " + r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"msg":"Provided credentials are missing or invalid"}`))
	}))
	defer fastly.Close()
	oldCF, oldB, oldF := gocdnCFBase, gocdnBunnyBase, gocdnFastlyBase
	gocdnCFBase, gocdnBunnyBase, gocdnFastlyBase = cf.URL, bunny.URL, fastly.URL
	defer func() { gocdnCFBase, gocdnBunnyBase, gocdnFastlyBase = oldCF, oldB, oldF }()

	s := gocdnSite(t)
	zone := strings.Repeat("c", 32)
	form := url.Values{"purge-provider": {"cloudflare"}, "cloudflare-zone": {zone}, "cloudflare-token": {"cf-token"}, "cloudflare-auto": {"1"},
		"purge-extra-form": {"1"},
		"extra-bunny":      {"1"}, "extra-bunny-zone": {"4242"}, "extra-bunny-secret": {"bunny-key"},
		"extra-fastly": {"1"}, "extra-fastly-zone": {"SU1Z0isxPaozGVKXdv0eY"}, "extra-fastly-secret": {"bad-token"}}
	if code, _, loc := s.pageForm("purge", form); code != http.StatusFound || !strings.Contains(loc, "gocdn-msg") {
		t.Fatalf("save: %d %s", code, loc)
	}
	st := s.srv.gocdnLoad()
	if got := gocdnPurgeTargets(st); len(got) != 3 || got[0].Provider != "cloudflare" || got[1].Provider != "bunny" || got[2].Provider != "fastly" {
		t.Fatalf("targets %+v", got)
	}
	stored, _ := s.srv.settings.Get(gocdnOption).(map[string]any)
	raw, _ := stored["purge-extra"].([]any)
	enc, _ := json.Marshal(raw)
	if len(raw) != 2 || strings.Contains(string(enc), "bunny-key") || strings.Contains(string(enc), "bad-token") {
		t.Fatalf("stored extras %s", enc)
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=purge")
	if strings.Contains(body, "bunny-key") || strings.Contains(body, "bad-token") || !strings.Contains(body, "Purge all CDNs (Cloudflare, BunnyCDN, Fastly)") ||
		!strings.Contains(body, `name="extra-bunny" value="1" checked`) || strings.Contains(body, `name="extra-cloudfront" value="1" checked`) {
		t.Fatal("the Purge screen does not show the providers (or prints a secret)")
	}
	// Purge everything: all three are called, the failure names Fastly and the others are listed as purged.
	code, _, loc := s.pageForm("purge", url.Values{"do": {"purge-cdn"}})
	if code != http.StatusFound || !strings.Contains(loc, "gocdn-error") {
		t.Fatalf("purge: %d %s", code, loc)
	}
	msg, _ := url.QueryUnescape(strings.SplitN(loc, "gocdn-error=", 2)[1])
	if !strings.Contains(msg, "Fastly: 401") || !strings.Contains(msg, "Purged: Cloudflare, BunnyCDN.") || strings.Contains(msg, "BunnyCDN:") {
		t.Fatalf("failure message %q", msg)
	}
	mu.Lock()
	got := strings.Join(calls, "|")
	mu.Unlock()
	for _, want := range []string{"cloudflare /zones/" + zone + "/purge_cache", "bunny /pullzone/4242/purgeCache bunny-key", "fastly /service/SU1Z0isxPaozGVKXdv0eY/purge_all"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing call %q in %s", want, got)
		}
	}
	// The error of the library call names every failing provider.
	res := s.srv.gocdnCDNPurgeEach(context.Background(), st, nil)
	if len(res) != 3 || res[0].Err != nil || res[1].Err != nil || res[2].Err == nil {
		t.Fatalf("results %+v", res)
	}
	// A second form post without the extras block (the old form) keeps them; with it and nothing
	// ticked it removes them, and a secret left empty keeps the saved one.
	s.pageForm("purge", url.Values{"purge-provider": {"cloudflare"}})
	if len(s.srv.gocdnLoad().PurgeExtra) != 2 {
		t.Fatal("the old form dropped the additional providers")
	}
	s.pageForm("purge", url.Values{"purge-provider": {"cloudflare"}, "purge-extra-form": {"1"}, "extra-bunny": {"1"}, "extra-bunny-zone": {"4242"}})
	if ex := s.srv.gocdnLoad().PurgeExtra; len(ex) != 1 || ex[0].Provider != "bunny" || s.srv.gocdnReveal(ex[0].Secret) != "bunny-key" {
		t.Fatalf("extras after the edit: %+v", ex)
	}
	// Making an additional provider the main one removes it from the additional list.
	s.pageForm("purge", url.Values{"purge-provider": {"bunny"}, "purge-zone": {"4242"}, "purge-secret": {"bunny-key"}})
	if got := gocdnPurgeTargets(s.srv.gocdnLoad()); len(got) != 1 || got[0].Provider != "bunny" {
		t.Fatalf("targets after promoting %+v", got)
	}
	// An invalid zone of an additional provider is refused.
	if _, _, loc := s.pageForm("purge", url.Values{"purge-provider": {"bunny"}, "purge-extra-form": {"1"}, "extra-keycdn": {"1"}, "extra-keycdn-zone": {"not a zone"}}); !strings.Contains(loc, "gocdn-error") {
		t.Errorf("bad zone accepted: %s", loc)
	}
}
