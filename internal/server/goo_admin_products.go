package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Goocommerce's catalog admin screens: Products list, the product editor (Product data box with
// General, Inventory, Shipping, Linked Products, Attributes, Variations, Advanced), Attributes, Reviews.
// (Stub: replaced by the catalog admin implementation.)

func (s *Server) gooAdminProducts(c *gin.Context) { c.String(http.StatusNotImplemented, "Products") }
func (s *Server) gooAdminProductEdit(c *gin.Context, id int64) {
	c.String(http.StatusNotImplemented, "Edit product")
}
func (s *Server) gooAdminAttributes(c *gin.Context) {
	c.String(http.StatusNotImplemented, "Attributes")
}
func (s *Server) gooAdminReviews(c *gin.Context) { c.String(http.StatusNotImplemented, "Reviews") }
