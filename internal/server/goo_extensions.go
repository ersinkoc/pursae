package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Goocommerce › Extensions: the premium modules switched on and off. (Phase 2.)

func (s *Server) gooAdminExtensions(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Extensions")
}
