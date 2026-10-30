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
	"time"

	"wpgo/internal/gocdn"
)

// Purging. Every change that moves URLs (settings, a finished bulk job, the Purge screen) empties
// WPGo Cache through purgeAll. One CDN can be configured on the Purge screen: Cloudflare (zone ID
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

// gocdnPurgeReady reports whether a CDN purge provider is fully configured.
func (s *Server) gocdnPurgeReady(st gocdnSettings) bool {
	p := gocdnPurgerByKey(st.PurgeProvider)
	switch {
	case st.PurgeProvider == "" || p == nil:
		return false
	case st.PurgeProvider == "cloudflare":
		return gocdnZoneID.MatchString(st.CFZone) && s.gocdnReveal(st.CFToken) != ""
	}
	return p.Zone.MatchString(st.PurgeZone) && s.gocdnReveal(st.PurgeSecret) != "" && (p.KeyIDLabel == "" || st.PurgeKeyID != "")
}

// gocdnPurge empties the page cache; withCDN also purges the CDN (in the background) when
// automatic purging is on.
func (s *Server) gocdnPurge(ctx context.Context, withCDN bool) {
	s.purgeAll(ctx)
	if !withCDN {
		return
	}
	st := s.gocdnLoad()
	if st.PurgeProvider != "" && st.CFAuto && s.gocdnOn() {
		go func() {
			if err := s.gocdnCDNPurge(context.Background(), st, nil); err != nil {
				slog.Warn("gocdn: CDN purge", "provider", st.PurgeProvider, "err", err)
			}
		}()
	}
}

// gocdnCDNPurge purges the listed URLs, or everything with none, at the configured provider.
func (s *Server) gocdnCDNPurge(ctx context.Context, st gocdnSettings, urls []string) error {
	p := gocdnPurgerByKey(st.PurgeProvider)
	if st.PurgeProvider == "" || p == nil {
		return errors.New("no CDN is configured for purging")
	}
	if st.PurgeProvider == "cloudflare" {
		return s.gocdnCloudflarePurge(ctx, st, urls)
	}
	if !s.gocdnPurgeReady(st) {
		return fmt.Errorf("%s is not configured: the %s and the %s are needed", p.Name, strings.ToLower(p.ZoneLabel), strings.ToLower(p.SecretName))
	}
	secret := s.gocdnReveal(st.PurgeSecret)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	switch st.PurgeProvider {
	case "cloudfront":
		return gocdnCloudFrontInvalidate(ctx, st.PurgeZone, st.PurgeKeyID, secret, urls)
	case "bunny":
		return gocdnBunnyPurge(ctx, st.PurgeZone, secret, urls)
	case "keycdn":
		return gocdnKeyCDNPurge(ctx, st.PurgeZone, secret, urls)
	case "fastly":
		return gocdnFastlyPurge(ctx, st.PurgeZone, secret, urls)
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
