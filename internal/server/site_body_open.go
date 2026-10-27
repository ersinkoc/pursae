package server

import (
	"html/template"

	"github.com/gin-gonic/gin"
)

// wpBodyOpen is what wp_body_open() prints right after <body> (themes call {{.BodyOpen}}): the
// Golementor Custom Code snippets of the "<body> - Start" location. The editor preview prints none.
func (s *Server) wpBodyOpen(c *gin.Context, v *siteView) template.HTML {
	if c == nil || gwePreviewing(c) || !s.golementorOn() {
		return ""
	}
	return template.HTML(s.gweProSnippets(c, "body_start"))
}
