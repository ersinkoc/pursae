package server

// Allowed external sources: origins the site owner adds to a few directives of the front-end
// Content-Security-Policy (Settings › Security, GET/POST /pursae/v1/csp, the csp_* MCP tools).
//
// The front-end policy keeps connect-src, font-src and style-src on 'self' (connect-src through
// default-src), so a theme's script cannot fetch https://api.github.com and a stylesheet cannot load
// a font from a third-party host. The site owner may open exactly those directives for named https
// hosts. script-src is never opened: it would undo the nonce / 'strict-dynamic' policy
// (csp_nonce.go). img-src, media-src and frame-src already allow every https: source, so they need
// nothing here.
//
// Every path (the admin form, REST, MCP) goes through cspValidateExtra, and the stored option is
// validated again when it is read, so a value written some other way (wp option, a settings import)
// never reaches the header raw. Only the canonical form cspValidateExtra rebuilds is sent.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
	"wpgo/internal/settings"
	"wpgo/internal/store"
)

const (
	cspExtraOption       = "csp_extra_sources"
	cspExtraPerDirective = 20
	cspExtraMaxLen       = 253
)

// cspExtraDirectives are the directives an external source may be added to, in display order.
var cspExtraDirectives = []string{"connect-src", "font-src", "style-src"}

// cspExtraHelp describes the accepted form (REST, MCP and the admin screen show it).
const cspExtraHelp = "Directives: connect-src, font-src, style-src (img-src, media-src and frame-src already allow every https: source; " +
	"script-src is never opened). Sources: https://host or https://host:port, optionally https://*.example.com (leftmost wildcard only); " +
	"wss://host for connect-src only. No path, query, IP address, localhost, http:, data:, blob:, * or quoted keyword. At most 20 per directive."

// cspExtraIssue is one rejected entry.
type cspExtraIssue struct {
	Directive string `json:"directive"`
	Source    string `json:"source"`
	Reason    string `json:"reason"`
}

var cspIDNA = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.StrictDomainName(true), idna.ValidateLabels(true))

// cspValidateExtra checks one source for a directive and returns its canonical form.
func cspValidateExtra(directive, source string) (string, error) {
	if !slices.Contains(cspExtraDirectives, directive) {
		return "", fmt.Errorf("directive %q cannot be extended (allowed: %s)", directive, strings.Join(cspExtraDirectives, ", "))
	}
	if source == "" {
		return "", errors.New("empty source")
	}
	if len(source) > cspExtraMaxLen {
		return "", fmt.Errorf("longer than %d characters", cspExtraMaxLen)
	}
	for _, r := range source {
		if r < 0x21 || r == 0x7f || strings.ContainsRune(";,'\"\\`<>", r) {
			return "", errors.New("contains a space, control character, quote, ';' or ','")
		}
	}
	scheme, rest, ok := strings.Cut(source, "://")
	if !ok {
		return "", errors.New("must start with https://")
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "https":
	case "wss":
		if directive != "connect-src" {
			return "", errors.New("wss:// is allowed for connect-src only")
		}
	default:
		return "", errors.New("only https:// sources (and wss:// for connect-src) are allowed")
	}
	rest = strings.TrimSuffix(rest, "/")
	if strings.ContainsAny(rest, "/?#@[]%") {
		return "", errors.New("only an origin is allowed: no path, query, fragment or user name")
	}
	host, port := rest, ""
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		host, port = rest[:i], rest[i+1:]
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", errors.New("invalid port")
		}
		if (scheme == "https" || scheme == "wss") && n == 443 {
			port = ""
		}
	}
	wild := strings.HasPrefix(host, "*.")
	if wild {
		host = host[2:]
	}
	if host == "" || strings.Contains(host, "*") {
		return "", errors.New("a wildcard is allowed only as the leftmost label (https://*.example.com)")
	}
	ascii, err := cspIDNA.ToASCII(host)
	if err != nil {
		return "", errors.New("invalid host name")
	}
	host = strings.ToLower(strings.TrimSuffix(ascii, "."))
	if net.ParseIP(host) != nil {
		return "", errors.New("IP addresses are not allowed; use a host name")
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", errors.New("the host needs a domain (example.com)")
	}
	if last := labels[len(labels)-1]; strings.Trim(last, "0123456789") == "" {
		return "", errors.New("IP addresses are not allowed; use a host name")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", errors.New("localhost is not allowed")
	}
	if _, err := publicsuffix.EffectiveTLDPlusOne(host); err != nil {
		return "", errors.New("the host is a public suffix (like com or co.uk), not a domain")
	}
	out := scheme + "://"
	if wild {
		out += "*."
	}
	out += host
	if port != "" {
		out += ":" + port
	}
	return out, nil
}

// cspNormalizeExtra validates a directive → sources map: the canonical sources without duplicates,
// and every rejected entry. Directives absent from in are absent from the result.
func cspNormalizeExtra(in map[string][]string) (map[string][]string, []cspExtraIssue) {
	out := map[string][]string{}
	var issues []cspExtraIssue
	names := make([]string, 0, len(in))
	for d := range in {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		list := []string{}
		for _, raw := range in[d] {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			src, err := cspValidateExtra(d, raw)
			if err != nil {
				issues = append(issues, cspExtraIssue{Directive: d, Source: raw, Reason: err.Error()})
				continue
			}
			if !slices.Contains(list, src) {
				list = append(list, src)
			}
		}
		if len(list) > cspExtraPerDirective {
			issues = append(issues, cspExtraIssue{Directive: d, Reason: fmt.Sprintf("at most %d sources per directive", cspExtraPerDirective)})
			list = list[:cspExtraPerDirective]
		}
		out[d] = list
	}
	return out, issues
}

// cspExtraFromAny reads a decoded JSON value ({"connect-src": ["https://…"]}) into a map; ok is false
// when the shape is wrong.
func cspExtraFromAny(v any) (map[string][]string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, v == nil
	}
	out := map[string][]string{}
	for d, list := range m {
		items, ok := list.([]any)
		if !ok {
			if list == nil {
				out[d] = nil
				continue
			}
			return nil, false
		}
		for _, it := range items {
			s, ok := it.(string)
			if !ok {
				return nil, false
			}
			out[d] = append(out[d], s)
		}
	}
	return out, true
}

// cspExtraValue is the validated option with the stored JSON it was validated from.
type cspExtraValue struct {
	raw   string
	value map[string][]string
	hash  string
}

// cspExtraMemo memoizes the validated option per settings revision, with no global mutex (the option is
// read on every front-end response, page-cache hits included). After an unrelated settings change the
// stored JSON is compared, so validation (IDNA, the public-suffix lookup) runs only when the option
// itself changed. The cached map is shared: callers must not modify it.
var cspExtraMemo settings.Memo[*cspExtraValue]

// cspExtraSources is the stored option, validated: only canonical sources of allowed directives.
func (s *Server) cspExtraSources() map[string][]string {
	src, _ := s.cspExtraState()
	return src
}

// cspExtraState returns the validated sources and a short hash of them ("" when there are none).
func (s *Server) cspExtraState() (map[string][]string, string) {
	if s.settings == nil {
		return nil, ""
	}
	e := cspExtraMemo.Get(s.settings, s.cspExtraBuild)
	return e.value, e.hash
}

// cspExtraBuild validates the stored option, reusing the previous result when its JSON is unchanged.
func (s *Server) cspExtraBuild() *cspExtraValue {
	v := s.settings.Get(cspExtraOption)
	b, _ := json.Marshal(v)
	if prev, ok := cspExtraMemo.Peek(s.settings); ok && prev.raw == string(b) {
		return prev
	}
	in, _ := cspExtraFromAny(v)
	norm, issues := cspNormalizeExtra(in)
	if len(issues) > 0 {
		slog.Warn("csp: stored external sources dropped", "option", cspExtraOption, "issues", len(issues))
	}
	out := map[string][]string{}
	for d, list := range norm {
		if len(list) > 0 && slices.Contains(cspExtraDirectives, d) {
			out[d] = list
		}
	}
	hash := ""
	if len(out) > 0 {
		canon, _ := json.Marshal(out) // map keys marshal sorted
		sum := sha256.Sum256(canon)
		hash = hex.EncodeToString(sum[:6])
	}
	return &cspExtraValue{raw: string(b), value: out, hash: hash}
}

// cspAllowExtra adds the site's external sources to this front-end response's policy.
func (s *Server) cspAllowExtra(c *gin.Context) {
	for d, list := range s.cspExtraSources() {
		cspAllow(c, d, list...)
	}
}

// cspSaveExtra validates and stores the sources of the directives in update (a directive left out
// keeps its sources; an empty list clears it). Nothing is saved when an entry is invalid. The change
// goes to the activity log (AdminSecGo) and the server log.
func (s *Server) cspSaveExtra(c *gin.Context, u *store.User, via string, update map[string][]string) (map[string][]string, []cspExtraIssue, error) {
	norm, issues := cspNormalizeExtra(update)
	if len(issues) > 0 {
		return nil, issues, nil
	}
	before := s.cspExtraSources()
	next := map[string][]string{}
	for d, list := range before {
		next[d] = list
	}
	for d, list := range norm {
		if len(list) == 0 {
			delete(next, d)
		} else {
			next[d] = list
		}
	}
	stored := map[string]any{}
	for d, list := range next {
		stored[d] = list
	}
	if err := s.settings.Set(c.Request.Context(), map[string]any{cspExtraOption: stored}); err != nil {
		return nil, nil, err
	}
	// Page-cache entries remember the policy state they were stored under (cspCacheState includes
	// these sources): no purge.
	if diff := cspExtraDiff(before, next); diff != "" {
		login := ""
		if u != nil {
			login = u.Login
		}
		slog.Info("csp: allowed external sources changed", "user", login, "via", via, "change", diff)
		s.asAudit(c, asEvent{Event: "csp_sources_changed", Severity: "notice", User: u, Object: via, Message: diff})
	}
	return s.cspExtraSources(), nil, nil
}

// cspExtraDiff describes what changed: "connect-src: added https://a; removed https://b".
func cspExtraDiff(before, after map[string][]string) string {
	var parts []string
	for _, d := range cspExtraDirectives {
		var add, del []string
		for _, s := range after[d] {
			if !slices.Contains(before[d], s) {
				add = append(add, s)
			}
		}
		for _, s := range before[d] {
			if !slices.Contains(after[d], s) {
				del = append(del, s)
			}
		}
		if len(add) > 0 {
			parts = append(parts, d+": added "+strings.Join(add, " "))
		}
		if len(del) > 0 {
			parts = append(parts, d+": removed "+strings.Join(del, " "))
		}
	}
	return strings.Join(parts, "; ")
}

// cspExtraView is every allowed directive with its sources (empty lists included).
func cspExtraView(src map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, d := range cspExtraDirectives {
		out[d] = append([]string{}, src[d]...)
	}
	return out
}

// pursaeCSPExtra is GET/POST /pursae/v1/csp. POST takes {"sources": {"connect-src": [...]}}: the lists
// of the directives named replace the stored ones; an invalid entry fails the whole request with 400.
func (s *Server) pursaeCSPExtra(c *gin.Context, r *restRequest) {
	if r.Method != http.MethodGet {
		raw, present := r.Body["sources"]
		update, ok := cspExtraFromAny(raw)
		if !present || !ok || raw == nil {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", `sources must be an object of directive → list of origins, e.g. {"connect-src": ["https://api.github.com"]}. `+cspExtraHelp)
			return
		}
		_, issues, err := s.cspSaveExtra(c, restCurrent(c), "REST ("+s.mcpClientName(c)+")", update)
		if err != nil {
			restErr(c, http.StatusInternalServerError, "rest_cannot_update", err.Error())
			return
		}
		if len(issues) > 0 {
			msg := "Invalid source"
			if len(issues) > 1 {
				msg = "Invalid sources"
			}
			for i, is := range issues {
				sep := "; "
				if i == 0 {
					sep = ": "
				}
				msg += sep + is.Directive + " " + strconv.Quote(is.Source) + " (" + is.Reason + ")"
			}
			restErr(c, http.StatusBadRequest, "rest_invalid_param", msg+". Nothing was saved.", map[string]any{"invalid": issues})
			return
		}
	}
	restJSON(c, http.StatusOK, map[string]any{"sources": cspExtraView(s.cspExtraSources()), "directives": cspExtraDirectives, "rules": cspExtraHelp})
}
