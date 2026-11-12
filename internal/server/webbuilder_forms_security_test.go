package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// A Web Builder form mail names the page it was sent from and the site's home. On a site whose
// home URL is a local or private address, a visitor naming another private address in Host must not
// get those links pointed at that address.
func TestWebBuilderFormMailIgnoresForgedLocalHost(t *testing.T) {
	w := wbformStart(t)
	const forged = "192.168.66.6:8080"
	v := wbfValid()
	v.Set(webbuilder.FormNonce, w.formToken(nil, w.page.ID, wbfContact))
	req, _ := http.NewRequest(http.MethodPost, w.base+"/wp-json/web-builder/v2/forms/"+strconv.FormatInt(w.page.ID, 10)+"/"+wbfContact, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "http://"+forged)
	req.Host = forged
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit with a forged Host: %d", resp.StatusCode)
	}
	jobs := mailJobs(t, w.srv)
	if len(jobs) != 1 {
		t.Fatalf("mails: %d", len(jobs))
	}
	body, _ := jobs[0]["Body"].(string)
	if strings.Contains(body, "192.168.66.6") || !strings.Contains(body, w.base+"/") {
		t.Fatalf("form mail does not keep the configured home %q:\n%s", w.base, body)
	}
}
