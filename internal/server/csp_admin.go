package server

import (
	"net/http"

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
		if err := s.settings.Set(c.Request.Context(), map[string]any{"csp_mode": mode, "csp_admin_scripts": admin}); err != nil {
			c.Redirect(http.StatusFound, "/wp-admin/options-security?error=save")
			return
		}
		// Page-cache entries remember the policy they were stored under (cspCacheState): no purge.
		c.Redirect(http.StatusFound, "/wp-admin/options-security?settings-updated=true")
		return
	}
	v := cspSettingsView{Nonce: s.nonce(c, "pursae-security"), Mode: s.cspMode(), AdminScripts: s.cspTrustOn(), ReportPath: cspReportPath}
	v.Reports, v.Dropped = cspReportLog.list()
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
