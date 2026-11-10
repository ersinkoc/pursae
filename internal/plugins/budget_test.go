package plugins

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCallBudgetByType(t *testing.T) {
	var c Config
	c.setDefaults()
	cases := []struct {
		req    handleRequest
		want   time.Duration
		exempt bool
	}{
		{handleRequest{Type: "filter", Hook: "the_content"}, 2 * time.Second, false},
		{handleRequest{Type: "shortcode", Hook: "x"}, 2 * time.Second, false},
		{handleRequest{Type: "rest", Hook: "/x"}, 10 * time.Second, true},
		{handleRequest{Type: "action", Hook: "cron_sync"}, 30 * time.Second, true},
	}
	for _, tc := range cases {
		if got := c.budget(&tc.req); got != tc.want {
			t.Errorf("%s/%s budget = %v, want %v", tc.req.Type, tc.req.Hook, got, tc.want)
		}
		if got := c.slowExempt(&tc.req); got != tc.exempt {
			t.Errorf("%s/%s slowExempt = %v", tc.req.Type, tc.req.Hook, got)
		}
	}
}

func TestHostHTTPRequest(t *testing.T) {
	var cfg Config
	cfg.setDefaults()
	var got HTTPRequest
	host := NewMemoryHost()
	host.HTTPReq = func(_ context.Context, r HTTPRequest) (*HTTPResponse, error) {
		got = r
		return &HTTPResponse{Status: 201, Body: "ok"}, nil
	}
	p := &Plugin{host: host, rt: &Runtime{cfg: cfg}, manifest: &Manifest{Slug: "x"}}
	call := func(body string) (any, error) { return hostHTTPRequest(context.Background(), p, []byte(body)) }

	out, err := call(`{"method":"post","url":"https://example.com/hook","headers":{"Authorization":"Bearer t"},"body":"{}"}`)
	if err != nil || out.(*HTTPResponse).Status != 201 {
		t.Fatalf("out=%v err=%v", out, err)
	}
	if got.Method != "POST" || got.Headers["Authorization"] != "Bearer t" || got.Body != "{}" {
		t.Errorf("request not passed through: %+v", got)
	}
	for _, bad := range []string{
		`{"method":"TRACE","url":"https://example.com"}`,
		`{"url":"ftp://example.com"}`,
		`{"url":"https://example.com","headers":{"Host":"evil"}}`,
		`{"url":"https://example.com","headers":{"X-A":"a\r\nX-B: b"}}`,
		`{"method":"POST","url":"https://example.com","body":"` + strings.Repeat("a", MaxHTTPRequestBody+1) + `"}`,
	} {
		if _, err := call(bad); err == nil {
			t.Errorf("accepted %.60s", bad)
		}
	}
}

func TestHostSendMail(t *testing.T) {
	var cfg Config
	cfg.setDefaults()
	host := NewMemoryHost()
	p := &Plugin{host: host, rt: &Runtime{cfg: cfg}, manifest: &Manifest{Slug: "x"}}
	call := func(body string) error { _, err := hostSendMail(context.Background(), p, []byte(body)); return err }
	if err := call(`{"to":["a@example.com"],"subject":"Hi","body":"text"}`); err != nil || len(host.Mails) != 1 {
		t.Fatalf("err=%v mails=%d", err, len(host.Mails))
	}
	for _, bad := range []string{
		`{"to":[],"subject":"Hi","body":"x"}`,
		`{"to":["not-an-address"],"subject":"Hi","body":"x"}`,
		`{"to":["a@example.com\r\nBcc: b@example.com"],"subject":"Hi","body":"x"}`,
		`{"to":["a@example.com"],"subject":"Hi\r\nBcc: b@example.com","body":"x"}`,
		`{"to":["a@example.com"],"subject":"Hi"}`,
	} {
		if call(bad) == nil {
			t.Errorf("accepted %.70s", bad)
		}
	}
	if HookPermission("wp_login") != PermUsersRead || HookPermission("wp_head") != PermContentFilter {
		t.Error("hook permissions not mapped")
	}
}
