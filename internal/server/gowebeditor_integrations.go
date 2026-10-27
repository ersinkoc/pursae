package server

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/builder"
)

// Golementor Pro form integrations (Actions After Submit): ports of upstream Pro 4.3.0's
// Mailchimp, Drip, ActiveCampaign, GetResponse, ConvertKit, MailerLite, Slack and Discord actions
// and their handlers (modules/forms/actions, modules/forms/classes/*-handler.php), the editor's
// forms_panel_action_data requests that fill the list / group / field-mapping controls, and the
// global API keys under Golementor › Settings › Integrations.
//
// Outbound requests only go to each service's official API host over HTTPS (gweOutboundDo checks
// the host before connecting), never follow redirects and time out. Tests swap the transport
// (gweOutboundTransport) for httptest fakes.

// gweOutboundTransport, when set, carries every request to the integrations, Stripe and Google
// Fonts (tests point it at httptest servers). nil = http.DefaultTransport.
var gweOutboundTransport http.RoundTripper

// gweOutboundTimeout bounds one request to a third-party API.
const gweOutboundTimeout = 15 * time.Second

var (
	gweMailchimpHostRe      = regexp.MustCompile(`^us[0-9]{1,3}\.api\.mailchimp\.com$`)
	gweActiveCampaignHostRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.(api-us[0-9]{1,2}\.com|activehosted\.com)$`)
)

// gweHosts allows exactly the given host names.
func gweHosts(hosts ...string) func(string) bool {
	return func(h string) bool {
		for _, x := range hosts {
			if h == x {
				return true
			}
		}
		return false
	}
}

// gweHTTPResult is a third-party API response.
type gweHTTPResult struct {
	Code int
	Body []byte
}

// gweCheckOutboundURL refuses anything but https:// URLs on an allowed host (default port).
func gweCheckOutboundURL(raw string, allowed func(string) bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("Invalid API URL.")
	}
	if !allowed(strings.ToLower(u.Hostname())) {
		return nil, errors.New("API host not allowed.")
	}
	return u, nil
}

// gweOutboundDo sends one request to an allowed host; the response body is capped at 8 MB.
func gweOutboundDo(ctx context.Context, method, raw string, header http.Header, body []byte, allowed func(string) bool, timeout time.Duration) (*gweHTTPResult, error) {
	u, err := gweCheckOutboundURL(raw, allowed)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = gweOutboundTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Golementor Forms")
	}
	rt := gweOutboundTransport
	if rt == nil {
		rt = http.DefaultTransport
	}
	client := &http.Client{Transport: rt, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return &gweHTTPResult{Code: resp.StatusCode, Body: b}, nil
}

// --- Rest_Client ------------------------------------------------------------------------------------

// gweRest mirrors upstream Pro's Rest_Client: JSON responses, one valid status code.
type gweRest struct {
	ctx     context.Context
	base    string
	header  http.Header
	allowed func(string) bool
	cache   []int // status codes of failed requests (GetResponse inspects them)
}

func newGWERest(ctx context.Context, base string, allowed func(string) bool) *gweRest {
	return &gweRest{ctx: ctx, base: base, header: http.Header{}, allowed: allowed}
}

// request returns the decoded body (a JSON object or array) or Rest_Client's errors.
func (r *gweRest) request(method, endpoint string, query url.Values, body []byte, valid int) (any, []byte, error) {
	target := r.base + endpoint
	if len(query) > 0 {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + query.Encode()
	}
	res, err := gweOutboundDo(r.ctx, method, target, r.header, body, r.allowed, 0)
	code := 0
	if res != nil {
		code = res.Code
	}
	if err != nil || code != valid {
		r.cache = append(r.cache, code)
		return nil, nil, fmt.Errorf("Rest Client Error: response code %d.", code)
	}
	v, ok := gweDecodeJSON(res.Body)
	if !ok {
		return nil, nil, errors.New("Rest Client Error: unexpected response type.")
	}
	return v, res.Body, nil
}

func (r *gweRest) get(endpoint string, query url.Values) (any, []byte, error) {
	return r.request(http.MethodGet, endpoint, query, nil, http.StatusOK)
}

func (r *gweRest) postJSON(endpoint string, data any, valid int) (any, []byte, error) {
	b, _ := json.Marshal(data)
	return r.request(http.MethodPost, endpoint, nil, b, valid)
}

// gweDecodeJSON decodes an object or array (numbers kept as json.Number).
func gweDecodeJSON(b []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil, false
	}
	switch v.(type) {
	case map[string]any, []any:
		return v, true
	}
	return nil, false
}

func gweMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// --- form record ------------------------------------------------------------------------------------

// gweFormRecord is what the integrations read of a submission (Form_Record).
type gweFormRecord struct {
	settings map[string]any
	fields   []*gweFormField
	byID     map[string]*gweFormField
	clientIP string
	referrer string
	siteURL  string
}

func (s *Server) gweNewFormRecord(c *gin.Context, settings map[string]any, fields []*gweFormField) *gweFormRecord {
	r := &gweFormRecord{settings: settings, fields: fields, byID: map[string]*gweFormField{}, clientIP: c.ClientIP(),
		referrer: c.PostForm("referrer"), siteURL: s.links(c).Home}
	for _, f := range fields {
		r.byID[f.ID] = f
	}
	return r
}

func (r *gweFormRecord) setting(k string) string { return gweStr(r.settings[k]) }

// fieldsMap is a fields_map control's value: {remote_id, local_id} pairs.
func (r *gweFormRecord) fieldsMap(key string) [][2]string {
	var out [][2]string
	for _, it := range asAnyList(r.settings[key]) {
		m := gweMap(it)
		if m == nil {
			continue
		}
		out = append(out, [2]string{gweStr(m["remote_id"]), gweStr(m["local_id"])})
	}
	return out
}

// value is a field's value ("" for unknown fields).
func (r *gweFormRecord) value(id string) string {
	if f := r.byID[id]; f != nil {
		return f.Value
	}
	return ""
}

// --- registry ---------------------------------------------------------------------------------------

// gweIntegration is one marketing / notification action.
type gweIntegration struct {
	name, label string
	keyLabel    string // the global key's name in the editor alert ("" = no global key)
	keySource   string // control holding default/custom
	keyOption   string // settings option of the global key
	run         func(s *Server, ctx context.Context, rec *gweFormRecord) error
	panel       func(s *Server, ctx context.Context, data map[string]any) (any, error)
}

var gweIntegrations = []*gweIntegration{
	{name: "mailchimp", label: "MailChimp", keyLabel: "MailChimp API Key", keySource: "mailchimp_api_key_source", keyOption: "gwe_mailchimp_api_key",
		run: (*Server).gweMailchimpRun, panel: (*Server).gweMailchimpPanel},
	{name: "drip", label: "Drip", keyLabel: "Drip API Token", keySource: "drip_api_token_source", keyOption: "gwe_drip_api_token",
		run: (*Server).gweDripRun, panel: (*Server).gweDripPanel},
	{name: "activecampaign", label: "ActiveCampaign", keyLabel: "ActiveCampaign API credentials", keySource: "activecampaign_api_credentials_source",
		keyOption: "gwe_activecampaign_api_key", run: (*Server).gweActiveCampaignRun, panel: (*Server).gweActiveCampaignPanel},
	{name: "getresponse", label: "GetResponse", keyLabel: "GetResponse API key", keySource: "getresponse_api_key_source", keyOption: "gwe_getresponse_api_key",
		run: (*Server).gweGetResponseRun, panel: (*Server).gweGetResponsePanel},
	{name: "convertkit", label: "ConvertKit", keyLabel: "ConvertKit API key", keySource: "convertkit_api_key_source", keyOption: "gwe_convertkit_api_key",
		run: (*Server).gweConvertKitRun, panel: (*Server).gweConvertKitPanel},
	{name: "mailerlite", label: "MailerLite", keyLabel: "MailerLite API Key", keySource: "mailerlite_api_key_source", keyOption: "gwe_mailerlite_api_key",
		run: (*Server).gweMailerLiteRun, panel: (*Server).gweMailerLitePanel},
	{name: "slack", label: "Slack", run: (*Server).gweSlackRun},
	{name: "discord", label: "Discord", run: (*Server).gweDiscordRun},
}

func gweIntegrationByName(name string) *gweIntegration {
	for _, in := range gweIntegrations {
		if in.name == name {
			return in
		}
	}
	return nil
}

// gweIntegrationsAction handles the editor's ajax actions for integrations and payments
// (pro_forms_panel_action_data, pro_get_stripe_tax_rates); handled is false for other actions.
func (s *Server) gweIntegrationsAction(c *gin.Context, action string, data map[string]any, editorID int64) (any, bool, error) {
	switch action {
	case "pro_forms_panel_action_data":
		if _, err := s.gwePostFor(c, gweInt(data["editor_post_id"]), "edit_post"); err != nil {
			if _, err2 := s.gwePostFor(c, editorID, "edit_post"); err2 != nil {
				return nil, true, err
			}
		}
		service := gweStr(data["service"])
		if service == "" {
			return nil, true, gweFail(http.StatusBadRequest, "Service required.")
		}
		in := gweIntegrationByName(service)
		if in == nil || in.panel == nil {
			return nil, true, gweFail(http.StatusBadRequest, "Action not found.")
		}
		out, err := in.panel(s, c.Request.Context(), data)
		if err != nil {
			return nil, true, gweFail(http.StatusBadRequest, err.Error())
		}
		return out, true, nil
	case "pro_get_stripe_tax_rates":
		if _, err := s.gwePostFor(c, editorID, "edit_post"); err != nil {
			return nil, true, err
		}
		return s.gweStripeTaxRates(c.Request.Context()), true, nil
	}
	return nil, false, nil
}

// gweGlobalOrCustom resolves an integration's key from the panel request: the global key when the
// source control says "default", else the custom key.
func (s *Server) gweGlobalOrCustom(source, custom, option string) string {
	if source == "default" {
		return strings.TrimSpace(s.settings.String(option))
	}
	return strings.TrimSpace(custom)
}

func gweSelectOptions() builder.Ordered { return builder.Ordered{{K: "", V: "Select..."}} }

// gweRemoteField is one entry of a fields_map control's remote fields.
func gweRemoteField(label, typ any, id string, required bool) map[string]any {
	return map[string]any{"remote_label": label, "remote_type": typ, "remote_id": id, "remote_required": required}
}

// --- Mailchimp --------------------------------------------------------------------------------------

type gweMailchimp struct {
	ctx  context.Context
	key  string
	base string
}

func newGWEMailchimp(ctx context.Context, key string) (*gweMailchimp, error) {
	parts := strings.Split(key, "-")
	if key == "" || len(parts) < 2 || !gweMailchimpHostRe.MatchString(parts[1]+".api.mailchimp.com") {
		return nil, errors.New("Invalid API key.")
	}
	return &gweMailchimp{ctx: ctx, key: key, base: "https://" + parts[1] + ".api.mailchimp.com/3.0/"}, nil
}

func (m *gweMailchimp) header() http.Header {
	return http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("user:"+m.key))}}
}

func (m *gweMailchimp) query(endpoint string) (map[string]any, error) {
	res, err := gweOutboundDo(m.ctx, http.MethodGet, m.base+endpoint, m.header(), nil, gweMailchimpHostRe.MatchString, 0)
	if err != nil || res.Code != http.StatusOK {
		return nil, errors.New("Mailchimp error.")
	}
	v, ok := gweDecodeJSON(res.Body)
	if !ok {
		return nil, errors.New("Mailchimp error.")
	}
	out := gweMap(v)
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func (m *gweMailchimp) send(method, endpoint string, data any) (int, map[string]any, error) {
	b, _ := json.Marshal(data)
	h := m.header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	res, err := gweOutboundDo(m.ctx, method, m.base+endpoint, h, b, gweMailchimpHostRe.MatchString, 0)
	if err != nil {
		return 0, nil, errors.New("Mailchimp error.")
	}
	v, ok := gweDecodeJSON(res.Body)
	if res.Code != http.StatusNoContent && !ok {
		return 0, nil, errors.New("Mailchimp error.")
	}
	return res.Code, gweMap(v), nil
}

func (m *gweMailchimp) lists() (map[string]any, error) {
	r, err := m.query("lists?count=999")
	if err != nil {
		return nil, err
	}
	lists := gweSelectOptions()
	for _, l := range asAnyList(r["lists"]) {
		lm := gweMap(l)
		lists = append(lists, builder.KV{K: gweStr(lm["id"]), V: lm["name"]})
	}
	return map[string]any{"lists": lists}, nil
}

func (m *gweMailchimp) groups(list string) (map[string]any, error) {
	r, err := m.query("lists/" + url.PathEscape(list) + "/interest-categories?count=999")
	if err != nil {
		return nil, err
	}
	groups := builder.Ordered{}
	for _, cat := range asAnyList(r["categories"]) {
		cm := gweMap(cat)
		ir, err := m.query("lists/" + url.PathEscape(list) + "/interest-categories/" + url.PathEscape(gweStr(cm["id"])) + "/interests?count=999")
		if err != nil {
			return nil, err
		}
		for _, in := range asAnyList(ir["interests"]) {
			im := gweMap(in)
			groups = append(groups, builder.KV{K: gweStr(im["id"]), V: gweStr(cm["title"]) + " - " + gweStr(im["name"])})
		}
	}
	return map[string]any{"groups": groups}, nil
}

var gweMailchimpTypes = map[string]string{"text": "text", "number": "number", "address": "text", "phone": "text", "date": "text", "url": "url",
	"imageurl": "url", "radio": "radio", "dropdown": "select", "birthday": "text", "zip": "text"}

func gweNormalizeType(types map[string]string, t string) any {
	if v, ok := types[t]; ok {
		return v
	}
	return nil
}

func (m *gweMailchimp) fields(list string) (map[string]any, error) {
	r, err := m.query("lists/" + url.PathEscape(list) + "/merge-fields?count=999")
	if err != nil {
		return nil, err
	}
	fields := []any{gweRemoteField("Email", "email", "email", true)}
	for _, f := range asAnyList(r["merge_fields"]) {
		fm := gweMap(f)
		fields = append(fields, gweRemoteField(fm["name"], gweNormalizeType(gweMailchimpTypes, gweStr(fm["type"])), gweStr(fm["tag"]), truthyAny(fm["required"])))
	}
	return map[string]any{"fields": fields}, nil
}

func (s *Server) gweMailchimpPanel(ctx context.Context, data map[string]any) (any, error) {
	key := s.gweGlobalOrCustom(gweStr(data["use_global_api_key"]), gweStr(data["api_key"]), "gwe_mailchimp_api_key")
	if key == "" {
		return nil, errors.New("`api_key` is required.")
	}
	m, err := newGWEMailchimp(ctx, key)
	if err != nil {
		return nil, err
	}
	list := gweStr(data["mailchimp_list"])
	switch gweStr(data["mailchimp_action"]) {
	case "lists":
		return m.lists()
	case "fields":
		return m.fields(list)
	case "groups":
		return m.groups(list)
	}
	g, err := m.groups(list)
	if err != nil {
		return nil, err
	}
	f, err := m.fields(list)
	if err != nil {
		return nil, err
	}
	return map[string]any{"list_details": map[string]any{"groups": g["groups"], "fields": f["fields"]}}, nil
}

func gweEmailHash(email string) string {
	h := md5.Sum([]byte(strings.ToLower(email)))
	return hex.EncodeToString(h[:])
}

func gweSplitTags(tags string) []string {
	var out []string
	for _, t := range strings.Split(strings.TrimSpace(tags), ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (s *Server) gweMailchimpRun(ctx context.Context, rec *gweFormRecord) error {
	key := s.gweGlobalOrCustom(rec.setting("mailchimp_api_key_source"), rec.setting("mailchimp_api_key"), "gwe_mailchimp_api_key")
	m, err := newGWEMailchimp(ctx, key)
	if err != nil {
		return err
	}
	// map_fields
	sub := map[string]any{}
	merge := map[string]any{}
	for _, mp := range rec.fieldsMap("mailchimp_fields_map") {
		v := rec.value(mp[1])
		if v == "" {
			continue
		}
		if mp[0] == "email" {
			sub["email_address"] = v
		} else {
			merge[mp[0]] = v
		}
	}
	if len(merge) > 0 {
		sub["merge_fields"] = merge
	}
	email := gweStr(sub["email_address"])
	if email == "" {
		return errors.New("Integration requires an email field.")
	}
	if groups := asAnyList(rec.settings["mailchimp_groups"]); len(groups) > 0 {
		interests := map[string]bool{}
		for _, g := range groups {
			interests[gweStr(g)] = true
		}
		sub["interests"] = interests
	}
	tags := gweSplitTags(rec.setting("mailchimp_tags"))
	if rec.setting("mailchimp_tags") != "" {
		raw := []string{}
		for _, t := range strings.Split(strings.TrimSpace(rec.setting("mailchimp_tags")), ",") {
			raw = append(raw, t)
		}
		sub["tags"] = raw
	}
	list := rec.setting("mailchimp_list")
	hash := gweEmailHash(email)
	double := rec.setting("mailchimp_double_opt_in") == "yes"
	if double {
		sub["status_if_new"] = "pending"
		if cur, err := m.query("lists/" + url.PathEscape(list) + "/members/" + hash); err == nil && gweStr(cur["status"]) != "subscribed" {
			sub["status"] = "pending"
		}
	} else {
		sub["status_if_new"] = "subscribed"
		sub["status"] = "subscribed"
	}
	code, body, err := m.send(http.MethodPut, "lists/"+url.PathEscape(list)+"/members/"+hash, sub)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("HTTP %d - %s", code, gweStr(body["detail"]))
	}
	// Set the subscriber's tags only if he doesn't have exactly them already.
	var current []string
	for _, t := range asAnyList(body["tags"]) {
		current = append(current, gweStr(gweMap(t)["name"]))
	}
	if gweSameTags(tags, current) {
		return nil
	}
	req := []map[string]string{}
	for _, t := range current {
		req = append(req, map[string]string{"name": t, "status": "active"})
	}
	for _, t := range tags {
		req = append(req, map[string]string{"name": t, "status": "active"})
	}
	listID := gweStr(body["list_id"])
	if listID == "" {
		listID = list
	}
	if e := gweStr(body["email_address"]); e != "" {
		email = e
	}
	code, body, err = m.send(http.MethodPost, "lists/"+url.PathEscape(listID)+"/members/"+gweEmailHash(email)+"/tags", map[string]any{"tags": req})
	if err != nil {
		return err
	}
	if code != http.StatusNoContent {
		return fmt.Errorf("HTTP %d - %s", code, gweStr(body["detail"]))
	}
	return nil
}

// gweSameTags mirrors subscriber_has_tags: the same set of tags.
func gweSameTags(a, b []string) bool {
	set := func(l []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range l {
			m[x] = true
		}
		return m
	}
	sa, sb := set(a), set(b)
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	for k := range sb {
		if !sa[k] {
			return false
		}
	}
	return true
}

// --- Drip -------------------------------------------------------------------------------------------

func newGWEDrip(ctx context.Context, token string) (*gweRest, builder.Ordered, error) {
	if token == "" {
		return nil, nil, errors.New("Invalid API key.")
	}
	r := newGWERest(ctx, "https://api.getdrip.com/v2/", gweHosts("api.getdrip.com"))
	r.header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(token)))
	r.header.Set("Content-Type", "application/vnd.api+json")
	v, _, err := r.get("accounts", nil)
	if err != nil {
		return nil, nil, err
	}
	accounts := gweSelectOptions()
	for _, a := range asAnyList(gweMap(v)["accounts"]) {
		am := gweMap(a)
		accounts = append(accounts, builder.KV{K: gweStr(am["id"]), V: am["name"]})
	}
	if len(accounts) == 0 {
		return nil, nil, errors.New("Invalid API key.")
	}
	return r, accounts, nil
}

func (s *Server) gweDripPanel(ctx context.Context, data map[string]any) (any, error) {
	key := s.gweGlobalOrCustom(gweStr(data["api_token"]), gweStr(data["custom_api_token"]), "gwe_drip_api_token")
	if key == "" {
		return nil, errors.New("`api_token` is required.")
	}
	_, accounts, err := newGWEDrip(ctx, key)
	if err != nil {
		return nil, err
	}
	return map[string]any{"accounts": accounts}, nil
}

func (s *Server) gweDripRun(ctx context.Context, rec *gweFormRecord) error {
	email, emailLocal := "", ""
	for _, mp := range rec.fieldsMap("drip_fields_map") {
		if mp[0] == "email" {
			emailLocal = mp[1]
			if v := rec.value(mp[1]); v != "" && email == "" {
				email = v
			}
		}
	}
	if email == "" {
		return errors.New("Integration requires an email field.")
	}
	sub := map[string]any{"ip_address": rec.clientIP, "email": email}
	if tags := rec.setting("tags"); tags != "" {
		sub["tags"] = strings.Split(gweProReplaceShortcodes(tags, rec.fields, false), ",")
	}
	custom := map[string]any{}
	if rec.setting("drip_custom_fields") == "yes" {
		for _, f := range rec.fields {
			if f.ID != emailLocal {
				custom[f.ID] = f.Value
			}
		}
	}
	sub["custom_fields"] = custom
	key := s.gweGlobalOrCustom(rec.setting("drip_api_token_source"), rec.setting("drip_custom_api_token"), "gwe_drip_api_token")
	r, _, err := newGWEDrip(ctx, key)
	if err != nil {
		return err
	}
	_, _, err = r.postJSON(url.PathEscape(rec.setting("drip_account"))+"/subscribers/", map[string]any{"subscribers": []any{sub}}, http.StatusOK)
	return err
}

// --- ActiveCampaign ---------------------------------------------------------------------------------

var gweActiveCampaignTypes = gweMailchimpTypes

type gweActiveCampaign struct {
	rest *gweRest
	key  string
}

func newGWEActiveCampaign(ctx context.Context, key, base string) (*gweActiveCampaign, map[string]any, error) {
	if key == "" {
		return nil, nil, errors.New("Invalid API key.")
	}
	if base == "" {
		return nil, nil, errors.New("Invalid API URL.")
	}
	if _, err := gweCheckOutboundURL(strings.TrimRight(base, "/")+"/admin/api.php", gweActiveCampaignHostRe.MatchString); err != nil {
		return nil, nil, errors.New("Invalid API URL.")
	}
	ac := &gweActiveCampaign{rest: newGWERest(ctx, strings.TrimRight(base, "/")+"/admin/api.php", gweActiveCampaignHostRe.MatchString), key: key}
	lists, err := ac.lists()
	if err != nil {
		return nil, nil, err
	}
	if len(lists["lists"].(builder.Ordered)) == 0 {
		return nil, nil, errors.New("Invalid API key or URL.")
	}
	return ac, lists, nil
}

// gweACItems iterates the array-valued entries of an ActiveCampaign v1 response in order.
func gweACItems(raw []byte) []map[string]any {
	obj, err := gweOrderedRaw(raw)
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, kv := range obj {
		v, ok := gweDecodeJSON(kv.V)
		if m := gweMap(v); ok && m != nil {
			out = append(out, m)
		}
	}
	return out
}

func (a *gweActiveCampaign) lists() (map[string]any, error) {
	_, raw, err := a.rest.get("?api_action=list_list", url.Values{"api_key": {a.key}, "ids": {"all"}, "api_output": {"json"}})
	if err != nil {
		return nil, err
	}
	lists := builder.Ordered{{K: "", V: "Select..."}}
	for _, l := range gweACItems(raw) {
		lists = append(lists, builder.KV{K: gweStr(l["id"]), V: l["name"]})
	}
	_, fraw, err := a.rest.get("?api_action=list_field_view", url.Values{"api_key": {a.key}, "ids": {"all"}, "api_output": {"json"}})
	if err != nil {
		return nil, err
	}
	fields := []any{}
	for _, f := range gweACItems(fraw) {
		fields = append(fields, gweRemoteField(f["title"], gweNormalizeType(gweActiveCampaignTypes, gweStr(f["type"])), "field["+gweStr(f["id"])+",0]", truthyAny(f["isrequired"])))
	}
	return map[string]any{"lists": lists, "fields": fields}, nil
}

func (s *Server) gweActiveCampaignCreds(source, key, apiURL string) (string, string) {
	if source == "default" {
		return strings.TrimSpace(s.settings.String("gwe_activecampaign_api_key")), strings.TrimSpace(s.settings.String("gwe_activecampaign_api_url"))
	}
	return strings.TrimSpace(key), strings.TrimSpace(apiURL)
}

func (s *Server) gweActiveCampaignPanel(ctx context.Context, data map[string]any) (any, error) {
	key, apiURL := s.gweActiveCampaignCreds(gweStr(data["api_cred"]), gweStr(data["api_key"]), gweStr(data["api_url"]))
	if key == "" {
		return nil, errors.New("`api_key` is required.")
	}
	if apiURL == "" {
		return nil, errors.New("`api_url` is required.")
	}
	_, lists, err := newGWEActiveCampaign(ctx, key, apiURL)
	return lists, err
}

func (s *Server) gweActiveCampaignRun(ctx context.Context, rec *gweFormRecord) error {
	sub := url.Values{}
	for _, mp := range rec.fieldsMap("activecampaign_fields_map") {
		if v := rec.value(mp[1]); v != "" {
			sub.Set(mp[0], v)
		}
	}
	list := rec.setting("activecampaign_list")
	if sub.Get("email") == "" || list == "" {
		return errors.New("Integration requires an email field and a selected list.")
	}
	sub.Set("ip4", rec.clientIP)
	sub.Set("p["+list+"]", list)
	if t := rec.setting("activecampaign_tags"); t != "" {
		sub.Set("tags", t)
	}
	if f := rec.setting("form_id"); f != "" {
		sub.Set("form", f)
	}
	key, apiURL := s.gweActiveCampaignCreds(rec.setting("activecampaign_api_credentials_source"), rec.setting("activecampaign_api_key"), rec.setting("activecampaign_api_url"))
	ac, _, err := newGWEActiveCampaign(ctx, key, apiURL)
	if err != nil {
		return err
	}
	ac.rest.header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, _, err = ac.rest.request(http.MethodPost, "?api_action=contact_sync&api_key="+url.QueryEscape(key)+"&api_output=json", nil, []byte(sub.Encode()), http.StatusOK)
	return err
}

// --- GetResponse ------------------------------------------------------------------------------------

var gweGetResponseTypes = map[string]string{"text": "text", "number": "number", "address": "text", "phone": "text", "date": "text", "url": "url",
	"imageurl": "url", "radio": "radio", "dropdown": "select", "single_select": "select", "textarea": "text", "birthday": "text", "zip": "text",
	"country": "text", "gender": "text"}

func newGWEGetResponse(ctx context.Context, key string) (*gweRest, map[string]any, error) {
	if key == "" {
		return nil, nil, errors.New("Invalid API key.")
	}
	r := newGWERest(ctx, "https://api.getresponse.com/v3/", gweHosts("api.getresponse.com"))
	r.header.Set("X-Auth-Token", "api-key "+key)
	r.header.Set("Content-Type", "application/json")
	v, _, err := r.get("campaigns", nil)
	if err != nil {
		return nil, nil, err
	}
	lists := gweSelectOptions()
	for _, l := range asAnyList(v) {
		lm := gweMap(l)
		if lm == nil {
			continue
		}
		lists = append(lists, builder.KV{K: gweStr(lm["campaignId"]), V: lm["name"]})
	}
	return r, map[string]any{"lists": lists}, nil
}

func (s *Server) gweGetResponsePanel(ctx context.Context, data map[string]any) (any, error) {
	key := s.gweGlobalOrCustom(gweStr(data["api_key"]), gweStr(data["custom_api_key"]), "gwe_getresponse_api_key")
	if key == "" {
		return nil, errors.New("`api_key` is required.")
	}
	r, lists, err := newGWEGetResponse(ctx, key)
	if err != nil {
		return nil, err
	}
	if gweStr(data["getresponse_action"]) == "lists" {
		return lists, nil
	}
	v, _, err := r.get("custom-fields", nil)
	if err != nil {
		return nil, err
	}
	fields := []any{gweRemoteField("Email", "email", "email", true), gweRemoteField("Name", "text", "name", false)}
	for _, f := range asAnyList(v) {
		fm := gweMap(f)
		fields = append(fields, gweRemoteField(fm["name"], gweNormalizeType(gweGetResponseTypes, gweStr(fm["type"])), gweStr(fm["customFieldId"]), false))
	}
	return map[string]any{"fields": fields}, nil
}

func (s *Server) gweGetResponseRun(ctx context.Context, rec *gweFormRecord) error {
	sub := map[string]any{}
	custom := []any{}
	for _, mp := range rec.fieldsMap("getresponse_fields_map") {
		v := rec.value(mp[1])
		if v == "" {
			continue
		}
		if mp[0] == "name" || mp[0] == "email" {
			sub[mp[0]] = v
			continue
		}
		custom = append(custom, map[string]any{"customFieldId": mp[0], "value": []string{v}})
	}
	sub["customFieldValues"] = custom
	if _, ok := sub["email"]; !ok {
		return errors.New("Integration requires an email field.")
	}
	if _, ok := rec.settings["getresponse_dayofcycle"]; ok {
		n, _ := strconv.Atoi(strings.TrimSpace(rec.setting("getresponse_dayofcycle")))
		sub["dayOfCycle"] = n
	}
	sub["ipAddress"] = rec.clientIP
	sub["campaign"] = map[string]any{"campaignId": rec.setting("getresponse_list")}
	key := s.gweGlobalOrCustom(rec.setting("getresponse_api_key_source"), rec.setting("getresponse_custom_api_key"), "gwe_getresponse_api_key")
	r, _, err := newGWEGetResponse(ctx, key)
	if err != nil {
		return err
	}
	if _, _, err := r.postJSON("contacts", sub, http.StatusAccepted); err != nil {
		// 409 (the contact exists already) counts as success upstream.
		for _, code := range r.cache {
			if code != 200 && code != 202 && code != 409 {
				return err
			}
		}
	}
	return nil
}

// --- ConvertKit -------------------------------------------------------------------------------------

func newGWEConvertKit(ctx context.Context, key string) (*gweRest, builder.Ordered, error) {
	if key == "" {
		return nil, nil, errors.New("Invalid API key.")
	}
	r := newGWERest(ctx, "https://api.convertkit.com/v3/", gweHosts("api.convertkit.com"))
	forms, err := gweConvertKitList(r, "forms", key)
	if err != nil {
		return nil, nil, err
	}
	return r, forms, nil
}

func gweConvertKitList(r *gweRest, kind, key string) (builder.Ordered, error) {
	v, _, err := r.get(kind+"/", url.Values{"api_key": {key}})
	if err != nil {
		return nil, err
	}
	out := gweSelectOptions()
	for _, it := range asAnyList(gweMap(v)[kind]) {
		m := gweMap(it)
		if m == nil {
			continue
		}
		out = append(out, builder.KV{K: gweStr(m["id"]), V: m["name"]})
	}
	return out, nil
}

func (s *Server) gweConvertKitPanel(ctx context.Context, data map[string]any) (any, error) {
	key := s.gweGlobalOrCustom(gweStr(data["api_key"]), gweStr(data["custom_api_key"]), "gwe_convertkit_api_key")
	if key == "" {
		return nil, errors.New("`api_key` is required.")
	}
	r, forms, err := newGWEConvertKit(ctx, key)
	if err != nil {
		return nil, err
	}
	tags, err := gweConvertKitList(r, "tags", key)
	if err != nil {
		return nil, err
	}
	return map[string]any{"data": map[string]any{"forms": forms, "tags": tags}}, nil
}

func (s *Server) gweConvertKitRun(ctx context.Context, rec *gweFormRecord) error {
	sub := map[string]any{}
	for _, mp := range rec.fieldsMap("convertkit_fields_map") {
		if v := rec.value(mp[1]); v != "" && (mp[0] == "first_name" || mp[0] == "email") {
			sub[mp[0]] = v
		}
	}
	if _, ok := sub["email"]; !ok {
		return errors.New("Integration requires an email field.")
	}
	sub["ipAddress"] = rec.clientIP
	key := s.gweGlobalOrCustom(rec.setting("convertkit_api_key_source"), rec.setting("convertkit_custom_api_key"), "gwe_convertkit_api_key")
	if tags, ok := rec.settings["convertkit_tags"]; ok && !(gweStr(tags) == "" && asAnyList(tags) == nil) {
		sub["tags"] = tags
	}
	r, _, err := newGWEConvertKit(ctx, key)
	if err != nil {
		return err
	}
	r.header.Set("Content-Type", "application/json")
	_, _, err = r.postJSON("forms/"+url.PathEscape(rec.setting("convertkit_form"))+"/subscribe?api_key="+url.QueryEscape(key), sub, http.StatusOK)
	return err
}

// --- MailerLite -------------------------------------------------------------------------------------

func newGWEMailerLite(ctx context.Context, key string) (*gweRest, error) {
	if key == "" {
		return nil, errors.New("Invalid API key.")
	}
	r := newGWERest(ctx, "https://api.mailerlite.com/api/v2/", gweHosts("api.mailerlite.com"))
	r.header.Set("X-MailerLite-ApiKey", key)
	r.header.Set("Content-Type", "application/json")
	if _, _, err := r.get("groups", nil); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *Server) gweMailerLitePanel(ctx context.Context, data map[string]any) (any, error) {
	key := s.gweGlobalOrCustom(gweStr(data["api_key"]), gweStr(data["custom_api_key"]), "gwe_mailerlite_api_key")
	if key == "" {
		return nil, errors.New("`api_key` is required.")
	}
	r, err := newGWEMailerLite(ctx, key)
	if err != nil {
		return nil, err
	}
	if gweStr(data["mailerlite_action"]) != "groups" {
		return nil, nil
	}
	v, _, err := r.get("groups", nil)
	if err != nil {
		return nil, err
	}
	groups := gweSelectOptions()
	for _, g := range asAnyList(v) {
		gm := gweMap(g)
		groups = append(groups, builder.KV{K: gweStr(gm["id"]), V: gm["name"]})
	}
	fields := []any{}
	if fv, _, err := r.get("fields", nil); err == nil {
		for _, f := range asAnyList(fv) {
			fm := gweMap(f)
			if fm == nil || gweStr(fm["date_updated"]) == "" {
				continue
			}
			fields = append(fields, gweRemoteField(fm["title"], strings.ToLower(gweStr(fm["type"])), gweStr(fm["key"]), false))
		}
	}
	return map[string]any{"groups": groups, "fields": fields}, nil
}

func (s *Server) gweMailerLiteRun(ctx context.Context, rec *gweFormRecord) error {
	mapped := func(id string) string {
		for _, mp := range rec.fieldsMap("mailerlite_fields_map") {
			if v := rec.value(mp[1]); v != "" && mp[0] == id {
				return v
			}
		}
		return ""
	}
	email := mapped("email")
	if email == "" {
		return errors.New("Integration requires an email field")
	}
	custom := map[string]any{}
	for _, mp := range rec.fieldsMap("mailerlite_fields_map") {
		if mp[0] == "email" || mp[0] == "name" || mp[1] == "" {
			continue
		}
		if f := rec.byID[mp[1]]; f != nil {
			custom[mp[0]] = f.Value
		}
	}
	sub := map[string]any{"email": email, "name": mapped("name"), "fields": custom}
	if rec.setting("allow_resubscribe") == "yes" {
		sub["resubscribe"] = true
	}
	key := s.gweGlobalOrCustom(rec.setting("mailerlite_api_key_source"), rec.setting("mailerlite_custom_api_key"), "gwe_mailerlite_api_key")
	r, err := newGWEMailerLite(ctx, key)
	if err != nil {
		return err
	}
	_, _, err = r.postJSON("groups/"+url.PathEscape(rec.setting("mailerlite_group"))+"/subscribers", sub, http.StatusOK)
	return err
}

// --- Slack and Discord ------------------------------------------------------------------------------

func (s *Server) gweSlackRun(ctx context.Context, rec *gweFormRecord) error {
	hook := rec.setting("slack_webhook")
	if hook == "" || !strings.HasPrefix(hook, "https://hooks.slack.com/services/") {
		return nil
	}
	data := map[string]any{"username": rec.setting("slack_username")}
	if ch := rec.setting("slack_channel"); ch != "" {
		data["channel"] = ch
	}
	link := rec.referrer
	if link == "" {
		link = rec.siteURL
	}
	color := "#D30C5C"
	if _, ok := rec.settings["slack_webhook_color"]; ok {
		color = rec.setting("slack_webhook_color")
	}
	att := map[string]any{"text": "A new Form Submission has been received", "title": "A new Submission", "color": color, "title_link": link}
	if v := rec.setting("slack_title"); v != "" {
		att["title"] = v
	}
	if v := rec.setting("slack_text"); v != "" {
		att["text"] = v
	}
	if v := rec.setting("slack_pretext"); v != "" {
		att["pretext"] = v
	}
	if rec.setting("slack_add_fields") == "yes" {
		fields := []any{}
		for _, f := range rec.fields {
			title := f.Title
			if title == "" {
				title = f.ID
			}
			fields = append(fields, map[string]any{"title": title, "value": f.Value, "short": false})
		}
		att["fields"] = fields
	}
	if rec.setting("slack_add_ts") == "yes" {
		att["footer"] = "Powered by Golementor"
		att["ts"] = time.Now().Unix()
	}
	data["attachments"] = []any{att}
	b, _ := json.Marshal(data)
	res, err := gweOutboundDo(ctx, http.MethodPost, hook, http.Header{"Content-Type": {"application/json"}}, b, gweHosts("hooks.slack.com"), 0)
	if err != nil || res.Code != http.StatusOK {
		return errors.New("Webhook error.")
	}
	return nil
}

func (s *Server) gweDiscordRun(ctx context.Context, rec *gweFormRecord) error {
	hook := rec.setting("discord_webhook")
	if hook == "" || !(strings.HasPrefix(hook, "https://discordapp.com/api/webhooks/") || strings.HasPrefix(hook, "https://discord.com/api/webhooks/")) {
		return nil
	}
	page := rec.referrer
	if page == "" {
		page = rec.siteURL
	}
	color := int64(0x9c0244)
	if _, ok := rec.settings["discord_webhook_color"]; ok {
		n, _ := strconv.ParseInt(strings.TrimLeft(rec.setting("discord_webhook_color"), "#"), 16, 64)
		color = n
	}
	or := func(k, def string) string {
		if _, ok := rec.settings[k]; ok {
			return rec.setting(k)
		}
		return def
	}
	var icon any
	if _, ok := rec.settings["discord_avatar_url"]; ok {
		icon = rec.setting("discord_avatar_url")
	}
	embed := map[string]any{
		"title":       or("discord_title", "A new Submission"),
		"description": or("discord_content", "A new Form Submission has been received"),
		"author":      map[string]any{"name": or("discord_username", "Golementor Forms"), "url": page, "icon_url": icon},
		"url":         page, "color": color,
	}
	if rec.setting("discord_form_data") == "yes" {
		fields := []any{}
		for _, f := range rec.fields {
			fields = append(fields, map[string]any{"name": f.ID, "value": f.Value, "inline": false})
		}
		embed["fields"] = fields
	}
	if rec.setting("discord_ts") == "yes" {
		embed["timestamp"] = time.Now().UTC().Format("2006-01-02T15:04:05-0700")
		embed["footer"] = map[string]any{"text": "Powered by Golementor"}
	}
	b, _ := json.Marshal(map[string]any{"embeds": []any{embed}})
	res, err := gweOutboundDo(ctx, http.MethodPost, hook, http.Header{"Content-Type": {"application/json; charset=utf-8"}}, b,
		gweHosts("discord.com", "discordapp.com"), 0)
	if err != nil || res.Code != http.StatusNoContent {
		return errors.New("Webhook error.")
	}
	return nil
}

// --- editor widgets config --------------------------------------------------------------------------

// gweIntegrationsSettingsURL is Golementor › Settings › Integrations.
const gweIntegrationsSettingsURL = "/wp-admin/gowebeditor-pro?tab=settings#integrations"

// gweIntegrationsWidgetsConfig points the form and payment widgets' notices at Golementor ›
// Settings and turns each "API key" alert into upstream's info / warning variant depending on
// whether the global key is set.
func (s *Server) gweIntegrationsWidgetsConfig(c *gin.Context, cfg []byte) []byte {
	home := s.links(c).Home
	out := bytes.ReplaceAll(cfg, []byte(home+"/wp-admin/admin?page=elementor-settings#tab-integrations"), []byte(home+gweIntegrationsSettingsURL))
	out = bytes.ReplaceAll(out, []byte(home+"/wp-admin/admin?page=e-form-submissions"), []byte(home+gweProAdminPath+"?tab=submissions"))
	obj, err := gweOrderedRaw(out)
	if err != nil {
		return out
	}
	for i := range obj {
		if obj[i].K != "form" {
			continue
		}
		var w map[string]json.RawMessage
		if json.Unmarshal(obj[i].V, &w) != nil {
			break
		}
		ctrls, err := gweOrderedRaw(w["controls"])
		if err != nil {
			break
		}
		link := `<a href="` + home + gweIntegrationsSettingsURL + `" target="_blank">Integrations Settings</a>`
		for _, in := range gweIntegrations {
			if in.keyOption == "" {
				continue
			}
			name := in.name + "_api_key_msg"
			var ctl map[string]any
			for _, kv := range ctrls {
				if kv.K == name {
					_ = json.Unmarshal(kv.V, &ctl)
				}
			}
			if ctl == nil {
				continue
			}
			set := s.settings.String(in.keyOption) != ""
			if in.name == "activecampaign" {
				set = set && s.settings.String("gwe_activecampaign_api_url") != ""
			}
			if set {
				ctl["alert_type"] = "info"
				ctl["content"] = "You are using " + in.keyLabel + " set in the " + link + ". You can also set a different " + in.keyLabel + " by choosing &quot;Custom&quot;."
			} else {
				ctl["alert_type"] = "warning"
				ctl["content"] = "Set your " + in.keyLabel + " in the " + link + ". You can also set a different " + in.keyLabel + " by choosing &quot;Custom&quot;."
			}
			raw, _ := json.Marshal(ctl)
			ctrls = ctrls.set(name, raw)
		}
		w["controls"], _ = json.Marshal(ctrls)
		obj[i].V, _ = json.Marshal(w)
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return out
	}
	return b
}

// --- Golementor › Settings › Integrations ------------------------------------------------------------

// gweSecretSetting is one API key field of the Integrations settings.
type gweSecretSetting struct {
	Key, Label, Type, Desc string
	Validate               string // service name for the "Validate" button ("" = none)
}

var gweIntegrationSettingSections = []struct {
	ID, Title, Intro string
	Fields           []gweSecretSetting
}{
	{"mailchimp", "MailChimp", "", []gweSecretSetting{{Key: "gwe_mailchimp_api_key", Label: "API Key", Type: "secret", Validate: "mailchimp",
		Desc: `To integrate with our forms you need an <a href="https://mailchimp.com/help/about-api-keys/" target="_blank" rel="noopener">API Key</a>.`}}},
	{"drip", "Drip", "", []gweSecretSetting{{Key: "gwe_drip_api_token", Label: "API Token", Type: "secret", Validate: "drip",
		Desc: `To integrate with our forms you need an <a href="https://www.getdrip.com/user/edit" target="_blank" rel="noopener">API Token</a>.`}}},
	{"activecampaign", "ActiveCampaign", "", []gweSecretSetting{{Key: "gwe_activecampaign_api_key", Label: "API Key", Type: "secret"},
		{Key: "gwe_activecampaign_api_url", Label: "API URL", Type: "url", Validate: "activecampaign",
			Desc: `To integrate with our forms you need an <a href="https://help.activecampaign.com/hc/en-us/articles/207317590-Getting-started-with-the-API" target="_blank" rel="noopener">API Key</a>.`}}},
	{"getresponse", "GetResponse", "", []gweSecretSetting{{Key: "gwe_getresponse_api_key", Label: "API Key", Type: "secret", Validate: "getresponse",
		Desc: `To integrate with our forms you need an <a href="https://www.getresponse.com/help/integrations-and-api/api.html" target="_blank" rel="noopener">API Key</a>.`}}},
	{"convertkit", "ConvertKit", "", []gweSecretSetting{{Key: "gwe_convertkit_api_key", Label: "API Key", Type: "secret", Validate: "convertkit",
		Desc: `To integrate with our forms you need an <a href="https://app.convertkit.com/account_settings/advanced_settings" target="_blank" rel="noopener">API Key</a>.`}}},
	{"mailerlite", "MailerLite", "", []gweSecretSetting{{Key: "gwe_mailerlite_api_key", Label: "API Key", Type: "secret", Validate: "mailerlite",
		Desc: `To integrate with our forms you need an <a href="https://help.mailerlite.com/article/show/35040-where-can-i-find-the-api-key" target="_blank" rel="noopener">API Key</a>.`}}},
	{"stripe", "Stripe", "Insert the API keys provided in the Stripe dashboard to start collecting payments on your website using Stripe. These keys serve as the default keys for every Stripe button on the site.",
		[]gweSecretSetting{{Key: "gwe_stripe_test_secret_key", Label: "Test Secret key", Type: "secret", Validate: "stripe_test",
			Desc: `Enter your test secret key (<a href="https://dashboard.stripe.com/test/apikeys" target="_blank" rel="noopener">Stripe dashboard</a>).`},
			{Key: "gwe_stripe_live_secret_key", Label: "Live Secret key", Type: "secret", Validate: "stripe_live",
				Desc: `Enter your live secret key (<a href="https://dashboard.stripe.com/apikeys" target="_blank" rel="noopener">Stripe dashboard</a>). The Stripe name and logos are trademarks of Stripe, Inc.`}}},
}

// gweMaskSecret shows the last four characters of a stored key ("" when none).
func gweMaskSecret(v string) string {
	if v == "" {
		return ""
	}
	r := []rune(v)
	tail := ""
	if len(r) > 8 {
		tail = string(r[len(r)-4:])
	}
	return strings.Repeat("•", 12) + tail
}

// gweIntegrationsSettingsSections renders the Integrations and Fonts sections of Golementor ›
// Settings (inside its form). Stored keys are never printed: the inputs show a mask, leaving it
// untouched keeps the key, clearing the input removes it.
func (s *Server) gweIntegrationsSettingsSections(c *gin.Context) string {
	var b strings.Builder
	b.WriteString(`<h2 id="integrations">Integrations</h2><p class="description">API keys for the form actions and payment buttons. Forms and buttons use these keys unless they set a custom one. Saved keys are shown masked.</p>`)
	for _, sec := range gweIntegrationSettingSections {
		fmt.Fprintf(&b, `<h3 id="gwe-integration-%s">%s</h3>`, sec.ID, esc(sec.Title))
		if sec.Intro != "" {
			b.WriteString(`<p class="description">` + esc(sec.Intro) + `</p>`)
		}
		b.WriteString(`<table class="form-table" role="presentation"><tbody>`)
		for _, f := range sec.Fields {
			val, typ, extra := s.settings.String(f.Key), "url", ""
			if f.Type == "secret" {
				val, typ, extra = gweMaskSecret(val), "text", ` spellcheck="false" data-gwe-secret="1"`
			}
			fmt.Fprintf(&b, `<tr><th scope="row"><label for="%s">%s</label></th><td><input id="%s" name="%s" type="%s" class="regular-text code" value="%s" autocomplete="off"%s/>`,
				f.Key, esc(f.Label), f.Key, f.Key, typ, esc(val), extra)
			if f.Validate != "" {
				fmt.Fprintf(&b, ` <button type="button" class="button gwe-validate-key" data-service="%s">Validate API Key</button> <span class="gwe-validate-result" aria-live="polite"></span>`, f.Validate)
			}
			if f.Desc != "" {
				b.WriteString(`<p class="description">` + f.Desc + `</p>`)
			}
			b.WriteString(`</td></tr>`)
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(s.gweFontsSettingsSection(c))
	nonce := s.nonce(c, "gwe-integration-validate")
	b.WriteString(`<script id="gwe-integrations-settings-js">
(function(){
	document.querySelectorAll('.gwe-validate-key').forEach(function(btn){
		btn.addEventListener('click', function(){
			var service = btn.getAttribute('data-service'), out = btn.parentNode.querySelector('.gwe-validate-result');
			var fd = new FormData();
			fd.append('action', 'gwe_integration_validate');
			fd.append('_nonce', '` + nonce + `');
			fd.append('service', service);
			btn.closest('table').querySelectorAll('input').forEach(function(i){ fd.append(i.name, i.value); });
			btn.disabled = true; out.textContent = 'Checking…';
			fetch('` + s.links(c).HomeURL("/wp-admin/admin-ajax") + `', {method: 'POST', body: fd, credentials: 'same-origin'})
				.then(function(r){ return r.json(); })
				.then(function(r){ out.textContent = r && r.success ? 'The API key is valid.' : ((r && r.data) || 'The API key is not valid.'); })
				.catch(function(){ out.textContent = 'Could not check the key.'; })
				.finally(function(){ btn.disabled = false; });
		});
	});
})();
</script>`)
	return b.String()
}

// gweIntegrationsSaveSettings adds the Integrations and Fonts fields to Golementor › Settings' save.
func (s *Server) gweIntegrationsSaveSettings(c *gin.Context, vals map[string]any) {
	for _, sec := range gweIntegrationSettingSections {
		for _, f := range sec.Fields {
			v := strings.TrimSpace(c.PostForm(f.Key))
			if f.Type == "secret" && v != "" && v == gweMaskSecret(s.settings.String(f.Key)) {
				continue // the mask: keep the stored key
			}
			if f.Type == "url" && v != "" {
				if u, err := url.Parse(v); err != nil || u.Scheme != "https" || u.Host == "" {
					continue
				}
			}
			vals[f.Key] = v
		}
	}
	s.gweFontsSaveSettings(c, vals)
}

// gweSecretFromForm reads a key from the validate request: the typed key, or the stored one when
// the field still shows the mask.
func (s *Server) gweSecretFromForm(c *gin.Context, key string) string {
	v := strings.TrimSpace(c.PostForm(key))
	if v == "" || v == gweMaskSecret(s.settings.String(key)) {
		return strings.TrimSpace(s.settings.String(key))
	}
	return v
}

// gweIntegrationValidate is admin-ajax gwe_integration_validate (the settings' "Validate API
// Key" buttons; upstream's pro_<service>_api_key_validate actions).
func (s *Server) gweIntegrationValidate(c *gin.Context) {
	u := currentUser(c)
	if u == nil || !u.Can("manage_options", nil) {
		jsonError(c, http.StatusForbidden, "Permission denied")
		return
	}
	if !s.checkNonceValue(c, c.PostForm("_nonce"), "gwe-integration-validate") {
		jsonError(c, http.StatusForbidden, "Something went wrong, please refresh the page.")
		return
	}
	ctx := c.Request.Context()
	var err error
	switch c.PostForm("service") {
	case "mailchimp":
		var m *gweMailchimp
		if m, err = newGWEMailchimp(ctx, s.gweSecretFromForm(c, "gwe_mailchimp_api_key")); err == nil {
			_, err = m.lists()
		}
	case "drip":
		_, _, err = newGWEDrip(ctx, s.gweSecretFromForm(c, "gwe_drip_api_token"))
	case "activecampaign":
		_, _, err = newGWEActiveCampaign(ctx, s.gweSecretFromForm(c, "gwe_activecampaign_api_key"), strings.TrimSpace(c.PostForm("gwe_activecampaign_api_url")))
	case "getresponse":
		_, _, err = newGWEGetResponse(ctx, s.gweSecretFromForm(c, "gwe_getresponse_api_key"))
	case "convertkit":
		_, _, err = newGWEConvertKit(ctx, s.gweSecretFromForm(c, "gwe_convertkit_api_key"))
	case "mailerlite":
		_, err = newGWEMailerLite(ctx, s.gweSecretFromForm(c, "gwe_mailerlite_api_key"))
	case "stripe_test":
		err = gweStripeValidate(ctx, s.gweSecretFromForm(c, "gwe_stripe_test_secret_key"))
	case "stripe_live":
		err = gweStripeValidate(ctx, s.gweSecretFromForm(c, "gwe_stripe_live_secret_key"))
	default:
		err = errors.New("Unknown service.")
	}
	if err != nil {
		jsonError(c, http.StatusOK, "The API key is not valid: "+err.Error())
		return
	}
	jsonSuccess(c, true)
}

// gweIntegrationsSortedKeys lists the option names this file stores (for tests and exports).
func gweIntegrationsSortedKeys() []string {
	var out []string
	for _, sec := range gweIntegrationSettingSections {
		for _, f := range sec.Fields {
			out = append(out, f.Key)
		}
	}
	sort.Strings(out)
	return out
}
