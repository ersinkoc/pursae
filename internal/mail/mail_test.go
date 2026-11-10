package mail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAndFileDelivery(t *testing.T) {
	dir := t.TempDir()
	m := &Mailer{From: "My Site <wordpress@example.test>", Dir: dir}
	raw, err := m.Build(Message{To: []string{"a@example.test"}, Subject: "Héllo [My Site]", Body: "Line one\nLine two"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"From: My Site <wordpress@example.test>", "To: a@example.test", "Subject: =?utf-8?q?", "Content-Type: text/plain; charset=UTF-8", "Line one\r\nLine two"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if _, err := m.Build(Message{To: []string{"not an address"}}); err == nil {
		t.Error("invalid recipient accepted")
	}
	if err := m.Send(context.Background(), Message{To: []string{"a@example.test"}, Subject: "x", Body: "y", HTML: "<p>y</p>"}); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.eml"))
	if len(files) != 1 {
		t.Fatalf("want 1 .eml, got %d", len(files))
	}
	b, _ := os.ReadFile(files[0])
	if !strings.Contains(string(b), "multipart/alternative") {
		t.Error("html part missing")
	}
}

func TestBuildReplyToAndAttachmentNameAreHeaderSafe(t *testing.T) {
	m := &Mailer{From: "Site <wordpress@example.test>"}
	raw, err := m.Build(Message{To: []string{"a@example.test"}, Subject: "s", Body: "b",
		ReplyTo:     "x@example.test\r\nBcc: victim@example.test\r\nContent-Type: text/html",
		Attachments: []Attachment{{Name: `a".exe"; x="y.pdf`, Data: []byte("d")}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Contains(s, "Bcc:") || strings.Contains(s, "Reply-To") || strings.Contains(s, "victim@") {
		t.Fatalf("Reply-To injected headers:\n%s", s)
	}
	if strings.Contains(s, `filename="a"`) {
		t.Fatalf("attachment name broke out of its quotes:\n%s", s)
	}
	raw, _ = m.Build(Message{To: []string{"a@example.test"}, Subject: "s", Body: "b", ReplyTo: "Jo Doe <jo@example.test>"})
	if !strings.Contains(string(raw), "Reply-To: \"Jo Doe\" <jo@example.test>\r\n") {
		t.Fatalf("valid Reply-To lost:\n%s", raw)
	}
}
