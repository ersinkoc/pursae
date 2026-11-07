package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"wpgo/internal/gocdn"
)

// Purging. Every change that moves URLs (settings, a finished bulk job, the Purge screen) empties
// Pursae Cache through purgeAll. One or more CDNs can be configured on the Purge screen (the main
// provider and any number of additional ones, each with its own credentials; a purge goes to all of
// them at once and a failure is reported per provider). The providers: Cloudflare (zone ID
// + API token with "Zone › Cache Purge"), Amazon CloudFront (distribution ID + IAM key with
// cloudfront:CreateInvalidation; invalidations signed with GoCDN's own SigV4 signer), BunnyCDN
// (pull zone ID + account API key), KeyCDN (zone ID + API key) or Fastly (service ID + API token
// with purge_all / purge_select). The Purge screen purges it, and with "purge automatically" every
// automatic purge of GoCDN (purge everything) and every deleted offloaded file (its URLs) is sent
// to it. Secrets are sealed like the storage secret and never shown again.

var gocdnZoneID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// The providers' API bases (tests point them at httptest fakes).
var (
	gocdnCloudFrontBase = "https://cloudfront.amazonaws.com"
	gocdnBunnyBase      = "https://api.bunny.net"
	gocdnKeyCDNBase     = "https://api.keycdn.com"
	gocdnFastlyBase     = "https://api.fastly.com"
)

// gocdnPurger describes a CDN purge provider for the settings screen.
type gocdnPurger struct {
	Key, Name                         string
	ZoneLabel, KeyIDLabel, SecretName string // KeyIDLabel "" = no key ID field
	Zone                              *regexp.Regexp
	Help                              string
}

var gocdnPurgers = []gocdnPurger{
	{Key: "cloudflare", Name: "Cloudflare", ZoneLabel: "Zone ID", SecretName: "API token", Zone: gocdnZoneID,
		Help: "A token with the Zone › Cache Purge permission; the zone ID is on the zone's Overview page."},
	{Key: "cloudfront", Name: "Amazon CloudFront", ZoneLabel: "Distribution ID", KeyIDLabel: "Access key ID", SecretName: "Secret access key",
		Zone: regexp.MustCompile(`^[A-Z0-9]{8,32}$`), Help: "An IAM key allowed cloudfront:CreateInvalidation on the distribution. Purging everything invalidates /*."},
	{Key: "bunny", Name: "BunnyCDN", ZoneLabel: "Pull zone ID", SecretName: "Account API key", Zone: regexp.MustCompile(`^[0-9]{1,20}$`),
		Help: "The API key of your bunny.net account (Account › API); the pull zone ID is in the pull zone's URL."},
	{Key: "keycdn", Name: "KeyCDN", ZoneLabel: "Zone ID", SecretName: "API key", Zone: regexp.MustCompile(`^[0-9]{1,20}$`),
		Help: "The API key of your KeyCDN account (Account Settings › Authentication)."},
	{Key: "fastly", Name: "Fastly", ZoneLabel: "Service ID", SecretName: "API token", Zone: regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`),
		Help: "An API token with the purge_all and purge_select scopes for the service."},
}

func gocdnPurgerByKey(k string) *gocdnPurger {
	if k == "" {
		return &gocdnPurger{}
	}
	for i := range gocdnPurgers {
		if gocdnPurgers[i].Key == k {
			return &gocdnPurgers[i]
		}
	}
	return nil
}

// gocdnPurgeConf is an additional purge provider: the primary one lives in the flat settings
// (PurgeProvider and its fields), more of them here. For Cloudflare Zone is the zone ID and Secret
// the API token; the other providers use the zone / service ID, key ID (CloudFront) and secret.
type gocdnPurgeConf struct {
	Provider string `json:"provider"`
	Zone     string `json:"zone"`
	KeyID    string `json:"access-key-id,omitempty"`
	Secret   string `json:"secret"` // sealed
}

// gocdnCleanExtras drops unknown, duplicate and empty providers and the primary one.
func gocdnCleanExtras(primary string, in []gocdnPurgeConf) []gocdnPurgeConf {
	var out []gocdnPurgeConf
	seen := map[string]bool{primary: true, "": true}
	for _, c := range in {
		if seen[c.Provider] || gocdnPurgerByKey(c.Provider) == nil {
			continue
		}
		seen[c.Provider] = true
		c.Zone, c.KeyID = strings.TrimSpace(c.Zone), strings.TrimSpace(c.KeyID)
		out = append(out, c)
	}
	return out
}

// gocdnPurgeTargets are the configured purge providers, the primary one first (whether or not
// their credentials are complete: gocdnTargetReady says).
func gocdnPurgeTargets(st gocdnSettings) []gocdnPurgeConf {
	var out []gocdnPurgeConf
	switch st.PurgeProvider {
	case "":
	case "cloudflare":
		out = append(out, gocdnPurgeConf{Provider: "cloudflare", Zone: st.CFZone, Secret: st.CFToken})
	default:
		out = append(out, gocdnPurgeConf{Provider: st.PurgeProvider, Zone: st.PurgeZone, KeyID: st.PurgeKeyID, Secret: st.PurgeSecret})
	}
	return append(out, gocdnCleanExtras(st.PurgeProvider, st.PurgeExtra)...)
}

// gocdnTargetReady reports whether a purge provider's credentials are complete.
func (s *Server) gocdnTargetReady(t gocdnPurgeConf) bool {
	p := gocdnPurgerByKey(t.Provider)
	switch {
	case t.Provider == "" || p == nil:
		return false
	case t.Provider == "cloudflare":
		return gocdnZoneID.MatchString(t.Zone) && s.gocdnReveal(t.Secret) != ""
	}
	return p.Zone.MatchString(t.Zone) && s.gocdnReveal(t.Secret) != "" && (p.KeyIDLabel == "" || t.KeyID != "")
}

// gocdnPurgeReady reports whether any CDN purge provider is fully configured.
func (s *Server) gocdnPurgeReady(st gocdnSettings) bool {
	for _, t := range gocdnPurgeTargets(st) {
		if s.gocdnTargetReady(t) {
			return true
		}
	}
	return false
}

// gocdnPurge empties the page cache; withCDN also purges the CDNs (in the background) when
// automatic purging is on.
func (s *Server) gocdnPurge(ctx context.Context, withCDN bool) {
	s.purgeAll(ctx)
	if !withCDN {
		return
	}
	st := s.gocdnLoad()
	if len(gocdnPurgeTargets(st)) > 0 && st.CFAuto && s.gocdnOn() {
		s.gocdnQueuePurge(st, nil)
	}
}

// gocdnCDNPurgeLogged purges every provider and logs a warning for each one that fails.
func (s *Server) gocdnCDNPurgeLogged(ctx context.Context, st gocdnSettings, urls []string) {
	for _, r := range s.gocdnCDNPurgeEach(ctx, st, urls) {
		if r.Err != nil {
			slog.Warn("gocdn: CDN purge", "provider", r.Provider, "err", r.Err)
		}
	}
}

// Automatic purges are coalesced: requests arriving within gocdnPurgeDelay of each other become one
// purge (of everything when any of them asked for everything, or when too many URLs pile up), and
// at most one purge runs at a time. A burst of saves therefore costs one API call per provider
// instead of one goroutine and one call each.
const (
	gocdnPurgeDelay   = 2 * time.Second
	gocdnPurgeMaxURLs = 500
	gocdnPurgeTimeout = 2 * time.Minute
)

// cdnPurgeQueue is the pending automatic CDN purge (runtimeState.cdn).
type cdnPurgeQueue struct {
	mu      sync.Mutex
	pending bool
	all     bool
	urls    []string
	seen    map[string]bool
	st      gocdnSettings
	timer   *time.Timer
	running bool
	wg      sync.WaitGroup
	delay   time.Duration                                              // tests; 0 means gocdnPurgeDelay
	run     func(ctx context.Context, st gocdnSettings, urls []string) // tests; nil means the providers
}

// gocdnQueuePurge schedules a CDN purge of urls (everything when urls is empty) with settings st.
func (s *Server) gocdnQueuePurge(st gocdnSettings, urls []string) {
	q := &s.rt.cdn
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending, q.st = true, st
	if len(urls) == 0 {
		q.all = true
	}
	if !q.all {
		if q.seen == nil {
			q.seen = map[string]bool{}
		}
		for _, u := range urls {
			if !q.seen[u] {
				q.seen[u] = true
				q.urls = append(q.urls, u)
			}
		}
		if len(q.urls) > gocdnPurgeMaxURLs {
			q.all = true
		}
	}
	if q.all {
		q.urls, q.seen = nil, nil
	}
	if q.timer == nil {
		d := q.delay
		if d <= 0 {
			d = gocdnPurgeDelay
		}
		q.timer = time.AfterFunc(d, func() { q.drain(s) })
	}
}

// take removes the pending batch; ok is false when nothing is pending.
func (q *cdnPurgeQueue) take() (st gocdnSettings, urls []string, ok bool) {
	if !q.pending {
		return st, nil, false
	}
	st, urls = q.st, q.urls
	if q.all {
		urls = nil
	}
	q.pending, q.all, q.urls, q.seen = false, false, nil, nil
	return st, urls, true
}

// drain runs the pending purges one after the other (a purge queued meanwhile follows).
func (q *cdnPurgeQueue) drain(s *Server) {
	q.mu.Lock()
	q.timer = nil
	if q.running {
		q.mu.Unlock()
		return // the running loop picks the batch up
	}
	q.running = true
	q.wg.Add(1)
	defer q.wg.Done()
	for {
		st, urls, ok := q.take()
		if !ok {
			q.running = false
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), gocdnPurgeTimeout)
		q.exec(ctx, s, st, urls)
		cancel()
		q.mu.Lock()
	}
}

func (q *cdnPurgeQueue) exec(ctx context.Context, s *Server, st gocdnSettings, urls []string) {
	if q.run != nil {
		q.run(ctx, st, urls)
		return
	}
	s.gocdnCDNPurgeLogged(ctx, st, urls)
}

// flush runs a purge still waiting for its timer and waits for the one in flight (shutdown).
func (q *cdnPurgeQueue) flush(ctx context.Context, s *Server) {
	q.mu.Lock()
	if q.timer != nil && q.timer.Stop() {
		q.timer = nil
	}
	q.mu.Unlock()
	done := make(chan struct{})
	go func() {
		q.drain(s)  // runs the pending batch, or returns at once when a purge is in flight …
		q.wg.Wait() // … which then picks the batch up
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// gocdnPurgeResult is the outcome at one provider.
type gocdnPurgeResult struct {
	Provider, Name string
	Err            error
}

// gocdnCDNPurgeEach purges the listed URLs, or everything with none, at every configured provider
// (all at once: one slow or failing provider does not hold up the others) and reports each.
func (s *Server) gocdnCDNPurgeEach(ctx context.Context, st gocdnSettings, urls []string) []gocdnPurgeResult {
	targets := gocdnPurgeTargets(st)
	res := make([]gocdnPurgeResult, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		name := t.Provider
		if p := gocdnPurgerByKey(t.Provider); p != nil && p.Name != "" {
			name = p.Name
		}
		res[i] = gocdnPurgeResult{Provider: t.Provider, Name: name}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i].Err = s.gocdnPurgeOne(ctx, st, t, urls)
		}()
	}
	wg.Wait()
	return res
}

// gocdnCDNPurge purges the listed URLs, or everything with none, at all configured providers. The
// error names every provider that failed.
func (s *Server) gocdnCDNPurge(ctx context.Context, st gocdnSettings, urls []string) error {
	res := s.gocdnCDNPurgeEach(ctx, st, urls)
	if len(res) == 0 {
		return errors.New("no CDN is configured for purging")
	}
	return gocdnPurgeError(res)
}

// gocdnPurgeError is nil when every provider succeeded, else one message with a part per failure
// (the provider's name first unless the provider's own message starts with it).
func gocdnPurgeError(res []gocdnPurgeResult) error {
	var parts []string
	for _, r := range res {
		if r.Err == nil {
			continue
		}
		msg := r.Err.Error()
		if !strings.HasPrefix(msg, r.Name) {
			msg = r.Name + ": " + msg
		}
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
}

// gocdnPurgeOne purges at one provider.
func (s *Server) gocdnPurgeOne(ctx context.Context, st gocdnSettings, t gocdnPurgeConf, urls []string) error {
	p := gocdnPurgerByKey(t.Provider)
	if t.Provider == "" || p == nil {
		return errors.New("unknown CDN provider")
	}
	if t.Provider == "cloudflare" {
		st.CFZone, st.CFToken = t.Zone, t.Secret
		return s.gocdnCloudflarePurge(ctx, st, urls)
	}
	if !s.gocdnTargetReady(t) {
		return fmt.Errorf("%s is not configured: the %s and the %s are needed", p.Name, strings.ToLower(p.ZoneLabel), strings.ToLower(p.SecretName))
	}
	secret := s.gocdnReveal(t.Secret)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	switch t.Provider {
	case "cloudfront":
		return gocdnCloudFrontInvalidate(ctx, t.Zone, t.KeyID, secret, urls)
	case "bunny":
		return gocdnBunnyPurge(ctx, t.Zone, secret, urls)
	case "keycdn":
		return gocdnKeyCDNPurge(ctx, t.Zone, secret, urls)
	case "fastly":
		return gocdnFastlyPurge(ctx, t.Zone, secret, urls)
	}
	return errors.New("unknown CDN provider")
}

// gocdnCloudflarePurge purges the listed URLs (30 per request) or, with none, the whole zone.
func (s *Server) gocdnCloudflarePurge(ctx context.Context, st gocdnSettings, urls []string) error {
	token := s.gocdnReveal(st.CFToken)
	if !gocdnZoneID.MatchString(st.CFZone) || token == "" {
		return errors.New("Cloudflare is not configured: a zone ID (32 hex characters) and an API token are needed")
	}
	if len(urls) == 0 {
		return gocdnCFCall(ctx, st.CFZone, token, map[string]any{"purge_everything": true})
	}
	for len(urls) > 0 {
		n := min(30, len(urls))
		if err := gocdnCFCall(ctx, st.CFZone, token, map[string]any{"files": urls[:n]}); err != nil {
			return err
		}
		urls = urls[n:]
	}
	return nil
}

func gocdnCFCall(ctx context.Context, zone, token string, payload map[string]any) error {
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gocdnCFBase+"/zones/"+url.PathEscape(zone)+"/purge_cache", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := gocdnHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var res struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(data, &res)
	if resp.StatusCode/100 == 2 && res.Success {
		return nil
	}
	var msgs []string
	for _, e := range res.Errors {
		msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
	}
	if len(msgs) == 0 {
		msgs = append(msgs, resp.Status)
	}
	return errors.New("Cloudflare: " + strings.Join(msgs, "; "))
}

// gocdnAPIError reads a failed answer of a purge API into an error.
func gocdnAPIError(name string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := strings.TrimSpace(string(data))
	var j struct {
		Message     string `json:"message"`
		Msg         string `json:"msg"`
		Description string `json:"description"`
		Detail      string `json:"detail"`
		Error       string `json:"error"`
	}
	var x struct {
		Code    string `xml:"Error>Code"`
		Message string `xml:"Error>Message"`
	}
	switch {
	case json.Unmarshal(data, &j) == nil && (j.Message+j.Msg+j.Description+j.Detail+j.Error) != "":
		msg = strings.TrimSpace(strings.Join([]string{j.Error, j.Message, j.Msg, j.Description, j.Detail}, " "))
	case xml.Unmarshal(data, &x) == nil && x.Code != "":
		msg = x.Code + ": " + x.Message
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("%s: %d %s", name, resp.StatusCode, msg)
}

func gocdnDo(req *http.Request, name string) error {
	resp, err := gocdnHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return gocdnAPIError(name, resp)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

// gocdnURLPaths are the paths (with query) of URLs, for CloudFront.
func gocdnURLPaths(urls []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		p := u.EscapedPath()
		if p == "" {
			p = "/"
		}
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// gocdnCloudFrontInvalidate creates an invalidation (CreateInvalidation, API 2020-05-31) signed
// with SigV4 (service cloudfront, region us-east-1). Everything is "/*"; at most 3000 paths per
// invalidation.
func gocdnCloudFrontInvalidate(ctx context.Context, distribution, keyID, secret string, urls []string) error {
	paths := []string{"/*"}
	if len(urls) > 0 {
		paths = gocdnURLPaths(urls)
	}
	signer := gocdn.Signer{AccessKey: keyID, SecretKey: secret, Region: "us-east-1", Service: "cloudfront"}
	for len(paths) > 0 {
		n := min(3000, len(paths))
		batch := paths[:n]
		paths = paths[n:]
		ref := make([]byte, 8)
		_, _ = rand.Read(ref)
		var b bytes.Buffer
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><InvalidationBatch xmlns="http://cloudfront.amazonaws.com/doc/2020-05-31/"><Paths><Quantity>`)
		b.WriteString(fmt.Sprint(len(batch)))
		b.WriteString(`</Quantity><Items>`)
		for _, p := range batch {
			b.WriteString("<Path>")
			_ = xml.EscapeText(&b, []byte(p))
			b.WriteString("</Path>")
		}
		b.WriteString(`</Items></Paths><CallerReference>gocdn-` + time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(ref) + `</CallerReference></InvalidationBatch>`)
		body := b.Bytes()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, gocdnCloudFrontBase+"/2020-05-31/distribution/"+url.PathEscape(distribution)+"/invalidation", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/xml")
		signer.Sign(req, gocdn.SHA256Hex(body), time.Now())
		if err := gocdnDo(req, "CloudFront"); err != nil {
			return err
		}
	}
	return nil
}

// gocdnBunnyPurge purges a pull zone (everything) or URLs one by one (POST /purge?url=).
func gocdnBunnyPurge(ctx context.Context, zone, key string, urls []string) error {
	send := func(method, u string) error {
		req, err := http.NewRequestWithContext(ctx, method, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("AccessKey", key)
		req.Header.Set("Accept", "application/json")
		return gocdnDo(req, "BunnyCDN")
	}
	if len(urls) == 0 {
		return send(http.MethodPost, gocdnBunnyBase+"/pullzone/"+url.PathEscape(zone)+"/purgeCache")
	}
	for _, u := range urls {
		if err := send(http.MethodPost, gocdnBunnyBase+"/purge?"+url.Values{"url": {u}, "async": {"false"}}.Encode()); err != nil {
			return err
		}
	}
	return nil
}

// gocdnKeyCDNCall sends a KeyCDN API request (HTTP basic auth with the API key as the user); KeyCDN
// answers {"status":"success"} or {"status":"error","description":…}.
func gocdnKeyCDNCall(ctx context.Context, method, u, key string, payload any) error {
	var rd io.Reader
	if payload != nil {
		b, _ := json.Marshal(payload)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(key, "")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := gocdnHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("KeyCDN: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return gocdnAPIError("KeyCDN", resp)
	}
	var res struct {
		Status      string `json:"status"`
		Description string `json:"description"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if json.Unmarshal(data, &res) == nil && res.Status != "" && res.Status != "success" {
		return errors.New("KeyCDN: " + res.Description)
	}
	return nil
}

// gocdnKeyCDNPurge purges a zone, or URLs (sent without the scheme, 200 per request).
func gocdnKeyCDNPurge(ctx context.Context, zone, key string, urls []string) error {
	if len(urls) == 0 {
		return gocdnKeyCDNCall(ctx, http.MethodDelete, gocdnKeyCDNBase+"/zones/purge/"+url.PathEscape(zone)+".json", key, nil)
	}
	var list []string
	for _, u := range urls {
		list = append(list, strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://"))
	}
	for len(list) > 0 {
		n := min(200, len(list))
		if err := gocdnKeyCDNCall(ctx, http.MethodDelete, gocdnKeyCDNBase+"/zones/purgeurl/"+url.PathEscape(zone)+".json", key, map[string]any{"urls": list[:n]}); err != nil {
			return err
		}
		list = list[n:]
	}
	return nil
}

// gocdnFastlyPurge purges a service (purge_all) or single URLs (POST /purge/<host/path>).
func gocdnFastlyPurge(ctx context.Context, service, token string, urls []string) error {
	send := func(u string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Fastly-Key", token)
		req.Header.Set("Accept", "application/json")
		return gocdnDo(req, "Fastly")
	}
	if len(urls) == 0 {
		return send(gocdnFastlyBase + "/service/" + url.PathEscape(service) + "/purge_all")
	}
	for _, u := range urls {
		hostPath := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
		if err := send(gocdnFastlyBase + "/purge/" + hostPath); err != nil {
			return err
		}
	}
	return nil
}
