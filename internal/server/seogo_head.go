package server

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/links"
	"wpgo/internal/store"
)

// The <head> output of SEO-Go: title, meta description, robots, canonical, Open Graph, Twitter/X
// cards, webmaster verification and the JSON-LD graph (seogo_schema.go). While the plugin is on,
// wpHead prints this block instead of core's robots meta and canonical link.

// seogoTitle is the SEO title of a context (no HTML escaping).
func (sc *seogoCtx) title() string {
	o := sc.s.seogoOpts()
	tpl := ""
	switch {
	case sc.singular():
		tpl = seogoMeta(sc.Post.Meta, "title")
		if tpl == "" && sc.Kind == "front-page" {
			tpl = o.Types["home"].Title
		}
	case sc.Term != nil:
		tpl = seogoMeta(sc.Term.Meta, "title")
	}
	if tpl == "" {
		tpl = o.Types[sc.typeKey()].Title
	}
	if tpl == "" {
		tpl = "%title% %page% %sep% %sitename%"
	}
	return sc.replace(tpl)
}

func (sc *seogoCtx) description() string {
	o := sc.s.seogoOpts()
	tpl := ""
	switch {
	case sc.singular():
		tpl = seogoMeta(sc.Post.Meta, "description")
		if tpl == "" && sc.Kind == "front-page" {
			tpl = o.Types["home"].Description
		}
	case sc.Term != nil:
		tpl = seogoMeta(sc.Term.Meta, "description")
	}
	if tpl == "" {
		tpl = o.Types[sc.typeKey()].Description
	}
	return cut(sc.replace(tpl), 320)
}

// robots returns the robots directives, index/noindex and follow/nofollow first.
func (sc *seogoCtx) robots(preview bool) []string {
	s := sc.s
	o := s.seogoOpts()
	if !s.settings.Bool("blog_public") || preview {
		return []string{"noindex", "nofollow"}
	}
	noindex := o.Types[sc.typeKey()].Noindex
	nofollow := false
	adv := ""
	var meta map[string]any
	switch {
	case sc.singular():
		meta = sc.Post.Meta
		if sc.Post.Status != "publish" {
			noindex = true
		}
	case sc.Term != nil:
		meta = sc.Term.Meta
		if o.NoindexEmptyTerms && sc.Term.Count == 0 {
			noindex = true
		}
	}
	if meta != nil {
		switch seogoMeta(meta, "noindex") {
		case "noindex":
			noindex = true
		case "index":
			noindex = false
		}
		nofollow = seogoMeta(meta, "nofollow") == "1"
		adv = seogoMeta(meta, "robots_adv")
	}
	if o.NoindexPaged && sc.Paged > 1 {
		noindex = true
	}
	if sc.Kind == "404" || sc.Kind == "search" {
		noindex = true
	}
	out := []string{map[bool]string{true: "noindex", false: "index"}[noindex], map[bool]string{true: "nofollow", false: "follow"}[nofollow]}
	for _, d := range strings.Split(adv, ",") {
		if d = strings.TrimSpace(d); d == "noarchive" || d == "nosnippet" || d == "noimageindex" {
			out = append(out, d)
		}
	}
	if !noindex {
		limits := map[string]string{}
		for _, d := range strings.Split(o.RobotsDefaults, ",") {
			if k, v, ok := strings.Cut(strings.TrimSpace(d), ":"); ok {
				limits[k] = v
			}
		}
		if meta != nil {
			for _, f := range []string{"max_snippet", "max_image_preview", "max_video_preview"} {
				if k, v := strings.ReplaceAll(f, "_", "-"), seogoMeta(meta, f); v != "" && seogoRobotsLimitOK(k, v) {
					limits[k] = v // an invalid value keeps the site default
				}
			}
		}
		for _, k := range []string{"max-snippet", "max-image-preview", "max-video-preview"} {
			if v, ok := limits[k]; ok && seogoRobotsLimitOK(k, v) {
				out = append(out, k+":"+v)
			}
		}
	}
	return out
}

// url is the canonical URL of the context ("" for search results and 404s).
func (sc *seogoCtx) url() string {
	lb := sc.lb
	switch sc.Kind {
	case "single", "page":
		u := lb.Permalink(sc.Post)
		if sc.Page > 1 {
			u = lb.PostPageLink(sc.Post, sc.Page)
		}
		return u
	case "front-page":
		return lb.HomeURL("/")
	case "home":
		base := lb.HomeURL("/")
		if sc.Post != nil {
			base = lb.Permalink(sc.Post)
		}
		if sc.Paged > 1 {
			return lb.Paged(base, sc.Paged)
		}
		return base
	case "category", "tag":
		if sc.Paged > 1 {
			return lb.Paged(lb.TermLink(sc.Term), sc.Paged)
		}
		return lb.TermLink(sc.Term)
	case "author":
		if a := sc.author(); a != nil {
			u := lb.AuthorLink(a.ID, a.Nicename)
			if sc.Paged > 1 {
				return lb.Paged(u, sc.Paged)
			}
			return u
		}
	case "date":
		var u string
		switch {
		case sc.Day != 0:
			u = lb.DayLink(sc.Year, sc.Month, sc.Day)
		case sc.Month != 0:
			u = lb.MonthLink(sc.Year, sc.Month)
		default:
			u = lb.YearLink(sc.Year)
		}
		if sc.Paged > 1 {
			return lb.Paged(u, sc.Paged)
		}
		return u
	}
	return ""
}

func (sc *seogoCtx) canonical() string {
	if sc.singular() {
		if v := seogoMeta(sc.Post.Meta, "canonical"); v != "" {
			return v
		}
	}
	if sc.Term != nil {
		if v := seogoMeta(sc.Term.Meta, "canonical"); v != "" {
			return v
		}
	}
	return sc.url()
}

// seogoImage is an image for social cards and the schema graph.
type seogoImage struct {
	URL           string
	Width, Height int
	Mime, Alt     string
	ID            int64
}

var seogoImgSrcRe = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["']`)

// image picks the social image: the field's own value, the featured image, the first image of the
// content, the site default.
func (sc *seogoCtx) image(field string) *seogoImage {
	s := sc.s
	if sc.singular() || sc.Term != nil {
		meta := map[string]any(nil)
		if sc.Post != nil && sc.singular() {
			meta = sc.Post.Meta
		} else if sc.Term != nil {
			meta = sc.Term.Meta
		}
		if field != "" {
			if v := seogoMeta(meta, field); v != "" {
				return &seogoImage{URL: v}
			}
		}
		if v := seogoMeta(meta, "og_image"); v != "" && field == "tw_image" {
			return &seogoImage{URL: v}
		}
	}
	if sc.Post != nil && sc.singular() {
		if img := s.seogoAttachmentImage(sc.ctx, sc.Post.FeaturedMediaID); img != nil {
			return img
		}
		if m := seogoImgSrcRe.FindStringSubmatch(sc.Post.Content); m != nil {
			return &seogoImage{URL: html.UnescapeString(m[1])}
		}
	}
	o := s.seogoOpts()
	if (sc.Kind == "front-page" || (sc.Kind == "home" && sc.Post == nil)) && o.HomeOGImage != "" {
		return &seogoImage{URL: o.HomeOGImage}
	}
	if o.DefaultImage != "" {
		return &seogoImage{URL: o.DefaultImage}
	}
	return nil
}

func (s *Server) seogoAttachmentImage(ctx context.Context, id int64) *seogoImage {
	if id <= 0 {
		return nil
	}
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil || !strings.HasPrefix(a.Mime, "image/") {
		return nil
	}
	return &seogoImage{URL: s.uploadURL(ctx, a.FilePath), Width: a.Width, Height: a.Height, Mime: a.Mime, Alt: a.Alt, ID: a.ID}
}

// readingTime is the estimated reading time in minutes (Yoast's "Est. reading time", 200 wpm).
func readingTime(text string) int {
	n := len(strings.Fields(text))
	m := (n + 199) / 200
	if m < 1 {
		m = 1
	}
	return m
}

func (s *Server) seogoLocale() string {
	if l := s.settings.String("WPLANG"); l != "" {
		return l
	}
	return "en_US"
}

// seogoHead is the SEO-Go block of wp_head (empty while the plugin is off).
func (s *Server) seogoHead(c *gin.Context, q *siteQuery, lb *links.Builder) string {
	if !s.seogoOn() || q == nil {
		return ""
	}
	sc := s.seogoCtxFor(c.Request.Context(), q)
	sc.lb = lb
	if p := sc.Post; p != nil && p.Password != "" && !currentUser(c).Can("edit_post", p) {
		// The head is public and cacheable: nothing in it (schema, image, reading time) may come from the body
		// of a password-protected post.
		cp := *p
		cp.Content, cp.Excerpt = "", ""
		sc.Post, sc.Pages = &cp, 1
	}
	return s.seogoHeadFor(sc, q.Preview)
}

// seogoRobotsLimitOK accepts the values the robots limits can have: a number of characters / seconds
// (-1 = no limit) or an image preview size. Anything else (the fields are free text in post meta) is
// dropped instead of reaching the robots tag.
func seogoRobotsLimitOK(k, v string) bool {
	if k == "max-image-preview" {
		return v == "none" || v == "standard" || v == "large"
	}
	n, err := strconv.Atoi(v)
	return err == nil && n >= -1
}

// seogoHeadFor renders the head block of a context (also the REST get_head endpoint).
func (s *Server) seogoHeadFor(sc *seogoCtx, preview bool) string {
	o := s.seogoOpts()
	esc := html.EscapeString
	var sb strings.Builder
	sb.WriteString("<!-- This site is optimized with SEO-Go " + "1.0.0" + " - Pursae -->\n")
	title := sc.title()
	desc := sc.description()
	if desc != "" {
		fmt.Fprintf(&sb, "<meta name=\"description\" content=\"%s\" />\n", esc(desc))
	}
	robots := sc.robots(preview)
	fmt.Fprintf(&sb, "<meta name=\"robots\" content=\"%s\" />\n", esc(strings.Join(robots, ", ")))
	noindex := robots[0] == "noindex"
	canonical := sc.canonical()
	if canonical != "" && !noindex {
		fmt.Fprintf(&sb, "<link rel=\"canonical\" href=\"%s\" />\n", esc(canonical))
	}
	// rel prev/next on paginated archives
	if (!sc.singular() || sc.Kind == "home") && sc.Paged > 1 {
		paged := sc.Paged
		sc.Paged--
		if prev := sc.url(); prev != "" {
			fmt.Fprintf(&sb, "<link rel=\"prev\" href=\"%s\" />\n", esc(prev))
		}
		sc.Paged = paged
	}
	url := sc.url()
	if canonical != "" {
		url = canonical
	}
	if o.OpenGraph {
		fmt.Fprintf(&sb, "<meta property=\"og:locale\" content=\"%s\" />\n", esc(s.seogoLocale()))
		typ := "website"
		if sc.singular() && sc.Kind != "front-page" && sc.Kind != "home" && sc.Post.Type == "post" {
			typ = "article"
		} else if sc.Kind == "author" {
			typ = "profile"
		}
		fmt.Fprintf(&sb, "<meta property=\"og:type\" content=\"%s\" />\n", typ)
		ogTitle, ogDesc := title, desc
		if sc.singular() {
			if v := seogoMeta(sc.Post.Meta, "og_title"); v != "" {
				ogTitle = sc.replace(v)
			}
			if v := seogoMeta(sc.Post.Meta, "og_description"); v != "" {
				ogDesc = sc.replace(v)
			}
		}
		fmt.Fprintf(&sb, "<meta property=\"og:title\" content=\"%s\" />\n", esc(ogTitle))
		if ogDesc != "" {
			fmt.Fprintf(&sb, "<meta property=\"og:description\" content=\"%s\" />\n", esc(ogDesc))
		}
		if url != "" {
			fmt.Fprintf(&sb, "<meta property=\"og:url\" content=\"%s\" />\n", esc(url))
		}
		fmt.Fprintf(&sb, "<meta property=\"og:site_name\" content=\"%s\" />\n", esc(s.settings.String("blogname")))
		if typ == "article" {
			if o.FacebookURL != "" {
				fmt.Fprintf(&sb, "<meta property=\"article:publisher\" content=\"%s\" />\n", esc(o.FacebookURL))
			}
			if sc.Post.PublishedAt != nil {
				fmt.Fprintf(&sb, "<meta property=\"article:published_time\" content=\"%s\" />\n", sc.Post.PublishedAt.UTC().Format(time.RFC3339))
			}
			if sc.Post.PublishedAt == nil || sc.Post.ModifiedAt.After(*sc.Post.PublishedAt) {
				fmt.Fprintf(&sb, "<meta property=\"article:modified_time\" content=\"%s\" />\n", sc.Post.ModifiedAt.UTC().Format(time.RFC3339))
			}
		}
		if img := sc.image("og_image"); img != nil {
			fmt.Fprintf(&sb, "<meta property=\"og:image\" content=\"%s\" />\n", esc(img.URL))
			if img.Width > 0 && img.Height > 0 {
				fmt.Fprintf(&sb, "<meta property=\"og:image:width\" content=\"%d\" />\n<meta property=\"og:image:height\" content=\"%d\" />\n", img.Width, img.Height)
			}
			if img.Mime != "" {
				fmt.Fprintf(&sb, "<meta property=\"og:image:type\" content=\"%s\" />\n", esc(img.Mime))
			}
		}
		if o.FacebookAppID != "" {
			fmt.Fprintf(&sb, "<meta property=\"fb:app_id\" content=\"%s\" />\n", esc(o.FacebookAppID))
		}
	}
	if o.TwitterCards {
		card := o.TwitterCard
		if card == "" {
			card = "summary_large_image"
		}
		fmt.Fprintf(&sb, "<meta name=\"twitter:card\" content=\"%s\" />\n", esc(card))
		if sc.singular() {
			if v := seogoMeta(sc.Post.Meta, "tw_title"); v != "" {
				fmt.Fprintf(&sb, "<meta name=\"twitter:title\" content=\"%s\" />\n", esc(sc.replace(v)))
			} else if !o.OpenGraph {
				fmt.Fprintf(&sb, "<meta name=\"twitter:title\" content=\"%s\" />\n", esc(title))
			}
			if v := seogoMeta(sc.Post.Meta, "tw_description"); v != "" {
				fmt.Fprintf(&sb, "<meta name=\"twitter:description\" content=\"%s\" />\n", esc(sc.replace(v)))
			}
			if v := seogoMeta(sc.Post.Meta, "tw_image"); v != "" {
				fmt.Fprintf(&sb, "<meta name=\"twitter:image\" content=\"%s\" />\n", esc(v))
			}
		}
		if u := strings.TrimPrefix(o.TwitterUser, "@"); u != "" {
			fmt.Fprintf(&sb, "<meta name=\"twitter:site\" content=\"@%s\" />\n", esc(u))
		}
		if sc.singular() && sc.Post.Type == "post" && sc.Kind == "single" {
			if a := sc.author(); a != nil {
				fmt.Fprintf(&sb, "<meta name=\"twitter:label1\" content=\"Written by\" />\n<meta name=\"twitter:data1\" content=\"%s\" />\n", esc(a.DisplayName))
			}
			mins := readingTime(plainText(sc.Post.Content))
			fmt.Fprintf(&sb, "<meta name=\"twitter:label2\" content=\"Est. reading time\" />\n<meta name=\"twitter:data2\" content=\"%d minute%s\" />\n", mins, map[bool]string{true: "", false: "s"}[mins == 1])
		}
	}
	if sc.Kind == "front-page" || (sc.Kind == "home" && sc.Paged <= 1) {
		for _, v := range []struct{ key, name string }{{"google", "google-site-verification"}, {"bing", "msvalidate.01"},
			{"baidu", "baidu-site-verification"}, {"yandex", "yandex-verification"}, {"pinterest", "p:domain_verify"}, {"naver", "naver-site-verification"}} {
			if code := strings.TrimSpace(o.Verify[v.key]); code != "" {
				fmt.Fprintf(&sb, "<meta name=\"%s\" content=\"%s\" />\n", v.name, esc(seogoVerifyCode(code)))
			}
		}
	}
	if o.Modules["schema"] && sc.Kind != "404" {
		sb.WriteString(s.seogoSchemaScript(sc, title, desc))
	}
	sb.WriteString("<!-- / SEO-Go -->\n")
	return sb.String()
}

// seogoVerifyCode accepts either the bare code or the whole <meta> tag a search console shows.
func seogoVerifyCode(v string) string {
	if m := regexp.MustCompile(`content=["']([^"']+)["']`).FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return v
}

// seogoDocumentTitle replaces wp_get_document_title() while the plugin is on ("" otherwise).
func (s *Server) seogoDocumentTitle(c *gin.Context, q *siteQuery) template.HTML {
	if !s.seogoOn() || q == nil {
		return ""
	}
	t := s.seogoCtxFor(c.Request.Context(), q).title()
	if t == "" {
		return ""
	}
	return template.HTML(html.EscapeString(t))
}

// seogoQueryForPost is the main query a post would have on the front end (REST, previews).
func (s *Server) seogoQueryForPost(p *store.Post) *siteQuery {
	q := &siteQuery{Kind: "single", Post: p, Paged: 1}
	if p.Type == "page" {
		q.Kind = "page"
		if s.settings.String("show_on_front") == "page" && int64(s.settings.Int("page_on_front")) == p.ID {
			q.Kind = "front-page"
		}
	}
	return q
}

func itoaS(n int64) string { return strconv.FormatInt(n, 10) }
