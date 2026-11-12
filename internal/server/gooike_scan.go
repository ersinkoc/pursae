package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"
)

// The cookie scanner: a simple, server-side scan. It requests the home page (and any extra paths
// the admin lists) through the site itself, as an anonymous visitor and bypassing the page cache,
// and lists the cookies the responses set. JavaScript cookies cannot be seen on the server, so the
// page's HTML and the script manager are also checked for known services, whose cookies are known.

type gooikeScanItem struct {
	gooikeCookieRow
	Source   string
	Declared bool
}

type gooikeScanResult struct {
	At       string
	Pages    []string
	Items    []gooikeScanItem
	Services []string
	Errors   []string
}

// gooikeFirstParty are purposes of cookies WordPress Go and its plugins set. pll_language is
// necessary: Pursae Multi Language sets it on every visit, before any choice, and it only keeps the
// language the visitor asked for (a user-interface preference, which needs no consent).
var gooikeFirstParty = map[string]gooikeCookieRow{
	"wpgo_logged_in":    {Category: "necessary", Purpose: "Keeps a logged-in user signed in.", Duration: "Session / 14 days"},
	"comment_author_":   {Category: "preferences", Purpose: "Remembers the details you entered when you left a comment.", Duration: "1 year"},
	"wp-postpass_":      {Category: "necessary", Purpose: "Remembers the password of a protected post.", Duration: "10 days"},
	"wpgo_mtnc_":        {Category: "necessary", Purpose: "Lets a visitor with the access link or password see the site in maintenance mode.", Duration: "1 day"},
	"pll_language":      {Category: "necessary", Purpose: gooikePllPurpose, Duration: "1 year"},
	gooikeCookie:        {Category: "necessary", Purpose: "Stores your cookie consent choice.", Duration: "12 months"},
	"wp-settings-":      {Category: "necessary", Purpose: "Keeps the admin interface settings of a logged-in user.", Duration: "1 year"},
	"wordpress_test_co": {Category: "necessary", Purpose: "Checks that the browser accepts cookies.", Duration: "Session"},
}

// gooikePllPurpose is the declared purpose of pll_language. Sites scanned before it moved to
// "necessary" declared it as "preferences" with this purpose; gooikeSanitize moves those rows.
const gooikePllPurpose = "Remembers the language you chose."

func gooikeKnownFirstParty(name string) (gooikeCookieRow, bool) {
	for k, v := range gooikeFirstParty {
		if name == k || strings.HasPrefix(name, k) {
			return v, true
		}
	}
	return gooikeCookieRow{}, false
}

func gooikeDuration(ck *http.Cookie) string {
	switch {
	case ck.MaxAge > 0:
		return gooikeHumanDuration(time.Duration(ck.MaxAge) * time.Second)
	case !ck.Expires.IsZero() && ck.Expires.After(time.Now()):
		return gooikeHumanDuration(time.Until(ck.Expires))
	}
	return "Session"
}

func gooikeHumanDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 365:
		return fmt.Sprintf("%d year(s)", days/365)
	case days >= 28:
		return fmt.Sprintf("%d month(s)", days/30)
	case days >= 1:
		return fmt.Sprintf("%d day(s)", days)
	case d >= time.Hour:
		return fmt.Sprintf("%d hour(s)", int(d.Hours()))
	}
	return fmt.Sprintf("%d minute(s)", max(int(d.Minutes()), 1))
}

// gooikeDeclared reports whether a cookie name is in the declaration (exact or by a * pattern).
func gooikeDeclared(st *gooikeSettings, name string) bool {
	for _, ck := range st.Cookies {
		if ck.Name == name {
			return true
		}
		if p, ok := strings.CutSuffix(ck.Name, "*"); ok && strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// gooikeScan runs the scan.
func (s *Server) gooikeScan(ctx context.Context, st *gooikeSettings, extra []string) gooikeScanResult {
	res := gooikeScanResult{At: time.Now().In(s.loc()).Format("2006-01-02 15:04")}
	seen := map[string]bool{}
	add := func(row gooikeCookieRow, source string) {
		if row.Name == "" || seen[row.Name] {
			return
		}
		seen[row.Name] = true
		res.Items = append(res.Items, gooikeScanItem{gooikeCookieRow: row, Source: source, Declared: gooikeDeclared(st, row.Name)})
	}
	paths := []string{"/"}
	for _, p := range extra {
		if u, err := url.Parse(strings.TrimSpace(p)); err == nil && strings.HasPrefix(u.Path, "/") && !strings.HasPrefix(u.Path, "/wp-admin") && len(paths) < 10 {
			paths = append(paths, u.RequestURI())
		}
	}
	home, _ := url.Parse(s.links(ctx).HomeURL("/"))
	app := s.appHandler()
	var pages strings.Builder
	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil).WithContext(ctx)
		if home != nil && home.Host != "" {
			req.Host = home.Host
		}
		req.Header.Set("User-Agent", "Pursae Cookie cookie scanner (Pursae)")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		resp := rec.Result()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		res.Pages = append(res.Pages, fmt.Sprintf("%s (%d)", p, resp.StatusCode))
		if resp.StatusCode >= 500 {
			res.Errors = append(res.Errors, fmt.Sprintf("%s answered %d.", p, resp.StatusCode))
		}
		pages.Write(body)
		for _, ck := range resp.Cookies() {
			row := gooikeCookieRow{Name: ck.Name, Provider: "This site", Category: "necessary", Purpose: "Set by this site.", Duration: gooikeDuration(ck)}
			if k, ok := gooikeKnownFirstParty(ck.Name); ok {
				row.Category, row.Purpose, row.Duration = k.Category, k.Purpose, k.Duration
			}
			add(row, "Set by "+p)
		}
	}
	html := strings.ToLower(pages.String())
	found := map[string]string{} // service key -> source
	for _, sc := range st.Scripts {
		if sc.Enabled && sc.Service != "" {
			found[sc.Service] = "Script manager"
		}
	}
	for _, sv := range gooikeServices {
		if _, ok := found[sv.Key]; ok {
			continue
		}
		for _, d := range sv.Detect {
			if strings.Contains(html, strings.ToLower(d)) {
				found[sv.Key] = "Detected in the page"
				break
			}
		}
	}
	var keys []string
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sv := gooikeServiceByKey(k)
		res.Services = append(res.Services, sv.Name+" ("+found[k]+")")
		for _, ck := range sv.Cookies {
			ck.Category = sv.Category
			add(ck, sv.Name+": "+found[k])
		}
	}
	if strings.Contains(html, "google.com/recaptcha/api.js") {
		res.Services = append(res.Services, "Google reCAPTCHA (Detected in the page)")
		for _, ck := range gooikeRecaptchaCookies {
			ck.Category = st.EmbedCategory
			add(ck, "Google reCAPTCHA")
		}
	}
	for _, e := range gooikeEmbeds {
		for _, h := range e.Hosts {
			hit := strings.Contains(html, h+"/embed") || strings.Contains(html, "//"+h+"/video") || (e.Key == "maps" && strings.Contains(html, "google.com/maps/embed"))
			switch e.Key {
			case "youtube", "vimeo", "maps":
			default: // the newer providers: an iframe on one of their embed hosts, or their loader script
				hit = hit || strings.Contains(html, `src="https://`+h) || strings.Contains(html, `src="//`+h)
				for _, sig := range e.Scripts {
					hit = hit || gooikeSigIn(strings.ToLower(html), sig)
				}
			}
			if hit {
				res.Services = append(res.Services, e.Name+" embed (Detected in the page)")
				for _, ck := range e.Cookies {
					ck.Category = st.EmbedCategory
					add(ck, e.Name+" embed")
				}
				break
			}
		}
	}
	return res
}
