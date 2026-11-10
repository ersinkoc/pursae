package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// runMCP is `wpgo mcp`: a stdio MCP server for clients that start a local command (Claude Desktop,
// Claude Code, Cursor). It forwards each JSON-RPC message to the site's streamable HTTP endpoint
// /wp-json/pursae/v1/mcp, signed in with an Application Password, so the tools run on the site with
// that password's capabilities and scopes. The site can be local or remote.
func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "login" {
		return runMCPLogin(ctx, args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	site := fs.String("url", os.Getenv("PURSAE_URL"), "site URL, e.g. https://example.com (env PURSAE_URL)")
	user := fs.String("user", os.Getenv("PURSAE_USER"), "user name (env PURSAE_USER)")
	pass := fs.String("password", "", "Application Password; prefer the env PURSAE_APP_PASSWORD (a flag is visible to other users in ps)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pass != "" {
		fmt.Fprintln(stderr, "wpgo mcp: warning: --password is visible in the process list; set PURSAE_APP_PASSWORD instead")
	} else {
		*pass = os.Getenv("PURSAE_APP_PASSWORD")
	}
	var tok *mcpToken
	tokPath := ""
	if *site != "" && *user == "" && *pass == "" { // no Application Password: use the token from `wpgo mcp login`
		if p, err := mcpTokenPath(strings.TrimRight(*site, "/")); err == nil {
			if t, err := loadMCPToken(p); err == nil {
				tok, tokPath = t, p
			}
		}
	}
	if *site == "" || (tok == nil && (*user == "" || *pass == "")) {
		return errors.New("wpgo mcp needs --url and either `wpgo mcp login` first, or --user and PURSAE_APP_PASSWORD (or PURSAE_URL, PURSAE_USER; --password also works)")
	}
	auth := &mcpAuth{user: *user, pass: *pass, tok: tok, path: tokPath}
	u, err := url.Parse(strings.TrimRight(*site, "/"))
	if err != nil || u.Host == "" {
		return fmt.Errorf("wpgo mcp: invalid --url %q", *site)
	}
	if err := mcpURLAllowed(u); err != nil {
		return err
	}
	endpoint := u.String() + "/wp-json/pursae/v1/mcp"
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return mcpURLAllowed(r.URL) // never follow a redirect that would send the password in clear text
	}}
	out := json.NewEncoder(stdout)
	in := bufio.NewReader(stdin)
	for {
		line, err := in.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if resp := forwardMCP(ctx, client, endpoint, auth, bytes.TrimSpace(line)); resp != nil {
				if werr := out.Encode(resp); werr != nil {
					return werr
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// mcpURLAllowed refuses plain http except to this machine: the Application Password travels in every
// request's Authorization header.
func mcpURLAllowed(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		switch h := strings.ToLower(u.Hostname()); h {
		case "localhost", "127.0.0.1", "::1":
			return nil
		default:
			if strings.HasSuffix(h, ".localhost") {
				return nil
			}
		}
		return fmt.Errorf("wpgo mcp: refusing to send the password over plain http to %s; use https (http is allowed for localhost only)", u.Host)
	}
	return fmt.Errorf("wpgo mcp: unsupported URL scheme %q", u.Scheme)
}

// forwardMCP posts one message and returns the answer to print (nil for notifications). Transport
// failures become JSON-RPC errors so the client sees them.
func forwardMCP(ctx context.Context, client *http.Client, endpoint string, auth *mcpAuth, msg []byte) json.RawMessage {
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(msg, &probe)
	isRequest := len(probe.ID) > 0 && string(probe.ID) != "null"
	fail := func(text string) json.RawMessage {
		if !isRequest {
			fmt.Fprintln(os.Stderr, "wpgo mcp:", text)
			return nil
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": probe.ID, "error": map[string]any{"code": -32000, "message": text}})
		return b
	}
	do := func() (*http.Response, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(msg))
		if err != nil {
			return nil, "", err
		}
		auth.sign(req)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("User-Agent", "wpgo-mcp")
		resp, err := client.Do(req)
		return resp, strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "), err
	}
	resp, used, err := do()
	if err == nil && resp.StatusCode == http.StatusUnauthorized && auth.tok != nil {
		resp.Body.Close() // the access token expired: refresh once and retry
		if rerr := auth.refresh(ctx, client, used); rerr != nil {
			return fail(rerr.Error())
		}
		resp, _, err = do()
	}
	if err != nil {
		return fail(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode == http.StatusAccepted || !isRequest {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Message == "" {
			e.Message = strings.TrimSpace(string(body))
		}
		return fail(fmt.Sprintf("%s: %s", resp.Status, e.Message))
	}
	return json.RawMessage(bytes.TrimSpace(body))
}
