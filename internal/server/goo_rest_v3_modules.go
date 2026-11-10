package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// wc/v3 endpoints of the optional modules, for ERP / CRM / mobile integrations:
//
//	/bookings        (WooCommerce Bookings)        list, read, update status
//	/memberships     (WooCommerce Memberships, user memberships) list, read, update status / end date
//
// /subscriptions lives in goo_rest_v3_subscriptions.go and /gift-cards in goo_ext_gift_cards_rest.go.
//
// Each answers 404 rest_no_route while its module is off and needs manage_woocommerce.

func (s *Server) registerGooV3Modules() {
	mw := gooCapsAll("manage_woocommerce")
	reg := func(path, module string, list restHandler, get func(c *gin.Context, id int64) (map[string]any, *gooAPIErr), upd func(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr)) {
		on := func(h restHandler) restHandler {
			return func(c *gin.Context, req *restRequest) {
				if !s.gooExtOn(module) {
					gooNoRoute(c)
					return
				}
				h(c, req)
			}
		}
		s.gooV3("/"+path, mw, map[string]restHandler{http.MethodGet: on(list)})
		s.gooV3("/"+path+"/(?P<id>[\\d]+)", mw, map[string]restHandler{
			http.MethodGet: on(s.gooV3Get(get)),
			http.MethodPut: on(s.gooV3Update(upd)),
		})
	}
	reg("bookings", bkKey, s.gooV3BookingsList, s.gooV3BookingGet, s.gooV3BookingUpdate)
	reg("memberships", gmemKey, s.gooV3MembershipsList, s.gooV3MembershipGet, s.gooV3MembershipUpdate)
}

func gooTimeVal(v any, s *Server) (*time.Time, bool) {
	str := strings.TrimSpace(gooStr(v))
	if str == "" {
		return nil, true // cleared
	}
	return s.gooParseDate(str, false)
}

// gooQueryStatuses reads a status filter (comma separated, "any"/empty = all) and checks it against opts.
func gooQueryStatuses(c *gin.Context, opts []gooOpt) ([]string, bool) {
	raw := strings.TrimSpace(c.Query("status"))
	if raw == "" || raw == "any" || raw == "all" {
		return nil, true
	}
	var out []string
	for _, st := range strings.Split(raw, ",") {
		st = strings.TrimSpace(st)
		if gooOptLabel(opts, st) == st {
			return nil, false
		}
		out = append(out, st)
	}
	return out, true
}

// Bookings -------------------------------------------------------------------------------------------------

func (s *Server) gooV3BookingJSON(ctx context.Context, b *bkBooking) map[string]any {
	end := b.End
	start := b.Start
	return map[string]any{"id": b.ID, "product_id": b.ProductID, "resource_id": b.ResourceID, "order_id": b.OrderID, "order_item_id": b.OrderItemID,
		"customer_id": b.CustomerID, "status": b.Status, "all_day": b.AllDay, "persons": b.Persons, "cost": s.gooDec(b.Cost),
		"start": start.Unix(), "end": end.Unix(), "start_date": s.gooDate(&start), "end_date": s.gooDate(&end),
		"date_created": s.gooDate(&b.Created), "date_created_gmt": gooDateGMT(&b.Created)}
}

func (s *Server) gooV3BookingGet(c *gin.Context, id int64) (map[string]any, *gooAPIErr) {
	b, err := s.bkGet(c.Request.Context(), id)
	if err != nil {
		return nil, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID.")
	}
	return s.gooV3BookingJSON(c.Request.Context(), b), nil
}

func (s *Server) gooV3BookingsList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	statuses, ok := gooQueryStatuses(c, bkStatuses)
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status")
		return
	}
	conds, args := []string{"status <> 'in-cart'"}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.Replace(cond, "?", "$"+itoa64(int64(len(args))), 1))
	}
	if len(statuses) > 0 {
		add("status = ANY(?)", statuses)
	}
	if v := gooInt(c.Query("product")); v != 0 {
		add("product_id = ?", v)
	}
	if v := gooInt(c.Query("customer")); v != 0 {
		add("customer_id = ?", v)
	}
	if v := gooInt(c.Query("order")); v != 0 {
		add("order_id = ?", v)
	}
	list := s.bkQuery(ctx, strings.Join(conds, " AND ")+` ORDER BY id DESC`, args...)
	out := []any{}
	for _, b := range gooPaginateSlice(c, list) {
		out = append(out, s.gooV3BookingJSON(ctx, b))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3BookingUpdate(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	bk, err := s.bkGet(ctx, id)
	if err != nil {
		return nil, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID.")
	}
	if v, ok := b["status"]; ok {
		st := gooStr(v)
		if gooOptLabel(bkStatuses, st) == st {
			return nil, gooErr(400, "rest_invalid_param", "Invalid parameter(s): status")
		}
		if err := s.bkSetStatus(ctx, bk, st); err != nil {
			return nil, gooErr(500, "woocommerce_rest_cannot_edit", err.Error())
		}
	}
	fresh, _ := s.bkGet(ctx, id)
	return s.gooV3BookingJSON(ctx, fresh), nil
}

// Memberships ------------------------------------------------------------------------------------------------

func (s *Server) gooV3MembershipJSON(ctx context.Context, m *gooUserMembership) map[string]any {
	planName := ""
	if p, err := s.gmemPlan(ctx, m.PlanID); err == nil {
		planName = p.Name
	}
	return map[string]any{"id": m.ID, "customer_id": m.UserID, "plan_id": m.PlanID, "plan_name": planName, "status": m.Status,
		"order_id": m.OrderID, "product_id": m.ProductID,
		"start_date": s.gooDate(&m.Start), "start_date_gmt": gooDateGMT(&m.Start), "end_date": s.gooDate(m.End), "end_date_gmt": gooDateGMT(m.End),
		"has_access": m.HasAccess(time.Now()), "date_created": s.gooDate(&m.DateCreated), "date_created_gmt": gooDateGMT(&m.DateCreated)}
}

func (s *Server) gooV3MembershipGet(c *gin.Context, id int64) (map[string]any, *gooAPIErr) {
	m, err := s.gmemUM(c.Request.Context(), id)
	if err != nil {
		return nil, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID.")
	}
	return s.gooV3MembershipJSON(c.Request.Context(), m), nil
}

func (s *Server) gooV3MembershipsList(c *gin.Context, req *restRequest) {
	ctx := c.Request.Context()
	status := strings.TrimSpace(c.Query("status"))
	if status == "any" || status == "all" {
		status = ""
	}
	if status != "" && gooOptLabel(gmemStatuses, status) == status {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): status")
		return
	}
	out := []any{}
	for _, m := range gooPaginateSlice(c, s.gmemUMList(ctx, gooInt(c.Query("customer")), gooInt(c.Query("plan")), status)) {
		out = append(out, s.gooV3MembershipJSON(ctx, m))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) gooV3MembershipUpdate(c *gin.Context, id int64, b map[string]any) (map[string]any, *gooAPIErr) {
	ctx := c.Request.Context()
	m, err := s.gmemUM(ctx, id)
	if err != nil {
		return nil, gooErr(404, "woocommerce_rest_invalid_id", "Invalid ID.")
	}
	if v, ok := b["end_date"]; ok {
		t, valid := gooTimeVal(v, s)
		if !valid {
			return nil, gooErr(400, "rest_invalid_param", "Invalid parameter(s): end_date")
		}
		m.End = t
	}
	if v, ok := b["status"]; ok {
		st := gooStr(v)
		if gooOptLabel(gmemStatuses, st) == st {
			return nil, gooErr(400, "rest_invalid_param", "Invalid parameter(s): status")
		}
		if st != m.Status {
			m.addNote("Status changed from " + gmemStatusLabel(m.Status) + " to " + gmemStatusLabel(st) + " through the REST API.")
			m.Status = st
		}
	}
	if err := s.gmemSaveUM(ctx, m); err != nil {
		return nil, gooErr(500, "woocommerce_rest_cannot_edit", err.Error())
	}
	return s.gooV3MembershipJSON(ctx, m), nil
}
