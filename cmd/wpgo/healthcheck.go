package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// healthcheck asks the local server's /healthz (or /readyz with "ready") and fails unless it
// answers 200 within three seconds. The image has no curl or wget worth relying on, so the
// container HEALTHCHECK runs the binary itself: `wpgo healthcheck`.
func healthcheck(addr string, args []string) error {
	path := "/healthz"
	if len(args) > 0 && args[0] == "ready" {
		path = "/readyz"
	}
	u, err := healthURL(addr, path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", path, resp.Status)
	}
	return nil
}

// healthURL is the loopback URL of path on the listen address (":8080" → http://127.0.0.1:8080).
func healthURL(addr, path string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("WPGO_ADDR %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path, nil
}
