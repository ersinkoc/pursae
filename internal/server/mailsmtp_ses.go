package server

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Amazon SES delivery status for Pursae SMTP.
//
// SES publishes bounces, complaints and deliveries to an Amazon SNS topic; the topic's HTTPS
// subscription points at POST /wp-mail-smtp/v1/webhook/amazonses?token=<token>. Unlike the other
// mailers the body is an SNS envelope, and it is only trusted after its signature verifies:
//
//   - SigningCertURL must be https on sns.<region>.amazonaws.com (or .amazonaws.com.cn) and end
//     in .pem, so the certificate can only come from Amazon;
//   - the certificate must be inside its validity period and hold an RSA key;
//   - the signature (SHA1withRSA for SignatureVersion 1, SHA256withRSA for 2) must match the
//     canonical string SNS defines for the message type.
//
// A SubscriptionConfirmation is confirmed by fetching its SubscribeURL, which has to be on the
// same kind of host. Redirects are never followed.

// msSNSMessage is the SNS HTTP(S) envelope.
type msSNSMessage struct {
	Type             string
	MessageID        string `json:"MessageId"`
	Token            string
	TopicArn         string
	Subject          *string
	Message          string
	Timestamp        string
	SignatureVersion string
	Signature        string
	SigningCertURL   string
	SubscribeURL     string
}

var msSNSHostRe = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)

// msSNSURL validates a URL an SNS message points at: https, an Amazon SNS host, default port, no
// credentials.
func msSNSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || !msSNSHostRe.MatchString(strings.ToLower(u.Hostname())) {
		return nil, errors.New("not an https Amazon SNS URL")
	}
	return u, nil
}

// msSNSCanonical is the string SNS signs: the listed fields as "Name\nValue\n", in this order.
func msSNSCanonical(m *msSNSMessage) (string, error) {
	var b strings.Builder
	add := func(k, v string) { b.WriteString(k + "\n" + v + "\n") }
	switch m.Type {
	case "Notification":
		add("Message", m.Message)
		add("MessageId", m.MessageID)
		if m.Subject != nil {
			add("Subject", *m.Subject)
		}
		add("Timestamp", m.Timestamp)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		add("Message", m.Message)
		add("MessageId", m.MessageID)
		add("SubscribeURL", m.SubscribeURL)
		add("Timestamp", m.Timestamp)
		add("Token", m.Token)
		add("TopicArn", m.TopicArn)
		add("Type", m.Type)
	default:
		return "", fmt.Errorf("unknown SNS message type %q", m.Type)
	}
	return b.String(), nil
}

var msSNSClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// msSNSGet fetches an SNS URL (certificate or subscription confirmation). A variable so tests can
// answer without the network; callers validate the URL with msSNSURL first.
var msSNSGet = func(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := msSNSClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SNS answered %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<10))
}

var msSNSCerts = struct {
	sync.Mutex
	m map[string]*x509.Certificate
}{m: map[string]*x509.Certificate{}}

// msSNSCert returns the signing certificate of an SNS message, cached per URL.
func msSNSCert(ctx context.Context, certURL string) (*x509.Certificate, error) {
	u, err := msSNSURL(certURL)
	if err != nil {
		return nil, fmt.Errorf("SigningCertURL: %w", err)
	}
	if !strings.HasSuffix(u.Path, ".pem") {
		return nil, errors.New("SigningCertURL: not a .pem file")
	}
	key := u.String()
	msSNSCerts.Lock()
	cert := msSNSCerts.m[key]
	msSNSCerts.Unlock()
	if cert == nil {
		raw, err := msSNSGet(ctx, key)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(raw)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, errors.New("SigningCertURL: no certificate in the response")
		}
		if cert, err = x509.ParseCertificate(block.Bytes); err != nil {
			return nil, err
		}
		msSNSCerts.Lock()
		if len(msSNSCerts.m) > 32 {
			clear(msSNSCerts.m)
		}
		msSNSCerts.m[key] = cert
		msSNSCerts.Unlock()
	}
	if now := time.Now(); now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		msSNSCerts.Lock()
		delete(msSNSCerts.m, key)
		msSNSCerts.Unlock()
		return nil, errors.New("SNS signing certificate is outside its validity period")
	}
	return cert, nil
}

// msSNSVerify checks the signature of an SNS message.
func msSNSVerify(ctx context.Context, m *msSNSMessage) error {
	canonical, err := msSNSCanonical(m)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) == 0 {
		return errors.New("SNS signature is not base64")
	}
	cert, err := msSNSCert(ctx, m.SigningCertURL)
	if err != nil {
		return err
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("SNS signing certificate has no RSA key")
	}
	switch m.SignatureVersion {
	case "1":
		sum := sha1.Sum([]byte(canonical))
		return rsa.VerifyPKCS1v15(pub, crypto.SHA1, sum[:], sig)
	case "2":
		sum := sha256.Sum256([]byte(canonical))
		return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
	}
	return fmt.Errorf("unsupported SNS SignatureVersion %q", m.SignatureVersion)
}

// msParseSES turns an SES notification (the Message of an SNS Notification) into delivery events.
// Both formats are read: identity notifications ("notificationType") and configuration-set event
// publishing ("eventType").
func msParseSES(message string) []msDeliveryEvent {
	var p struct {
		NotificationType string `json:"notificationType"`
		EventType        string `json:"eventType"`
		Mail             struct {
			MessageID string `json:"messageId"`
		} `json:"mail"`
		Bounce struct {
			BounceType        string `json:"bounceType"`
			BounceSubType     string `json:"bounceSubType"`
			BouncedRecipients []struct {
				EmailAddress   string `json:"emailAddress"`
				DiagnosticCode string `json:"diagnosticCode"`
			} `json:"bouncedRecipients"`
		} `json:"bounce"`
		Complaint struct {
			ComplaintFeedbackType string `json:"complaintFeedbackType"`
			ComplainedRecipients  []struct {
				EmailAddress string `json:"emailAddress"`
			} `json:"complainedRecipients"`
		} `json:"complaint"`
	}
	if json.Unmarshal([]byte(message), &p) != nil || p.Mail.MessageID == "" {
		return nil
	}
	kind := p.NotificationType
	if kind == "" {
		kind = p.EventType
	}
	switch kind {
	case "Delivery":
		return []msDeliveryEvent{{p.Mail.MessageID, "delivered", ""}}
	case "Bounce":
		if p.Bounce.BounceType == "Transient" { // soft bounce: SES keeps retrying or it is an auto-reply
			return nil
		}
		detail := strings.Trim(p.Bounce.BounceType+"/"+p.Bounce.BounceSubType, "/")
		for _, r := range p.Bounce.BouncedRecipients {
			detail += "; " + strings.TrimSpace(r.EmailAddress+" "+r.DiagnosticCode)
		}
		return []msDeliveryEvent{{p.Mail.MessageID, "bounced", truncateRunes(detail, 500)}}
	case "Complaint":
		var who []string
		for _, r := range p.Complaint.ComplainedRecipients {
			who = append(who, r.EmailAddress)
		}
		detail := "Marked as spam"
		if len(who) > 0 {
			detail += " by " + strings.Join(who, ", ")
		}
		if p.Complaint.ComplaintFeedbackType != "" {
			detail += " (" + p.Complaint.ComplaintFeedbackType + ")"
		}
		return []msDeliveryEvent{{p.Mail.MessageID, "complained", truncateRunes(detail, 500)}}
	}
	return nil
}

// msSESWebhook handles one SNS delivery: it verifies the signature, confirms a subscription, and
// returns the delivery events of a notification.
func msSESWebhook(ctx context.Context, body []byte) ([]msDeliveryEvent, error) {
	var m msSNSMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if err := msSNSVerify(ctx, &m); err != nil {
		return nil, err
	}
	switch m.Type {
	case "SubscriptionConfirmation":
		u, err := msSNSURL(m.SubscribeURL)
		if err != nil {
			return nil, fmt.Errorf("SubscribeURL: %w", err)
		}
		_, err = msSNSGet(ctx, u.String())
		return nil, err
	case "Notification":
		return msParseSES(m.Message), nil
	}
	return nil, nil
}
