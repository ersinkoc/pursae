package server

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// Goocommerce Wishlists (WooCommerce Wishlists): customers — and guests, through a wishlist key kept in
// a cookie and the session — save products to lists (public, shared by link, or private), share them
// at /?goo-wishlist=<token>, move items to the cart and see when a price dropped. The "Wishlist" page
// ([goocommerce_wishlist]) and the My Account "Wishlist" tab show the lists; Goocommerce › Wishlists
// lists the most wished-for products. A guest's lists join the customer's account the first time the
// logged-in customer's request still carries the guest key.

const (
	gooWLKey    = "wishlist"
	gooWLCookie = "goocommerce_wishlist_key"
	gooWLNonce  = "goo-wishlist"
)

var gooWLPrivacy = []gooOpt{{"public", "Public"}, {"shared", "Shared"}, {"private", "Private"}}

type gooWishlist struct {
	ID         int64
	Token      string
	UserID     int64
	SessionKey string
	Title      string
	Privacy    string
	IsDefault  bool
	Created    time.Time
	Count      int
}

type gooWishlistItem struct {
	ID          int64
	WishlistID  int64
	ProductID   int64
	VariationID int64
	Quantity    int
	Added       time.Time
	PriceAtAdd  *float64
}

func init() {
	gooRegisterExtension(&gooExtension{
		Key:         gooWLKey,
		Name:        "Wishlists",
		Description: "Let customers and guests save products to wishlists, share them by link, move items to the cart and see price drops.",
		Icon:        "dashicons-heart",
		Category:    "Customer engagement",
		Fields: []gooField{
			{ID: "general", Title: "Wishlist options", Type: "title"},
			{ID: "button_text", Title: "Button text", Type: "text", Default: "Add to wishlist"},
			{ID: "added_text", Title: "Added button text", Type: "text", Default: "Remove from wishlist"},
			{ID: "show_in_loops", Title: "Catalog", Type: "checkbox", Default: true, Desc: "Show the wishlist button in product lists"},
			{ID: "require_login", Title: "Require login", Type: "checkbox", Default: false, Desc: "Only logged-in customers can use wishlists"},
			{ID: "redirect_after_add", Title: "Redirect", Type: "checkbox", Default: false, Desc: "Go to the wishlist after adding a product"},
			{ID: "remove_on_cart", Title: "Move to cart", Type: "checkbox", Default: true, Desc: "Remove an item from the wishlist when it is added to the cart"},
			{ID: "general_end", Type: "sectionend"},
		},
		OnActivate: func(s *Server, ctx context.Context) error {
			_, err := s.gooXCEnsurePage(ctx, "wishlist", "wishlist", "Wishlist", "[goocommerce_wishlist]")
			return err
		},
		Pages: []gooExtMenuItem{{Page: "wishlists", Title: "Wishlists"}},
	})
	gooExtAdminPages["wishlists"] = func(s *Server, c *gin.Context) { s.gooWLAdmin(c) }
	gooOnHTML("single_summary_end", func(s *Server, c *gin.Context, p *gooProduct) template.HTML { return s.gooWLButton(c, p, true) })
	gooOnHTML("loop_after_item", func(s *Server, c *gin.Context, p *gooProduct) template.HTML { return s.gooWLButton(c, p, false) })
	gooOnPage("footer", func(s *Server, c *gin.Context) template.HTML {
		if !s.gooExtOn(gooWLKey) {
			return ""
		}
		return template.HTML(`<link rel="stylesheet" id="goo-ext-wishlist-css" href="` + gooAssets + `/css/ext-wishlist.css?ver=` + gooVersion + `" media="all">` +
			`<script src="` + gooAssets + `/js/ext-wishlist.js?ver=` + gooVersion + `" id="goo-ext-wishlist-js" data-endpoint="` +
			gooEsc(s.links(c).HomeURL("/?goo-ajax=wishlist_toggle")) + `" defer></script>`)
	})
	gooFrontRoutes = append(gooFrontRoutes, gooWLRoute)
	gooContentFilters = append(gooContentFilters, gooWLShortcode)
	gooAccountEndpoints = append(gooAccountEndpoints, &gooAccountEndpoint{Slug: "wishlist", Title: "Wishlist", Module: gooWLKey, After: "orders",
		Render: func(s *Server, c *gin.Context, arg string) template.HTML { return s.gooWLAccount(c, arg) }})
	gooOn("daily", 50, func(s *Server, ctx context.Context, _ any) {
		if s.gooExtOn(gooWLKey) { // forgotten guest lists
			_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_wishlists WHERE user_id = 0 AND session_key <> '' AND created < now() - interval '60 days'
				AND NOT EXISTS (SELECT 1 FROM goo_wishlist_items i WHERE i.wishlist_id = goo_wishlists.id AND i.added > now() - interval '30 days')`)
		}
	})
}

// Storage ---------------------------------------------------------------------------------------------

const gooWLCols = `w.id, w.token, w.user_id, w.session_key, w.title, w.privacy, w.is_default, w.created,
	(SELECT count(*) FROM goo_wishlist_items i WHERE i.wishlist_id = w.id)`

func gooWLScan(row pgx.Row) (*gooWishlist, error) {
	w := &gooWishlist{}
	err := row.Scan(&w.ID, &w.Token, &w.UserID, &w.SessionKey, &w.Title, &w.Privacy, &w.IsDefault, &w.Created, &w.Count)
	return w, err
}

func (s *Server) gooWLQuery(ctx context.Context, where string, args ...any) ([]*gooWishlist, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+gooWLCols+` FROM goo_wishlists w WHERE `+where+` ORDER BY w.is_default DESC, w.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*gooWishlist
	for rows.Next() {
		w, err := gooWLScan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// gooWLByToken finds a list by its share token.
func (s *Server) gooWLByToken(ctx context.Context, token string) (*gooWishlist, error) {
	if token == "" {
		return nil, errors.New("no token")
	}
	return gooWLScan(s.store.Pool.QueryRow(ctx, `SELECT `+gooWLCols+` FROM goo_wishlists w WHERE w.token = $1`, token))
}

// gooWLCreate inserts a list for a customer (userID) or a guest (sessionKey).
func (s *Server) gooWLCreate(ctx context.Context, userID int64, sessionKey, title, privacy string, isDefault bool) (*gooWishlist, error) {
	if !gooWLValidPrivacy(privacy) {
		privacy = "private"
	}
	w := &gooWishlist{Token: gooRandomKey(20), UserID: userID, SessionKey: sessionKey, Title: defaultStr(strings.TrimSpace(title), "Wishlist"), Privacy: privacy, IsDefault: isDefault}
	if userID != 0 {
		w.SessionKey = ""
	}
	err := s.store.Pool.QueryRow(ctx, `INSERT INTO goo_wishlists (token, user_id, session_key, title, privacy, is_default) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created`,
		w.Token, w.UserID, w.SessionKey, w.Title, w.Privacy, w.IsDefault).Scan(&w.ID, &w.Created)
	return w, err
}

func gooWLValidPrivacy(p string) bool { return p == "public" || p == "shared" || p == "private" }

// gooWLItems lists a list's items, newest first.
func (s *Server) gooWLItems(ctx context.Context, wishlistID int64) ([]*gooWishlistItem, error) {
	rows, err := s.store.Pool.Query(ctx, `SELECT id, wishlist_id, product_id, variation_id, quantity, added, price_at_add::float8
		FROM goo_wishlist_items WHERE wishlist_id = $1 ORDER BY added DESC, id DESC`, wishlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*gooWishlistItem
	for rows.Next() {
		it := &gooWishlistItem{}
		if err := rows.Scan(&it.ID, &it.WishlistID, &it.ProductID, &it.VariationID, &it.Quantity, &it.Added, &it.PriceAtAdd); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// gooWLAddItem adds a product (or raises nothing when it is already there) with today's price.
func (s *Server) gooWLAddItem(ctx context.Context, w *gooWishlist, productID, variationID int64, qty int) error {
	if qty <= 0 {
		qty = 1
	}
	var price *float64
	pid := productID
	if variationID != 0 {
		pid = variationID
	}
	if p, err := s.gooGetProduct(ctx, pid); err == nil && p.HasPrice() {
		v := s.gooDisplayPrice(ctx, p)
		price = &v
	}
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO goo_wishlist_items (wishlist_id, product_id, variation_id, quantity, price_at_add) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (wishlist_id, product_id, variation_id) DO NOTHING`, w.ID, productID, variationID, qty, price)
	return err
}

// gooWLRemoveItem removes a product from a list.
func (s *Server) gooWLRemoveItem(ctx context.Context, w *gooWishlist, productID, variationID int64) error {
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM goo_wishlist_items WHERE wishlist_id = $1 AND product_id = $2 AND variation_id = $3`, w.ID, productID, variationID)
	return err
}

// gooWLHas reports whether a list holds a product (any variation when variationID is -1).
func (s *Server) gooWLHas(ctx context.Context, w *gooWishlist, productID, variationID int64) bool {
	if w == nil {
		return false
	}
	var n int
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_wishlist_items WHERE wishlist_id = $1 AND product_id = $2 AND ($3 < 0 OR variation_id = $3)`,
		w.ID, productID, variationID).Scan(&n)
	return n > 0
}

// Owner -------------------------------------------------------------------------------------------------

// gooWLGuestKey is the guest's wishlist key (session Extra, else the cookie); create makes one.
func (s *Server) gooWLGuestKey(c *gin.Context, create bool) string {
	sess := s.gooSessionFor(c)
	if k, ok := sess.Data.Extra["wishlist_key"].(string); ok && len(k) == 32 && gooHexOnly(k) {
		return k
	}
	if ck, err := c.Cookie(gooWLCookie); err == nil && len(ck) == 32 && gooHexOnly(ck) {
		if currentUser(c) == nil {
			if sess.Data.Extra == nil {
				sess.Data.Extra = map[string]any{}
			}
			sess.Data.Extra["wishlist_key"] = ck
			sess.touch()
		}
		return ck
	}
	if !create {
		return ""
	}
	k := gooRandomKey(32)
	if sess.Data.Extra == nil {
		sess.Data.Extra = map[string]any{}
	}
	sess.Data.Extra["wishlist_key"] = k
	sess.touch()
	http.SetCookie(c.Writer, &http.Cookie{Name: gooWLCookie, Value: k, Path: "/", Expires: time.Now().Add(365 * 24 * time.Hour),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.Request.TLS != nil})
	return k
}

// gooWLMerge moves a guest's lists into the logged-in customer's account: the guest's default list
// is merged into the customer's default one, other lists change owner. It runs lazily, once, when the
// customer's request still carries the guest key (session Extra or cookie).
func (s *Server) gooWLMerge(c *gin.Context) {
	u := currentUser(c)
	if u == nil {
		return
	}
	if _, done := c.Get("goo.wl.merged"); done {
		return
	}
	c.Set("goo.wl.merged", true)
	key := s.gooWLGuestKey(c, false)
	if key == "" {
		return
	}
	ctx := c.Request.Context()
	guest, _ := s.gooWLQuery(ctx, `w.user_id = 0 AND w.session_key = $1`, key)
	if len(guest) > 0 {
		// A customer without lists takes the guest's lists as they are; else the guest's default list
		// is merged into the customer's default one.
		def := s.gooWLUserDefault(ctx, u.ID, false)
		for _, g := range guest {
			if def == nil {
				_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_wishlists SET user_id = $2, session_key = '' WHERE id = $1`, g.ID, u.ID)
				continue
			}
			if g.IsDefault && def != nil {
				_, _ = s.store.Pool.Exec(ctx, `INSERT INTO goo_wishlist_items (wishlist_id, product_id, variation_id, quantity, added, price_at_add)
					SELECT $1, product_id, variation_id, quantity, added, price_at_add FROM goo_wishlist_items WHERE wishlist_id = $2
					ON CONFLICT (wishlist_id, product_id, variation_id) DO NOTHING`, def.ID, g.ID)
				_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_wishlists WHERE id = $1`, g.ID)
				continue
			}
			_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_wishlists SET user_id = $2, session_key = '', is_default = false WHERE id = $1`, g.ID, u.ID)
		}
	}
	sess := s.gooSessionFor(c)
	if _, ok := sess.Data.Extra["wishlist_key"]; ok {
		delete(sess.Data.Extra, "wishlist_key")
		sess.touch()
	}
	gooClearCookie(c, gooWLCookie)
}

// gooWLUserDefault is a customer's default list (created on demand).
func (s *Server) gooWLUserDefault(ctx context.Context, userID int64, create bool) *gooWishlist {
	lists, _ := s.gooWLQuery(ctx, `w.user_id = $1`, userID)
	for _, w := range lists {
		if w.IsDefault {
			return w
		}
	}
	if len(lists) > 0 {
		return lists[0]
	}
	if !create {
		return nil
	}
	w, err := s.gooWLCreate(ctx, userID, "", "Wishlist", "private", true)
	if err != nil {
		return nil
	}
	return w
}

// gooWLLists are the visitor's own lists.
func (s *Server) gooWLLists(c *gin.Context) []*gooWishlist {
	ctx := c.Request.Context()
	if u := currentUser(c); u != nil {
		s.gooWLMerge(c)
		lists, _ := s.gooWLQuery(ctx, `w.user_id = $1`, u.ID)
		return lists
	}
	key := s.gooWLGuestKey(c, false)
	if key == "" {
		return nil
	}
	lists, _ := s.gooWLQuery(ctx, `w.user_id = 0 AND w.session_key = $1`, key)
	return lists
}

// gooWLDefault is the visitor's default list; create makes it (and a guest key) when missing.
func (s *Server) gooWLDefault(c *gin.Context, create bool) *gooWishlist {
	if v, ok := c.Get("goo.wl.default"); ok && v.(*gooWishlist) != nil {
		return v.(*gooWishlist)
	}
	ctx := c.Request.Context()
	var w *gooWishlist
	if u := currentUser(c); u != nil {
		s.gooWLMerge(c)
		w = s.gooWLUserDefault(ctx, u.ID, create)
	} else {
		key := s.gooWLGuestKey(c, create)
		if key != "" {
			lists, _ := s.gooWLQuery(ctx, `w.user_id = 0 AND w.session_key = $1`, key)
			for _, l := range lists {
				if l.IsDefault || w == nil {
					w = l
				}
			}
			if w == nil && create {
				w, _ = s.gooWLCreate(ctx, 0, key, "Wishlist", "private", true)
			}
		}
	}
	if w != nil {
		c.Set("goo.wl.default", w)
	}
	return w
}

// gooWLOwns reports whether the visitor owns a list.
func (s *Server) gooWLOwns(c *gin.Context, w *gooWishlist) bool {
	if w == nil {
		return false
	}
	if u := currentUser(c); u != nil {
		return w.UserID == u.ID
	}
	return w.UserID == 0 && w.SessionKey != "" && w.SessionKey == s.gooWLGuestKey(c, false)
}

// gooWLCanView: owners always; others for public and shared lists.
func (s *Server) gooWLCanView(c *gin.Context, w *gooWishlist) bool {
	return w != nil && (w.Privacy != "private" || s.gooWLOwns(c, w))
}

// gooWLShareURL is a list's share link.
func (s *Server) gooWLShareURL(ctx context.Context, w *gooWishlist) string {
	return s.links(ctx).HomeURL("/?goo-wishlist=" + url.QueryEscape(w.Token))
}

// gooWLPageURL is the Wishlist page (My Account › Wishlist when the page is missing).
func (s *Server) gooWLPageURL(ctx context.Context) string {
	if s.gooPageID("wishlist") > 0 {
		return s.gooPageURL(ctx, "wishlist")
	}
	return s.gooAccountURL(ctx, "wishlist")
}

// gooWLListed caches the product ids of the visitor's default list for the buttons of a page.
func (s *Server) gooWLListed(c *gin.Context) map[int64]bool {
	if v, ok := c.Get("goo.wl.listed"); ok && v.(map[int64]bool) != nil {
		return v.(map[int64]bool)
	}
	out := map[int64]bool{}
	if w := s.gooWLDefault(c, false); w != nil {
		items, _ := s.gooWLItems(c.Request.Context(), w.ID)
		for _, it := range items {
			out[it.ProductID] = true
		}
	}
	c.Set("goo.wl.listed", out)
	return out
}

// Front ---------------------------------------------------------------------------------------------------

// gooWLButton is the heart button (a small form, enhanced by ext-wishlist.js).
func (s *Server) gooWLButton(c *gin.Context, p *gooProduct, single bool) template.HTML {
	if !s.gooExtOn(gooWLKey) || !gooXCVisible(p) || (!single && !s.gooExtBool(gooWLKey, "show_in_loops")) {
		return ""
	}
	in := s.gooWLListed(c)[p.ID]
	text := defaultStr(s.gooExtString(gooWLKey, "button_text"), "Add to wishlist")
	cls, heart, pressed := "goo-wishlist-button", "&#9825;", "false"
	if in {
		text = defaultStr(s.gooExtString(gooWLKey, "added_text"), "Remove from wishlist")
		cls, heart, pressed = cls+" added", "&#9829;", "true"
	}
	if single {
		cls += " button"
	}
	return template.HTML(`<div class="goo-wishlist-button-wrap"><form class="goo-wishlist-form" method="post" action="">` +
		`<input type="hidden" name="goo_module" value="wishlist"><input type="hidden" name="goo_wishlist_action" value="toggle">` +
		`<input type="hidden" name="product_id" value="` + itoa64(p.ID) + `"><input type="hidden" name="_wpnonce" value="` + gooEsc(s.gooWLNonceValue(c)) + `">` +
		`<button type="submit" class="` + cls + `" data-product_id="` + itoa64(p.ID) + `" aria-pressed="` + pressed + `" data-text="` +
		gooEsc(defaultStr(s.gooExtString(gooWLKey, "button_text"), "Add to wishlist")) + `" data-added-text="` +
		gooEsc(defaultStr(s.gooExtString(gooWLKey, "added_text"), "Remove from wishlist")) + `"><span class="goo-wishlist-heart" aria-hidden="true">` + heart +
		`</span> <span class="goo-wishlist-text">` + gooEsc(text) + `</span></button></form></div>`)
}

func (s *Server) gooWLNonceValue(c *gin.Context) string {
	if v, ok := c.Get("goo.wl.nonce"); ok {
		return v.(string)
	}
	n := s.nonce(c, gooWLNonce)
	c.Set("goo.wl.nonce", n)
	return n
}

// gooWLRoute handles /?goo-ajax=wishlist_toggle, the wishlist forms (goo_module=wishlist) and the
// share links /?goo-wishlist=<token>.
func gooWLRoute(s *Server, c *gin.Context, path string) bool {
	if !s.gooExtOn(gooWLKey) {
		return false
	}
	ctx := c.Request.Context()
	if tok := c.Query("goo-wishlist"); tok != "" && strings.Trim(path, "/") == "" && c.Request.Method == http.MethodGet {
		page := s.gooWLPageURL(ctx)
		if u, err := url.Parse(page); err != nil || strings.Trim(u.Path, "/") == "" {
			return false
		}
		sep := "?"
		if strings.Contains(page, "?") {
			sep = "&"
		}
		c.Redirect(http.StatusFound, page+sep+"goo-wishlist="+url.QueryEscape(tok))
		return true
	}
	ajax := c.Query("goo-ajax") == "wishlist_toggle"
	form := c.Request.Method == http.MethodPost && c.PostForm("goo_module") == gooWLKey
	if !ajax && !form {
		return false
	}
	fail := func(code int, msg string, extra gin.H) bool {
		if ajax {
			out := gin.H{"success": false, "message": msg}
			for k, v := range extra {
				out[k] = v
			}
			s.gooSaveSession(c)
			gooXCJSON(c, code, out)
			return true
		}
		s.gooAddNotice(c, "error", gooEsc(msg))
		s.gooSaveSession(c)
		c.Redirect(http.StatusSeeOther, gooXCSafeReferer(c, s.gooWLPageURL(ctx)))
		return true
	}
	if !s.checkNonce(c, gooWLNonce) {
		return fail(http.StatusForbidden, "Your session has expired. Please reload the page and try again.", nil)
	}
	if s.gooExtBool(gooWLKey, "require_login") && currentUser(c) == nil {
		return fail(http.StatusOK, "Please log in to use your wishlist.", gin.H{"login_required": true, "login_url": s.gooAccountURL(ctx, "")})
	}
	action := defaultStr(c.PostForm("goo_wishlist_action"), "toggle")
	pid, _ := strconv.ParseInt(defaultStr(c.PostForm("product_id"), c.Query("product_id")), 10, 64)
	vid, _ := strconv.ParseInt(defaultStr(c.PostForm("variation_id"), c.Query("variation_id")), 10, 64)
	// The list acted on: a posted token the visitor owns, else the default list.
	var w *gooWishlist
	if tok := c.PostForm("wishlist"); tok != "" {
		if have, err := s.gooWLByToken(ctx, tok); err == nil && s.gooWLOwns(c, have) {
			w = have
		} else {
			return fail(http.StatusForbidden, "You cannot change this wishlist.", nil)
		}
	}
	msg, added, redirect := "", false, ""
	switch action {
	case "toggle", "add", "remove":
		p, err := s.gooGetProduct(ctx, pid)
		if err != nil {
			return fail(http.StatusNotFound, "This product does not exist.", nil)
		}
		if p.Type == gooTypeVariation {
			pid, vid = p.ParentID, p.ID
			if parent, err := s.gooGetProduct(ctx, pid); err == nil {
				p = parent
			}
		}
		if !gooXCVisible(p) {
			return fail(http.StatusNotFound, "This product cannot be added to a wishlist.", nil)
		}
		if w == nil {
			w = s.gooWLDefault(c, action != "remove")
		}
		if w == nil {
			break
		}
		has := s.gooWLHas(ctx, w, pid, vid)
		if action == "add" || (action == "toggle" && !has) {
			if err := s.gooWLAddItem(ctx, w, pid, vid, 1); err != nil {
				return fail(http.StatusInternalServerError, err.Error(), nil)
			}
			added, msg = true, "“"+gooEsc(p.Name)+"” has been added to your wishlist."
			if s.gooExtBool(gooWLKey, "redirect_after_add") {
				redirect = s.gooWLPageURL(ctx)
			}
		} else {
			_ = s.gooWLRemoveItem(ctx, w, pid, vid)
			msg = "“" + gooEsc(p.Name) + "” has been removed from your wishlist."
		}
	case "move_to_cart":
		if w == nil {
			w = s.gooWLDefault(c, false)
		}
		if w == nil || !s.gooWLHas(ctx, w, pid, vid) {
			return fail(http.StatusNotFound, "This item is not in your wishlist.", nil)
		}
		buy := pid
		if vid != 0 {
			buy = vid
		}
		qty := 1
		_ = s.store.Pool.QueryRow(ctx, `SELECT quantity FROM goo_wishlist_items WHERE wishlist_id = $1 AND product_id = $2 AND variation_id = $3`, w.ID, pid, vid).Scan(&qty)
		if _, err := s.gooCartFor(c).Add(ctx, buy, max(qty, 1), 0, nil, nil, nil); err != nil {
			return fail(http.StatusOK, err.Error(), nil)
		}
		if s.gooExtBool(gooWLKey, "remove_on_cart") {
			_ = s.gooWLRemoveItem(ctx, w, pid, vid)
		}
		msg = `“` + gooEsc(s.gooWLItemName(ctx, pid, vid)) + `” has been added to your cart. <a href="` + gooEsc(s.gooPageURL(ctx, "cart")) + `" class="button wc-forward">View cart</a>`
	case "create":
		lists := s.gooWLLists(c)
		owner, key := int64(0), ""
		if u := currentUser(c); u != nil {
			owner = u.ID
		} else {
			key = s.gooWLGuestKey(c, true)
		}
		nw, err := s.gooWLCreate(ctx, owner, key, c.PostForm("wishlist_title"), c.PostForm("wishlist_privacy"), len(lists) == 0)
		if err != nil {
			return fail(http.StatusInternalServerError, err.Error(), nil)
		}
		w, msg = nw, "Your wishlist has been created."
	case "update":
		if w == nil {
			return fail(http.StatusBadRequest, "Choose a wishlist.", nil)
		}
		title := strings.TrimSpace(c.PostForm("wishlist_title"))
		privacy := c.PostForm("wishlist_privacy")
		if !gooWLValidPrivacy(privacy) {
			privacy = w.Privacy
		}
		_, _ = s.store.Pool.Exec(ctx, `UPDATE goo_wishlists SET title = $2, privacy = $3 WHERE id = $1`, w.ID, defaultStr(title, w.Title), privacy)
		msg = "Your wishlist has been updated."
	case "delete_list":
		if w == nil {
			return fail(http.StatusBadRequest, "Choose a wishlist.", nil)
		}
		_, _ = s.store.Pool.Exec(ctx, `DELETE FROM goo_wishlists WHERE id = $1`, w.ID)
		w, msg = nil, "Your wishlist has been deleted."
	default:
		return fail(http.StatusBadRequest, "Unknown wishlist action.", nil)
	}
	c.Set("goo.wl.default", (*gooWishlist)(nil))
	c.Set("goo.wl.listed", map[int64]bool(nil))
	count := 0
	if w != nil {
		_ = s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM goo_wishlist_items WHERE wishlist_id = $1`, w.ID).Scan(&count)
	}
	if ajax {
		s.gooSaveSession(c)
		out := gin.H{"success": true, "added": added, "count": count, "message": msg, "wishlist_url": s.gooWLPageURL(ctx)}
		if redirect != "" {
			out["redirect"] = redirect
		}
		gooXCJSON(c, http.StatusOK, out)
		return true
	}
	if msg != "" {
		s.gooAddNotice(c, "success", msg)
	}
	s.gooSaveSession(c)
	if redirect == "" {
		redirect = gooXCSafeReferer(c, s.gooWLPageURL(ctx))
	}
	c.Redirect(http.StatusSeeOther, redirect)
	return true
}

func (s *Server) gooWLItemName(ctx context.Context, pid, vid int64) string {
	id := pid
	if vid != 0 {
		id = vid
	}
	if p, err := s.gooGetProduct(ctx, id); err == nil {
		if p.Type == gooTypeVariation {
			if sum := s.gooVariationSummary(ctx, p); sum != "" {
				return s.gooProductTitle(ctx, p) + " – " + sum
			}
		}
		return s.gooProductTitle(ctx, p)
	}
	return "Product"
}

// gooWLShortcode expands [goocommerce_wishlist]: the shared list of ?goo-wishlist=<token>, else the
// visitor's default list.
func gooWLShortcode(s *Server, ctx context.Context, content string) string {
	if !strings.Contains(content, "[goocommerce_wishlist") || !s.gooExtOn(gooWLKey) {
		return content
	}
	c, ok := ctx.(*gin.Context)
	if !ok || c.Request == nil {
		return content
	}
	out := `<div class="woocommerce goo-wishlist">` + string(s.gooWLView(c, c.Query("goo-wishlist"))) + `</div>`
	for _, tag := range []string{"<p>[goocommerce_wishlist]</p>", "[goocommerce_wishlist]"} {
		content = strings.ReplaceAll(content, tag, out)
	}
	return content
}

// gooWLView renders a list by token (or the default list for "").
func (s *Server) gooWLView(c *gin.Context, token string) template.HTML {
	ctx := c.Request.Context()
	if token != "" {
		w, err := s.gooWLByToken(ctx, token)
		if err != nil || !s.gooWLCanView(c, w) {
			return `<p class="woocommerce-info">This wishlist is private or does not exist.</p>`
		}
		return s.gooWLTable(c, w, s.gooWLOwns(c, w))
	}
	if s.gooExtBool(gooWLKey, "require_login") && currentUser(c) == nil {
		return template.HTML(`<p class="woocommerce-info">Please <a href="` + gooEsc(s.gooAccountURL(ctx, "")) + `">log in</a> to see your wishlist.</p>`)
	}
	w := s.gooWLDefault(c, false)
	if w == nil {
		return template.HTML(`<p class="cart-empty woocommerce-info">Your wishlist is currently empty.</p><p class="return-to-shop"><a class="button wc-backward" href="` +
			gooEsc(s.gooShopURL(ctx)) + `">Return to shop</a></p>`)
	}
	return s.gooWLTable(c, w, true)
}

// gooWLTable is a list's table: products, prices (with a "Price dropped" badge), stock, date added and
// the owner's actions (remove, add to cart), then the share and privacy box.
func (s *Server) gooWLTable(c *gin.Context, w *gooWishlist, owner bool) template.HTML {
	ctx := c.Request.Context()
	items, _ := s.gooWLItems(ctx, w.ID)
	nonce := s.gooWLNonceValue(c)
	hidden := func(action string) string {
		return `<input type="hidden" name="goo_module" value="wishlist"><input type="hidden" name="goo_wishlist_action" value="` + action + `">` +
			`<input type="hidden" name="wishlist" value="` + gooEsc(w.Token) + `"><input type="hidden" name="_wpnonce" value="` + gooEsc(nonce) + `">`
	}
	var b strings.Builder
	b.WriteString(`<h2 class="goo-wishlist-title">` + gooEsc(w.Title) + `</h2>`)
	if len(items) == 0 {
		b.WriteString(`<p class="cart-empty woocommerce-info">This wishlist is currently empty.</p><p class="return-to-shop"><a class="button wc-backward" href="` +
			gooEsc(s.gooShopURL(ctx)) + `">Return to shop</a></p>`)
	} else {
		b.WriteString(`<table class="shop_table shop_table_responsive cart goo-wishlist-table" data-wishlist="` + gooEsc(w.Token) + `"><thead><tr>`)
		if owner {
			b.WriteString(`<th class="product-remove"><span class="screen-reader-text">Remove item</span></th>`)
		}
		b.WriteString(`<th class="product-thumbnail"><span class="screen-reader-text">Thumbnail image</span></th><th class="product-name">Product</th>` +
			`<th class="product-price">Unit price</th><th class="product-stock-status">Stock status</th><th class="product-date">Added</th><th class="product-add-to-cart"><span class="screen-reader-text">Actions</span></th></tr></thead><tbody>`)
		ids := []int64{}
		for _, it := range items {
			ids = append(ids, it.ProductID)
			if it.VariationID != 0 {
				ids = append(ids, it.VariationID)
			}
		}
		prods, _ := s.gooGetProducts(ctx, ids)
		for _, it := range items {
			p := prods[it.ProductID]
			if p == nil {
				continue
			}
			buy := p
			if it.VariationID != 0 {
				if v := prods[it.VariationID]; v != nil {
					v.parent = p
					buy = v
				}
			}
			name := s.gooWLItemName(ctx, it.ProductID, it.VariationID)
			b.WriteString(`<tr class="goo-wishlist-item cart_item">`)
			if owner {
				b.WriteString(`<td class="product-remove"><form method="post" action="">` + hidden("remove") + `<input type="hidden" name="product_id" value="` + itoa64(it.ProductID) +
					`"><input type="hidden" name="variation_id" value="` + itoa64(it.VariationID) + `"><button type="submit" class="remove" aria-label="` +
					gooEsc("Remove "+name+" from wishlist") + `">&times;</button></form></td>`)
			}
			link := s.gooPermalink(ctx, buy)
			b.WriteString(`<td class="product-thumbnail"><a href="` + gooEsc(link) + `">` + string(s.gooXCThumb(ctx, buy, "woocommerce_thumbnail")) + `</a></td>`)
			b.WriteString(`<td class="product-name" data-title="Product"><a href="` + gooEsc(link) + `">` + gooEsc(name) + `</a>`)
			cur := s.gooDisplayPrice(ctx, buy)
			dropped := it.PriceAtAdd != nil && buy.HasPrice() && cur < *it.PriceAtAdd-0.005
			if dropped {
				b.WriteString(` <span class="goo-wishlist-price-drop badge">Price dropped</span>`)
			}
			b.WriteString(`</td><td class="product-price" data-title="Unit price">`)
			if dropped {
				b.WriteString(`<del aria-hidden="true">` + string(s.gooPrice(*it.PriceAtAdd)) + `</del> <span class="screen-reader-text">Price when added: ` +
					gooEsc(s.gooPricePlain(*it.PriceAtAdd, s.gooCurrency())) + `.</span>`)
			}
			b.WriteString(string(s.gooXCPriceHTML(ctx, buy)) + `</td>`)
			avail, cls := s.gooXCAvailability(ctx, buy)
			b.WriteString(`<td class="product-stock-status" data-title="Stock status"><span class="stock ` + cls + `">` + gooEsc(avail) + `</span></td>`)
			b.WriteString(`<td class="product-date" data-title="Added"><time datetime="` + it.Added.Format(time.RFC3339) + `">` + gooEsc(it.Added.Format("January 2, 2006")) + `</time></td>`)
			b.WriteString(`<td class="product-add-to-cart">`)
			purchasable := s.gooIsPurchasable(ctx, buy) && s.gooIsInStock(ctx, buy) && (buy.Type == gooTypeSimple || buy.Type == gooTypeVariation)
			switch {
			case purchasable && owner:
				b.WriteString(`<form method="post" action="">` + hidden("move_to_cart") + `<input type="hidden" name="product_id" value="` + itoa64(it.ProductID) +
					`"><input type="hidden" name="variation_id" value="` + itoa64(it.VariationID) + `"><button type="submit" class="button alt add_to_cart_button">Add to cart</button></form>`)
			case purchasable:
				b.WriteString(string(s.gooXCLoopAddToCart(ctx, buy)))
			default:
				b.WriteString(`<a class="button" href="` + gooEsc(link) + `">` + gooEsc(s.gooXCAddToCartText(p, false)) + `</a>`)
			}
			b.WriteString(`</td></tr>`)
		}
		b.WriteString(`</tbody></table>`)
	}
	if owner {
		b.WriteString(`<div class="goo-wishlist-manage">`)
		if w.Privacy != "private" {
			share := s.gooWLShareURL(ctx, w)
			b.WriteString(`<p class="goo-wishlist-share"><label for="goo-wishlist-share-` + itoa64(w.ID) + `">Share this wishlist</label> <input type="text" readonly id="goo-wishlist-share-` +
				itoa64(w.ID) + `" class="input-text" value="` + gooEsc(share) + `" onfocus="this.select()"> <a class="button" href="mailto:?subject=` +
				url.PathEscape(w.Title) + `&amp;body=` + url.PathEscape(share) + `">Email</a></p>`)
		} else {
			b.WriteString(`<p class="goo-wishlist-private">This wishlist is private. Make it shared or public to get a link you can send.</p>`)
		}
		b.WriteString(`<form method="post" action="" class="goo-wishlist-settings">` + hidden("update") +
			`<p><label for="goo-wl-title-` + itoa64(w.ID) + `">Name</label> <input type="text" class="input-text" id="goo-wl-title-` + itoa64(w.ID) + `" name="wishlist_title" value="` + gooEsc(w.Title) + `"> ` +
			`<label for="goo-wl-privacy-` + itoa64(w.ID) + `">Privacy</label> <select id="goo-wl-privacy-` + itoa64(w.ID) + `" name="wishlist_privacy">`)
		for _, o := range gooWLPrivacy {
			sel := ""
			if o.Value == w.Privacy {
				sel = " selected"
			}
			b.WriteString(`<option value="` + o.Value + `"` + sel + `>` + o.Label + `</option>`)
		}
		b.WriteString(`</select> <button type="submit" class="button">Save</button></p></form></div>`)
	}
	return template.HTML(b.String())
}

// gooWLAccount is My Account › Wishlist: the visitor's lists, a list (arg = token), and a form to add one.
func (s *Server) gooWLAccount(c *gin.Context, arg string) template.HTML {
	if !s.gooExtOn(gooWLKey) {
		return ""
	}
	ctx := c.Request.Context()
	if arg != "" {
		if w, err := s.gooWLByToken(ctx, arg); err == nil && s.gooWLOwns(c, w) {
			return template.HTML(`<p><a href="`+gooEsc(s.gooAccountURL(ctx, "wishlist"))+`">&larr; All wishlists</a></p>`) + s.gooWLTable(c, w, true)
		}
		return `<p class="woocommerce-info">This wishlist does not exist.</p>`
	}
	lists := s.gooWLLists(c)
	var b strings.Builder
	if len(lists) == 0 {
		b.WriteString(`<div class="woocommerce-message woocommerce-message--info woocommerce-Message woocommerce-Message--info woocommerce-info">You have not saved any products yet.</div>`)
	} else {
		b.WriteString(`<table class="woocommerce-orders-table shop_table shop_table_responsive my_account_orders goo-wishlist-lists"><thead><tr><th>Wishlist</th><th>Privacy</th><th>Items</th><th>Created</th><th><span class="screen-reader-text">Actions</span></th></tr></thead><tbody>`)
		for _, w := range lists {
			view := s.gooEndpointURL(ctx, "myaccount", "wishlist", w.Token)
			privacy := w.Privacy
			for _, o := range gooWLPrivacy {
				if o.Value == w.Privacy {
					privacy = o.Label
				}
			}
			b.WriteString(`<tr><td data-title="Wishlist"><a href="` + gooEsc(view) + `">` + gooEsc(w.Title) + `</a></td><td data-title="Privacy">` + gooEsc(privacy) +
				`</td><td data-title="Items">` + strconv.Itoa(w.Count) + `</td><td data-title="Created"><time datetime="` + w.Created.Format(time.RFC3339) + `">` +
				gooEsc(w.Created.Format("January 2, 2006")) + `</time></td><td><a class="woocommerce-button button view" href="` + gooEsc(view) + `">View</a>` +
				`<form method="post" action="" style="display:inline"><input type="hidden" name="goo_module" value="wishlist"><input type="hidden" name="goo_wishlist_action" value="delete_list">` +
				`<input type="hidden" name="wishlist" value="` + gooEsc(w.Token) + `"><input type="hidden" name="_wpnonce" value="` + gooEsc(s.gooWLNonceValue(c)) +
				`"><button type="submit" class="woocommerce-button button delete">Delete</button></form></td></tr>`)
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`<h3>Create a new wishlist</h3><form method="post" action="" class="goo-wishlist-create"><input type="hidden" name="goo_module" value="wishlist">` +
		`<input type="hidden" name="goo_wishlist_action" value="create"><input type="hidden" name="_wpnonce" value="` + gooEsc(s.gooWLNonceValue(c)) + `">` +
		`<p class="form-row"><label for="goo-wl-new-title">Name</label><input type="text" class="input-text" id="goo-wl-new-title" name="wishlist_title" required></p>` +
		`<p class="form-row"><label for="goo-wl-new-privacy">Privacy</label><select id="goo-wl-new-privacy" name="wishlist_privacy">` +
		`<option value="private">Private</option><option value="shared">Shared</option><option value="public">Public</option></select></p>` +
		`<p><button type="submit" class="button">Create wishlist</button></p></form>`)
	return template.HTML(b.String())
}

// Admin ---------------------------------------------------------------------------------------------------

// gooWLAdmin is Goocommerce › Wishlists: totals, the products customers wish for most and recent lists.
func (s *Server) gooWLAdmin(c *gin.Context) {
	ctx := c.Request.Context()
	var stats struct{ Lists, Customers, Guests, Items, Shared int }
	_ = s.store.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE user_id <> 0), count(*) FILTER (WHERE user_id = 0),
		(SELECT count(*) FROM goo_wishlist_items), count(*) FILTER (WHERE privacy <> 'private') FROM goo_wishlists`).
		Scan(&stats.Lists, &stats.Customers, &stats.Guests, &stats.Items, &stats.Shared)
	type popular struct {
		ID           int64
		Name, Edit   string
		Lists, Total int
		Price        template.HTML
	}
	var pop []popular
	rows, err := s.store.Pool.Query(ctx, `SELECT product_id, count(DISTINCT wishlist_id), sum(quantity) FROM goo_wishlist_items GROUP BY product_id
		ORDER BY count(DISTINCT wishlist_id) DESC, product_id LIMIT 20`)
	if err == nil {
		for rows.Next() {
			var p popular
			if rows.Scan(&p.ID, &p.Lists, &p.Total) == nil {
				pop = append(pop, p)
			}
		}
		rows.Close()
	}
	for i := range pop {
		if p, err := s.gooGetProduct(ctx, pop[i].ID); err == nil {
			pop[i].Name, pop[i].Price = p.Name, s.gooXCPriceHTML(ctx, p)
		} else {
			pop[i].Name = "#" + itoa64(pop[i].ID) + " (deleted)"
		}
		pop[i].Edit = "/wp-admin/post?post=" + itoa64(pop[i].ID) + "&action=edit"
	}
	type recent struct {
		*gooWishlist
		Owner, Share string
	}
	var rec []recent
	lists, _ := s.gooWLQuery(ctx, `true`)
	if len(lists) > 30 {
		lists = lists[len(lists)-30:]
	}
	for i := len(lists) - 1; i >= 0; i-- {
		w := lists[i]
		owner := "Guest"
		if w.UserID != 0 {
			if u, err := s.store.UserByID(ctx, w.UserID); err == nil {
				owner = defaultStr(u.DisplayName, u.Login)
			} else {
				owner = "#" + itoa64(w.UserID)
			}
		}
		r := recent{gooWishlist: w, Owner: owner}
		if w.Privacy != "private" {
			r.Share = s.gooWLShareURL(ctx, w)
		}
		rec = append(rec, r)
	}
	s.adminPage(c, "goocommerce?page=wishlists", "Wishlists", "goocommerce_page_goo-wishlists", "admin/goo-ext-wishlist",
		map[string]any{"Stats": stats, "Popular": pop, "Recent": rec, "On": s.gooExtOn(gooWLKey), "PageURL": s.gooWLPageURL(ctx)}, gooFlash(c)...)
}
