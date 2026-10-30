package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Goocommerce's store admin screens: Home, Orders, Customers, Coupons, Marketing, Settings,
// Reports, Analytics and Status. (Stub: replaced by the store admin implementation.)

func (s *Server) gooAdminHome(c *gin.Context)      { c.String(http.StatusNotImplemented, "Home") }
func (s *Server) gooAdminOrders(c *gin.Context)    { c.String(http.StatusNotImplemented, "Orders") }
func (s *Server) gooAdminCustomers(c *gin.Context) { c.String(http.StatusNotImplemented, "Customers") }
func (s *Server) gooAdminReports(c *gin.Context)   { c.String(http.StatusNotImplemented, "Reports") }
func (s *Server) gooAdminSettings(c *gin.Context)  { c.String(http.StatusNotImplemented, "Settings") }
func (s *Server) gooAdminStatus(c *gin.Context)    { c.String(http.StatusNotImplemented, "Status") }
func (s *Server) gooAdminCoupons(c *gin.Context)   { c.String(http.StatusNotImplemented, "Coupons") }
func (s *Server) gooAdminMarketing(c *gin.Context) { c.String(http.StatusNotImplemented, "Marketing") }
func (s *Server) gooAdminAnalytics(c *gin.Context) { c.String(http.StatusNotImplemented, "Analytics") }
