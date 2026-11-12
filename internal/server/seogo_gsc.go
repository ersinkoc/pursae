package server

// Pursae SEO › Search Console: connects the site to Google Search Console with the site owner's own
// OAuth client (like Rank Math's / Site Kit's "own app" setup) and shows the search performance of
// the chosen property: clicks, impressions, CTR and position of the last 28 days, top queries and
// top pages. Read-only scope (webmasters.readonly). The client secret and the tokens are stored
// encrypted (msSeal) in their own option, so the settings export never carries them.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	gomail "wpgo/internal/mail"
)

const (
	seogoGSCKey   = "seogo_search_console"
	seogoGSCScope = "https://www.googleapis.com/auth/webmasters.readonly"
	seogoGSCAPI   = "https://www.googleapis.com/webmasters/v3"
	seogoGSCDays  = 28
	seogoGSCCache = time.Hour
)

// seogoGSCBase points the Google URLs (OAuth and API) at a test server when set.
var seogoGSCBase string

var seogoGSCHTTP = &http.Client{Timeout: 20 * time.Second}

// seogoGSC is the stored connection (option seogo_search_console).
type seogoGSC struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"` // sealed
	AccessToken  string `json:"access_token"`  // sealed
	RefreshToken string `json:"refresh_token"` // sealed
	Expires      int64  `json:"expires"`
	Property     string `json:"property"` // e.g. "sc-domain:example.com" or "https://example.com/"
	State        string `json:"state"`    // pending authorization: random state, user and expiry
	StateUser    int64  `json:"state_user"`
	StateExp     int64  `json:"state_exp"`
	ConnectedBy  int64  `json:"connected_by"`
}

func (s *Server) seogoGSCLoad() *seogoGSC {
	g := &seogoGSC{}
	if raw := s.settings.Get(seogoGSCKey); raw != nil {
		b, _ := json.Marshal(raw)
		_ = json.Unmarshal(b, g)
	}
	return g
}

func (s *Server) seogoGSCSave(ctx context.Context, g *seogoGSC) error {
	b, _ := json.Marshal(g)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	seogoGSCReport.drop()
	return s.settings.Set(ctx, map[string]any{seogoGSCKey: m})
}

func (s *Server) seogoGSCRedirectURI() string {
	return strings.TrimRight(s.settings.String("home"), "/") + "/wp-admin/seogo?page=search-console"
}

func (s *Server) seogoGSCOAuth(g *seogoGSC) *gomail.OAuth {
	return &gomail.OAuth{Mailer: "google", Scope: seogoGSCScope, ClientID: g.ClientID, ClientSecret: s.msReveal(g.ClientSecret),
		RedirectURI: s.seogoGSCRedirectURI(), Client: seogoGSCHTTP, Base: seogoGSCBase}
}

func seogoGSCURL(def string) string {
	if seogoGSCBase == "" {
		return def
	}
	u, err := url.Parse(def)
	if err != nil {
		return def
	}
	return strings.TrimRight(seogoGSCBase, "/") + u.RequestURI()
}

// seogoGSCToken is a valid access token, refreshed (and stored) when it is about to expire.
func (s *Server) seogoGSCToken(ctx context.Context, g *seogoGSC) (string, error) {
	if g.RefreshToken == "" {
		return "", fmt.Errorf("Search Console is not connected")
	}
	if g.AccessToken != "" && time.Now().Add(time.Minute).Unix() < g.Expires {
		return s.msReveal(g.AccessToken), nil
	}
	tok, err := s.seogoGSCOAuth(g).Refresh(ctx, s.msReveal(g.RefreshToken))
	if err != nil {
		return "", err
	}
	g.AccessToken, g.RefreshToken, g.Expires = s.msSeal(tok.AccessToken), s.msSeal(tok.RefreshToken), tok.Expiry.Unix()
	if err := s.seogoGSCSave(ctx, g); err != nil {
		slog.Error("seogo: saving Search Console token", "err", err)
	}
	return tok.AccessToken, nil
}

// seogoGSCCall calls the Search Console API and decodes its JSON answer into out.
func (s *Server) seogoGSCCall(ctx context.Context, g *seogoGSC, method, path string, in, out any) error {
	tok, err := s.seogoGSCToken(ctx, g)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, seogoGSCURL(seogoGSCAPI+path), body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := seogoGSCHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("Search Console answered HTTP %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(raw, out)
}

// seogoGSCSites lists the properties the connected account may read.
func (s *Server) seogoGSCSites(ctx context.Context, g *seogoGSC) ([]string, error) {
	var v struct {
		SiteEntry []struct {
			SiteURL         string `json:"siteUrl"`
			PermissionLevel string `json:"permissionLevel"`
		} `json:"siteEntry"`
	}
	if err := s.seogoGSCCall(ctx, g, http.MethodGet, "/sites", nil, &v); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range v.SiteEntry {
		if e.PermissionLevel != "siteUnverifiedUser" {
			out = append(out, e.SiteURL)
		}
	}
	return out, nil
}

// seogoGSCRow is one row of a Search Analytics answer.
type seogoGSCRow struct {
	Keys        []string `json:"keys"`
	Clicks      float64  `json:"clicks"`
	Impressions float64  `json:"impressions"`
	CTR         float64  `json:"ctr"`
	Position    float64  `json:"position"`
}

func (r seogoGSCRow) Key() string {
	if len(r.Keys) == 0 {
		return ""
	}
	return r.Keys[0]
}
func (r seogoGSCRow) CTRPct() string { return strconv.FormatFloat(r.CTR*100, 'f', 1, 64) + "%" }
func (r seogoGSCRow) Pos() string    { return strconv.FormatFloat(r.Position, 'f', 1, 64) }
func (r seogoGSCRow) ClicksN() int64 { return int64(r.Clicks) }
func (r seogoGSCRow) ImprN() int64   { return int64(r.Impressions) }

// seogoGSCData is the performance report of the screen.
type seogoGSCData struct {
	From, To string
	Totals   seogoGSCRow
	Queries  []seogoGSCRow
	Pages    []seogoGSCRow
	Fetched  time.Time
}

// seogoGSCReport caches the report (one property) for an hour.
var seogoGSCReport = &seogoGSCCacheT{}

type seogoGSCCacheT struct {
	mu   sync.Mutex
	key  string
	data *seogoGSCData
}

func (c *seogoGSCCacheT) drop() { c.mu.Lock(); c.data = nil; c.mu.Unlock() }

func (s *Server) seogoGSCPerformance(ctx context.Context, g *seogoGSC, fresh bool) (*seogoGSCData, error) {
	seogoGSCReport.mu.Lock()
	if !fresh && seogoGSCReport.data != nil && seogoGSCReport.key == g.Property && time.Since(seogoGSCReport.data.Fetched) < seogoGSCCache {
		d := seogoGSCReport.data
		seogoGSCReport.mu.Unlock()
		return d, nil
	}
	seogoGSCReport.mu.Unlock()
	// Search Console data lags about two days behind.
	end := time.Now().UTC().AddDate(0, 0, -2)
	d := &seogoGSCData{From: end.AddDate(0, 0, -seogoGSCDays+1).Format("2006-01-02"), To: end.Format("2006-01-02"), Fetched: time.Now()}
	path := "/sites/" + url.PathEscape(g.Property) + "/searchAnalytics/query"
	query := func(dim string, limit int) ([]seogoGSCRow, error) {
		in := map[string]any{"startDate": d.From, "endDate": d.To, "rowLimit": limit}
		if dim != "" {
			in["dimensions"] = []string{dim}
		}
		var v struct {
			Rows []seogoGSCRow `json:"rows"`
		}
		err := s.seogoGSCCall(ctx, g, http.MethodPost, path, in, &v)
		return v.Rows, err
	}
	tot, err := query("", 1)
	if err != nil {
		return nil, err
	}
	if len(tot) > 0 {
		d.Totals = tot[0]
	}
	if d.Queries, err = query("query", 10); err != nil {
		return nil, err
	}
	if d.Pages, err = query("page", 10); err != nil {
		return nil, err
	}
	seogoGSCReport.mu.Lock()
	seogoGSCReport.key, seogoGSCReport.data = g.Property, d
	seogoGSCReport.mu.Unlock()
	return d, nil
}

// seogoGSCGet handles the GET actions of the Search Console screen (connect, the OAuth callback);
// true when it answered.
func (s *Server) seogoGSCGet(c *gin.Context) bool {
	ctx := c.Request.Context()
	switch {
	case c.Query("action") == "connect":
		if !s.checkNonce(c, "seogo-search-console") {
			s.denyNonce(c)
			return true
		}
		g := s.seogoGSCLoad()
		if g.ClientID == "" || g.ClientSecret == "" {
			s.seogoBack(c, "search-console", "error", "Enter the OAuth client ID and secret first.")
			return true
		}
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		g.State, g.StateUser, g.StateExp = hex.EncodeToString(b), currentUser(c).ID, time.Now().Add(10*time.Minute).Unix()
		if err := s.seogoGSCSave(ctx, g); err != nil {
			s.seogoBack(c, "search-console", "error", err.Error())
			return true
		}
		c.Redirect(http.StatusFound, s.seogoGSCOAuth(g).AuthCodeURL(g.State))
		return true
	case c.Query("state") != "" || c.Query("code") != "" || c.Query("error") != "":
		g := s.seogoGSCLoad()
		state := c.Query("state")
		ok := g.State != "" && subtle.ConstantTimeCompare([]byte(state), []byte(g.State)) == 1 &&
			g.StateUser == currentUser(c).ID && time.Now().Unix() < g.StateExp
		g.State, g.StateUser, g.StateExp = "", 0, 0 // one use
		_ = s.seogoGSCSave(ctx, g)
		if !ok {
			s.seogoBack(c, "search-console", "error", "The authorization could not be verified (expired or not started here). Please connect again.")
			return true
		}
		if e := c.Query("error"); e != "" {
			s.seogoBack(c, "search-console", "error", "Google did not grant access: "+e)
			return true
		}
		tok, err := s.seogoGSCOAuth(g).Exchange(ctx, c.Query("code"))
		if err != nil {
			s.seogoBack(c, "search-console", "error", err.Error())
			return true
		}
		if tok.RefreshToken == "" {
			s.seogoBack(c, "search-console", "error", "Google sent no refresh token. Remove the app's access in your Google account and connect again.")
			return true
		}
		g.AccessToken, g.RefreshToken, g.Expires, g.ConnectedBy = s.msSeal(tok.AccessToken), s.msSeal(tok.RefreshToken), tok.Expiry.Unix(), currentUser(c).ID
		// Preselect the property of this site when the account has it.
		if g.Property == "" {
			if sites, err := s.seogoGSCSites(ctx, g); err == nil {
				g.Property = seogoGSCMatch(sites, s.settings.String("home"))
			}
		}
		if err := s.seogoGSCSave(ctx, g); err != nil {
			s.seogoBack(c, "search-console", "error", err.Error())
			return true
		}
		s.seogoBack(c, "search-console", "updated", "Connected to Google Search Console.")
		return true
	}
	return false
}

// seogoGSCMatch picks the property of home: the URL-prefix property, else the domain property.
func seogoGSCMatch(sites []string, home string) string {
	h, err := url.Parse(home)
	if err != nil || h.Host == "" {
		return ""
	}
	prefix := strings.TrimRight(home, "/") + "/"
	domain := "sc-domain:" + strings.TrimPrefix(h.Hostname(), "www.")
	for _, s := range sites {
		if s == prefix {
			return s
		}
	}
	for _, s := range sites {
		if s == domain {
			return s
		}
	}
	return ""
}

// seogoGSCPost handles the forms of the Search Console screen.
func (s *Server) seogoGSCPost(c *gin.Context) (string, error) {
	ctx := c.Request.Context()
	g := s.seogoGSCLoad()
	switch c.PostForm("action") {
	case "gsc_client":
		id := strings.TrimSpace(c.PostForm("client_id"))
		if id != g.ClientID {
			// Another client: the old tokens belong to the old one.
			g.AccessToken, g.RefreshToken, g.Expires, g.Property = "", "", 0, ""
		}
		g.ClientID = id
		if sec := strings.TrimSpace(c.PostForm("client_secret")); sec != "" {
			g.ClientSecret = s.msSeal(sec)
		}
		return "Settings saved.", s.seogoGSCSave(ctx, g)
	case "gsc_property":
		p := strings.TrimSpace(c.PostForm("property"))
		sites, err := s.seogoGSCSites(ctx, g)
		if err != nil {
			return "", err
		}
		found := false
		for _, s := range sites {
			found = found || s == p
		}
		if !found {
			return "", fmt.Errorf("the connected account cannot read this property")
		}
		g.Property = p
		return "Property saved.", s.seogoGSCSave(ctx, g)
	case "gsc_refresh":
		seogoGSCReport.drop()
		return "The report was reloaded.", nil
	case "gsc_disconnect":
		if g.RefreshToken != "" { // best effort: revoke the grant at Google
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, seogoGSCURL("https://oauth2.googleapis.com/revoke"),
				strings.NewReader(url.Values{"token": {s.msReveal(g.RefreshToken)}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if resp, err := seogoGSCHTTP.Do(req); err == nil {
				resp.Body.Close()
			}
		}
		g.AccessToken, g.RefreshToken, g.Expires, g.Property, g.ConnectedBy = "", "", 0, "", 0
		return "Disconnected from Google Search Console.", s.seogoGSCSave(ctx, g)
	}
	return "Nothing changed.", nil
}

// seogoGSCView fills the screen's data.
func (s *Server) seogoGSCView(ctx context.Context, d *seogoPage) {
	g := s.seogoGSCLoad()
	d.Data["GSCRedirect"] = s.seogoGSCRedirectURI()
	d.Data["GSCClientID"] = g.ClientID
	d.Data["GSCHasSecret"] = g.ClientSecret != ""
	d.Data["GSCConnected"] = g.RefreshToken != ""
	d.Data["GSCProperty"] = g.Property
	if g.RefreshToken == "" {
		return
	}
	sites, err := s.seogoGSCSites(ctx, g)
	if err != nil {
		d.Data["GSCError"] = err.Error()
		return
	}
	d.Data["GSCSites"] = sites
	if g.Property == "" {
		return
	}
	rep, err := s.seogoGSCPerformance(ctx, g, false)
	if err != nil {
		d.Data["GSCError"] = err.Error()
		return
	}
	d.Data["GSCReport"] = rep
}
