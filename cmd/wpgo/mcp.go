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
	"os"
	"strings"
	"time"
)

// runMCP is `wpgo mcp`: a stdio MCP server for clients that start a local command (Claude Desktop,
// Claude Code, Cursor). It forwards each JSON-RPC message to the site's streamable HTTP endpoint
// /wp-json/pursae/v1/mcp, signed in with an Application Password, so the tools run on the site with
// that password's capabilities and scopes. The site can be local or remote.
func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	site := fs.String("url", os.Getenv("PURSAE_URL"), "site URL, e.g. https://example.com (env PURSAE_URL)")
	user := fs.String("user", os.Getenv("PURSAE_USER"), "user name (env PURSAE_USER)")
	pass := fs.String("password", os.Getenv("PURSAE_APP_PASSWORD"), "Application Password (env PURSAE_APP_PASSWORD)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *site == "" || *user == "" || *pass == "" {
		return errors.New("wpgo mcp needs --url, --user and --password (or PURSAE_URL, PURSAE_USER, PURSAE_APP_PASSWORD)")
	}
	endpoint := strings.TrimRight(*site, "/") + "/wp-json/pursae/v1/mcp"
	client := &http.Client{Timeout: 5 * time.Minute}
	out := json.NewEncoder(stdout)
	in := bufio.NewReader(stdin)
	for {
		line, err := in.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if resp := forwardMCP(ctx, client, endpoint, *user, *pass, bytes.TrimSpace(line)); resp != nil {
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

// forwardMCP posts one message and returns the answer to print (nil for notifications). Transport
// failures become JSON-RPC errors so the client sees them.
func forwardMCP(ctx context.Context, client *http.Client, endpoint, user, pass string, msg []byte) json.RawMessage {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(msg))
	if err != nil {
		return fail(err.Error())
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "wpgo-mcp")
	resp, err := client.Do(req)
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
