package server

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Taxes (WC_Tax): rates by class and location, matched by priority, applied exclusive or inclusive,
// with compound rates applied on top of the others.

// gooTaxRate is a row of a tax rate table.
type gooTaxRate struct {
	ID        int64    `json:"id"`
	Country   string   `json:"country"`
	State     string   `json:"state"`
	Postcodes []string `json:"postcodes"`
	Cities    []string `json:"cities"`
	Rate      float64  `json:"rate"`
	Name      string   `json:"name"`
	Priority  int      `json:"priority"`
	Compound  bool     `json:"compound"`
	Shipping  bool     `json:"shipping"`
	Position  int      `json:"order"`
	Class     string   `json:"class"`
}

// gooLocation is a tax / shipping location.
type gooLocation struct {
	Country, State, Postcode, City string
}

// gooTaxRates are every rate of a class in table order.
func (s *Server) gooTaxRates(ctx context.Context, class string) ([]gooTaxRate, error) {
	q := `SELECT id, country, state, postcodes, cities, rate::float8, name, priority, compound, shipping, position, class FROM goo_tax_rates`
	args := []any{}
	if class != "*" {
		q += ` WHERE class = $1`
		args = append(args, class)
	}
	rows, err := s.store.Pool.Query(ctx, q+` ORDER BY class, position, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []gooTaxRate
	for rows.Next() {
		var r gooTaxRate
		if err := rows.Scan(&r.ID, &r.Country, &r.State, &r.Postcodes, &r.Cities, &r.Rate, &r.Name, &r.Priority, &r.Compound, &r.Shipping, &r.Position, &r.Class); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// gooSaveTaxRate inserts (ID 0) or updates a rate.
func (s *Server) gooSaveTaxRate(ctx context.Context, r *gooTaxRate) error {
	r.Country, r.State = strings.ToUpper(strings.TrimSpace(r.Country)), strings.ToUpper(strings.TrimSpace(r.State))
	if r.Country == "*" {
		r.Country = ""
	}
	if r.State == "*" {
		r.State = ""
	}
	if r.Name == "" {
		r.Name = "Tax"
	}
	if r.Priority < 1 {
		r.Priority = 1
	}
	if r.Postcodes == nil {
		r.Postcodes = []string{}
	}
	if r.Cities == nil {
		r.Cities = []string{}
	}
	if r.ID == 0 {
		return s.store.Pool.QueryRow(ctx, `INSERT INTO goo_tax_rates (country, state, postcodes, cities, rate, name, priority, compound, shipping, position, class)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, r.Country, r.State, r.Postcodes, r.Cities, r.Rate, r.Name, r.Priority,
			r.Compound, r.Shipping, r.Position, r.Class).Scan(&r.ID)
	}
	_, err := s.store.Pool.Exec(ctx, `UPDATE goo_tax_rates SET country=$2, state=$3, postcodes=$4, cities=$5, rate=$6, name=$7, priority=$8,
		compound=$9, shipping=$10, position=$11, class=$12 WHERE id=$1`, r.ID, r.Country, r.State, r.Postcodes, r.Cities, r.Rate, r.Name,
		r.Priority, r.Compound, r.Shipping, r.Position, r.Class)
	return err
}

// gooPostcodeMatch is wc_postcode_location_matcher() for one pattern: exact, "902*" wildcard or "90210...90299" range.
func gooPostcodeMatch(pattern, postcode string) bool {
	norm := func(v string) string { return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(v), " ", "")) }
	p, pc := norm(pattern), norm(postcode)
	if p == "" {
		return true
	}
	if lo, hi, ok := strings.Cut(p, "..."); ok {
		a, e1 := strconv.Atoi(lo)
		b, e2 := strconv.Atoi(hi)
		n, e3 := strconv.Atoi(pc)
		if e1 == nil && e2 == nil && e3 == nil {
			return n >= a && n <= b
		}
		return pc >= lo && pc <= hi
	}
	if strings.HasSuffix(p, "*") {
		return strings.HasPrefix(pc, strings.TrimSuffix(p, "*"))
	}
	return p == pc
}

// gooFindRates is WC_Tax::find_rates(): the matching rates of a class, one per priority, in priority order.
func (s *Server) gooFindRates(ctx context.Context, class string, loc gooLocation, forShipping bool) []gooTaxRate {
	if !s.gooTaxEnabled() || loc.Country == "" {
		return nil
	}
	all, err := s.gooTaxRates(ctx, class)
	if err != nil {
		return nil
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Priority != all[j].Priority {
			return all[i].Priority < all[j].Priority
		}
		return all[i].Position < all[j].Position
	})
	var out []gooTaxRate
	used := map[int]bool{}
	for _, r := range all {
		if used[r.Priority] {
			continue
		}
		if forShipping && !r.Shipping {
			continue
		}
		if r.Country != "" && !strings.EqualFold(r.Country, loc.Country) {
			continue
		}
		if r.State != "" && !strings.EqualFold(r.State, loc.State) {
			continue
		}
		if len(r.Postcodes) > 0 {
			ok := false
			for _, p := range r.Postcodes {
				if gooPostcodeMatch(p, loc.Postcode) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		if len(r.Cities) > 0 {
			ok := false
			for _, c := range r.Cities {
				if strings.EqualFold(strings.TrimSpace(c), strings.TrimSpace(loc.City)) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		used[r.Priority] = true
		out = append(out, r)
	}
	return out
}

// gooBaseLocation is the store's address as a tax location.
func (s *Server) gooBaseLocation() gooLocation {
	return gooLocation{Country: s.gooBaseCountry(), State: s.gooBaseState(), Postcode: s.gooOptString("store_postcode"), City: s.gooOptString("store_city")}
}

// gooCalcTax is WC_Tax::calc_tax(): taxes per rate id of a price, inclusive or exclusive. Amounts
// are not rounded (rounding happens per line or at subtotal by the caller).
func gooCalcTax(price float64, rates []gooTaxRate, inclusive bool) map[int64]float64 {
	taxes := map[int64]float64{}
	if len(rates) == 0 {
		return taxes
	}
	if inclusive {
		var compound, regular []gooTaxRate
		var regularSum float64
		for _, r := range rates {
			if r.Compound {
				compound = append(compound, r)
			} else {
				regular = append(regular, r)
				regularSum += r.Rate
			}
		}
		nonCompound := price
		for i := len(compound) - 1; i >= 0; i-- {
			r := compound[i]
			t := nonCompound - nonCompound/(1+r.Rate/100)
			taxes[r.ID] += t
			nonCompound -= t
		}
		regularRate := 1 + regularSum/100
		for _, r := range regular {
			the := (r.Rate / 100) / regularRate
			net := price - the*nonCompound
			taxes[r.ID] += price - net
		}
		return taxes
	}
	var preCompound float64
	for _, r := range rates {
		if r.Compound {
			continue
		}
		t := price * r.Rate / 100
		taxes[r.ID] += t
		preCompound += t
	}
	compoundBase := price + preCompound
	for _, r := range rates {
		if !r.Compound {
			continue
		}
		t := compoundBase * r.Rate / 100
		taxes[r.ID] += t
		compoundBase += t
	}
	return taxes
}

func gooSumTaxes(t map[int64]float64) float64 {
	var sum float64
	for _, v := range t {
		sum += v
	}
	return sum
}

// gooRoundTaxes rounds every rate's amount to dp decimals.
func gooRoundTaxes(t map[int64]float64, dp int) map[int64]float64 {
	out := make(map[int64]float64, len(t))
	for k, v := range t {
		out[k] = gooRound(v, dp)
	}
	return out
}

// gooTaxRateByID loads one rate (labels of order tax lines).
func (s *Server) gooTaxRateByID(ctx context.Context, id int64) *gooTaxRate {
	var r gooTaxRate
	if err := s.store.Pool.QueryRow(ctx, `SELECT id, country, state, postcodes, cities, rate::float8, name, priority, compound, shipping, position, class
		FROM goo_tax_rates WHERE id = $1`, id).Scan(&r.ID, &r.Country, &r.State, &r.Postcodes, &r.Cities, &r.Rate, &r.Name, &r.Priority,
		&r.Compound, &r.Shipping, &r.Position, &r.Class); err != nil {
		return nil
	}
	return &r
}

// gooTaxRateCode is WC_Tax::get_rate_code(): COUNTRY-STATE-NAME-PRIORITY.
func gooTaxRateCode(r *gooTaxRate) string {
	parts := []string{}
	if r.Country != "" {
		parts = append(parts, r.Country)
	}
	if r.State != "" {
		parts = append(parts, r.State)
	}
	name := strings.ToUpper(strings.ReplaceAll(r.Name, " ", "-"))
	if name == "" {
		name = "TAX"
	}
	parts = append(parts, name, strconv.Itoa(r.Priority))
	return strings.Join(parts, "-")
}

// gooPriceIncludingTax is wc_get_price_including_tax() for display at the base location (shop pages
// show prices for the store's location unless the customer's is known).
func (s *Server) gooPriceIncludingTax(ctx context.Context, p *gooProduct, price float64, qty int, loc gooLocation) float64 {
	line := price * float64(qty)
	if !s.gooTaxEnabled() || p.TaxStatus != "taxable" {
		return line
	}
	class := s.gooTaxClassOf(ctx, p)
	if s.gooPricesIncludeTax() {
		base := s.gooFindRates(ctx, class, s.gooBaseLocation(), false)
		rates := s.gooFindRates(ctx, class, loc, false)
		if gooSameRates(base, rates) {
			return line
		}
		ex := line - gooSumTaxes(gooCalcTax(line, base, true))
		return ex + gooSumTaxes(gooCalcTax(ex, rates, false))
	}
	rates := s.gooFindRates(ctx, class, loc, false)
	return line + gooSumTaxes(gooCalcTax(line, rates, false))
}

// gooPriceExcludingTax is wc_get_price_excluding_tax().
func (s *Server) gooPriceExcludingTax(ctx context.Context, p *gooProduct, price float64, qty int, loc gooLocation) float64 {
	line := price * float64(qty)
	if !s.gooTaxEnabled() || p.TaxStatus != "taxable" || !s.gooPricesIncludeTax() {
		return line
	}
	rates := s.gooFindRates(ctx, s.gooTaxClassOf(ctx, p), s.gooBaseLocation(), false)
	return line - gooSumTaxes(gooCalcTax(line, rates, true))
}

// gooPriceToDisplay is wc_get_price_to_display() under the "Display prices in the shop" option.
func (s *Server) gooPriceToDisplay(ctx context.Context, p *gooProduct, price float64, loc gooLocation) float64 {
	if s.gooOptString("tax_display_shop") == "incl" {
		return s.gooPriceIncludingTax(ctx, p, price, 1, loc)
	}
	return s.gooPriceExcludingTax(ctx, p, price, 1, loc)
}

func gooSameRates(a, b []gooTaxRate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}
