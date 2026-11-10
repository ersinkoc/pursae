package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// wc-bookings/v1 (the WooCommerce Bookings REST API, trimmed to what the module stores):
//   GET  /products/slots   free slots of a day (public, like the booking form's own AJAX)
//   GET  /bookings         list, filters: product_id, customer_id, status, after, before
//   GET  /bookings/{id}    one booking
//   PUT  /bookings/{id}    change the status (goes through the same status flow as the admin)
// and the Store API's add-item accepts the booking form fields (see gooStoreBookingForm).

const gooBookingsNS = "wc-bookings/v1"

func (s *Server) registerGooBookingsREST() {
	off := func(c *gin.Context) bool {
		if !s.gooExtOn(bkKey) {
			gooNoRoute(c)
			return true
		}
		return false
	}
	// admin wraps a handler with authentication and the manage_woocommerce capability.
	admin := func(h restHandler) restHandler {
		return func(c *gin.Context, req *restRequest) {
			if off(c) {
				return
			}
			u, aerr := s.gooV3User(c, req.Method)
			if aerr != nil {
				gooFail(c, aerr)
				return
			}
			if u == nil {
				restErr(c, http.StatusUnauthorized, "woocommerce_rest_cannot_view", "Sorry, you cannot list resources.")
				return
			}
			if !u.Can("manage_woocommerce", nil) {
				restErr(c, http.StatusForbidden, "woocommerce_rest_cannot_view", "Sorry, you cannot list resources.")
				return
			}
			h(c, req)
		}
	}
	s.gooRoute(gooBookingsNS, "/products/slots", map[string]restHandler{http.MethodGet: func(c *gin.Context, req *restRequest) {
		if !off(c) {
			s.gooBkRESTSlots(c, req)
		}
	}})
	s.gooRoute(gooBookingsNS, "/bookings", map[string]restHandler{http.MethodGet: admin(s.gooBkRESTList)})
	s.gooRoute(gooBookingsNS, "/bookings/(?P<id>[\\d]+)", map[string]restHandler{
		http.MethodGet: admin(s.gooBkRESTGet),
		http.MethodPut: admin(s.gooBkRESTUpdate),
	})
}

func (s *Server) gooBkRESTJSON(b *bkBooking) map[string]any {
	ts := func(t time.Time) any { return t.UTC().Format("2006-01-02T15:04:05") }
	persons := b.Persons
	if persons == nil {
		persons = map[string]any{}
	}
	return map[string]any{"id": b.ID, "product_id": b.ProductID, "resource_id": b.ResourceID, "order_id": b.OrderID,
		"order_item_id": b.OrderItemID, "customer_id": b.CustomerID, "status": b.Status, "all_day": b.AllDay,
		"start": b.Start.Unix(), "end": b.End.Unix(), "start_gmt": ts(b.Start), "end_gmt": ts(b.End),
		"persons": persons, "cost": b.Cost, "date_created_gmt": ts(b.Created)}
}

// gooBkRESTSlots answers ?product_id=&date=YYYY-MM-DD[&resource_id=].
func (s *Server) gooBkRESTSlots(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(req.Params["product_id"], 10, 64)
	if v, ok := req.Str(c, "product_id"); ok {
		id, _ = strconv.ParseInt(v, 10, 64)
	}
	p, err := s.gooGetProduct(ctx, id)
	if err != nil || p.Type != bkType || p.Status != "publish" {
		restErr(c, http.StatusNotFound, "woocommerce_rest_invalid_product", "Invalid bookable product.")
		return
	}
	date, _ := req.Str(c, "date")
	conf := bkConfOf(p)
	if conf.Unit == "month" && len(date) == 7 {
		date += "-01"
	}
	day, err := time.ParseInLocation("2006-01-02", date, s.gxLoc())
	if err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): date")
		return
	}
	var resource int64
	if v, ok := req.Str(c, "resource_id"); ok {
		resource, _ = strconv.ParseInt(v, 10, 64)
	}
	slots := s.gooBkSlotsAll(ctx, p, conf, day, resource)
	if slots == nil {
		slots = []bkSlot{}
	}
	c.JSON(http.StatusOK, map[string]any{"product_id": p.ID, "date": date, "records": slots})
}

func (s *Server) gooBkRESTList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	where, args := "TRUE", []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where += " AND " + strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args)))
	}
	for _, k := range []string{"product_id", "customer_id", "order_id"} {
		if v, ok := req.Str(c, k); ok && v != "" {
			n, _ := strconv.ParseInt(v, 10, 64)
			add(k+" = ?", n)
		}
	}
	if v, ok := req.Str(c, "status"); ok && v != "" {
		if bkStatusLabel(v) == v {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status")
			return
		}
		add("status = ?", v)
	}
	for k, cond := range map[string]string{"after": "start_at >= ?", "before": "start_at < ?"} {
		if v, ok := req.Str(c, k); ok && v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				t, err = time.Parse("2006-01-02", v)
			}
			if err != nil {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): "+k)
				return
			}
			add(cond, t)
		}
	}
	out := []map[string]any{}
	for _, b := range s.bkQuery(ctx, where+" ORDER BY start_at, id LIMIT 100", args...) {
		out = append(out, s.gooBkRESTJSON(b))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) gooBkRESTGet(c *gin.Context, req *restRequest) {
	b, err := s.bkGet(c.Request.Context(), req.ID())
	if err != nil {
		restErr(c, http.StatusNotFound, "woocommerce_rest_booking_invalid_id", "Invalid ID.")
		return
	}
	c.JSON(http.StatusOK, s.gooBkRESTJSON(b))
}

func (s *Server) gooBkRESTUpdate(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	b, err := s.bkGet(ctx, req.ID())
	if err != nil {
		restErr(c, http.StatusNotFound, "woocommerce_rest_booking_invalid_id", "Invalid ID.")
		return
	}
	if v, ok := req.Str(c, "status"); ok && v != "" {
		if bkStatusLabel(v) == v {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status")
			return
		}
		if err := s.bkSetStatus(ctx, b, v); err != nil {
			restErr(c, http.StatusBadRequest, "woocommerce_rest_booking_update_failed", err.Error())
			return
		}
	}
	c.JSON(http.StatusOK, s.gooBkRESTJSON(b))
}

// gooStoreBookingForm reads the booking form fields a Store API add-item request carries, in
// "booking" or "extensions.bookings": {"wc_bookings_field_start_date": "2026-12-01", ...}.
func gooStoreBookingForm(body map[string]any) map[string][]string {
	m := gooObj(body["booking"])
	if len(m) == 0 {
		m = gooObj(gooObj(body["extensions"])["bookings"])
	}
	if len(m) == 0 {
		return nil
	}
	form := map[string][]string{}
	for k, v := range m {
		form[k] = []string{gooStr(v)}
	}
	return form
}
