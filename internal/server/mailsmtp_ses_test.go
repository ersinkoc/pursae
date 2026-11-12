package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

// snsTestSigner signs SNS envelopes with a throwaway certificate and serves it through msSNSGet.
type snsTestSigner struct {
	key     *rsa.PrivateKey
	certPEM []byte
	fetched []string
}

func newSNSTestSigner(t *testing.T, notAfter time.Time) *snsTestSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.amazonaws.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sg := &snsTestSigner{key: key, certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
	prev := msSNSGet
	msSNSGet = func(_ context.Context, u string) ([]byte, error) {
		sg.fetched = append(sg.fetched, u)
		if strings.HasSuffix(u, ".pem") {
			return sg.certPEM, nil
		}
		return []byte("<ConfirmSubscriptionResponse/>"), nil
	}
	clearCerts := func() {
		msSNSCerts.Lock()
		clear(msSNSCerts.m)
		msSNSCerts.Unlock()
	}
	clearCerts()
	t.Cleanup(func() { msSNSGet = prev; clearCerts() })
	return sg
}

func (sg *snsTestSigner) sign(t *testing.T, m msSNSMessage) []byte {
	t.Helper()
	canonical, err := msSNSCanonical(&m)
	if err != nil {
		t.Fatal(err)
	}
	var sig []byte
	if m.SignatureVersion == "2" {
		sum := sha256.Sum256([]byte(canonical))
		sig, err = rsa.SignPKCS1v15(rand.Reader, sg.key, crypto.SHA256, sum[:])
	} else {
		sum := sha1.Sum([]byte(canonical))
		sig, err = rsa.SignPKCS1v15(rand.Reader, sg.key, crypto.SHA1, sum[:])
	}
	if err != nil {
		t.Fatal(err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	b, _ := json.Marshal(m)
	return b
}

func TestMailSMTPSESWebhookSNS(t *testing.T) {
	const certURL = "https://sns.eu-west-1.amazonaws.com/SimpleNotificationService-abc.pem"
	const topic = "arn:aws:sns:eu-west-1:123456789012:ses-events"
	ctx := context.Background()
	sg := newSNSTestSigner(t, time.Now().Add(time.Hour))
	bounce := `{"notificationType":"Bounce","mail":{"messageId":"ses-42"},"bounce":{"bounceType":"Permanent","bounceSubType":"General","bouncedRecipients":[{"emailAddress":"x@example.test","diagnosticCode":"smtp; 550 5.1.1 user unknown"}]}}`
	base := msSNSMessage{Type: "Notification", MessageID: "m-1", TopicArn: topic, Message: bounce,
		Timestamp: "2026-11-11T10:00:00.000Z", SignatureVersion: "1", SigningCertURL: certURL}

	for _, ver := range []string{"1", "2"} {
		m := base
		m.SignatureVersion = ver
		ev, err := msSESWebhook(ctx, sg.sign(t, m))
		if err != nil || len(ev) != 1 || ev[0].MessageID != "ses-42" || ev[0].Status != "bounced" || !strings.Contains(ev[0].Detail, "550 5.1.1") {
			t.Fatalf("signature version %s: %+v %v", ver, ev, err)
		}
	}
	if len(sg.fetched) != 1 {
		t.Fatalf("certificate fetched %d times, want 1 (cached)", len(sg.fetched))
	}

	// A subject is part of the signed string when present.
	subj := "Amazon SES Email Event Notification"
	m := base
	m.Subject = &subj
	if _, err := msSESWebhook(ctx, sg.sign(t, m)); err != nil {
		t.Fatalf("with subject: %v", err)
	}

	// Tampering with the signed message breaks the signature.
	var tampered msSNSMessage
	_ = json.Unmarshal(sg.sign(t, base), &tampered)
	tampered.Message = strings.Replace(bounce, "ses-42", "ses-43", 1)
	b, _ := json.Marshal(tampered)
	if ev, err := msSESWebhook(ctx, b); err == nil || ev != nil {
		t.Fatal("a tampered message verified")
	}

	// The certificate may only come from an https Amazon SNS host, as a .pem file.
	for _, bad := range []string{
		"http://sns.eu-west-1.amazonaws.com/x.pem",
		"https://evil.test/x.pem",
		"https://sns.eu-west-1.amazonaws.com.evil.test/x.pem",
		"https://evil.test/sns.eu-west-1.amazonaws.com/x.pem",
		"https://user@sns.eu-west-1.amazonaws.com/x.pem",
		"https://sns.eu-west-1.amazonaws.com:8443/x.pem",
		"https://s3.amazonaws.com/bucket/x.pem",
		"https://sns.eu-west-1.amazonaws.com/x.txt",
		"",
	} {
		n := len(sg.fetched)
		m := base
		m.SigningCertURL = bad
		if _, err := msSESWebhook(ctx, sg.sign(t, m)); err == nil {
			t.Fatalf("certificate URL %q accepted", bad)
		}
		if len(sg.fetched) != n {
			t.Fatalf("certificate URL %q was fetched", bad)
		}
	}

	// Subscription confirmation: the SubscribeURL is fetched once, and only on an SNS host.
	sub := msSNSMessage{Type: "SubscriptionConfirmation", MessageID: "m-2", TopicArn: topic, Token: "tok", Message: "You have chosen to subscribe",
		SubscribeURL: "https://sns.eu-west-1.amazonaws.com/?Action=ConfirmSubscription&Token=tok", Timestamp: base.Timestamp, SignatureVersion: "1", SigningCertURL: certURL}
	n := len(sg.fetched)
	if _, err := msSESWebhook(ctx, sg.sign(t, sub)); err != nil || len(sg.fetched) != n+1 || sg.fetched[n] != sub.SubscribeURL {
		t.Fatalf("subscription not confirmed: %v %v", err, sg.fetched)
	}
	sub.SubscribeURL = "https://evil.test/?Action=ConfirmSubscription"
	n = len(sg.fetched)
	if _, err := msSESWebhook(ctx, sg.sign(t, sub)); err == nil || len(sg.fetched) != n {
		t.Fatal("a SubscribeURL outside Amazon SNS was fetched")
	}

	// Other SES payloads.
	for _, c := range []struct{ msg, status string }{
		{`{"eventType":"Delivery","mail":{"messageId":"a"}}`, "delivered"},
		{`{"notificationType":"Complaint","mail":{"messageId":"a"},"complaint":{"complainedRecipients":[{"emailAddress":"y@example.test"}],"complaintFeedbackType":"abuse"}}`, "complained"},
		{`{"notificationType":"Bounce","mail":{"messageId":"a"},"bounce":{"bounceType":"Transient"}}`, ""},
		{`{"notificationType":"Bounce","bounce":{"bounceType":"Permanent"}}`, ""},
	} {
		ev := msParseSES(c.msg)
		if (c.status == "" && len(ev) != 0) || (c.status != "" && (len(ev) != 1 || ev[0].Status != c.status)) {
			t.Fatalf("%s -> %+v", c.msg, ev)
		}
	}
}

func TestMailSMTPSESExpiredCertificate(t *testing.T) {
	sg := newSNSTestSigner(t, time.Now().Add(-time.Minute))
	m := msSNSMessage{Type: "Notification", MessageID: "m", TopicArn: "t", Message: "{}", Timestamp: "x", SignatureVersion: "1",
		SigningCertURL: "https://sns.us-east-1.amazonaws.com/c.pem"}
	if _, err := msSESWebhook(context.Background(), sg.sign(t, m)); err == nil {
		t.Fatal("an expired signing certificate was accepted")
	}
}
