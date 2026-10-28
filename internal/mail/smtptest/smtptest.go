// Package smtptest is a small in-process SMTP server for tests: it accepts messages (optionally
// over implicit TLS or STARTTLS, with AUTH PLAIN/LOGIN) and records them. It never relays.
package smtptest

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"net"
	"strings"
	"sync"
)

// Message is one accepted message.
type Message struct {
	From, User string
	To         []string
	Data       string
	TLS        bool
}

// Server records what clients send.
type Server struct {
	Addr     string
	User     string // when set, AUTH is advertised and required with Pass
	Pass     string
	TLS      *tls.Config // with Implicit: TLS from the first byte; otherwise STARTTLS is offered
	Implicit bool
	RejectTo string // RCPT TO this address gets 550
	Mechs    string // advertised AUTH mechanisms, "PLAIN LOGIN" when empty

	ln   net.Listener
	mu   sync.Mutex
	msgs []Message
}

// Start listens on 127.0.0.1 and serves until Close.
func (s *Server) Start() error {
	var err error
	if s.Implicit {
		s.ln, err = tls.Listen("tcp", "127.0.0.1:0", s.TLS)
	} else {
		s.ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return err
	}
	s.Addr = s.ln.Addr().String()
	go func() {
		for {
			c, err := s.ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return nil
}

// Host and Port split Addr.
func (s *Server) Host() string { h, _, _ := net.SplitHostPort(s.Addr); return h }
func (s *Server) Port() string { _, p, _ := net.SplitHostPort(s.Addr); return p }

func (s *Server) Close() { _ = s.ln.Close() }

// Messages returns the accepted messages.
func (s *Server) Messages() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.msgs...)
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r, w := bufio.NewReader(c), bufio.NewWriter(c)
	say := func(l string) { _, _ = w.WriteString(l + "\r\n"); _ = w.Flush() }
	read := func() (string, bool) {
		l, err := r.ReadString('\n')
		return strings.TrimRight(l, "\r\n"), err == nil
	}
	say("220 smtptest ESMTP ready")
	var cur Message
	_, cur.TLS = c.(*tls.Conn)
	authed := s.User == ""
	for {
		line, ok := read()
		if !ok {
			return
		}
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			ext := []string{"250-smtptest"}
			if s.TLS != nil && !s.Implicit && !cur.TLS {
				ext = append(ext, "250-STARTTLS")
			}
			if s.User != "" {
				mechs := s.Mechs
				if mechs == "" {
					mechs = "PLAIN LOGIN"
				}
				ext = append(ext, "250-AUTH "+mechs)
			}
			ext = append(ext, "250 8BITMIME")
			for _, e := range ext {
				_, _ = w.WriteString(e + "\r\n")
			}
			_ = w.Flush()
		case strings.HasPrefix(cmd, "HELO"):
			say("250 smtptest")
		case cmd == "STARTTLS" && s.TLS != nil:
			say("220 go ahead")
			tc := tls.Server(c, s.TLS)
			if tc.Handshake() != nil {
				return
			}
			c, cur.TLS = tc, true
			r, w = bufio.NewReader(c), bufio.NewWriter(c)
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == s.User && parts[2] == s.Pass {
				authed, cur.User = true, parts[1]
				say("235 2.7.0 Authentication successful")
			} else {
				say("535 5.7.8 Username and Password not accepted")
			}
		case cmd == "AUTH LOGIN":
			say("334 VXNlcm5hbWU6")
			u, _ := read()
			say("334 UGFzc3dvcmQ6")
			p, _ := read()
			user, _ := base64.StdEncoding.DecodeString(u)
			pass, _ := base64.StdEncoding.DecodeString(p)
			if string(user) == s.User && string(pass) == s.Pass {
				authed, cur.User = true, string(user)
				say("235 2.7.0 Authentication successful")
			} else {
				say("535 5.7.8 Username and Password not accepted")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !authed {
				say("530 5.7.0 Authentication required")
				continue
			}
			cur.From = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			say("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			to := strings.Trim(line[len("RCPT TO:"):], "<> ")
			if s.RejectTo != "" && strings.EqualFold(to, s.RejectTo) {
				say("550 5.1.1 No such user")
				continue
			}
			cur.To = append(cur.To, to)
			say("250 OK")
		case cmd == "DATA":
			say("354 End data with <CR><LF>.<CR><LF>")
			var b strings.Builder
			for {
				l, ok := read()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				b.WriteString(strings.TrimPrefix(l, ".") + "\n")
			}
			cur.Data = b.String()
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			cur = Message{TLS: cur.TLS, User: cur.User}
			say("250 2.0.0 Ok: queued as SMTPTEST1")
		case cmd == "RSET":
			cur = Message{TLS: cur.TLS, User: cur.User}
			say("250 OK")
		case cmd == "QUIT":
			say("221 Bye")
			return
		default:
			say("502 command not implemented")
		}
	}
}
