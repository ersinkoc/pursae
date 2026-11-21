package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Settings › Security (options-security): the script Content-Security-Policy. The site owner picks
// the mode (csp_mode), whether scripts that administrators stored get the nonce
// (csp_admin_scripts, csp_trusted.go), and reads the violations browsers reported (csp_report.go).

type cspSettingsView struct {
	Nonce        string
	Mode         string
	AdminScripts bool
	Reports      []cspViolation
	Dropped      int
	ReportPath   string
	Extra        []cspExtraRow
}

// cspExtraRow is one directive of the "Allowed external sources" table.
type cspExtraRow struct {
	Directive, Help, Text string
	Errors                []string
}

var cspExtraRowHelp = map[string]string{
	"connect-src": "Hosts that scripts on your pages may request data from (fetch, XMLHttpRequest, WebSocket), e.g. https://api.github.com.",
	"font-src":    "Hosts your pages may load web fonts from, e.g. https://fonts.gstatic.com.",
	"style-src":   "Hosts your pages may load stylesheets from, e.g. https://fonts.googleapis.com.",
}

// cspExtraRows builds the table from the stored sources, or from the submitted text and its errors.
func cspExtraRows(src map[string][]string, text map[string]string, issues []cspExtraIssue) []cspExtraRow {
	rows := make([]cspExtraRow, 0, len(cspExtraDirectives))
	for _, d := range cspExtraDirectives {
		r := cspExtraRow{Directive: d, Help: cspExtraRowHelp[d], Text: strings.Join(src[d], "\n")}
		if t, ok := text[d]; ok {
			r.Text = t
		}
		for _, is := range issues {
			if is.Directive == d {
				if is.Source != "" {
					r.Errors = append(r.Errors, is.Source+": "+is.Reason)
				} else {
					r.Errors = append(r.Errors, is.Reason)
				}
			}
		}
		rows = append(rows, r)
	}
	return rows
}

func (s *Server) optionsSecurity(c *gin.Context) {
	if !currentUser(c).Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to manage options for this site.")
		return
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "pursae-security") {
			s.denyNonce(c)
			return
		}
		if c.PostForm("clear_reports") != "" {
			cspReportLog.clear()
			c.Redirect(http.StatusFound, "/wp-admin/options-security?cleared=true")
			return
		}
		mode := c.PostForm("csp_mode")
		switch mode {
		case cspModeReport, cspModeEnforce, cspModeOff:
		default:
			c.Redirect(http.StatusFound, "/wp-admin/options-security?error=invalid")
			return
		}
		admin := false
		if vals := c.Request.PostForm["csp_admin_scripts"]; len(vals) > 0 { // hidden 0, then the checkbox's 1
			admin = vals[len(vals)-1] == "1"
		}
		// Allowed external sources: one origin per line and directive. An invalid line saves nothing
		// and shows the form again with the error beside its field.
		text, update := map[string]string{}, map[string][]string{}
		for _, d := range cspExtraDirectives {
			if v, ok := c.GetPostForm("csp_extra_" + d); ok {
				text[d] = v
				update[d] = strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' })
				if update[d] == nil {
					update[d] = []string{}
				}
			}
		}
		if _, issues := cspNormalizeExtra(update); len(issues) > 0 {
			v := s.cspSecurityView(c)
			v.Mode, v.AdminScripts = mode, admin
			v.Extra = cspExtraRows(nil, text, issues)
			s.adminPage(c, "options-security", "Security Settings", "options-security-php", "admin/options-security", v,
				notice{Type: "error", Message: "The settings were not saved: check the allowed external sources below.", Dismissible: true})
			return
		}
		if err := s.settings.Set(c.Request.Context(), map[string]any{"csp_mode": mode, "csp_admin_scripts": admin}); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/options-security?error=save")
			return
		}
		if len(update) > 0 {
			if _, _, err := s.cspSaveExtra(c, currentUser(c), "Settings › Security", update); err != nil {
				c.Redirect(http.StatusFound, "/wp-admin/options-security?error=save")
				return
			}
		}
		// Page-cache entries remember the policy they were stored under (cspCacheState): no purge.
		c.Redirect(http.StatusFound, "/wp-admin/options-security?settings-updated=true")
		return
	}
	v := s.cspSecurityView(c)
	var notices []notice
	switch {
	case c.Query("settings-updated") == "true":
		notices = append(notices, notice{Type: "success", Message: "Settings saved.", Dismissible: true})
	case c.Query("cleared") == "true":
		notices = append(notices, notice{Type: "success", Message: "The reports were cleared.", Dismissible: true})
	case c.Query("error") != "":
		notices = append(notices, notice{Type: "error", Message: "The settings could not be saved.", Dismissible: true})
	}
	s.adminPage(c, "options-security", "Security Settings", "options-security-php", "admin/options-security", v, notices...)
}

func (s *Server) cspSecurityView(c *gin.Context) cspSettingsView {
	v := cspSettingsView{Nonce: s.nonce(c, "pursae-security"), Mode: s.cspMode(), AdminScripts: s.cspTrustOn(), ReportPath: cspReportPath,
		Extra: cspExtraRows(s.cspExtraSources(), nil, nil)}
	v.Reports, v.Dropped = cspReportLog.list()
	return v
}
