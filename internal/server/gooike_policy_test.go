package server

import (
	"context"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/store"
)

func TestGooikeConsentLimiterIsPerIDAndAddress(t *testing.T) {
	gooikeLimiter.Lock()
	gooikeLimiter.hits = map[string]int{}
	gooikeLimiter.Unlock()
	// Many visitors behind one /24 each keep their record (the old limit was 30 per /24 and minute).
	for i := 1; i <= 100; i++ {
		ip := "203.0.113." + strconv.Itoa(i)
		id := "0123456789abcdef" + strconv.Itoa(1000+i)
		if !gooikeConsentAllowed(ip, id) {
			t.Fatalf("visitor %d behind a shared /24 was limited", i)
		}
	}
	// One consent ID cannot flood.
	allowed := 0
	for i := 0; i < 20; i++ {
		if gooikeConsentAllowed("198.51.100.7", "feedfeedfeedfeed0001") {
			allowed++
		}
	}
	if allowed != 8 {
		t.Errorf("one consent ID got %d records a minute, want 8", allowed)
	}
}

func TestGooikePolicyDocs(t *testing.T) {
	for reg, title := range map[string]string{"gdpr": "Cookie Policy", "ccpa": "Cookie Policy", "kvkk": "Cerez Politikasi"} {
		d := gooikeCookiePolicyDoc(reg, "My <Site>")
		if d.Title != title || !strings.Contains(d.Content, "[gooike_cookie_table]") || !strings.Contains(d.Content, "[gooike_preferences") {
			t.Errorf("%s policy: %q", reg, d.Title)
		}
		if strings.Contains(d.Content, "<Site>") {
			t.Errorf("%s policy does not escape the site name", reg)
		}
	}
	if n := gooikeNoticeDoc("Acme"); !strings.Contains(n.Content, "KVKK") || !strings.Contains(n.Content, "madde 11") {
		t.Error("the KVKK notice lacks the rights section")
	}
	for k, p := range gooikeTextPresets {
		tx := p.Texts()
		if tx.Accept == "" || tx.Title == "" || tx.Message == "" || tx.EmbedText == "" || strings.Count(tx.EmbedText, "%s") != 2 {
			t.Errorf("preset %s is incomplete", k)
		}
	}
}

func TestGooikePolicyPagesAndTextPreset(t *testing.T) {
	base, c, srv := gooikeSite(t)
	gooikePost(t, c, base, url.Values{"page": {"settings"}, "do": {"settings"}, "regulation": {"kvkk"}, "expiry_months": {"12"}, "retention_months": {"12"}, "ip_mode": {"truncate"}})
	gooikePost(t, c, base, url.Values{"page": {"settings"}, "do": {"policy_pages"}})
	posts, _, err := srv.store.QueryPosts(context.Background(), store.PostQuery{Types: []string{"page"}, Statuses: []string{"draft"}, PerPage: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, p := range posts {
		found[p.Slug] = strings.Contains(p.Content, "gooike_")
	}
	if _, ok := found["aydinlatma-metni"]; !found["cerez-politikasi"] || !ok {
		t.Errorf("KVKK pages not created: %v", found)
	}
	gooikePost(t, c, base, url.Values{"page": {"banner"}, "tab": {"texts"}, "do": {"texts_preset"}, "preset": {"tr"}})
	if got := srv.gooikeLoad().Texts.Accept; got != "Tumunu kabul et" {
		t.Errorf("Turkish preset not applied: %q", got)
	}
}

// TestGooikeJS runs the scenario tests of web/gooike/gooike.js when Node is available.
func TestGooikeJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "--test", "../../web/gooike/gooike.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("gooike.js scenarios failed:\n%s", out)
	}
}
