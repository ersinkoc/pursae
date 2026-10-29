package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// The consent log: POST /wp-json/gooike/v1/consent records each banner decision. Pages are cached
// and the same for everyone, so gooike.js sends the decision here (the cache never stores
// /wp-json). The visitor is identified only by the random consent ID kept in their consent cookie,
// which the preferences modal shows them; the IP address is truncated or hashed before it is
// stored and the user agent is only kept as a keyed hash.

var gooikeActions = map[string]string{
	"accept_all": "Accept all",
	"reject_all": "Reject all",
	"custom":     "Custom",
	"withdraw":   "Withdrawn / changed",
	"gpc":        "Privacy signal (GPC / DNT)",
}

var gooikeConsentIDRe = regexp.MustCompile(`^[a-f0-9][a-f0-9-]{15,63}$`)

func (s *Server) registerGooikeREST() {
	s.rest.register(gooikeNamespace, "/"+gooikeNamespace+"/consent", map[string]restHandler{http.MethodPost: s.gooikeConsentREST}, nil)
	s.rest.register(gooikeNamespace, "/"+gooikeNamespace+"/geo", map[string]restHandler{http.MethodGet: s.gooikeGeoREST}, nil)
}

// gooikeRESTHidden hides Gooike's routes while the plugin is off.
func (s *Server) gooikeRESTHidden(ns string) bool {
	return ns == gooikeNamespace && !s.gooikeOn()
}

// gooikeLimiter is a small per-address rate limit of the consent endpoint.
var gooikeLimiter = struct {
	sync.Mutex
	window time.Time
	hits   map[string]int
}{hits: map[string]int{}}

func gooikeAllow(key string, limit int) bool {
	gooikeLimiter.Lock()
	defer gooikeLimiter.Unlock()
	if now := time.Now(); now.Sub(gooikeLimiter.window) > time.Minute {
		gooikeLimiter.window, gooikeLimiter.hits = now, map[string]int{}
	}
	gooikeLimiter.hits[key]++
	return gooikeLimiter.hits[key] <= limit
}

// gooikeConsentREST logs one decision.
func (s *Server) gooikeConsentREST(c *gin.Context, r *restRequest) {
	c.Header("Cache-Control", "no-store")
	st := s.gooikeLoad()
	if !st.LogConsent {
		c.JSON(http.StatusOK, gin.H{"logged": false})
		return
	}
	if o := c.GetHeader("Origin"); o != "" && hostOf(o) != "" && !strings.EqualFold(hostOf(o), hostOf("http://"+c.Request.Host)) {
		restErr(c, http.StatusForbidden, "gooike_origin", "Cross-site consent records are not accepted.")
		return
	}
	ip := clientIP(c)
	if !gooikeAllow(gooikeTruncateIP(ip), 30) {
		restErr(c, http.StatusTooManyRequests, "gooike_rate_limited", "Too many consent records from this address.")
		return
	}
	id, _ := r.Str(c, "consent_id")
	id = strings.ToLower(strings.TrimSpace(id))
	if !gooikeConsentIDRe.MatchString(id) {
		restErr(c, http.StatusBadRequest, "gooike_invalid_id", "Invalid consent ID.")
		return
	}
	action, _ := r.Str(c, "action")
	if _, ok := gooikeActions[action]; !ok {
		restErr(c, http.StatusBadRequest, "gooike_invalid_action", "Invalid action.")
		return
	}
	cats := []string{"necessary"}
	var raw []any
	switch v := r.Body["categories"].(type) {
	case []any:
		raw = v
	case string:
		for _, p := range strings.Split(v, ",") {
			raw = append(raw, p)
		}
	}
	for _, v := range raw {
		k, _ := v.(string)
		if k != "necessary" && st.hasCategory(k) && !gooikeContains(cats, k) {
			cats = append(cats, k)
		}
	}
	version, ok := r.Int(c, "policy_version")
	if !ok || version < 1 || version > 1_000_000 {
		version = int64(st.PolicyVersion)
	}
	reg, _ := r.Str(c, "regulation")
	if reg != "gdpr" && reg != "ccpa" && reg != "kvkk" {
		reg = st.Regulation
	}
	page, _ := r.Str(c, "url")
	if !strings.HasPrefix(page, "/") || strings.HasPrefix(page, "//") {
		page = ""
	}
	if len(page) > 255 {
		page = page[:255]
	}
	gpc, _ := r.Bool(c, "gpc")
	var logID int64
	err := s.store.Pool.QueryRow(c, `INSERT INTO gooike_consent_log (consent_id, action, categories, policy_version, regulation, ip_anon, ua_hash, url, gpc)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		id, action, cats, version, reg, s.gooikeAnonIP(st.IPMode, ip), s.gooikeHash("ua", c.Request.UserAgent()), page, gpc).Scan(&logID)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "gooike_db", "The consent could not be recorded.")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"logged": true, "consent_id": id})
}

func gooikeContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// gooikeTruncateIP drops the host part of an address: the last octet of IPv4 (/24), everything
// after the first 48 bits of IPv6.
func gooikeTruncateIP(addr string) string {
	ip := net.ParseIP(strings.TrimSpace(addr))
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return ip.Mask(net.CIDRMask(48, 128)).String()
}

// gooikeAnonIP is the address as stored in the log.
func (s *Server) gooikeAnonIP(mode, addr string) string {
	switch mode {
	case "none":
		return ""
	case "hash":
		if addr == "" {
			return ""
		}
		return "h:" + s.gooikeHash("ip", addr)
	}
	return gooikeTruncateIP(addr)
}

// gooikeHash is a keyed, shortened hash: equal inputs can be matched, the input cannot be read back.
func (s *Server) gooikeHash(kind, v string) string {
	if v == "" {
		return ""
	}
	m := hmac.New(sha256.New, []byte(s.settings.String("auth_secret")+"|gooike|"+kind))
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// gooikeEEA are the countries where the GDPR (or the UK GDPR / Swiss FADP) applies.
var gooikeEEA = map[string]bool{"AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true, "DK": true, "EE": true, "FI": true,
	"FR": true, "DE": true, "GR": true, "HU": true, "IE": true, "IT": true, "LV": true, "LT": true, "LU": true, "MT": true, "NL": true,
	"PL": true, "PT": true, "RO": true, "SK": true, "SI": true, "ES": true, "SE": true, "IS": true, "LI": true, "NO": true, "GB": true,
	"CH": true}

// gooikeCountry reads the visitor's country from the headers CDNs and proxies add.
func gooikeCountry(c *gin.Context) string {
	for _, h := range []string{"CF-IPCountry", "CloudFront-Viewer-Country", "X-Vercel-IP-Country", "Fastly-Geo-Country", "X-Country-Code", "X-Geo-Country"} {
		if v := strings.ToUpper(strings.TrimSpace(c.GetHeader(h))); len(v) == 2 && v != "XX" {
			return v
		}
	}
	return ""
}

// gooikeGeoREST tells gooike.js which regulation applies to the visitor (geo targeting). Without a
// country header every visitor gets the main regulation.
func (s *Server) gooikeGeoREST(c *gin.Context, _ *restRequest) {
	c.Header("Cache-Control", "no-store, private")
	st := s.gooikeLoad()
	country := gooikeCountry(c)
	reg := st.Regulation
	if st.Geo && country != "" {
		switch {
		case gooikeEEA[country]:
			reg = "gdpr"
		case country == "TR":
			reg = "kvkk"
		case country == "US":
			reg = "ccpa"
		default:
			reg = st.GeoOther
		}
	}
	c.JSON(http.StatusOK, gin.H{"country": country, "regulation": reg})
}

// gooikePurge deletes log entries older than the retention period (the daily cron event).
func (s *Server) gooikePurge(ctx context.Context) (int64, error) {
	st := s.gooikeLoad()
	if st.RetentionMonths <= 0 {
		return 0, nil
	}
	tag, err := s.store.Pool.Exec(ctx, `DELETE FROM gooike_consent_log WHERE created_at < now() - make_interval(months => $1)`, st.RetentionMonths)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// gooikeLogEntry is one row of the consent log.
type gooikeLogEntry struct {
	ID            int64
	ConsentID     string
	Action        string
	ActionLabel   string
	Categories    []string
	PolicyVersion int
	Regulation    string
	IP, UAHash    string
	URL           string
	GPC           bool
	Created       time.Time
	When          string
}

// gooikeLogFilter are the list's filters.
type gooikeLogFilter struct {
	Action, Search, From, To string
	Page, PerPage            int
}

func (f gooikeLogFilter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if _, ok := gooikeActions[f.Action]; ok {
		add("action = $%d", f.Action)
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		add("(consent_id ILIKE '%%' || $%[1]d || '%%' OR ip_anon ILIKE '%%' || $%[1]d || '%%' OR url ILIKE '%%' || $%[1]d || '%%')", q)
	}
	if t, err := time.Parse("2006-01-02", f.From); err == nil {
		add("created_at >= $%d", t)
	}
	if t, err := time.Parse("2006-01-02", f.To); err == nil {
		add("created_at < $%d", t.AddDate(0, 0, 1))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func (s *Server) gooikeLogs(ctx context.Context, f gooikeLogFilter) ([]gooikeLogEntry, int, error) {
	where, args := f.where()
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gooike_consent_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id, consent_id, action, categories, policy_version, regulation, ip_anon, ua_hash, url, gpc, created_at FROM gooike_consent_log` + where + ` ORDER BY created_at DESC, id DESC`
	if f.PerPage > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.PerPage, max(f.Page-1, 0)*f.PerPage)
	}
	rows, err := s.store.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []gooikeLogEntry
	for rows.Next() {
		var e gooikeLogEntry
		if err := rows.Scan(&e.ID, &e.ConsentID, &e.Action, &e.Categories, &e.PolicyVersion, &e.Regulation, &e.IP, &e.UAHash, &e.URL, &e.GPC, &e.Created); err != nil {
			return nil, 0, err
		}
		e.ActionLabel = gooikeActions[e.Action]
		e.When = e.Created.In(s.loc()).Format("2006-01-02 15:04:05")
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// gooikeStats are the dashboard's numbers over the last days.
type gooikeStats struct {
	Days                                         int
	Total, Accept, Reject, Custom, Withdraw, GPC int
	AcceptPct, RejectPct, CustomPct              string
	AllTime                                      int
	Series                                       []gooikeDay
	Max                                          int
}

type gooikeDay struct {
	Date                          string
	Label                         string
	Accept, Reject, Custom, Other int
	Total                         int
	AcceptH, RejectH, CustomH     int // bar heights in percent of the busiest day
	OtherH                        int
}

func (s *Server) gooikeStats(ctx context.Context, days int) gooikeStats {
	st := gooikeStats{Days: days}
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM gooike_consent_log`).Scan(&st.AllTime)
	loc := s.loc()
	today := time.Now().In(loc)
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	byDay := map[string]*gooikeDay{}
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		day := &gooikeDay{Date: d.Format("2006-01-02"), Label: d.Format("Jan 2")}
		byDay[day.Date] = day
		st.Series = append(st.Series, *day)
	}
	rows, err := s.store.Pool.Query(ctx, `SELECT created_at, action FROM gooike_consent_log WHERE created_at >= $1`, start)
	if err == nil {
		for rows.Next() {
			var t time.Time
			var a string
			if rows.Scan(&t, &a) != nil {
				continue
			}
			d := byDay[t.In(loc).Format("2006-01-02")]
			if d == nil {
				continue
			}
			st.Total++
			d.Total++
			switch a {
			case "accept_all":
				st.Accept++
				d.Accept++
			case "reject_all":
				st.Reject++
				d.Reject++
			case "custom":
				st.Custom++
				d.Custom++
			case "withdraw":
				st.Withdraw++
				d.Other++
			default:
				st.GPC++
				d.Other++
			}
		}
		rows.Close()
	}
	for i := range st.Series {
		st.Series[i] = *byDay[st.Series[i].Date]
		st.Max = max(st.Max, st.Series[i].Total)
	}
	pct := func(n, d int) int {
		if d == 0 {
			return 0
		}
		return n * 100 / d
	}
	for i := range st.Series {
		d := &st.Series[i]
		d.AcceptH, d.RejectH, d.CustomH, d.OtherH = pct(d.Accept, st.Max), pct(d.Reject, st.Max), pct(d.Custom, st.Max), pct(d.Other, st.Max)
	}
	f := func(n int) string {
		if st.Total == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.0f%%", float64(n)*100/float64(st.Total))
	}
	st.AcceptPct, st.RejectPct, st.CustomPct = f(st.Accept), f(st.Reject), f(st.Custom)
	return st
}
