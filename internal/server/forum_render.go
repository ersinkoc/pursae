package server

import (
	"bytes"
	"context"
	"encoding/json"
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
	// v0.2.5.
	"liked":         "Thanks for the like.",
	"unliked":       "Like removed.",
	"solved":        "The answer is marked as the solution.",
	"unsolved":      "The solution mark is removed.",
	"subscribed":    "You will get an email when someone replies.",
	"unsubscribed":  "You no longer follow this topic.",
	"reported":      "Thanks. A moderator will look at it.",
	"draft_saved":   "Your draft is saved.",
	"draft_deleted": "Your draft is deleted.",
	"email_changed": "Your email address is changed. Check your inbox for the confirmation link.",
	"points_saved":  "The points are saved.",
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
	if st.Title == forumDefaults().Title { // the untouched default speaks the site's language
		st.Title = s.forumT(ctx, st.Title)
	} else { // an owner's own title: its Pursae Multi Language translation (Languages › Translations, group Pursae Forum)
		st.Title = s.seogoTr(ctx, st.Title)
	}
	if title == "" {
		title = st.Title
	}
	p := &forumPage{View: view, Title: title, OwnTitle: forumThemeSupports(s.forumTheme(c)), GoogleOn: s.forumGoogleReady(),
		RegisterOn: st.Registration, URLs: s.forumURLsFor(c), Form: map[string]string{}, Extra: map[string]any{},
		cat: s.gooFrontCatalog(ctx)}
	s.forumPageData(ctx, p)
	p.Crumbs = []forumCrumb{{Label: st.Title, URL: p.URLs.Index}}
	p.totals = sync.OnceValue(func() *forumStats {
		n, err := s.forumStatsOf(ctx)
		if err != nil {
			log.Printf("forum totals: %v", err)
			return &forumStats{}
		}
		return n
	})
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
	if out, ok := s.forumSiteExec(c, name, data); ok { // the site's own template (forum_site_templates.go)
		return out
	}
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
	out := ""
	if _, ok := forumSiteMain(c, q); !ok { // a site template that prints the whole main brings its own CSS
		out = `<link rel="stylesheet" id="pursae-forum-css" href="` + forumAssetsPrefix + `/forum.css?ver=` + forumVersion + `" media="all" />` + "\n"
	}
	if q.Forum == "" || c == nil || c.Request == nil {
		return out
	}
	// v0.2.5: forum.js and its configuration (the REST base, the topic and forum of the page; the
	// member's REST nonce only on member pages, which are never cached).
	cfg := map[string]any{"api": s.links(c.Request.Context()).HomeURL("/wp-json/" + forumRESTNamespace + "/")}
	{
		if v, ok := c.Get(forumCtxTopic); ok {
			cfg["topic"] = v
		}
		if v, ok := c.Get(forumCtxForum); ok {
			cfg["forum"] = v
		}
		if currentUser(c) != nil {
			cfg["nonce"] = s.nonce(c, "wp_rest")
		}
	}
	raw, _ := json.Marshal(cfg)
	out += `<script id="pf-config" type="application/json">` + strings.ReplaceAll(string(raw), "</", `<\/`) + `</script>` + "\n" +
		`<script` + cspNonceAttr() + ` src="` + forumAssetsPrefix + `/forum.js?ver=` + forumVersion + `" id="pursae-forum-js" defer></script>` + "\n"
	return out
}

// forumCtxForum is the gin key of the forum a page belongs to (forum.js marks the visitor there).
const forumCtxForum = "forum.front.forum"

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

// forumCtxSEO is the gin key of a public forum view's description (forumSetSEO).
const forumCtxSEO = "forum.front.seo"

// forumSEO is what a public view says about itself in the head.
type forumSEO struct{ Title, Description string }

// forumSetSEO records a view's title and description for the head (plain text; trimmed to ~160
// runes). Only public, canonical views print it.
func forumSetSEO(c *gin.Context, title, desc string) {
	desc = strings.Join(strings.Fields(desc), " ")
	if r := []rune(desc); len(r) > 160 {
		desc = strings.TrimSpace(string(r[:157])) + "…"
	}
	c.Set(forumCtxSEO, forumSEO{Title: title, Description: desc})
}

// forumSEOHead is the meta description and the Open Graph / Twitter tags of a public forum view
// (index, forum, topic, topic lists, profile): Pursae SEO does not handle synthetic forum pages.
func (s *Server) forumSEOHead(c *gin.Context, q *siteQuery) string {
	if c == nil || q == nil || q.ForumURL == "" {
		return ""
	}
	v, ok := c.Get(forumCtxSEO)
	if !ok {
		return ""
	}
	m, _ := v.(forumSEO)
	if m.Title == "" && m.Description == "" {
		return ""
	}
	u := q.ForumURL
	if q.Paged > 1 {
		u = strings.TrimSuffix(u, "/") + "/page/" + strconv.Itoa(q.Paged) + "/"
	}
	e := html.EscapeString
	var b strings.Builder
	if m.Description != "" {
		b.WriteString(`<meta name="description" content="` + e(m.Description) + `" />` + "\n")
	}
	ogType := "website"
	if q.Forum == forumViewTopic {
		ogType = "article"
	}
	b.WriteString(`<meta property="og:type" content="` + ogType + `" />` + "\n")
	b.WriteString(`<meta property="og:title" content="` + e(m.Title) + `" />` + "\n")
	if m.Description != "" {
		b.WriteString(`<meta property="og:description" content="` + e(m.Description) + `" />` + "\n")
	}
	b.WriteString(`<meta property="og:url" content="` + e(u) + `" />` + "\n")
	if img := s.forumSocialImage(c.Request.Context()); img != nil { // Pursae SEO's default social image
		b.WriteString(`<meta property="og:image" content="` + e(img.URL) + `" />` + "\n")
		if img.Width > 0 && img.Height > 0 {
			b.WriteString(`<meta property="og:image:width" content="` + strconv.Itoa(img.Width) + `" />` + "\n" +
				`<meta property="og:image:height" content="` + strconv.Itoa(img.Height) + `" />` + "\n")
		}
		if img.Mime != "" {
			b.WriteString(`<meta property="og:image:type" content="` + e(img.Mime) + `" />` + "\n")
		}
		if img.Alt != "" {
			b.WriteString(`<meta property="og:image:alt" content="` + e(img.Alt) + `" />` + "\n")
		}
	}
	if site := s.seogoTr(c.Request.Context(), s.settings.String("blogname")); site != "" {
		b.WriteString(`<meta property="og:site_name" content="` + e(site) + `" />` + "\n")
	}
	if img := s.forumSocialImage(c.Request.Context()); img != nil {
		b.WriteString(`<meta name="twitter:card" content="summary_large_image" />` + "\n" +
			`<meta name="twitter:image" content="` + e(img.URL) + `" />` + "\n")
	} else {
		b.WriteString(`<meta name="twitter:card" content="summary" />` + "\n")
	}
	return b.String()
}

// forumSocialImage is Pursae SEO's default social image (Titles & Meta › default image) while that
// plugin is on, with its size, type and alt text when it is a Media Library upload; nil otherwise.
func (s *Server) forumSocialImage(ctx context.Context) *seogoImage {
	if !s.seogoOn() {
		return nil
	}
	u := strings.TrimSpace(s.seogoOpts().DefaultImage)
	if u == "" {
		return nil
	}
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") {
		u = s.links(ctx).HomeURL(u)
	}
	img := &seogoImage{URL: u}
	if i := strings.Index(u, "/wp-content/uploads/"); i >= 0 {
		rel := u[i+len("/wp-content/uploads/"):]
		if q := strings.IndexAny(rel, "?#"); q >= 0 {
			rel = rel[:q]
		}
		var id int64
		if s.store.Pool.QueryRow(ctx, `SELECT id FROM media WHERE file_path = $1 ORDER BY id LIMIT 1`, rel).Scan(&id) == nil {
			if a := s.seogoAttachmentImage(ctx, id); a != nil {
				img.Width, img.Height, img.Mime, img.Alt = a.Width, a.Height, a.Mime, a.Alt
			}
		}
	}
	return img
}
