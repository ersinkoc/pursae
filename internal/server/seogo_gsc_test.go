package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"wpgo/internal/store"
)

// fakeGSCGoogle is Google's OAuth token endpoint and the Search Console API.
func fakeGSCGoogle(t *testing.T, sites ...string) (*httptest.Server, *atomic.Int32) {
	var revoked atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csecret" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "good-code" || !strings.HasSuffix(r.Form.Get("redirect_uri"), "/wp-admin/seogo?page=search-console") {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"at1","refresh_token":"rt1","expires_in":3600}`)
		case "refresh_token":
			_, _ = io.WriteString(w, `{"access_token":"at2","expires_in":3600}`)
		}
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) { revoked.Add(1) })
	mux.HandleFunc("/webmasters/v3/sites", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var entries []map[string]string
		for _, s := range sites {
			entries = append(entries, map[string]string{"siteUrl": s, "permissionLevel": "siteOwner"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"siteEntry": entries})
	})
	mux.HandleFunc("/webmasters/v3/sites/", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Dimensions []string `json:"dimensions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch {
		case len(in.Dimensions) == 0:
			_, _ = io.WriteString(w, `{"rows":[{"clicks":42,"impressions":1000,"ctr":0.042,"position":7.25}]}`)
		case in.Dimensions[0] == "query":
			_, _ = io.WriteString(w, `{"rows":[{"keys":["pursae <seo>"],"clicks":30,"impressions":400,"ctr":0.075,"position":3.1}]}`)
		default:
			_, _ = io.WriteString(w, `{"rows":[{"keys":["https://example.test/hello/"],"clicks":12,"impressions":600,"ctr":0.02,"position":9}]}`)
		}
	})
	g := httptest.NewServer(mux)
	t.Cleanup(g.Close)
	return g, &revoked
}

func TestSEOGoSearchConsoleOAuth(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	g, revoked := fakeGSCGoogle(t, base+"/", "sc-domain:other.test")
	seogoGSCBase = g.URL
	defer func() { seogoGSCBase = "" }()
	ctx := t.Context()

	if code, loc := seogoForm(t, c, base, "search-console", url.Values{"action": {"gsc_client"}, "client_id": {"cid"}, "client_secret": {"csecret"}}); code != http.StatusFound || strings.Contains(loc, "seogo-error") {
		t.Fatalf("save client: %d %s", code, loc)
	}
	st := srv.seogoGSCLoad()
	if st.ClientSecret == "csecret" || srv.msReveal(st.ClientSecret) != "csecret" {
		t.Fatal("the client secret is not stored sealed")
	}
	// The settings export never carries the secret.
	if b, _ := json.Marshal(srv.seogoOpts()); strings.Contains(string(b), "client_secret") {
		t.Error("the Search Console client is part of the SEO settings")
	}

	_, page, _ := get(t, c, base+"/wp-admin/seogo?page=search-console")
	m := regexp.MustCompile(`href="(/wp-admin/seogo\?page=search-console&amp;action=connect[^"]+)"`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no Connect to Google button")
	}
	code, _, loc := get(t, c, base+strings.ReplaceAll(m[1], "&amp;", "&"))
	if code != http.StatusFound || !strings.HasPrefix(loc, g.URL+"/o/oauth2/v2/auth?") {
		t.Fatalf("connect: %d %s", code, loc)
	}
	au, _ := url.Parse(loc)
	q := au.Query()
	if q.Get("scope") != seogoGSCScope || q.Get("access_type") != "offline" || q.Get("client_id") != "cid" || q.Get("state") == "" {
		t.Fatalf("authorization URL: %s", loc)
	}
	state := q.Get("state")

	// A forged state is refused and burns the pending one.
	if _, _, loc := get(t, c, base+"/wp-admin/seogo?page=search-console&code=good-code&state=forged"); !strings.Contains(loc, "seogo-error") {
		t.Fatalf("forged state accepted: %s", loc)
	}
	if srv.seogoGSCLoad().RefreshToken != "" {
		t.Fatal("tokens stored after a forged state")
	}
	// Start again; the real callback connects and preselects this site's property.
	get(t, c, base+strings.ReplaceAll(m[1], "&amp;", "&"))
	state = srv.seogoGSCLoad().State
	if _, _, loc := get(t, c, base+"/wp-admin/seogo?page=search-console&code=good-code&state="+state); !strings.Contains(loc, "seogo-updated") {
		t.Fatalf("callback: %s", loc)
	}
	st = srv.seogoGSCLoad()
	if st.RefreshToken == "" || st.RefreshToken == "rt1" || st.Property != base+"/" || st.State != "" {
		t.Fatalf("stored connection: %+v", st)
	}
	// The state is single use.
	if _, _, loc := get(t, c, base+"/wp-admin/seogo?page=search-console&code=good-code&state="+state); !strings.Contains(loc, "seogo-error") {
		t.Error("a used state was accepted again")
	}

	// The report.
	_, page, _ = get(t, c, base+"/wp-admin/seogo?page=search-console")
	for _, want := range []string{"seogo-gsc-report", "<strong>42</strong>", "4.2%", "7.2", "pursae &lt;seo&gt;", "https://example.test/hello/"} {
		if !strings.Contains(page, want) {
			t.Errorf("report misses %q", want)
		}
	}
	// An expired access token is refreshed.
	st.Expires = 1
	_ = srv.seogoGSCSave(ctx, st)
	if tok, err := srv.seogoGSCToken(ctx, srv.seogoGSCLoad()); err != nil || tok != "at2" {
		t.Fatalf("refresh: %q %v", tok, err)
	}
	if srv.msReveal(srv.seogoGSCLoad().RefreshToken) != "rt1" {
		t.Error("the refresh token was lost on refresh")
	}
	// A property the account cannot read is refused.
	if _, loc := seogoForm(t, c, base, "search-console", url.Values{"action": {"gsc_property"}, "property": {"https://evil.test/"}}); !strings.Contains(loc, "seogo-error") {
		t.Error("foreign property accepted")
	}
	if _, loc := seogoForm(t, c, base, "search-console", url.Values{"action": {"gsc_property"}, "property": {"sc-domain:other.test"}}); strings.Contains(loc, "seogo-error") {
		t.Error("own property refused")
	}
	// Disconnect revokes and forgets the tokens.
	seogoForm(t, c, base, "search-console", url.Values{"action": {"gsc_disconnect"}})
	if st := srv.seogoGSCLoad(); st.RefreshToken != "" || st.Property != "" || revoked.Load() != 1 {
		t.Fatalf("disconnect: %+v revoked=%d", st, revoked.Load())
	}
	// Only seogo_general users reach the screen.
	if srv.seogoCan(&store.User{ID: 77, Roles: []string{"author"}}, seogoPageCaps["search-console"]) {
		t.Error("an author may open Search Console")
	}
}

func TestSEOGoGSCMatch(t *testing.T) {
	sites := []string{"sc-domain:example.com", "https://example.com/"}
	if got := seogoGSCMatch(sites, "https://example.com"); got != "https://example.com/" {
		t.Errorf("prefix: %q", got)
	}
	if got := seogoGSCMatch(sites[:1], "https://www.example.com/"); got != "sc-domain:example.com" {
		t.Errorf("domain: %q", got)
	}
	if got := seogoGSCMatch(sites, "https://other.com/"); got != "" {
		t.Errorf("other: %q", got)
	}
}

func TestSEOGoRedirectsExportAndImageSEO(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	seogoToggle(t, c, base, "activate")
	ctx := t.Context()
	seogoForm(t, c, base, "redirections", url.Values{"action": {"import"}, "csv": {"/x-old/,/x-new/,307,start\n"}})
	_, page, _ := get(t, c, base+"/wp-admin/seogo?page=redirections")
	nonce := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(page)[1]
	code, body, _ := get(t, c, base+"/wp-admin/seogo?page=redirections&action=export&_wpnonce="+nonce)
	if code != http.StatusOK || !strings.HasPrefix(body, "source,target,code,match_type,status,hits\n") || !strings.Contains(body, "/x-new/,307,start,active,0") {
		t.Fatalf("export: %d %q", code, body)
	}
	if code, _, _ := get(t, c, base+"/wp-admin/seogo?page=redirections&action=export&_wpnonce=bad"); code == http.StatusOK {
		t.Error("export without a valid nonce")
	}
	// The exported file imports again unchanged.
	if _, err := srv.store.Pool.Exec(ctx, `DELETE FROM redirects`); err != nil {
		t.Fatal(err)
	}
	srv.seogoRedirectsChanged()
	if n, err := srv.seogoImportRedirects(ctx, []byte(body)); err != nil || n != 1 {
		t.Fatalf("re-import: %d %v", n, err)
	}

	// Image SEO fills empty alt and missing title attributes, and leaves written ones alone.
	o := srv.seogoOpts().clone()
	o.Modules["image_seo"] = true
	o.ImgAlt, o.ImgTitle = "%filename%", "%filename%"
	_ = srv.seogoSave(ctx, o)
	out := srv.seogoImageSEO(ctx, `<p><img src="/u/red-apple_tree-300x200.jpg" alt=""><img src="/u/b.png" alt="Kept" title="Mine"></p>`)
	if !strings.Contains(out, `alt="red apple tree"`) || !strings.Contains(out, `title="red apple tree"`) {
		t.Errorf("empty alt not filled: %s", out)
	}
	if !strings.Contains(out, `alt="Kept" title="Mine"`) || strings.Count(out, "title=") != 2 {
		t.Errorf("written alt/title changed: %s", out)
	}
	o.Modules["image_seo"] = false
	_ = srv.seogoSave(ctx, o)
	if in := `<img src="/u/a.jpg" alt="">`; srv.seogoImageSEO(ctx, in) != in {
		t.Error("Image SEO ran with the module off")
	}
}
