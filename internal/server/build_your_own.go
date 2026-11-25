package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// pluginBuild is the "Build your own plugin" help page (Plugins > Add Plugin button).
func (s *Server) pluginBuild(c *gin.Context) {
	if !currentUser(c).Can("install_plugins", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to install plugins on this site.")
		return
	}
	s.adminPage(c, "plugin-build", "Build your own plugin", "plugins-php", "admin/plugin-build", nil)
}

// themeBuild is the "Build your own theme" help page (Themes > Add Theme button).
func (s *Server) themeBuild(c *gin.Context) {
	if !currentUser(c).Can("install_themes", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to install themes on this site.")
		return
	}
	s.adminPage(c, "theme-build", "Build your own theme", "themes-php", "admin/theme-build", nil)
}
