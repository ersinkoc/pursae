package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// mcpAuth signs the bridge's requests: an Application Password (HTTP Basic) or an OAuth token pair
// created by `wpgo mcp login` and refreshed here when the access token has expired.
type mcpAuth struct {
	user, pass string
	mu         sync.Mutex
	tok        *mcpToken
	path       string // token file to rewrite after a refresh
}

// mcpToken is what `wpgo mcp login` stores (0600) per site.
type mcpToken struct {
	Site         string    `json:"site"`
	ClientID     string    `json:"client_id"`
	TokenURL     string    `json:"token_endpoint"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expires      time.Time `json:"expires_at"`
}

func (a *mcpAuth) sign(req *http.Request) {
	if a.tok == nil {
		req.SetBasicAuth(a.user, a.pass)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	req.Header.Set("Authorization", "Bearer "+a.tok.AccessToken)
}

// refresh trades the refresh token for a new pair (rotating) and saves it. failedToken is the access
// token the caller was refused with: if another message already refreshed, nothing is done.
func (a *mcpAuth) refresh(ctx context.Context, client *http.Client, failedToken string) error {
	if a.tok == nil {
		return errors.New("no OAuth token to refresh")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tok.AccessToken != failedToken {
		return nil
	}
	tr, err := mcpTokenRequest(ctx, client, a.tok.TokenURL, url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {a.tok.RefreshToken}, "client_id": {a.tok.ClientID}})
	if err != nil {
		return fmt.Errorf("%w (run `wpgo mcp login --url %s` again)", err, a.tok.Site)
	}
	a.tok.AccessToken, a.tok.RefreshToken = tr.AccessToken, tr.RefreshToken
	a.tok.Expires = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	if a.path != "" {
		_ = saveMCPToken(a.path, a.tok)
	}
	return nil
}

type mcpTokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func mcpTokenRequest(ctx context.Context, client *http.Client, endpoint string, form url.Values) (*mcpTokenResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "wpgo-mcp")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var tr mcpTokenResp
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(body, &tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		msg := tr.Error
		if tr.ErrorDesc != "" {
			msg += ": " + tr.ErrorDesc
		}
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("token endpoint: %s", msg)
	}
	return &tr, nil
}

// mcpTokenPath is the per-site token file in the user's config directory.
func mcpTokenPath(site string) (string, error) {
	dir := os.Getenv("PURSAE_CONFIG_DIR")
	if dir == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(d, "pursae")
	}
	u, err := url.Parse(site)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid site URL %q", site)
	}
	name := strings.NewReplacer(":", "_", "/", "_").Replace(strings.ToLower(u.Host + u.Path))
	return filepath.Join(dir, "mcp-"+strings.Trim(name, "_")+".json"), nil
}

func saveMCPToken(path string, t *mcpToken) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(t, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadMCPToken(path string) (*mcpToken, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t mcpToken
	if err := json.Unmarshal(b, &t); err != nil || t.AccessToken == "" || t.TokenURL == "" {
		return nil, fmt.Errorf("%s is not a valid token file", path)
	}
	return &t, nil
}

// runMCPLogin is `wpgo mcp login --url https://site`: OAuth 2.1 authorization code + PKCE against the
// site (dynamic client registration, loopback redirect), then stores the token for `wpgo mcp`.
func runMCPLogin(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	site := fs.String("url", os.Getenv("PURSAE_URL"), "site URL (env PURSAE_URL)")
	scope := fs.String("scope", "", "space separated scopes to ask for (default: the consent screen's default)")
	noBrowser := fs.Bool("no-browser", false, "only print the sign-in URL")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *site == "" {
		return errors.New("wpgo mcp login needs --url (or PURSAE_URL)")
	}
	u, err := url.Parse(strings.TrimRight(*site, "/"))
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid --url %q", *site)
	}
	if err := mcpURLAllowed(u); err != nil {
		return err
	}
	base := u.String()
	client := &http.Client{Timeout: 30 * time.Second}
	var meta struct {
		Auth  string `json:"authorization_endpoint"`
		Token string `json:"token_endpoint"`
		Reg   string `json:"registration_endpoint"`
	}
	if err := mcpGetJSON(ctx, client, base+"/.well-known/oauth-authorization-server", &meta); err != nil || meta.Auth == "" || meta.Token == "" || meta.Reg == "" {
		return fmt.Errorf("could not read the site's OAuth metadata: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://%s/callback", ln.Addr().String())
	regBody, _ := json.Marshal(map[string]any{"client_name": "Pursae MCP bridge", "redirect_uris": []string{redirect}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, meta.Reg, strings.NewReader(string(regBody)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(rb, &reg) != nil || reg.ClientID == "" {
		return fmt.Errorf("client registration failed: %s", strings.TrimSpace(string(rb)))
	}
	verifier, state := mcpRand(), mcpRand()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {redirect}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {base + "/wp-json/pursae/v1/mcp"}}
	if *scope != "" {
		q.Set("scope", *scope)
	}
	authURL := meta.Auth + "?" + q.Encode()
	type result struct{ code, err string }
	done := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		v := r.URL.Query()
		res := result{code: v.Get("code")}
		switch {
		case v.Get("state") != state:
			res = result{err: "state mismatch"}
		case v.Get("error") != "":
			res = result{err: v.Get("error")}
		case res.code == "":
			res = result{err: "no code returned"}
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if res.err != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintln(w, "Sign-in failed: "+res.err)
		} else {
			fmt.Fprintln(w, "Signed in. You can close this tab and return to your terminal.")
		}
		select {
		case done <- res:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	fmt.Fprintln(stderr, "Open this URL to sign in and approve access:\n\n  "+authURL+"\n")
	if !*noBrowser {
		openBrowser(authURL)
	}
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Minute):
		return errors.New("timed out waiting for the sign-in")
	}
	if res.err != "" {
		return errors.New("sign-in failed: " + res.err)
	}
	tr, err := mcpTokenRequest(ctx, client, meta.Token, url.Values{"grant_type": {"authorization_code"}, "code": {res.code},
		"code_verifier": {verifier}, "client_id": {reg.ClientID}, "redirect_uri": {redirect}})
	if err != nil {
		return err
	}
	path, err := mcpTokenPath(base)
	if err != nil {
		return err
	}
	if err := saveMCPToken(path, &mcpToken{Site: base, ClientID: reg.ClientID, TokenURL: meta.Token, AccessToken: tr.AccessToken,
		RefreshToken: tr.RefreshToken, Expires: time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Signed in. Token saved to %s\nClient config:\n{\"mcpServers\":{\"pursae\":{\"command\":\"wpgo\",\"args\":[\"mcp\",\"--url\",%q]}}}\n", path, base)
	return nil
}

func mcpGetJSON(ctx context.Context, client *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func mcpRand() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}
