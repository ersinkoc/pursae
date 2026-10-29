package server

import (
	"html/template"

	"github.com/gin-gonic/gin"
)

// wpBodyOpen is what wp_body_open() prints right after <body> (themes call {{.BodyOpen}}): the
// Golementor Custom Code snippets of the "<body> - Start" location and Gooike's body-open scripts.
// The editor preview prints none.
func (s *Server) wpBodyOpen(c *gin.Context, v *siteView) template.HTML {
	if c == nil || gwePreviewing(c) {
		return ""
	}
	out := s.gooikeBodyOpen(c) // Gooike: the "body open" scripts of its script manager (gooike_front.go)
	if s.golementorOn() {
		out += s.gweProSnippets(c, "body_start")
	}
	return template.HTML(out)
}
