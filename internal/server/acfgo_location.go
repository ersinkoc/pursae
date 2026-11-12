package server

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"wpgo/internal/store"
)

// acfScreen is the context location rules are matched against (ACF's $screen array): the object
// being edited and where.
type acfScreen struct {
	// Post edit screens.
	PostID       int64
	PostType     string
	PostStatus   string
	PostFormat   string
	PostTemplate string
	PostParent   int64
	PostTerms    map[string][]*store.Term // by taxonomy
	HasChildren  bool
	isPost       bool

	// User screens: UserForm is add, edit or register; UserRoles the roles of the edited user.
	UserForm  string
	UserRoles []string
	UserID    int64

	Taxonomy    string // term add/edit screens
	Attachment  string // attachment edit screen: the MIME type
	Comment     string // comment edit screen: the post type of the comment's post
	OptionsPage string
	Block       string

	// Appearance › Menus: the ID of the menu being edited (NavMenu for the menu's own fields,
	// NavMenuItem for the fields of its items) and the theme locations it is assigned to.
	NavMenu, NavMenuItem string
	NavMenuLocations     []string

	// Current user (current_user / current_user_role rules).
	User      *store.User
	FrontEnd  bool
	frontPage int64
	postsPage int64
}

// acfgoPostScreen is the screen of a post's edit form.
func (s *Server) acfgoPostScreen(ctx context.Context, p *store.Post, u *store.User) *acfScreen {
	sc := &acfScreen{isPost: true, PostID: p.ID, PostType: p.Type, PostStatus: p.Status, PostFormat: defaultStr(p.Format, "standard"),
		PostParent: p.ParentID, User: u, PostTerms: map[string][]*store.Term{},
		frontPage: int64(s.settings.Int("page_on_front")), postsPage: int64(s.settings.Int("page_for_posts"))}
	if p.Status == "auto-draft" {
		sc.PostStatus = "draft"
	}
	sc.PostTemplate, _ = p.Meta["_wp_page_template"].(string)
	if sc.PostTemplate == "" {
		sc.PostTemplate = "default"
	}
	if p.ID > 0 {
		rows, err := s.store.Pool.Query(ctx, `SELECT t.id, t.taxonomy, t.slug, t.name FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = $1`, p.ID)
		if err == nil {
			for rows.Next() {
				t := &store.Term{}
				if rows.Scan(&t.ID, &t.Taxonomy, &t.Slug, &t.Name) == nil {
					sc.PostTerms[t.Taxonomy] = append(sc.PostTerms[t.Taxonomy], t)
				}
			}
			rows.Close()
		}
		_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE parent_id = $1 AND status <> 'trash')`, p.ID).Scan(&sc.HasChildren)
	}
	return sc
}

// acfgoMatchGroup is acf_get_field_group_visibility(): any OR group whose rules all match.
func (s *Server) acfgoMatchGroup(g *acfGroup, sc *acfScreen) bool {
	if !g.Active || g.Status == "trash" {
		return false
	}
	for _, rules := range g.Location {
		ok := len(rules) > 0
		for _, r := range rules {
			if !acfgoMatchRule(r, sc) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// acfgoGroupsFor returns the active groups shown on a screen, in menu order.
func (s *Server) acfgoGroupsFor(ctx context.Context, sc *acfScreen) []*acfGroup {
	var out []*acfGroup
	for _, g := range s.acfgoAllGroups(ctx, false) {
		if s.acfgoMatchGroup(g, sc) {
			out = append(out, g)
		}
	}
	return out
}

func acfgoCompare(op string, match bool) bool {
	if op == "!=" {
		return !match
	}
	return match
}

// acfgoMatchRule matches one rule. A rule about something the screen does not have (a post rule
// on a user form) never matches, whatever the operator — ACF's behaviour.
func acfgoMatchRule(r acfRule, sc *acfScreen) bool {
	v := r.Value
	switch r.Param {
	case "post_type":
		if !sc.isPost {
			return false
		}
		return acfgoCompare(r.Operator, v == "all" || sc.PostType == v)
	case "post_status":
		if !sc.isPost {
			return false
		}
		return acfgoCompare(r.Operator, sc.PostStatus == v)
	case "post_format":
		if !sc.isPost {
			return false
		}
		return acfgoCompare(r.Operator, sc.PostFormat == v)
	case "post_template", "page_template":
		if !sc.isPost || (r.Param == "page_template" && sc.PostType != "page") {
			return false
		}
		return acfgoCompare(r.Operator, sc.PostTemplate == v || (v == "default" && sc.PostTemplate == ""))
	case "post_category", "post_taxonomy":
		if !sc.isPost {
			return false
		}
		tax, slug, found := strings.Cut(v, ":")
		if !found {
			tax, slug = "category", v
		}
		match := false
		for _, t := range sc.PostTerms[tax] {
			if t.Slug == slug || strconv.FormatInt(t.ID, 10) == slug {
				match = true
			}
		}
		return acfgoCompare(r.Operator, match)
	case "post", "page":
		if !sc.isPost || (r.Param == "page" && sc.PostType != "page") {
			return false
		}
		return acfgoCompare(r.Operator, strconv.FormatInt(sc.PostID, 10) == v)
	case "page_parent":
		if !sc.isPost || sc.PostType != "page" {
			return false
		}
		return acfgoCompare(r.Operator, strconv.FormatInt(sc.PostParent, 10) == v)
	case "page_type":
		if !sc.isPost || sc.PostType != "page" {
			return false
		}
		var match bool
		switch v {
		case "front_page":
			match = sc.frontPage != 0 && sc.PostID == sc.frontPage
		case "posts_page":
			match = sc.postsPage != 0 && sc.PostID == sc.postsPage
		case "top_level":
			match = sc.PostParent == 0
		case "parent":
			match = sc.HasChildren
		case "child":
			match = sc.PostParent != 0
		}
		return acfgoCompare(r.Operator, match)
	case "current_user":
		var match bool
		switch v {
		case "logged_in":
			match = sc.User != nil && sc.User.ID != 0
		case "viewing_front":
			match = sc.FrontEnd
		case "viewing_back":
			match = !sc.FrontEnd
		}
		return acfgoCompare(r.Operator, match)
	case "current_user_role":
		if sc.User == nil {
			return false
		}
		match := v == "all"
		for _, role := range sc.User.Roles {
			if role == v || (v == "super_admin" && role == "administrator") {
				match = true
			}
		}
		return acfgoCompare(r.Operator, match)
	case "user_form":
		if sc.UserForm == "" {
			return false
		}
		return acfgoCompare(r.Operator, v == "all" || sc.UserForm == v || (v == "edit" && sc.UserForm == "edit"))
	case "user_role":
		if sc.UserForm == "" {
			return false
		}
		match := v == "all"
		for _, role := range sc.UserRoles {
			if role == v {
				match = true
			}
		}
		if sc.UserForm == "add" && len(sc.UserRoles) == 0 { // the add form has no role yet: show the fields
			match = true
		}
		return acfgoCompare(r.Operator, match)
	case "taxonomy":
		if sc.Taxonomy == "" {
			return false
		}
		return acfgoCompare(r.Operator, v == "all" || sc.Taxonomy == v)
	case "attachment":
		if sc.Attachment == "" {
			return false
		}
		match := v == "all" || sc.Attachment == v || strings.HasPrefix(sc.Attachment, v+"/")
		return acfgoCompare(r.Operator, match)
	case "comment":
		if sc.Comment == "" {
			return false
		}
		return acfgoCompare(r.Operator, v == "all" || sc.Comment == v)
	case "options_page":
		if sc.OptionsPage == "" {
			return false
		}
		return acfgoCompare(r.Operator, v == "all" || sc.OptionsPage == v)
	case "block":
		if sc.Block == "" {
			return false
		}
		return acfgoCompare(r.Operator, v == "all" || sc.Block == v)
	case "nav_menu":
		if sc.NavMenu == "" {
			return false
		}
		return acfgoCompare(r.Operator, acfgoMenuRuleMatch(v, sc.NavMenu, sc.NavMenuLocations))
	case "nav_menu_item":
		if sc.NavMenuItem == "" {
			return false
		}
		return acfgoCompare(r.Operator, acfgoMenuRuleMatch(v, sc.NavMenuItem, sc.NavMenuLocations))
	}
	return false // widget: Pursae has no widget form to carry fields
}

// Location rule choices (the Location Rules editor) ----------------------------------------------

type acfgoChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Group string `json:"group,omitempty"`
}

// acfgoLocationParams are ACF's rule types, grouped as in its editor.
var acfgoLocationParams = []acfgoChoice{
	{"post_type", "Post Type", "Post"}, {"post_template", "Post Template", "Post"}, {"post_status", "Post Status", "Post"},
	{"post_format", "Post Format", "Post"}, {"post_category", "Post Category", "Post"}, {"post_taxonomy", "Post Taxonomy", "Post"}, {"post", "Post", "Post"},
	{"page_template", "Page Template", "Page"}, {"page_type", "Page Type", "Page"}, {"page_parent", "Page Parent", "Page"}, {"page", "Page", "Page"},
	{"current_user", "Current User", "User"}, {"current_user_role", "Current User Role", "User"}, {"user_form", "User Form", "User"}, {"user_role", "User Role", "User"},
	{"taxonomy", "Taxonomy", "Forms"}, {"attachment", "Attachment", "Forms"}, {"comment", "Comment", "Forms"},
	{"widget", "Widget", "Forms"}, {"nav_menu", "Menu", "Forms"}, {"nav_menu_item", "Menu Item", "Forms"},
	{"block", "Block", "Forms"}, {"options_page", "Options Page", "Forms"},
}

// acfgoLocationValues lists the values of every rule type for the editor.
func (s *Server) acfgoLocationValues(ctx context.Context) map[string][]acfgoChoice {
	out := map[string][]acfgoChoice{}
	types := []acfgoChoice{{"post", "Post", ""}, {"page", "Page", ""}, {"attachment", "Media", ""}}
	for _, pt := range s.acfgoPostTypes(ctx) {
		types = append(types, acfgoChoice{pt.Name, pt.label("singular_name"), ""})
	}
	out["post_type"] = types
	out["comment"] = append([]acfgoChoice{{"all", "All", ""}}, types...)
	tpls := []acfgoChoice{{"default", "Default Template", ""}}
	for _, t := range s.pageTemplates() {
		if t.Value != "" && t.Value != "default" {
			tpls = append(tpls, acfgoChoice{t.Value, t.Label, ""})
		}
	}
	out["post_template"], out["page_template"] = tpls, tpls
	out["post_status"] = []acfgoChoice{{"publish", "Published", ""}, {"future", "Scheduled", ""}, {"draft", "Draft", ""}, {"pending", "Pending Review", ""}, {"private", "Private", ""}, {"trash", "Trash", ""}}
	var formats []acfgoChoice
	for _, f := range []string{"standard", "aside", "chat", "gallery", "link", "image", "quote", "status", "video", "audio"} {
		formats = append(formats, acfgoChoice{f, postFormatLabels[f], ""})
	}
	out["post_format"] = formats
	rows, err := s.store.Pool.Query(ctx, `SELECT t.taxonomy, t.slug, t.name, coalesce(x.labels->>'singular_name', x.name) FROM terms t JOIN taxonomies x ON x.slug = t.taxonomy
		WHERE t.taxonomy <> 'post_format' ORDER BY t.taxonomy, t.name LIMIT 2000`)
	if err == nil {
		for rows.Next() {
			var tax, slug, name, tl string
			if rows.Scan(&tax, &slug, &name, &tl) == nil {
				out["post_taxonomy"] = append(out["post_taxonomy"], acfgoChoice{tax + ":" + slug, name, tl})
				if tax == "category" {
					out["post_category"] = append(out["post_category"], acfgoChoice{"category:" + slug, name, ""})
				}
			}
		}
		rows.Close()
	}
	posts, _, _ := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{"post", "page"}, Statuses: []string{"publish", "private", "draft", "pending", "future"}, PerPage: 500, OrderBy: "title", Order: "asc"})
	for _, p := range posts {
		t := defaultStr(p.Title, "(no title)")
		if p.Type == "page" {
			out["page"] = append(out["page"], acfgoChoice{itoa64(p.ID), t, ""})
			out["page_parent"] = append(out["page_parent"], acfgoChoice{itoa64(p.ID), t, ""})
		} else {
			out["post"] = append(out["post"], acfgoChoice{itoa64(p.ID), t, "Posts"})
		}
	}
	out["post"] = append(out["post"], func() []acfgoChoice {
		var l []acfgoChoice
		for _, c := range out["page"] {
			l = append(l, acfgoChoice{c.Value, c.Label, "Pages"})
		}
		return l
	}()...)
	out["page_type"] = []acfgoChoice{{"front_page", "Front Page", ""}, {"posts_page", "Posts Page", ""}, {"top_level", "Top Level Page (no parent)", ""}, {"parent", "Parent Page (has children)", ""}, {"child", "Child Page (has parent)", ""}}
	out["current_user"] = []acfgoChoice{{"logged_in", "Logged in", ""}, {"viewing_front", "Viewing front end", ""}, {"viewing_back", "Viewing back end", ""}}
	var roles []acfgoChoice
	if rr, err := s.store.Pool.Query(ctx, `SELECT slug, name FROM roles ORDER BY id`); err == nil {
		for rr.Next() {
			var slug, name string
			if rr.Scan(&slug, &name) == nil {
				roles = append(roles, acfgoChoice{slug, name, ""})
			}
		}
		rr.Close()
	}
	out["current_user_role"] = append([]acfgoChoice{{"super_admin", "Super Admin", ""}}, roles...)
	out["user_role"] = append([]acfgoChoice{{"all", "All", ""}}, roles...)
	out["user_form"] = []acfgoChoice{{"all", "All", ""}, {"add", "Add", ""}, {"edit", "Add / Edit", ""}, {"register", "Register", ""}}
	taxes := []acfgoChoice{{"all", "All", ""}}
	if tr, err := s.store.Pool.Query(ctx, `SELECT slug, coalesce(labels->>'name', name) FROM taxonomies WHERE slug <> 'post_format' ORDER BY id`); err == nil {
		for tr.Next() {
			var slug, name string
			if tr.Scan(&slug, &name) == nil {
				taxes = append(taxes, acfgoChoice{slug, name, ""})
			}
		}
		tr.Close()
	}
	out["taxonomy"] = taxes
	out["attachment"] = []acfgoChoice{{"all", "All", ""}, {"image", "Image", ""}, {"video", "Video", ""}, {"audio", "Audio", ""}, {"application", "Application", ""}, {"text", "Text", ""}}
	out["widget"] = []acfgoChoice{{"all", "All (no such screen in Pursae)", ""}}
	out["nav_menu"] = s.acfgoMenuLocationChoices(ctx)
	out["nav_menu_item"] = out["nav_menu"]
	blocks := []acfgoChoice{{"all", "All", ""}}
	for _, b := range s.acfgoBlocks() {
		blocks = append(blocks, acfgoChoice{b.Name, b.Title, ""})
	}
	out["block"] = blocks
	ops := []acfgoChoice{}
	for _, op := range s.acfgoOptionsPages(ctx) {
		ops = append(ops, acfgoChoice{op.MenuSlug, op.PageTitle, ""})
	}
	if len(ops) == 0 {
		ops = append(ops, acfgoChoice{"", "No options pages exist", ""})
	}
	out["options_page"] = ops
	for k := range out {
		if out[k] == nil {
			out[k] = []acfgoChoice{}
		}
	}
	return out
}

// acfgoLocationSummary is the Location column of the field group list ("Post Type is equal to Post").
// acfgoUnsupportedLocation reports whether a group has a location rule whose screen does not exist in
// Pursae (widgets), so the group would never be shown there.
func acfgoUnsupportedLocation(g *acfGroup) bool {
	for _, grp := range g.Location {
		for _, r := range grp {
			switch r.Param {
			case "widget":
				return true
			}
		}
	}
	return false
}

func (s *Server) acfgoLocationSummary(g *acfGroup, values map[string][]acfgoChoice) []string {
	params := map[string]string{}
	for _, p := range acfgoLocationParams {
		params[p.Value] = p.Label
	}
	var out []string
	for _, grp := range g.Location {
		var parts []string
		for _, r := range grp {
			label := r.Value
			for _, c := range values[r.Param] {
				if c.Value == r.Value {
					label = c.Label
				}
			}
			op := "is equal to"
			if r.Operator == "!=" {
				op = "is not equal to"
			}
			parts = append(parts, params[r.Param]+" "+op+" "+label)
		}
		out = append(out, strings.Join(parts, " and "))
	}
	sort.Strings(out)
	return out
}
