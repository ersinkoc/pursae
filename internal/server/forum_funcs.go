package server

import (
	"context"
	"html"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/wp"
)

// Theme template functions of Pursae Forum. They are merged into every theme's (and the admin's)
// function map (render.go funcs()) and into the plugin's own set, so each one is safe while the
// plugin is off: false, "" or empty HTML. They reach the request through gooViewCtx.

func (s *Server) forumTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"forum_on": func() bool { return s.forumOn() },
		"forum_url": func(v *siteView, view string) string {
			ctx, c := gooViewCtx(v)
			if !s.forumOn() {
				return ""
			}
			return s.forumViewURL(ctx, c, view)
		},
		"forum_user_menu": func(v *siteView) template.HTML {
			_, c := gooViewCtx(v)
			if !s.forumOn() || c == nil {
				return ""
			}
			return s.forumUserMenu(c)
		},
		"forum_recent_topics": func(v *siteView, n int) template.HTML {
			ctx, c := gooViewCtx(v)
			if !s.forumOn() {
				return ""
			}
			forumCacheDep(c, ctx)
			return s.forumRecentTopicsHTML(ctx, c, n)
		},
		"forum_stats": func(v *siteView) template.HTML {
			ctx, c := gooViewCtx(v)
			if !s.forumOn() {
				return ""
			}
			forumCacheDep(c, ctx)
			return s.forumStatsHTML(ctx, c)
		},
		"forum_is_view": func(v *siteView) bool {
			return s.forumOn() && v != nil && v.Q != nil && v.Q.Forum != ""
		},
		// Formatting helpers for the plugin's templates and theme overrides (additions to the design).
		"forum_format": func(content string) template.HTML { return forumFormat(content) },
		"forum_date": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return wp.FormatDate(t.In(s.loc()), s.settings.String("date_format")) + " " + wp.FormatDate(t.In(s.loc()), s.settings.String("time_format"))
		},
		"forum_iso": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.UTC().Format(time.RFC3339)
		},
		// WP7 rendering helpers (pure, safe while the plugin is off).
		"forum_icon":       func(name string, size int) template.HTML { return forumIcon(name, size) },
		"forum_icon_names": forumIconNames,
		"forum_badge":      func(b forumBadge) template.HTML { return forumBadgeHTML(b) },
		"forum_level":      func(l *forumLevel) template.HTML { return forumLevelHTML(l) },
		"forum_avatar":     func(a *forumAuthor, size int) template.HTML { return forumAvatarSized(a, size) },
		"forum_ago": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return wp.HumanTimeDiff(t, time.Now())
		},
	}
}

// forumAvatarSized is forumAvatarHTML with the size also on a photo as --pf-av-size, so the
// stylesheet sizes photos and initials alike (forum_avatar in templates).
func forumAvatarSized(a *forumAuthor, size int) template.HTML {
	h := string(forumAvatarHTML(a, size))
	if size <= 0 || size > 512 {
		size = 40
	}
	const img = `<img class="pf-avatar avatar" `
	if strings.HasPrefix(h, img) {
		h = img + `style="--pf-av-size:` + strconv.Itoa(size) + `px" ` + h[len(img):]
	}
	return template.HTML(h)
}

// forumViewURL is forum_url(): index, login, register, account, search, members (while the member
// list is on), or new (the new-topic form of the forum being viewed; the index elsewhere).
func (s *Server) forumViewURL(ctx context.Context, c *gin.Context, view string) string {
	switch view {
	case "login", "register", "account", "search":
		return s.forumURL(ctx, view)
	case "members":
		if s.forumLoad().ShowMembers {
			return s.forumURL(ctx, view)
		}
	case "new":
		if rt := forumRouteFrom(c); rt != nil && rt.View == forumViewForum && rt.Slug != "" {
			return s.forumURL(ctx, "f", rt.Slug, "new")
		}
	}
	return s.forumURL(ctx)
}

// forumT translates a front-end string into the request's language.
func (s *Server) forumT(ctx context.Context, msg string) string {
	return s.gooFrontCatalog(ctx).T(msg)
}

// forumUserMenu is forum_user_menu(): Log in and Join for visitors; avatar, name (the account page)
// and Log out for members.
func (s *Server) forumUserMenu(c *gin.Context) template.HTML {
	ctx := c.Request.Context()
	u := s.forumURLsFor(c)
	var b strings.Builder
	b.WriteString(`<nav class="pf-usermenu" aria-label="` + html.EscapeString(s.forumT(ctx, "Forum account")) + `">`)
	if cu := currentUser(c); cu != nil {
		a := s.forumAuthorOf(ctx, cu)
		b.WriteString(`<a class="pf-usermenu-account" href="` + html.EscapeString(u.Account) + `">` + string(forumAvatarSized(a, 28)) +
			`<span class="pf-usermenu-name">` + s.pllUC(ctx, html.EscapeString(a.DisplayName), 0) + `</span></a>`) // member text: never a string translation
		b.WriteString(` <a class="pf-usermenu-logout" href="` + html.EscapeString(u.Logout) + `">` + html.EscapeString(s.forumT(ctx, "Log out")) + `</a>`)
	} else {
		b.WriteString(`<a class="pf-usermenu-login" href="` + html.EscapeString(u.Login) + `">` + html.EscapeString(s.forumT(ctx, "Log in")) + `</a>`)
		if s.forumLoad().Registration {
			b.WriteString(` <a class="pf-usermenu-join pf-button pf-button--primary" href="` + html.EscapeString(u.Register) + `">` + html.EscapeString(s.forumT(ctx, "Join")) + `</a>`)
		}
	}
	b.WriteString(`</nav>`)
	return template.HTML(b.String())
}

// forumRecentTopicsHTML is forum_recent_topics(): the n most recently active published topics.
func (s *Server) forumRecentTopicsHTML(ctx context.Context, c *gin.Context, n int) template.HTML {
	if n <= 0 {
		n = 5
	}
	if n > 50 {
		n = 50
	}
	topics, err := s.forumRecentTopics(ctx, n) // published topics of published forums only
	if err != nil || len(topics) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<ul class="pf-recent">`)
	for _, t := range topics {
		// Titles are member and site content: Multi Language string capture and translation skip them.
		b.WriteString(`<li><a href="` + html.EscapeString(t.URL) + `">` + s.pllUC(ctx, html.EscapeString(t.Title), 0) + `</a>`)
		meta := []string{}
		if t.ForumTitle != "" {
			meta = append(meta, s.pllUC(ctx, html.EscapeString(t.ForumTitle), 0))
		}
		if !t.LastActive.IsZero() {
			meta = append(meta, `<time datetime="`+t.LastActive.UTC().Format(time.RFC3339)+`">`+html.EscapeString(forumTimeDiff(s.gooFrontCatalog(ctx), t.LastActive))+`</time>`)
		}
		if len(meta) > 0 {
			b.WriteString(` <span class="pf-recent-meta">` + strings.Join(meta, " &middot; ") + `</span>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	return template.HTML(b.String())
}

// forumStatsHTML is forum_stats(): the number of forums, topics, replies and members.
func (s *Server) forumStatsHTML(ctx context.Context, c *gin.Context) template.HTML {
	var forums, topics, replies, members int
	err := s.store.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM posts WHERE type = 'forum' AND status = 'publish' AND coalesce(meta->>'`+forumMetaKind+`','forum') <> 'category'),
		(SELECT count(*) FROM posts p WHERE p.type = 'topic' AND p.status = 'publish' AND `+forumNotSpam("p")+`),
		(SELECT count(*) FROM posts p WHERE p.type = 'reply' AND p.status = 'publish' AND `+forumNotSpam("p")+`),
		(SELECT count(*) FROM users)`).Scan(&forums, &topics, &replies, &members)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<dl class="pf-stats">`)
	for _, it := range []struct {
		Label string
		N     int
	}{{"Forums", forums}, {"Topics", topics}, {"Replies", replies}, {"Members", members}} {
		b.WriteString(`<div><dt>` + html.EscapeString(s.forumT(ctx, it.Label)) + `</dt><dd>` + strconv.Itoa(it.N) + `</dd></div>`)
	}
	b.WriteString(`</dl>`)
	return template.HTML(b.String())
}
