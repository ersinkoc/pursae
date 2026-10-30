package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Shipping (WC_Shipping): zones matched by the destination, their method instances, and the rates
// those methods calculate for a package. Flat rate, free shipping and local pickup are built in;
// modules add methods (Table Rate Shipping) through gooShippingMethods.

// gooZoneLocation is a location of a zone: a country "US", a state "US:CA", a continent "EU" or a postcode.
type gooZoneLocation struct {
	Code string `json:"code"`
	Type string `json:"type"` // country, state, continent, postcode
}

// gooZone is a shipping zone; zone 0 is "Locations not covered by your other zones".
type gooZone struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Position  int               `json:"order"`
	Locations []gooZoneLocation `json:"locations"`
	Methods   []*gooMethodInstance
}

// gooMethodInstance is a method added to a zone (flat_rate:3).
type gooMethodInstance struct {
	InstanceID int64          `json:"instance_id"`
	ZoneID     int64          `json:"zone_id"`
	MethodID   string         `json:"method_id"`
	Position   int            `json:"order"`
	Enabled    bool           `json:"enabled"`
	Settings   map[string]any `json:"settings"`
}

func (m *gooMethodInstance) Setting(key string) string {
	switch v := m.Settings[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "yes"
		}
		return "no"
	}
	if def := gooShippingMethods[m.MethodID]; def != nil {
		for _, f := range def.Fields {
			if f.ID == key {
				if s, ok := f.Default.(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// Title is the method title customers see.
func (m *gooMethodInstance) Title() string {
	if t := m.Setting("title"); t != "" {
		return t
	}
	if def := gooShippingMethods[m.MethodID]; def != nil {
		return def.Title
	}
	return m.MethodID
}

// gooShippingRate is a calculated rate (WC_Shipping_Rate).
type gooShippingRate struct {
	ID         string            `json:"id"` // method_id:instance_id
	MethodID   string            `json:"method_id"`
	InstanceID int64             `json:"instance_id"`
	Label      string            `json:"label"`
	Cost       float64           `json:"cost"`
	Taxes      map[int64]float64 `json:"taxes"`
	Taxable    bool              `json:"taxable"`
	Meta       map[string]string `json:"meta_data,omitempty"`
}

// gooPackage is a shipping package: the items that need shipping and the destination.
type gooPackage struct {
	Items       []*gooCartItem
	Contents    float64 // contents cost (line totals after discounts)
	ContentsTax float64
	Subtotal    float64 // before discounts, including tax when displayed so
	Destination gooLocation
	Coupons     []*gooCoupon
	cart        *gooCart
}

// gooShippingMethodDef is a shipping method type.
type gooShippingMethodDef struct {
	ID, Title, Description string
	Module                 string // the extension that provides it, "" for core
	Fields                 []gooField
	// Calculate returns the rates of an instance for a package (none when the method does not apply).
	Calculate func(s *Server, ctx context.Context, m *gooMethodInstance, pkg *gooPackage) []*gooShippingRate
}

var gooShippingMethods = map[string]*gooShippingMethodDef{}

func gooRegisterShippingMethod(d *gooShippingMethodDef) { gooShippingMethods[d.ID] = d }

// gooShippingMethodOrder lists the method types for the "Add shipping method" modal.
func (s *Server) gooShippingMethodOrder() []*gooShippingMethodDef {
	var out []*gooShippingMethodDef
	for _, d := range gooShippingMethods {
		if d.Module == "" || s.gooExtOn(d.Module) {
			out = append(out, d)
		}
	}
	order := map[string]int{"flat_rate": 0, "free_shipping": 1, "local_pickup": 2}
	sort.Slice(out, func(i, j int) bool {
		oi, ok1 := order[out[i].ID]
		oj, ok2 := order[out[j].ID]
		if !ok1 {
			oi = 10
		}
		if !ok2 {
			oj = 10
		}
		if oi != oj {
			return oi < oj
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func init() {
	taxStatus := gooField{ID: "tax_status", Title: "Tax status", Type: "select", Default: "taxable", Options: []gooOpt{{"taxable", "Taxable"}, {"none", "None"}}}
	gooRegisterShippingMethod(&gooShippingMethodDef{ID: "flat_rate", Title: "Flat rate", Description: "Lets you charge a fixed rate for shipping.",
		Fields: []gooField{
			{ID: "title", Title: "Method title", Type: "text", Default: "Flat rate"},
			taxStatus,
			{ID: "cost", Title: "Cost", Type: "text", Default: "0", Holder: "0",
				Desc: "Enter a cost (excl. tax) or sum, e.g. <code>10.00 * [qty]</code>. Use <code>[qty]</code> for the number of items, <code>[cost]</code> for the total cost of items, and <code>[fee percent=\"10\" min_fee=\"20\" max_fee=\"\"]</code> for percentage based fees."},
			{ID: "class_costs", Title: "Shipping class costs", Type: "title", Desc: "These costs can optionally be added based on the product shipping class."},
			{ID: "no_class_cost", Title: "No shipping class cost", Type: "text", Default: ""},
			{ID: "type", Title: "Calculation type", Type: "select", Default: "class", Options: []gooOpt{{"class", "Per class: Charge shipping for each shipping class individually"}, {"order", "Per order: Charge shipping for the most expensive shipping class"}}},
		},
		Calculate: gooFlatRate})
	gooRegisterShippingMethod(&gooShippingMethodDef{ID: "free_shipping", Title: "Free shipping", Description: "Free shipping is a special method which can be triggered with coupons and minimum spends.",
		Fields: []gooField{
			{ID: "title", Title: "Title", Type: "text", Default: "Free shipping"},
			{ID: "requires", Title: "Free shipping requires…", Type: "select", Default: "", Options: []gooOpt{{"", "No requirement"}, {"coupon", "A valid free shipping coupon"},
				{"min_amount", "A minimum order amount"}, {"either", "A minimum order amount OR a coupon"}, {"both", "A minimum order amount AND a coupon"}}},
			{ID: "min_amount", Title: "Minimum order amount", Type: "price", Default: "0"},
			{ID: "ignore_discounts", Title: "Coupons discounts", Type: "checkbox", Default: "no", Desc: "Apply minimum order rule before coupon discount"},
		},
		Calculate: gooFreeShipping})
	gooRegisterShippingMethod(&gooShippingMethodDef{ID: "local_pickup", Title: "Local pickup", Description: "Allow customers to pick up orders themselves. By default, when using local pickup store base taxes will apply regardless of customer address.",
		Fields: []gooField{
			{ID: "title", Title: "Title", Type: "text", Default: "Local pickup"},
			taxStatus,
			{ID: "cost", Title: "Cost", Type: "text", Default: "0"},
		},
		Calculate: func(s *Server, ctx context.Context, m *gooMethodInstance, pkg *gooPackage) []*gooShippingRate {
			cost, _ := strconv.ParseFloat(strings.TrimSpace(m.Setting("cost")), 64)
			return []*gooShippingRate{{MethodID: m.MethodID, InstanceID: m.InstanceID, Label: m.Title(), Cost: cost, Taxable: m.Setting("tax_status") != "none"}}
		}})
}

// gooFlatRate is WC_Shipping_Flat_Rate::calculate_shipping().
func gooFlatRate(s *Server, ctx context.Context, m *gooMethodInstance, pkg *gooPackage) []*gooShippingRate {
	qty := 0
	for _, it := range pkg.Items {
		qty += it.Quantity
	}
	cost, ok := gooEvalCost(m.Setting("cost"), qty, pkg.Contents)
	if !ok {
		return nil
	}
	// Shipping class costs.
	classes := map[int64][]*gooCartItem{}
	for _, it := range pkg.Items {
		classes[s.gooShippingClassOf(ctx, it.product)] = append(classes[s.gooShippingClassOf(ctx, it.product)], it)
	}
	hasClassCosts := false
	for k := range m.Settings {
		if strings.HasPrefix(k, "class_cost_") && m.Setting(k) != "" {
			hasClassCosts = true
		}
	}
	if hasClassCosts || m.Setting("no_class_cost") != "" {
		var highest, sum float64
		for classID, items := range classes {
			raw := m.Setting("no_class_cost")
			if classID != 0 {
				raw = m.Setting("class_cost_" + itoa64(classID))
			}
			if strings.TrimSpace(raw) == "" {
				continue
			}
			q, contents := 0, 0.0
			for _, it := range items {
				q += it.Quantity
				contents += it.LineTotal
			}
			c, ok := gooEvalCost(raw, q, contents)
			if !ok {
				continue
			}
			sum += c
			highest = math.Max(highest, c)
		}
		if m.Setting("type") == "order" {
			cost += highest
		} else {
			cost += sum
		}
	}
	return []*gooShippingRate{{MethodID: m.MethodID, InstanceID: m.InstanceID, Label: m.Title(), Cost: cost, Taxable: m.Setting("tax_status") != "none"}}
}

// gooFreeShipping is WC_Shipping_Free_Shipping::is_available().
func gooFreeShipping(s *Server, ctx context.Context, m *gooMethodInstance, pkg *gooPackage) []*gooShippingRate {
	hasCoupon := false
	for _, c := range pkg.Coupons {
		if c.FreeShipping {
			hasCoupon = true
		}
	}
	minAmount, _ := strconv.ParseFloat(strings.TrimSpace(m.Setting("min_amount")), 64)
	total := pkg.Subtotal
	if m.Setting("ignore_discounts") != "yes" && pkg.cart != nil {
		total = pkg.cart.displayedContentsAfterDiscount()
	}
	hasMin := total >= gooRound(minAmount, s.gooDecimals())
	ok := true
	switch m.Setting("requires") {
	case "coupon":
		ok = hasCoupon
	case "min_amount":
		ok = hasMin
	case "either":
		ok = hasMin || hasCoupon
	case "both":
		ok = hasMin && hasCoupon
	}
	if !ok {
		return nil
	}
	return []*gooShippingRate{{MethodID: m.MethodID, InstanceID: m.InstanceID, Label: m.Title(), Cost: 0}}
}

var gooFeeRe = regexp.MustCompile(`\[fee([^\]]*)\]`)
var gooFeeAttrRe = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)

// gooEvalCost evaluates a flat rate cost: numbers, + - * / ( ), [qty], [cost] and [fee …].
func gooEvalCost(expr string, qty int, cost float64) (float64, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return 0, true
	}
	expr = gooFeeRe.ReplaceAllStringFunc(expr, func(m string) string {
		attrs := map[string]float64{}
		for _, a := range gooFeeAttrRe.FindAllStringSubmatch(m, -1) {
			attrs[a[1]], _ = strconv.ParseFloat(a[2], 64)
		}
		fee := cost * attrs["percent"] / 100
		if v := attrs["min_fee"]; v > 0 && fee < v {
			fee = v
		}
		if v := attrs["max_fee"]; v > 0 && fee > v {
			fee = v
		}
		return strconv.FormatFloat(fee, 'f', -1, 64)
	})
	expr = strings.ReplaceAll(expr, "[qty]", strconv.Itoa(qty))
	expr = strings.ReplaceAll(expr, "[cost]", strconv.FormatFloat(cost, 'f', -1, 64))
	expr = strings.ReplaceAll(expr, ",", ".")
	p := &gooExprParser{in: expr}
	v, ok := p.expr()
	p.skip()
	if !ok || p.pos != len(p.in) {
		return 0, false
	}
	return v, true
}

type gooExprParser struct {
	in  string
	pos int
}

func (p *gooExprParser) skip() {
	for p.pos < len(p.in) && unicode.IsSpace(rune(p.in[p.pos])) {
		p.pos++
	}
}

func (p *gooExprParser) expr() (float64, bool) {
	v, ok := p.term()
	for ok {
		p.skip()
		if p.pos >= len(p.in) || (p.in[p.pos] != '+' && p.in[p.pos] != '-') {
			break
		}
		op := p.in[p.pos]
		p.pos++
		var r float64
		r, ok = p.term()
		if op == '+' {
			v += r
		} else {
			v -= r
		}
	}
	return v, ok
}

func (p *gooExprParser) term() (float64, bool) {
	v, ok := p.factor()
	for ok {
		p.skip()
		if p.pos >= len(p.in) || (p.in[p.pos] != '*' && p.in[p.pos] != '/') {
			break
		}
		op := p.in[p.pos]
		p.pos++
		var r float64
		r, ok = p.factor()
		if op == '*' {
			v *= r
		} else if r != 0 {
			v /= r
		} else {
			return 0, false
		}
	}
	return v, ok
}

func (p *gooExprParser) factor() (float64, bool) {
	p.skip()
	if p.pos >= len(p.in) {
		return 0, false
	}
	switch c := p.in[p.pos]; {
	case c == '(':
		p.pos++
		v, ok := p.expr()
		p.skip()
		if !ok || p.pos >= len(p.in) || p.in[p.pos] != ')' {
			return 0, false
		}
		p.pos++
		return v, true
	case c == '-':
		p.pos++
		v, ok := p.factor()
		return -v, ok
	}
	start := p.pos
	for p.pos < len(p.in) && (p.in[p.pos] == '.' || (p.in[p.pos] >= '0' && p.in[p.pos] <= '9')) {
		p.pos++
	}
	v, err := strconv.ParseFloat(p.in[start:p.pos], 64)
	return v, err == nil
}

// Zones -------------------------------------------------------------------------------------------

// gooZones are the shipping zones in order, with their methods; withRest appends zone 0.
func (s *Server) gooZones(ctx context.Context, withRest bool) ([]*gooZone, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, name, position, locations FROM goo_shipping_zones ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	var zones []*gooZone
	for rows.Next() {
		z := &gooZone{}
		var locs []byte
		if err := rows.Scan(&z.ID, &z.Name, &z.Position, &locs); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(locs, &z.Locations)
		zones = append(zones, z)
	}
	rows.Close()
	if withRest {
		zones = append(zones, &gooZone{ID: 0, Name: "Locations not covered by your other zones"})
	}
	methods, err := s.gooMethodInstances(ctx, -1)
	if err != nil {
		return nil, err
	}
	for _, z := range zones {
		for _, m := range methods {
			if m.ZoneID == z.ID {
				z.Methods = append(z.Methods, m)
			}
		}
	}
	return zones, nil
}

// gooZone loads one zone (0 = the rest of the world) with its methods.
func (s *Server) gooZone(ctx context.Context, id int64) (*gooZone, error) {
	zones, err := s.gooZones(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, z := range zones {
		if z.ID == id {
			return z, nil
		}
	}
	return nil, fmt.Errorf("zone %d not found", id)
}

// gooMethodInstances lists method instances of a zone (-1 = every zone).
func (s *Server) gooMethodInstances(ctx context.Context, zoneID int64) ([]*gooMethodInstance, error) {
	q := `SELECT instance_id, zone_id, method_id, position, enabled, settings FROM goo_shipping_methods`
	args := []any{}
	if zoneID >= 0 {
		q += ` WHERE zone_id = $1`
		args = append(args, zoneID)
	}
	rows, err := s.store.Pool.Query(ctx, q+` ORDER BY zone_id, position, instance_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*gooMethodInstance
	for rows.Next() {
		m := &gooMethodInstance{}
		var st []byte
		if err := rows.Scan(&m.InstanceID, &m.ZoneID, &m.MethodID, &m.Position, &m.Enabled, &st); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(st, &m.Settings)
		if m.Settings == nil {
			m.Settings = map[string]any{}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// gooSaveZone inserts (ID 0, a new zone) or updates a zone's name, order and locations.
func (s *Server) gooSaveZone(ctx context.Context, z *gooZone) error {
	locs, _ := json.Marshal(nonNilSlice(z.Locations))
	if z.ID == 0 {
		return s.store.Pool.QueryRow(ctx, `INSERT INTO goo_shipping_zones (name, position, locations) VALUES ($1,$2,$3) RETURNING id`,
			z.Name, z.Position, locs).Scan(&z.ID)
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_shipping_zones SET name=$2, position=$3, locations=$4 WHERE id=$1`, z.ID, z.Name, z.Position, locs)
	return err
}

// gooDeleteZone deletes a zone and its methods.
func (s *Server) gooDeleteZone(ctx context.Context, id int64) error {
	if _, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_shipping_methods WHERE zone_id = $1`, id); err != nil {
		return err
	}
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_shipping_zones WHERE id = $1`, id)
	return err
}

// gooAddMethod adds a method instance to a zone with the method's default settings.
func (s *Server) gooAddMethod(ctx context.Context, zoneID int64, methodID string, settings map[string]any) (*gooMethodInstance, error) {
	def := gooShippingMethods[methodID]
	if def == nil {
		return nil, fmt.Errorf("unknown shipping method %q", methodID)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	for _, f := range def.Fields {
		if _, ok := settings[f.ID]; !ok && f.Type != "title" && f.Default != nil {
			settings[f.ID] = f.Default
		}
	}
	m := &gooMethodInstance{ZoneID: zoneID, MethodID: methodID, Enabled: true, Settings: settings}
	st, _ := json.Marshal(settings)
	err := s.store.Pool.QueryRow(ctx, `INSERT INTO goo_shipping_methods (zone_id, method_id, position, enabled, settings)
		VALUES ($1, $2, (SELECT coalesce(max(position),0)+1 FROM goo_shipping_methods WHERE zone_id = $1), true, $3) RETURNING instance_id, position`,
		zoneID, methodID, st).Scan(&m.InstanceID, &m.Position)
	return m, err
}

// gooSaveMethod writes a method instance's settings and enabled flag.
func (s *Server) gooSaveMethod(ctx context.Context, m *gooMethodInstance) error {
	st, _ := json.Marshal(m.Settings)
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_shipping_methods SET settings=$2, enabled=$3, position=$4 WHERE instance_id=$1`,
		m.InstanceID, st, m.Enabled, m.Position)
	return err
}

func (s *Server) gooDeleteMethod(ctx context.Context, instanceID int64) error {
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_shipping_methods WHERE instance_id = $1`, instanceID)
	return err
}

// gooZoneMatches reports whether a destination is in a zone (WC_Shipping_Zone_Data_Store::get_zone_id_from_package).
func gooZoneMatches(z *gooZone, dest gooLocation) bool {
	if len(z.Locations) == 0 {
		return false
	}
	region, postcodes := false, []string{}
	hasRegion := false
	for _, l := range z.Locations {
		switch l.Type {
		case "postcode":
			postcodes = append(postcodes, l.Code)
		case "country":
			hasRegion = true
			if strings.EqualFold(l.Code, dest.Country) {
				region = true
			}
		case "state":
			hasRegion = true
			if strings.EqualFold(l.Code, dest.Country+":"+dest.State) {
				region = true
			}
		case "continent":
			hasRegion = true
			if gooContinentOf(dest.Country) == l.Code {
				region = true
			}
		}
	}
	if hasRegion && !region {
		return false
	}
	if len(postcodes) > 0 {
		for _, p := range postcodes {
			if gooPostcodeMatch(p, dest.Postcode) {
				return true
			}
		}
		return false
	}
	return hasRegion
}

// gooMatchZone is WC_Shipping_Zones::get_zone_matching_package(): the first matching zone, else zone 0.
func (s *Server) gooMatchZone(ctx context.Context, dest gooLocation) *gooZone {
	zones, err := s.gooZones(ctx, true)
	if err != nil || len(zones) == 0 {
		return &gooZone{ID: 0, Name: "Locations not covered by your other zones"}
	}
	for _, z := range zones {
		if z.ID != 0 && gooZoneMatches(z, dest) {
			return z
		}
	}
	return zones[len(zones)-1]
}

// gooPackageRates calculates the rates of a package's zone (enabled instances, in order).
func (s *Server) gooPackageRates(ctx context.Context, pkg *gooPackage) []*gooShippingRate {
	zone := s.gooMatchZone(ctx, pkg.Destination)
	var out []*gooShippingRate
	for _, m := range zone.Methods {
		def := gooShippingMethods[m.MethodID]
		if !m.Enabled || def == nil || def.Calculate == nil || (def.Module != "" && !s.gooExtOn(def.Module)) {
			continue
		}
		for _, r := range def.Calculate(s, ctx, m, pkg) {
			if r.ID == "" {
				r.ID = r.MethodID + ":" + itoa64(r.InstanceID)
			}
			if r.Taxes == nil {
				r.Taxes = map[int64]float64{}
			}
			out = append(out, r)
		}
	}
	return out
}
