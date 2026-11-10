package server

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Settings › Tax › <class> rates: WooCommerce's editable tax rates grid (Country code, State code,
// Postcode / ZIP, City, Rate %, Tax name, Priority, Compound, Shipping), saved by a form post, with
// CSV import and export.

type gooTaxGrid struct {
	Class     string
	ClassName string
	Rows      []gooTaxGridRow
	ExportURL string
}

type gooTaxGridRow struct {
	ID                                            string
	Country, State, Postcodes, Cities, Rate, Name string
	Priority                                      int
	Compound, Shipping                            bool
	Order                                         int
}

// gooTaxClassBySection maps a section id back to a tax class slug ("standard" → "").
func (s *Server) gooTaxClassBySection(section string) (string, string, bool) {
	for _, cl := range s.gooTaxClasses() {
		if gooTaxSection(cl.Value) == section {
			return cl.Value, cl.Label, true
		}
	}
	return "", "", false
}

func (s *Server) gooTaxRatesView(c *gin.Context, d *gooSettingsData, section string) {
	class, name, ok := s.gooTaxClassBySection(section)
	if !ok {
		d.Kind = "fields"
		d.Body = `<p>Invalid tax class.</p>`
		d.HideSave = true
		return
	}
	if c.Query("export") != "" {
		s.gooTaxExport(c, class)
		c.Abort()
		return
	}
	d.Kind = "taxrates"
	g := &gooTaxGrid{Class: class, ClassName: name, ExportURL: gooAdminURL("settings", "tab", "tax", "section", section, "export", "1")}
	rates, _ := s.gooTaxRates(c.Request.Context(), class)
	for _, r := range rates {
		g.Rows = append(g.Rows, gooTaxGridRow{ID: itoa64(r.ID), Country: r.Country, State: r.State, Postcodes: strings.Join(r.Postcodes, "; "),
			Cities: strings.Join(r.Cities, "; "), Rate: strconv.FormatFloat(r.Rate, 'f', -1, 64), Name: r.Name, Priority: r.Priority,
			Compound: r.Compound, Shipping: r.Shipping, Order: r.Position})
	}
	d.Tax = g
	d.Title = name + " rates"
}

func gooSplitList(v string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == '\n' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// gooTaxRatesPost saves the grid (updates, new rows "new-N", removals) or imports a CSV.
func (s *Server) gooTaxRatesPost(c *gin.Context, section string) (string, string) {
	ctx := c.Request.Context()
	class, _, ok := s.gooTaxClassBySection(section)
	if !ok {
		return "", "Invalid tax class."
	}
	if c.PostForm("goo_do") == "import" {
		n, err := s.gooTaxImport(c, class)
		if err != nil {
			return "", err.Error()
		}
		return plural(n, "Import complete - imported 1 tax rate.", "Import complete - imported %d tax rates."), ""
	}
	form := c.Request.PostForm
	var ids []string
	for k := range form {
		if id, ok := strings.CutPrefix(k, "tax_rate_country["); ok {
			ids = append(ids, strings.TrimSuffix(id, "]"))
		}
	}
	sort.Strings(ids)
	get := func(field, id string) string { return strings.TrimSpace(c.PostForm(field + "[" + id + "]")) }
	type pending struct {
		r     *gooTaxRate
		order int
	}
	var list []pending
	removed := 0
	for _, id := range ids {
		n, _ := strconv.ParseInt(id, 10, 64)
		if get("remove_tax_rate", id) != "" {
			if n > 0 {
				_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_tax_rates WHERE id = $1 AND class = $2`, n, class)
				removed++
			}
			continue
		}
		r := &gooTaxRate{ID: n, Class: class, Country: get("tax_rate_country", id), State: get("tax_rate_state", id),
			Postcodes: gooSplitList(get("tax_rate_postcode", id)), Cities: gooSplitList(get("tax_rate_city", id)),
			Name: get("tax_rate_name", id), Compound: get("tax_rate_compound", id) != "", Shipping: get("tax_rate_shipping", id) != ""}
		r.Rate, _ = strconv.ParseFloat(strings.ReplaceAll(get("tax_rate", id), ",", "."), 64)
		r.Priority, _ = strconv.Atoi(get("tax_rate_priority", id))
		if n == 0 { // a new row: skip it when left empty
			if r.Country == "" && r.State == "" && len(r.Postcodes) == 0 && len(r.Cities) == 0 && get("tax_rate", id) == "" && r.Name == "" {
				continue
			}
		}
		order, err := strconv.Atoi(get("tax_rate_order", id))
		if err != nil {
			order = 1 << 20
		}
		list = append(list, pending{r, order})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].order < list[j].order })
	for i, p := range list {
		p.r.Position = i
		if err := s.gooSaveTaxRate(ctx, p.r); err != nil {
			return "", err.Error()
		}
	}
	s.purgeAll(ctx)
	return "Your settings have been saved.", ""
}

var gooTaxCSVHeader = []string{"Country code", "State code", "Postcode / ZIP", "City", "Rate %", "Tax name", "Priority", "Compound", "Shipping", "Tax class"}

func (s *Server) gooTaxExport(c *gin.Context, class string) {
	rates, _ := s.gooTaxRates(c.Request.Context(), class)
	var rows [][]string
	b := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	for _, r := range rates {
		rows = append(rows, []string{defaultStr(r.Country, "*"), defaultStr(r.State, "*"), defaultStr(strings.Join(r.Postcodes, ";"), "*"),
			defaultStr(strings.Join(r.Cities, ";"), "*"), strconv.FormatFloat(r.Rate, 'f', 4, 64), r.Name, strconv.Itoa(r.Priority), b(r.Compound), b(r.Shipping), class})
	}
	gooCSV(c, "tax_rates.csv", gooTaxCSVHeader, rows)
}

// gooTaxImport reads a WooCommerce tax rates CSV (the export's columns) into the class.
func (s *Server) gooTaxImport(c *gin.Context, class string) (int, error) {
	fh, err := c.FormFile("import_file")
	if err != nil {
		return 0, err
	}
	f, err := fh.Open()
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	var maxPos int
	_ = s.store.Pool.QueryRow(c, `SELECT coalesce(max(position),0) FROM goo_tax_rates WHERE class = $1`, class).Scan(&maxPos)
	n := 0
	for line := 0; ; line++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return n, err
		}
		if line == 0 && len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "Country code") {
			continue
		}
		for len(rec) < 10 {
			rec = append(rec, "")
		}
		star := func(v string) string {
			if v = gooCSVUnescape(strings.TrimSpace(v)); v == "*" {
				return ""
			}
			return v
		}
		rate := &gooTaxRate{Country: star(rec[0]), State: star(rec[1]), Postcodes: gooSplitList(star(rec[2])), Cities: gooSplitList(star(rec[3])),
			Name: gooCSVUnescape(strings.TrimSpace(rec[5])), Compound: rec[7] == "1", Shipping: rec[8] != "0", Class: class}
		rate.Rate, _ = strconv.ParseFloat(strings.TrimSpace(rec[4]), 64)
		rate.Priority, _ = strconv.Atoi(strings.TrimSpace(rec[6]))
		if cl := strings.TrimSpace(rec[9]); cl != "" && cl != class {
			if _, _, ok := s.gooTaxClassBySection(gooTaxSection(cl)); ok {
				rate.Class = cl
			}
		}
		maxPos++
		rate.Position = maxPos
		if err := s.gooSaveTaxRate(c, rate); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
