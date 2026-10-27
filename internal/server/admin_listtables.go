package server

import "github.com/gin-gonic/gin"

// sortLinks builds the sortable column header links of a list table (WP_List_Table::print_column_headers())
// for the request's orderby/order. cols maps each sortable orderby value to whether its first click
// sorts descending; defCol/defOrder are the list's default sort.
func sortLinks(c *gin.Context, cols map[string]bool, defCol, defOrder string) map[string]sortLink {
	orderby, order := c.Query("orderby"), c.Query("order")
	if _, ok := cols[orderby]; !ok {
		orderby, order = defCol, defOrder
	}
	if order != "asc" && order != "desc" {
		order = defOrder
	}
	out := map[string]sortLink{}
	for col, descFirst := range cols {
		u := *c.Request.URL
		q := u.Query()
		q.Del("paged")
		q.Set("orderby", col)
		cur := col == orderby
		next := "asc"
		if (cur && order == "asc") || (!cur && descFirst) {
			next = "desc"
		}
		q.Set("order", next)
		u.RawQuery = q.Encode()
		sl := sortLink{URL: u.String()}
		switch {
		case cur && order == "asc":
			sl.Class, sl.AriaSort, sl.SRText = "sorted asc", "ascending", "Sort descending."
		case cur:
			sl.Class, sl.AriaSort, sl.SRText = "sorted desc", "descending", "Sort ascending."
		case next == "asc":
			sl.Class, sl.SRText = "sortable desc", "Sort ascending."
		default:
			sl.Class, sl.SRText = "sortable asc", "Sort descending."
		}
		out[col] = sl
	}
	return out
}
