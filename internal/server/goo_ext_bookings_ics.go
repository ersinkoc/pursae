package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Calendar integration for Bookings: a signed .ics download per booking (linked from the booking
// e-mails and My Account) and a token-protected ICS feed of all bookings for the merchant's calendar.

func (s *Server) bkICSToken(purpose string, id int64) string {
	m := hmac.New(sha256.New, []byte("goo-booking-ics|"+s.siteSecret()))
	fmt.Fprintf(m, "%s|%d", purpose, id)
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func (s *Server) bkICSTokenOK(purpose string, id int64, tok string) bool {
	return tok != "" && hmac.Equal([]byte(tok), []byte(s.bkICSToken(purpose, id)))
}

// bkICSURL is the download link of one booking's .ics file.
func (s *Server) bkICSURL(ctx context.Context, b *bkBooking) string {
	return s.links(ctx).HomeURL("/") + "?goo-ajax=booking_ics&booking_id=" + itoa64(b.ID) + "&token=" + url.QueryEscape(s.bkICSToken("booking", b.ID))
}

// bkFeedURL is the merchant's ICS feed URL (all active bookings).
func (s *Server) bkFeedURL(ctx context.Context) string {
	return s.links(ctx).HomeURL("/") + "?goo-ajax=booking_feed&token=" + url.QueryEscape(s.bkICSToken("feed", 0))
}

func bkICSEscape(v string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", "")
	return r.Replace(v)
}

// bkICSFold folds a content line at 75 octets (RFC 5545 3.1).
func bkICSFold(line string) string {
	var b strings.Builder
	n := 0
	for _, r := range line {
		w := len(string(r))
		if n+w > 74 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += w
	}
	return b.String()
}

// bkICSEvents renders the VEVENTs of the bookings into a VCALENDAR.
func (s *Server) bkICS(ctx context.Context, list []*bkBooking) string {
	var out []string
	add := func(l string) { out = append(out, bkICSFold(l)) }
	add("BEGIN:VCALENDAR")
	add("VERSION:2.0")
	add("PRODID:-//Pursae//Bookings//EN")
	add("CALSCALE:GREGORIAN")
	add("METHOD:PUBLISH")
	host := "pursae"
	if u, err := url.Parse(s.links(ctx).HomeURL("/")); err == nil && u.Host != "" {
		host = u.Host
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, b := range list {
		name := "Booking"
		if p, err := s.gooGetProduct(ctx, b.ProductID); err == nil {
			name = p.Name
		}
		add("BEGIN:VEVENT")
		add(fmt.Sprintf("UID:booking-%d@%s", b.ID, host))
		add("DTSTAMP:" + stamp)
		if b.AllDay {
			add("DTSTART;VALUE=DATE:" + b.Start.Format("20060102"))
			add("DTEND;VALUE=DATE:" + b.End.Format("20060102"))
		} else {
			add("DTSTART:" + b.Start.UTC().Format("20060102T150405Z"))
			add("DTEND:" + b.End.UTC().Format("20060102T150405Z"))
		}
		add("SUMMARY:" + bkICSEscape(name))
		desc := fmt.Sprintf("Booking #%d (%s)", b.ID, bkStatusLabel(b.Status))
		if n := bkPersonsTotal(b.Persons); n > 0 {
			desc += fmt.Sprintf(", %d persons", n)
		}
		add("DESCRIPTION:" + bkICSEscape(desc))
		if b.Status == "cancelled" {
			add("STATUS:CANCELLED")
		} else if b.Status == "pending-confirmation" || b.Status == "unpaid" {
			add("STATUS:TENTATIVE")
		} else {
			add("STATUS:CONFIRMED")
		}
		add("END:VEVENT")
	}
	add("END:VCALENDAR")
	return strings.Join(out, "\r\n") + "\r\n"
}

func (s *Server) bkServeICS(c *gin.Context, name string, list []*bkBooking) {
	c.Header("Content-Disposition", `inline; filename="`+name+`"`)
	c.Header("Cache-Control", "private, no-store")
	c.Data(http.StatusOK, "text/calendar; charset=utf-8", []byte(s.bkICS(c.Request.Context(), list)))
}

// bkICSRoute serves /?goo-ajax=booking_ics and booking_feed.
func (s *Server) bkICSRoute(c *gin.Context) {
	ctx := c.Request.Context()
	switch c.Query("goo-ajax") {
	case "booking_ics":
		id, _ := strconv.ParseInt(c.Query("booking_id"), 10, 64)
		b, err := s.bkGet(ctx, id)
		if err != nil || !s.bkICSTokenOK("booking", id, c.Query("token")) {
			c.String(http.StatusNotFound, "Not found")
			return
		}
		s.bkServeICS(c, "booking-"+itoa64(id)+".ics", []*bkBooking{b})
	case "booking_feed":
		if !s.bkICSTokenOK("feed", 0, c.Query("token")) {
			c.String(http.StatusNotFound, "Not found")
			return
		}
		list := s.bkQuery(ctx, `status IN ('confirmed','paid','complete','pending-confirmation','unpaid') AND end_at > now() - interval '30 days' ORDER BY start_at`)
		s.bkServeICS(c, "bookings.ics", list)
	}
}
