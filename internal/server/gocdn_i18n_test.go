package server

import (
	"context"
	"strings"
	"testing"

	"wpgo/internal/i18n"
)

// TestGoCDNWholeSentences: in Turkish the Pursae CDN screens show their sentences as whole
// translations (a value or an input in the middle no longer splits them into fragments).
func TestGoCDNWholeSentences(t *testing.T) {
	s := gocdnSite(t)
	s.configure(nil)
	if err := s.srv.settings.Set(context.Background(), map[string]any{"WPLANG": "tr_TR"}); err != nil {
		t.Fatal(err)
	}
	cat := s.srv.lang.Catalog("tr_TR")
	tr := func(msgid string, args ...string) string {
		t.Helper()
		v, ok := cat.Lookup(msgid)
		if !ok {
			t.Fatalf("no Turkish translation of %q", msgid)
		}
		return i18n.Sprintf(v, args...)
	}
	_, body, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn")
	for _, want := range []string{
		tr("Path-style requests (<code>endpoint/bucket/key</code> instead of <code>bucket.endpoint/key</code>; MinIO and most self-hosted servers)"),
		tr("Upload objects as <code>public-read</code>"),
		tr(`Prefix of every key; <code>2026/10/photo.jpg</code> is stored as <code translate="no">%s</code>.`, "wp-content/uploads/2026/10/photo.jpg"),
		tr(`Leave empty to use the provider's endpoint for the region (now <code translate="no">%s</code>). Cloudflare R2: <code>https://&lt;ACCOUNT_ID&gt;.r2.cloudflarestorage.com</code>; MinIO: e.g. <code>http://127.0.0.1:9000</code>.`, s.fake.URL),
		tr("SSE-KMS: encrypt with an AWS KMS key (<code>aws:kms</code>)"),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("storage screen lacks %q", want)
		}
	}
	for _, fragment := range []string{"Path-style requests", "Upload objects as", "is stored as", "Leave empty to use", "encrypt with an AWS KMS key"} {
		if strings.Contains(body, fragment) {
			t.Errorf("storage screen still shows the English %q", fragment)
		}
	}
	_, body, _ = get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=purge")
	if want := tr("Purge the CDN (everything)"); !strings.Contains(body, want) {
		t.Errorf("purge screen lacks %q", want)
	}
	// The progress the Media Offload screen polls: whole sentences, translated on the server.
	_, js, _ := get(t, s.c, s.ts.URL+"/wp-admin/gocdn?page=offload&action=progress")
	if want := tr("%1$d done, %2$d failed of %3$d (%4$d files)", "0", "0", "0", "0"); !strings.Contains(js, want) {
		t.Errorf("progress lacks %q: %s", want, js)
	}
}
