package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// Email is a message as the HTTP API mailers of WPGO Mail SMTP take it: the parts most APIs want
// as JSON fields, plus the complete MIME message for the APIs that send raw messages.
type Email struct {
	FromName, FromEmail string
	To                  []string // addresses, "Name <a@b>" allowed
	ReplyTo             string
	Subject, Text, HTML string
	Raw                 []byte // RFC 5322 message (Gmail, Microsoft Graph, Amazon SES)
}

// API sends through a mail provider's HTTP API. Mailer is WP Mail SMTP's mailer slug; Opts holds
// that mailer's settings with WP Mail SMTP's keys (api_key, domain, region, …) in plain text.
type API struct {
	Mailer string
	Opts   map[string]string
	Client *http.Client
	Base   string    // replaces the scheme and host of the provider URLs (tests)
	Debug  io.Writer // request line, status and response body, optional
}

// APIMailers lists the mailers API.Send handles.
var APIMailers = []string{"sendlayer", "smtpcom", "sendinblue", "mailgun", "sendgrid", "postmark", "sparkpost", "amazonses",
	"gmail", "outlook", "zoho", "elasticemail", "mailjet", "mailersend", "resend", "smtp2go", "mandrill"}

type addr struct{ Name, Email string }

func parseAddrs(list []string) []addr {
	var out []addr
	for _, s := range list {
		if a, err := mail.ParseAddress(s); err == nil {
			out = append(out, addr{a.Name, a.Address})
		}
	}
	return out
}

func (a *API) opt(k string) string { return strings.TrimSpace(a.Opts[k]) }

// url points a provider URL at Base when set.
func (a *API) url(def string) string {
	if a.Base == "" {
		return def
	}
	u, err := url.Parse(def)
	if err != nil {
		return def
	}
	return strings.TrimRight(a.Base, "/") + u.RequestURI()
}

type apiReq struct {
	method, url string
	header      map[string]string
	body        []byte
	user, pass  string // basic auth
	sign        func(*http.Request, []byte)
}

func jsonBody(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// Send delivers e and returns the provider's message id when it reports one.
func (a *API) Send(ctx context.Context, e Email) (string, error) {
	to := parseAddrs(e.To)
	if len(to) == 0 {
		return "", fmt.Errorf("no valid recipients")
	}
	var reply *addr
	if r := parseAddrs([]string{e.ReplyTo}); len(r) == 1 {
		reply = &r[0]
	}
	from := (&mail.Address{Name: e.FromName, Address: e.FromEmail}).String()
	text, html := e.Text, e.HTML
	var r apiReq
	r.method = http.MethodPost
	r.header = map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
	need := func(keys ...string) error {
		for _, k := range keys {
			if a.opt(k) == "" {
				return fmt.Errorf("the %s setting %q is empty", a.Mailer, k)
			}
		}
		return nil
	}
	switch a.Mailer {
	case "sendlayer":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://console.sendlayer.com/api/v1/email")
		r.header["Authorization"] = "Bearer " + a.opt("api_key")
		var tos []map[string]string
		for _, t := range to {
			tos = append(tos, map[string]string{"name": t.Name, "email": t.Email})
		}
		body := map[string]any{"From": map[string]string{"name": e.FromName, "email": e.FromEmail}, "To": tos, "Subject": e.Subject,
			"ContentType": "Plain", "PlainContent": text}
		if html != "" {
			body["ContentType"], body["HTMLContent"] = "HTML", html
		}
		if reply != nil {
			body["ReplyTo"] = []map[string]string{{"name": reply.Name, "email": reply.Email}}
		}
		r.body = jsonBody(body)
	case "smtpcom":
		if err := need("api_key", "channel"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.smtp.com/v4/messages")
		r.header["Authorization"] = "Bearer " + a.opt("api_key")
		var tos []map[string]string
		for _, t := range to {
			tos = append(tos, map[string]string{"name": t.Name, "address": t.Email})
		}
		parts := []map[string]string{{"type": "text/plain", "content": text}}
		if html != "" {
			parts = append(parts, map[string]string{"type": "text/html", "content": html})
		}
		orig := map[string]any{"from": map[string]string{"name": e.FromName, "address": e.FromEmail}}
		if reply != nil {
			orig["reply_to"] = map[string]string{"name": reply.Name, "address": reply.Email}
		}
		r.body = jsonBody(map[string]any{"channel": a.opt("channel"), "recipients": map[string]any{"to": tos}, "originator": orig,
			"subject": e.Subject, "body": map[string]any{"parts": parts}})
	case "sendinblue":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.brevo.com/v3/smtp/email")
		r.header["api-key"] = a.opt("api_key")
		var tos []map[string]string
		for _, t := range to {
			m := map[string]string{"email": t.Email}
			if t.Name != "" {
				m["name"] = t.Name
			}
			tos = append(tos, m)
		}
		body := map[string]any{"sender": map[string]string{"name": e.FromName, "email": e.FromEmail}, "to": tos, "subject": e.Subject, "textContent": text}
		if html != "" {
			body["htmlContent"] = html
		}
		if reply != nil {
			body["replyTo"] = map[string]string{"email": reply.Email}
		}
		r.body = jsonBody(body)
	case "mailgun":
		if err := need("api_key", "domain"); err != nil {
			return "", err
		}
		host := "https://api.mailgun.net"
		if strings.EqualFold(a.opt("region"), "EU") {
			host = "https://api.eu.mailgun.net"
		}
		r.url = a.url(host + "/v3/" + url.PathEscape(a.opt("domain")) + "/messages")
		r.user, r.pass = "api", a.opt("api_key")
		f := url.Values{"from": {from}, "subject": {e.Subject}, "text": {text}}
		for _, t := range e.To {
			f.Add("to", t)
		}
		if html != "" {
			f.Set("html", html)
		}
		if reply != nil {
			f.Set("h:Reply-To", e.ReplyTo)
		}
		r.header["Content-Type"] = "application/x-www-form-urlencoded"
		r.body = []byte(f.Encode())
	case "sendgrid":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.sendgrid.com/v3/mail/send")
		r.header["Authorization"] = "Bearer " + a.opt("api_key")
		var tos []map[string]string
		for _, t := range to {
			m := map[string]string{"email": t.Email}
			if t.Name != "" {
				m["name"] = t.Name
			}
			tos = append(tos, m)
		}
		content := []map[string]string{{"type": "text/plain", "value": firstNonEmpty(text, " ")}}
		if html != "" {
			content = append(content, map[string]string{"type": "text/html", "value": html})
		}
		body := map[string]any{"personalizations": []any{map[string]any{"to": tos}}, "from": map[string]string{"email": e.FromEmail, "name": e.FromName},
			"subject": e.Subject, "content": content}
		if reply != nil {
			body["reply_to"] = map[string]string{"email": reply.Email}
		}
		r.body = jsonBody(body)
	case "postmark":
		if err := need("server_api_token"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.postmarkapp.com/email")
		r.header["X-Postmark-Server-Token"] = a.opt("server_api_token")
		body := map[string]any{"From": from, "To": strings.Join(e.To, ","), "Subject": e.Subject, "TextBody": text}
		if html != "" {
			body["HtmlBody"] = html
		}
		if reply != nil {
			body["ReplyTo"] = e.ReplyTo
		}
		if s := a.opt("message_stream"); s != "" {
			body["MessageStream"] = s
		}
		r.body = jsonBody(body)
	case "sparkpost":
		if err := need("api_key"); err != nil {
			return "", err
		}
		host := "https://api.sparkpost.com"
		if strings.EqualFold(a.opt("region"), "EU") {
			host = "https://api.eu.sparkpost.com"
		}
		r.url = a.url(host + "/api/v1/transmissions")
		r.header["Authorization"] = a.opt("api_key")
		var rcpts []any
		for _, t := range to {
			rcpts = append(rcpts, map[string]any{"address": map[string]string{"email": t.Email, "name": t.Name}})
		}
		content := map[string]any{"from": map[string]string{"email": e.FromEmail, "name": e.FromName}, "subject": e.Subject, "text": text}
		if html != "" {
			content["html"] = html
		}
		if reply != nil {
			content["reply_to"] = e.ReplyTo
		}
		r.body = jsonBody(map[string]any{"content": content, "recipients": rcpts})
	case "amazonses":
		if err := need("client_id", "client_secret", "region"); err != nil {
			return "", err
		}
		region := a.opt("region")
		r.url = a.url("https://email." + region + ".amazonaws.com/v2/email/outbound-emails")
		var tos []string
		for _, t := range to {
			tos = append(tos, t.Email)
		}
		r.body = jsonBody(map[string]any{"FromEmailAddress": from, "Destination": map[string]any{"ToAddresses": tos},
			"Content": map[string]any{"Raw": map[string]string{"Data": base64.StdEncoding.EncodeToString(e.Raw)}}})
		key, secret := a.opt("client_id"), a.opt("client_secret")
		r.sign = func(req *http.Request, body []byte) { SignV4(req, body, key, secret, region, "ses", time.Now()) }
	case "gmail":
		if err := need("access_token"); err != nil {
			return "", fmt.Errorf("Gmail is not connected: authorize the plugin with your Google account first")
		}
		r.url = a.url("https://gmail.googleapis.com/gmail/v1/users/me/messages/send")
		r.header["Authorization"] = "Bearer " + a.opt("access_token")
		r.body = jsonBody(map[string]string{"raw": base64.RawURLEncoding.EncodeToString(e.Raw)})
	case "outlook":
		if err := need("access_token"); err != nil {
			return "", fmt.Errorf("Outlook is not connected: authorize the plugin with your Microsoft account first")
		}
		r.url = a.url("https://graph.microsoft.com/v1.0/me/sendMail")
		r.header["Authorization"] = "Bearer " + a.opt("access_token")
		r.header["Content-Type"] = "text/plain"
		r.body = []byte(base64.StdEncoding.EncodeToString(e.Raw))
	case "zoho":
		if err := need("access_token", "account_id"); err != nil {
			return "", fmt.Errorf("Zoho Mail is not connected: authorize the plugin with your Zoho account first")
		}
		r.url = a.url("https://mail.zoho." + ZohoDomain(a.opt("domain")) + "/api/accounts/" + url.PathEscape(a.opt("account_id")) + "/messages")
		r.header["Authorization"] = "Zoho-oauthtoken " + a.opt("access_token")
		var tos []string
		for _, t := range to {
			tos = append(tos, t.Email)
		}
		body := map[string]any{"fromAddress": e.FromEmail, "toAddress": strings.Join(tos, ","), "subject": e.Subject,
			"content": firstNonEmpty(html, text), "mailFormat": map[bool]string{true: "html", false: "plaintext"}[html != ""]}
		if reply != nil {
			body["replyTo"] = reply.Email
		}
		r.body = jsonBody(body)
	case "elasticemail":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.elasticemail.com/v4/emails/transactional")
		r.header["X-ElasticEmail-ApiKey"] = a.opt("api_key")
		parts := []map[string]string{{"ContentType": "PlainText", "Content": text}}
		if html != "" {
			parts = append([]map[string]string{{"ContentType": "HTML", "Content": html}}, parts...)
		}
		content := map[string]any{"From": from, "Subject": e.Subject, "Body": parts}
		if reply != nil {
			content["ReplyTo"] = e.ReplyTo
		}
		r.body = jsonBody(map[string]any{"Recipients": map[string]any{"To": e.To}, "Content": content})
	case "mailjet":
		if err := need("api_key", "secret_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.mailjet.com/v3.1/send")
		r.user, r.pass = a.opt("api_key"), a.opt("secret_key")
		var tos []map[string]string
		for _, t := range to {
			tos = append(tos, map[string]string{"Email": t.Email, "Name": t.Name})
		}
		m := map[string]any{"From": map[string]string{"Email": e.FromEmail, "Name": e.FromName}, "To": tos, "Subject": e.Subject, "TextPart": text}
		if html != "" {
			m["HTMLPart"] = html
		}
		if reply != nil {
			m["ReplyTo"] = map[string]string{"Email": reply.Email, "Name": reply.Name}
		}
		r.body = jsonBody(map[string]any{"Messages": []any{m}})
	case "mailersend":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.mailersend.com/v1/email")
		r.header["Authorization"] = "Bearer " + a.opt("api_key")
		var tos []map[string]string
		for _, t := range to {
			m := map[string]string{"email": t.Email}
			if t.Name != "" {
				m["name"] = t.Name
			}
			tos = append(tos, m)
		}
		body := map[string]any{"from": map[string]string{"email": e.FromEmail, "name": e.FromName}, "to": tos, "subject": e.Subject, "text": text}
		if html != "" {
			body["html"] = html
		}
		if reply != nil {
			body["reply_to"] = map[string]string{"email": reply.Email}
		}
		r.body = jsonBody(body)
	case "resend":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.resend.com/emails")
		r.header["Authorization"] = "Bearer " + a.opt("api_key")
		body := map[string]any{"from": from, "to": e.To, "subject": e.Subject, "text": text}
		if html != "" {
			body["html"] = html
		}
		if reply != nil {
			body["reply_to"] = e.ReplyTo
		}
		r.body = jsonBody(body)
	case "smtp2go":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://api.smtp2go.com/v3/email/send")
		r.header["X-Smtp2go-Api-Key"] = a.opt("api_key")
		body := map[string]any{"sender": from, "to": e.To, "subject": e.Subject, "text_body": text}
		if html != "" {
			body["html_body"] = html
		}
		if reply != nil {
			body["custom_headers"] = []map[string]string{{"header": "Reply-To", "value": e.ReplyTo}}
		}
		r.body = jsonBody(body)
	case "mandrill":
		if err := need("api_key"); err != nil {
			return "", err
		}
		r.url = a.url("https://mandrillapp.com/api/1.0/messages/send.json")
		var tos []map[string]string
		for _, t := range to {
			tos = append(tos, map[string]string{"email": t.Email, "name": t.Name, "type": "to"})
		}
		m := map[string]any{"from_email": e.FromEmail, "from_name": e.FromName, "to": tos, "subject": e.Subject, "text": text}
		if html != "" {
			m["html"] = html
		}
		if reply != nil {
			m["headers"] = map[string]string{"Reply-To": e.ReplyTo}
		}
		r.body = jsonBody(map[string]any{"key": a.opt("api_key"), "message": m})
	default:
		return "", fmt.Errorf("unknown mailer %q", a.Mailer)
	}
	status, hdr, body, err := a.do(ctx, r)
	if err != nil {
		return "", err
	}
	if status >= 300 {
		return "", fmt.Errorf("%s API error (HTTP %d): %s", a.Mailer, status, apiErrorText(body))
	}
	return a.messageID(hdr, body)
}

func (a *API) do(ctx context.Context, r apiReq) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, r.method, r.url, bytes.NewReader(r.body))
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "WPGO Mail SMTP")
	if r.user != "" || r.pass != "" {
		req.SetBasicAuth(r.user, r.pass)
	}
	if r.sign != nil {
		r.sign(req, r.body)
	}
	if a.Debug != nil {
		fmt.Fprintf(a.Debug, "%s %s (%d bytes)\n", r.method, r.url, len(r.body))
	}
	c := a.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		if a.Debug != nil {
			fmt.Fprintf(a.Debug, "Request failed: %v\n", err)
		}
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if a.Debug != nil {
		fmt.Fprintf(a.Debug, "HTTP %d\n%s\n", resp.StatusCode, truncate(string(body), 2000))
	}
	return resp.StatusCode, resp.Header, body, nil
}

// messageID reads the provider's message id; APIs that answer 200 with a per-recipient failure
// (Mandrill, Mailjet, SMTP2GO) turn that into an error.
func (a *API) messageID(hdr http.Header, body []byte) (string, error) {
	var v any
	_ = json.Unmarshal(body, &v)
	str := func(path ...any) string {
		cur := v
		for _, p := range path {
			switch k := p.(type) {
			case string:
				m, _ := cur.(map[string]any)
				cur = m[k]
			case int:
				l, _ := cur.([]any)
				if k >= len(l) {
					return ""
				}
				cur = l[k]
			}
		}
		switch t := cur.(type) {
		case string:
			return t
		case float64:
			return fmt.Sprintf("%.0f", t)
		}
		return ""
	}
	switch a.Mailer {
	case "sendlayer", "postmark":
		return str("MessageID"), nil
	case "smtpcom":
		return str("data", "message"), nil
	case "sendinblue":
		return str("messageId"), nil
	case "mailgun", "resend", "gmail":
		return str("id"), nil
	case "sendgrid", "mailersend":
		return hdr.Get("X-Message-Id"), nil
	case "sparkpost":
		return str("results", "id"), nil
	case "amazonses":
		return str("MessageId"), nil
	case "zoho":
		return str("data", "messageId"), nil
	case "elasticemail":
		return firstNonEmpty(str("MessageID"), str("TransactionID")), nil
	case "mailjet":
		if st := str("Messages", 0, "Status"); st != "" && st != "success" {
			return "", fmt.Errorf("mailjet: message status %q: %s", st, truncate(string(body), 300))
		}
		return str("Messages", 0, "To", 0, "MessageUUID"), nil
	case "smtp2go":
		if str("data", "failed") != "" && str("data", "failed") != "0" {
			return "", fmt.Errorf("smtp2go: %s", truncate(string(body), 300))
		}
		return str("data", "email_id"), nil
	case "mandrill":
		if st := str(0, "status"); st == "rejected" || st == "invalid" {
			return "", fmt.Errorf("mandrill: message %s (%s)", st, str(0, "reject_reason"))
		}
		return str(0, "_id"), nil
	}
	return "", nil
}

// apiErrorText pulls the human message out of a provider's error body.
func apiErrorText(body []byte) string {
	var v map[string]any
	if json.Unmarshal(body, &v) == nil {
		for _, k := range []string{"message", "Message", "error_description", "error", "ErrorMessage", "errors"} {
			switch t := v[k].(type) {
			case string:
				if t != "" {
					return t
				}
			case map[string]any:
				if m, ok := t["message"].(string); ok {
					return m
				}
			case []any:
				if len(t) > 0 {
					b, _ := json.Marshal(t[0])
					return string(b)
				}
			}
		}
	}
	return truncate(strings.TrimSpace(string(body)), 300)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
