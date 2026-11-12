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
	"sync"
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
	br := &mcpBridge{client: client, endpoint: endpoint, auth: auth, out: json.NewEncoder(stdout), stderr: stderr,
		// The stream stays open for hours: same redirect rule, no overall timeout.
		streamClient: &http.Client{CheckRedirect: client.CheckRedirect}}
	sctx, stop := context.WithCancel(ctx)
	defer br.wg.Wait()
	defer stop()
	defer br.endSession(ctx)
	in := bufio.NewReader(stdin)
	for {
		line, err := in.ReadBytes('\n')
		if msg := bytes.TrimSpace(line); len(msg) > 0 {
			resp, newSession := br.forward(ctx, msg)
			if resp != nil {
				if werr := br.write(resp); werr != nil {
					return werr
				}
			}
			if newSession {
				br.wg.Add(1)
				go br.listen(sctx)
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

// mcpBridge is the state of one `wpgo mcp` run: the HTTP session the site opened at initialize
// (Mcp-Session-Id) and the stdout both the answers and the server's notifications are written to.
type mcpBridge struct {
	client, streamClient *http.Client
	endpoint             string
	auth                 *mcpAuth
	stderr               io.Writer
	wg                   sync.WaitGroup

	mu      sync.Mutex // guards out and session
	out     *json.Encoder
	session string
}

func (b *mcpBridge) write(msg json.RawMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.out.Encode(msg)
}

func (b *mcpBridge) sessionID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.session
}

func (b *mcpBridge) setSession(id string) {
	b.mu.Lock()
	b.session = id
	b.mu.Unlock()
}

// endSession tells the site the session is over (best effort, when stdin closes).
func (b *mcpBridge) endSession(ctx context.Context) {
	id := b.sessionID()
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, b.endpoint, nil)
	if err != nil {
		return
	}
	b.auth.sign(req)
	req.Header.Set(mcpSessionHeader, id)
	req.Header.Set("User-Agent", "wpgo-mcp")
	if resp, err := b.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

const mcpSessionHeader = "Mcp-Session-Id"

// mcpStreamRetry is the pause before the stream is reopened after it dropped (a variable for tests).
var mcpStreamRetry = 3 * time.Second

// listen holds the session's GET event stream open and prints every message the site pushes
// (notifications/tools/list_changed, …) to stdout. A dropped stream is reopened with Last-Event-ID so
// nothing is missed; it ends with the session (404), on a site without streams (405) or a refused
// credential.
func (b *mcpBridge) listen(ctx context.Context) {
	defer b.wg.Done()
	id := b.sessionID()
	last := ""
	for ctx.Err() == nil && b.sessionID() == id {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.endpoint, nil)
		if err != nil {
			return
		}
		b.auth.sign(req)
		used := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set(mcpSessionHeader, id)
		req.Header.Set("User-Agent", "wpgo-mcp")
		if last != "" {
			req.Header.Set("Last-Event-ID", last)
		}
		resp, err := b.streamClient.Do(req)
		if err == nil {
			switch {
			case resp.StatusCode == http.StatusOK:
				last = b.readEvents(resp.Body, last)
			case resp.StatusCode == http.StatusUnauthorized && b.auth.tok != nil:
				if b.auth.refresh(ctx, b.client, used) != nil {
					resp.Body.Close()
					return
				}
			default: // 404 session ended, 405 no stream, 401 with a password: nothing to wait for
				resp.Body.Close()
				return
			}
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(mcpStreamRetry):
		}
	}
}

// readEvents prints the data of each server-sent event and returns the id of the last one.
func (b *mcpBridge) readEvents(body io.Reader, last string) string {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var data []byte
	id := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if len(data) > 0 && json.Valid(data) {
				if b.write(json.RawMessage(data)) != nil {
					return last
				}
			}
			if id != "" {
				last = id
			}
			data, id = nil, ""
		case strings.HasPrefix(line, "data:"):
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
		case strings.HasPrefix(line, "id:"):
			id = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
	}
	return last
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

// forward posts one message and returns the answer to print (nil for notifications), and whether the
// site opened a new session with it (the caller then starts the event stream). Transport failures
// become JSON-RPC errors so the client sees them.
func (b *mcpBridge) forward(ctx context.Context, msg []byte) (json.RawMessage, bool) {
	client, endpoint, auth := b.client, b.endpoint, b.auth
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	_ = json.Unmarshal(msg, &probe)
	isRequest := len(probe.ID) > 0 && string(probe.ID) != "null"
	fail := func(text string) (json.RawMessage, bool) {
		if !isRequest {
			fmt.Fprintln(b.stderr, "wpgo mcp:", text)
			return nil, false
		}
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": probe.ID, "error": map[string]any{"code": -32000, "message": text}})
		return out, false
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
		if id := b.sessionID(); id != "" && probe.Method != "initialize" {
			req.Header.Set(mcpSessionHeader, id)
		}
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
	if err == nil && resp.StatusCode == http.StatusNotFound && resp.Request.Header.Get(mcpSessionHeader) != "" {
		resp.Body.Close() // the session expired on the site: carry on without one
		b.setSession("")
		resp, _, err = do()
	}
	if err != nil {
		return fail(err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode == http.StatusAccepted || !isRequest {
		return nil, false
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
	newSession := false
	if id := resp.Header.Get(mcpSessionHeader); id != "" && probe.Method == "initialize" && id != b.sessionID() {
		b.setSession(id)
		newSession = true
	}
	return json.RawMessage(bytes.TrimSpace(body)), newSession
}
