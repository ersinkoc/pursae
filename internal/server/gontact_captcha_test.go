package server

import (
	"bytes"
	"context"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

const gfCaptchaTestForm = `<label> Your name [text* your-name] </label>
<label> Code [captchac your-captcha size:l fg:#112233 bg:#fafafa] [captchar your-captcha size:8 maxlength:6 placeholder "Code"] </label>
[submit "Send"]`

// captchaForm creates a form with an image CAPTCHA and a page that shows it.
func captchaForm(t *testing.T, srv *Server) (*gfForm, string) {
	t.Helper()
	ctx := context.Background()
	f := srv.gontactDefaultForm()
	f.Title, f.Form, f.AuthorID = "Captcha form", gfCaptchaTestForm, 1
	if err := srv.gontactSave(ctx, f, 1); err != nil {
		t.Fatal(err)
	}
	_, link := gontactPage(t, srv, `[contact-form-7 id="`+f.ShortHash()+`"]`)
	return f, link
}

var gfChallengeRe = regexp.MustCompile(`name="_wpcf7_captcha_challenge_your-captcha" value="([0-9]+)"`)

func challengeOf(t *testing.T, page string) string {
	t.Helper()
	m := gfChallengeRe.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no CAPTCHA challenge in the page:\n%s", page)
	}
	return m[1]
}

// setCaptchaAnswer replaces a challenge's answer so a test knows the code in the image.
func setCaptchaAnswer(t *testing.T, srv *Server, prefix, code string) {
	t.Helper()
	tag, err := srv.store.Pool.Exec(context.Background(), `UPDATE gontact_captcha SET answer_hash = $2 WHERE prefix = $1`, prefix, srv.gontactCaptchaHash(prefix, code))
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("challenge %s: %v %d", prefix, err, tag.RowsAffected())
	}
}

func captchaImage(t *testing.T, c *http.Client, u string) (*http.Response, []byte) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestGontactCaptcha(t *testing.T) {
	base, c, srv := gontactSetup(t, true)
	ctx := context.Background()
	f, link := captchaForm(t, srv)
	id := itoa64(f.ID)
	anon := anonClient()

	// Tag rendering.
	_, page, _ := get(t, anon, link)
	prefix := challengeOf(t, page)
	for _, want := range []string{
		// The page optimizer may add fetchpriority to the first image, so the tag is matched in two parts.
		`<input type="hidden" name="_wpcf7_captcha_challenge_your-captcha" value="` + prefix + `" /><img decoding="async" class="wpcf7-form-control wpcf7-captchac wpcf7-captcha-your-captcha" width="84" height="28" alt="CAPTCHA: type the characters shown in this image" src="` + base + gfCaptchaPath + `/` + prefix + `.png"`,
		`<span role="button" tabindex="0" class="wpcf7-captcha-refresh" data-captcha="your-captcha" title="Show a new image" hidden>`,
		`<span class="wpcf7-form-control-wrap" data-name="your-captcha"><input size="8" maxlength="6" class="wpcf7-form-control wpcf7-captchar wpcf7-text" autocomplete="off" aria-invalid="false" placeholder="Code" value="" type="text" name="your-captcha" /></span>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("page lacks %q:\n%s", want, page)
		}
	}

	// The image: a PNG at three times the displayed size, never cached.
	resp, img := captchaImage(t, anon, base+gfCaptchaPath+"/"+prefix+".png")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("image: %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Cache-Control"))
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(img)); err != nil || cfg.Width != 84*gfCaptchaScale || cfg.Height != 28*gfCaptchaScale {
		t.Fatalf("png: %v %+v", err, cfg)
	}
	for _, bad := range []string{"/123.png", "/" + prefix + ".gif", "/../x.png", "/abc.png"} {
		if resp, _ := captchaImage(t, anon, base+gfCaptchaPath+bad); resp.StatusCode != http.StatusNotFound && !(bad == "/../x.png" && resp.StatusCode == http.StatusForbidden) { // 403: the core firewall
			t.Fatalf("%s: %d", bad, resp.StatusCode)
		}
	}

	hidden := hiddenFields(t, page)
	feedback := base + "/wp-json/contact-form-7/v1/contact-forms/" + id + "/feedback"
	fields := func(h map[string]string, kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range h {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}

	// A wrong code: the field is invalid, the challenge is used up and the response refills it.
	setCaptchaAnswer(t, srv, prefix, "AB3C")
	_, out, raw := multipartPost(t, anon, feedback, fields(hidden, "your-name", "Bo", "your-captcha", "XXXX"), nil, testUA)
	if out["status"] != "validation_failed" || !strings.Contains(raw, `{"error_id":"`+hidden["_wpcf7_unit_tag"]+`-ve-your-captcha","field":"your-captcha","idref":null,"message":"Your entered code is incorrect."}`) {
		t.Fatalf("wrong code: %s", raw)
	}
	refill, _ := out["captcha"].(map[string]any)
	newURL, _ := refill["your-captcha"].(string)
	if !regexp.MustCompile(`^`+regexp.QuoteMeta(base+gfCaptchaPath)+`/[0-9]+\.png$`).MatchString(newURL) || strings.Contains(newURL, prefix) {
		t.Fatalf("no new CAPTCHA in the feedback response: %s", raw)
	}
	if resp, _ := captchaImage(t, anon, base+gfCaptchaPath+"/"+prefix+".png"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a used challenge still has an image: %d", resp.StatusCode)
	}
	// The right code for the used challenge no longer helps.
	_, out, raw = multipartPost(t, anon, feedback, fields(hidden, "your-name", "Bo", "your-captcha", "AB3C"), nil, testUA)
	if out["status"] != "validation_failed" {
		t.Fatalf("reused challenge: %s", raw)
	}

	// The right code of a fresh challenge (case and blanks do not matter); then reused, it fails.
	prefix = regexp.MustCompile(`([0-9]+)\.png$`).FindStringSubmatch(newURL)[1]
	setCaptchaAnswer(t, srv, prefix, "AB3C")
	h2 := fields(hidden, "_wpcf7_captcha_challenge_your-captcha", prefix)
	_, out, raw = multipartPost(t, anon, feedback, fields(h2, "your-name", "Bo", "your-captcha", " ab 3c "), nil, testUA)
	if out["status"] != "mail_sent" {
		t.Fatalf("right code: %s", raw)
	}
	_, out, raw = multipartPost(t, anon, feedback, fields(h2, "your-name", "Bo", "your-captcha", "AB3C"), nil, testUA)
	if out["status"] != "validation_failed" || !strings.Contains(raw, "Your entered code is incorrect.") {
		t.Fatalf("reused right code: %s", raw)
	}
	// No challenge at all.
	h3 := fields(hidden)
	delete(h3, "_wpcf7_captcha_challenge_your-captcha")
	if _, out, raw = multipartPost(t, anon, feedback, fields(h3, "your-name", "Bo", "your-captcha", "AB3C"), nil, testUA); out["status"] != "validation_failed" {
		t.Fatalf("no challenge: %s", raw)
	}

	// The refill endpoint rotates the prefix on every call.
	refillURL := base + "/wp-json/contact-form-7/v1/contact-forms/" + id + "/refill"
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		resp, body := captchaImage(t, anon, refillURL)
		m := regexp.MustCompile(`"captcha":\{"your-captcha":"[^"]*/([0-9]+)\.png"\}`).FindStringSubmatch(string(body))
		if resp.StatusCode != http.StatusOK || m == nil || seen[m[1]] || !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
			t.Fatalf("refill %d: %d %s", i, resp.StatusCode, body)
		}
		seen[m[1]] = true
		if resp, _ := captchaImage(t, anon, base+gfCaptchaPath+"/"+m[1]+".png"); resp.StatusCode != http.StatusOK {
			t.Fatalf("refilled image: %d", resp.StatusCode)
		}
	}

	// Expiry: an hour-old challenge has no image and is not accepted; the cleanup event drops it.
	old, err := srv.gontactCaptchaNew(ctx, gfCaptchaOptions(nil))
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := srv.gontactCaptchaNew(ctx, gfCaptchaOptions(nil))
	setCaptchaAnswer(t, srv, old, "AB3C")
	if _, err := srv.store.Pool.Exec(ctx, `UPDATE gontact_captcha SET created_at = now() - interval '61 minutes' WHERE prefix IN ($1, $2)`, old, stale); err != nil {
		t.Fatal(err)
	}
	if resp, _ := captchaImage(t, anon, base+gfCaptchaPath+"/"+stale+".png"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expired image: %d", resp.StatusCode)
	}
	if srv.gontactCaptchaCheck(ctx, old, "AB3C") {
		t.Fatal("an expired challenge was accepted")
	}
	if err := srv.jobHandlers()["gontact_captcha_cleanup"](ctx, nil); err != nil {
		t.Fatal(err)
	}
	var left, fresh int
	_ = srv.store.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE prefix = $1), count(*) FROM gontact_captcha`, stale).Scan(&left, &fresh)
	if left != 0 || fresh < 2 {
		t.Fatalf("cleanup: stale %d, remaining %d", left, fresh)
	}
	var scheduled bool
	srv.ensureEvents(ctx)
	_ = srv.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE name = 'gontact_captcha_cleanup' AND recurrence = 'hourly')`).Scan(&scheduled)
	if !scheduled {
		t.Fatal("the cleanup event is not scheduled")
	}

	// Editor: the two tag generator buttons and the editable message.
	_, body, _ := get(t, c, base+"/wp-admin/admin?page=wpcf7&post="+id+"&action=edit")
	for _, want := range []string{`data-type="captchac" title="Form-tag Generator: CAPTCHA (image)">CAPTCHA (image)</button>`,
		`data-type="captchar" title="Form-tag Generator: CAPTCHA (answer)">CAPTCHA (answer)</button>`, `wpcf7-messages[captcha_not_match]`, "Your entered code is incorrect."} {
		if !strings.Contains(body, want) {
			t.Fatalf("editor lacks %q", want)
		}
	}
	if strings.Contains(body, `<span class="mailtag code used">[your-captcha]</span>`) || strings.Contains(body, `>[your-captcha]<`) {
		t.Fatal("the CAPTCHA is offered as a mail-tag")
	}
	f.Messages["captcha_not_match"] = "Wrong code, try again."
	if err := srv.gontactSave(ctx, f, 1); err != nil {
		t.Fatal(err)
	}
	if _, _, raw = multipartPost(t, anon, feedback, fields(hidden, "your-name", "Bo", "your-captcha", "AB3C"), nil, testUA); !strings.Contains(raw, "Wrong code, try again.") {
		t.Fatalf("custom message: %s", raw)
	}

	// Without JavaScript the page posts to itself and shows a new challenge with the error.
	_, page, _ = get(t, anon, link)
	h4 := hiddenFields(t, page)
	vals := map[string][]string{}
	for k, v := range fields(h4, "your-name", "Bo", "your-captcha", "nope") {
		vals[k] = []string{v}
	}
	_, page, _ = post(t, anon, link, vals)
	if p := challengeOf(t, page); p == h4["_wpcf7_captcha_challenge_your-captcha"] || !strings.Contains(page, `<span class="wpcf7-not-valid-tip" aria-hidden="true">Wrong code, try again.</span>`) ||
		!strings.Contains(page, `wpcf7-captchar wpcf7-text wpcf7-not-valid`) {
		t.Fatalf("no-JS submission:\n%s", page)
	}
}

func TestGontactCaptchaPageCache(t *testing.T) {
	base, _, srv := gontactSetup(t, true)
	ctx := context.Background()
	if err := srv.settings.Set(ctx, map[string]any{"wpgo_cache_enabled": true}); err != nil {
		t.Fatal(err)
	}
	srv.configureCache()
	f, link := captchaForm(t, srv)

	first := anonGet(t, link)
	page1 := bodyOf(first)
	second := anonGet(t, link)
	page2 := bodyOf(second)
	if second.Header.Get("X-WPGo-Cache") != "hit" {
		t.Fatalf("the contact page is not served from the page cache: %q", second.Header.Get("X-WPGo-Cache"))
	}
	cached := challengeOf(t, page1)
	if challengeOf(t, page2) != cached || !strings.Contains(page2, `"cached":1`) {
		t.Fatal("the cached page does not carry the cached flag for the script's refill")
	}
	// Every visitor of the cached page gets a fresh challenge from the refill endpoint (the
	// script calls it on load); refill responses and images bypass the cache.
	anon := anonClient()
	refillURL := base + "/wp-json/contact-form-7/v1/contact-forms/" + itoa64(f.ID) + "/refill"
	var prefixes []string
	for i := 0; i < 2; i++ {
		resp, body := captchaImage(t, anon, refillURL)
		m := regexp.MustCompile(`/([0-9]+)\.png"`).FindStringSubmatch(string(body))
		if m == nil || m[1] == cached || resp.Header.Get("X-WPGo-Cache") == "hit" {
			t.Fatalf("refill on a cached page: %s", body)
		}
		prefixes = append(prefixes, m[1])
		img, _ := captchaImage(t, anon, base+gfCaptchaPath+"/"+m[1]+".png")
		img2, _ := captchaImage(t, anon, base+gfCaptchaPath+"/"+m[1]+".png")
		if img.StatusCode != http.StatusOK || img2.Header.Get("X-WPGo-Cache") == "hit" {
			t.Fatalf("image of a refilled challenge: %d %q", img.StatusCode, img2.Header.Get("X-WPGo-Cache"))
		}
	}
	if prefixes[0] == prefixes[1] {
		t.Fatal("two refills gave the same challenge")
	}
	// A visitor answers the refilled challenge of the cached form.
	hidden := hiddenFields(t, page2)
	hidden["_wpcf7_captcha_challenge_your-captcha"] = prefixes[1]
	hidden["your-name"], hidden["your-captcha"] = "Bo", "Q7RZ"
	setCaptchaAnswer(t, srv, prefixes[1], "Q7RZ")
	_, out, raw := multipartPost(t, anon, base+"/wp-json/contact-form-7/v1/contact-forms/"+itoa64(f.ID)+"/feedback", hidden, nil, testUA)
	if out["status"] != "mail_sent" {
		t.Fatalf("submission from the cached page: %s", raw)
	}
}

func TestGontactCaptchaRender(t *testing.T) {
	for i := 0; i < 200; i++ {
		w := gfCaptchaWord()
		if len(w) != gfCaptchaLen || strings.ContainsAny(w, "0O1lI") {
			t.Fatalf("word %q", w)
		}
	}
	if p := gfCaptchaPrefix(); !gfCaptchaPrefixRe.MatchString(p) {
		t.Fatalf("prefix %q", p)
	}
	for opt, size := range map[string][2]int{"size:s": {60, 20}, "size:m": {72, 24}, "size:l": {84, 28}, "": {72, 24}} {
		o := gfCaptchaOptions(&gfTag{Options: []string{opt, "fg:#FF0000", "bg:#00ff00"}})
		if o.W != size[0] || o.H != size[1] || o.FG.R != 255 || o.BG.G != 255 {
			t.Fatalf("%s: %+v", opt, o)
		}
		b, err := gfCaptchaRender("AB3C", o)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil || img.Bounds().Dx() != size[0]*gfCaptchaScale {
			t.Fatalf("%s: %v", opt, err)
		}
		// Foreground pixels are drawn: some pixel is (nearly) pure red.
		red := false
		for y := 0; y < img.Bounds().Dy() && !red; y++ {
			for x := 0; x < img.Bounds().Dx(); x++ {
				r, g, _, _ := img.At(x, y).RGBA()
				if r > 0xf000 && g < 0x1000 {
					red = true
					break
				}
			}
		}
		if !red {
			t.Fatalf("%s: no text drawn", opt)
		}
	}
}
