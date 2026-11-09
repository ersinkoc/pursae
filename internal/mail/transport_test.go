package mail_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"wpgo/internal/mail"
	"wpgo/internal/mail/smtptest"
)

func testTLS(t *testing.T) (server, client *tls.Config) {
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(ts.Close)
	roots := ts.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	return &tls.Config{Certificates: ts.TLS.Certificates}, &tls.Config{RootCAs: roots, ServerName: "example.com"}
}

func TestSMTPEncryptionAndAuth(t *testing.T) {
	srvTLS, cliTLS := testTLS(t)
	raw := []byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: Hi\r\n\r\n.leading dot\r\nbody\r\n")
	cases := []struct {
		name       string
		srv        *smtptest.Server
		enc        string
		autoTLS    bool
		wantTLS    bool
		user, pass string
		wantErr    string
	}{
		{name: "none, no auth", srv: &smtptest.Server{}, enc: "none"},
		{name: "none + auto TLS upgrades", srv: &smtptest.Server{TLS: srvTLS}, enc: "none", autoTLS: true, wantTLS: true},
		{name: "none without auto TLS stays plain", srv: &smtptest.Server{TLS: srvTLS}, enc: "none"},
		{name: "tls (STARTTLS) + auth", srv: &smtptest.Server{TLS: srvTLS, User: "u", Pass: "p@ss"}, enc: "tls", wantTLS: true, user: "u", pass: "p@ss"},
		{name: "ssl (implicit)", srv: &smtptest.Server{TLS: srvTLS, Implicit: true}, enc: "ssl", wantTLS: true},
		{name: "tls required but missing", srv: &smtptest.Server{}, enc: "tls", wantErr: "STARTTLS"},
		{name: "bad password", srv: &smtptest.Server{User: "u", Pass: "right"}, enc: "none", user: "u", pass: "wrong", wantErr: "authentication failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.srv.Start(); err != nil {
				t.Fatal(err)
			}
			defer tc.srv.Close()
			var debug bytes.Buffer
			c := &mail.SMTP{Host: tc.srv.Host(), Port: tc.srv.Port(), Encryption: tc.enc, AutoTLS: tc.autoTLS, Auth: tc.user != "",
				User: tc.user, Pass: tc.pass, TLSConfig: cliTLS, Debug: &debug, Timeout: 5 * time.Second}
			reply, err := c.Send(context.Background(), "a@example.test", []string{"b@example.test"}, raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("send: %v\n%s", err, debug.String())
			}
			msgs := tc.srv.Messages()
			if len(msgs) != 1 || msgs[0].From != "a@example.test" || msgs[0].To[0] != "b@example.test" || !strings.Contains(msgs[0].Data, ".leading dot") {
				t.Fatalf("server got %+v", msgs)
			}
			if msgs[0].TLS != tc.wantTLS {
				t.Errorf("TLS = %v, want %v", msgs[0].TLS, tc.wantTLS)
			}
			if !strings.Contains(reply, "queued as SMTPTEST1") {
				t.Errorf("reply %q", reply)
			}
			tr := debug.String()
			if tc.pass != "" && (strings.Contains(tr, tc.pass) || strings.Contains(tr, base64.StdEncoding.EncodeToString([]byte("\x00"+tc.user+"\x00"+tc.pass)))) {
				t.Errorf("transcript leaks the password:\n%s", tr)
			}
			if !strings.Contains(tr, "C: MAIL FROM:<a@example.test>") || strings.Contains(tr, "body") {
				t.Errorf("transcript:\n%s", tr)
			}
		})
	}
}

func TestSMTPLoginAuthFallback(t *testing.T) {
	srv := &smtptest.Server{User: "u", Pass: "p", Mechs: "LOGIN"}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	var debug bytes.Buffer
	c := &mail.SMTP{Host: srv.Host(), Port: srv.Port(), Encryption: "none", Auth: true, User: "u", Pass: "p", Debug: &debug}
	if _, err := c.Send(context.Background(), "a@example.test", []string{"b@example.test"}, []byte("Subject: x\r\n\r\nx\r\n")); err != nil {
		t.Fatal(err)
	}
	if m := srv.Messages(); len(m) != 1 || m[0].User != "u" {
		t.Fatalf("%+v", m)
	}
	if tr := debug.String(); !strings.Contains(tr, "C: AUTH LOGIN") || !strings.Contains(tr, "[password hidden]") {
		t.Fatalf("transcript:\n%s", tr)
	}
}

func TestMailerTransportHook(t *testing.T) {
	var got mail.Message
	m := &mail.Mailer{From: "Site <w@example.test>", Transport: hook(func(_ context.Context, mm *mail.Mailer, msg mail.Message) error {
		got = msg
		return nil
	})}
	if err := m.Send(context.Background(), mail.Message{To: []string{"x@example.test"}, Subject: "S"}); err != nil {
		t.Fatal(err)
	}
	if got.Subject != "S" {
		t.Fatal("transport not used")
	}
}

type hook func(context.Context, *mail.Mailer, mail.Message) error

func (h hook) Deliver(ctx context.Context, m *mail.Mailer, msg mail.Message) error {
	return h(ctx, m, msg)
}

// apiCase describes what one provider must receive.
type apiCase struct {
	mailer   string
	opts     map[string]string
	path     string
	header   [2]string // a header that must be present (name, substring)
	body     []string  // substrings of the request body
	response string
	respHdr  [2]string
	wantID   string
}

func TestAPIMailers(t *testing.T) {
	cases := []apiCase{
		{mailer: "sendlayer", opts: map[string]string{"api_key": "SL"}, path: "/api/v1/email", header: [2]string{"Authorization", "Bearer SL"},
			body: []string{`"Subject":"Hello"`, `"HTMLContent":"<p>Hi</p>"`, `"email":"to@example.test"`}, response: `{"MessageID":"sl-1"}`, wantID: "sl-1"},
		{mailer: "smtpcom", opts: map[string]string{"api_key": "SC", "channel": "chan"}, path: "/v4/messages", header: [2]string{"Authorization", "Bearer SC"},
			body: []string{`"channel":"chan"`, `"address":"to@example.test"`}, response: `{"status":"success","data":{"message":"msg-2"}}`, wantID: "msg-2"},
		{mailer: "sendinblue", opts: map[string]string{"api_key": "BR"}, path: "/v3/smtp/email", header: [2]string{"Api-Key", "BR"},
			body: []string{`"htmlContent":"<p>Hi</p>"`, `"replyTo":{"email":"reply@example.test"}`}, response: `{"messageId":"<br-3>"}`, wantID: "<br-3>"},
		{mailer: "mailgun", opts: map[string]string{"api_key": "MG", "domain": "mg.example.test", "region": "EU"}, path: "/v3/mg.example.test/messages",
			header: [2]string{"Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte("api:MG"))},
			body:   []string{"to=to%40example.test", "h%3AReply-To=reply%40example.test"}, response: `{"id":"<mg-4>","message":"Queued"}`, wantID: "<mg-4>"},
		{mailer: "sendgrid", opts: map[string]string{"api_key": "SG"}, path: "/v3/mail/send", header: [2]string{"Authorization", "Bearer SG"},
			body: []string{`"personalizations":[{"to":[{"email":"to@example.test"}]}]`, `"type":"text/html"`}, response: ``, respHdr: [2]string{"X-Message-Id", "sg-5"}, wantID: "sg-5"},
		{mailer: "postmark", opts: map[string]string{"server_api_token": "PM", "message_stream": "outbound"}, path: "/email", header: [2]string{"X-Postmark-Server-Token", "PM"},
			body: []string{`"MessageStream":"outbound"`, `"HtmlBody":"<p>Hi</p>"`}, response: `{"MessageID":"pm-6","ErrorCode":0}`, wantID: "pm-6"},
		{mailer: "sparkpost", opts: map[string]string{"api_key": "SP"}, path: "/api/v1/transmissions", header: [2]string{"Authorization", "SP"},
			body: []string{`"recipients":[{"address":{"email":"to@example.test"`}, response: `{"results":{"id":"sp-7"}}`, wantID: "sp-7"},
		{mailer: "amazonses", opts: map[string]string{"client_id": "AKID", "client_secret": "SECRET", "region": "eu-west-1"}, path: "/v2/email/outbound-emails",
			header: [2]string{"Authorization", "AWS4-HMAC-SHA256 Credential=AKID/"}, body: []string{`"ToAddresses":["to@example.test"]`, `"Raw":{"Data":"`},
			response: `{"MessageId":"ses-8"}`, wantID: "ses-8"},
		{mailer: "gmail", opts: map[string]string{"access_token": "GT"}, path: "/gmail/v1/users/me/messages/send", header: [2]string{"Authorization", "Bearer GT"},
			body: []string{`"raw":"`}, response: `{"id":"gm-9"}`, wantID: "gm-9"},
		{mailer: "outlook", opts: map[string]string{"access_token": "OT"}, path: "/v1.0/me/sendMail", header: [2]string{"Content-Type", "text/plain"},
			body: []string{base64.StdEncoding.EncodeToString([]byte("From: "))[:8]}, response: ``},
		{mailer: "zoho", opts: map[string]string{"access_token": "ZT", "account_id": "42", "domain": "eu"}, path: "/api/accounts/42/messages",
			header: [2]string{"Authorization", "Zoho-oauthtoken ZT"}, body: []string{`"mailFormat":"html"`}, response: `{"data":{"messageId":"zo-10"}}`, wantID: "zo-10"},
		{mailer: "elasticemail", opts: map[string]string{"api_key": "EE"}, path: "/v4/emails/transactional", header: [2]string{"X-Elasticemail-Apikey", "EE"},
			body: []string{`"ContentType":"HTML"`}, response: `{"TransactionID":"ee-11","MessageID":"ee-m"}`, wantID: "ee-m"},
		{mailer: "mailjet", opts: map[string]string{"api_key": "MJ", "secret_key": "MS"}, path: "/v3.1/send",
			header: [2]string{"Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte("MJ:MS"))}, body: []string{`"HTMLPart":"<p>Hi</p>"`},
			response: `{"Messages":[{"Status":"success","To":[{"MessageUUID":"mj-12"}]}]}`, wantID: "mj-12"},
		{mailer: "mailersend", opts: map[string]string{"api_key": "MS"}, path: "/v1/email", header: [2]string{"Authorization", "Bearer MS"},
			body: []string{`"html":"<p>Hi</p>"`}, respHdr: [2]string{"X-Message-Id", "ms-13"}, wantID: "ms-13"},
		{mailer: "resend", opts: map[string]string{"api_key": "RS"}, path: "/emails", header: [2]string{"Authorization", "Bearer RS"},
			body: []string{`"from":"\"Site\" <from@example.test>"`, `"reply_to":"reply@example.test"`}, response: `{"id":"rs-14"}`, wantID: "rs-14"},
		{mailer: "smtp2go", opts: map[string]string{"api_key": "S2"}, path: "/v3/email/send", header: [2]string{"X-Smtp2go-Api-Key", "S2"},
			body: []string{`"html_body":"<p>Hi</p>"`}, response: `{"data":{"succeeded":1,"failed":0,"email_id":"s2-15"}}`, wantID: "s2-15"},
		{mailer: "mandrill", opts: map[string]string{"api_key": "MD"}, path: "/api/1.0/messages/send.json", body: []string{`"key":"MD"`, `"type":"to"`},
			response: `[{"email":"to@example.test","status":"sent","_id":"md-16"}]`, wantID: "md-16"},
	}
	for _, tc := range cases {
		t.Run(tc.mailer, func(t *testing.T) {
			var gotPath, gotBody string
			var gotHdr http.Header
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				gotPath, gotBody, gotHdr = r.URL.Path, string(b), r.Header
				if tc.respHdr[0] != "" {
					w.Header().Set(tc.respHdr[0], tc.respHdr[1])
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer ts.Close()
			api := &mail.API{Mailer: tc.mailer, Opts: tc.opts, Base: ts.URL}
			id, err := api.Send(context.Background(), mail.Email{FromName: "Site", FromEmail: "from@example.test", To: []string{"to@example.test"},
				ReplyTo: "reply@example.test", Subject: "Hello", Text: "Hi", HTML: "<p>Hi</p>", Raw: []byte("From: x\r\n\r\nHi")})
			if err != nil {
				t.Fatal(err)
			}
			if gotPath != tc.path {
				t.Errorf("path %q, want %q", gotPath, tc.path)
			}
			if tc.header[0] != "" && !strings.Contains(gotHdr.Get(tc.header[0]), tc.header[1]) {
				t.Errorf("header %s = %q, want %q", tc.header[0], gotHdr.Get(tc.header[0]), tc.header[1])
			}
			for _, b := range tc.body {
				if !strings.Contains(gotBody, b) {
					t.Errorf("body lacks %s:\n%s", b, gotBody)
				}
			}
			if id != tc.wantID {
				t.Errorf("id %q, want %q", id, tc.wantID)
			}
		})
	}
}

// Cc, Bcc and attachments reach every API mailer in its own format (WP Mail SMTP's mailers do the same).
func TestAPIMailersCcBccAttachments(t *testing.T) {
	opts := map[string]map[string]string{
		"sendlayer": {"api_key": "k"}, "smtpcom": {"api_key": "k", "channel": "c"}, "sendinblue": {"api_key": "k"}, "mailgun": {"api_key": "k", "domain": "d"},
		"sendgrid": {"api_key": "k"}, "postmark": {"server_api_token": "k"}, "sparkpost": {"api_key": "k"},
		"amazonses": {"client_id": "a", "client_secret": "s", "region": "eu-west-1"}, "gmail": {"access_token": "t"}, "outlook": {"access_token": "t"},
		"zoho": {"access_token": "t", "account_id": "1"}, "elasticemail": {"api_key": "k"}, "mailjet": {"api_key": "k", "secret_key": "s"},
		"mailersend": {"api_key": "k"}, "resend": {"api_key": "k"}, "smtp2go": {"api_key": "k"}, "mandrill": {"api_key": "k"},
	}
	rawCarried := map[string]bool{"gmail": true, "outlook": true, "amazonses": true} // attachments travel in the raw message
	for _, m := range mail.APIMailers {
		t.Run(m, func(t *testing.T) {
			var gotBody string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
				switch m {
				case "mailjet":
					_, _ = w.Write([]byte(`{"Messages":[{"Status":"success"}]}`))
				case "mandrill":
					_, _ = w.Write([]byte(`[{"status":"sent"}]`))
				default:
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			defer ts.Close()
			api := &mail.API{Mailer: m, Opts: opts[m], Base: ts.URL}
			e := mail.Email{FromName: "Site", FromEmail: "from@example.test", To: []string{"to@example.test"}, Cc: []string{"cc@example.test"},
				Bcc: []string{"bcc@example.test"}, Subject: "Hello", Text: "Hi", Raw: []byte("From: x\r\n\r\nHi"),
				Attachments: []mail.Attachment{{Name: "note.txt", ContentType: "text/plain", Data: []byte("hello attachment")}}}
			_, err := api.Send(context.Background(), e)
			if m == "zoho" {
				if err == nil {
					t.Fatal("zoho sent a message without its attachment")
				}
				e.Attachments = nil
				_, err = api.Send(context.Background(), e)
			}
			if err != nil {
				t.Fatal(err)
			}
			body := gotBody
			switch m {
			case "gmail":
				var v map[string]string
				_ = json.Unmarshal([]byte(gotBody), &v)
				raw, _ := base64.RawURLEncoding.DecodeString(v["raw"])
				body = string(raw)
			case "outlook":
				raw, _ := base64.StdEncoding.DecodeString(gotBody)
				body = string(raw)
			}
			for _, want := range []string{"cc@example.test", "bcc@example.test"} {
				if !strings.Contains(body, want) {
					t.Errorf("body lacks %s:\n%s", want, body)
				}
			}
			if m != "zoho" && !rawCarried[m] && !strings.Contains(body, base64.StdEncoding.EncodeToString([]byte("hello attachment"))) &&
				!strings.Contains(body, "hello attachment") {
				t.Errorf("attachment missing:\n%s", body)
			}
		})
	}
}

func TestAPIErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "mandrill") || strings.HasSuffix(r.URL.Path, "send.json") {
			_, _ = w.Write([]byte(`[{"status":"rejected","reject_reason":"unsigned"}]`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid API key"}`))
	}))
	defer ts.Close()
	e := mail.Email{FromEmail: "f@example.test", To: []string{"t@example.test"}, Subject: "s", Text: "t"}
	_, err := (&mail.API{Mailer: "sendgrid", Opts: map[string]string{"api_key": "bad"}, Base: ts.URL}).Send(context.Background(), e)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("got %v", err)
	}
	_, err = (&mail.API{Mailer: "mandrill", Opts: map[string]string{"api_key": "k"}, Base: ts.URL}).Send(context.Background(), e)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("mandrill: %v", err)
	}
	if _, err := (&mail.API{Mailer: "mailgun", Opts: map[string]string{"api_key": "k"}, Base: ts.URL}).Send(context.Background(), e); err == nil || !strings.Contains(err.Error(), "domain") {
		t.Fatalf("missing domain: %v", err)
	}
}

func TestSignV4(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "https://email.us-east-1.amazonaws.com/v2/email/outbound-emails", nil)
	req.Header.Set("Content-Type", "application/json")
	body := []byte(`{}`)
	mail.SignV4(req, body, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "us-east-1", "ses", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	auth := req.Header.Get("Authorization")
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260102/us-east-1/ses/aws4_request, SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date, Signature="
	if !strings.HasPrefix(auth, want) || len(auth) != len(want)+64 {
		t.Fatalf("authorization %q", auth)
	}
	again, _ := http.NewRequest(http.MethodPost, req.URL.String(), nil)
	again.Header.Set("Content-Type", "application/json")
	mail.SignV4(again, body, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "us-east-1", "ses", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if again.Header.Get("Authorization") != auth {
		t.Fatal("signature is not deterministic")
	}
}

func TestOAuthFlow(t *testing.T) {
	var form url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			form = r.PostForm
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "AT-" + r.PostForm.Get("grant_type"), "refresh_token": "RT", "expires_in": 3600})
		case "/gmail/v1/users/me/profile":
			_, _ = w.Write([]byte(`{"emailAddress":"me@gmail.test"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	o := &mail.OAuth{Mailer: "gmail", ClientID: "cid", ClientSecret: "sec", RedirectURI: "https://site.test/cb", Base: ts.URL}
	u, _ := url.Parse(o.AuthCodeURL("st"))
	if u.Query().Get("client_id") != "cid" || u.Query().Get("access_type") != "offline" || u.Query().Get("scope") != "https://mail.google.com/" {
		t.Fatalf("auth url %s", u)
	}
	// Google's token endpoint path is /token on oauth2.googleapis.com.
	tok, err := o.Exchange(context.Background(), "CODE")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "AT-authorization_code" || tok.RefreshToken != "RT" || form.Get("code") != "CODE" || form.Get("client_secret") != "sec" {
		t.Fatalf("token %+v form %v", tok, form)
	}
	tok, err = o.Refresh(context.Background(), "RT")
	if err != nil || tok.AccessToken != "AT-refresh_token" {
		t.Fatalf("refresh %+v %v", tok, err)
	}
	email, _, err := o.Account(context.Background(), tok.AccessToken)
	if err != nil || email != "me@gmail.test" {
		t.Fatalf("account %q %v", email, err)
	}
}
