package server

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// CSV exports of the Orders, Coupons and Subscriptions lists (Customers has its own "Download"). Each
// takes the list's filters (status view, search, month, customer) and exports every matching row, not
// just the current page. The link carries the store nonce: an export holds customer data.

func (s *Server) gooExportAllowed(c *gin.Context) bool {
	if !s.checkNonce(c, gooStoreNonce) {
		s.gooDeny(c)
		return false
	}
	return true
}

func (s *Server) gooExportTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.In(s.loc()).Format("2006-01-02 15:04:05")
}

func gooExportAddress(a map[string]string) string {
	var parts []string
	for _, k := range []string{"address_1", "address_2", "city", "state", "postcode", "country"} {
		if v := strings.TrimSpace(a[k]); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, ", ")
}

func (s *Server) gooExportOrdersCSV(c *gin.Context) {
	if !s.gooExportAllowed(c) {
		return
	}
	ctx := c.Request.Context()
	q := s.gooOrdersListQuery(c)
	q.PerPage = -1
	q.OrderBy, q.Order = "id", "asc"
	orders, _, err := s.gooQueryOrders(ctx, q)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	refunded := s.gooTotalsRefunded(ctx, gooOrderIDs(orders))
	var out [][]string
	for _, o := range orders {
		var items, coupons []string
		for _, it := range o.Items {
			switch it.Type {
			case "line_item":
				qty := strconv.FormatFloat(it.Quantity, 'f', -1, 64)
				items = append(items, it.Name+" x "+qty)
			case "coupon":
				coupons = append(coupons, it.Name)
			}
		}
		subtotal := 0.0
		for _, it := range o.Items {
			if it.Type == "line_item" {
				subtotal += it.Subtotal
			}
		}
		b, sh := o.Billing, o.Shipping
		out = append(out, []string{itoa64(o.ID), o.Status, s.gooExportTime(&o.DateCreated), s.gooExportTime(o.DatePaid), s.gooExportTime(o.DateCompleted),
			itoa64(o.CustomerID), strings.TrimSpace(b["first_name"] + " " + b["last_name"]), b["company"], b["email"], b["phone"], gooExportAddress(b),
			strings.TrimSpace(sh["first_name"] + " " + sh["last_name"]), gooExportAddress(sh), o.PaymentMethodTitle, o.TransactionID, o.Currency,
			s.gooFmtDec(subtotal), s.gooFmtDec(o.DiscountTotal), s.gooFmtDec(o.ShippingTotal),
			s.gooFmtDec(o.TotalTax), s.gooFmtDec(o.Total), s.gooFmtDec(refunded[o.ID]), strings.Join(coupons, ", "), strings.Join(items, "; "), o.CustomerNote})
	}
	gooCSV(c, "orders-"+time.Now().In(s.loc()).Format("2006-01-02")+".csv", []string{"Order ID", "Status", "Date created", "Date paid", "Date completed",
		"Customer ID", "Billing name", "Billing company", "Billing email", "Billing phone", "Billing address", "Shipping name", "Shipping address",
		"Payment method", "Transaction ID", "Currency", "Items subtotal", "Discount", "Shipping", "Tax", "Total", "Refunded", "Coupons", "Items", "Customer note"}, out)
}

func (s *Server) gooExportCouponsCSV(c *gin.Context) {
	if !s.gooExportAllowed(c) {
		return
	}
	ctx := c.Request.Context()
	coupons, _, err := s.gooQueryCoupons(ctx, c.Query("post_status"), strings.TrimSpace(c.Query("s")), 1, 1_000_000)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	ids := func(v []int64) string {
		var p []string
		for _, id := range v {
			p = append(p, itoa64(id))
		}
		return strings.Join(p, ", ")
	}
	num := func(v *int) string {
		if v == nil {
			return ""
		}
		return strconv.Itoa(*v)
	}
	var out [][]string
	for _, cp := range coupons {
		var min, max string
		if cp.MinimumAmount != nil {
			min = s.gooFmtDec(*cp.MinimumAmount)
		}
		if cp.MaximumAmount != nil {
			max = s.gooFmtDec(*cp.MaximumAmount)
		}
		out = append(out, []string{itoa64(cp.ID), cp.Code, cp.Status, cp.DiscountType, s.gooFmtDec(cp.Amount), cp.Description, s.gooExportTime(cp.DateExpires),
			gooBool01(cp.FreeShipping), min, max, gooBool01(cp.IndividualUse), gooBool01(cp.ExcludeSaleItems), ids(cp.ProductIDs), ids(cp.ExcludedProductIDs),
			ids(cp.ProductCategories), ids(cp.ExcludedProductCategories), strings.Join(cp.EmailRestrictions, ", "), num(cp.UsageLimit), num(cp.UsageLimitPerUser),
			num(cp.LimitUsageToXItems), strconv.Itoa(cp.UsageCount)})
	}
	gooCSV(c, "coupons-"+time.Now().In(s.loc()).Format("2006-01-02")+".csv", []string{"ID", "Code", "Status", "Discount type", "Amount", "Description", "Expiry date",
		"Free shipping", "Minimum spend", "Maximum spend", "Individual use only", "Exclude sale items", "Products", "Exclude products", "Categories", "Exclude categories",
		"Allowed emails", "Usage limit", "Usage limit per user", "Limit usage to X items", "Usage count"}, out)
}

func (s *Server) gooExportSubscriptionsCSV(c *gin.Context) {
	if !s.gooExportAllowed(c) {
		return
	}
	ctx := c.Request.Context()
	q := gsubQuery{Search: strings.TrimSpace(c.Query("s")), PerPage: -1}
	if st := c.Query("status"); st != "" {
		q.Status = []string{st}
	}
	subs, _, err := s.gsubList(ctx, q)
	if err != nil {
		c.String(500, err.Error())
		return
	}
	var out [][]string
	for _, sub := range subs {
		var items []string
		for _, it := range sub.Items {
			items = append(items, it.Name+" x "+strconv.Itoa(it.Qty))
		}
		b := sub.Billing
		out = append(out, []string{itoa64(sub.ID), sub.Status, itoa64(sub.ParentOrderID), itoa64(sub.CustomerID), strings.TrimSpace(b["first_name"] + " " + b["last_name"]), b["email"],
			sub.Period, strconv.Itoa(sub.Interval), sub.Currency, s.gooFmtDec(sub.RecurringTotal), s.gooFmtDec(sub.RecurringTax), s.gooExportTime(&sub.Start),
			s.gooExportTime(sub.TrialEnd), s.gooExportTime(sub.NextPayment), s.gooExportTime(sub.End), itoa64(sub.LastOrderID), sub.PaymentMethodTitle, strings.Join(items, "; ")})
	}
	gooCSV(c, "subscriptions-"+time.Now().In(s.loc()).Format("2006-01-02")+".csv", []string{"Subscription ID", "Status", "Parent order", "Customer ID", "Billing name",
		"Billing email", "Billing period", "Billing interval", "Currency", "Recurring total", "Recurring tax", "Start date", "Trial end", "Next payment", "End date",
		"Last order", "Payment method", "Items"}, out)
}
