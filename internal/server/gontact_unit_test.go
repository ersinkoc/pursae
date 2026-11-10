package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wpgo/internal/settings"
)

func gfUnitSub(tags []*gfTag, raw map[string][]string) *gfSubmission {
	posted := map[string][]string{}
	for k, v := range raw {
		posted[k] = v
	}
	return &gfSubmission{form: &gfForm{}, tags: tags, raw: raw, posted: posted, files: map[string][]gfUpload{}}
}

func gfInvalidNames(sub *gfSubmission) map[string]string {
	out := map[string]string{}
	for _, i := range sub.invalid {
		out[i.name] = i.reason
	}
	return out
}

// validate needs no database: the field rules are table tested here.
func TestGfValidateRules(t *testing.T) {
	cases := []struct {
		name    string
		tag     string
		attrs   string
		value   string
		invalid bool
	}{
		{"required empty", "text*", "f", "  ", true},
		{"required filled", "text*", "f", "x", false},
		{"optional empty", "text", "f", "", false},
		{"bad email", "email", "f", "not-an-email", true},
		{"good email", "email", "f", "a@example.com", false},
		{"bad url", "url", "f", "nope", true},
		{"good url", "url", "f", "https://example.com/x", false},
		{"bad tel", "tel", "f", "abc", true},
		{"too long", "text", "f maxlength:3", "abcd", true},
		{"too short", "text", "f minlength:3", "ab", true},
		{"number below min", "number", "f min:5", "3", true},
		{"number above max", "number", "f max:5", "9", true},
		{"number ok", "number", "f min:1 max:5", "3", false},
		{"not a number", "number", "f", "x", true},
	}
	for _, c := range cases {
		tg := gfParseTag(c.tag, c.attrs, "")
		if tg == nil {
			t.Fatalf("%s: tag did not parse", c.name)
		}
		sub := gfUnitSub([]*gfTag{tg}, map[string][]string{"f": {c.value}})
		sub.validate()
		if got := len(sub.invalid) > 0; got != c.invalid {
			t.Errorf("%s: invalid = %v, want %v (%v)", c.name, got, c.invalid, sub.invalid)
		}
	}
	// Fields inside a hidden conditional group are not validated.
	sub := gfUnitSub([]*gfTag{gfParseTag("text*", "f", "")}, map[string][]string{"f": {""}})
	sub.hidden = map[string]bool{"f": true}
	if sub.validate(); len(sub.invalid) != 0 {
		t.Errorf("hidden field validated: %v", sub.invalid)
	}
}

func TestGfComposeMailBody(t *testing.T) {
	tags := []*gfTag{gfParseTag("text", "your-name", ""), gfParseTag("text", "your-subject", "")}
	sub := gfUnitSub(tags, map[string][]string{"your-name": {"Ann <b>"}, "your-subject": {""}})
	msg, ok := sub.composeMail(gfMail{Recipient: "a@example.com, bad", Subject: "Hi [your-name]", Body: "Name: [your-name]\nSubj: [your-subject]\n[[escaped]]",
		ExcludeBlank: true})
	if !ok || len(msg.To) != 1 {
		t.Fatalf("recipients: ok=%v to=%v", ok, msg.To)
	}
	if msg.Subject != "Hi Ann <b>" {
		t.Errorf("subject = %q", msg.Subject)
	}
	if strings.Contains(msg.Body, "Subj:") || !strings.Contains(msg.Body, "Name: Ann <b>") || !strings.Contains(msg.Body, "[escaped]") {
		t.Errorf("body = %q", msg.Body)
	}
	// HTML mode escapes values.
	hm, _ := sub.composeMail(gfMail{Recipient: "a@example.com", Subject: "s", Body: "[your-name]", UseHTML: true})
	if !strings.Contains(hm.HTML, "Ann &lt;b&gt;") || strings.Contains(hm.HTML, "<b>") {
		t.Errorf("html = %q", hm.HTML)
	}
	// No valid recipient: not sent.
	if _, ok := sub.composeMail(gfMail{Recipient: "nonsense", Body: "x"}); ok {
		t.Error("composed without a recipient")
	}
}

func TestGfAttachments(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	_ = os.WriteFile(outside, []byte("secret"), 0o644)
	defer os.Remove(outside)

	s := &Server{settings: settings.New(nil)}
	s.cfg.UploadsDir = dir
	sub := gfUnitSub(nil, map[string][]string{})
	sub.s = s
	sub.ctx = t.Context()
	sub.files["upload"] = []gfUpload{{name: "u.pdf", contentType: "application/pdf", data: []byte("%PDF")}}

	got := sub.attachments("[upload]\nuploads/a.txt\nuploads/../secret.txt\n/etc/passwd")
	if len(got) != 2 || got[0].Name != "u.pdf" || got[1].Name != "a.txt" || string(got[1].Data) != "hello" {
		t.Fatalf("attachments = %+v", got)
	}
}
