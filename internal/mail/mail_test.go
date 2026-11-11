package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type auditRoundTripper func(*http.Request) (*http.Response, error)

func (f auditRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type auditFailingBody struct{ err error }

func (b auditFailingBody) Read([]byte) (int, error) { return 0, b.err }
func (b auditFailingBody) Close() error             { return nil }

func TestAPIReportsResponseReadError(t *testing.T) {
	want := errors.New("response interrupted")
	a := &API{Mailer: "mandrill", Opts: map[string]string{"api_key": "key"}, Client: &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: auditFailingBody{want}}, nil
	})}}
	_, err := a.Send(context.Background(), Email{FromEmail: "f@example.test", To: []string{"t@example.test"}, Text: "hello"})
	if !errors.Is(err, want) {
		t.Fatalf("error=%v, want %v", err, want)
	}
}

func TestOAuthReportsResponseReadError(t *testing.T) {
	want := errors.New("response interrupted")
	o := &OAuth{Mailer: "gmail", Client: &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: auditFailingBody{want}}, nil
	})}}
	if _, err := o.Exchange(context.Background(), "code"); !errors.Is(err, want) {
		t.Fatalf("error=%v, want %v", err, want)
	}
}

func TestOAuthAccountRejectsIncompleteResponse(t *testing.T) {
	for _, tc := range []struct{ provider, body string }{
		{"gmail", `{`}, {"gmail", `{}`}, {"outlook", `{}`},
		{"zoho", `{"data":[{"accountId":"42"}]}`}, {"zoho", `{"data":[{"mailboxAddress":"z@example.test"}]}`},
	} {
		o := &OAuth{Mailer: tc.provider, Client: &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		if _, _, err := o.Account(context.Background(), "token"); err == nil {
			t.Errorf("%s accepted %s", tc.provider, tc.body)
		}
	}
}

func TestOAuthAccountKeepsLargeNumericID(t *testing.T) {
	o := &OAuth{Mailer: "zoho", Client: &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":[{"accountId":123456789012345678,"mailboxAddress":"z@example.test"}]}`))}, nil
	})}}
	_, id, err := o.Account(context.Background(), "token")
	if err != nil || id != "123456789012345678" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestAPIRejectsMixedInvalidToRecipients(t *testing.T) {
	calls := 0
	a := &API{Mailer: "resend", Opts: map[string]string{"api_key": "key"}, Client: &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"ok"}`))}, nil
	})}}
	_, err := a.Send(context.Background(), Email{FromEmail: "f@example.test", To: []string{"good@example.test", "invalid address"}, Text: "hello"})
	if err == nil || calls != 0 {
		t.Fatalf("error=%v requests=%d", err, calls)
	}
}

func TestBuildNormalizesMixedLineEndings(t *testing.T) {
	m := &Mailer{From: "sender@example.test"}
	raw, err := m.Build(Message{To: []string{"a@example.test"}, Body: "one\r\ntwo\nthree"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil || string(body) != "one\r\ntwo\r\nthree" {
		t.Fatalf("decoded body=%q err=%v", body, err)
	}
}

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

func TestBuildPreservesUnicodeAttachmentFilename(t *testing.T) {
	m := &Mailer{From: "a@example.test"}
	raw, err := m.Build(Message{To: []string{"b@example.test"}, Attachments: []Attachment{{Name: "résumé.pdf", Data: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	if _, err := r.NextPart(); err != nil {
		t.Fatal(err)
	}
	part, err := r.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	_, params, err = mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil || params["filename"] != "résumé.pdf" {
		t.Fatalf("params=%v err=%v", params, err)
	}
}

func TestBuildEncodesUnicodeDisplayNames(t *testing.T) {
	m := &Mailer{From: "Gönderen <sender@example.test>"}
	raw, err := m.Build(Message{To: []string{"Özlem <to@example.test>"}, Cc: []string{"José <cc@example.test>"}})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for key, name := range map[string]string{"From": "Gönderen", "To": "Özlem", "Cc": "José"} {
		value := msg.Header.Get(key)
		for _, r := range value {
			if r > 127 {
				t.Errorf("%s contains unencoded character: %q", key, value)
			}
		}
		a, err := mail.ParseAddress(value)
		if err != nil || a.Name != name {
			t.Errorf("%s = %q: %v", key, value, err)
		}
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
