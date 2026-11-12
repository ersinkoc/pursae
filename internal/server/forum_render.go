package server

import (
	"bytes"
	"context"
	"html"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/cache"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// Every forum view renders as a synthetic page of the active theme (the Commerce pattern): the view
// body is rendered first (forumExec: a theme override templates/forum/<view>.html when the active
// theme has one, else the plugin's web/forum/templates), kept on the request, and the theme renders
// a page whose content is forumRenderMarker, which forumShortcodes swaps for the body. A theme that
// supports "pursae-forum" gets the page template "forum" (templates/forum.html) and prints no title
// of its own (forumPage.OwnTitle: the body prints the H1).

// forumCtxStatus is the gin key of a status other than 200 for a forum page (a validation error is
// 422); forumRender reads it from forumPage.Extra["status"].
const forumCtxStatus = ctxSiteStatus

// forumMessages are the notices ?forum_msg=<code> shows.
var forumMessages = map[string]string{
	"verified":             "Your email is confirmed. Welcome!",
	"verify_sent":          "Check your inbox: we sent you a confirmation link.",
	"google_linked":        "Your Google account is connected.",
	"google_unlinked":      "Your Google account is disconnected.",
	"google_cancelled":     "Google sign-in was cancelled.",
	"google_failed":        "Google sign-in failed. Please try again.",
	"google_link_required": "An account with this email already exists. Log in with your password, then connect Google from your account page.",
	"google_blocked":       "This account signs in with its password.",
	"registration_closed":  "Registration is closed.",
	"login_blocked":        "Access from your IP address has been blocked.",
	"topic_pending":        "Your topic is waiting for a moderator.",
	"reply_pending":        "Your reply is waiting for a moderator.",
	"saved":                "Saved.",
	"password_changed":     "Your password was changed.",
	"blocked":              "The member can no longer post.",
	"unblocked":            "The member can post again.",
	"avatar_saved":         "Your profile photo is updated.",
	"avatar_removed":       "Your profile photo is removed.",
	"badge_awarded":        "The badge is awarded.",
	"badge_revoked":        "The badge is revoked.",
}

// forumCtxNoindex is the gin key a public view sets when this request of it must not be indexed nor
// cached (the member list with ?q=, like search).
const forumCtxNoindex = "forum.front.noindex"

// forumNoindex reports whether the forum view of q is private on this request: noindex and no-store.
func forumNoindex(c *gin.Context, q *siteQuery) bool {
	if q == nil || q.Forum == "" {
		return false
	}
	return forumPrivateViews[q.Forum] || (c != nil && c.GetBool(forumCtxNoindex))
}

// forumTheme is the theme rendering this request (a theme preview, else the active theme).
func (s *Server) forumTheme(c *gin.Context) *Theme {
	if c != nil {
		if t := s.previewTheme(c); t != nil {
			return t
		}
	}
	return s.activeTheme()
}

// forumThemeSupports reports whether the theme declares supports["pursae-forum"].
func forumThemeSupports(t *Theme) bool {
	if t == nil || t.Supports == nil {
		return false
	}
	v, ok := t.Supports[forumThemeSupport]
	return ok && v != false
}

// forumNewPage is a view's dot with everything every view needs: the member, their rights, nonces
// (only when someone is logged in, or on private views), the notice of ?forum_msg, the links, the
// spam traps and the front end's catalog.
func (s *Server) forumNewPage(c *gin.Context, view, title string) *forumPage {
	ctx := c.Request.Context()
	st := s.forumLoad()
	if st.Title == forumDefaults().Title { // the untouched default speaks the site's language; an owner's own title stays as written
		st.Title = s.forumT(ctx, st.Title)
	}
	if title == "" {
		title = st.Title
	}
	p := &forumPage{View: view, Title: title, OwnTitle: forumThemeSupports(s.forumTheme(c)), GoogleOn: s.forumGoogleReady(),
		RegisterOn: st.Registration, URLs: s.forumURLsFor(c), Form: map[string]string{}, Extra: map[string]any{},
		cat: s.gooFrontCatalog(ctx)}
	p.Crumbs = []forumCrumb{{Label: st.Title, URL: p.URLs.Index}}
	if u := currentUser(c); u != nil {
		p.User = s.forumAuthorOf(ctx, u)
		p.Unverified = forumMetaBool(u.Meta, forumUMetaUnverified)
		p.CanModerate = u.Can(capForumModerate, nil)
		p.CanPost = u.Can(capForumParticipate, nil) && !(st.RequireVerify && p.Unverified)
		p.Nonce = s.nonce(c, forumNonce)
		p.AuthNonce = s.nonce(c, forumAuthNonce)
	} else if forumPrivateViews[view] {
		p.AuthNonce = s.nonce(c, forumAuthNonce) // private views are no-store: a logged-out nonce is safe there
	}
	if st.ShowMembers {
		p.Extra["members_url"] = s.forumURL(ctx, "members")
	}
	if msg := forumMessages[c.Query("forum_msg")]; msg != "" {
		p.Notice = p.T(msg)
	}
	p.Traps = template.HTML(`<p class="pf-hp" style="display:none !important" aria-hidden="true"><label for="pf_hp">Leave this field empty</label>` +
		`<input type="text" name="` + commentHoneypot + `" id="pf_hp" value="" tabindex="-1" autocomplete="off" /></p>` +
		`<input type="hidden" name="` + commentTimeField + `" value="` + s.commentTimeToken(0, time.Now()) + `" />`)
	if view == forumViewLogin || view == forumViewRegister {
		p.Captcha = s.loginCaptchaWidget(c)
	}
	return p
}

// forumRender renders a view inside the active theme. canonical is the view's unpaged URL ("" for
// private views); paged is the page number (canonical gets /page/N/).
func (s *Server) forumRender(c *gin.Context, p *forumPage, tmpl string, canonical string, paged int) {
	body := s.forumExec(c, tmpl, p)
	now := time.Now()
	post := &store.Post{ID: 0, Type: "page", Slug: p.View, Title: p.Title, Status: "publish", CommentStatus: "closed", PingStatus: "closed",
		Format: "standard", Content: forumRenderMarker, Meta: map[string]any{"_forum_synthetic": true, "_wp_page_template": "forum"}}
	post.PublishedAt, post.ModifiedAt, post.CreatedAt = &now, now, now
	c.Set(forumCtxRender, body)
	if paged < 1 {
		paged = 1
	}
	q := &siteQuery{Kind: "page", Post: post, Paged: paged, Forum: p.View, ForumURL: canonical}
	if forumNoindex(c, q) {
		c.Header("Cache-Control", "no-store")
		c.Header(cache.HeaderControl, "no-store")
		c.Header("X-Robots-Tag", "noindex")
		q.ForumURL = ""
	}
	if n, ok := p.Extra["status"].(int); ok && n >= 200 && n < 600 {
		c.Set(forumCtxStatus, n)
		if n != http.StatusOK {
			c.Header(cache.HeaderControl, "no-store")
		}
	}
	c.Set(ctxSiteQuery, q)
	s.renderSite(c, q)
}

// forumTplCache holds each Server's plugin template set (see gooTplCache).
var forumTplCache struct {
	sync.Mutex
	sets map[*Server]*template.Template
}

// forumTemplates is the plugin's template set (web/forum/templates), reloaded in development.
func (s *Server) forumTemplates() *template.Template {
	forumTplCache.Lock()
	defer forumTplCache.Unlock()
	if forumTplCache.sets == nil {
		forumTplCache.sets = map[*Server]*template.Template{}
	}
	if set := forumTplCache.sets[s]; set != nil && s.cfg.WebDir == "" {
		return set
	}
	set, err := parseTree(s.web, "forum/templates", s.forumPluginFuncs())
	if err != nil {
		log.Printf("forum templates: %v", err)
		if prev := forumTplCache.sets[s]; prev != nil {
			return prev
		}
		set = template.Must(template.New("").Parse(""))
	}
	forumTplCache.sets[s] = set
	return set
}

// forumPluginFuncs are the functions of the plugin's own templates: the core ones and the forum's.
func (s *Server) forumPluginFuncs() template.FuncMap {
	funcs := s.tmpl.coreFuncs(s)
	for k, f := range s.forumTemplateFuncs() {
		funcs[k] = f
	}
	return funcs
}

// forumExec renders view template tmpl ("index", "topic", "shortcode-recent", …): the active theme's
// "forum/<tmpl>" (templates/forum/<tmpl>.html) when it has one, else the plugin's "forum/<tmpl>".
// A theme override that fails is logged and the plugin's template is used instead.
func (s *Server) forumExec(c *gin.Context, tmpl string, data any) string {
	name := "forum/" + strings.TrimPrefix(tmpl, "forum/")
	if th := s.forumTheme(c); th != nil && s.tmpl != nil {
		if set := s.tmpl.getTheme(true, th.Slug); set != nil && set.Lookup(name) != nil {
			var buf bytes.Buffer
			if err := set.ExecuteTemplate(&buf, name, data); err == nil {
				return buf.String()
			} else {
				log.Printf("forum: theme %s override %s: %v", th.Slug, name, err)
			}
		}
	}
	var buf bytes.Buffer
	if err := s.forumTemplates().ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("forum template %s: %v", name, err)
		lastRenderError.Store(err.Error())
		return "<!-- forum template error: " + html.EscapeString(name) + " -->"
	}
	return buf.String()
}

// forumNotFound is the theme's 404 page.
func (s *Server) forumNotFound(c *gin.Context) {
	c.Header("Cache-Control", "no-cache, must-revalidate, max-age=0")
	s.renderSite(c, &siteQuery{Kind: "404"})
}

// forumShortcodeTags are the tags forumShortcodes expands.
var forumShortcodeTags = []string{forumRenderTag, forumShortcodeTag}

// forumShortcodes swaps the render marker of a synthetic forum page for the view body, and expands
// [pursae_forum view="index"] (the forum list) and [pursae_forum view="recent" limit="5"] (recent
// topics) on normal pages. Nothing while the plugin is off.
func (s *Server) forumShortcodes(ctx context.Context, content string) string {
	if !s.forumOn() || !strings.Contains(content, "["+forumShortcodeTag) {
		return content
	}
	c := gooCtxGin(ctx)
	return wp.DoShortcode(content, forumShortcodeTags, func(tag string, attrs map[string]string, inner string) string {
		if tag == forumRenderTag {
			if v, ok := c.Get(forumCtxRender); ok {
				str, _ := v.(string)
				return str
			}
			return ""
		}
		forumCacheDep(c, ctx)
		view := strings.TrimSpace(attrs["view"])
		switch view {
		case "", "index":
			p := s.forumNewPage(c, "shortcode-index", "")
			list, err := s.forumList(ctx)
			if err != nil {
				log.Printf("forum shortcode: %v", err)
			}
			p.Forums = list
			p.Nonce, p.AuthNonce = "", "" // the page around it may be cached
			return s.forumExec(c, "shortcode-index", p)
		case "recent":
			limit, _ := strconv.Atoi(attrs["limit"])
			if limit <= 0 {
				limit = 5
			}
			if limit > 50 {
				limit = 50
			}
			p := s.forumNewPage(c, "shortcode-recent", "")
			topics, err := s.forumRecentTopics(ctx, limit) // skips topics of unpublished forums
			if err != nil {
				log.Printf("forum shortcode: %v", err)
			}
			p.Topics = topics
			p.Nonce, p.AuthNonce = "", ""
			return s.forumExec(c, "shortcode-recent", p)
		}
		return ""
	})
}

// forumCacheDep tags the page being rendered with the forum cache tag (a [pursae_forum] shortcode, a
// theme's forum_recent_topics): a forum write then purges it. The page is buffered until the
// template ran, so the header can still change.
func forumCacheDep(c *gin.Context, ctx context.Context) {
	cacheDep(ctx, forumCacheTag)
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	h := c.Writer.Header()
	cur := h.Get(cache.HeaderTags)
	for _, t := range strings.Split(cur, ",") {
		if t == forumCacheTag {
			return
		}
	}
	if cur == "" {
		h.Set(cache.HeaderTags, forumCacheTag)
		return
	}
	h.Set(cache.HeaderTags, cur+","+forumCacheTag)
}

// forumBodyClasses are the body classes of a forum view.
func (s *Server) forumBodyClasses(q *siteQuery) []string {
	if q == nil || q.Forum == "" {
		return nil
	}
	return []string{"pursae-forum", "pf-view-" + q.Forum}
}

// forumHead is the forum stylesheet: on forum views and on posts embedding [pursae_forum].
func (s *Server) forumHead(c *gin.Context, q *siteQuery) string {
	if q == nil || !s.forumOn() {
		return ""
	}
	if q.Forum == "" && (q.Post == nil || !strings.Contains(q.Post.Content, "["+forumShortcodeTag)) {
		return ""
	}
	return `<link rel="stylesheet" id="pursae-forum-css" href="` + forumAssetsPrefix + `/forum.css?ver=` + forumVersion + `" media="all" />` + "\n"
}

// forumPagination is the pager of a forum list (addition to the design, for forumPage.Pagination):
// <nav class="pf-pagination"> with previous / next and the page numbers around the current one.
// base is the unpaged URL (a query such as ?q= is kept); "" when there is one page.
func (s *Server) forumPagination(c *gin.Context, base string, current, total int) template.HTML {
	if total <= 1 {
		return ""
	}
	if current < 1 {
		current = 1
	}
	path, query, _ := strings.Cut(base, "?")
	if query != "" {
		query = "?" + query
	}
	path = strings.TrimSuffix(path, "/") + "/"
	pageURL := func(n int) string {
		if n <= 1 {
			return path + query
		}
		return path + "page/" + strconv.Itoa(n) + "/" + query
	}
	var ctx context.Context = context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	var b strings.Builder
	b.WriteString(`<nav class="pf-pagination" aria-label="` + html.EscapeString(s.forumT(ctx, "Pages")) + `">`)
	if current > 1 {
		b.WriteString(`<a class="pf-page pf-page--prev" rel="prev" href="` + html.EscapeString(pageURL(current-1)) + `">` + html.EscapeString(s.forumT(ctx, "Previous")) + `</a>`)
	}
	gap := false
	for n := 1; n <= total; n++ {
		if n != 1 && n != total && (n < current-2 || n > current+2) {
			if !gap {
				b.WriteString(`<span class="pf-page pf-page--gap">&hellip;</span>`)
				gap = true
			}
			continue
		}
		gap = false
		if n == current {
			b.WriteString(`<span class="pf-page is-current" aria-current="page">` + strconv.Itoa(n) + `</span>`)
		} else {
			b.WriteString(`<a class="pf-page" href="` + html.EscapeString(pageURL(n)) + `">` + strconv.Itoa(n) + `</a>`)
		}
	}
	if current < total {
		b.WriteString(`<a class="pf-page pf-page--next" rel="next" href="` + html.EscapeString(pageURL(current+1)) + `">` + html.EscapeString(s.forumT(ctx, "Next")) + `</a>`)
	}
	b.WriteString(`</nav>`)
	return template.HTML(b.String())
}
