package server

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"wpgo/internal/plugins"
)

// pluginRowLink finds a Plugins screen row action of a plugin (by its URL slug).
func pluginRowLink(t *testing.T, c *http.Client, base, action, urlSlug string) string {
	t.Helper()
	_, body, _ := get(t, c, base+"/wp-admin/plugins")
	m := regexp.MustCompile(`href="(/wp-admin/plugins\?action=` + action + `&amp;plugin=` + regexp.QuoteMeta(urlSlug) + `&amp;[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no %s link for %s on the Plugins screen", action, urlSlug)
	}
	return base + html.UnescapeString(m[1])
}

func setupNonce(t *testing.T, body string) string {
	t.Helper()
	m := regexp.MustCompile(`name="_wpnonce" value="([^"]+)"`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no nonce on the setup guide")
	}
	return m[1]
}

// TestEveryBuiltinPluginHasASetupGuide: each built-in plugin has a guide whose steps link to admin
// screens, and the guide's texts are translated to Turkish.
func TestEveryBuiltinPluginHasASetupGuide(t *testing.T) {
	_, _, srv := testServerBuilderOff(t)
	guides := nativeSetupGuides()
	cat := srv.lang.Catalog("tr_TR")
	for _, r := range srv.nativePluginRows() {
		g, ok := guides[r.Slug]
		if !ok {
			t.Errorf("%s (%s) has no setup guide", r.Name, r.Slug)
			continue
		}
		full, _ := srv.setupGuideForRow(context.Background(), r)
		if len(g.Steps) == 0 && full.WizardURL == "" {
			t.Errorf("%s: neither steps nor a setup wizard", r.Slug)
		}
		texts := []string{g.Intro}
		for _, st := range g.Steps {
			if !plugins.ValidSetupURL(st.URL) {
				t.Errorf("%s: step %q links to %q", r.Slug, st.Title, st.URL)
			}
			texts = append(texts, st.Title, st.Body)
		}
		for _, s := range texts {
			if _, ok := cat.Lookup(s); !ok {
				t.Errorf("%s: no Turkish translation of %q", r.Slug, s)
			}
		}
	}
}

// TestPluginSetupFirstActivation: the first activation asks guide or settings; the answer is
// remembered, the guide walks the steps and the Plugins screen keeps a link to it.
func TestPluginSetupFirstActivation(t *testing.T) {
	ts, c, srv := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)

	// First activation: the question.
	code, _, loc := get(t, c, pluginRowLink(t, c, base, "activate", bmgrSlug))
	if code != http.StatusFound || loc != pluginSetupURL(bmgrSlug, true) {
		t.Fatalf("first activation: %d %q", code, loc)
	}
	if !srv.bmgrOn() {
		t.Fatal("the plugin was not activated")
	}
	_, body, _ := get(t, c, base+loc)
	for _, want := range []string{"Pursae Backup Manager", "Start the setup guide", "Go straight to the settings"} {
		if !strings.Contains(body, want) {
			t.Errorf("welcome screen lacks %q", want)
		}
	}
	nonce := setupNonce(t, body)

	// "Go straight to the settings".
	code, _, loc = post(t, c, base+pluginSetupPath, url.Values{"_wpnonce": {nonce}, "plugin": {bmgrSlug}, "do": {"settings"}})
	if code != http.StatusFound || loc != "/wp-admin/admin?page="+bmgrSlug {
		t.Fatalf("settings choice: %d %q", code, loc)
	}
	if !srv.pluginSetupLoad(bmgrSlug).Asked {
		t.Fatal("the answer was not remembered")
	}

	// Asked once: deactivating and activating again goes back to the Plugins screen.
	get(t, c, pluginRowLink(t, c, base, "deactivate", bmgrSlug))
	if _, _, loc = get(t, c, pluginRowLink(t, c, base, "activate", bmgrSlug)); !activationOK(loc) {
		t.Fatalf("second activation went to %q", loc)
	}

	// The row links the guide; the guide starts at step 1 and walks on.
	_, plist, _ := get(t, c, base+"/wp-admin/plugins")
	if !strings.Contains(plist, `href="`+html.EscapeString(pluginSetupURL(bmgrSlug, false))+`"`) {
		t.Error("no Setup guide link on the Plugins screen")
	}
	_, body, _ = get(t, c, base+pluginSetupURL(bmgrSlug, false))
	if !strings.Contains(body, "Step 1 of 3") || !strings.Contains(body, "Take your first backup") || !strings.Contains(body, "0 of 3 steps done") {
		t.Fatalf("guide does not start at step 1:\n%s", body)
	}
	code, _, loc = post(t, c, base+pluginSetupPath, url.Values{"_wpnonce": {nonce}, "plugin": {bmgrSlug}, "do": {"done"}, "step": {"0"}})
	if code != http.StatusFound || !strings.HasSuffix(loc, "&step=2") {
		t.Fatalf("done: %d %q", code, loc)
	}
	_, body, _ = get(t, c, base+loc)
	if !strings.Contains(body, "Step 2 of 3") || !strings.Contains(body, "1 of 3 steps done") {
		t.Fatal("guide did not move on to step 2")
	}
	for _, step := range []string{"1", "2"} {
		_, _, loc = post(t, c, base+pluginSetupPath, url.Values{"_wpnonce": {nonce}, "plugin": {bmgrSlug}, "do": {"done"}, "step": {step}})
	}
	_, body, _ = get(t, c, base+loc)
	if !strings.Contains(body, "All set!") || !strings.Contains(body, "3 of 3 steps done") || !srv.pluginSetupLoad(bmgrSlug).Finished {
		t.Fatal("finishing the last step did not finish the guide")
	}

	// A bad nonce changes nothing.
	if code, _, _ := post(t, c, base+pluginSetupPath, url.Values{"_wpnonce": {"x"}, "plugin": {bmgrSlug}, "do": {"restart"}}); code == http.StatusFound {
		t.Error("restart without a valid nonce was accepted")
	}

	// An inactive plugin's guide is refused.
	if _, _, loc := get(t, c, base+pluginSetupURL(maintSlug, false)); !strings.Contains(loc, "error=") {
		t.Errorf("guide of an inactive plugin: %q", loc)
	}
}

// TestPluginSetupWizardPlugins: for a plugin with its own setup wizard, "Start the setup guide" opens it.
func TestPluginSetupWizardPlugins(t *testing.T) {
	ts, c, _ := testServerBuilderOff(t)
	base := ts.URL
	installAndLogin(t, ts, c)
	_, _, loc := get(t, c, pluginRowLink(t, c, base, "activate", "seo-go"))
	if loc != pluginSetupURL(seogoSlug, true) {
		t.Fatalf("first activation of Pursae SEO went to %q", loc)
	}
	_, body, _ := get(t, c, base+loc)
	code, _, loc := post(t, c, base+pluginSetupPath, url.Values{"_wpnonce": {setupNonce(t, body)}, "plugin": {seogoSlug}, "do": {"start"}})
	if code != http.StatusFound || loc != "/wp-admin/seogo?page=wizard" {
		t.Fatalf("start guide: %d %q", code, loc)
	}
	if _, _, loc := get(t, c, base+pluginSetupURL(seogoSlug, false)); loc != "/wp-admin/seogo?page=wizard" {
		t.Errorf("guide link of Pursae SEO goes to %q", loc)
	}
}

func TestManifestSetupValidation(t *testing.T) {
	base := `{"slug":"demo-x","name":"Demo","version":"1.0.0","permissions":[],"hooks":[],"setup":`
	for _, tc := range []struct {
		setup string
		ok    bool
	}{
		{`[{"title":"Connect","body":"Paste the key.","url":"/wp-admin/admin?page=demo-x"}]`, true},
		{`[{"title":"No link"}]`, true},
		{`[{"title":""}]`, false},
		{`[{"title":"Away","url":"https://evil.example/"}]`, false},
		{`[{"title":"Proto","url":"//evil.example/wp-admin/"}]`, false},
		{`[{"title":"Script","url":"/wp-admin/x\"><script>"}]`, false},
		{`[{"title":"Up","url":"/wp-admin/../wp-login"}]`, false},
	} {
		_, err := plugins.ParseManifest([]byte(base + tc.setup + `}`))
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.setup, err)
		}
	}
}

// activationOK reports whether loc is where a successful activation from the Plugins screen leads:
// the Plugins screen, or the first-activation question of a plugin with a setup guide.
func activationOK(loc string) bool {
	return strings.Contains(loc, "activate=true") || (strings.HasPrefix(loc, pluginSetupPath+"?plugin=") && strings.HasSuffix(loc, "&welcome=1"))
}

// toggleOK is activationOK for either Plugins screen action.
func toggleOK(loc, action string) bool {
	if action == "activate" {
		return activationOK(loc)
	}
	return strings.Contains(loc, action+"=true")
}
