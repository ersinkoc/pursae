package mail

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SignV4 signs an AWS request (Signature Version 4) for Amazon SES.
func SignV4(req *http.Request, body []byte, accessKey, secretKey, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	host := req.URL.Host
	names := []string{"host"}
	vals := map[string]string{"host": host}
	for k := range req.Header {
		lk := strings.ToLower(k)
		if lk == "content-type" || strings.HasPrefix(lk, "x-amz-") {
			names = append(names, lk)
			vals[lk] = strings.TrimSpace(req.Header.Get(k))
		}
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		canonHeaders.WriteString(n + ":" + vals[n] + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{req.Method, path, req.URL.Query().Encode(), canonHeaders.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	ch := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	mac := func(key []byte, data string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(data))
		return h.Sum(nil)
	}
	k := mac(mac(mac(mac([]byte("AWS4"+secretKey), day), region), service), "aws4_request")
	sig := hex.EncodeToString(mac(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

// OAuth is the authorization-code flow of the Gmail, Outlook and Zoho mailers, used with the site
// owner's own app registration (client id + secret) like WP Mail SMTP's "own app" setup.
type OAuth struct {
	Mailer       string // gmail, outlook or zoho
	Domain       string // Zoho data center: com, eu, in, com.au, jp, com.cn
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Client       *http.Client
	Base         string // replaces scheme and host of the provider URLs (tests)
}

// Token is an OAuth token pair.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
}

// ZohoDomain normalizes a Zoho data center ("com" when unknown).
func ZohoDomain(d string) string {
	switch d {
	case "eu", "in", "com.au", "jp", "com.cn", "ca", "sa":
		return d
	}
	return "com"
}

func (o *OAuth) url(def string) string {
	return (&API{Base: o.Base}).url(def)
}

func (o *OAuth) endpoints() (auth, token, scope string) {
	switch o.Mailer {
	case "gmail":
		return "https://accounts.google.com/o/oauth2/v2/auth", "https://oauth2.googleapis.com/token", "https://mail.google.com/"
	case "outlook":
		return "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", "https://login.microsoftonline.com/common/oauth2/v2.0/token",
			"https://graph.microsoft.com/Mail.Send https://graph.microsoft.com/User.Read offline_access"
	default: // zoho
		d := ZohoDomain(o.Domain)
		return "https://accounts.zoho." + d + "/oauth/v2/auth", "https://accounts.zoho." + d + "/oauth/v2/token",
			"ZohoMail.messages.CREATE,ZohoMail.accounts.READ"
	}
}

// AuthCodeURL is where the admin is sent to grant access.
func (o *OAuth) AuthCodeURL(state string) string {
	auth, _, scope := o.endpoints()
	q := url.Values{"client_id": {o.ClientID}, "redirect_uri": {o.RedirectURI}, "response_type": {"code"}, "scope": {scope}, "state": {state}}
	switch o.Mailer {
	case "gmail":
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	case "zoho":
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	}
	return o.url(auth) + "?" + q.Encode()
}

// Exchange trades an authorization code for tokens.
func (o *OAuth) Exchange(ctx context.Context, code string) (*Token, error) {
	return o.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {o.RedirectURI}})
}

// Refresh gets a new access token; the refresh token is kept when the provider sends none.
func (o *OAuth) Refresh(ctx context.Context, refresh string) (*Token, error) {
	t, err := o.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
	if err == nil && t.RefreshToken == "" {
		t.RefreshToken = refresh
	}
	return t, err
}

func (o *OAuth) token(ctx context.Context, f url.Values) (*Token, error) {
	_, tokenURL, _ := o.endpoints()
	f.Set("client_id", o.ClientID)
	f.Set("client_secret", o.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.url(tokenURL), strings.NewReader(f.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var v struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		ExpiresIn    float64 `json:"expires_in"`
		Error        string  `json:"error"`
		Description  string  `json:"error_description"`
	}
	status, body, err := o.do(req)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(body, &v)
	if status >= 300 || v.AccessToken == "" {
		return nil, fmt.Errorf("%s token request failed (HTTP %d): %s", o.Mailer, status, firstNonEmpty(v.Description, v.Error, truncate(string(body), 200)))
	}
	if v.ExpiresIn == 0 {
		v.ExpiresIn = 3600
	}
	return &Token{AccessToken: v.AccessToken, RefreshToken: v.RefreshToken, Expiry: time.Now().Add(time.Duration(v.ExpiresIn) * time.Second)}, nil
}

func (o *OAuth) do(req *http.Request) (int, []byte, error) {
	c := o.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}

// Account looks up the connected mailbox: its address, and for Zoho the account id the send API needs.
func (o *OAuth) Account(ctx context.Context, accessToken string) (email, accountID string, err error) {
	var u, auth string
	switch o.Mailer {
	case "gmail":
		u, auth = "https://gmail.googleapis.com/gmail/v1/users/me/profile", "Bearer "
	case "outlook":
		u, auth = "https://graph.microsoft.com/v1.0/me", "Bearer "
	default:
		u, auth = "https://mail.zoho."+ZohoDomain(o.Domain)+"/api/accounts", "Zoho-oauthtoken "
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.url(u), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", auth+accessToken)
	status, body, err := o.do(req)
	if err != nil {
		return "", "", err
	}
	if status >= 300 {
		return "", "", fmt.Errorf("%s account lookup failed (HTTP %d): %s", o.Mailer, status, apiErrorText(body))
	}
	var v map[string]any
	_ = json.Unmarshal(body, &v)
	s := func(m map[string]any, k string) string { x, _ := m[k].(string); return x }
	switch o.Mailer {
	case "gmail":
		return s(v, "emailAddress"), "", nil
	case "outlook":
		return firstNonEmpty(s(v, "mail"), s(v, "userPrincipalName")), "", nil
	}
	list, _ := v["data"].([]any)
	if len(list) == 0 {
		return "", "", fmt.Errorf("zoho: no mail account found")
	}
	acc, _ := list[0].(map[string]any)
	id := s(acc, "accountId")
	if f, ok := acc["accountId"].(float64); ok {
		id = fmt.Sprintf("%.0f", f)
	}
	return firstNonEmpty(s(acc, "primaryEmailAddress"), s(acc, "mailboxAddress")), id, nil
}
