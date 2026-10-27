package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/wp"
)

//go:embed defaults/block-patterns.json
var blockPatternsJSON []byte

var patternsOnce sync.Once
var patternData struct {
	Patterns   []map[string]any `json:"patterns"`
	Categories []map[string]any `json:"categories"`
}

func loadPatterns() {
	patternsOnce.Do(func() { _ = json.Unmarshal(blockPatternsJSON, &patternData) })
}

// corePatterns are WordPress' bundled block patterns plus Twenty Sixteen's own.
func corePatterns() []map[string]any {
	loadPatterns()
	return patternData.Patterns
}

func corePatternCategories() []map[string]any {
	loadPatterns()
	return patternData.Categories
}

// oEmbed ----------------------------------------------------------------------

type oembedProvider struct {
	re       *regexp.Regexp
	endpoint string
}

// oembedProviders is a subset of WordPress' WP_oEmbed provider list.
var oembedProviders = []oembedProvider{
	{regexp.MustCompile(`^https?://((m|www)\.)?youtube\.com/(watch|playlist|shorts|live)`), "https://www.youtube.com/oembed"},
	{regexp.MustCompile(`^https?://youtu\.be/`), "https://www.youtube.com/oembed"},
	{regexp.MustCompile(`^https?://(.+\.)?vimeo\.com/`), "https://vimeo.com/api/oembed.json"},
	{regexp.MustCompile(`^https?://(www\.)?(twitter|x)\.com/\w{1,15}/status(es)?/`), "https://publish.twitter.com/oembed"},
	{regexp.MustCompile(`^https?://(www\.)?soundcloud\.com/`), "https://soundcloud.com/oembed"},
	{regexp.MustCompile(`^https?://(open|play)\.spotify\.com/`), "https://embed.spotify.com/oembed/"},
	{regexp.MustCompile(`^https?://(www\.)?flickr\.com/`), "https://www.flickr.com/services/oembed/"},
	{regexp.MustCompile(`^https?://(www\.)?(dailymotion\.com|dai\.ly)/`), "https://www.dailymotion.com/services/oembed"},
	{regexp.MustCompile(`^https?://(www\.)?tiktok\.com/`), "https://www.tiktok.com/oembed"},
	{regexp.MustCompile(`^https?://(www\.)?reddit\.com/r/`), "https://www.reddit.com/oembed"},
}

var oembedCache sync.Map // url -> cached response

type oembedEntry struct {
	data    map[string]any
	expires time.Time
}

// fetchOEmbed resolves a URL through its provider, caching results for a day like WordPress' oEmbed cache.
func fetchOEmbed(ctx context.Context, target string, maxWidth string) (map[string]any, error) {
	key := target + "|" + maxWidth
	if v, ok := oembedCache.Load(key); ok && v.(oembedEntry).expires.After(time.Now()) {
		return v.(oembedEntry).data, nil
	}
	var endpoint string
	for _, p := range oembedProviders {
		if p.re.MatchString(target) {
			endpoint = p.endpoint
			break
		}
	}
	if endpoint == "" {
		return nil, fmt.Errorf("no provider")
	}
	q := url.Values{"url": {target}, "format": {"json"}}
	if maxWidth != "" {
		q.Set("maxwidth", maxWidth)
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	req.Header.Set("User-Agent", "WordPress Go/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider status %d", resp.StatusCode)
	}
	var data map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return nil, err
	}
	oembedCache.Store(key, oembedEntry{data: data, expires: time.Now().Add(24 * time.Hour)})
	return data, nil
}

func (api *restAPI) oembedProxy(c *gin.Context, r *restRequest) {
	if restCurrent(c) == nil || !restCurrent(c).Can("edit_posts", nil) {
		restErr(c, http.StatusForbidden, "rest_forbidden", "Sorry, you are not allowed to make proxied oEmbed requests.")
		return
	}
	data, err := fetchOEmbed(c.Request.Context(), c.Query("url"), c.Query("maxwidth"))
	if err != nil {
		restErr(c, http.StatusNotFound, "oembed_invalid_url", "Not Found")
		return
	}
	restJSON(c, http.StatusOK, data)
}

// Comments --------------------------------------------------------------------

func (s *Server) restComment(c *gin.Context, cm *store.Comment) map[string]any {
	home := s.links(c).Home
	loc := s.loc()
	link := ""
	if p, err := s.store.GetPost(c, cm.PostID); err == nil {
		link = s.links(c).Permalink(p) + "#comment-" + itoa64(cm.ID)
	}
	status := cm.Status
	if status == "pending" {
		status = "hold"
	}
	return map[string]any{
		"id": cm.ID, "post": cm.PostID, "parent": cm.ParentID, "author": cm.UserID, "author_name": cm.AuthorName,
		"author_url": cm.AuthorURL, "date": cm.CreatedAt.In(loc).Format("2006-01-02T15:04:05"), "date_gmt": gmt(cm.CreatedAt),
		"content": map[string]any{"rendered": commentText(cm.Content)}, "link": link, "status": status, "type": cm.Type,
		"author_avatar_urls": map[string]string{"24": s.avatarURL(c, cm.AuthorEmail, 24), "48": s.avatarURL(c, cm.AuthorEmail, 48), "96": s.avatarURL(c, cm.AuthorEmail, 96)},
		"meta":               []any{},
		"_links":             restLinks(home+"/wp-json/wp/v2/comments/"+itoa64(cm.ID), home+"/wp-json/wp/v2/comments", nil),
	}
}

func (api *restAPI) registerComments() {
	api.register("wp/v2", "/wp/v2/comments", map[string]restHandler{
		http.MethodGet: func(c *gin.Context, r *restRequest) {
			s := api.s
			postID, _ := strconv.ParseInt(c.Query("post"), 10, 64)
			var list []*store.Comment
			if postID > 0 {
				p, err := s.store.GetPost(c, postID)
				if err != nil || !api.readable(c, p) {
					restJSON(c, http.StatusOK, []any{})
					return
				}
				list, _ = s.store.ApprovedComments(c, postID, c.DefaultQuery("order", "asc"), "")
			} else {
				list, _ = s.store.RecentComments(c, 100, false)
			}
			page, per := pageArgs(c)
			out := []map[string]any{}
			start := (page - 1) * per
			for i, cm := range list {
				if i < start || i >= start+per {
					continue
				}
				out = append(out, s.restComment(c, cm))
			}
			paginate(c, len(list), per, page)
			restJSON(c, http.StatusOK, out)
		},
		// Anonymous comments through the API are allowed only when the site allows them (rest_allow_anonymous_comments is off in WordPress by default; logged-in users may always comment).
		http.MethodPost: func(c *gin.Context, r *restRequest) {
			s := api.s
			u := restCurrent(c)
			if u == nil {
				restErr(c, http.StatusUnauthorized, "rest_comment_login_required", "Sorry, you must be logged in to comment.")
				return
			}
			postID, _ := r.Int(c, "post")
			p, err := s.store.GetPost(c, postID)
			if err != nil || p.Status != "publish" || p.CommentStatus != "open" {
				restErr(c, http.StatusForbidden, "rest_comment_closed", "Sorry, comments are closed for this item.")
				return
			}
			content, _ := r.Str(c, "content")
			if strings.TrimSpace(content) == "" {
				restErr(c, http.StatusBadRequest, "rest_comment_content_invalid", "Invalid comment content.")
				return
			}
			parent, _ := r.Int(c, "parent")
			cm := &store.Comment{PostID: p.ID, ParentID: parent, UserID: u.ID, AuthorName: u.DisplayName, AuthorEmail: u.Email,
				AuthorURL: u.URL, Content: wp.KsesPost(content), AuthorIP: clientIP(c), UserAgent: c.Request.UserAgent()}
			cm.Status = s.commentStatus(c, cm, u)
			if err := s.store.InsertComment(c, cm); err != nil {
				restErr(c, http.StatusInternalServerError, "rest_comment_failed_create", "Creating comment failed.")
				return
			}
			s.afterPostSave(c, p)
			restJSON(c, http.StatusCreated, s.restComment(c, cm))
		},
	}, nil)
}

// RegisterREST wires every route (called from routes()).
func (s *Server) restRoutes() *restAPI {
	api := &restAPI{s: s}
	api.registerPosts()
	api.registerTerms()
	api.registerUsers()
	api.registerMedia()
	api.registerComments()
	api.registerMisc()
	api.registerWidgets()
	return api
}
