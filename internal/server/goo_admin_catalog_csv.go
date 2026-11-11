package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Products › Export / Import: WooCommerce's product CSV (WC_Product_CSV_Exporter and
// WC_Product_CSV_Importer) with the same column names, so files move between the two.
//
// Export: /wp-admin/edit?post_type=product&page=product_exporter (the form) and
// &action=download_product_csv (the file). Import: /wp-admin/edit?post_type=product&page=product_importer
// shows the upload form, which posts to /wp-admin/goocommerce?page=product_importer (upload → column
// mapping → import → done).

func init() {
	gooExtAdminPages["product_importer"] = func(s *Server, c *gin.Context) { s.gooProductImporter(c) }
}

// gooCSVColumn is an export column: its key (the importer's field) and header.
type gooCSVColumn struct{ Key, Header string }

func (s *Server) gooCSVColumns() []gooCSVColumn {
	wu := defaultStr(s.gooOptString("weight_unit"), "kg")
	du := defaultStr(s.gooOptString("dimension_unit"), "cm")
	return []gooCSVColumn{{"id", "ID"}, {"type", "Type"}, {"sku", "SKU"}, {"name", "Name"}, {"published", "Published"}, {"featured", "Is featured?"},
		{"catalog_visibility", "Visibility in catalog"}, {"short_description", "Short description"}, {"description", "Description"},
		{"date_on_sale_from", "Date sale price starts"}, {"date_on_sale_to", "Date sale price ends"}, {"tax_status", "Tax status"}, {"tax_class", "Tax class"},
		{"stock_status", "In stock?"}, {"stock_quantity", "Stock"}, {"low_stock_amount", "Low stock amount"}, {"backorders", "Backorders allowed?"},
		{"sold_individually", "Sold individually?"}, {"weight", "Weight (" + wu + ")"}, {"length", "Length (" + du + ")"}, {"width", "Width (" + du + ")"},
		{"height", "Height (" + du + ")"}, {"reviews_allowed", "Allow customer reviews?"}, {"purchase_note", "Purchase note"}, {"sale_price", "Sale price"},
		{"regular_price", "Regular price"}, {"category_ids", "Categories"}, {"tag_ids", "Tags"}, {"shipping_class_id", "Shipping class"}, {"images", "Images"},
		{"download_limit", "Download limit"}, {"download_expiry", "Download expiry days"}, {"parent_id", "Parent"}, {"grouped_products", "Grouped products"},
		{"upsell_ids", "Upsells"}, {"cross_sell_ids", "Cross-sells"}, {"product_url", "External URL"}, {"button_text", "Button text"}, {"menu_order", "Position"}}
}

// gooCSVHeaderKeys maps a header (units stripped, lower case) to its field key.
func (s *Server) gooCSVHeaderKey(header string) string {
	h := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")))
	if i := strings.Index(h, " ("); i > 0 && strings.HasSuffix(h, ")") {
		h = h[:i]
	}
	for _, col := range s.gooCSVColumns() {
		ch := strings.ToLower(col.Header)
		if i := strings.Index(ch, " ("); i > 0 {
			ch = ch[:i]
		}
		if ch == h || col.Key == h {
			return col.Key
		}
	}
	if m := regexp.MustCompile(`^attribute (\d+) (name|value\(s\)|visible|global|default)$`).FindStringSubmatch(h); m != nil {
		field := map[string]string{"name": "name", "value(s)": "value", "visible": "visible", "global": "taxonomy", "default": "default"}[m[2]]
		return "attributes:" + field + m[1]
	}
	if m := regexp.MustCompile(`^download (\d+) (id|name|url)$`).FindStringSubmatch(h); m != nil {
		return "downloads:" + m[2] + m[1]
	}
	if strings.HasPrefix(h, "meta: ") {
		return "meta:" + strings.TrimSpace(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff"))[len("meta: "):])
	}
	return ""
}

// gooCSVEscape is WC_CSV_Exporter::escape_data(): cells starting with = + - @ get a leading quote.
// gooCSVEscape prefixes cells that a spreadsheet would run as a formula with a quote, like
// WC_CSV_Exporter::escape_data (triggers = + - @ tab CR).
func gooCSVEscape(v string) string {
	if v != "" && strings.ContainsAny(v[:1], gooCSVTriggers) {
		return "'" + v
	}
	return v
}

const gooCSVTriggers = "=+-@\t\r"

func gooCSVUnescape(v string) string {
	if len(v) > 1 && v[0] == '\'' && strings.ContainsAny(v[1:2], gooCSVTriggers) {
		return v[1:]
	}
	return v
}

// gooCSVList joins list items, escaping commas inside them ("\,") like WooCommerce.
func gooCSVList(items []string) string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = strings.ReplaceAll(it, ",", `\,`)
	}
	return strings.Join(out, ", ")
}

func gooCSVSplit(v string) []string {
	v = strings.ReplaceAll(v, `\,`, "\x00")
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(strings.ReplaceAll(part, "\x00", ","))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func gooBool01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Export --------------------------------------------------------------------------------------------------

type gooExportData struct {
	Columns    []gooCSVColumn
	Types      []gooOpt
	Categories []*store.Term
	Nonce      string
}

func (s *Server) gooProductExporter(c *gin.Context) {
	ctx := c.Request.Context()
	if !currentUser(c).Can("edit_products", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to export products.")
		return
	}
	if c.Query("action") == "download_product_csv" {
		if !s.checkNonce(c, "product-csv-exporter") {
			s.denyNonce(c)
			return
		}
		data, err := s.gooExportCSVMeta(ctx, c.QueryArray("export_types[]"), c.QueryArray("export_category[]"), c.QueryArray("export_columns[]"), c.Query("export_meta") == "1")
		if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		name := "wc-product-export-" + time.Now().In(s.loc()).Format("2-1-2006") + "-" + strconv.FormatInt(time.Now().UnixMilli(), 10) + ".csv"
		c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
		c.Data(http.StatusOK, "text/csv; charset=utf-8", data)
		return
	}
	d := gooExportData{Columns: s.gooCSVColumns(), Nonce: s.nonce(c, "product-csv-exporter"),
		Types: append([]gooOpt{{gooTypeVariation, "Product variations"}}, s.gooProductTypeOptions()...)}
	d.Categories, _ = s.store.HierarchicalTerms(ctx, "product_cat", false)
	s.gooCatalogAssets(c)
	s.adminPage(c, "edit?post_type=product", "Export Products", "product_page_product_exporter woocommerce", "admin/goo-product-csv", map[string]any{"Export": d})
}

// gooExportCSV builds the product CSV: products in id order, each variable product followed by its variations.
func (s *Server) gooExportCSV(ctx context.Context, types, cats, columns []string) ([]byte, error) {
	return s.gooExportCSVMeta(ctx, types, cats, columns, false)
}

// gooMetaCell is how a product meta value is written to a "Meta: key" cell: strings as they are,
// numbers, booleans, lists and objects as JSON. A string that would read back as JSON (a numeric
// SKU-like "007", "true", "[1]" or a quoted text) is written as a JSON string so the round trip
// keeps its type.
func gooMetaCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		if x == "" {
			return ""
		}
		if gooMetaLooksJSON(x) {
			b, _ := json.Marshal(x)
			return string(b)
		}
		return x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func gooMetaLooksJSON(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && (t[0] == '"' || json.Valid([]byte(t)))
}

// gooMetaFromCell is the inverse of gooMetaCell.
func gooMetaFromCell(cell string) any {
	t := strings.TrimSpace(cell)
	if t != "" && json.Valid([]byte(t)) {
		var v any
		if json.Unmarshal([]byte(t), &v) == nil {
			return v
		}
	}
	return cell
}

// gooExportCSVMeta is gooExportCSV with WooCommerce's "Export custom meta" option: every product meta
// key (subscription, booking, gift card, add-on settings, …) becomes a "Meta: key" column.
func (s *Server) gooExportCSVMeta(ctx context.Context, types, cats, columns []string, withMeta bool) ([]byte, error) {
	q := gooProductQuery{Status: []string{"publish", "private", "draft", "pending", "future"}, OrderBy: "id", Order: "asc", PerPage: -1}
	wantVariations := len(types) == 0 || containsStr(types, gooTypeVariation)
	for _, t := range types {
		if t != gooTypeVariation {
			q.Types = append(q.Types, t)
		}
	}
	if len(types) > 0 && len(q.Types) == 0 {
		q.Types = []string{"__none__"}
	}
	for _, slug := range cats {
		if t, err := s.store.TermBySlug(ctx, "product_cat", slug); err == nil {
			q.Category = append(q.Category, s.gooTermAndChildren(ctx, t.ID)...)
		}
	}
	products, _, err := s.gooQueryProducts(ctx, q)
	if err != nil {
		return nil, err
	}
	var all []*gooProduct
	for _, p := range products {
		all = append(all, p)
		if gooSubsVariableLike(p.Type) && wantVariations {
			vars, _ := s.gooVariations(ctx, p.ID, true)
			all = append(all, vars...)
		}
	}
	maxAttrs, maxDownloads := 0, 0
	for _, p := range all {
		n := len(p.Attributes)
		if p.Type == gooTypeVariation {
			n = len(p.VariationAttrs)
		}
		maxAttrs = max(maxAttrs, n)
		maxDownloads = max(maxDownloads, len(p.Downloads))
	}
	cols := s.gooCSVColumns()
	if len(columns) > 0 {
		var keep []gooCSVColumn
		for _, col := range cols {
			if containsStr(columns, col.Key) {
				keep = append(keep, col)
			}
		}
		cols = keep
	}
	withAttrs := len(columns) == 0 || containsStr(columns, "attributes")
	withDownloads := len(columns) == 0 || containsStr(columns, "downloads")
	header := []string{}
	for _, col := range cols {
		header = append(header, col.Header)
	}
	if withDownloads {
		for i := 1; i <= maxDownloads; i++ {
			n := strconv.Itoa(i)
			header = append(header, "Download "+n+" ID", "Download "+n+" name", "Download "+n+" URL")
		}
	}
	if withAttrs {
		for i := 1; i <= maxAttrs; i++ {
			n := strconv.Itoa(i)
			header = append(header, "Attribute "+n+" name", "Attribute "+n+" value(s)", "Attribute "+n+" visible", "Attribute "+n+" global", "Attribute "+n+" default")
		}
	}
	var metaKeys []string
	if withMeta {
		seen := map[string]bool{}
		for _, p := range all {
			for k := range p.Meta {
				if k != "" && !seen[k] {
					seen[k] = true
					metaKeys = append(metaKeys, k)
				}
			}
		}
		sort.Strings(metaKeys)
		for _, k := range metaKeys {
			header = append(header, "Meta: "+k)
		}
	}
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	w := csv.NewWriter(&buf)
	_ = w.Write(header)
	for _, p := range all {
		vals := s.gooExportValues(ctx, p)
		row := make([]string, 0, len(header))
		for _, col := range cols {
			row = append(row, gooCSVEscape(vals[col.Key]))
		}
		if withDownloads {
			for i := 0; i < maxDownloads; i++ {
				if i < len(p.Downloads) {
					dl := p.Downloads[i]
					row = append(row, dl.ID, gooCSVEscape(dl.Name), gooCSVEscape(dl.File))
				} else {
					row = append(row, "", "", "")
				}
			}
		}
		if withAttrs {
			row = append(row, s.gooExportAttributes(ctx, p, maxAttrs)...)
		}
		for _, k := range metaKeys {
			row = append(row, gooCSVEscape(gooMetaCell(p.Meta[k])))
		}
		_ = w.Write(row)
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// gooLinkedRef is how the exporter names a linked product: its SKU, else "id:<id>".
func (s *Server) gooLinkedRefs(ctx context.Context, ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	m, _ := s.gooGetProducts(ctx, ids)
	var out []string
	for _, id := range ids {
		if p := m[id]; p != nil && p.SKU != "" {
			out = append(out, p.SKU)
		} else {
			out = append(out, "id:"+itoa64(id))
		}
	}
	return gooCSVList(out)
}

func (s *Server) gooExportValues(ctx context.Context, p *gooProduct) map[string]string {
	v := map[string]string{}
	typ := []string{p.Type}
	if p.Downloadable {
		typ = append(typ, "downloadable")
	}
	if p.Virtual {
		typ = append(typ, "virtual")
	}
	v["id"] = itoa64(p.ID)
	v["type"] = strings.Join(typ, ", ")
	v["sku"] = p.SKU
	v["name"] = p.Name
	switch p.Status {
	case "publish":
		v["published"] = "1"
	case "private":
		v["published"] = "-1"
		if p.Type == gooTypeVariation {
			v["published"] = "0"
		}
	default:
		v["published"] = "0"
	}
	v["featured"] = gooBool01(p.Featured)
	v["catalog_visibility"] = defaultStr(p.CatalogVisibility, "visible")
	if p.Type == gooTypeVariation {
		v["description"] = p.Description
	} else {
		v["short_description"] = p.Post.Excerpt
		v["description"] = p.Post.Content
	}
	if p.SaleFrom != nil {
		v["date_on_sale_from"] = p.SaleFrom.In(s.loc()).Format("2006-01-02 15:04:05")
	}
	if p.SaleTo != nil {
		v["date_on_sale_to"] = p.SaleTo.In(s.loc()).Format("2006-01-02 15:04:05")
	}
	v["tax_status"] = p.TaxStatus
	v["tax_class"] = p.TaxClass
	v["stock_status"] = map[string]string{"instock": "1", "outofstock": "0", "onbackorder": "backorder"}[defaultStr(p.StockStatus, "instock")]
	if p.ManageStock && p.StockQuantity != nil {
		v["stock_quantity"] = strconv.Itoa(*p.StockQuantity)
	}
	v["low_stock_amount"] = gooIntStr(p.LowStockAmount)
	v["backorders"] = map[string]string{"no": "0", "yes": "1", "notify": "notify"}[defaultStr(p.Backorders, "no")]
	v["sold_individually"] = gooBool01(p.SoldIndividually)
	v["weight"], v["length"], v["width"], v["height"] = p.Weight, p.Length, p.Width, p.Height
	v["reviews_allowed"] = gooBool01(p.Post.CommentStatus == "open")
	v["purchase_note"] = p.PurchaseNote
	v["sale_price"] = gooAmount(p.SalePrice)
	v["regular_price"] = gooAmount(p.RegularPrice)
	if p.Type != gooTypeVariation {
		v["category_ids"] = s.gooCategoryPaths(ctx, p.ID)
		if tags, err := s.store.TermsForPosts(ctx, []int64{p.ID}, "product_tag"); err == nil {
			var names []string
			for _, t := range tags[p.ID] {
				names = append(names, t.Name)
			}
			v["tag_ids"] = gooCSVList(names)
		}
	}
	if p.ShippingClassID > 0 {
		if t, err := s.store.GetTerm(ctx, p.ShippingClassID); err == nil {
			v["shipping_class_id"] = t.Name
		}
	}
	var imgs []string
	for _, id := range append([]int64{p.ImageID}, p.GalleryIDs...) {
		if id > 0 {
			if u := s.gooAttachmentURL(ctx, id, "full"); u != "" {
				imgs = append(imgs, u)
			}
		}
	}
	v["images"] = strings.Join(imgs, ", ")
	if p.DownloadLimit >= 0 {
		v["download_limit"] = strconv.Itoa(p.DownloadLimit)
	}
	if p.DownloadExpiry >= 0 {
		v["download_expiry"] = strconv.Itoa(p.DownloadExpiry)
	}
	if p.Type == gooTypeVariation && p.ParentID > 0 {
		v["parent_id"] = s.gooLinkedRefs(ctx, []int64{p.ParentID})
	}
	if p.Type == gooTypeGrouped {
		v["grouped_products"] = s.gooLinkedRefs(ctx, p.Children)
	}
	v["upsell_ids"] = s.gooLinkedRefs(ctx, p.UpsellIDs)
	v["cross_sell_ids"] = s.gooLinkedRefs(ctx, p.CrossSellIDs)
	v["product_url"] = p.ProductURL
	v["button_text"] = p.ButtonText
	v["menu_order"] = strconv.Itoa(p.Post.MenuOrder)
	return v
}

// gooCategoryPaths is "Parent > Child, Other" (WC_Product_CSV_Exporter::format_term_ids()).
func (s *Server) gooCategoryPaths(ctx context.Context, id int64) string {
	terms, err := s.store.TermsForPosts(ctx, []int64{id}, "product_cat")
	if err != nil {
		return ""
	}
	var out []string
	for _, t := range terms[id] {
		path := []string{t.Name}
		parent := t.ParentID
		for depth := 0; parent != 0 && depth < 20; depth++ {
			pt, err := s.store.GetTerm(ctx, parent)
			if err != nil {
				break
			}
			path = append([]string{pt.Name}, path...)
			parent = pt.ParentID
		}
		out = append(out, strings.Join(path, " > "))
	}
	return gooCSVList(out)
}

func (s *Server) gooExportAttributes(ctx context.Context, p *gooProduct, n int) []string {
	var out []string
	if p.Type == gooTypeVariation {
		parent := s.gooParent(ctx, p)
		var attrs []gooProductAttr
		if parent != nil {
			for _, a := range parent.Attributes {
				if _, ok := p.VariationAttrs[a.Key()]; ok {
					attrs = append(attrs, a)
				}
			}
		}
		for _, a := range attrs {
			val := p.VariationAttrs[a.Key()]
			if val != "" {
				val = s.gooAttrValueLabel(ctx, a, val)
			}
			out = append(out, s.gooAttrLabel(ctx, a), gooCSVEscape(val), "", gooBool01(a.Taxonomy != ""), "")
		}
	} else {
		for _, a := range p.Attributes {
			var vals []string
			for _, o := range a.Options {
				vals = append(vals, s.gooAttrValueLabel(ctx, a, o))
			}
			def := ""
			if dv := p.DefaultAttributes[a.Key()]; dv != "" {
				def = s.gooAttrValueLabel(ctx, a, dv)
			}
			out = append(out, s.gooAttrLabel(ctx, a), gooCSVEscape(gooCSVList(vals)), gooBool01(a.Visible), gooBool01(a.Taxonomy != ""), def)
		}
	}
	for len(out) < n*5 {
		out = append(out, "")
	}
	return out
}

// Import --------------------------------------------------------------------------------------------------

type gooImportMapRow struct {
	Index    int
	Header   string
	Sample   string
	Selected string
}

type gooImportResult struct {
	Imported, Updated, Skipped, Failed int
	Log                                []string
}

type gooImportData struct {
	Step      string
	Nonce     string
	File      string
	Update    bool
	Delimiter string
	Map       []gooImportMapRow
	Fields    []gooOpt
	Result    *gooImportResult
	Error     string
}

var gooImportTokenRe = regexp.MustCompile(`^goo-import-[0-9]+\.csv$`)

func (s *Server) gooImportFields() []gooOpt {
	out := []gooOpt{{"", "Do not import"}}
	for _, col := range s.gooCSVColumns() {
		out = append(out, gooOpt{col.Key, col.Header})
	}
	for i := 1; i <= 6; i++ {
		n := strconv.Itoa(i)
		out = append(out, gooOpt{"attributes:name" + n, "Attribute " + n + " name"}, gooOpt{"attributes:value" + n, "Attribute " + n + " value(s)"},
			gooOpt{"attributes:visible" + n, "Attribute " + n + " visible"}, gooOpt{"attributes:taxonomy" + n, "Attribute " + n + " global"},
			gooOpt{"attributes:default" + n, "Attribute " + n + " default"})
	}
	for i := 1; i <= 6; i++ {
		n := strconv.Itoa(i)
		out = append(out, gooOpt{"downloads:id" + n, "Download " + n + " ID"}, gooOpt{"downloads:name" + n, "Download " + n + " name"}, gooOpt{"downloads:url" + n, "Download " + n + " URL"})
	}
	return out
}

// gooProductImporter is the importer: GET shows the upload form, POST runs a step.
func (s *Server) gooProductImporter(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("edit_products", nil) || !u.Can("import", nil) && !u.Can("manage_woocommerce", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to import products.")
		return
	}
	d := gooImportData{Step: "upload", Nonce: s.nonce(c, "woocommerce-csv-importer"), Delimiter: ","}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "woocommerce-csv-importer") {
			s.denyNonce(c)
			return
		}
		d.Update = c.PostForm("update_existing") == "1"
		d.Delimiter = defaultStr(c.PostForm("delimiter"), ",")
		switch c.PostForm("step") {
		case "mapping":
			s.gooImportMapping(c, &d)
		case "import":
			s.gooImportRun(c, &d)
		case "batch":
			s.gooImportBatch(c, &d)
			return
		default:
			d.Step = "upload"
		}
	}
	s.gooCatalogAssets(c)
	s.adminPage(c, "edit?post_type=product", "Import Products", "product_page_product_importer woocommerce", "admin/goo-product-csv", map[string]any{"Import": d})
}

func gooReadCSV(data []byte, delim string) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if delim != "" {
		r.Comma = []rune(delim)[0]
	}
	return r.ReadAll()
}

func (s *Server) gooImportMapping(c *gin.Context, d *gooImportData) {
	fh, err := c.FormFile("import")
	if err != nil {
		d.Error = "Please select a file to import."
		return
	}
	if ext := strings.ToLower(filepath.Ext(fh.Filename)); ext != ".csv" && ext != ".txt" {
		d.Error = "Invalid file type. The importer supports CSV and TXT file formats."
		return
	}
	f, err := fh.Open()
	if err != nil {
		d.Error = err.Error()
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<20))
	f.Close()
	if err != nil {
		d.Error = err.Error()
		return
	}
	rows, err := gooReadCSV(data, d.Delimiter)
	if err != nil || len(rows) == 0 {
		d.Error = "The file is empty or using a different encoding than UTF-8, please try again with a new file."
		return
	}
	tmp, err := os.CreateTemp(os.TempDir(), "goo-import-*.csv")
	if err != nil {
		d.Error = err.Error()
		return
	}
	// A short write (full disk) would import only the rows that made it to the file.
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		d.Error = "The uploaded file could not be saved: " + err.Error()
		return
	}
	// os.CreateTemp's random part is digits: rename to keep the token pattern strict.
	d.File = filepath.Base(tmp.Name())
	if !gooImportTokenRe.MatchString(d.File) {
		name := "goo-import-" + strconv.FormatInt(time.Now().UnixNano(), 10) + ".csv"
		if err := os.Rename(tmp.Name(), filepath.Join(os.TempDir(), name)); err != nil {
			os.Remove(tmp.Name())
			d.Error = "The uploaded file could not be saved: " + err.Error()
			return
		}
		d.File = name
	}
	d.Step = "mapping"
	d.Fields = s.gooImportFields()
	for _, h := range rows[0] {
		if k := s.gooCSVHeaderKey(h); strings.HasPrefix(k, "meta:") {
			d.Fields = append(d.Fields, gooOpt{k, "Meta: " + strings.TrimPrefix(k, "meta:")})
		}
	}
	for i, h := range rows[0] {
		row := gooImportMapRow{Index: i, Header: strings.TrimPrefix(h, "\ufeff"), Selected: s.gooCSVHeaderKey(h)}
		if len(rows) > 1 && i < len(rows[1]) {
			row.Sample = rows[1][i]
		}
		d.Map = append(d.Map, row)
	}
}

func (s *Server) gooImportRun(c *gin.Context, d *gooImportData) {
	token := c.PostForm("file")
	if !gooImportTokenRe.MatchString(token) {
		d.Error = "Invalid file."
		return
	}
	path := filepath.Join(os.TempDir(), token)
	data, err := os.ReadFile(path)
	if err != nil {
		d.Error = "The file does not exist, please try again."
		return
	}
	rows, err := gooReadCSV(data, d.Delimiter)
	if err != nil || len(rows) == 0 {
		d.Error = "The file is empty or using a different encoding than UTF-8, please try again with a new file."
		return
	}
	mapping := make([]string, len(rows[0]))
	for i, h := range rows[0] {
		key := "map_to[" + strconv.Itoa(i) + "]"
		if v, ok := c.GetPostForm(key); ok {
			mapping[i] = v
		} else {
			mapping[i] = s.gooCSVHeaderKey(h)
		}
	}
	res := s.gooImportRows(c.Request.Context(), rows[1:], mapping, d.Update, currentUser(c).ID)
	_ = os.Remove(path)
	d.Step = "done"
	d.Result = res
}

// gooImportRows imports CSV rows (WC_Product_CSV_Importer::import()).
func (s *Server) gooImportRows(ctx context.Context, rows [][]string, mapping []string, update bool, editorID int64) *gooImportResult {
	res := &gooImportResult{}
	s.gooImportRowsRange(ctx, rows, 0, mapping, update, editorID, map[int64]int64{}, res)
	return res
}

// gooImportRowsRange imports rows[from:to] into res; idMap (ids of the file → ids created by this
// import) and res carry over between the batches of one import. Row numbers in the log are the CSV's.
func (s *Server) gooImportRowsRange(ctx context.Context, all [][]string, offset int, mapping []string, update bool, editorID int64, idMap map[int64]int64, res *gooImportResult) {
	rows := all
	for n, row := range rows {
		n += offset
		fields := map[string]string{}
		empty := true
		for i, key := range mapping {
			if key == "" || i >= len(row) {
				continue
			}
			fields[key] = gooCSVUnescape(row[i])
			if strings.TrimSpace(row[i]) != "" {
				empty = false
			}
		}
		if empty {
			continue
		}
		created, err := s.gooImportRow(ctx, fields, update, idMap, editorID)
		label := defaultStr(fields["name"], defaultStr(fields["sku"], "#"+fields["id"]))
		switch {
		case err == errGooImportSkip || (err != nil && strings.HasPrefix(err.Error(), "skip:")):
			res.Skipped++
			res.Log = append(res.Log, fmt.Sprintf("Row %d (%s): %s", n+2, label, strings.TrimPrefix(err.Error(), "skip: ")))
		case err != nil:
			res.Failed++
			res.Log = append(res.Log, fmt.Sprintf("Row %d (%s): %s", n+2, label, err.Error()))
		case created:
			res.Imported++
		default:
			res.Updated++
		}
	}
}

var errGooImportSkip = fmt.Errorf("skip: skipped")

// gooRefID resolves "id:12" (through the ids created by this import) or a SKU.
func (s *Server) gooRefID(ctx context.Context, ref string, idMap map[int64]int64) int64 {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0
	}
	if raw, ok := strings.CutPrefix(ref, "id:"); ok {
		id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if nid, ok := idMap[id]; ok {
			return nid
		}
		var typ string
		if s.store.Pool.QueryRow(ctx, `SELECT type FROM posts WHERE id = $1 AND status <> 'trash'`, id).Scan(&typ) == nil && (typ == "product" || typ == "product_variation") {
			return id
		}
		return 0
	}
	var id int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT p.post_id FROM goo_products p JOIN posts po ON po.id = p.post_id WHERE p.sku = $1 AND po.status NOT IN ('trash','auto-draft') ORDER BY p.post_id LIMIT 1`, ref).Scan(&id)
	return id
}

func (s *Server) gooRefIDs(ctx context.Context, v string, idMap map[int64]int64) []int64 {
	var out []int64
	for _, ref := range gooCSVSplit(v) {
		if id := s.gooRefID(ctx, ref, idMap); id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func gooParseCSVDate(v string, loc *time.Location, end bool) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			if layout == "2006-01-02" && end {
				t = t.Add(24*time.Hour - time.Second)
			}
			return &t
		}
	}
	return nil
}

func (s *Server) gooCSVAmount(v string) *float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return &f
	}
	return s.gooParseAmount(v)
}

func (s *Server) gooImportRow(ctx context.Context, f map[string]string, update bool, idMap map[int64]int64, editorID int64) (bool, error) {
	has := func(k string) bool { _, ok := f[k]; return ok }
	typeParts := gooCSVSplit(strings.ToLower(f["type"]))
	typ := ""
	flags := map[string]bool{}
	for _, t := range typeParts {
		switch t {
		case "downloadable", "virtual":
			flags[t] = true
		default:
			typ = t
		}
	}
	fileID, _ := strconv.ParseInt(strings.TrimSpace(f["id"]), 10, 64)

	// Find the product to update.
	var p *gooProduct
	if fileID > 0 {
		if gp, err := s.gooGetProduct(ctx, fileID); err == nil && gp.Status != "trash" {
			p = gp
		}
	}
	if p == nil && strings.TrimSpace(f["sku"]) != "" {
		if id := s.gooRefID(ctx, f["sku"], nil); id > 0 {
			p, _ = s.gooGetProduct(ctx, id)
		}
	}
	created := false
	switch {
	case update && p == nil:
		return false, fmt.Errorf("skip: No matching product exists to update.")
	case !update && p != nil:
		if fileID > 0 && p.ID == fileID {
			return false, fmt.Errorf("skip: A product with this ID already exists.")
		}
		return false, fmt.Errorf("skip: A product with this SKU already exists.")
	case p == nil:
		created = true
		if typ == "" {
			typ = gooTypeSimple
		}
		if typ != gooTypeSimple && typ != gooTypeVariable && typ != gooTypeGrouped && typ != gooTypeExternal && typ != gooTypeVariation && gooProductTypes[typ] == nil {
			return false, fmt.Errorf("Invalid product type.")
		}
		p = gooNewProduct(typ)
		p.Post = &store.Post{Type: "product", CommentStatus: "open", PingStatus: "closed", Meta: map[string]any{}}
		if typ == gooTypeVariation {
			p.Post.Type, p.Post.CommentStatus = "product_variation", "closed"
			p.TaxClass = "parent"
		}
	default:
		if typ != "" && typ != p.Type && (typ == gooTypeVariation) != (p.Type == gooTypeVariation) {
			return false, fmt.Errorf("Updating product of type %s to %s is not allowed.", p.Type, typ)
		}
		if typ != "" {
			p.Type = typ
		}
	}
	var parent *gooProduct
	if p.Type == gooTypeVariation {
		pid := p.ParentID
		if has("parent_id") && strings.TrimSpace(f["parent_id"]) != "" {
			pid = s.gooRefID(ctx, f["parent_id"], idMap)
		}
		if pid == 0 {
			return false, fmt.Errorf("Variation cannot be imported: Missing parent ID or parent does not exist yet.")
		}
		var err error
		if parent, err = s.gooGetProduct(ctx, pid); err != nil || !gooSubsVariableLike(parent.Type) {
			return false, fmt.Errorf("Variation cannot be imported: Parent product cannot be a product variation")
		}
		p.ParentID = pid
	}
	if has("type") {
		p.Downloadable, p.Virtual = flags["downloadable"], flags["virtual"]
	}
	if has("sku") {
		sku := strings.TrimSpace(f["sku"])
		if s.gooSKUTaken(ctx, sku, p.ID) {
			return false, fmt.Errorf("Invalid or duplicated SKU.")
		}
		p.SKU = sku
	}
	if has("name") && (f["name"] != "" || p.Type != gooTypeVariation) {
		p.Name = f["name"]
	}
	if has("published") {
		switch strings.TrimSpace(f["published"]) {
		case "1":
			p.Status = "publish"
		case "-1":
			p.Status = "private"
		default:
			p.Status = "draft"
			if p.Type == gooTypeVariation {
				p.Status = "private"
			}
		}
	} else if created {
		p.Status = "publish"
	}
	if has("featured") {
		p.Featured = f["featured"] == "1"
	}
	if has("catalog_visibility") && f["catalog_visibility"] != "" {
		p.CatalogVisibility = f["catalog_visibility"]
	}
	if has("short_description") && p.Type != gooTypeVariation {
		p.Post.Excerpt = f["short_description"]
	}
	if has("description") {
		if p.Type == gooTypeVariation {
			p.Description = f["description"]
		} else {
			p.Post.Content = f["description"]
		}
	}
	if has("date_on_sale_from") {
		p.SaleFrom = gooParseCSVDate(f["date_on_sale_from"], s.loc(), false)
	}
	if has("date_on_sale_to") {
		p.SaleTo = gooParseCSVDate(f["date_on_sale_to"], s.loc(), true)
	}
	if has("tax_status") && f["tax_status"] != "" {
		p.TaxStatus = f["tax_status"]
	}
	if has("tax_class") {
		p.TaxClass = f["tax_class"]
	}
	if has("stock_status") {
		switch strings.ToLower(strings.TrimSpace(f["stock_status"])) {
		case "1", "instock":
			p.StockStatus = "instock"
		case "backorder", "onbackorder":
			p.StockStatus = "onbackorder"
		case "0", "outofstock":
			p.StockStatus = "outofstock"
		}
	}
	if has("stock_quantity") {
		if q := gooIntPtrFrom(f["stock_quantity"]); q != nil {
			p.ManageStock, p.StockQuantity = true, q
		} else {
			p.ManageStock, p.StockQuantity = false, nil
		}
	}
	if has("low_stock_amount") {
		p.LowStockAmount = gooIntPtrFrom(f["low_stock_amount"])
	}
	if has("backorders") {
		p.Backorders = map[string]string{"1": "yes", "yes": "yes", "notify": "notify"}[strings.ToLower(strings.TrimSpace(f["backorders"]))]
		if p.Backorders == "" {
			p.Backorders = "no"
		}
	}
	if has("sold_individually") {
		p.SoldIndividually = f["sold_individually"] == "1"
	}
	for _, k := range []string{"weight", "length", "width", "height"} {
		if has(k) {
			val := strings.TrimSpace(f[k])
			switch k {
			case "weight":
				p.Weight = val
			case "length":
				p.Length = val
			case "width":
				p.Width = val
			case "height":
				p.Height = val
			}
		}
	}
	if has("reviews_allowed") && p.Type != gooTypeVariation {
		p.Post.CommentStatus = map[bool]string{true: "open", false: "closed"}[f["reviews_allowed"] == "1"]
	}
	if has("purchase_note") {
		p.PurchaseNote = f["purchase_note"]
	}
	if has("regular_price") {
		p.RegularPrice = s.gooCSVAmount(f["regular_price"])
	}
	if has("sale_price") {
		p.SalePrice = s.gooCSVAmount(f["sale_price"])
	}
	if has("shipping_class_id") {
		p.ShippingClassID = 0
		if name := strings.TrimSpace(f["shipping_class_id"]); name != "" {
			if ids, err := s.store.TermIDsByNames(ctx, "product_shipping_class", []string{name}); err == nil && len(ids) > 0 {
				p.ShippingClassID = ids[0]
			}
		}
	}
	if has("images") {
		var ids []int64
		for _, u := range gooCSVSplit(f["images"]) {
			id, err := s.gooImportImage(ctx, u, editorID)
			if err != nil {
				return false, fmt.Errorf("Error getting image %q: %v", u, err)
			}
			if id > 0 {
				ids = append(ids, id)
			}
		}
		p.ImageID, p.GalleryIDs = 0, nil
		if len(ids) > 0 {
			p.ImageID = ids[0]
			if p.Type != gooTypeVariation {
				p.GalleryIDs = ids[1:]
			}
		}
	}
	if has("download_limit") {
		p.DownloadLimit = gooLimitFrom(f["download_limit"])
	}
	if has("download_expiry") {
		p.DownloadExpiry = gooLimitFrom(f["download_expiry"])
	}
	if has("grouped_products") && p.Type == gooTypeGrouped {
		p.Children = s.gooRefIDs(ctx, f["grouped_products"], idMap)
	}
	if has("upsell_ids") {
		p.UpsellIDs = s.gooRefIDs(ctx, f["upsell_ids"], idMap)
	}
	if has("cross_sell_ids") {
		p.CrossSellIDs = s.gooRefIDs(ctx, f["cross_sell_ids"], idMap)
	}
	if has("product_url") {
		p.ProductURL = f["product_url"]
	}
	if has("button_text") {
		p.ButtonText = f["button_text"]
	}
	if has("menu_order") {
		p.Post.MenuOrder, _ = strconv.Atoi(strings.TrimSpace(f["menu_order"]))
	}

	// Downloads (Download N ID / name / URL).
	var dls []gooDownload
	touchedDl := false
	for i := 1; i <= 50; i++ {
		n := strconv.Itoa(i)
		if !has("downloads:url" + n) {
			if i > 1 && !has("downloads:name"+n) {
				break
			}
			continue
		}
		touchedDl = true
		if u := strings.TrimSpace(f["downloads:url"+n]); u != "" {
			dls = append(dls, gooDownload{ID: strings.TrimSpace(f["downloads:id"+n]), Name: f["downloads:name"+n], File: u})
		}
	}
	if touchedDl {
		p.Downloads = dls
	}

	// Attributes.
	attrs, touchedAttrs, err := s.gooImportAttributes(ctx, f, p.Type == gooTypeVariation)
	if err != nil {
		return false, err
	}
	if touchedAttrs {
		if p.Type == gooTypeVariation {
			if p.VariationAttrs == nil {
				p.VariationAttrs = map[string]string{}
			}
			for _, ia := range attrs {
				p.VariationAttrs[ia.attr.Key()] = ia.value
				s.gooMergeParentAttr(parent, ia.attr, ia.value)
			}
		} else {
			old := map[string]gooProductAttr{}
			for _, a := range p.Attributes {
				old[a.Key()] = a
			}
			p.Attributes = nil
			for i, ia := range attrs {
				a := ia.attr
				a.Position = i
				if prev, ok := old[a.Key()]; ok {
					a.Variation = prev.Variation
				}
				if gooSubsVariableLike(p.Type) && !a.Variation && ia.def != "" {
					a.Variation = true
				}
				p.Attributes = append(p.Attributes, a)
				if ia.def != "" {
					p.DefaultAttributes[a.Key()] = ia.def
				}
			}
		}
	}

	// "Meta: key" columns restore the product meta (modules keep their settings there). An empty
	// cell leaves the stored value alone.
	for k, v := range f {
		key, ok := strings.CutPrefix(k, "meta:")
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		if p.Meta == nil {
			p.Meta = map[string]any{}
		}
		p.Meta[strings.TrimSpace(key)] = gooMetaFromCell(v)
	}

	if created && p.Type != gooTypeVariation {
		// A new product keeps its slug from the name, like wp_insert_post().
		p.Slug = ""
	}
	if p.Type == gooTypeVariation && p.Name == "" && parent != nil {
		p.Name = s.gooVariationTitle(ctx, parent, p)
	}
	if err := s.gooSaveProduct(ctx, p, editorID); err != nil {
		return false, err
	}
	if p.Type == gooTypeVariation && parent != nil && touchedAttrs {
		if err := s.gooSaveProduct(ctx, parent, editorID); err != nil {
			return false, err
		}
	}
	if created && fileID > 0 {
		idMap[fileID] = p.ID
	}
	if p.Type != gooTypeVariation {
		if has("category_ids") {
			var ids []int64
			for _, path := range gooCSVSplit(f["category_ids"]) {
				if id := s.gooCategoryFromPath(ctx, path); id > 0 {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 && created {
				if t, err := s.store.TermBySlug(ctx, "product_cat", s.gooDefaultCatSlug()); err == nil {
					ids = []int64{t.ID}
				}
			}
			_ = s.store.SetPostTerms(ctx, p.ID, "product_cat", ids)
		} else if created {
			if t, err := s.store.TermBySlug(ctx, "product_cat", s.gooDefaultCatSlug()); err == nil {
				_ = s.store.SetPostTerms(ctx, p.ID, "product_cat", []int64{t.ID})
			}
		}
		if has("tag_ids") {
			ids, _ := s.store.TermIDsByNames(ctx, "product_tag", gooCSVSplit(f["tag_ids"]))
			_ = s.store.SetPostTerms(ctx, p.ID, "product_tag", ids)
		}
	}
	return created, nil
}

// gooMergeParentAttr adds a variation's attribute value to its parent and flags the attribute "used for
// variations" (WC_Product_CSV_Importer::get_variation_parent_attributes()).
func (s *Server) gooMergeParentAttr(parent *gooProduct, a gooProductAttr, value string) {
	if parent == nil {
		return
	}
	for i := range parent.Attributes {
		if parent.Attributes[i].Key() == a.Key() {
			parent.Attributes[i].Variation = true
			if value != "" && !containsStr(parent.Attributes[i].Options, value) {
				parent.Attributes[i].Options = append(parent.Attributes[i].Options, value)
			}
			return
		}
	}
	a.Variation, a.Visible, a.Position = true, true, len(parent.Attributes)
	a.Options = nil
	if value != "" {
		a.Options = []string{value}
	}
	parent.Attributes = append(parent.Attributes, a)
}

type gooImportedAttr struct {
	attr  gooProductAttr
	value string // a variation's value (slug for taxonomy attributes)
	def   string // the default value of a parent
}

// gooImportAttributes reads "Attribute N name / value(s) / visible / global / default"; global attributes
// (and their terms) are created when missing.
func (s *Server) gooImportAttributes(ctx context.Context, f map[string]string, variation bool) ([]gooImportedAttr, bool, error) {
	var out []gooImportedAttr
	touched := false
	var nums []int
	for k := range f {
		if rest, ok := strings.CutPrefix(k, "attributes:name"); ok {
			if n, err := strconv.Atoi(rest); err == nil {
				nums = append(nums, n)
			}
		}
	}
	sort.Ints(nums)
	for _, num := range nums {
		touched = true
		n := strconv.Itoa(num)
		name := strings.TrimSpace(f["attributes:name"+n])
		if name == "" {
			continue
		}
		global := f["attributes:taxonomy"+n] == "1"
		a := gooProductAttr{Name: name, Visible: f["attributes:visible"+n] != "0"}
		raw := f["attributes:value"+n]
		var values []string
		if variation {
			if v := strings.TrimSpace(raw); v != "" {
				values = []string{v}
			}
		} else {
			values = gooCSVSplit(raw)
		}
		if global {
			ga, err := s.gooFindOrCreateAttribute(ctx, name)
			if err != nil {
				return nil, touched, err
			}
			a.ID, a.Taxonomy, a.Name = ga.ID, ga.Taxonomy(), ga.Name
			var slugs []string
			for _, v := range values {
				ids, err := s.store.TermIDsByNames(ctx, a.Taxonomy, []string{v})
				if err != nil || len(ids) == 0 {
					continue
				}
				if t, err := s.store.GetTerm(ctx, ids[0]); err == nil {
					slugs = append(slugs, t.Slug)
				}
			}
			values = slugs
		}
		ia := gooImportedAttr{attr: a}
		if variation {
			if len(values) > 0 {
				ia.value = values[0]
			}
		} else {
			a.Options = values
			ia.attr = a
			if def := strings.TrimSpace(f["attributes:default"+n]); def != "" {
				ia.def = def
				if global {
					if t, err := s.store.TermByName(ctx, a.Taxonomy, def); err == nil {
						ia.def = t.Slug
					}
				}
			}
		}
		out = append(out, ia)
	}
	return out, touched, nil
}

// gooFindOrCreateAttribute finds a global attribute by name or slug, creating it when missing.
func (s *Server) gooFindOrCreateAttribute(ctx context.Context, name string) (*gooAttribute, error) {
	attrs, err := s.gooAttributes(ctx)
	if err != nil {
		return nil, err
	}
	slug := gooSanitizeKey(name)
	for _, a := range attrs {
		if strings.EqualFold(a.Name, name) || a.Slug == slug || "pa_"+a.Slug == slug {
			return a, nil
		}
	}
	a := &gooAttribute{Name: name, Slug: strings.TrimPrefix(slug, "pa_")}
	if len(a.Slug) > 28 {
		a.Slug = a.Slug[:28]
	}
	if gooReservedTerms[a.Slug] {
		a.Slug = "attr-" + a.Slug
	}
	return a, s.gooSaveAttribute(ctx, a)
}

// gooCategoryFromPath resolves "Parent > Child", creating the missing categories.
func (s *Server) gooCategoryFromPath(ctx context.Context, path string) int64 {
	var parent int64
	for _, name := range strings.Split(path, ">") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var id int64
		_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM terms WHERE taxonomy = 'product_cat' AND lower(name) = lower($1) AND coalesce(parent_id,0) = $2 LIMIT 1`, name, parent).Scan(&id)
		if id == 0 {
			t := &store.Term{Taxonomy: "product_cat", Name: name, ParentID: parent}
			if err := s.store.InsertTerm(ctx, t); err != nil {
				return 0
			}
			id = t.ID
		}
		parent = id
	}
	return parent
}

// gooAttachmentByURL finds the attachment of an uploads URL of this site.
func (s *Server) gooAttachmentByURL(ctx context.Context, u string) int64 {
	_, rel, ok := strings.Cut(u, "/wp-content/uploads/")
	if !ok {
		return 0
	}
	rel, _, _ = strings.Cut(rel, "?") // a replaced file's URL carries ?v= (media_replace.go)
	rel, _, _ = strings.Cut(rel, "#")
	var id int64
	_ = s.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE file_path = $1 LIMIT 1`, rel).Scan(&id)
	return id
}

const gooImportSourceKey = "_wc_attachment_source_url"

// gooImportAllowPrivate lets tests download images from httptest servers on the loopback address.
var gooImportAllowPrivate atomic.Bool

// gooImportImageClient fetches product images from public addresses only (checked when the connection is
// made, so DNS tricks and redirects cannot reach the private network), like the webhook client.
var gooImportImageClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{TLSHandshakeTimeout: 10 * time.Second, DialContext: (&net.Dialer{Timeout: 10 * time.Second,
		Control: publicOnly(gooImportAllowPrivate.Load, "the image URL points at a private network address")}).DialContext},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

// gooImportImage resolves a CSV image URL to a Media Library attachment like WC_Product_CSV_Importer::
// get_attachment_id_from_url(): an uploads URL of this site is matched by file, a remote http(s) URL that
// was imported before is found by its stored source URL, anything else is downloaded into the Media
// Library (and remembered by URL so the next row with the same image does not download it again).
func (s *Server) gooImportImage(ctx context.Context, rawURL string, uploaderID int64) (int64, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return 0, nil
	}
	if id := s.gooAttachmentByURL(ctx, u); id > 0 {
		return id, nil
	}
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
		return 0, nil // not a downloadable URL: ignored, as before
	}
	var id int64
	if err := s.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE meta->>$1 = $2 ORDER BY id LIMIT 1`, gooImportSourceKey, u).Scan(&id); err == nil && id > 0 {
		return id, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Pursae/"+Version+"; product-importer")
	req.Header.Set("Accept", "image/*")
	resp, err := gooImportImageClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the server answered %d", resp.StatusCode)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if mt != "" && !strings.HasPrefix(mt, "image/") {
		return 0, fmt.Errorf("not an image (%s)", mt)
	}
	name := path.Base(pu.Path)
	if name == "" || name == "." || name == "/" {
		name = "image"
	}
	if path.Ext(name) == "" {
		if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
			name += exts[0]
		}
	}
	a, err := s.insertAttachmentFile(ctx, name, resp.Body, uploaderID, 0)
	if err != nil {
		return 0, err
	}
	a.Meta[gooImportSourceKey] = u
	if err := s.store.UpdateAttachment(ctx, a); err != nil {
		return a.ID, nil
	}
	return a.ID, nil
}
