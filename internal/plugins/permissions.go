package plugins

import (
	"sort"
	"strings"
)

// Permission is a capability a plugin declares in plugin.json and an
// administrator approves. A plugin can only use a host function or subscribe
// to a hook when the permission it needs is both declared and granted.
type Permission string

// The fixed permission vocabulary.
const (
	// PermOptionsRead allows reading site options (get_option).
	PermOptionsRead Permission = "options:read"
	// PermOptionsWrite allows creating and updating site options
	// (update_option).
	PermOptionsWrite Permission = "options:write"
	// PermPostsRead allows reading posts (get_post, query_posts) and
	// subscribing to hooks that expose post data, such as save_post or
	// wpgo_post_stats.
	PermPostsRead Permission = "posts:read"
	// PermPostsWrite allows creating and changing posts (insert_post,
	// update_post) and subscribing to hooks that can change post data before
	// it is stored, such as wp_insert_post_data.
	PermPostsWrite Permission = "posts:write"
	// PermHTTPOutbound allows outbound HTTP GET requests through the host
	// (http_get). The host may further restrict destinations.
	PermHTTPOutbound Permission = "http:outbound"
	// PermCron allows subscribing to scheduled-event hooks (names starting
	// with "cron_" or "wpgo_cron").
	PermCron Permission = "cron"
	// PermAdminNotices allows subscribing to admin_notices and queueing
	// dashboard notices (add_admin_notice).
	PermAdminNotices Permission = "admin:notices"
	// PermContentFilter allows filtering rendered content: the_content,
	// the_title, the_excerpt, comment_text, widget_text and the other
	// "the_*" display filters.
	PermContentFilter Permission = "content:filter"
	// PermStorage allows using the plugin-private key/value store
	// (storage_get, storage_set) and transients (transient_get,
	// transient_set, transient_delete).
	PermStorage Permission = "storage"
	// PermRestRoutes allows registering REST API routes under the plugin's
	// own namespace (register_rest_route).
	PermRestRoutes Permission = "rest:routes"
	// PermUsersRead allows reading the current user's identity, roles and
	// capabilities (get_current_user).
	PermUsersRead Permission = "users:read"
	// PermAdminSettings allows registering a settings page under Settings in
	// the admin (register_settings_page).
	PermAdminSettings Permission = "admin:settings"
	// PermForms allows subscribing to the contact form hooks (names starting
	// with "wpcf7_"): what visitors submitted, and the spam verdict.
	PermForms Permission = "forms:access"
	// PermContentHTML lets the plugin's shortcodes and content filters print
	// HTML the core does not sanitise (scripts, iframes, forms, event
	// handlers) and enqueue scripts on the front end. Without it their
	// output goes through KSES like a contributor's post. A script on the
	// front end runs for logged-in administrators too, so this is close to
	// full control of the site.
	PermContentHTML Permission = "content:html"
	// PermAdminScripts allows enqueueing scripts in the admin
	// (enqueue_script with where "admin" or "both"). Code running in the
	// admin acts with the administrator's session: full control of the site.
	PermAdminScripts Permission = "admin:scripts"
	// PermMailSend allows sending e-mail through the site's mail settings
	// (send_mail, the wp_mail equivalent). The host limits recipients and rate.
	PermMailSend Permission = "mail:send"
)

// permissionDescriptions documents every permission for admin screens.
var permissionDescriptions = map[Permission]string{
	PermOptionsRead:   "Read site options",
	PermOptionsWrite:  "Create and change site options",
	PermPostsRead:     "Read posts and receive post data from post lifecycle hooks",
	PermPostsWrite:    "Create and change posts",
	PermHTTPOutbound:  "Make outbound HTTP requests",
	PermCron:          "Run scheduled tasks",
	PermAdminNotices:  "Show notices in the admin dashboard",
	PermContentFilter: "Modify content shown to visitors",
	PermStorage:       "Store private plugin data",
	PermRestRoutes:    "Add REST API endpoints",
	PermUsersRead:     "See who the current user is",
	PermAdminSettings: "Add a settings page to the admin",
	PermForms:         "See what visitors submit in contact forms and decide what counts as spam",
	PermContentHTML:   "Show unfiltered HTML and scripts to visitors (they also run for logged-in administrators: full control of the site)",
	PermAdminScripts:  "Run code in the admin (full control of the site)",
	PermMailSend:      "Send e-mail from the site",
}

// Dangerous reports whether p gives the plugin the equivalent of an
// administrator's session (the consent screen leaves it unticked).
func (p Permission) Dangerous() bool { return p == PermContentHTML || p == PermAdminScripts }

// AllPermissions returns the permission vocabulary in a stable order.
func AllPermissions() []Permission {
	out := make([]Permission, 0, len(permissionDescriptions))
	for p := range permissionDescriptions {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Valid reports whether p belongs to the vocabulary.
func (p Permission) Valid() bool {
	_, ok := permissionDescriptions[p]
	return ok
}

// Description returns a short human readable description of p.
func (p Permission) Description() string { return permissionDescriptions[p] }

// PermissionSet is a set of permissions.
type PermissionSet map[Permission]bool

// NewPermissionSet builds a set from a list.
func NewPermissionSet(perms ...Permission) PermissionSet {
	s := make(PermissionSet, len(perms))
	for _, p := range perms {
		s[p] = true
	}
	return s
}

// Has reports whether p is in the set.
func (s PermissionSet) Has(p Permission) bool { return s[p] }

// List returns the set's members sorted.
func (s PermissionSet) List() []Permission {
	out := make([]Permission, 0, len(s))
	for p := range s {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Intersect returns the permissions present in both sets.
func (s PermissionSet) Intersect(other PermissionSet) PermissionSet {
	out := PermissionSet{}
	for p := range s {
		if other[p] {
			out[p] = true
		}
	}
	return out
}

var (
	adminNoticeHooks = map[string]bool{
		"admin_notices": true, "all_admin_notices": true, "network_admin_notices": true, "user_admin_notices": true,
	}
	contentHooks = map[string]bool{
		"comment_text": true, "widget_text": true, "widget_title": true, "get_the_excerpt": true,
		"wp_head": true, "wp_footer": true, "wp_title": true, "document_title_parts": true, "body_class": true, "post_class": true,
	}
	userHooks     = map[string]bool{"user_register": true, "wp_login": true}
	postReadHooks = map[string]bool{
		"save_post": true, "wp_insert_post": true, "publish_post": true, "transition_post_status": true,
		"before_delete_post": true, "deleted_post": true, "trashed_post": true, "untrashed_post": true,
		"post_updated": true, "wpgo_post_stats": true,
	}
	postWriteHooks = map[string]bool{
		"wp_insert_post_data": true, "wp_insert_attachment_data": true, "pre_post_update": true,
		"content_save_pre": true, "title_save_pre": true, "excerpt_save_pre": true,
	}
)

// HookPermission returns the permission needed to subscribe to hook, or ""
// when the hook is unrestricted (e.g. "init").
func HookPermission(hook string) Permission {
	switch {
	case adminNoticeHooks[hook]:
		return PermAdminNotices
	case userHooks[hook]:
		return PermUsersRead
	case postWriteHooks[hook]:
		return PermPostsWrite
	case postReadHooks[hook] || strings.HasPrefix(hook, "save_post_"):
		return PermPostsRead
	case contentHooks[hook] || strings.HasPrefix(hook, "the_"):
		return PermContentFilter
	case strings.HasPrefix(hook, "cron_") || strings.HasPrefix(hook, "wpgo_cron"):
		return PermCron
	case strings.HasPrefix(hook, "wpcf7_"):
		return PermForms // submissions carry visitors' names, e-mail addresses and messages
	}
	return ""
}
