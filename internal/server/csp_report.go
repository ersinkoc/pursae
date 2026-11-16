package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// CSP violation reports (report-uri cspReportPath). Browsers post them without credentials, so the
// endpoint is public: bodies are bounded, reports are rate-limited and folded into a small
// in-memory table of distinct violations (directive, blocked resource, source location, page)
// with counts. Administrators read it at GET /wp-json/pursae/v1/csp-reports; each new violation
// is logged once.

const (
	cspReportMaxBody    = 16 << 10
	cspReportMaxEntries = 500
	cspReportPerSecond  = 50
)

type cspViolation struct {
	Directive string    `json:"directive"`
	Blocked   string    `json:"blocked"`
	Source    string    `json:"source"`
	Page      string    `json:"page"`
	Sample    string    `json:"sample,omitempty"`
	Count     int       `json:"count"`
	First     time.Time `json:"first"`
	Last      time.Time `json:"last"`
}

type cspReports struct {
	mu       sync.Mutex
	m        map[string]*cspViolation
	dropped  int
	second   int64
	inSecond int
}

var cspReportLog = &cspReports{m: map[string]*cspViolation{}}

// allow is the global rate limit: reports beyond cspReportPerSecond in one second are dropped.
func (r *cspReports) allow(now time.Time) bool {
	if sec := now.Unix(); sec != r.second {
		r.second, r.inSecond = sec, 0
	}
	r.inSecond++
	return r.inSecond <= cspReportPerSecond
}

func (r *cspReports) add(v cspViolation, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.allow(now) {
		r.dropped++
		return
	}
	// One row per violation (directive, blocked resource, source): the same script on many pages is
	// one row, with the first page as an example.
	key := v.Directive + "\x00" + v.Blocked + "\x00" + v.Source
	if e := r.m[key]; e != nil {
		e.Count++
		e.Last = now
		return
	}
	if len(r.m) >= cspReportMaxEntries { // full: the row seen least recently makes room
		var oldest string
		for k, e := range r.m {
			if oldest == "" || e.Last.Before(r.m[oldest].Last) {
				oldest = k
			}
		}
		delete(r.m, oldest)
		r.dropped++
	}
	v.Count, v.First, v.Last = 1, now, now
	r.m[key] = &v
	slog.Info("csp violation", "directive", v.Directive, "blocked", v.Blocked, "source", v.Source, "page", v.Page)
}

func (r *cspReports) list() ([]cspViolation, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]cspViolation, 0, len(r.m))
	for _, v := range r.m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Last.After(out[j].Last)
	})
	return out, r.dropped
}

func (r *cspReports) clear() {
	r.mu.Lock()
	r.m, r.dropped = map[string]*cspViolation{}, 0
	r.mu.Unlock()
}

// cspReportBody is the report-uri format (application/csp-report); the Reporting API format
// (application/reports+json, a list of {type, body}) is read too.
type cspReportBody struct {
	DocumentURI        string `json:"document-uri"`
	BlockedURI         string `json:"blocked-uri"`
	EffectiveDirective string `json:"effective-directive"`
	ViolatedDirective  string `json:"violated-directive"`
	SourceFile         string `json:"source-file"`
	LineNumber         int    `json:"line-number"`
	ScriptSample       string `json:"script-sample"`
}

type cspReportingBody struct {
	DocumentURL        string `json:"documentURL"`
	BlockedURL         string `json:"blockedURL"`
	EffectiveDirective string `json:"effectiveDirective"`
	SourceFile         string `json:"sourceFile"`
	LineNumber         int    `json:"lineNumber"`
	Sample             string `json:"sample"`
}

func (s *Server) cspReport(c *gin.Context) {
	switch ct := strings.ToLower(c.ContentType()); ct {
	case "application/csp-report", "application/reports+json", "application/json":
	default:
		c.Status(http.StatusUnsupportedMediaType)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, cspReportMaxBody+1))
	if err != nil || len(raw) > cspReportMaxBody {
		c.Status(http.StatusRequestEntityTooLarge)
		return
	}
	now := time.Now()
	var one struct {
		Report cspReportBody `json:"csp-report"`
	}
	if json.Unmarshal(raw, &one) == nil && (one.Report.DocumentURI != "" || one.Report.EffectiveDirective != "") {
		r := one.Report
		dir := r.EffectiveDirective
		if dir == "" {
			dir = r.ViolatedDirective
		}
		cspReportLog.add(cspViolationOf(dir, r.BlockedURI, r.SourceFile, r.LineNumber, r.DocumentURI, r.ScriptSample), now)
		c.Status(http.StatusNoContent)
		return
	}
	var many []struct {
		Type string           `json:"type"`
		Body cspReportingBody `json:"body"`
	}
	if json.Unmarshal(raw, &many) == nil {
		for i, r := range many {
			if i >= 20 {
				break
			}
			if r.Type != "csp-violation" {
				continue
			}
			b := r.Body
			cspReportLog.add(cspViolationOf(b.EffectiveDirective, b.BlockedURL, b.SourceFile, b.LineNumber, b.DocumentURL, b.Sample), now)
		}
	}
	c.Status(http.StatusNoContent)
}

// cspViolationOf normalises a report: page and source without query strings (they may hold
// personal data or nonces), bounded lengths.
func cspViolationOf(directive, blocked, source string, line int, page, sample string) cspViolation {
	v := cspViolation{
		Directive: cspClip(directive, 64),
		Blocked:   cspClip(cspStripQuery(blocked), 300),
		Source:    cspClip(cspStripQuery(source), 300),
		Page:      cspClip(cspPathOf(page), 300),
		Sample:    cspClip(sample, 80),
	}
	if v.Source != "" && line > 0 {
		v.Source += ":" + itoa(line)
	}
	return v
}

func cspStripQuery(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		return u[:i]
	}
	return u
}

func cspPathOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Path != "" {
		return p.Path
	}
	return cspStripQuery(u)
}

func cspClip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) > n {
		s = s[:n]
		s = strings.ToValidUTF8(s, "")
	}
	return s
}

func (s *Server) pursaeCSPReports(c *gin.Context, _ *restRequest) {
	list, dropped := cspReportLog.list()
	restJSON(c, http.StatusOK, map[string]any{"mode": s.cspMode(), "violations": list, "dropped": dropped})
}

func (s *Server) pursaeCSPReportsClear(c *gin.Context, _ *restRequest) {
	cspReportLog.clear()
	restJSON(c, http.StatusOK, map[string]any{"cleared": true})
}
