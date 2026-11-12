// Package caps resolves capabilities with the multi-role rules from the spec:
// a user's capabilities are the union of their roles, per-user exceptions can
// allow or deny a capability, and a deny always wins. Meta capabilities such as
// edit_post are mapped to primitive ones the way map_meta_cap() does.
package caps

// Grants is the effective capability set of a user.
type Grants map[string]bool

// Resolve applies the precedence: user deny > user allow > union of role capabilities.
func Resolve(roleCaps []map[string]bool, exceptions map[string]string) Grants {
	g := Grants{}
	for _, rc := range roleCaps {
		for c, ok := range rc {
			if ok {
				g[c] = true
			}
		}
	}
	for c, effect := range exceptions {
		if effect == "allow" {
			g[c] = true
		}
	}
	for c, effect := range exceptions {
		if effect == "deny" {
			delete(g, c)
		}
	}
	return g
}

// Post is the subset of a post that capability checks need.
type Post interface {
	PostAuthorID() int64
	PostStatus() string
	PostType() string
}

// Can reports whether the user may perform cap, mapping meta capabilities against post when given.
func Can(userID int64, g Grants, cap string, post Post) bool {
	for _, c := range MapMetaCap(userID, cap, post) {
		if c == "do_not_allow" || !g[c] {
			return false
		}
	}
	return true
}

// typeCaps maps a post type to the one primitive capability that every post meta capability
// (edit/delete/publish/read) on posts of that type requires, whoever the author is. It is for
// types that are site configuration rather than content, such as Theme Builder templates
// (edit_theme_options), so screens that only check edit_post cannot be used to change them.
// Filled once at start-up by RegisterTypeCap; read-only afterwards.
var typeCaps = map[string]typeCap{}

// typeCap is a registered type capability; on (when set) says whether it applies right now (a
// plugin's types follow the core rules while the plugin is off).
type typeCap struct {
	cap string
	on  func() bool
}

// RegisterTypeCap makes every post meta capability on posts of postType require cap.
// Call it during package initialisation only.
func RegisterTypeCap(postType, cap string) { typeCaps[postType] = typeCap{cap: cap} }

// RegisterTypeCapWhen is RegisterTypeCap that applies only while on() reports true (a native
// plugin's post types, which must not change who can edit a same-named type while it is off).
// Call it during package initialisation only.
func RegisterTypeCapWhen(postType, cap string, on func() bool) {
	typeCaps[postType] = typeCap{cap: cap, on: on}
}

// MapMetaCap returns the primitive capabilities required for cap, like WordPress' map_meta_cap().
func MapMetaCap(userID int64, cap string, post Post) []string {
	if post != nil {
		if tc, ok := typeCaps[post.PostType()]; ok && (tc.on == nil || tc.on()) {
			switch cap {
			case "edit_post", "edit_page", "delete_post", "delete_page", "publish_post", "read_post", "read_page":
				return []string{tc.cap}
			}
		}
	}
	if post == nil {
		switch cap {
		case "edit_post", "delete_post", "publish_post", "read_post", "edit_page", "delete_page", "read_page":
			return []string{"do_not_allow"}
		}
		return []string{cap}
	}
	plural := "posts"
	if post.PostType() == "page" {
		plural = "pages"
	}
	own := userID != 0 && post.PostAuthorID() == userID
	status := post.PostStatus()

	switch cap {
	case "edit_post", "edit_page":
		if status == "trash" {
			// Trashed posts keep the capability of the status they had; treat as published, the stricter case.
			status = "publish"
		}
		if own {
			switch status {
			case "publish", "future":
				return []string{"edit_published_" + plural}
			case "private":
				return []string{"edit_private_" + plural}
			}
			return []string{"edit_" + plural}
		}
		caps := []string{"edit_others_" + plural}
		switch status {
		case "publish", "future":
			caps = append(caps, "edit_published_"+plural)
		case "private":
			caps = append(caps, "edit_private_"+plural)
		}
		return caps
	case "delete_post", "delete_page":
		if status == "trash" {
			status = "publish"
		}
		if own {
			switch status {
			case "publish", "future":
				return []string{"delete_published_" + plural}
			case "private":
				return []string{"delete_private_" + plural}
			}
			return []string{"delete_" + plural}
		}
		caps := []string{"delete_others_" + plural}
		switch status {
		case "publish", "future":
			caps = append(caps, "delete_published_"+plural)
		case "private":
			caps = append(caps, "delete_private_"+plural)
		}
		return caps
	case "publish_post":
		return []string{"publish_" + plural}
	case "read_post", "read_page":
		if status == "private" {
			if own {
				return []string{"read"}
			}
			return []string{"read_private_" + plural}
		}
		if status == "publish" {
			return []string{"read"}
		}
		// Drafts and pending posts are readable by whoever may edit them.
		return MapMetaCap(userID, "edit_post", post)
	}
	return []string{cap}
}
