package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// The Forums admin screen of Pursae Forum (/wp-admin/forum), WordPress admin markup throughout:
//
//	?tab=overview    totals, the moderation queue, a 30-day chart, top contributors, activity  forum_moderate
//	?tab=forums      categories and forums (list + add form, ?action=edit&id= edit form)        manage_options
//	?tab=moderation  topics and replies by status, row and bulk actions                          forum_moderate
//	?tab=members     the member list (filters, search, sort, bulk and row actions) and one
//	                 member (?action=member&user=N); roles and badges need manage_options         forum_moderate
//	?tab=badges      point weights, the levels table, the badge list and its add / edit form     manage_options
//	?tab=settings    URL base, titles, paging, registration, posting rules, the member list      manage_options
//	?tab=google      Google sign-in (client id, write-only secret, redirect URI help)            manage_options
//
// Every write is a POST with the pursae-forum-admin nonce, except the row action links (delete a
// forum, moderate one item, block a member, move a badge), which carry the nonce in the query string
// as WordPress' row actions do. Every write answers with a redirect (PRG) and a notice code.

// The screen's tabs.
const (
	forumTabOverview   = "overview"
	forumTabForums     = "forums"
	forumTabModeration = "moderation"
	forumTabMembers    = "members"
	forumTabBadges     = "badges"
	forumTabSettings   = "settings"
	forumTabGoogle     = "google"
)

// forumAdminTabs are the screen's tabs in order: the nav tab label, the page title and the capability
// that opens the tab (the Forums submenu is built from the same list, forumMenu).
var forumAdminTabs = []struct{ Key, Label, Title, Cap string }{
	{forumTabOverview, "Overview", "Forum Overview", capForumModerate},
	{forumTabForums, "Forums", "Forums", "manage_options"},
	{forumTabModeration, "Moderation", "Moderation", capForumModerate},
	{forumTabMembers, "Members", "Forum Members", capForumModerate},
	{forumTabBadges, "Badges & Levels", "Badges & Levels", "manage_options"},
	{forumTabSettings, "Settings", "Forum Settings", "manage_options"},
	{forumTabGoogle, "Google sign-in", "Google Sign-in", "manage_options"},
}

// forumAdminTabCap is the capability of a tab ("" for an unknown tab).
func forumAdminTabCap(tab string) string {
	for _, t := range forumAdminTabs {
		if t.Key == tab {
			return t.Cap
		}
	}
	return ""
}

// forumAdminTab is one nav tab.
type forumAdminTab struct {
	Key, Label, URL string
	Current         bool
}

// forumAdminForm is the add / edit forum form.
type forumAdminForm struct {
	ID, ParentID int64
	Title, Slug  string
	Description  string
	Kind         string
	Closed       bool
	Order        int
	URL          string
}

// forumAdminRow is a forum or category in the Forums list.
type forumAdminRow struct {
	*forumForum
	Depth      int
	ParentName string
	EditURL    string
	DeleteURL  string // "" while it holds topics or forums
}

// forumAdminCat is a top-level category in the parent picker.
type forumAdminCat struct {
	ID    int64
	Title string
}

// forumAdminView is a status filter link (subsubsub).
type forumAdminView struct {
	Key, Label, URL string
	Count           int
	Current         bool
}

// forumAdminAction is a row action link.
type forumAdminAction struct {
	Key, Label, URL string
	Confirm         string // "" = no confirmation
}

// forumAdminItem is a topic or reply in the Moderation list.
type forumAdminItem struct {
	ID, TopicID         int64
	IsTopic             bool
	Title               string // topic title, or the topic a reply belongs to
	Excerpt             string
	Author              *forumAuthor
	ForumTitle          string
	Status, StatusLabel string
	Spam                bool
	Date, ISO           string
	IP                  string
	ViewURL             string
	Actions             []forumAdminAction
	RowClass            string
}

// forumAdminOpt is a bulk action.
type forumAdminOpt struct{ Value, Label string }

// forumAdminData is the dot of admin/forum.
type forumAdminData struct {
	Tab       string
	Title     string // the page heading
	Tabs      []forumAdminTab
	IsAdmin   bool // manage_options: roles, badges, the configuration tabs
	SelfID    int64
	Nonce     string
	IndexURL  string // the forum on the site
	Error     string // a validation error shown above the form
	Forums    []forumAdminRow
	Cats      []forumAdminCat
	New       forumAdminForm // the add form (re-filled after an error)
	Edit      *forumAdminForm
	ModView   string
	Search    string
	Views     []forumAdminView
	Items     []forumAdminItem
	Bulk      []forumAdminOpt
	Paging    pagination
	Settings  forumSettings
	SecretSet bool
	Redirect  string // the Google redirect URI
	Origin    string // the site origin (Authorized JavaScript origins)
	NotHTTPS  bool
	Ready     bool   // Google sign-in is ready
	Missing   string // what keeps an enabled Google sign-in from working ("" = nothing)

	// Overview tab.
	Overview *forumOverview
	Cards    []forumAdminCard
	Chart    []forumAdminBar
	ChartMax int
	ChartMid int
	ChartSum string // the chart's text alternative
	Top      []forumAdminTopRow
	Activity []forumAdminActivity
	Quiet    bool // no member joined and nothing was posted in the 30 days

	// Members tab.
	MemberQuery forumMemberQuery
	ListURL     string // the member list as it is shown (filter, search, sort, page): the bulk form posts there
	MemberRows  []forumAdminMemberRow
	SortLinks   map[string]sortLink
	BadgeOpts   []forumBadge // the badge <select> of the bulk actions and the member screen
	Member      *forumAdminMemberView

	// Badges & Levels tab.
	Levels    forumLevels
	LevelRows []forumAdminLevelRow
	BadgeRows []forumAdminBadgeRow
	BadgeForm forumAdminBadgeForm  // the add form (re-filled after an error)
	BadgeEdit *forumAdminBadgeForm // the edit form (?action=edit&badge=id)
	Rules     []forumAdminRule
	Icons     []string
	CanUpload bool // the media modal can be offered (upload_files)
}

// forumAdminMessages are the notices of ?msg= after a redirect.
var forumAdminMessages = map[string]string{
	"forum_added":      "Forum added.",
	"forum_updated":    "Forum updated.",
	"forum_deleted":    "Forum deleted.",
	"google_saved":     "Google sign-in settings saved.",
	"levels_saved":     "Levels and points saved.",
	"levels_restored":  "The default levels and points are restored.",
	"badge_added":      "Badge added.",
	"badge_updated":    "Badge updated.",
	"badge_deleted":    "Badge deleted.",
	"badge_enabled":    "Badge enabled.",
	"badge_disabled":   "Badge disabled.",
	"badge_moved":      "Badge order saved.",
	"badges_restored":  "The default badges are restored.",
	"member_badges":    "Badges updated.",
	"member_role":      "Forum role updated.",
	"member_blocked":   "Member blocked.",
	"member_unblocked": "Member unblocked.",
	"member_verified":  "Email address marked as verified.",
}

// forumAdminErrors are the error notices of ?error=<code> after a redirect (a code, never free text,
// so a crafted link cannot put its own words on the screen).
var forumAdminErrors = map[string]string{
	"not_empty":        "The forum still holds topics or forums: move or delete them first.",
	"not_found":        "The forum does not exist.",
	"failed":           "The forum could not be deleted.",
	"badge_not_found":  "The badge does not exist.",
	"badge_failed":     "The badges could not be saved.",
	"levels_failed":    "The levels could not be saved.",
	"member_not_found": "The member does not exist.",
	"member_failed":    "The member could not be changed.",
	"self_block":       "You cannot block yourself.",
	"admin_block":      "Administrators cannot be blocked.",
	"mod_block":        "Only an administrator can block a moderator.",
	"self_role":        "You cannot change your own forum role.",
	"denied":           "Sorry, you are not allowed to do that.",
	"no_badge":         "Please choose a badge for this action.",
	"no_members":       "Please select at least one member.",
	"levels_empty":     "Keep at least one level.",
	"levels_min":       "Please enter the minimum points of every level.",
	"levels_same_min":  "Two levels cannot share the same minimum points.",
}

// forumAdminDone are the notices of ?done=<op>&n=<count> after moderation.
var forumAdminDone = map[string][2]string{
	"approve": {"%d item approved.", "%d items approved."},
	"spam":    {"%d item marked as spam.", "%d items marked as spam."},
	"unspam":  {"%d item restored from the spam.", "%d items restored from the spam."},
	"trash":   {"%d item moved to the Trash.", "%d items moved to the Trash."},
	"restore": {"%d item restored from the Trash.", "%d items restored from the Trash."},
	"delete":  {"%d item permanently deleted.", "%d items permanently deleted."},
}

// forumAdminURL is the screen URL of a tab with extra query values.
func forumAdminURL(tab string, kv ...string) string {
	q := url.Values{"tab": {tab}}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	return "/wp-admin/forum?" + q.Encode()
}

// forumAdminErr turns a store error into a sentence for a notice.
func forumAdminErr(err error) string {
	if errors.Is(err, errForumNotFound) {
		return "The forum does not exist."
	}
	msg := strings.TrimPrefix(err.Error(), "forum: ")
	if msg == "" {
		return "Something went wrong."
	}
	msg = strings.ToUpper(msg[:1]) + msg[1:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

// forumAdmin serves GET/POST /wp-admin/forum?tab=overview|forums|moderation|members|badges|settings|google.
func (s *Server) forumAdmin(c *gin.Context) {
	u := currentUser(c)
	if u == nil || !u.Can(capForumModerate, nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	admin := u.Can("manage_options", nil)
	tab := c.Query("tab")
	if tab == "" {
		tab = forumTabOverview
	}
	cp := forumAdminTabCap(tab)
	if cp == "" {
		c.Redirect(http.StatusFound, forumAdminURL(forumTabOverview))
		return
	}
	if !u.Can(cp, nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	d := &forumAdminData{Tab: tab, IsAdmin: admin, SelfID: u.ID, Nonce: s.nonce(c, forumAdminNonce), IndexURL: s.forumURL(c)}
	for _, t := range forumAdminTabs {
		if !u.Can(t.Cap, nil) {
			continue
		}
		if t.Key == tab {
			d.Title = t.Title
		}
		d.Tabs = append(d.Tabs, forumAdminTab{Key: t.Key, Label: t.Label, URL: forumAdminURL(t.Key), Current: t.Key == tab})
	}
	var notices []notice
	if m := forumAdminMessages[c.Query("msg")]; m != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(m)), Dismissible: true})
	}
	if e := forumAdminErrors[c.Query("error")]; e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(e)), Dismissible: true})
	}
	ok := true
	switch tab {
	case forumTabOverview:
		ok = s.forumAdminOverview(c, d)
	case forumTabForums:
		ok = s.forumAdminForums(c, d)
	case forumTabModeration:
		ok = s.forumAdminModeration(c, d, &notices)
	case forumTabMembers:
		ok = s.forumAdminMembers(c, d, &notices)
	case forumTabBadges:
		ok = s.forumAdminBadges(c, d)
	case forumTabSettings, forumTabGoogle:
		ok = s.forumAdminSettings(c, d)
	}
	if !ok {
		return
	}
	if d.Error != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(d.Error))})
	}
	switch {
	case d.Edit != nil:
		d.Title = "Edit Forum"
	case d.BadgeEdit != nil:
		d.Title = "Edit Badge"
	case d.Member != nil:
		d.Title = "Forum Member"
	}
	s.adminPage(c, "forum?tab="+tab, d.Title, "toplevel_page_pursae-forum", "admin/forum", d, notices...)
}

// Forums tab --------------------------------------------------------------------------------------

// forumAdminFormOf reads the add / edit form.
func forumAdminFormOf(c *gin.Context) forumAdminForm {
	id, _ := strconv.ParseInt(c.PostForm("id"), 10, 64)
	parent, _ := strconv.ParseInt(c.PostForm("parent"), 10, 64)
	order, _ := strconv.Atoi(strings.TrimSpace(c.PostForm("order")))
	kind := c.PostForm("kind")
	if kind != "category" {
		kind = "forum"
	}
	return forumAdminForm{ID: id, ParentID: parent, Title: strings.TrimSpace(c.PostForm("title")), Slug: strings.TrimSpace(c.PostForm("slug")),
		Description: c.PostForm("description"), Kind: kind, Closed: c.PostForm("closed") == "1", Order: max(-9999, min(order, 9999))}
}

// forumAdminForums handles the Forums tab; false when it already answered (a redirect).
func (s *Server) forumAdminForums(c *gin.Context, d *forumAdminData) bool {
	ctx := c.Request.Context()
	d.New = forumAdminForm{Kind: "forum"}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		f := forumAdminFormOf(c)
		id, err := s.forumSaveForum(ctx, forumForumInput{ID: f.ID, ParentID: f.ParentID, Title: f.Title, Slug: f.Slug,
			Description: f.Description, Kind: f.Kind, Closed: f.Closed, Order: f.Order}, currentUser(c).ID)
		if err != nil {
			d.Error = forumAdminErr(err)
			if f.ID > 0 {
				d.Edit = &f
			} else {
				d.New = f
			}
		} else {
			msg := "forum_added"
			if f.ID > 0 {
				msg = "forum_updated"
				c.Redirect(http.StatusFound, forumAdminURL("forums", "action", "edit", "id", strconv.FormatInt(id, 10), "msg", msg))
				return false
			}
			c.Redirect(http.StatusFound, forumAdminURL("forums", "msg", msg))
			return false
		}
	} else if c.Query("do") == "delete" {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
		if err := s.forumDeleteForum(ctx, id); err != nil {
			code := "failed"
			switch {
			case errors.Is(err, errForumNotFound):
				code = "not_found"
			case strings.Contains(err.Error(), "not empty"):
				code = "not_empty"
			}
			c.Redirect(http.StatusFound, forumAdminURL("forums", "error", code))
			return false
		}
		c.Redirect(http.StatusFound, forumAdminURL("forums", "msg", "forum_deleted"))
		return false
	}
	list, err := s.forumList(ctx)
	if err != nil {
		d.Error = "The forums could not be read: " + err.Error()
	}
	editID, _ := strconv.ParseInt(c.Query("id"), 10, 64)
	if c.Query("action") == "edit" && d.Edit == nil {
		f, err := s.forumByID(ctx, editID)
		if err != nil {
			c.Redirect(http.StatusFound, forumAdminURL("forums", "error", "not_found"))
			return false
		}
		d.Edit = &forumAdminForm{ID: f.ID, ParentID: f.ParentID, Title: f.Title, Slug: f.Slug, Description: string(f.Description),
			Kind: f.Kind, Closed: f.Closed, Order: f.Order, URL: f.URL}
	}
	// Topics of any status decide whether Delete is offered (forumDeleteForum checks again).
	held := map[int64]bool{}
	if rows, err := s.store.Pool.Query(ctx, `SELECT DISTINCT parent_id FROM posts WHERE type IN ('topic','forum') AND parent_id IS NOT NULL`); err == nil {
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				held[id] = true
			}
		}
		rows.Close()
	}
	add := func(f *forumForum, depth int, parent string) {
		r := forumAdminRow{forumForum: f, Depth: depth, ParentName: parent, EditURL: forumAdminURL("forums", "action", "edit", "id", strconv.FormatInt(f.ID, 10))}
		if !held[f.ID] {
			r.DeleteURL = forumAdminURL("forums", "do", "delete", "id", strconv.FormatInt(f.ID, 10), "_wpnonce", d.Nonce)
		}
		d.Forums = append(d.Forums, r)
	}
	for _, f := range list {
		add(f, 0, "")
		if f.Kind == "category" && f.ParentID == 0 && (d.Edit == nil || d.Edit.ID != f.ID) {
			d.Cats = append(d.Cats, forumAdminCat{ID: f.ID, Title: f.Title})
		}
		for _, ch := range f.Children {
			add(ch, 1, f.Title)
		}
	}
	return true
}

// Moderation tab ----------------------------------------------------------------------------------

// forumAdminViews are the Moderation status filters.
var forumAdminViews = []struct{ Key, Label string }{
	{"all", "All"}, {"pending", "Pending"}, {"spam", "Spam"}, {"trash", "Trash"},
}

// forumAdminViewSQL is the status condition of a Moderation filter (alias p).
func forumAdminViewSQL(view string) string {
	switch view {
	case "pending":
		return "p.status = 'pending' AND " + forumNotSpam("p")
	case "spam":
		return "p.status = 'trash' AND coalesce(p.meta->>'" + forumMetaSpam + "','') = 'true'"
	case "trash":
		return "p.status = 'trash' AND " + forumNotSpam("p")
	}
	return "p.status IN ('publish','pending') AND " + forumNotSpam("p")
}

// forumAdminBulk are the bulk actions offered in each filter.
func forumAdminBulk(view string) []forumAdminOpt {
	switch view {
	case "spam":
		return []forumAdminOpt{{"unspam", "Not Spam"}, {"delete", "Delete Permanently"}}
	case "trash":
		return []forumAdminOpt{{"restore", "Restore"}, {"delete", "Delete Permanently"}}
	}
	return []forumAdminOpt{{"approve", "Approve"}, {"spam", "Mark as Spam"}, {"trash", "Move to Trash"}}
}

// forumAdminApply runs a moderation op on one topic or reply; "delete" deletes it permanently (only
// from the Trash or the spam).
func (s *Server) forumAdminApply(ctx context.Context, id int64, op string, actor int64) error {
	switch op {
	case "approve", "spam", "unspam", "trash", "restore":
		return s.forumModerate(ctx, id, op, actor)
	case "delete":
		return s.forumAdminDelete(ctx, id)
	}
	return fmt.Errorf("forum: unknown action %q", op)
}

// forumAdminDelete permanently deletes a trashed (or spam) topic with its replies, or a reply, and
// recounts what held it.
func (s *Server) forumAdminDelete(ctx context.Context, id int64) error {
	typ, topicID, forumID, err := s.forumParents(ctx, id)
	if err != nil {
		return err
	}
	var status string
	if err := s.store.Pool.QueryRow(ctx, `SELECT status FROM posts WHERE id = $1`, id).Scan(&status); err != nil {
		return err
	}
	if status != "trash" {
		return errors.New("forum: only items in the Trash or the spam can be deleted permanently")
	}
	if err := pgx.BeginFunc(ctx, s.store.Pool, func(tx pgx.Tx) error {
		if typ == "topic" { // its replies go with it (they would be orphans)
			if _, err := tx.Exec(ctx, `DELETE FROM posts WHERE type = 'reply' AND parent_id = $1`, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM posts WHERE id = $1 AND type IN ('topic','reply')`, id)
		return err
	}); err != nil {
		return err
	}
	if typ == "topic" {
		topicID = 0
	}
	if err := s.forumRecount(ctx, topicID, forumID); err != nil {
		return err
	}
	s.forumPurge(ctx)
	return nil
}

// forumAdminModeration handles the Moderation tab; false when it already answered.
func (s *Server) forumAdminModeration(c *gin.Context, d *forumAdminData, notices *[]notice) bool {
	ctx := c.Request.Context()
	u := currentUser(c)
	view := c.Query("view")
	if view == "" {
		view = c.PostForm("view")
	}
	ok := false
	for _, v := range forumAdminViews {
		ok = ok || v.Key == view
	}
	if !ok {
		view = "all"
	}
	search := strings.TrimSpace(c.Query("s"))
	if len([]rune(search)) > 200 {
		search = string([]rune(search)[:200])
	}
	back := func(op string, n int, failed int) {
		kv := []string{"view", view, "s", search, "done", op, "n", strconv.Itoa(n)}
		if failed > 0 {
			kv = append(kv, "failed", strconv.Itoa(failed))
		}
		c.Redirect(http.StatusFound, forumAdminURL("moderation", kv...))
	}
	allowed := func(op string) bool { _, ok := forumAdminDone[op]; return ok } // every moderation op has a notice
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		op := c.PostForm("action")
		if op == "" || op == "-1" {
			op = c.PostForm("action2")
		}
		if !allowed(op) {
			back("", 0, 0)
			return false
		}
		n, failed := 0, 0
		for _, raw := range c.PostFormArray("item[]") {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				continue
			}
			if err := s.forumAdminApply(ctx, id, op, u.ID); err != nil {
				if op == "delete" && errors.Is(err, errForumNotFound) {
					continue // a reply that went with its topic, deleted earlier in this batch
				}
				failed++
				continue
			}
			n++
		}
		back(op, n, failed)
		return false
	}
	if op := c.Query("do"); op != "" {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		id, _ := strconv.ParseInt(c.Query("id"), 10, 64)
		if !allowed(op) || id <= 0 {
			back("", 0, 0)
			return false
		}
		if err := s.forumAdminApply(ctx, id, op, u.ID); err != nil {
			back(op, 0, 1)
			return false
		}
		back(op, 1, 0)
		return false
	}
	if done, ok := forumAdminDone[c.Query("done")]; ok {
		n, _ := strconv.Atoi(c.Query("n"))
		msg := done[1]
		if n == 1 {
			msg = done[0]
		}
		*notices = append(*notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(fmt.Sprintf(s.__(c, msg), n))), Dismissible: true})
	}
	if f, _ := strconv.Atoi(c.Query("failed")); f > 0 {
		*notices = append(*notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(fmt.Sprintf(s.__(c, "%d item(s) could not be changed."), f))), Dismissible: true})
	}
	d.ModView, d.Search, d.Bulk = view, search, forumAdminBulk(view)

	// Counts of each filter.
	counts := map[string]int{}
	var all, pending, spam, trash int
	if err := s.store.Pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE `+forumAdminViewSQL("all")+`),
		count(*) FILTER (WHERE `+forumAdminViewSQL("pending")+`),
		count(*) FILTER (WHERE `+forumAdminViewSQL("spam")+`),
		count(*) FILTER (WHERE `+forumAdminViewSQL("trash")+`)
		FROM posts p WHERE p.type IN ('topic','reply')`).Scan(&all, &pending, &spam, &trash); err != nil {
		d.Error = "The topics and replies could not be read: " + err.Error()
	}
	counts["all"], counts["pending"], counts["spam"], counts["trash"] = all, pending, spam, trash
	for _, v := range forumAdminViews {
		if v.Key != "all" && counts[v.Key] == 0 && v.Key != view {
			continue // WordPress hides empty status links
		}
		d.Views = append(d.Views, forumAdminView{Key: v.Key, Label: v.Label, Count: counts[v.Key], Current: v.Key == view,
			URL: forumAdminURL("moderation", "view", map[bool]string{true: "", false: v.Key}[v.Key == "all"])})
	}

	// The page of items.
	args := []any{}
	cond := "p.type IN ('topic','reply') AND " + forumAdminViewSQL(view)
	if search != "" {
		args = append(args, search)
		cond += " AND p.search @@ websearch_to_tsquery('simple', $1)"
	}
	var total int
	if err := s.store.Pool.QueryRow(ctx, `SELECT count(*) FROM posts p WHERE `+cond, args...).Scan(&total); err != nil {
		d.Error = "The topics and replies could not be read: " + err.Error()
		return true
	}
	const per = 20
	page, _ := strconv.Atoi(c.Query("paged"))
	pages := max(1, (total+per-1)/per)
	page = max(1, min(page, pages))
	base := url.URL{Path: "/wp-admin/forum", RawQuery: url.Values{"tab": {"moderation"}}.Encode()}
	q := base.Query()
	if view != "all" {
		q.Set("view", view)
	}
	if search != "" {
		q.Set("s", search)
	}
	base.RawQuery = q.Encode()
	d.Paging = newPagination(total, per, page, base)
	rows, err := s.store.Pool.Query(ctx, `SELECT p.id, p.type, coalesce(p.author_id,0), coalesce(p.parent_id,0), p.title, p.content, p.status,
		coalesce(p.meta->>'`+forumMetaSpam+`','') = 'true', p.created_at, coalesce(p.meta->>'`+forumMetaIP+`',''),
		coalesce(t.id,0), coalesce(t.title,''), coalesce(t.slug,''), coalesce(f.title,'')
		FROM posts p
		LEFT JOIN posts t ON p.type = 'reply' AND t.id = p.parent_id
		LEFT JOIN posts f ON f.type = 'forum' AND f.id = CASE WHEN p.type = 'topic' THEN p.parent_id ELSE t.parent_id END
		WHERE `+cond+fmt.Sprintf(` ORDER BY p.created_at DESC, p.id DESC LIMIT %d OFFSET %d`, per, (page-1)*per), args...)
	if err != nil {
		d.Error = "The topics and replies could not be read: " + err.Error()
		return true
	}
	type rec struct {
		id, author, parent, topicID int64
		typ, title, content, status string
		spam                        bool
		at                          time.Time
		ip, topicTitle, topicSlug   string
		forum                       string
	}
	var recs []rec
	var authorIDs []int64
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.typ, &r.author, &r.parent, &r.title, &r.content, &r.status, &r.spam, &r.at, &r.ip,
			&r.topicID, &r.topicTitle, &r.topicSlug, &r.forum); err != nil {
			rows.Close()
			d.Error = "The topics and replies could not be read: " + err.Error()
			return true
		}
		recs = append(recs, r)
		authorIDs = append(authorIDs, r.author)
	}
	rows.Close()
	authors, _ := s.forumAuthors(ctx, authorIDs)
	loc := s.loc()
	dateFmt, timeFmt := defaultStr(s.settings.String("date_format"), "F j, Y"), defaultStr(s.settings.String("time_format"), "g:i a")
	act := func(id int64, op, label, confirm string) forumAdminAction {
		return forumAdminAction{Key: op, Label: label, Confirm: confirm,
			URL: forumAdminURL("moderation", "do", op, "id", strconv.FormatInt(id, 10), "view", map[bool]string{true: "", false: view}[view == "all"], "s", search, "_wpnonce", d.Nonce)}
	}
	for _, r := range recs {
		it := forumAdminItem{ID: r.id, IsTopic: r.typ == "topic", Status: r.status, Spam: r.spam, IP: r.ip, ForumTitle: r.forum,
			Author: authors[r.author], ISO: r.at.UTC().Format(time.RFC3339),
			Date: wp.FormatDate(r.at.In(loc), dateFmt) + " at " + wp.FormatDate(r.at.In(loc), timeFmt)}
		if it.IsTopic {
			it.Title = r.title
			it.TopicID = r.id
			it.ViewURL = s.forumTopicURL(ctx, &forumTopic{ID: r.id, Slug: forumSlugOf(r.title)}, 1)
		} else {
			it.Title = r.topicTitle
			it.TopicID = r.topicID
			it.ViewURL = s.forumReplyURL(ctx, &forumReply{ID: r.id, TopicID: r.topicID})
		}
		ex := strings.Join(strings.Fields(wp.StripTags(r.content)), " ")
		if rs := []rune(ex); len(rs) > 180 {
			ex = strings.TrimSpace(string(rs[:180])) + "…"
		}
		it.Excerpt = ex
		switch {
		case r.spam:
			it.StatusLabel, it.RowClass = "Spam", "spam"
			it.Actions = []forumAdminAction{act(r.id, "unspam", "Not Spam", ""), act(r.id, "delete", "Delete Permanently", "Delete this item permanently? This cannot be undone.")}
		case r.status == "trash":
			it.StatusLabel, it.RowClass = "Trash", "trash"
			it.Actions = []forumAdminAction{act(r.id, "restore", "Restore", ""), act(r.id, "delete", "Delete Permanently", "Delete this item permanently? This cannot be undone.")}
		case r.status == "pending":
			it.StatusLabel, it.RowClass = "Pending", "unapproved"
			it.Actions = []forumAdminAction{act(r.id, "approve", "Approve", ""), act(r.id, "spam", "Spam", ""), act(r.id, "trash", "Trash", "")}
		default:
			it.StatusLabel, it.RowClass = "Published", "approved"
			it.Actions = []forumAdminAction{act(r.id, "spam", "Spam", ""), act(r.id, "trash", "Trash", "")}
		}
		d.Items = append(d.Items, it)
	}
	return true
}

// Settings and Google tabs ------------------------------------------------------------------------

// forumAdminRedirectURI is the Google OAuth redirect URI: the site root (no language prefix) plus the
// fixed /forum-auth/google/callback route.
func (s *Server) forumAdminRedirectURI(ctx context.Context) string {
	return strings.TrimSuffix(s.links(ctx).HomeURL("/"), "/") + forumAuthPrefix + "/google/callback"
}

// forumAdminSettings handles the Settings and Google sign-in tabs; false when it already answered.
func (s *Server) forumAdminSettings(c *gin.Context, d *forumAdminData) bool {
	ctx := c.Request.Context()
	st := s.forumLoad()
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		atoi := func(k string) int {
			n, _ := strconv.Atoi(strings.TrimSpace(c.PostForm(k)))
			return n
		}
		if d.Tab == "google" {
			st.GoogleEnabled = c.PostForm("google_enabled") == "1"
			st.GoogleClientID = c.PostForm("google_client_id")
			st.GoogleClientSecret = c.PostForm("google_client_secret") // empty keeps the stored secret (forumSave)
			if c.PostForm("google_clear_secret") == "1" && strings.TrimSpace(st.GoogleClientSecret) == "" {
				st = forumSanitize(st)
				st.GoogleClientSecret = ""
				st.GoogleEnabled = false
				if err := s.settings.Set(ctx, map[string]any{forumOptions: st}); err != nil {
					c.String(http.StatusInternalServerError, err.Error())
					return false
				}
				s.purgeAll(ctx)
				c.Redirect(http.StatusFound, forumAdminURL("google", "msg", "google_saved"))
				return false
			}
			if err := s.forumSave(ctx, st); err != nil {
				c.String(http.StatusInternalServerError, err.Error())
				return false
			}
			c.Redirect(http.StatusFound, forumAdminURL("google", "msg", "google_saved"))
			return false
		}
		st.Base = c.PostForm("base")
		st.Title = c.PostForm("title")
		st.TopicsPerPage = atoi("topics_per_page")
		st.RepliesPerPage = atoi("replies_per_page")
		st.Registration = c.PostForm("registration") == "1"
		st.RequireVerify = c.PostForm("require_verify") == "1"
		st.DefaultRole = c.PostForm("default_role")
		st.ModerateFirstPost = c.PostForm("moderate_first_post") == "1"
		st.EditWindow = atoi("edit_window")
		st.FloodSeconds = atoi("flood_seconds")
		st.ShowMembers = c.PostForm("show_members") == "1"
		st.GoogleClientSecret = "" // keep the stored secret
		if err := s.forumSave(ctx, st); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return false
		}
		c.Redirect(http.StatusFound, forumAdminURL("settings", "settings-updated", "true"))
		return false
	}
	d.SecretSet = st.GoogleClientSecret != ""
	st.GoogleClientSecret = "" // never rendered back
	d.Settings = st
	d.Redirect = s.forumAdminRedirectURI(ctx)
	if u, err := url.Parse(d.Redirect); err == nil {
		d.Origin = u.Scheme + "://" + u.Host
		host := u.Hostname()
		d.NotHTTPS = u.Scheme != "https" && host != "localhost" && host != "127.0.0.1" && host != "::1" && !strings.HasSuffix(host, ".localhost")
	}
	d.Ready = s.forumGoogleReady()
	if st.GoogleEnabled && !d.Ready {
		switch {
		case st.GoogleClientID == "" && !d.SecretSet:
			d.Missing = "the Client ID and the Client secret"
		case st.GoogleClientID == "":
			d.Missing = "the Client ID"
		case !d.SecretSet:
			d.Missing = "the Client secret"
		}
	}
	return true
}

// Overview tab -------------------------------------------------------------------------------------

// forumAdminCard is a total on the Overview tab.
type forumAdminCard struct {
	Key, Label string
	Value      string // formatted
	Sub        string // "+12 in the last 30 days"
	URL        string // "" = no link
	Icon       string // a forum_icon name
}

// forumAdminBar is one day of the Overview chart: heights are percent of the busiest day.
type forumAdminBar struct {
	Label, Title                      string
	Members, Topics, Replies          int
	MembersPct, TopicsPct, RepliesPct int
	Tick, Today                       bool
}

// forumAdminTopRow is a top contributor.
type forumAdminTopRow struct {
	*forumMember
	Author *forumAuthor // for forum_avatar (the embedded author is not reachable from a template)
	Rank   int
}

// forumAdminActivity is a recent topic or reply.
type forumAdminActivity struct {
	IsTopic             bool
	Title, URL, Excerpt string
	Author              *forumAuthor
	Ago, ISO            string
}

// forumAdminPct is v as percent of top for a bar height: at least 2 when v is not zero, so a small
// day stays visible next to a busy one.
func forumAdminPct(v, top int) int {
	if v <= 0 || top <= 0 {
		return 0
	}
	return max(2, min(100, v*100/top))
}

// forumAdminOverview fills the Overview tab (read-only).
func (s *Server) forumAdminOverview(c *gin.Context, d *forumAdminData) bool {
	ctx := c.Request.Context()
	o, err := s.forumOverview(ctx)
	if err != nil {
		d.Error = "The forum statistics could not be read: " + err.Error()
		return true
	}
	d.Overview = o
	in30 := func(n int) string {
		if n == 0 {
			return s.__(c, "None in the last 30 days")
		}
		return fmt.Sprintf(s.__(c, "+%s in the last 30 days"), formatNumber(n))
	}
	forumsURL := ""
	if d.IsAdmin {
		forumsURL = forumAdminURL(forumTabForums)
	}
	cats := fmt.Sprintf(s.__(c, "%s categories"), formatNumber(o.Categories))
	if o.Categories == 1 {
		cats = s.__(c, "1 category")
	}
	d.Cards = []forumAdminCard{
		{Key: "forums", Label: "Forums", Value: formatNumber(o.Forums), Sub: cats, URL: forumsURL, Icon: "compass"},
		{Key: "topics", Label: "Topics", Value: formatNumber(o.Topics), Sub: in30(o.NewTopics), URL: forumAdminURL(forumTabModeration), Icon: "chat"},
		{Key: "replies", Label: "Replies", Value: formatNumber(o.Replies), Sub: in30(o.NewReplies), URL: forumAdminURL(forumTabModeration), Icon: "feather"},
		{Key: "members", Label: "Members", Value: formatNumber(o.Members), Sub: in30(o.NewMembers), URL: forumAdminURL(forumTabMembers), Icon: "users"},
	}
	for i := range d.Cards {
		if !forumIconValid(d.Cards[i].Icon) {
			d.Cards[i].Icon = "star"
		}
	}
	top := max(1, o.DayMax)
	d.ChartMax, d.ChartMid = top, 0
	if top >= 2 {
		d.ChartMid = top / 2
	}
	tip := s.__(c, "%s: %d new members, %d topics, %d replies")
	for i, day := range o.Days {
		d.Chart = append(d.Chart, forumAdminBar{
			Label:   wp.FormatDate(day.Date, "M j"),
			Title:   fmt.Sprintf(tip, wp.FormatDate(day.Date, "F j"), day.Members, day.Topics, day.Replies),
			Members: day.Members, Topics: day.Topics, Replies: day.Replies,
			MembersPct: forumAdminPct(day.Members, top), TopicsPct: forumAdminPct(day.Topics, top), RepliesPct: forumAdminPct(day.Replies, top),
			Tick: (len(o.Days)-1-i)%7 == 0, Today: i == len(o.Days)-1,
		})
	}
	d.Quiet = o.NewMembers+o.NewTopics+o.NewReplies == 0
	d.ChartSum = fmt.Sprintf(s.__(c, "In the last 30 days: %d new members, %d topics and %d replies."), o.NewMembers, o.NewTopics, o.NewReplies)
	for i, m := range o.Top {
		d.Top = append(d.Top, forumAdminTopRow{forumMember: m, Author: m.forumAuthor, Rank: i + 1})
	}
	now := time.Now()
	ago := s.__(c, "%s ago")
	for _, h := range o.Recent {
		if len(d.Activity) >= 10 {
			break
		}
		if h == nil || h.Topic == nil {
			continue
		}
		a := forumAdminActivity{IsTopic: h.Reply == nil, Title: h.Topic.Title, URL: h.URL, Author: h.Author,
			Excerpt: forumTruncRunes(h.Excerpt, 140), ISO: h.At.UTC().Format(time.RFC3339), Ago: fmt.Sprintf(ago, wp.HumanTimeDiffN(h.At, now, s.catalog(c).N))}
		if utf8.RuneCountInString(h.Excerpt) > 140 {
			a.Excerpt = strings.TrimSpace(a.Excerpt) + "…"
		}
		d.Activity = append(d.Activity, a)
	}
	return true
}

// Members tab --------------------------------------------------------------------------------------

// forumAdminMemberFilters are the Members filters (subsubsub), forumMemberQuery.Filter keys.
var forumAdminMemberFilters = []struct{ Key, Label string }{
	{"all", "All"}, {"unverified", "Unverified"}, {"blocked", "Blocked"}, {"moderators", "Moderators"}, {"google", "Google"},
}

// forumAdminMemberSortCols are the sortable columns (orderby = forumMemberQuery.Sort key) and whether
// the first click sorts descending.
var forumAdminMemberSortCols = map[string]bool{
	"name": false, "topics": true, "replies": true, "points": true, "joined": true, "last_active": true,
}

// forumAdminMemberDone are the notices of ?done=<op>&n=<count> after a member action.
var forumAdminMemberDone = map[string][2]string{
	"verify":           {"%d member marked as verified.", "%d members marked as verified."},
	"block":            {"%d member blocked.", "%d members blocked."},
	"unblock":          {"%d member unblocked.", "%d members unblocked."},
	"award":            {"Badge awarded to %d member.", "Badges awarded to %d members."},
	"revoke":           {"Badge taken back from %d member.", "Badges taken back from %d members."},
	"role_participant": {"%d member is now a Forum Participant.", "%d members are now Forum Participants."},
	"role_moderator":   {"%d member is now a Forum Moderator.", "%d members are now Forum Moderators."},
}

// forumAdminMemberAdminOp reports whether a member action needs manage_options (roles and badges);
// verify, block and unblock are for moderators too.
func forumAdminMemberAdminOp(op string) bool {
	switch op {
	case "award", "revoke", "role_participant", "role_moderator":
		return true
	}
	return false
}

// forumAdminMemberRow is a row of the Members list.
type forumAdminMemberRow struct {
	*forumMember
	Author              *forumAuthor // for forum_avatar and forum_level
	Shown               []forumBadge // the first badges of the Badges column
	More                int          // badges beyond Shown
	Status, StatusLabel string       // blocked | unverified | verified
	RoleNames           string       // the member's roles, comma separated
	IsAdmin             bool         // the member is an administrator
	Self                bool
	Joined, JoinedISO   string
	Active, ActiveISO   string // "3 days ago", "" = never posted
	ManageURL           string
	Actions             []forumAdminAction
}

// forumAdminMemberBadge is a badge on the member screen.
type forumAdminMemberBadge struct {
	forumBadge
	Chip      template.HTML
	RuleLabel string
	Earned    bool // earned by its rule (or awarded by hand)
	Manual    bool // awarded by hand
}

// forumAdminMemberView is the member screen (?tab=members&action=member&user=N).
type forumAdminMemberView struct {
	forumAdminMemberRow
	ToNext   string // "40 points to Regular"
	Role     string // the forum role: forum_participant | forum_moderator | ""
	Badges   []forumAdminMemberBadge
	CanBlock bool
	CanRole  bool // an administrator, not their own account
	CanEdit  bool // edit_users: the Edit user button
	URL      string
	ListURL  string
}

// forumAdminMemberState is the list state of the request (filter, search, sort, page) as key/value
// pairs for forumAdminURL: row actions and bulk actions return to the same view. Unknown values go.
func forumAdminMemberState(get func(string) string) []string {
	filter := get("filter")
	known := false
	for _, f := range forumAdminMemberFilters {
		known = known || (f.Key == filter && filter != "all")
	}
	if !known {
		filter = ""
	}
	orderby, order := get("orderby"), get("order")
	if _, ok := forumAdminMemberSortCols[orderby]; !ok {
		orderby, order = "", ""
	}
	if order != "asc" && order != "desc" {
		order = ""
	}
	paged := ""
	if n, err := strconv.Atoi(get("paged")); err == nil && n > 1 {
		paged = strconv.Itoa(min(n, 100000))
	}
	return []string{"filter", filter, "s", forumTruncRunes(strings.TrimSpace(get("s")), 100), "orderby", orderby, "order", order, "paged", paged}
}

// forumAdminStateGet reads a key of forumAdminMemberState's pairs.
func forumAdminStateGet(kv []string, key string) string {
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			return kv[i+1]
		}
	}
	return ""
}

// forumAdminStateWith is kv with key set to v (v "" drops it).
func forumAdminStateWith(kv []string, key, v string) []string {
	out := make([]string, 0, len(kv)+2)
	found := false
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i] == key {
			out = append(out, key, v)
			found = true
			continue
		}
		out = append(out, kv[i], kv[i+1])
	}
	if !found {
		out = append(out, key, v)
	}
	return out
}

// forumAdminMemberSortLinks are the sortable column header links (WP_List_Table::print_column_headers()).
func forumAdminMemberSortLinks(state []string) map[string]sortLink {
	cur, order := forumAdminStateGet(state, "orderby"), forumAdminStateGet(state, "order")
	if cur == "" {
		cur, order = "joined", "desc" // the default sort: newest members first
	}
	if order == "" {
		order = map[bool]string{true: "desc", false: "asc"}[forumAdminMemberSortCols[cur]]
	}
	out := map[string]sortLink{}
	for col, descFirst := range forumAdminMemberSortCols {
		isCur := col == cur
		next := "asc"
		if (isCur && order == "asc") || (!isCur && descFirst) {
			next = "desc"
		}
		kv := forumAdminStateWith(forumAdminStateWith(forumAdminStateWith(state, "paged", ""), "orderby", col), "order", next)
		sl := sortLink{URL: forumAdminURL(forumTabMembers, kv...)}
		switch {
		case isCur && order == "asc":
			sl.Class, sl.AriaSort, sl.SRText = "sorted asc", "ascending", "Sort descending."
		case isCur:
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

// forumAdminMemberReason is the ?error= code of a failed member action.
func forumAdminMemberReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errForumNotFound) {
		return "member_not_found"
	}
	switch err.Error() {
	case "You cannot block yourself.":
		return "self_block"
	case "Administrators cannot be blocked.":
		return "admin_block"
	case "Only an administrator can block a moderator.":
		return "mod_block"
	case "You cannot change your own forum role.":
		return "self_role"
	case "Sorry, you are not allowed to block members.", "Sorry, you are not allowed to change forum roles.":
		return "denied"
	}
	return "member_failed"
}

// forumAdminMemberApply runs a member action on the users: the count done, the count failed and the
// error code of the first failure.
func (s *Server) forumAdminMemberApply(ctx context.Context, actor *store.User, op, badge string, ids []int64) (done, failed int, reason string) {
	fail := func(err error) {
		failed++
		if reason == "" {
			reason = forumAdminMemberReason(err)
		}
	}
	switch op {
	case "verify":
		if err := s.forumVerifyMembers(ctx, ids...); err != nil {
			return 0, len(ids), "member_failed"
		}
		return len(ids), 0, ""
	case "award", "revoke":
		var err error
		if op == "award" {
			err = s.forumAward(ctx, badge, ids...)
		} else {
			err = s.forumRevoke(ctx, badge, ids...)
		}
		if err != nil {
			if errors.Is(err, errForumNotFound) {
				return 0, len(ids), "badge_not_found"
			}
			return 0, len(ids), "member_failed"
		}
		return len(ids), 0, ""
	}
	for _, id := range ids {
		var err error
		switch op {
		case "block", "unblock":
			err = s.forumSetBlocked(ctx, actor, id, op == "block")
		case "role_participant":
			err = s.forumSetForumRole(ctx, actor, id, forumRoleParticipant)
		case "role_moderator":
			err = s.forumSetForumRole(ctx, actor, id, forumRoleModerator)
		default:
			err = errors.New("unknown action")
		}
		if err != nil {
			fail(err)
			continue
		}
		done++
	}
	return done, failed, reason
}

// forumAdminIDs parses user ids of a form (positive, unique, at most 500).
func forumAdminIDs(raw []string) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, r := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(r), 10, 64)
		if err != nil || id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= 500 {
			break
		}
	}
	return out
}

// forumAdminMembers handles the Members tab; false when it already answered.
func (s *Server) forumAdminMembers(c *gin.Context, d *forumAdminData, notices *[]notice) bool {
	ctx := c.Request.Context()
	u := currentUser(c)
	state := forumAdminMemberState(c.Query)
	back := func(kv ...string) {
		c.Redirect(http.StatusFound, forumAdminURL(forumTabMembers, append(append([]string{}, state...), kv...)...))
	}
	result := func(op string, n, failed int, reason string) {
		kv := []string{"done", op, "n", strconv.Itoa(n)}
		if failed > 0 {
			kv = append(kv, "failed", strconv.Itoa(failed), "error", reason)
		}
		back(kv...)
	}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		if c.PostForm("member_do") != "" {
			s.forumAdminMemberPost(c, d)
			return false
		}
		op, badge := c.PostForm("action"), c.PostForm("badge")
		if op == "" || op == "-1" {
			op, badge = c.PostForm("action2"), c.PostForm("badge2")
		}
		if _, ok := forumAdminMemberDone[op]; !ok {
			back()
			return false
		}
		if forumAdminMemberAdminOp(op) && !d.IsAdmin {
			c.String(http.StatusForbidden, "Sorry, you are not allowed to do that.")
			return false
		}
		ids := forumAdminIDs(c.PostFormArray("users[]"))
		if len(ids) == 0 {
			back("error", "no_members")
			return false
		}
		if op == "award" || op == "revoke" {
			if _, ok := s.forumBadgeByID(ctx, badge); !ok {
				back("error", "no_badge")
				return false
			}
		}
		n, failed, reason := s.forumAdminMemberApply(ctx, u, op, badge, ids)
		result(op, n, failed, reason)
		return false
	}
	if op := c.Query("do"); op != "" {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		id, _ := strconv.ParseInt(c.Query("user"), 10, 64)
		if (op != "verify" && op != "block" && op != "unblock") || id <= 0 {
			back()
			return false
		}
		n, failed, reason := s.forumAdminMemberApply(ctx, u, op, "", []int64{id})
		result(op, n, failed, reason)
		return false
	}
	if c.Query("action") == "member" {
		return s.forumAdminMemberScreen(c, d)
	}

	if done, ok := forumAdminMemberDone[c.Query("done")]; ok {
		if n, _ := strconv.Atoi(c.Query("n")); n > 0 {
			msg := done[1]
			if n == 1 {
				msg = done[0]
			}
			*notices = append(*notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(fmt.Sprintf(s.__(c, msg), n))), Dismissible: true})
		}
	}
	if f, _ := strconv.Atoi(c.Query("failed")); f > 0 {
		msg := s.__(c, "%d members could not be changed.")
		if f == 1 {
			msg = s.__(c, "%d member could not be changed.")
		}
		*notices = append(*notices, notice{Type: "warning", Message: template.HTML(template.HTMLEscapeString(fmt.Sprintf(msg, f))), Dismissible: true})
	}

	filter := defaultStr(forumAdminStateGet(state, "filter"), "all")
	q := forumMemberQuery{Search: forumAdminStateGet(state, "s"), Filter: filter, Sort: forumAdminStateGet(state, "orderby"),
		Order: forumAdminStateGet(state, "order"), PerPage: 20, Admin: true, Identity: currentUser(c).Can("list_users", nil)}
	if q.Sort == "" {
		q.Sort = "newest"
	}
	q.Page, _ = strconv.Atoi(forumAdminStateGet(state, "paged"))
	q = forumNormalizeMemberQuery(q)
	d.MemberQuery, d.Search = q, q.Search
	d.ListURL = forumAdminURL(forumTabMembers, state...)
	d.SortLinks = forumAdminMemberSortLinks(state)

	// The filters with their counts (one count each; WordPress hides empty views but All).
	for _, f := range forumAdminMemberFilters {
		_, n, err := s.forumMembers(ctx, forumMemberQuery{Filter: f.Key, PerPage: 1, Admin: true})
		if err != nil {
			d.Error = "The members could not be read: " + err.Error()
			return true
		}
		if f.Key != "all" && n == 0 && f.Key != filter {
			continue
		}
		kv := []string{}
		if f.Key != "all" {
			kv = append(kv, "filter", f.Key)
		}
		d.Views = append(d.Views, forumAdminView{Key: f.Key, Label: f.Label, Count: n, Current: f.Key == filter, URL: forumAdminURL(forumTabMembers, kv...)})
	}

	list, total, err := s.forumMembers(ctx, q)
	if err != nil {
		d.Error = "The members could not be read: " + err.Error()
		return true
	}
	pages := max(1, (total+q.PerPage-1)/q.PerPage)
	if q.Page > pages && total > 0 { // past the last page (a stale link): the last page, not a redirect loop
		c.Redirect(http.StatusFound, forumAdminURL(forumTabMembers, forumAdminStateWith(state, "paged", map[bool]string{true: "", false: strconv.Itoa(pages)}[pages == 1])...))
		return false
	}
	base, _ := url.Parse(forumAdminURL(forumTabMembers, forumAdminStateWith(state, "paged", "")...))
	d.Paging = newPagination(total, q.PerPage, q.Page, *base)
	names := s.forumAdminRoleNames(ctx)
	for _, m := range list {
		d.MemberRows = append(d.MemberRows, s.forumAdminMemberRowOf(c, d, m, names, state))
	}
	d.Bulk = []forumAdminOpt{{"verify", "Mark as verified"}, {"block", "Block"}, {"unblock", "Unblock"}}
	if d.IsAdmin {
		d.Bulk = append(d.Bulk, forumAdminOpt{"award", "Award badge"}, forumAdminOpt{"revoke", "Take back badge"},
			forumAdminOpt{"role_participant", "Set role: Forum Participant"}, forumAdminOpt{"role_moderator", "Set role: Forum Moderator"})
		d.BadgeOpts = s.forumBadges(ctx)
	}
	return true
}

// forumAdminRoleNames maps role slugs to their names.
func (s *Server) forumAdminRoleNames(ctx context.Context) map[string]string {
	names := map[string]string{}
	if roles, err := s.store.Roles(ctx); err == nil {
		for _, r := range roles {
			names[r.Slug] = r.Name
		}
	}
	return names
}

// forumAdminMemberRowOf builds a Members row with its row actions.
func (s *Server) forumAdminMemberRowOf(c *gin.Context, d *forumAdminData, m *forumMember, names map[string]string, state []string) forumAdminMemberRow {
	if !currentUser(c).Can("list_users", nil) {
		// A Forum Moderator sees public names, not every account's email address or login name
		// (WordPress' Users screen needs list_users).
		m.Email = ""
		a := *m.forumAuthor
		a.Login = a.Nicename
		m.forumAuthor = &a
	}
	r := forumAdminMemberRow{forumMember: m, Author: m.forumAuthor, Self: m.ID == d.SelfID}
	r.Shown = m.Badges
	if len(r.Shown) > 4 {
		r.Shown, r.More = m.Badges[:4], len(m.Badges)-4
	}
	var rn []string
	for _, role := range m.Roles {
		if role == "administrator" {
			r.IsAdmin = true
		}
		rn = append(rn, defaultStr(names[role], role))
	}
	r.RoleNames = strings.Join(rn, ", ")
	switch {
	case m.Blocked:
		r.Status, r.StatusLabel = "blocked", "Blocked"
	case !m.Verified:
		r.Status, r.StatusLabel = "unverified", "Unverified"
	default:
		r.Status, r.StatusLabel = "verified", "Verified"
	}
	loc := s.loc()
	dateFmt := defaultStr(s.settings.String("date_format"), "F j, Y")
	if !m.Joined.IsZero() {
		r.Joined, r.JoinedISO = wp.FormatDate(m.Joined.In(loc), dateFmt), m.Joined.UTC().Format(time.RFC3339)
	}
	if !m.LastActive.IsZero() {
		r.Active, r.ActiveISO = fmt.Sprintf(s.__(c, "%s ago"), forumTimeDiff(s.catalog(c), m.LastActive)), m.LastActive.UTC().Format(time.RFC3339)
	}
	id := strconv.FormatInt(m.ID, 10)
	r.ManageURL = forumAdminURL(forumTabMembers, "action", "member", "user", id)
	act := func(op, label string) forumAdminAction {
		return forumAdminAction{Key: op, Label: label, URL: forumAdminURL(forumTabMembers, append(append([]string{}, state...), "do", op, "user", id, "_wpnonce", d.Nonce)...)}
	}
	r.Actions = append(r.Actions, forumAdminAction{Key: "manage", Label: "Manage", URL: r.ManageURL})
	if m.ProfileURL != "" {
		r.Actions = append(r.Actions, forumAdminAction{Key: "view", Label: "View profile", URL: m.ProfileURL})
	}
	if currentUser(c).Can("edit_users", nil) && m.EditURL != "" {
		r.Actions = append(r.Actions, forumAdminAction{Key: "edit", Label: "Edit user", URL: m.EditURL})
	}
	if !m.Verified && !m.Blocked {
		r.Actions = append(r.Actions, act("verify", "Mark as verified"))
	}
	if !r.Self && !r.IsAdmin && (!m.Moderator || d.IsAdmin) {
		if m.Blocked {
			r.Actions = append(r.Actions, act("unblock", "Unblock"))
		} else {
			a := act("block", "Block")
			a.Confirm = "Block this member? They can still read and sign in, but cannot post."
			r.Actions = append(r.Actions, a)
		}
	}
	return r
}

// forumAdminRuleLabel is the admin label of a badge rule.
func forumAdminRuleLabel(rule string) string {
	for _, r := range forumBadgeRules {
		if r.Key == rule {
			return r.Label
		}
	}
	return rule
}

// forumAdminMemberScreen renders one member: statistics, level, status, forum role and badges.
func (s *Server) forumAdminMemberScreen(c *gin.Context, d *forumAdminData) bool {
	ctx := c.Request.Context()
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.Query("user"), 10, 64)
	authors, users, err := s.forumLoadAuthors(ctx, []int64{id})
	a, target := authors[id], users[id]
	if err != nil || a == nil || target == nil {
		c.Redirect(http.StatusFound, forumAdminURL(forumTabMembers, "error", "member_not_found"))
		return false
	}
	m := &forumMember{forumAuthor: a, Email: target.Email, Roles: target.Roles, EditURL: "/wp-admin/user-edit?user_id=" + itoa64(id)}
	v := &forumAdminMemberView{forumAdminMemberRow: s.forumAdminMemberRowOf(c, d, m, s.forumAdminRoleNames(ctx), nil),
		CanBlock: forumCanBlock(u, target), CanRole: d.IsAdmin && u.ID != id, CanEdit: u.Can("edit_users", nil),
		URL: forumAdminURL(forumTabMembers, "action", "member", "user", itoa64(id)), ListURL: forumAdminURL(forumTabMembers)}
	for _, r := range target.Roles {
		if r == forumRoleModerator || (r == forumRoleParticipant && v.Role == "") {
			v.Role = r
		}
	}
	if a.NextLevel != nil {
		need := max(0, a.NextLevel.Min-a.Points)
		v.ToNext = fmt.Sprintf(s.__(c, "%s points to %s"), formatNumber(need), s.__(c, a.NextLevel.Name))
		if need == 1 {
			v.ToNext = fmt.Sprintf(s.__(c, "1 point to %s"), s.__(c, a.NextLevel.Name))
		}
	}
	stats, _ := s.forumMemberStats(ctx, []int64{id})
	manual := map[string]bool{}
	if st := stats[id]; st != nil {
		for _, b := range st.Manual {
			manual[b] = true
		}
	}
	earned := map[string]bool{}
	for _, b := range a.Badges {
		earned[b.ID] = true
	}
	for _, b := range s.forumBadges(ctx) {
		v.Badges = append(v.Badges, forumAdminMemberBadge{forumBadge: b, Chip: forumBadgeHTML(b), RuleLabel: forumAdminRuleLabel(b.Rule),
			Earned: earned[b.ID], Manual: manual[b.ID]})
	}
	d.Member = v
	return true
}

// forumAdminMemberPost runs a form of the member screen and redirects back to it.
func (s *Server) forumAdminMemberPost(c *gin.Context, d *forumAdminData) {
	ctx := c.Request.Context()
	u := currentUser(c)
	id, _ := strconv.ParseInt(c.PostForm("user"), 10, 64)
	target, err := s.store.UserByID(ctx, id)
	if err != nil || id <= 0 {
		c.Redirect(http.StatusFound, forumAdminURL(forumTabMembers, "error", "member_not_found"))
		return
	}
	back := func(kv ...string) {
		c.Redirect(http.StatusFound, forumAdminURL(forumTabMembers, append([]string{"action", "member", "user", itoa64(target.ID)}, kv...)...))
	}
	do := c.PostForm("member_do")
	if (do == "role" || do == "badges") && !d.IsAdmin {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to do that.")
		return
	}
	msg := ""
	switch do {
	case "block", "unblock":
		err = s.forumSetBlocked(ctx, u, target.ID, do == "block")
		msg = map[bool]string{true: "member_blocked", false: "member_unblocked"}[do == "block"]
	case "verify":
		err = s.forumVerifyMembers(ctx, target.ID)
		msg = "member_verified"
	case "role":
		err = s.forumSetForumRole(ctx, u, target.ID, c.PostForm("forum_role"))
		msg = "member_role"
	case "badges":
		want := map[string]bool{}
		for _, b := range c.PostFormArray("badges[]") {
			want[b] = true
		}
		stats, serr := s.forumMemberStats(ctx, []int64{target.ID})
		if serr != nil {
			back("error", "member_failed")
			return
		}
		have := map[string]bool{}
		if st := stats[target.ID]; st != nil {
			for _, b := range st.Manual {
				have[b] = true
			}
		}
		for _, b := range s.forumBadges(ctx) {
			switch {
			case want[b.ID] && !have[b.ID]:
				err = errors.Join(err, s.forumAward(ctx, b.ID, target.ID))
			case !want[b.ID] && have[b.ID]:
				err = errors.Join(err, s.forumRevoke(ctx, b.ID, target.ID))
			}
		}
		msg = "member_badges"
	default:
		back()
		return
	}
	if err != nil {
		back("error", forumAdminMemberReason(err))
		return
	}
	back("msg", msg)
}

// Badges & Levels tab ------------------------------------------------------------------------------

// forumAdminRule is a badge rule in the rule <select>.
type forumAdminRule struct {
	Key, Label string
	Threshold  bool
}

// forumAdminLevelRow is a row of the levels table (Index names its fields: level_<i>_name, …).
type forumAdminLevelRow struct {
	forumLevel
	Index int
	New   bool // the empty row that adds a level
}

// forumAdminBadgeRow is a row of the badge list.
type forumAdminBadgeRow struct {
	forumBadge
	Chip      template.HTML
	RuleLabel string
	Awarded   int // manual awards
	EditURL   string
	Actions   []forumAdminAction
}

// forumAdminBadgeForm is the add / edit badge form.
type forumAdminBadgeForm struct {
	forumBadge
	IsNew bool
}

// forumAdminLevelsOf reads the levels form; code is the ?error= code of a form that cannot be saved
// as typed ("" = fine): no level left, a named level without minimum points, or two levels with the
// same minimum (forumSanitizeLevels would drop one of them without a word).
func forumAdminLevelsOf(c *gin.Context) (forumLevels, string) {
	atoi := func(k string) int {
		n, _ := strconv.Atoi(strings.TrimSpace(c.PostForm(k)))
		return n
	}
	l := forumLevels{TopicPoints: atoi("topic_points"), ReplyPoints: atoi("reply_points")}
	n := max(0, min(atoi("level_count"), forumMaxLevels+20))
	code := ""
	seen := map[int]bool{}
	for i := 0; i < n; i++ {
		k := "level_" + strconv.Itoa(i) + "_"
		name := strings.TrimSpace(c.PostForm(k + "name"))
		if c.PostForm(k+"remove") == "1" || name == "" {
			continue
		}
		raw := strings.TrimSpace(c.PostForm(k + "min"))
		if _, err := strconv.Atoi(raw); err != nil && code == "" {
			code = "levels_min"
		}
		img, _ := strconv.ParseInt(c.PostForm(k+"image_id"), 10, 64)
		lv := forumLevel{ID: c.PostForm(k + "id"), Name: name, Min: max(0, min(atoi(k+"min"), 10_000_000)), Icon: c.PostForm(k + "icon"),
			ImageID: img, Color: c.PostForm(k + "color")}
		if seen[lv.Min] && code == "" {
			code = "levels_same_min"
		}
		seen[lv.Min] = true
		l.Levels = append(l.Levels, lv)
	}
	if len(l.Levels) == 0 {
		code = "levels_empty"
	}
	return l, code
}

// forumAdminBadgeOf reads the badge form.
func forumAdminBadgeOf(c *gin.Context) forumBadge {
	th, _ := strconv.Atoi(strings.TrimSpace(c.PostForm("threshold")))
	img, _ := strconv.ParseInt(c.PostForm("image_id"), 10, 64)
	return forumBadge{ID: strings.TrimSpace(c.PostForm("badge_id")), Name: c.PostForm("name"), Description: c.PostForm("description"),
		Icon: c.PostForm("icon"), ImageID: img, Color: c.PostForm("color"), Rule: c.PostForm("rule"), Threshold: th, Enabled: c.PostForm("enabled") == "1"}
}

// forumAdminRestoreBadges puts the default badges back as shipped (deleted ones return, edited ones
// are reset) and keeps the site's own badges after them.
func (s *Server) forumAdminRestoreBadges(ctx context.Context) error {
	out := forumDefaultBadges()
	def := map[string]bool{}
	for _, b := range out {
		def[b.ID] = true
	}
	for _, b := range s.forumBadges(ctx) {
		if !def[b.ID] && len(out) < forumMaxBadges {
			out = append(out, b)
		}
	}
	_, err := s.forumSaveBadges(ctx, out)
	return err
}

// forumAdminBadgeCounts counts the manual awards of each badge (one query).
func (s *Server) forumAdminBadgeCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	rows, err := s.store.Pool.Query(ctx, `SELECT b, count(*) FROM users, jsonb_array_elements_text(meta->'`+forumUMetaBadges+`') b
		WHERE jsonb_typeof(meta->'`+forumUMetaBadges+`') = 'array' GROUP BY b`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out
}

// forumAdminBadges handles the Badges & Levels tab; false when it already answered.
func (s *Server) forumAdminBadges(c *gin.Context, d *forumAdminData) bool {
	ctx := c.Request.Context()
	back := func(kv ...string) { c.Redirect(http.StatusFound, forumAdminURL(forumTabBadges, kv...)) }
	d.BadgeForm = forumAdminBadgeForm{forumBadge: forumBadge{Color: forumDefaultColor, Rule: forumRuleManual, Icon: "star", Enabled: true}, IsNew: true}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		switch c.PostForm("do") {
		case "save_levels":
			l, code := forumAdminLevelsOf(c)
			if code != "" {
				back("error", code)
				return false
			}
			if _, err := s.forumSaveLevels(ctx, l); err != nil {
				back("error", "levels_failed")
				return false
			}
			back("msg", "levels_saved")
		case "restore_levels":
			if _, err := s.forumSaveLevels(ctx, forumDefaultLevels()); err != nil {
				back("error", "levels_failed")
				return false
			}
			back("msg", "levels_restored")
		case "restore_badges":
			if err := s.forumAdminRestoreBadges(ctx); err != nil {
				back("error", "badge_failed")
				return false
			}
			back("msg", "badges_restored")
		case "save_badge":
			b := forumAdminBadgeOf(c)
			if b.ID != "" {
				if _, ok := s.forumBadgeByID(ctx, b.ID); !ok {
					back("error", "badge_not_found")
					return false
				}
			}
			saved, err := s.forumSaveBadge(ctx, b)
			if err == nil {
				if b.ID != "" {
					back("action", "edit", "badge", saved.ID, "msg", "badge_updated")
				} else {
					back("msg", "badge_added")
				}
				return false
			}
			d.Error = err.Error()
			if b.ImageID > 0 {
				b.ImageURL = s.forumImageURLs(ctx, []int64{b.ImageID}, 32)[b.ImageID]
			}
			if b.ID != "" {
				d.BadgeEdit = &forumAdminBadgeForm{forumBadge: b}
			} else {
				d.BadgeForm = forumAdminBadgeForm{forumBadge: b, IsNew: true}
			}
		default:
			back()
		}
		if d.Error == "" {
			return false
		}
	} else if op := c.Query("do"); op != "" {
		if !s.checkNonce(c, forumAdminNonce) {
			s.denyNonce(c)
			return false
		}
		id := c.Query("badge")
		if op == "delete" {
			if err := s.forumDeleteBadge(ctx, id); err != nil {
				back("error", map[bool]string{true: "badge_not_found", false: "badge_failed"}[errors.Is(err, errForumNotFound)])
				return false
			}
			back("msg", "badge_deleted")
			return false
		}
		list := s.forumBadges(ctx)
		at := -1
		for i, b := range list {
			if b.ID == id {
				at = i
			}
		}
		if at < 0 {
			back("error", "badge_not_found")
			return false
		}
		msg := "badge_moved"
		switch op {
		case "up":
			if at > 0 {
				list[at-1], list[at] = list[at], list[at-1]
			}
		case "down":
			if at < len(list)-1 {
				list[at+1], list[at] = list[at], list[at+1]
			}
		case "enable", "disable":
			list[at].Enabled = op == "enable"
			msg = "badge_" + op + "d"
		default:
			back()
			return false
		}
		if _, err := s.forumSaveBadges(ctx, list); err != nil {
			back("error", "badge_failed")
			return false
		}
		back("msg", msg)
		return false
	}

	if u := currentUser(c); u.Can("upload_files", nil) {
		s.enqueueMedia(c, 0)
		d.CanUpload = true
	}
	for _, r := range forumBadgeRules {
		d.Rules = append(d.Rules, forumAdminRule{Key: r.Key, Label: r.Label, Threshold: r.Threshold})
	}
	d.Icons = forumIconNames()
	if c.Query("action") == "edit" && d.BadgeEdit == nil {
		b, ok := s.forumBadgeByID(ctx, c.Query("badge"))
		if !ok {
			back("error", "badge_not_found")
			return false
		}
		d.BadgeEdit = &forumAdminBadgeForm{forumBadge: b}
	}
	if d.BadgeEdit != nil {
		return true
	}
	d.Levels = s.forumLevelsLoad(ctx)
	for i, l := range d.Levels.Levels {
		d.LevelRows = append(d.LevelRows, forumAdminLevelRow{forumLevel: l, Index: i})
	}
	d.LevelRows = append(d.LevelRows, forumAdminLevelRow{forumLevel: forumLevel{Color: forumDefaultColor}, Index: len(d.LevelRows), New: true})
	counts := s.forumAdminBadgeCounts(ctx)
	list := s.forumBadges(ctx)
	for i, b := range list {
		row := forumAdminBadgeRow{forumBadge: b, Chip: forumBadgeHTML(b), RuleLabel: forumAdminRuleLabel(b.Rule), Awarded: counts[b.ID],
			EditURL: forumAdminURL(forumTabBadges, "action", "edit", "badge", b.ID)}
		act := func(op, label string) forumAdminAction {
			return forumAdminAction{Key: op, Label: label, URL: forumAdminURL(forumTabBadges, "do", op, "badge", b.ID, "_wpnonce", d.Nonce)}
		}
		row.Actions = append(row.Actions, forumAdminAction{Key: "edit", Label: "Edit", URL: row.EditURL})
		if b.Enabled {
			row.Actions = append(row.Actions, act("disable", "Disable"))
		} else {
			row.Actions = append(row.Actions, act("enable", "Enable"))
		}
		if i > 0 {
			row.Actions = append(row.Actions, act("up", "Move up"))
		}
		if i < len(list)-1 {
			row.Actions = append(row.Actions, act("down", "Move down"))
		}
		del := act("delete", "Delete")
		del.Confirm = "Delete this badge? Members who were awarded it by hand lose it."
		row.Actions = append(row.Actions, del)
		d.BadgeRows = append(d.BadgeRows, row)
	}
	return true
}
