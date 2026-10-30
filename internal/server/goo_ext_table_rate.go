package server

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Goocommerce Table Rate Shipping (WooCommerce Table Rate Shipping): a shipping method whose cost comes
// from a table of rates matched on the package's price, weight or item count and on shipping classes,
// calculated per order, per item, per line or per class. Rates are entered one per line in the method's
// "rates" field (the generic method settings are text-based):
//
//	condition|min|max|shipping_class_slug|row_cost|item_cost|cost_per_weight_unit|percent_cost|label|abort
//
// Abort rows hide the method; without "Calculate all matching rates" the first matching rate is used.

const gooTRKey = "table_rate"

// gooTRRate is one parsed rate row.
type gooTRRate struct {
	Condition string // none, price, weight, items, items_in_class
	Min, Max  *float64
	Class     string // "" or "*" any class, "none" no class, else a shipping class slug
	RowCost   float64
	ItemCost  float64
	PerWeight float64
	Percent   float64
	Label     string
	Abort     bool
	Line      int
}

func init() {
	gooRegisterExtension(&gooExtension{
		Key:         gooTRKey,
		Name:        "Table Rate Shipping",
		Description: "Add a \"Table rate\" shipping method to your zones: costs by price, weight, item count and shipping class, per order, item, line or class.",
		Icon:        "dashicons-editor-table",
		Category:    "Shipping & delivery",
	})
	gooRegisterShippingMethod(&gooShippingMethodDef{
		ID: "table_rate", Title: "Table rate", Module: gooTRKey,
		Description: "Table rates let you define shipping costs by weight, price, item count and shipping class, with handling fees and minimum / maximum costs.",
		Fields: []gooField{
			{ID: "title", Title: "Method title", Type: "text", Default: "Table rate"},
			{ID: "tax_status", Title: "Tax status", Type: "select", Default: "taxable", Options: []gooOpt{{"taxable", "Taxable"}, {"none", "None"}}},
			{ID: "calculation_type", Title: "Calculation type", Type: "select", Default: "order", Options: []gooOpt{
				{"order", "Per order: rates are calculated for the entire order"},
				{"item", "Calculated rates per item"},
				{"line", "Calculated rates per line item"},
				{"class", "Calculated rates per shipping class"}}},
			{ID: "calculate_all", Title: "Calculate all matching rates", Type: "checkbox", Default: "no", Desc: "Add up every matching rate instead of using the first one"},
			{ID: "handling_fee", Title: "Handling fee", Type: "text", Default: "", Holder: "0", Desc: "Added to the total: a fixed amount (2.50) or a percentage of the contents (5%)."},
			{ID: "min_cost", Title: "Minimum cost", Type: "text", Default: "", Holder: "n/a", Desc: "The shipping cost will never be lower than this."},
			{ID: "max_cost", Title: "Maximum cost", Type: "text", Default: "", Holder: "n/a", Desc: "The shipping cost will never be higher than this."},
			{ID: "rates", Title: "Rates", Type: "textarea", Default: "",
				Desc: "One rate per line: <code>condition|min|max|shipping_class_slug|row_cost|item_cost|cost_per_weight_unit|percent_cost|label|abort</code>. " +
					"Condition: <code>none</code>, <code>price</code>, <code>weight</code>, <code>items</code> or <code>items_in_class</code>; min / max empty for no limit. " +
					"Shipping class: empty or <code>*</code> for any, <code>none</code> for products without a class, else the class slug. " +
					"Row cost is charged once per order / item / line / class; item cost per item; cost per weight unit times the weight; percent cost of the contents. " +
					"An optional label replaces the method title; <code>abort</code> (or 1 / yes) makes the method unavailable when the row matches. Lines starting with # are ignored.<br>" +
					"Example: <code>weight|0|5||4.50|0|0|0|Light parcel|</code> and <code>weight|5||heavy|10|1.50|0.40|0||</code>."},
			{ID: "class_priorities", Title: "Class priorities", Type: "text", Default: "", Holder: "bulky:20, fragile:15",
				Desc: "Per order: when the cart mixes shipping classes, the class with the highest priority is used to match rates (<code>slug:priority</code>, comma separated; others count as 10)."},
		},
		Calculate: gooTableRate,
	})
}

// gooTRParseFloat reads a number, tolerating spaces, currency signs and decimal commas.
func gooTRParseFloat(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimRight(strings.TrimLeft(v, "$€£₺ "), "% ")
	if v == "" {
		return 0, false
	}
	if strings.Count(v, ",") == 1 && !strings.Contains(v, ".") {
		v = strings.ReplaceAll(v, ",", ".")
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// gooTRParseRates parses the rates field; malformed parts are read as empty / zero.
func gooTRParseRates(raw string) []gooTRRate {
	var out []gooTRRate
	for n, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		f := strings.Split(line, "|")
		for len(f) < 10 {
			f = append(f, "")
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		r := gooTRRate{Condition: strings.ToLower(f[0]), Class: strings.ToLower(f[3]), Label: f[8], Line: n + 1}
		switch r.Condition {
		case "price", "weight", "items", "items_in_class", "none":
		case "", "-":
			r.Condition = "none"
		case "item", "count", "quantity":
			r.Condition = "items"
		default:
			r.Condition = "none"
		}
		if v, ok := gooTRParseFloat(f[1]); ok {
			r.Min = &v
		}
		if v, ok := gooTRParseFloat(f[2]); ok {
			r.Max = &v
		}
		r.RowCost, _ = gooTRParseFloat(f[4])
		r.ItemCost, _ = gooTRParseFloat(f[5])
		r.PerWeight, _ = gooTRParseFloat(f[6])
		r.Percent, _ = gooTRParseFloat(f[7])
		switch strings.ToLower(f[9]) {
		case "1", "yes", "true", "abort", "y":
			r.Abort = true
		}
		out = append(out, r)
	}
	return out
}

// gooTRPriorities parses "slug:priority, slug:priority".
func gooTRPriorities(raw string) map[string]int {
	out := map[string]int{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		k, v, ok := strings.Cut(part, ":")
		if !ok {
			k, v, ok = strings.Cut(part, "=")
		}
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			out[strings.ToLower(strings.TrimSpace(k))] = n
		}
	}
	return out
}

// gooTRUnit is what a rate is matched against: the order, an item, a line or a class group.
type gooTRUnit struct {
	Class  string // the unit's shipping class slug ("" none); per order: the order class
	Price  float64
	Weight float64
	Items  float64
	// The totals of the class a rate targets, for per-order items_in_class and costs.
	byClass map[string][3]float64 // class → price, weight, items
	PerUnit float64               // per item: the quantity the cost is multiplied by
}

func (u *gooTRUnit) classTotals(class string) (float64, float64, float64) {
	if class == "" || class == "*" {
		return u.Price, u.Weight, u.Items
	}
	if class == "none" {
		class = ""
	}
	t := u.byClass[class]
	return t[0], t[1], t[2]
}

// gooTRClassMatches checks a rate's class against a unit.
func gooTRClassMatches(r gooTRRate, u *gooTRUnit) bool {
	switch r.Class {
	case "", "*", "any":
		return true
	case "none":
		return u.Class == ""
	}
	return r.Class == u.Class
}

// gooTRMatches checks a rate's condition and class against a unit.
func gooTRMatches(r gooTRRate, u *gooTRUnit) bool {
	if !gooTRClassMatches(r, u) {
		return false
	}
	var v float64
	switch r.Condition {
	case "none":
		return true
	case "price":
		v = u.Price
	case "weight":
		v = u.Weight
	case "items":
		v = u.Items
	case "items_in_class":
		_, _, v = u.classTotals(r.Class)
	}
	if r.Min != nil && v < *r.Min {
		return false
	}
	if r.Max != nil && v > *r.Max {
		return false
	}
	return true
}

// gooTRCost is a matched rate's cost for a unit.
func gooTRCost(r gooTRRate, u *gooTRUnit, calc string) float64 {
	if calc == "item" {
		return (r.RowCost + r.ItemCost + r.PerWeight*u.Weight + r.Percent/100*u.Price) * u.PerUnit
	}
	price, weight, items := u.Price, u.Weight, u.Items
	if calc == "order" {
		price, weight, items = u.classTotals(r.Class)
	}
	return r.RowCost + r.ItemCost*items + r.PerWeight*weight + r.Percent/100*price
}

// gooTableRate is the method's calculate_shipping().
func gooTableRate(s *Server, ctx context.Context, m *gooMethodInstance, pkg *gooPackage) []*gooShippingRate {
	if !s.gooExtOn(gooTRKey) || len(pkg.Items) == 0 {
		return nil
	}
	rates := gooTRParseRates(m.Setting("rates"))
	if len(rates) == 0 {
		return nil
	}
	calc := m.Setting("calculation_type")
	if calc != "item" && calc != "line" && calc != "class" {
		calc = "order"
	}
	all := m.Setting("calculate_all") == "yes"
	incl := s.gooPricesIncludeTax()

	// Item facts: class slug, line price, unit weight.
	slugs := map[int64]string{}
	classOf := func(it *gooCartItem) string {
		id := s.gooShippingClassOf(ctx, it.product)
		if id == 0 {
			return ""
		}
		if v, ok := slugs[id]; ok {
			return v
		}
		slug := ""
		if t, err := s.store.GetTerm(ctx, id); err == nil {
			slug = strings.ToLower(t.Slug)
		}
		slugs[id] = slug
		return slug
	}
	linePrice := func(it *gooCartItem) float64 {
		if incl {
			return it.LineTotal + it.LineTax
		}
		return it.LineTotal
	}

	var units []*gooTRUnit
	switch calc {
	case "order":
		u := &gooTRUnit{byClass: map[string][3]float64{}}
		classes := map[string]bool{}
		for _, it := range pkg.Items {
			cl := classOf(it)
			classes[cl] = true
			p, w, q := linePrice(it), s.gooWeightOf(ctx, it.product)*float64(it.Quantity), float64(it.Quantity)
			u.Price += p
			u.Weight += w
			u.Items += q
			t := u.byClass[cl]
			u.byClass[cl] = [3]float64{t[0] + p, t[1] + w, t[2] + q}
		}
		// The order class: the only class, else the one with the highest priority.
		prio := gooTRPriorities(m.Setting("class_priorities"))
		var list []string
		for cl := range classes {
			list = append(list, cl)
		}
		sort.Slice(list, func(i, j int) bool {
			pi, pj := 10, 10
			if v, ok := prio[list[i]]; ok {
				pi = v
			}
			if v, ok := prio[list[j]]; ok {
				pj = v
			}
			if list[i] == "" {
				pi = -1 << 30
			}
			if list[j] == "" {
				pj = -1 << 30
			}
			if pi != pj {
				return pi > pj
			}
			return list[i] < list[j]
		})
		if len(list) > 0 {
			u.Class = list[0]
		}
		units = append(units, u)
	case "item":
		for _, it := range pkg.Items {
			q := float64(it.Quantity)
			if q <= 0 {
				continue
			}
			units = append(units, &gooTRUnit{Class: classOf(it), Price: linePrice(it) / q, Weight: s.gooWeightOf(ctx, it.product), Items: 1, PerUnit: q})
		}
	case "line":
		for _, it := range pkg.Items {
			units = append(units, &gooTRUnit{Class: classOf(it), Price: linePrice(it), Weight: s.gooWeightOf(ctx, it.product) * float64(it.Quantity), Items: float64(it.Quantity)})
		}
	case "class":
		groups := map[string]*gooTRUnit{}
		var order []string
		for _, it := range pkg.Items {
			cl := classOf(it)
			g := groups[cl]
			if g == nil {
				g = &gooTRUnit{Class: cl}
				groups[cl] = g
				order = append(order, cl)
			}
			g.Price += linePrice(it)
			g.Weight += s.gooWeightOf(ctx, it.product) * float64(it.Quantity)
			g.Items += float64(it.Quantity)
		}
		for _, cl := range order {
			units = append(units, groups[cl])
		}
	}
	for _, u := range units {
		if u.byClass == nil {
			u.byClass = map[string][3]float64{u.Class: {u.Price, u.Weight, u.Items}}
		}
	}

	var total float64
	label := ""
	for _, u := range units {
		matched := false
		for _, r := range rates {
			if !gooTRMatches(r, u) {
				continue
			}
			if r.Abort {
				return nil // an abort row hides the method
			}
			matched = true
			total += gooTRCost(r, u, calc)
			if label == "" && r.Label != "" {
				label = r.Label
			}
			if !all {
				break
			}
		}
		if !matched {
			return nil // a unit without a rate: the method is not available
		}
	}
	if v, ok := gooTRParseFloat(m.Setting("min_cost")); ok && v > 0 && total < v {
		total = v
	}
	if v, ok := gooTRParseFloat(m.Setting("max_cost")); ok && v > 0 && total > v {
		total = v
	}
	if fee := strings.TrimSpace(m.Setting("handling_fee")); fee != "" {
		if v, ok := gooTRParseFloat(fee); ok {
			if strings.HasSuffix(fee, "%") {
				contents := pkg.Contents
				if incl {
					contents += pkg.ContentsTax
				}
				v = contents * v / 100
			}
			total += v
		}
	}
	if total < 0 {
		total = 0
	}
	return []*gooShippingRate{{MethodID: m.MethodID, InstanceID: m.InstanceID, Label: defaultStr(label, m.Title()), Cost: gooRound(total, s.gooDecimals()),
		Taxable: m.Setting("tax_status") != "none"}}
}
