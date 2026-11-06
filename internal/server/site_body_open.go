package server

import (
	"html/template"

	"github.com/gin-gonic/gin"
)

// wpBodyOpen is what wp_body_open() prints right after <body> (themes call {{.BodyOpen}}):
// Gooike's body-open scripts.
func (s *Server) wpBodyOpen(c *gin.Context, v *siteView) template.HTML {
	if c == nil {
		return ""
	}
	return template.HTML(s.gooikeBodyOpen(c)) // Gooike: the "body open" scripts of its script manager (gooike_front.go)
}
