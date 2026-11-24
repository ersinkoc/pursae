//go:build !(wasip1 && wasm)

package wpgotest_test

import (
	"testing"

	"wpgo/sdk/go/wpgo"
	"wpgo/sdk/go/wpgo/wpgotest"
)

func TestCapturedCallsAreSnapshots(t *testing.T) {
	h := wpgotest.New(t)
	const dest = "https://example.test/mock"
	h.StubHTTP(dest, 200, "ok")
	if _, err := wpgo.HTTPRequest(wpgo.HTTPRequestArgs{URL: dest, Headers: map[string]string{"Accept": "text/plain"}}); err != nil {
		t.Fatal(err)
	}
	if err := wpgo.SendMail(wpgo.Mail{To: []string{"recipient@example.test"}, Subject: "Example"}); err != nil {
		t.Fatal(err)
	}
	if err := wpgo.AdminNotice("info", "Original"); err != nil {
		t.Fatal(err)
	}
	wpgo.RegisterBlock(wpgo.Block{Name: "audit-ownership", Title: "Original", Attributes: []wpgo.SettingsField{{ID: "label", Label: "Original", Type: "text"}}})
	wpgo.Init()
	requests, mail, notices := h.Requests(), h.Mails(), h.Notices()
	blocks := h.Registered("register_block")
	if len(requests) != 1 || len(mail) != 1 || len(notices) != 1 || len(blocks) != 1 {
		t.Fatalf("fixtures: requests=%d mail=%d notices=%d blocks=%d", len(requests), len(mail), len(notices), len(blocks))
	}
	attrs, ok := blocks[0]["attributes"].([]any)
	if !ok || len(attrs) != 1 {
		t.Fatalf("fixture attributes: %+v", blocks[0])
	}
	field, ok := attrs[0].(map[string]any)
	if !ok {
		t.Fatalf("fixture attribute: %+v", attrs[0])
	}
	gate, done := make(chan struct{}), make(chan struct{})
	go func() {
		<-gate
		requests[0].Headers["Accept"] = "application/json"
		mail[0].To[0] = "changed@example.test"
		notices[0]["message"] = "Changed"
		blocks[0]["title"] = "Changed"
		field["label"] = "Changed"
		close(done)
	}()
	close(gate)
	<-done
	if got := h.Requests()[0].Headers["Accept"]; got != "text/plain" {
		t.Errorf("request header = %q", got)
	}
	if got := h.Mails()[0].To[0]; got != "recipient@example.test" {
		t.Errorf("mail recipient = %q", got)
	}
	if got := h.Notices()[0]["message"]; got != "Original" {
		t.Errorf("notice = %q", got)
	}
	reg := h.Registered("register_block")[0]
	if reg["title"] != "Original" || reg["attributes"].([]any)[0].(map[string]any)["label"] != "Original" {
		t.Errorf("block = %+v", reg)
	}
}

func TestOptionReturnsDetachedJSON(t *testing.T) {
	h := wpgotest.New(t)
	h.SetOption("name", "original")
	raw, found := h.Option("name")
	if !found || string(raw) != `"original"` {
		t.Fatalf("initial option = %s, found=%v", raw, found)
	}
	gate, done := make(chan struct{}), make(chan struct{})
	go func() {
		<-gate
		raw[1] = 'X'
		close(done)
	}()
	close(gate)
	<-done
	if got, found, err := wpgo.GetOption("name"); err != nil || !found || got != "original" {
		t.Fatalf("option = %q, found=%v, err=%v", got, found, err)
	}
}
