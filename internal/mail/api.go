package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// Email is a message as the HTTP API mailers of WPGO Mail SMTP take it: the parts most APIs want
// as JSON fields, plus the complete MIME message for the APIs that send raw messages.
type Email struct {
	FromName, FromEmail string
	To                  []string // addresses, "Name <a@b>" allowed
	Cc, Bcc             []string // extra recipients; Bcc never shows in the headers
	ReplyTo             string
	Subject, Text, HTML string
	Attachments         []Attachment
	Raw                 []byte // RFC 5322 message (Gmail, Microsoft Graph, Amazon SES); it has no Bcc header
}

func b64(d []byte) string { return base64.StdEncoding.EncodeToString(d) }

func ctype(a Attachment) string {
	if a.ContentType == "" {
		return "application/octet-stream"
	}
	return a.ContentType
}

// withBcc adds a Bcc header to a raw message for the APIs that take the recipients from the headers
// and strip Bcc before delivery (Gmail, Microsoft Graph).
func withBcc(raw []byte, bcc []addr) []byte {
	if len(bcc) == 0 {
		return raw
	}
	var l []string
	for _, b := range bcc {
		l = append(l, (&mail.Address{Name: b.Name, Address: b.Email}).String())
	}
	return append([]byte("Bcc: "+strings.Join(l, ", ")+"\r\n"), raw...)
}

// people lists addresses as JSON objects with the provider's key names; the name is left out when empty.
func people(list []addr, nameKey, emailKey string) []map[string]string {
	var out []map[string]string
	for _, t := range list {
		m := map[string]string{emailKey: t.Email}
		if t.Name != "" {
			m[nameKey] = t.Name
		}
		out = append(out, m)
	}
	return out
}

func emails(list []addr) []string {
	var out []string
	for _, t := range list {
		out = append(out, t.Email)
	}
	return out
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
	cc, bcc := parseAddrs(e.Cc), parseAddrs(e.Bcc)
	if len(cc) != len(e.Cc) || len(bcc) != len(e.Bcc) {
		return "", fmt.Errorf("invalid Cc or Bcc recipient")
	}
	atts := e.Attachments
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
		if len(cc) > 0 {
			body["CC"] = people(cc, "name", "email")
		}
		if len(bcc) > 0 {
			body["BCC"] = people(bcc, "name", "email")
		}
		for _, at := range atts {
			l, _ := body["Attachments"].([]map[string]string)
			body["Attachments"] = append(l, map[string]string{"Content": b64(at.Data), "Type": ctype(at), "Filename": at.Name, "Disposition": "attachment"})
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
		rcpts := map[string]any{"to": tos}
		if len(cc) > 0 {
			rcpts["cc"] = people(cc, "name", "address")
		}
		if len(bcc) > 0 {
			rcpts["bcc"] = people(bcc, "name", "address")
		}
		mb := map[string]any{"parts": parts}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"content": b64(at.Data), "type": ctype(at), "encoding": "base64", "filename": at.Name, "disposition": "attachment"})
		}
		if al != nil {
			mb["attachments"] = al
		}
		r.body = jsonBody(map[string]any{"channel": a.opt("channel"), "recipients": rcpts, "originator": orig,
			"subject": e.Subject, "body": mb})
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
		if len(cc) > 0 {
			body["cc"] = people(cc, "name", "email")
		}
		if len(bcc) > 0 {
			body["bcc"] = people(bcc, "name", "email")
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"content": b64(at.Data), "name": at.Name})
		}
		if al != nil {
			body["attachment"] = al
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
		for _, c := range e.Cc {
			f.Add("cc", c)
		}
		for _, b := range e.Bcc {
			f.Add("bcc", b)
		}
		if len(atts) == 0 {
			r.header["Content-Type"] = "application/x-www-form-urlencoded"
			r.body = []byte(f.Encode())
			break
		}
		var mb bytes.Buffer
		mw := multipart.NewWriter(&mb)
		for k, vs := range f {
			for _, v := range vs {
				_ = mw.WriteField(k, v)
			}
		}
		for _, at := range atts {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachment"; filename=%q`, at.Name))
			h.Set("Content-Type", ctype(at))
			pw, err := mw.CreatePart(h)
			if err != nil {
				return "", err
			}
			_, _ = pw.Write(at.Data)
		}
		_ = mw.Close()
		r.header["Content-Type"] = mw.FormDataContentType()
		r.body = mb.Bytes()
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
		pers := map[string]any{"to": tos}
		if len(cc) > 0 {
			pers["cc"] = people(cc, "name", "email")
		}
		if len(bcc) > 0 {
			pers["bcc"] = people(bcc, "name", "email")
		}
		body := map[string]any{"personalizations": []any{pers}, "from": map[string]string{"email": e.FromEmail, "name": e.FromName},
			"subject": e.Subject, "content": content}
		if reply != nil {
			body["reply_to"] = map[string]string{"email": reply.Email}
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"content": b64(at.Data), "type": ctype(at), "filename": at.Name, "disposition": "attachment"})
		}
		if al != nil {
			body["attachments"] = al
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
		if len(e.Cc) > 0 {
			body["Cc"] = strings.Join(e.Cc, ",")
		}
		if len(e.Bcc) > 0 {
			body["Bcc"] = strings.Join(e.Bcc, ",")
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"Name": at.Name, "Content": b64(at.Data), "ContentType": ctype(at)})
		}
		if al != nil {
			body["Attachments"] = al
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
		headerTo := strings.Join(emails(to), ",")
		for _, t := range append(append([]addr{}, cc...), bcc...) { // carbon copies: header_to names the primary recipients
			rcpts = append(rcpts, map[string]any{"address": map[string]string{"email": t.Email, "name": t.Name, "header_to": headerTo}})
		}
		content := map[string]any{"from": map[string]string{"email": e.FromEmail, "name": e.FromName}, "subject": e.Subject, "text": text}
		if html != "" {
			content["html"] = html
		}
		if reply != nil {
			content["reply_to"] = e.ReplyTo
		}
		if len(e.Cc) > 0 {
			content["headers"] = map[string]string{"CC": strings.Join(e.Cc, ", ")}
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"name": at.Name, "type": ctype(at), "data": b64(at.Data)})
		}
		if al != nil {
			content["attachments"] = al
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
		dest := map[string]any{"ToAddresses": tos}
		if len(cc) > 0 {
			dest["CcAddresses"] = emails(cc)
		}
		if len(bcc) > 0 {
			dest["BccAddresses"] = emails(bcc)
		}
		r.body = jsonBody(map[string]any{"FromEmailAddress": from, "Destination": dest,
			"Content": map[string]any{"Raw": map[string]string{"Data": base64.StdEncoding.EncodeToString(e.Raw)}}})
		key, secret := a.opt("client_id"), a.opt("client_secret")
		r.sign = func(req *http.Request, body []byte) { SignV4(req, body, key, secret, region, "ses", time.Now()) }
	case "gmail":
		if err := need("access_token"); err != nil {
			return "", fmt.Errorf("Gmail is not connected: authorize the plugin with your Google account first")
		}
		r.url = a.url("https://gmail.googleapis.com/gmail/v1/users/me/messages/send")
		r.header["Authorization"] = "Bearer " + a.opt("access_token")
		r.body = jsonBody(map[string]string{"raw": base64.RawURLEncoding.EncodeToString(withBcc(e.Raw, bcc))})
	case "outlook":
		if err := need("access_token"); err != nil {
			return "", fmt.Errorf("Outlook is not connected: authorize the plugin with your Microsoft account first")
		}
		r.url = a.url("https://graph.microsoft.com/v1.0/me/sendMail")
		r.header["Authorization"] = "Bearer " + a.opt("access_token")
		r.header["Content-Type"] = "text/plain"
		r.body = []byte(base64.StdEncoding.EncodeToString(withBcc(e.Raw, bcc)))
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
		if len(cc) > 0 {
			body["ccAddress"] = strings.Join(emails(cc), ",")
		}
		if len(bcc) > 0 {
			body["bccAddress"] = strings.Join(emails(bcc), ",")
		}
		if len(atts) > 0 { // Zoho needs each file uploaded first: refuse instead of sending without them
			return "", fmt.Errorf("the Zoho Mail mailer cannot send attachments; use another mailer for this email")
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
		rcpts := map[string]any{"To": e.To}
		if len(e.Cc) > 0 {
			rcpts["CC"] = e.Cc
		}
		if len(e.Bcc) > 0 {
			rcpts["BCC"] = e.Bcc
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"BinaryContent": b64(at.Data), "Name": at.Name, "ContentType": ctype(at)})
		}
		if al != nil {
			content["Attachments"] = al
		}
		r.body = jsonBody(map[string]any{"Recipients": rcpts, "Content": content})
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
		if len(cc) > 0 {
			m["Cc"] = people(cc, "Name", "Email")
		}
		if len(bcc) > 0 {
			m["Bcc"] = people(bcc, "Name", "Email")
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"ContentType": ctype(at), "Filename": at.Name, "Base64Content": b64(at.Data)})
		}
		if al != nil {
			m["Attachments"] = al
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
		if len(cc) > 0 {
			body["cc"] = people(cc, "name", "email")
		}
		if len(bcc) > 0 {
			body["bcc"] = people(bcc, "name", "email")
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"content": b64(at.Data), "filename": at.Name, "disposition": "attachment"})
		}
		if al != nil {
			body["attachments"] = al
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
		if len(e.Cc) > 0 {
			body["cc"] = e.Cc
		}
		if len(e.Bcc) > 0 {
			body["bcc"] = e.Bcc
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"filename": at.Name, "content": b64(at.Data)})
		}
		if al != nil {
			body["attachments"] = al
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
		if len(e.Cc) > 0 {
			body["cc"] = e.Cc
		}
		if len(e.Bcc) > 0 {
			body["bcc"] = e.Bcc
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"filename": at.Name, "fileblob": b64(at.Data), "mimetype": ctype(at)})
		}
		if al != nil {
			body["attachments"] = al
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
		for _, t := range cc {
			tos = append(tos, map[string]string{"email": t.Email, "name": t.Name, "type": "cc"})
		}
		for _, t := range bcc {
			tos = append(tos, map[string]string{"email": t.Email, "name": t.Name, "type": "bcc"})
		}
		m := map[string]any{"from_email": e.FromEmail, "from_name": e.FromName, "to": tos, "subject": e.Subject, "text": text}
		if html != "" {
			m["html"] = html
		}
		if reply != nil {
			m["headers"] = map[string]string{"Reply-To": e.ReplyTo}
		}
		if len(cc) > 0 {
			m["preserve_recipients"] = true // show the Cc header to every recipient
		}
		var al []map[string]string
		for _, at := range atts {
			al = append(al, map[string]string{"type": ctype(at), "name": at.Name, "content": b64(at.Data)})
		}
		if al != nil {
			m["attachments"] = al
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
	req.Header.Set("User-Agent", "Pursae SMTP")
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
