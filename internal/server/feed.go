package server

import (
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/blocks"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

func cdata(s string) string {
	return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
}

// feed serves the RSS2 post and comment feeds (feed-rss2.php, feed-rss2-comments.php).
func (s *Server) feed(c *gin.Context, q *siteQuery) {
	s.cacheTags(c, q)
	ctx := c.Request.Context()
	lb := s.links(ctx)
	rc := &renderCtx{s: s, lb: lb}
	ctx = withRender(ctx, rc)
	site := html.EscapeString(s.settings.String("blogname"))
	var sb strings.Builder
	sb.WriteString(xml.Header)
	sb.WriteString(`<rss version="2.0"
	xmlns:content="http://purl.org/rss/1.0/modules/content/"
	xmlns:wfw="http://wellformedweb.org/CommentAPI/"
	xmlns:dc="http://purl.org/dc/elements/1.1/"
	xmlns:atom="http://www.w3.org/2005/Atom"
	xmlns:sy="http://purl.org/rss/1.0/modules/syndication/"
	xmlns:slash="http://purl.org/rss/1.0/modules/slash/"
	>
<channel>
`)
	self := lb.HomeURL(c.Request.URL.RequestURI())
	if q.Feed == "comments" {
		fmt.Fprintf(&sb, "\t<title>Comments for %s</title>\n", site)
		fmt.Fprintf(&sb, "\t<atom:link href=\"%s\" rel=\"self\" type=\"application/rss+xml\" />\n\t<link>%s</link>\n", html.EscapeString(self), html.EscapeString(lb.HomeURL("/")))
		fmt.Fprintf(&sb, "\t<description>%s</description>\n\t<lastBuildDate>%s</lastBuildDate>\n", html.EscapeString(s.settings.String("blogdescription")), time.Now().UTC().Format(time.RFC1123Z))
		sb.WriteString("\t<sy:updatePeriod>\n\thourly\t</sy:updatePeriod>\n\t<sy:updateFrequency>\n\t1\t</sy:updateFrequency>\n\t<generator>https://wordpress.org/?v=wpgo-" + Version + "</generator>\n")
		comments, _ := s.store.RecentComments(ctx, s.settings.Int("posts_per_rss"), false)
		for _, cm := range comments {
			p, err := s.store.GetPost(ctx, cm.PostID)
			if err != nil || p.Password != "" {
				continue
			}
			link := lb.Permalink(p) + "#comment-" + itoa64(cm.ID)
			fmt.Fprintf(&sb, "\t<item>\n\t\t<title>Comment on %s by %s</title>\n\t\t<link>%s</link>\n\t\t<dc:creator>%s</dc:creator>\n\t\t<pubDate>%s</pubDate>\n\t\t<guid isPermaLink=\"false\">%s</guid>\n\t\t<description>%s</description>\n\t\t<content:encoded>%s</content:encoded>\n\t</item>\n",
				html.EscapeString(p.Title), html.EscapeString(cm.AuthorName), html.EscapeString(link), cdata(cm.AuthorName), cm.CreatedAt.UTC().Format(time.RFC1123Z),
				html.EscapeString(lb.HomeURL("/?p="+itoa64(p.ID)+"#comment-"+itoa64(cm.ID))), cdata(wp.TrimWords(cm.Content, 55, " [&#8230;]")), cdata(commentText(cm.Content)))
		}
	} else {
		title := site
		if q.Term != nil {
			title += " &raquo; " + html.EscapeString(q.Term.Name)
		}
		fmt.Fprintf(&sb, "\t<title>%s</title>\n", title)
		fmt.Fprintf(&sb, "\t<atom:link href=\"%s\" rel=\"self\" type=\"application/rss+xml\" />\n\t<link>%s</link>\n", html.EscapeString(self), html.EscapeString(lb.HomeURL("/")))
		fmt.Fprintf(&sb, "\t<description>%s</description>\n\t<lastBuildDate>%s</lastBuildDate>\n\t<language>en-US</language>\n", html.EscapeString(s.settings.String("blogdescription")), time.Now().UTC().Format(time.RFC1123Z))
		sb.WriteString("\t<sy:updatePeriod>\n\thourly\t</sy:updatePeriod>\n\t<sy:updateFrequency>\n\t1\t</sy:updateFrequency>\n\t<generator>https://wordpress.org/?v=wpgo-" + Version + "</generator>\n")
		pq := store.PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, PerPage: s.settings.Int("posts_per_rss"), Loc: s.loc()}
		if q.Term != nil {
			pq.TermIDs = []int64{q.Term.ID}
		}
		if q.Author != nil {
			pq.AuthorID = q.Author.ID
		}
		posts, _, _ := s.store.QueryPosts(ctx, pq)
		for _, p := range posts {
			author, _ := s.store.UserByID(ctx, p.AuthorID)
			name := ""
			if author != nil {
				name = author.DisplayName
			}
			cats, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "category")
			tags, _ := s.store.TermsForPosts(ctx, []int64{p.ID}, "post_tag")
			link := lb.Permalink(p)
			fmt.Fprintf(&sb, "\t<item>\n\t\t<title>%s</title>\n\t\t<link>%s</link>\n\t\t<comments>%s#respond</comments>\n\t\t<dc:creator>%s</dc:creator>\n\t\t<pubDate>%s</pubDate>\n",
				html.EscapeString(p.Title), html.EscapeString(link), html.EscapeString(link), cdata(name), p.Date().UTC().Format(time.RFC1123Z))
			for _, t := range append(cats[p.ID], tags[p.ID]...) {
				fmt.Fprintf(&sb, "\t\t<category>%s</category>\n", cdata(t.Name))
			}
			content := p.Content
			if p.Password != "" {
				content = "There is no excerpt because this is a protected post."
			} else if blocks.HasBlocks(content) {
				content = blocks.Render(ctx, content)
			} else {
				content = wp.Autop(content)
			}
			excerpt := p.Excerpt
			if excerpt == "" {
				excerpt = wp.TrimWords(stripBlocks(p.Content), 55, " [&#8230;]")
			}
			fmt.Fprintf(&sb, "\t\t<guid isPermaLink=\"false\">%s</guid>\n\t\t<description>%s</description>\n",
				html.EscapeString(lb.HomeURL("/?p="+itoa64(p.ID))), cdata(excerpt))
			if !s.settings.Bool("rss_use_excerpt") {
				fmt.Fprintf(&sb, "\t\t<content:encoded>%s</content:encoded>\n", cdata(content))
			}
			fmt.Fprintf(&sb, "\t\t<wfw:commentRss>%s</wfw:commentRss>\n\t\t<slash:comments>%d</slash:comments>\n\t</item>\n",
				html.EscapeString(lb.PostCommentsFeed(p)), p.CommentCount)
		}
	}
	sb.WriteString("</channel>\n</rss>\n")
	c.Data(http.StatusOK, "application/rss+xml; charset=UTF-8", []byte(sb.String()))
}

func (s *Server) robots(c *gin.Context) {
	lb := s.links(c)
	var sb strings.Builder
	sb.WriteString("User-agent: *\n")
	if s.settings.Bool("blog_public") {
		sb.WriteString("Disallow: /wp-admin/\nAllow: /wp-admin/admin-ajax\n\nSitemap: " + lb.HomeURL("/wp-sitemap.xml") + "\n")
	} else {
		sb.WriteString("Disallow: /\n")
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(sb.String()))
}

// sitemapIndex is /wp-sitemap.xml (core sitemaps): posts, pages, taxonomies and users.
func (s *Server) sitemapIndex(c *gin.Context) {
	if !s.settings.Bool("blog_public") {
		c.Status(http.StatusNotFound)
		return
	}
	lb := s.links(c)
	var sb strings.Builder
	sb.WriteString(xml.Header + `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, part := range []string{"posts-post-1", "posts-page-1", "taxonomies-category-1", "taxonomies-post_tag-1", "users-1"} {
		fmt.Fprintf(&sb, "<sitemap><loc>%s</loc></sitemap>", html.EscapeString(lb.HomeURL("/wp-sitemap-"+part+".xml")))
	}
	sb.WriteString("</sitemapindex>\n")
	c.Data(http.StatusOK, "application/xml; charset=UTF-8", []byte(sb.String()))
}

func (s *Server) sitemapPart(c *gin.Context, part string) {
	if !s.settings.Bool("blog_public") {
		c.Status(http.StatusNotFound)
		return
	}
	ctx := c.Request.Context()
	lb := s.links(ctx)
	idx := strings.LastIndex(part, "-")
	if idx < 0 {
		c.Status(http.StatusNotFound)
		return
	}
	page, _ := strconv.Atoi(part[idx+1:])
	if page < 1 {
		page = 1
	}
	kind := part[:idx]
	var locs []string
	switch kind {
	case "posts-post", "posts-page":
		typ := strings.TrimPrefix(kind, "posts-")
		posts, _, _ := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{typ}, Statuses: []string{"publish"}, PerPage: 2000, Page: page, OrderBy: "id", Order: "asc"})
		for _, p := range posts {
			if p.Password == "" {
				locs = append(locs, lb.Permalink(p))
			}
		}
	case "taxonomies-category", "taxonomies-post_tag":
		terms, _, _ := s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: strings.TrimPrefix(kind, "taxonomies-"), HideEmpty: true, PerPage: 2000, Page: page})
		for _, t := range terms {
			locs = append(locs, lb.TermLink(t))
		}
	case "users":
		rows, err := s.store.Pool.Query(ctx, `SELECT DISTINCT u.id, u.nicename FROM users u JOIN posts p ON p.author_id = u.id
			WHERE p.type = 'post' AND p.status = 'publish' ORDER BY u.id`)
		if err == nil {
			for rows.Next() {
				var id int64
				var nicename string
				if rows.Scan(&id, &nicename) == nil {
					locs = append(locs, lb.AuthorLink(id, nicename))
				}
			}
			rows.Close()
		}
	default:
		c.Status(http.StatusNotFound)
		return
	}
	var sb strings.Builder
	sb.WriteString(xml.Header + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, l := range locs {
		fmt.Fprintf(&sb, "<url><loc>%s</loc></url>", html.EscapeString(l))
	}
	sb.WriteString("</urlset>\n")
	c.Data(http.StatusOK, "application/xml; charset=UTF-8", []byte(sb.String()))
}
