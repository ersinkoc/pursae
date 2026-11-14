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
	Admin                                               bool // Can(manage_options) (v0.2.5: the moderator list's role label)
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
	// v0.2.5 (forum_engage.go): accepted answers, likes received, manual point adjustments.
	Solutions, LikesReceived, Bonus int
	Streak                          int // longest run of consecutive days with a published post
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

	// Design meta (v0.2.5, forum_meta.go; posts.meta of the forum post). All optional.
	Icon       string         // a forum_icon name ("" = none)
	Color      string         // a pastel key: sky|mint|peach|lilac|grey ("" = none)
	Label      string         // a short tab label ("Başlarken")
	Number     string         // a short number or code ("01"); "" = none
	Chips      []forumTag     // descriptive chips; each links to the forum's ?tag=<slug> filter
	Tips       []string       // "in this forum" tips, one line each
	StaffOnly  bool           // only moderators start topics; members still reply
	Moderators []*forumAuthor // filled on the forum view (forumModeratorsOf): the forum's own list, else the site's moderators
	Online     int            // members and visitors seen on this forum in the last minutes (forum view; REST refreshes it)
}

// forumTag is a topic tag (or a forum chip): its name as typed, its slug and its URL.
type forumTag struct {
	Name, Slug, URL string
	Count           int // topics carrying it (tag lists only)
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

	// v0.2.5 (forum_engage.go, forum_meta.go).
	Tags                              []forumTag     // topic tags (posts.meta _forum_tags)
	Views                             int            // forum_topic_views (the view beacon)
	Likes                             int            // likes of the opening post
	Solved                            bool           // a reply is accepted as the solution
	SolutionID                        int64          // that reply
	IsNew                             bool           // created in the last 24 hours
	Participants                      []*forumAuthor // up to 4 most recent distinct repliers (lists and the topic view)
	ForumIcon, ForumColor, ForumLabel string         // the forum's design meta, for the forum chip of a row
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
	Likes                          int  // v0.2.5: forum_likes of this reply
	Solution                       bool // v0.2.5: the topic's accepted answer
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
	// v0.2.5.
	forumViewTopics      = "topics"      // /forums/topics/[page/N/]?sort=&tag=  every topic
	forumViewTag         = "tag"         // /forums/tag/<slug>/[page/N/]?sort=  topics with a tag
	forumViewTags        = "tags"        // /forums/tags/  the tag list
	forumViewUnsubscribe = "unsubscribe" // /forums/unsubscribe/?t=&u=&k=  the email's unsubscribe link
)

// forumPrivateViews are noindex + no-store.
var forumPrivateViews = map[string]bool{forumViewNewTopic: true, forumViewEdit: true, forumViewSearch: true,
	forumViewLogin: true, forumViewRegister: true, forumViewVerify: true, forumViewAccount: true, forumViewUnsubscribe: true}

type forumCrumb struct{ Label, URL string }

type forumURLs struct {
	Index, Search, Login, Register, Account, Logout, LostPassword, GoogleStart, GoogleLink, NewTopic string
	// v0.2.5: every topic, the tag list, the new-topic form with the forum picker, the community rules.
	Topics, Tags, NewAny, Rules string
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

	// v0.2.5 additions (forum_meta.go, forum_engage.go). Every one is optional for templates.
	Stats      *forumStats     // index, register, login, verify: site totals (members, online, solved …)
	Sort       string          // topic lists: latest|popular|unanswered|solved
	Sorts      []forumSortLink // topic lists: the sort tabs (URLs keep ?tag=)
	Tag        *forumTag       // topic lists filtered by a tag (?tag= or /tag/<slug>/)
	Tags       []forumTag      // the tag list view; the topic list's popular tags
	Weekly     []*forumWeekly  // index, forum: members of the week (points earned in 7 days)
	Similar    []*forumTopic   // topic, new topic: similar topics
	Solution   *forumReply     // topic: the accepted answer (whatever page it is on)
	Preview    template.HTML   // new topic, reply: the rendered preview of the typed text
	Draft      *forumDraft     // new topic: the member's saved draft
	Subscribed bool            // topic: the member gets an email on new replies
	Liked      map[int64]bool  // topic: the posts the member likes
	cat        *i18n.Catalog
	levels     func() []forumLevel // lazy loaders (forumPageData): Levels, Badges, Points
	badges     func() []forumBadge
	points     func() forumLevels
	dates      func(t time.Time, withTime bool) string // forumPageData: the site's formats, the viewer's month names
}

// forumStats are the site totals (forumStatsOf).
type forumStats struct {
	Forums, Topics, Replies, Members int
	Online                           int // members and visitors seen in the last minutes
	OnlineMembers                    int // of whom logged in
	Solved                           int // topics with an accepted answer
	SolvedPct                        int // 0..100 of the published topics
	NewTopicsWeek, NewMembersWeek    int // the last 7 days
}

// forumSortLink is a sort tab of a topic list.
type forumSortLink struct {
	Key, Label, URL string
	Current         bool
}

// forumWeekly is a member of the week: the author (level and badges filled) and the points earned in
// the last seven days.
type forumWeekly struct {
	Author *forumAuthor
	Delta  int
}

// forumDraft is a member's saved new-topic draft (users.meta forum_draft).
type forumDraft struct {
	ForumID int64     `json:"forum_id"`
	Title   string    `json:"title"`
	Content string    `json:"content"`
	Tags    string    `json:"tags"`
	Notify  bool      `json:"notify"`
	Saved   time.Time `json:"saved"`
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
