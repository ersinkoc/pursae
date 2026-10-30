package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) hgGraphQL(c *gin.Context, a *hgAuth, st hgSettings) {
	hgErr(c, http.StatusNotImplemented, "NotImplementedError", "GraphQL is not available", nil)
}
