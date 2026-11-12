package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	gomail "wpgo/internal/mail"
)

// Open/click tracking and delivery status for Pursae SMTP.
//
// Tracking is off by default and needs the email log (the counters live on the log row). When on,
// the HTML part of each e-mail gets a 1x1 pixel and its http(s) links are routed through
// /wp-mail-smtp/v1/t/c/<id>?u=<url>&s=<signature>. The signature binds the id and the URL, so
// the redirect cannot be abused as an open redirect. Visits that send Do Not Track or Global
// Privacy Control are redirected/served but never counted.
//
// Delivery status (delivered, bounced, complained) comes from the mailer's webhook:
// POST /wp-mail-smtp/v1/webhook/<postmark|sendgrid|mailgun|amazonses>?token=<token>; the token is
// shown under Settings > Email Log and derived from the site secret. Amazon SES arrives through
// Amazon SNS and is signature-verified on top of the token (mailsmtp_ses.go).

const msTrackBase = "/wp-mail-smtp/v1"

// msTrackPixel is a transparent 1x1 GIF.
var msTrackPixel, _ = hex.DecodeString("47494638396101000100800000000000ffffff21f90401000000002c00000000010001000002024401003b")

func (s *Server) msSign(parts ...string) string {
	m := hmac.New(sha256.New, []byte("pursae-smtp-track\x00"+s.siteSecret()))
	for _, p := range parts {
		m.Write([]byte(p))
		m.Write([]byte{0})
	}
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// msWebhookToken is the secret part of the bounce webhook URLs.
func (s *Server) msWebhookToken() string { return s.msSign("webhook") }

var msHrefRe = regexp.MustCompile(`(?i)(<a\s[^>]*?\bhref\s*=\s*)(?:"([^"]*)"|'([^']*)')`)

// msApplyTracking rewrites the links and appends the open pixel of an HTML message.
func (s *Server) msApplyTracking(home, id string, opens, clicks bool, msg gomail.Message) gomail.Message {
	h := msg.HTML
	if h == "" {
		return msg
	}
	if clicks {
		h = msHrefRe.ReplaceAllStringFunc(h, func(m string) string {
			sub := msHrefRe.FindStringSubmatch(m)
			raw := sub[2]
			if raw == "" {
				raw = sub[3]
			}
			target := strings.TrimSpace(html.UnescapeString(raw))
			if l := strings.ToLower(target); !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
				return m
			}
			if strings.HasPrefix(target, home+msTrackBase+"/t/") {
				return m
			}
			link := home + msTrackBase + "/t/c/" + id + "?u=" + url.QueryEscape(target) + "&s=" + s.msSign("c", id, target)
			return sub[1] + `"` + html.EscapeString(link) + `"`
		})
	}
	if opens {
		pix := `<img src="` + html.EscapeString(home+msTrackBase+"/t/o/"+id+".gif") + `" width="1" height="1" alt="" style="display:none" />`
		if i := strings.LastIndex(strings.ToLower(h), "</body>"); i >= 0 {
			h = h[:i] + pix + h[i:]
		} else {
			h += pix
		}
	}
	msg.HTML = h
	return msg
}

// msTrackMessage returns the message to send and its tracking id ("" when tracking is off).
func (s *Server) msTrackMessage(ctx context.Context, o *msOptions, msg gomail.Message) (gomail.Message, string) {
	if !o.Logs.Enabled || (!o.Logs.TrackOpens && !o.Logs.TrackClicks) || msg.HTML == "" {
		return msg, ""
	}
	home := strings.TrimRight(s.links(ctx).Home, "/")
	if home == "" {
		return msg, ""
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return msg, ""
	}
	id := hex.EncodeToString(b)
	return s.msApplyTracking(home, id, o.Logs.TrackOpens, o.Logs.TrackClicks, msg), id
}

func msDoNotTrack(c *gin.Context) bool {
	return c.GetHeader("DNT") == "1" || c.GetHeader("Sec-GPC") == "1"
}

func (s *Server) registerMailSMTPTrack(r *gin.Engine) {
	r.GET(msTrackBase+"/t/o/:id", s.msTrackOpen)
	r.GET(msTrackBase+"/t/c/:id", s.msTrackClick)
	r.POST(msTrackBase+"/webhook/:mailer", s.msWebhook)
}

func (s *Server) msTrackOpen(c *gin.Context) {
	id := strings.TrimSuffix(c.Param("id"), ".gif")
	if s.mailSMTPOn() && s.msLoad().Logs.TrackOpens && !msDoNotTrack(c) && len(id) == 24 {
		_, _ = s.store.Pool.Exec(c.Request.Context(), `UPDATE wpmailsmtp_emails_log SET open_count = open_count + 1,
			first_opened_at = COALESCE(first_opened_at, now()) WHERE track_id = $1`, id)
	}
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate, private")
	c.Data(http.StatusOK, "image/gif", msTrackPixel)
}

func (s *Server) msTrackClick(c *gin.Context) {
	id, target := c.Param("id"), c.Query("u")
	l := strings.ToLower(target)
	if !s.mailSMTPOn() || !(strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")) ||
		!hmac.Equal([]byte(c.Query("s")), []byte(s.msSign("c", id, target))) {
		c.Status(http.StatusNotFound)
		return
	}
	if s.msLoad().Logs.TrackClicks && !msDoNotTrack(c) {
		_, _ = s.store.Pool.Exec(c.Request.Context(), `UPDATE wpmailsmtp_emails_log SET click_count = click_count + 1 WHERE track_id = $1`, id)
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(http.StatusFound, target)
}

// msDeliveryEvent is one delivery notification from a mailer.
type msDeliveryEvent struct {
	MessageID, Status, Detail string // Status: delivered, bounced, complained
}

// msParseWebhook turns a mailer's webhook body into delivery events.
func msParseWebhook(mailer string, body []byte) []msDeliveryEvent {
	var out []msDeliveryEvent
	switch mailer {
	case "postmark":
		var p struct {
			RecordType  string
			MessageID   string
			Type        string
			Description string
			Details     string
			Email       string
		}
		if json.Unmarshal(body, &p) != nil || p.MessageID == "" {
			return nil
		}
		switch p.RecordType {
		case "Delivery":
			out = append(out, msDeliveryEvent{p.MessageID, "delivered", ""})
		case "Bounce":
			out = append(out, msDeliveryEvent{p.MessageID, "bounced", strings.TrimSpace(p.Type + ": " + p.Description)})
		case "SpamComplaint":
			out = append(out, msDeliveryEvent{p.MessageID, "complained", "Marked as spam by " + p.Email})
		}
	case "sendgrid":
		var raw []map[string]any
		if json.Unmarshal(body, &raw) != nil {
			return nil
		}
		for _, e := range raw {
			id, _ := e["sg_message_id"].(string)
			id, _, _ = strings.Cut(id, ".") // "<id>.filter0001..." -> "<id>"
			ev, _ := e["event"].(string)
			reason, _ := e["reason"].(string)
			if id == "" {
				continue
			}
			switch ev {
			case "delivered":
				out = append(out, msDeliveryEvent{id, "delivered", ""})
			case "bounce", "dropped":
				out = append(out, msDeliveryEvent{id, "bounced", reason})
			case "spamreport":
				out = append(out, msDeliveryEvent{id, "complained", "Marked as spam"})
			}
		}
	case "mailgun":
		var p struct {
			EventData struct {
				Event    string
				Severity string
				Message  struct {
					Headers struct {
						MessageID string `json:"message-id"`
					}
				}
				DeliveryStatus struct{ Description, Message string } `json:"delivery-status"`
			} `json:"event-data"`
		}
		if json.Unmarshal(body, &p) != nil {
			return nil
		}
		e, id := p.EventData, p.EventData.Message.Headers.MessageID
		if id == "" {
			return nil
		}
		id = "<" + strings.Trim(id, "<>") + ">"
		switch {
		case e.Event == "delivered":
			out = append(out, msDeliveryEvent{id, "delivered", ""})
		case e.Event == "failed" && e.Severity == "permanent":
			out = append(out, msDeliveryEvent{id, "bounced", strings.TrimSpace(e.DeliveryStatus.Description + " " + e.DeliveryStatus.Message)})
		case e.Event == "complained":
			out = append(out, msDeliveryEvent{id, "complained", "Marked as spam"})
		}
	}
	return out
}

func (s *Server) msWebhook(c *gin.Context) {
	if !s.mailSMTPOn() || !hmac.Equal([]byte(c.Query("token")), []byte(s.msWebhookToken())) {
		c.Status(http.StatusNotFound)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	var events []msDeliveryEvent
	if mailer := c.Param("mailer"); mailer == "amazonses" {
		// Amazon SNS envelope: trusted only after its signature verifies (mailsmtp_ses.go).
		ev, err := msSESWebhook(c.Request.Context(), body)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "SNS message rejected"})
			return
		}
		events = ev
	} else {
		events = msParseWebhook(mailer, body)
	}
	n := 0
	for _, e := range events {
		tag, err := s.store.Pool.Exec(c.Request.Context(), `UPDATE wpmailsmtp_emails_log SET delivery_status = $2, delivery_detail = $3, delivery_at = now()
			WHERE message_id <> '' AND (message_id = $1 OR message_id = '<' || trim(both '<>' from $1) || '>' OR message_id LIKE $1 || '.%')
			AND NOT (delivery_status IN ('bounced','complained') AND $2 = 'delivered')`, e.MessageID, e.Status, e.Detail)
		if err == nil {
			n += int(tag.RowsAffected())
		}
	}
	c.JSON(http.StatusOK, gin.H{"updated": n})
}
