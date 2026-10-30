package server

import (
	"context"
	"html/template"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Settings › Shipping: the shipping zones list, a zone's editor (zone_id=new|ID) with its methods,
// a method's settings (instance_id=), and the shipping classes (section=classes).

type gooZoneRow struct {
	ID      int64
	Name    string
	Regions string
	Methods []gooMethodRow
	EditURL string
	Order   int
}

type gooMethodRow struct {
	InstanceID int64
	MethodID   string
	Title      string
	Desc       string
	Enabled    bool
	EditURL    string
}

type gooZoneEdit struct {
	ID        int64
	New       bool
	Rest      bool
	Name      string
	Regions   []gooRegionOpt
	Postcodes string
	Methods   []gooMethodRow
	Types     []gooOpt
}

type gooRegionOpt struct {
	Value, Label string
	Selected     bool
	Group        string
}

type gooClassRow struct {
	ID          int64
	Name, Slug  string
	Description string
	Count       int
}

// gooZoneRegions summarises a zone's locations ("United States (US), Europe and 2 other regions").
func gooZoneRegions(z *gooZone) string {
	var names, posts []string
	for _, l := range z.Locations {
		switch l.Type {
		case "continent":
			for _, ct := range gooContinents {
				if ct.Code == l.Code {
					names = append(names, ct.Name)
				}
			}
		case "country":
			names = append(names, gooCountryName(l.Code))
		case "state":
			cc, st, _ := strings.Cut(l.Code, ":")
			names = append(names, gooStateName(cc, st)+", "+gooCountryName(cc))
		case "postcode":
			posts = append(posts, l.Code)
		}
	}
	if len(names) == 0 {
		if z.ID == 0 {
			return "Shipping methods for customers in regions not covered by your other zones."
		}
		return "Everywhere"
	}
	out := names
	if len(names) > 10 {
		out = append(names[:10:10], strconv.Itoa(len(names)-10)+" other regions")
	}
	res := strings.Join(out, ", ")
	if len(posts) > 0 {
		res += " (" + strings.Join(posts, ", ") + ")"
	}
	return res
}

func gooMethodRowOf(m *gooMethodInstance, zoneID int64) gooMethodRow {
	r := gooMethodRow{InstanceID: m.InstanceID, MethodID: m.MethodID, Title: m.Title(), Enabled: m.Enabled,
		EditURL: gooAdminURL("settings", "tab", "shipping", "instance_id", itoa64(m.InstanceID))}
	if def := gooShippingMethods[m.MethodID]; def != nil {
		r.Desc = def.Description
	}
	return r
}

func (s *Server) gooZonesView(c *gin.Context, d *gooSettingsData) {
	d.Kind, d.Title = "zones", "Shipping zones"
	d.SaveText = "Save changes"
	zones, _ := s.gooZones(c.Request.Context(), true)
	for _, z := range zones {
		r := &gooZoneRow{ID: z.ID, Name: z.Name, Regions: gooZoneRegions(z), Order: z.Position,
			EditURL: gooAdminURL("settings", "tab", "shipping", "zone_id", itoa64(z.ID))}
		for _, m := range z.Methods {
			r.Methods = append(r.Methods, gooMethodRowOf(m, z.ID))
		}
		d.Zones = append(d.Zones, r)
	}
}

// gooZonesPost saves the zone order or deletes a zone.
func (s *Server) gooZonesPost(c *gin.Context) (string, string) {
	ctx := c.Request.Context()
	if id, ok := strings.CutPrefix(c.PostForm("goo_do"), "delete_zone:"); ok {
		n, _ := strconv.ParseInt(id, 10, 64)
		if n > 0 {
			_ = s.gooDeleteZone(ctx, n)
		}
		return "Zone deleted.", ""
	}
	zones, _ := s.gooZones(ctx, false)
	for _, z := range zones {
		if v := c.PostForm("zone_order[" + itoa64(z.ID) + "]"); v != "" {
			z.Position, _ = strconv.Atoi(v)
			_ = s.gooSaveZone(ctx, z)
		}
	}
	return "Your settings have been saved.", ""
}

func (s *Server) gooZoneView(c *gin.Context, d *gooSettingsData) {
	ctx := c.Request.Context()
	d.Kind = "zone"
	d.BackURL, d.BackText = gooAdminURL("settings", "tab", "shipping"), "Shipping zones"
	z := &gooZone{}
	ze := &gooZoneEdit{}
	if c.Query("zone_id") == "new" {
		ze.New = true
		d.Title = "Add shipping zone"
	} else {
		id, _ := strconv.ParseInt(c.Query("zone_id"), 10, 64)
		var err error
		if z, err = s.gooZone(ctx, id); err != nil {
			d.Kind, d.Body, d.HideSave = "fields", `<p>Invalid shipping zone.</p>`, true
			return
		}
		ze.ID, ze.Name, ze.Rest = z.ID, z.Name, z.ID == 0
		d.Title = z.Name
		for _, m := range z.Methods {
			ze.Methods = append(ze.Methods, gooMethodRowOf(m, z.ID))
		}
	}
	sel := map[string]bool{}
	var posts []string
	for _, l := range z.Locations {
		if l.Type == "postcode" {
			posts = append(posts, l.Code)
			continue
		}
		sel[l.Type+":"+l.Code] = true
	}
	ze.Postcodes = strings.Join(posts, "\n")
	for _, ct := range gooContinents {
		ze.Regions = append(ze.Regions, gooRegionOpt{Value: "continent:" + ct.Code, Label: ct.Name, Selected: sel["continent:"+ct.Code], Group: "Continents"})
	}
	for _, cn := range gooCountryOrder {
		ze.Regions = append(ze.Regions, gooRegionOpt{Value: "country:" + cn.Code, Label: cn.Name, Selected: sel["country:"+cn.Code], Group: "Countries"})
		for _, st := range gooStates[cn.Code] {
			v := "state:" + cn.Code + ":" + st.Code
			ze.Regions = append(ze.Regions, gooRegionOpt{Value: v, Label: st.Name + ", " + cn.Name, Selected: sel[v], Group: "States"})
		}
	}
	for _, m := range s.gooShippingMethodOrder() {
		ze.Types = append(ze.Types, gooOpt{m.ID, m.Title})
	}
	d.Zone = ze
}

// gooZonePost saves a zone (name, regions, postcodes, method toggles) and adds / deletes methods.
func (s *Server) gooZonePost(c *gin.Context) (string, string, string) {
	ctx := c.Request.Context()
	var z *gooZone
	if c.Query("zone_id") == "new" {
		z = &gooZone{}
		var pos int
		_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(max(position),0)+1 FROM goo_shipping_zones`).Scan(&pos)
		z.Position = pos
	} else {
		id, _ := strconv.ParseInt(c.Query("zone_id"), 10, 64)
		var err error
		if z, err = s.gooZone(ctx, id); err != nil {
			return gooAdminURL("settings", "tab", "shipping"), "", "Invalid shipping zone."
		}
	}
	if z.ID != 0 || c.Query("zone_id") == "new" {
		z.Name = strings.TrimSpace(c.PostForm("zone_name"))
		if z.Name == "" {
			if c.Query("zone_id") == "new" {
				return gooAdminURL("settings", "tab", "shipping", "zone_id", "new"), "", "Zone name is required."
			}
			z.Name = "Zone"
		}
		z.Locations = nil
		for _, v := range c.PostFormArray("zone_locations[]") {
			typ, code, ok := strings.Cut(v, ":")
			if ok && (typ == "continent" || typ == "country" || typ == "state") {
				z.Locations = append(z.Locations, gooZoneLocation{Code: code, Type: typ})
			}
		}
		if c.PostForm("zone_limit_postcodes") != "" || c.PostForm("zone_postcodes") != "" {
			for _, p := range strings.Split(c.PostForm("zone_postcodes"), "\n") {
				if p = strings.TrimSpace(p); p != "" {
					z.Locations = append(z.Locations, gooZoneLocation{Code: p, Type: "postcode"})
				}
			}
		}
		if err := s.gooSaveZone(ctx, z); err != nil {
			return gooAdminURL("settings", "tab", "shipping"), "", err.Error()
		}
	}
	back := gooAdminURL("settings", "tab", "shipping", "zone_id", itoa64(z.ID))
	methods, _ := s.gooMethodInstances(ctx, z.ID)
	if c.PostForm("methods_present") != "" {
		for _, m := range methods {
			en := c.PostForm("method_enabled["+itoa64(m.InstanceID)+"]") != ""
			pos, err := strconv.Atoi(c.PostForm("method_order[" + itoa64(m.InstanceID) + "]"))
			if err != nil {
				pos = m.Position
			}
			if en != m.Enabled || pos != m.Position {
				m.Enabled, m.Position = en, pos
				_ = s.gooSaveMethod(ctx, m)
			}
		}
	}
	do := c.PostForm("goo_do")
	switch {
	case do == "add_method":
		mid := c.PostForm("add_method_id")
		if _, err := s.gooAddMethod(ctx, z.ID, mid, nil); err != nil {
			return back, "", err.Error()
		}
		return back, "Shipping method added.", ""
	case strings.HasPrefix(do, "delete_method:"):
		n, _ := strconv.ParseInt(strings.TrimPrefix(do, "delete_method:"), 10, 64)
		for _, m := range methods {
			if m.InstanceID == n {
				_ = s.gooDeleteMethod(ctx, n)
			}
		}
		return back, "Shipping method deleted.", ""
	}
	s.purgeAll(ctx)
	return back, "Your settings have been saved.", ""
}

// gooShippingClassTerms are the product_shipping_class terms.
func (s *Server) gooShippingClassTerms(ctx context.Context) []*store.Term {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, name, slug, description FROM terms WHERE taxonomy = 'product_shipping_class' ORDER BY name, id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*store.Term
	for rows.Next() {
		t := &store.Term{Taxonomy: "product_shipping_class"}
		if rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Description) == nil {
			out = append(out, t)
		}
	}
	return out
}

// gooMethodFields are a method's settings fields; flat rate gets a cost field per shipping class.
func (s *Server) gooMethodFields(ctx context.Context, def *gooShippingMethodDef) []gooField {
	var out []gooField
	for _, f := range def.Fields {
		out = append(out, f)
		if def.ID == "flat_rate" && f.ID == "class_costs" {
			for _, t := range s.gooShippingClassTerms(ctx) {
				out = append(out, gooField{ID: "class_cost_" + itoa64(t.ID), Title: `"` + gooEsc(t.Name) + `" shipping class cost`, Type: "text", Holder: "N/A",
					Desc: "See \"Cost\" option for more information on how to use this field."})
			}
		}
	}
	return out
}

func (s *Server) gooShippingMethodView(c *gin.Context, d *gooSettingsData) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(c.Query("instance_id"), 10, 64)
	m := s.gooMethodInstance(ctx, id)
	d.Kind = "fields"
	if m == nil || gooShippingMethods[m.MethodID] == nil {
		d.Body, d.HideSave = `<p>Invalid shipping method.</p>`, true
		return
	}
	def := gooShippingMethods[m.MethodID]
	zoneName := "Locations not covered by your other zones"
	if z, err := s.gooZone(ctx, m.ZoneID); err == nil {
		zoneName = z.Name
	}
	d.Title = def.Title
	d.BackURL, d.BackText = gooAdminURL("settings", "tab", "shipping", "zone_id", itoa64(m.ZoneID)), zoneName
	d.Intro = template.HTML(`<p>` + gooEsc(def.Description) + `</p>`)
	prefix := "woocommerce_" + m.MethodID + "_"
	d.Body = s.gooFieldsHTML(ctx, s.gooMethodFields(ctx, def), func(id string) string { return prefix + id }, gooMapValue(m.Settings), nil)
}

func (s *Server) gooMethodInstance(ctx context.Context, id int64) *gooMethodInstance {
	all, _ := s.gooMethodInstances(ctx, -1)
	for _, m := range all {
		if m.InstanceID == id {
			return m
		}
	}
	return nil
}

func (s *Server) gooShippingMethodPost(c *gin.Context) (string, string, string) {
	ctx := c.Request.Context()
	id, _ := strconv.ParseInt(c.Query("instance_id"), 10, 64)
	m := s.gooMethodInstance(ctx, id)
	if m == nil || gooShippingMethods[m.MethodID] == nil {
		return gooAdminURL("settings", "tab", "shipping"), "", "Invalid shipping method."
	}
	prefix := "woocommerce_" + m.MethodID + "_"
	s.gooReadMapFields(c, s.gooMethodFields(ctx, gooShippingMethods[m.MethodID]), func(id string) string { return prefix + id }, m.Settings)
	if err := s.gooSaveMethod(ctx, m); err != nil {
		return gooAdminURL("settings", "tab", "shipping", "instance_id", itoa64(id)), "", err.Error()
	}
	s.purgeAll(ctx)
	return gooAdminURL("settings", "tab", "shipping", "instance_id", itoa64(id)), "Your settings have been saved.", ""
}

// Classes ---------------------------------------------------------------------------------------------------

func (s *Server) gooClassesView(c *gin.Context, d *gooSettingsData) {
	ctx := c.Request.Context()
	d.Kind, d.Title = "classes", "Shipping classes"
	counts := map[int64]int{}
	rows, err := s.store.Pool.Query(ctx, `SELECT shipping_class_id, count(*) FROM goo_products WHERE shipping_class_id IS NOT NULL GROUP BY shipping_class_id`)
	if err == nil {
		for rows.Next() {
			var id int64
			var n int
			if rows.Scan(&id, &n) == nil {
				counts[id] = n
			}
		}
		rows.Close()
	}
	for _, t := range s.gooShippingClassTerms(ctx) {
		d.Classes = append(d.Classes, gooClassRow{ID: t.ID, Name: t.Name, Slug: t.Slug, Description: t.Description, Count: counts[t.ID]})
	}
	sort.SliceStable(d.Classes, func(i, j int) bool { return strings.ToLower(d.Classes[i].Name) < strings.ToLower(d.Classes[j].Name) })
}

// gooClassesPost updates, adds and deletes shipping classes.
func (s *Server) gooClassesPost(c *gin.Context) (string, string) {
	ctx := c.Request.Context()
	if id, ok := strings.CutPrefix(c.PostForm("goo_do"), "delete_class:"); ok {
		n, _ := strconv.ParseInt(id, 10, 64)
		if t, err := s.store.GetTerm(ctx, n); err == nil && t.Taxonomy == "product_shipping_class" {
			_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_products SET shipping_class_id = NULL WHERE shipping_class_id = $1`, n)
			if err := s.store.DeleteTerm(ctx, t, 0); err != nil {
				return "", err.Error()
			}
		}
		return "Shipping class deleted.", ""
	}
	for _, t := range s.gooShippingClassTerms(ctx) {
		id := itoa64(t.ID)
		name, ok := c.GetPostForm("shipping_class_name[" + id + "]")
		if !ok {
			continue
		}
		full, err := s.store.GetTerm(ctx, t.ID)
		if err != nil {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			full.Name = name
		}
		full.Slug = strings.TrimSpace(c.PostForm("shipping_class_slug[" + id + "]"))
		full.Description = strings.TrimSpace(c.PostForm("shipping_class_description[" + id + "]"))
		if err := s.store.UpdateTerm(ctx, full); err != nil {
			return "", err.Error()
		}
	}
	if name := strings.TrimSpace(c.PostForm("new_shipping_class_name")); name != "" {
		t := &store.Term{Taxonomy: "product_shipping_class", Name: name, Slug: strings.TrimSpace(c.PostForm("new_shipping_class_slug")),
			Description: strings.TrimSpace(c.PostForm("new_shipping_class_description"))}
		if err := s.store.InsertTerm(ctx, t); err != nil {
			return "", err.Error()
		}
	}
	return "Your settings have been saved.", ""
}
