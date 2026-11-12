package server

import (
	"fmt"
	"html/template"
	"time"

	"wpgo/internal/i18n"
)

// The Pursae Forum domain types. They are the contract between the store (forum_store.go), the
// views (forum_views.go, forum_account.go) and the templates (plugin defaults and theme overrides).

// forumAuthor is a member as the forum shows them.
type forumAuthor struct {
	ID                                                  int64
	Login, Nicename, DisplayName, AvatarURL, ProfileURL string
	Moderator                                           bool // Can(forum_moderate)
	Blocked                                             bool // denied forum_participate

	// Avatar (WP7): AvatarURL is the photo or the default image; HasPhoto tells them apart.
	AvatarID int64  // users.meta avatar_id (0 = none)
	HasPhoto bool   // the member has a Media Library photo (else render the initials avatar)
	Initials string // one or two letters of the display name ("AB")
	Hue      int    // 0..359 from the user id: the initials avatar's background colour

	// Statistics, level and badges (WP7, forum_gamify.go); filled by forumAuthors (batched) and
	// forumGamifyFill. Stats is false while they are not loaded (forumAuthorOf: the current user).
	Stats                   bool
	Topics, Replies, Points int
	Joined, LastActive      time.Time   // LastActive: last published post, zero = never
	Verified, Google        bool        // email confirmed; Google account linked
	Level                   *forumLevel // nil while Stats is false
	NextLevel               *forumLevel // nil at the top level
	Progress                int         // 0..100 toward NextLevel
	Badges                  []forumBadge
}

// forumForum is a forum or a forum category.
type forumForum struct {
	ID, ParentID   int64
	Slug, Title    string
	Description    template.HTML
	Kind           string // "forum" | "category"
	Closed         bool
	Order          int
	TopicCount     int
	ReplyCount     int
	LastActive     time.Time
	LastTopicID    int64
	LastTopicTitle string
	LastTopicURL   string
	LastAuthor     *forumAuthor
	URL            string
	Children       []*forumForum
}

// forumTopic is a topic (its opening post included).
type forumTopic struct {
	ID, ForumID, AuthorID int64
	Slug, Title           string
	Content               string // stored sanitized HTML; render with forumFormat
	Status                string // publish|pending|trash
	Spam, Sticky, Closed  bool
	ReplyCount            int
	CreatedAt, LastActive time.Time
	EditedAt              *time.Time
	LastReplyID           int64
	LastAuthor, Author    *forumAuthor
	URL                   string
	ForumTitle, ForumURL  string
	IP                    string // filled only for moderators
}

// forumReply is a reply to a topic.
type forumReply struct {
	ID, TopicID, ForumID, AuthorID int64
	Content                        string
	Status                         string
	Spam                           bool
	CreatedAt                      time.Time
	EditedAt                       *time.Time
	Author                         *forumAuthor
	URL                            string
	Position                       int // 1-based among published replies
	IP                             string
}

// forumRoute is a parsed forum URL (forumParsePath).
type forumRoute struct {
	View string // forumView* below
	Slug string // forum slug, nicename
	ID   int64  // topic/reply/edit target
	Page int    // >=1
}

const (
	forumViewIndex    = "index"
	forumViewForum    = "forum"
	forumViewTopic    = "topic"
	forumViewReply    = "reply"
	forumViewNewTopic = "new-topic"
	forumViewEdit     = "edit"
	forumViewSearch   = "search"
	forumViewProfile  = "profile"
	forumViewLogin    = "login"
	forumViewRegister = "register"
	forumViewVerify   = "verify"
	forumViewAccount  = "account"
	forumViewMembers  = "members" // WP7: /forums/members/[page/N/]?q=&sort= (forumSettings.ShowMembers)
)

// forumPrivateViews are noindex + no-store.
var forumPrivateViews = map[string]bool{forumViewNewTopic: true, forumViewEdit: true, forumViewSearch: true,
	forumViewLogin: true, forumViewRegister: true, forumViewVerify: true, forumViewAccount: true}

type forumCrumb struct{ Label, URL string }

type forumURLs struct {
	Index, Search, Login, Register, Account, Logout, LostPassword, GoogleStart, GoogleLink, NewTopic string
}

type forumEdit struct {
	ID      int64
	IsTopic bool
	Title   string
	Content string
}

type forumProfileStats struct {
	Topics, Replies int
	Registered      time.Time
}

// forumPage is the dot of every forum view template (plugin default and theme override alike).
type forumPage struct {
	View, Title  string
	OwnTitle     bool // active theme supports["pursae-forum"]: the body prints its H1; else the theme prints the page title
	Crumbs       []forumCrumb
	User         *forumAuthor // nil = logged out
	CanPost      bool
	CanModerate  bool
	Unverified   bool
	GoogleOn     bool
	RegisterOn   bool
	Nonce        string // "" on cacheable anonymous views
	AuthNonce    string // "" on cacheable anonymous views
	Notice       string // plain text, escaped by template
	Error        string // plain text, escaped by template
	URLs         forumURLs
	Forums       []*forumForum // index (tree), new-topic forum picker
	Forum        *forumForum
	Topics       []*forumTopic
	Topic        *forumTopic
	Replies      []*forumReply
	Profile      *forumAuthor
	ProfileStats forumProfileStats
	Pagination   template.HTML
	Query        string
	Form         map[string]string // re-filled values after a validation error
	Edit         *forumEdit
	Traps        template.HTML    // honeypot + signed time fields
	Captcha      template.HTML    // loginCaptchaWidget() or ""
	Members      []*forumMember   // WP7 members view: one page of rows
	MemberQuery  forumMemberQuery // WP7 members view: the normalized query (Search, Sort, Page)
	MembersTotal int              // WP7 members view: matching members
	Extra        map[string]any   // package-private additions without contract changes
	cat          *i18n.Catalog
}

// T translates msg into the front end's language (msg itself when there is no translation).
func (p *forumPage) T(msg string) string {
	if p == nil || p.cat == nil {
		return msg
	}
	return p.cat.T(msg)
}

// Tf translates format and fills its %s / %d / %1$s verbs with args.
func (p *forumPage) Tf(format string, args ...any) string {
	strs := make([]string, len(args))
	for i, a := range args {
		strs[i] = fmt.Sprint(a)
	}
	return i18n.Sprintf(p.T(format), strs...)
}
