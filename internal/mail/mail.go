// Package mail sends e-mail for WordPress Go (wp_mail()): SMTP with STARTTLS when configured,
// otherwise .eml files in a directory so development sites never lose a message.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Message is one e-mail. Body is plain text; HTML is optional and sent as multipart/alternative.
type Message struct {
	To      []string
	Subject string
	Body    string
	HTML    string
	ReplyTo string
}

// Mailer delivers messages.
type Mailer struct {
	Host, Port, User, Pass string
	From                   string // "Site Name <wordpress@example.com>"
	Dir                    string // fallback directory for .eml files
}

// Configured reports whether real delivery (SMTP) is set up.
func (m *Mailer) Configured() bool { return m.Host != "" }

// Build renders a message as RFC 5322 bytes.
func (m *Mailer) Build(msg Message) ([]byte, error) {
	if len(msg.To) == 0 {
		return nil, fmt.Errorf("mail: no recipients")
	}
	for _, to := range msg.To {
		if _, err := mail.ParseAddress(to); err != nil {
			return nil, fmt.Errorf("mail: invalid recipient %q", to)
		}
	}
	var b bytes.Buffer
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	domain := "localhost"
	if a, err := mail.ParseAddress(m.From); err == nil {
		if i := strings.LastIndex(a.Address, "@"); i > 0 {
			domain = a.Address[i+1:]
		}
	}
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", m.From)
	h("To", strings.Join(msg.To, ", "))
	if msg.ReplyTo != "" {
		h("Reply-To", msg.ReplyTo)
	}
	h("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id)+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("X-Mailer", "WordPress Go")
	qp := func(s string) string {
		var q bytes.Buffer
		w := quotedprintable.NewWriter(&q)
		_, _ = w.Write([]byte(strings.ReplaceAll(s, "\n", "\r\n")))
		_ = w.Close()
		return q.String()
	}
	if msg.HTML == "" {
		h("Content-Type", "text/plain; charset=UTF-8")
		h("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n" + qp(msg.Body))
		return b.Bytes(), nil
	}
	boundary := "wpgo-" + hex.EncodeToString(id)
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n--" + boundary + "\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" + qp(msg.Body))
	b.WriteString("\r\n--" + boundary + "\r\nContent-Type: text/html; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n" + qp(msg.HTML))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes(), nil
}

// Send delivers a message by SMTP, or writes it to Dir when SMTP is not configured.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
	raw, err := m.Build(msg)
	if err != nil {
		return err
	}
	if !m.Configured() {
		if m.Dir == "" {
			return nil
		}
		if err := os.MkdirAll(m.Dir, 0o755); err != nil {
			return err
		}
		name := time.Now().Format("20060102-150405.000000000") + ".eml"
		return os.WriteFile(filepath.Join(m.Dir, name), raw, 0o644)
	}
	from := m.From
	if a, err := mail.ParseAddress(m.From); err == nil {
		from = a.Address
	}
	var rcpt []string
	for _, to := range msg.To {
		a, _ := mail.ParseAddress(to)
		rcpt = append(rcpt, a.Address)
	}
	addr := net.JoinHostPort(m.Host, m.Port)
	d := net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if m.Port == "465" {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok && m.Port != "465" {
		if err := c.StartTLS(&tls.Config{ServerName: m.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if m.User != "" {
		if err := c.Auth(smtp.PlainAuth("", m.User, m.Pass, m.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range rcpt {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
