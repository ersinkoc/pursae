package server

import (
	"context"
	"html/template"

	"github.com/gin-gonic/gin"
)

// Goocommerce's storefront: shop and product archives, the single product page, cart, checkout,
// order received, My Account, the mini-cart and the store's forms and AJAX actions.
// (Stub: replaced by the storefront implementation.)

func (s *Server) gooAcceptsPost(c *gin.Context) bool                       { return false }
func (s *Server) gooSiteIntercept(c *gin.Context, path string) bool        { return false }
func (s *Server) gooShortcodes(ctx context.Context, content string) string { return content }
func (s *Server) gooBodyClasses(c *gin.Context, q *siteQuery) []string     { return nil }
func (s *Server) gooHead(c *gin.Context, q *siteQuery) string              { return "" }
func (s *Server) gooFooter(c *gin.Context, q *siteQuery) string            { return "" }
func (s *Server) gooCacheTags(c *gin.Context, q *siteQuery) []string       { return nil }

var _ template.HTML
