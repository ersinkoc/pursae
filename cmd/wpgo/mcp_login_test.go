package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// fakeOAuthSite is a minimal site: metadata, registration, an authorize endpoint that redirects back
// with a code, a token endpoint (code + refresh, rotating) and the MCP endpoint behind a Bearer check.
func fakeOAuthSite(t *testing.T) (*httptest.Server, *int) {
	var ts *httptest.Server
	var mu sync.Mutex
	access, refresh, challenge, redirectURI := "", "", "", ""
	refreshes := 0
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]string{"authorization_endpoint": ts.URL + "/wp-admin/oauth-authorize",
				"token_endpoint": ts.URL + "/oauth/token", "registration_endpoint": ts.URL + "/oauth/register"})
		case "/oauth/register":
			var in struct {
				RedirectURIs []string `json:"redirect_uris"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			redirectURI = in.RedirectURIs[0]
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"client_id":"cid-1"}`))
		case "/wp-admin/oauth-authorize":
			q := r.URL.Query()
			challenge = q.Get("code_challenge")
			if q.Get("client_id") != "cid-1" || q.Get("redirect_uri") != redirectURI || q.Get("code_challenge_method") != "S256" {
				http.Error(w, "bad request", 400)
				return
			}
			http.Redirect(w, r, redirectURI+"?code=the-code&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
		case "/oauth/token":
			_ = r.ParseForm()
			switch r.PostForm.Get("grant_type") {
			case "authorization_code":
				sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
				if r.PostForm.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				access, refresh = "acc-1", "ref-1"
			case "refresh_token":
				if r.PostForm.Get("refresh_token") != refresh {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				refreshes++
				access, refresh = "acc-2", "ref-2"
			}
			_, _ = w.Write([]byte(`{"access_token":"` + access + `","refresh_token":"` + refresh + `","expires_in":3600}`))
		case "/wp-json/pursae/v1/mcp":
			if r.Header.Get("Authorization") != "Bearer "+access || access == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"expired"}`))
				return
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	return ts, &refreshes
}

// TestMCPLoginAndRefresh runs the whole flow: login stores a 0600 token file, the bridge then signs in
// with the Bearer token, and after the server drops the access token it refreshes once and retries.
func TestMCPLoginAndRefresh(t *testing.T) {
	ts, refreshes := fakeOAuthSite(t)
	defer ts.Close()
	dir := t.TempDir()
	t.Setenv("PURSAE_CONFIG_DIR", dir)
	t.Setenv("PURSAE_URL", "")
	t.Setenv("PURSAE_USER", "")
	t.Setenv("PURSAE_APP_PASSWORD", "")

	var out, errs syncBuf
	errc := make(chan error, 1)
	go func() {
		errc <- runMCPLogin(context.Background(), []string{"--url", ts.URL, "--no-browser"}, &out, &errs)
	}()
	var authURL string
	for i := 0; i < 200 && authURL == ""; i++ {
		for _, f := range strings.Fields(errs.String()) {
			if strings.HasPrefix(f, ts.URL+"/wp-admin/oauth-authorize") {
				authURL = f
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if authURL == "" {
		t.Fatalf("no sign-in URL printed: %q", errs.String())
	}
	resp, err := http.Get(authURL) // follows the redirect to the loopback callback
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	path, _ := mcpTokenPath(ts.URL)
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", st, err)
	}

	// The bridge uses the stored token (no user / password).
	var bout, berr bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	if err := runMCP(context.Background(), []string{"--url", ts.URL}, in, &bout, &berr); err != nil || !strings.Contains(bout.String(), `"ok":true`) {
		t.Fatalf("bridge: %v %q %q", err, bout.String(), berr.String())
	}
	// Expire the access token on the server side by storing a stale one: refresh happens once.
	tok, _ := loadMCPToken(path)
	tok.AccessToken = "stale"
	_ = saveMCPToken(path, tok)
	bout.Reset()
	in = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	if err := runMCP(context.Background(), []string{"--url", ts.URL}, in, &bout, &berr); err != nil || !strings.Contains(bout.String(), `"ok":true`) {
		t.Fatalf("bridge after expiry: %v %q %q", err, bout.String(), berr.String())
	}
	if *refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", *refreshes)
	}
	if tok, _ = loadMCPToken(path); tok.AccessToken != "acc-2" || tok.RefreshToken != "ref-2" {
		t.Errorf("rotated token not saved: %+v", tok)
	}
}

func TestMCPLoginNeedsURL(t *testing.T) {
	t.Setenv("PURSAE_URL", "")
	if err := runMCPLogin(context.Background(), nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("expected an error without --url")
	}
}
