package server

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Front-end requests Pursae SEO answers itself: redirections, robots.txt, the XML sitemaps (which
// replace core's wp-sitemap.xml the way Yoast does), the IndexNow key file; plus the 404 monitor
// and the archives Search Appearance switches off.

// seogoIntercept runs first for every public GET/HEAD request; true means it answered.
func (s *Server) seogoIntercept(c *gin.Context, path string) bool {
	if !s.seogoOn() {
		return false
	}
	o := s.seogoOpts()
	if o.Modules["redirections"] && s.seogoRedirect(c) {
		return true
	}
	switch {
	case path == "robots.txt":
		s.seogoRobotsTxt(c)
		return true
	case o.IndexNowKey != "" && path == o.IndexNowKey+".txt" && o.Modules["indexnow"]:
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(o.IndexNowKey))
		return true
	}
	if !o.Modules["sitemap"] {
		return false
	}
	switch {
	case path == "sitemap_index.xml":
		s.seogoSitemapIndex(c)
		return true
	case path == "main-sitemap.xsl":
		c.Header("Cache-Control", "public, max-age=86400")
		c.Data(http.StatusOK, "text/xsl; charset=utf-8", []byte(seogoXSL))
		return true
	case path == "sitemap.xml" || path == "wp-sitemap.xml" || (strings.HasPrefix(path, "wp-sitemap") && strings.HasSuffix(path, ".xml")):
		c.Redirect(http.StatusMovedPermanently, s.links(c).HomeURL("/sitemap_index.xml"))
		return true
	case strings.HasSuffix(path, "-sitemap.xml") || seogoSitemapPagedRe.MatchString(path):
		return s.seogoSitemap(c, path)
	}
	return false
}

var seogoSitemapPagedRe = regexp.MustCompile(`^([a-z0-9_]+)-sitemap(\d+)\.xml$`)

// --- robots.txt --------------------------------------------------------------------------------

func (s *Server) seogoRobotsTxt(c *gin.Context) {
	o := s.seogoOpts()
	body := strings.TrimSpace(o.RobotsTxt)
	if body == "" {
		body = s.seogoRobotsDefault(c)
	}
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(body+"\n"))
}

func (s *Server) seogoRobotsDefault(ctx context.Context) string {
	lb := s.links(ctx)
	if !s.settings.Bool("blog_public") {
		return "User-agent: *\nDisallow: /"
	}
	out := "User-agent: *\nDisallow: /wp-admin/\nAllow: /wp-admin/admin-ajax"
	if s.seogoOpts().Modules["sitemap"] {
		out += "\n\nSitemap: " + lb.HomeURL("/sitemap_index.xml")
	}
	return out
}

// --- XML sitemaps ------------------------------------------------------------------------------

type seogoURL struct {
	Loc    string
	Mod    time.Time
	Images []string
	Post   *store.Post
}

// seogoSitemapTypes are the sitemaps of the index, in order.
func (s *Server) seogoSitemapTypes() []string {
	o := s.seogoOpts()
	var out []string
	for _, t := range []string{"post", "page", "category", "post_tag", "author"} {
		to := o.Types[t]
		if to.Sitemap && !to.Noindex && !to.Disabled {
			out = append(out, t)
		}
	}
	return out
}

func idSet(list string) map[int64]bool {
	out := map[int64]bool{}
	for _, f := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		if n, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64); err == nil {
			out[n] = true
		}
	}
	return out
}

// seogoIndexable reports whether a published post belongs in sitemaps (no noindex, no password,
// not excluded, no canonical elsewhere).
func (s *Server) seogoIndexable(ctx context.Context, p *store.Post, excluded map[int64]bool) bool {
	if p.Status != "publish" || p.Password != "" || excluded[p.ID] {
		return false
	}
	o := s.seogoOpts()
	switch seogoMeta(p.Meta, "noindex") {
	case "noindex":
		return false
	case "":
		if o.Types[p.Type].Noindex {
			return false
		}
	}
	if can := seogoMeta(p.Meta, "canonical"); can != "" && can != s.links(ctx).Permalink(p) {
		return false
	}
	return true
}

func postImages(s *Server, ctx context.Context, p *store.Post) []string {
	var out []string
	seen := map[string]bool{}
	if img := s.seogoAttachmentImage(ctx, p.FeaturedMediaID); img != nil {
		out = append(out, img.URL)
		seen[img.URL] = true
	}
	for _, m := range seogoAllImgRe.FindAllStringSubmatch(p.Content, -1) {
		u := html.UnescapeString(m[1])
		if strings.HasPrefix(u, "/") {
			u = s.links(ctx).HomeURL(u)
		}
		if !seen[u] && !strings.HasPrefix(u, "data:") {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

var seogoAllImgRe = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)

// seogoSitemapURLs lists every URL of one sitemap type.
func (s *Server) seogoSitemapURLs(ctx context.Context, typ string) []seogoURL {
	o := s.seogoOpts()
	lb := s.links(ctx)
	var out []seogoURL
	switch typ {
	case "post", "page":
		posts, _, _ := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{typ}, Statuses: []string{"publish"}, PerPage: 50000, Page: 1, OrderBy: "id", Order: "asc"})
		excluded := idSet(o.SitemapExclude)
		front := int64(0)
		if s.settings.String("show_on_front") == "page" {
			front = int64(s.settings.Int("page_on_front"))
		}
		if typ == "page" && front == 0 { // the home page leads the page sitemap, like Yoast
			out = append(out, seogoURL{Loc: lb.HomeURL("/")})
		}
		for _, p := range posts {
			if !s.seogoIndexable(ctx, p, excluded) {
				continue
			}
			u := seogoURL{Loc: lb.Permalink(p), Mod: p.ModifiedAt, Post: p}
			if p.ID == front {
				u.Loc = lb.HomeURL("/")
			}
			if o.SitemapImages {
				u.Images = postImages(s, ctx, p)
			}
			out = append(out, u)
		}
	case "category", "post_tag":
		terms, _, _ := s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: typ, HideEmpty: true, PerPage: 50000, Page: 1})
		excluded := idSet(o.SitemapExcludeT)
		for _, t := range terms {
			if excluded[t.ID] || seogoMeta(t.Meta, "noindex") == "noindex" {
				continue
			}
			u := seogoURL{Loc: lb.TermLink(t)}
			_ = s.store.Pool.QueryRow(ctx, `SELECT coalesce(max(p.modified_at), now()) FROM posts p JOIN post_terms pt ON pt.post_id = p.id
				WHERE pt.term_id = $1 AND p.status = 'publish'`, t.ID).Scan(&u.Mod)
			out = append(out, u)
		}
	case "author":
		rows, err := s.store.Pool.Query(ctx, `SELECT u.id, u.nicename, max(p.modified_at) FROM users u JOIN posts p ON p.author_id = u.id
			WHERE p.type = 'post' AND p.status = 'publish' GROUP BY u.id, u.nicename ORDER BY u.id`)
		if err == nil {
			for rows.Next() {
				var id int64
				var nicename string
				var mod time.Time
				if rows.Scan(&id, &nicename, &mod) == nil {
					out = append(out, seogoURL{Loc: lb.AuthorLink(id, nicename), Mod: mod})
				}
			}
			rows.Close()
		}
	}
	return out
}

func (s *Server) seogoXMLHead(ctx context.Context) string {
	return xml.Header + `<?xml-stylesheet type="text/xsl" href="` + xmlEsc(s.links(ctx).HomeURL("/main-sitemap.xsl")) + `"?>` + "\n"
}

func (s *Server) seogoSitemapIndex(c *gin.Context) {
	if !s.settings.Bool("blog_public") {
		c.Status(http.StatusNotFound)
		return
	}
	ctx := c.Request.Context()
	lb := s.links(ctx)
	o := s.seogoOpts()
	var sb strings.Builder
	sb.WriteString(s.seogoXMLHead(ctx) + `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, typ := range s.seogoSitemapTypes() {
		urls := s.seogoSitemapURLs(ctx, typ)
		if len(urls) == 0 {
			continue
		}
		pages := (len(urls) + o.SitemapPerPage - 1) / o.SitemapPerPage
		for n := 1; n <= pages; n++ {
			name := typ + "-sitemap.xml"
			if n > 1 {
				name = typ + "-sitemap" + strconv.Itoa(n) + ".xml"
			}
			chunk := urls[(n-1)*o.SitemapPerPage : min(n*o.SitemapPerPage, len(urls))]
			var last time.Time
			for _, u := range chunk {
				if u.Mod.After(last) {
					last = u.Mod
				}
			}
			fmt.Fprintf(&sb, "\t<sitemap>\n\t\t<loc>%s</loc>\n", xmlEsc(lb.HomeURL("/"+name)))
			if !last.IsZero() {
				fmt.Fprintf(&sb, "\t\t<lastmod>%s</lastmod>\n", last.UTC().Format(time.RFC3339))
			}
			sb.WriteString("\t</sitemap>\n")
		}
	}
	if o.Modules["news_sitemap"] {
		fmt.Fprintf(&sb, "\t<sitemap>\n\t\t<loc>%s</loc>\n\t</sitemap>\n", xmlEsc(lb.HomeURL("/news-sitemap.xml")))
	}
	if o.Modules["video_sitemap"] {
		fmt.Fprintf(&sb, "\t<sitemap>\n\t\t<loc>%s</loc>\n\t</sitemap>\n", xmlEsc(lb.HomeURL("/video-sitemap.xml")))
	}
	sb.WriteString("</sitemapindex>\n<!-- XML Sitemap generated by Pursae SEO -->\n")
	c.Header("X-Robots-Tag", "noindex, follow")
	c.Data(http.StatusOK, "text/xml; charset=UTF-8", []byte(sb.String()))
}

// seogoSitemap serves <type>-sitemap[N].xml, news-sitemap.xml and video-sitemap.xml.
func (s *Server) seogoSitemap(c *gin.Context, path string) bool {
	if !s.settings.Bool("blog_public") {
		return false
	}
	ctx := c.Request.Context()
	o := s.seogoOpts()
	typ, page := strings.TrimSuffix(path, "-sitemap.xml"), 1
	if m := seogoSitemapPagedRe.FindStringSubmatch(path); m != nil {
		typ = m[1]
		page, _ = strconv.Atoi(m[2])
	}
	switch typ {
	case "news":
		if !o.Modules["news_sitemap"] {
			return false
		}
		s.seogoNewsSitemap(c)
		return true
	case "video":
		if !o.Modules["video_sitemap"] {
			return false
		}
		s.seogoVideoSitemap(c)
		return true
	}
	allowed := false
	for _, t := range s.seogoSitemapTypes() {
		allowed = allowed || t == typ
	}
	if !allowed || page < 1 {
		return false
	}
	urls := s.seogoSitemapURLs(ctx, typ)
	from := (page - 1) * o.SitemapPerPage
	if from >= len(urls) {
		return false
	}
	urls = urls[from:min(from+o.SitemapPerPage, len(urls))]
	var sb strings.Builder
	sb.WriteString(s.seogoXMLHead(ctx) + `<urlset xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1" xsi:schemaLocation="http://www.sitemaps.org/schemas/sitemap/0.9 http://www.sitemaps.org/schemas/sitemap/0.9/sitemap.xsd http://www.google.com/schemas/sitemap-image/1.1 http://www.google.com/schemas/sitemap-image/1.1/sitemap-image.xsd" xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, u := range urls {
		fmt.Fprintf(&sb, "\t<url>\n\t\t<loc>%s</loc>\n", xmlEsc(u.Loc))
		if !u.Mod.IsZero() {
			fmt.Fprintf(&sb, "\t\t<lastmod>%s</lastmod>\n", u.Mod.UTC().Format(time.RFC3339))
		}
		for _, img := range u.Images {
			fmt.Fprintf(&sb, "\t\t<image:image>\n\t\t\t<image:loc>%s</image:loc>\n\t\t</image:image>\n", xmlEsc(img))
		}
		sb.WriteString("\t</url>\n")
	}
	sb.WriteString("</urlset>\n<!-- XML Sitemap generated by Pursae SEO -->\n")
	c.Header("X-Robots-Tag", "noindex, follow")
	c.Data(http.StatusOK, "text/xml; charset=UTF-8", []byte(sb.String()))
	return true
}

func (s *Server) seogoNewsSitemap(c *gin.Context) {
	ctx := c.Request.Context()
	o := s.seogoOpts()
	lb := s.links(ctx)
	name := o.SitemapNewsTypes
	if name == "" {
		name = s.settings.String("blogname")
	}
	posts, _, _ := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{"post"}, Statuses: []string{"publish"}, PerPage: 1000, Page: 1, OrderBy: "date", Order: "desc"})
	excluded := idSet(o.SitemapExclude)
	var sb strings.Builder
	sb.WriteString(s.seogoXMLHead(ctx) + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:news="http://www.google.com/schemas/sitemap-news/0.9">` + "\n")
	cutoff := time.Now().Add(-48 * time.Hour)
	for _, p := range posts {
		if p.PublishedAt == nil || p.PublishedAt.Before(cutoff) || !s.seogoIndexable(ctx, p, excluded) {
			continue
		}
		fmt.Fprintf(&sb, "\t<url>\n\t\t<loc>%s</loc>\n\t\t<news:news>\n\t\t\t<news:publication>\n\t\t\t\t<news:name>%s</news:name>\n\t\t\t\t<news:language>%s</news:language>\n\t\t\t</news:publication>\n\t\t\t<news:publication_date>%s</news:publication_date>\n\t\t\t<news:title>%s</news:title>\n\t\t</news:news>\n\t</url>\n",
			xmlEsc(lb.Permalink(p)), xmlEsc(name), strings.SplitN(s.seogoLocale(), "_", 2)[0], p.PublishedAt.UTC().Format(time.RFC3339), xmlEsc(p.Title))
	}
	sb.WriteString("</urlset>\n")
	c.Data(http.StatusOK, "text/xml; charset=UTF-8", []byte(sb.String()))
}

var (
	ytRe    = regexp.MustCompile(`(?i)(?:youtube\.com/(?:watch\?v=|embed/)|youtu\.be/)([A-Za-z0-9_-]{11})`)
	vimeoRe = regexp.MustCompile(`(?i)vimeo\.com/(?:video/)?(\d+)`)
)

func (s *Server) seogoVideoSitemap(c *gin.Context) {
	ctx := c.Request.Context()
	lb := s.links(ctx)
	posts, _, _ := s.store.QueryPosts(ctx, store.PostQuery{Types: []string{"post", "page"}, Statuses: []string{"publish"}, PerPage: 50000, Page: 1, OrderBy: "id", Order: "asc"})
	excluded := idSet(s.seogoOpts().SitemapExclude)
	var sb strings.Builder
	sb.WriteString(s.seogoXMLHead(ctx) + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:video="http://www.google.com/schemas/sitemap-video/1.1">` + "\n")
	for _, p := range posts {
		if !s.seogoIndexable(ctx, p, excluded) {
			continue
		}
		var vids []string
		for _, m := range ytRe.FindAllStringSubmatch(p.Content, -1) {
			vids = append(vids, fmt.Sprintf("\t\t<video:video>\n\t\t\t<video:thumbnail_loc>https://i.ytimg.com/vi/%s/hqdefault.jpg</video:thumbnail_loc>\n\t\t\t<video:title>%s</video:title>\n\t\t\t<video:description>%s</video:description>\n\t\t\t<video:player_loc>https://www.youtube.com/embed/%s</video:player_loc>\n\t\t</video:video>\n",
				m[1], xmlEsc(p.Title), xmlEsc(cut(plainText(p.Content), 2000)), m[1]))
		}
		for _, m := range vimeoRe.FindAllStringSubmatch(p.Content, -1) {
			thumb := ""
			if img := s.seogoAttachmentImage(ctx, p.FeaturedMediaID); img != nil {
				thumb = img.URL
			}
			vids = append(vids, fmt.Sprintf("\t\t<video:video>\n\t\t\t<video:thumbnail_loc>%s</video:thumbnail_loc>\n\t\t\t<video:title>%s</video:title>\n\t\t\t<video:description>%s</video:description>\n\t\t\t<video:player_loc>https://player.vimeo.com/video/%s</video:player_loc>\n\t\t</video:video>\n",
				xmlEsc(thumb), xmlEsc(p.Title), xmlEsc(cut(plainText(p.Content), 2000)), m[1]))
		}
		if len(vids) > 0 {
			fmt.Fprintf(&sb, "\t<url>\n\t\t<loc>%s</loc>\n%s\t</url>\n", xmlEsc(lb.Permalink(p)), strings.Join(vids, ""))
		}
	}
	sb.WriteString("</urlset>\n")
	c.Data(http.StatusOK, "text/xml; charset=UTF-8", []byte(sb.String()))
}

// --- Redirections ------------------------------------------------------------------------------

type seogoRedirectRow struct {
	ID        int64
	Source    string
	Target    string
	Code      int
	MatchType string
	Status    string
	Category  string
	Origin    string
	Hits      int64
	LastHit   *time.Time
	CreatedAt time.Time
	re        *regexp.Regexp
}

// seogoRedirectRows loads the redirects (all of them, or only the active ones).
func (s *Server) seogoRedirectRows(ctx context.Context, activeOnly bool) ([]*seogoRedirectRow, error) {
	q := `SELECT id, source, target, code, match_type, status, category, origin, hits, last_hit_at, created_at FROM redirects`
	if activeOnly {
		q += ` WHERE status = 'active'`
	}
	rows, err := s.store.Pool.Query(ctx, q+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*seogoRedirectRow
	for rows.Next() {
		r := &seogoRedirectRow{}
		var code int16
		if err := rows.Scan(&r.ID, &r.Source, &r.Target, &code, &r.MatchType, &r.Status, &r.Category, &r.Origin, &r.Hits, &r.LastHit, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Code = int(code)
		if r.MatchType == "regex" {
			r.re, _ = regexp.Compile(r.Source)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// seogoRedirectCache keeps the active redirects in memory; any change clears it.
type seogoRedirectCache struct {
	rows   []*seogoRedirectRow
	loaded bool
}

var seogoRedirects = struct {
	m map[*Server]*seogoRedirectCache
}{m: map[*Server]*seogoRedirectCache{}}

func (s *Server) seogoActiveRedirects(ctx context.Context) []*seogoRedirectRow {
	rt := s.seogoRT()
	rt.mu.Lock()
	cache := seogoRedirects.m[s]
	rt.mu.Unlock()
	if cache != nil && cache.loaded {
		return cache.rows
	}
	rows, err := s.seogoRedirectRows(ctx, true)
	if err != nil {
		return nil
	}
	rt.mu.Lock()
	seogoRedirects.m[s] = &seogoRedirectCache{rows: rows, loaded: true}
	rt.mu.Unlock()
	return rows
}

func (s *Server) seogoRedirectsChanged() {
	rt := s.seogoRT()
	rt.mu.Lock()
	delete(seogoRedirects.m, s)
	rt.mu.Unlock()
}

// normPath is a redirect source or request path without slashes at either end, decoded.
func normPath(p string) string {
	if u, err := url.Parse(p); err == nil && u.Path != "" && (u.Scheme != "" || strings.HasPrefix(p, "/")) {
		p = u.Path
		if u.RawQuery != "" {
			p += "?" + u.RawQuery
		}
	}
	return strings.Trim(p, "/")
}

// match reports whether a redirect applies to a request path (and the target with $n filled).
func (r *seogoRedirectRow) match(reqPath, reqWithQuery string) (string, bool) {
	src := normPath(r.Source)
	lp, lq := strings.ToLower(reqPath), strings.ToLower(reqWithQuery)
	ls := strings.ToLower(src)
	switch r.MatchType {
	case "regex":
		if r.re == nil {
			return "", false
		}
		for _, cand := range []string{"/" + reqWithQuery, reqWithQuery, "/" + reqPath, reqPath} {
			if m := r.re.FindStringSubmatchIndex(cand); m != nil {
				return string(r.re.ExpandString(nil, r.Target, cand, m)), true
			}
		}
		return "", false
	case "contains":
		return r.Target, ls != "" && strings.Contains(lq, ls)
	case "start":
		return r.Target, ls != "" && strings.HasPrefix(lq, ls)
	case "end":
		return r.Target, ls != "" && strings.HasSuffix(lq, ls)
	}
	return r.Target, ls == lp || ls == lq
}

// seogoRedirect answers a request a redirect matches.
func (s *Server) seogoRedirect(c *gin.Context) bool {
	reqPath := strings.Trim(c.Request.URL.Path, "/")
	withQuery := reqPath
	if c.Request.URL.RawQuery != "" {
		withQuery += "?" + c.Request.URL.RawQuery
	}
	for _, r := range s.seogoActiveRedirects(c.Request.Context()) {
		target, ok := r.match(reqPath, withQuery)
		if !ok {
			continue
		}
		s.asyncExec(`UPDATE redirects SET hits = hits + 1, last_hit_at = now() WHERE id = $1`, r.ID) // bounded queue (background.go)
		c.Header("X-Redirect-By", "Pursae SEO")
		c.Header("Cache-Control", "no-cache, must-revalidate, max-age=0")
		switch r.Code {
		case 410, 451:
			msg := map[int]string{410: "This content has been permanently removed.", 451: "This content is unavailable for legal reasons."}[r.Code]
			c.Data(r.Code, "text/html; charset=UTF-8", []byte(`<!DOCTYPE html><html lang="en-US"><head><meta charset="utf-8"><meta name="robots" content="noindex"><title>`+
				html.EscapeString(http.StatusText(r.Code)+" – "+s.settings.String("blogname"))+`</title></head><body><h1>`+http.StatusText(r.Code)+`</h1><p>`+msg+`</p></body></html>`))
		default:
			if !strings.Contains(target, "://") {
				target = s.links(c).HomeURL("/" + strings.TrimLeft(target, "/"))
			}
			code := r.Code
			if code != 302 && code != 307 && code != 308 {
				code = 301
			}
			c.Redirect(code, target)
		}
		return true
	}
	return false
}

// seogoAddRedirect stores a redirect (an existing source is updated).
func (s *Server) seogoAddRedirect(ctx context.Context, r *seogoRedirectRow) error {
	if r.MatchType == "" {
		r.MatchType = "exact"
	}
	if r.MatchType == "regex" {
		if _, err := regexp.Compile(r.Source); err != nil {
			return fmt.Errorf("invalid regular expression: %v", err)
		}
	} else {
		slash := strings.HasSuffix(r.Source, "/")
		r.Source = "/" + normPath(r.Source)
		if strings.Trim(r.Source, "/") == "" {
			return fmt.Errorf("the source URL is empty")
		}
		if slash && !strings.Contains(r.Source, "?") {
			r.Source += "/"
		}
	}
	if r.Code == 0 {
		r.Code = 301
	}
	if r.Status == "" {
		r.Status = "active"
	}
	if r.Origin == "" {
		r.Origin = "manual"
	}
	if r.Code != 410 && r.Code != 451 && strings.TrimSpace(r.Target) == "" {
		return fmt.Errorf("a destination URL is required for a %d redirect", r.Code)
	}
	var err error
	if r.ID > 0 {
		_, err = s.store.Pool.Exec(ctx, `UPDATE redirects SET source=$2, target=$3, code=$4, match_type=$5, status=$6, category=$7, updated_at=now() WHERE id=$1`,
			r.ID, r.Source, r.Target, r.Code, r.MatchType, r.Status, r.Category)
	} else {
		_, err = s.store.Pool.Exec(ctx, `INSERT INTO redirects (source, target, code, match_type, status, category, origin) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (source) DO UPDATE SET target=EXCLUDED.target, code=EXCLUDED.code, match_type=EXCLUDED.match_type, status=EXCLUDED.status, updated_at=now()`,
			r.Source, r.Target, r.Code, r.MatchType, r.Status, r.Category, r.Origin)
	}
	s.seogoRedirectsChanged()
	return err
}

// seogoAfterSave runs after a post is saved: the automatic redirect of a changed slug (Rank Math's
// "Auto Post Redirect", Yoast Premium's slug-change redirect) and IndexNow.
func (s *Server) seogoAfterSave(ctx context.Context, p *store.Post) {
	if !s.seogoOn() || p == nil {
		return
	}
	o := s.seogoOpts()
	if p.Status == "publish" && o.Modules["redirections"] && o.AutoRedirect {
		lb := s.links(ctx)
		pathOf := func(link string) string {
			if u, err := url.Parse(link); err == nil && u.Path != "" {
				return u.Path
			}
			return "/" + normPath(link)
		}
		newPath := pathOf(lb.Permalink(p))
		for _, old := range p.OldSlugs() {
			cp := *p
			cp.Slug = old
			src := pathOf(lb.Permalink(&cp))
			if normPath(src) == normPath(newPath) || strings.Trim(src, "/") == "" {
				continue
			}
			var exists bool
			_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM redirects WHERE trim(both '/' from source) = $1)`, normPath(src)).Scan(&exists)
			if !exists {
				_ = s.seogoAddRedirect(ctx, &seogoRedirectRow{Source: src, Target: newPath, Code: 301, Origin: "auto"})
			}
		}
		// A redirect away from the post's current address would hide it: drop it.
		if tag, _ := s.store.Pool.Exec(ctx, `DELETE FROM redirects WHERE trim(both '/' from source) = $1 AND match_type = 'exact'`, normPath(newPath)); tag.RowsAffected() > 0 {
			s.seogoRedirectsChanged()
		}
	}
	if p.Status == "publish" && o.Modules["indexnow"] {
		s.seogoIndexNow(ctx, []string{s.links(ctx).Permalink(p)})
	}
}

// --- 404 monitor -------------------------------------------------------------------------------

// seogo404 logs a 404 and applies the "redirect all 404s" fallback; true means it answered.
func (s *Server) seogo404(c *gin.Context) bool {
	if !s.seogoOn() {
		return false
	}
	o := s.seogoOpts()
	if o.Modules["monitor"] {
		uri := c.Request.URL.RequestURI()
		skip := false
		for _, ig := range strings.Split(o.MonitorIgnore, "\n") {
			if ig = strings.TrimSpace(ig); ig != "" && strings.Contains(uri, ig) {
				skip = true
			}
		}
		if !skip && len(uri) < 2000 {
			ref, ua := c.Request.Referer(), c.Request.UserAgent()
			// Bounded queue (background.go); the table is trimmed to MonitorLimit by the hourly
			// seogo_404_trim job, not per request.
			s.asyncExec(`INSERT INTO seogo_404 (uri, referer, user_agent) VALUES ($1, $2, $3)
				ON CONFLICT (uri) DO UPDATE SET hits = seogo_404.hits + 1, last_seen = now(), referer = EXCLUDED.referer, user_agent = EXCLUDED.user_agent`, uri, cut(ref, 500), cut(ua, 500))
		}
	}
	if o.Modules["redirections"] && strings.TrimSpace(o.Redirect404) != "" {
		c.Redirect(http.StatusMovedPermanently, strings.TrimSpace(o.Redirect404))
		return true
	}
	return false
}

// seogo404Trim keeps the 404 log at the monitor's limit (the hourly seogo_404_trim job).
func (s *Server) seogo404Trim(ctx context.Context) error {
	if !s.seogoOn() {
		return nil
	}
	o := s.seogoOpts()
	if o.MonitorLimit <= 0 {
		return nil
	}
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM seogo_404 WHERE id IN (SELECT id FROM seogo_404 ORDER BY last_seen DESC OFFSET $1)`, o.MonitorLimit)
	return err
}

// seogoQueryRedirect sends disabled author and date archives to the home page (Yoast's
// "Show author archives" / "Show date archives" switched off). True means it answered.
func (s *Server) seogoQueryRedirect(c *gin.Context, q *siteQuery) bool {
	if !s.seogoOn() || q == nil || q.Feed != "" {
		return false
	}
	o := s.seogoOpts()
	if (q.Kind == "author" && o.Types["author"].Disabled) || (q.Kind == "date" && o.Types["date"].Disabled) {
		c.Redirect(http.StatusMovedPermanently, s.links(c).HomeURL("/"))
		return true
	}
	return false
}

// --- RSS ---------------------------------------------------------------------------------------

// seogoFeedContent adds the RSS before/after texts to a feed item.
func (s *Server) seogoFeedContent(ctx context.Context, p *store.Post, content string) string {
	if !s.seogoOn() {
		return content
	}
	o := s.seogoOpts()
	if o.RSSBefore == "" && o.RSSAfter == "" {
		return content
	}
	lb := s.links(ctx)
	sc := &seogoCtx{s: s, ctx: ctx, lb: lb, Kind: "single", Post: p}
	fill := func(tpl string) string {
		tpl = yoastVars(tpl)
		r := strings.NewReplacer(
			"%postlink%", `<a href="`+html.EscapeString(lb.Permalink(p))+`">`+html.EscapeString(p.Title)+`</a>`,
			"%bloglink%", `<a href="`+html.EscapeString(lb.HomeURL("/"))+`">`+html.EscapeString(s.settings.String("blogname"))+`</a>`,
			"%blogdesclink%", `<a href="`+html.EscapeString(lb.HomeURL("/"))+`">`+html.EscapeString(s.settings.String("blogname")+" - "+s.settings.String("blogdescription"))+`</a>`,
			"%authorlink%", func() string {
				if a := sc.author(); a != nil {
					return `<a href="` + html.EscapeString(lb.AuthorLink(a.ID, a.Nicename)) + `">` + html.EscapeString(a.DisplayName) + `</a>`
				}
				return ""
			}(),
		)
		return "<p>" + sc.replace(r.Replace(tpl)) + "</p>"
	}
	if o.RSSBefore != "" {
		content = fill(o.RSSBefore) + content
	}
	if o.RSSAfter != "" {
		content += fill(o.RSSAfter)
	}
	return content
}

// --- IndexNow ----------------------------------------------------------------------------------

// seogoIndexNowEndpoint is the shared IndexNow API (tests replace it).
var seogoIndexNowEndpoint = "https://api.indexnow.org/indexnow"

type seogoIndexNowLog struct {
	Time   time.Time
	URLs   []string
	Status string
}

var seogoIndexNowHistory = struct {
	m map[*Server][]seogoIndexNowLog
}{m: map[*Server][]seogoIndexNowLog{}}

// seogoIndexNow submits URLs in the background and keeps the last 20 results.
func (s *Server) seogoIndexNow(ctx context.Context, urls []string) {
	o := s.seogoOpts()
	if o.IndexNowKey == "" || len(urls) == 0 || !s.settings.Bool("blog_public") {
		return
	}
	lb := s.links(ctx)
	home, _ := url.Parse(lb.HomeURL("/"))
	if home == nil || home.Hostname() == "localhost" || home.Hostname() == "127.0.0.1" {
		if seogoIndexNowEndpoint == "https://api.indexnow.org/indexnow" {
			s.seogoIndexNowRecord(urls, "skipped: local site")
			return
		}
	}
	body, _ := json.Marshal(map[string]any{"host": home.Host, "key": o.IndexNowKey, "keyLocation": lb.HomeURL("/" + o.IndexNowKey + ".txt"), "urlList": urls})
	endpoint := seogoIndexNowEndpoint
	go func() {
		cl := &http.Client{Timeout: 10 * time.Second}
		resp, err := cl.Post(endpoint, "application/json; charset=utf-8", bytes.NewReader(body))
		status := ""
		if err != nil {
			status = "error: " + err.Error()
		} else {
			status = resp.Status
			resp.Body.Close()
		}
		s.seogoIndexNowRecord(urls, status)
	}()
}

func (s *Server) seogoIndexNowRecord(urls []string, status string) {
	rt := s.seogoRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	h := append([]seogoIndexNowLog{{Time: time.Now(), URLs: urls, Status: status}}, seogoIndexNowHistory.m[s]...)
	if len(h) > 20 {
		h = h[:20]
	}
	seogoIndexNowHistory.m[s] = h
}

func (s *Server) seogoIndexNowLog() []seogoIndexNowLog {
	rt := s.seogoRT()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]seogoIndexNowLog{}, seogoIndexNowHistory.m[s]...)
}

// sortedKeys returns a map's keys in order (templates).
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// seogoXSL styles the sitemaps in a browser like Yoast's main-sitemap.xsl.
const seogoXSL = `<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="2.0" xmlns:html="http://www.w3.org/TR/REC-html40" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1" xmlns:sitemap="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xsl="http://www.w3.org/1999/XSL/Transform">
<xsl:output method="html" version="1.0" encoding="UTF-8" indent="yes"/>
<xsl:template match="/">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
<title>XML Sitemap</title>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
<style type="text/css">
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Oxygen-Sans,Ubuntu,Cantarell,"Helvetica Neue",sans-serif;color:#3c434a;margin:0}
#content{margin:0 auto;max-width:960px;padding:16px}
h1{font-size:24px;font-weight:400;margin:0 0 8px}
p.desc{color:#646970;margin:0 0 20px}
table{border:none;border-collapse:collapse;width:100%;font-size:13px}
th{background:#2271b1;color:#fff;text-align:left;padding:8px 12px;font-weight:600}
td{padding:8px 12px;border-bottom:1px solid #f0f0f1}
tr:nth-child(even) td{background:#f6f7f7}
a{color:#2271b1;text-decoration:none}a:hover{text-decoration:underline}
</style>
</head>
<body>
<div id="content">
<h1>XML Sitemap</h1>
<p class="desc">Generated by Pursae SEO. It is meant for search engines such as Google and Bing.</p>
<xsl:if test="count(sitemap:sitemapindex/sitemap:sitemap) &gt; 0">
<p class="desc">This XML Sitemap Index file contains <xsl:value-of select="count(sitemap:sitemapindex/sitemap:sitemap)"/> sitemaps.</p>
<table><tr><th>Sitemap</th><th>Last Modified</th></tr>
<xsl:for-each select="sitemap:sitemapindex/sitemap:sitemap">
<tr><td><a href="{sitemap:loc}"><xsl:value-of select="sitemap:loc"/></a></td><td><xsl:value-of select="sitemap:lastmod"/></td></tr>
</xsl:for-each>
</table>
</xsl:if>
<xsl:if test="count(sitemap:sitemapindex/sitemap:sitemap) &lt; 1">
<p class="desc">This XML Sitemap contains <xsl:value-of select="count(sitemap:urlset/sitemap:url)"/> URLs.</p>
<table><tr><th>URL</th><th>Images</th><th>Last Mod.</th></tr>
<xsl:for-each select="sitemap:urlset/sitemap:url">
<tr><td><a href="{sitemap:loc}"><xsl:value-of select="sitemap:loc"/></a></td><td><xsl:value-of select="count(image:image)"/></td><td><xsl:value-of select="sitemap:lastmod"/></td></tr>
</xsl:for-each>
</table>
</xsl:if>
</div>
</body>
</html>
</xsl:template>
</xsl:stylesheet>
`
