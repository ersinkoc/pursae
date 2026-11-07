package server

import (
	"container/list"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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

// corePatterns are WordPress' bundled block patterns.
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

// oembedCache keeps provider responses for a day, bounded so editors pasting many URLs (each URL and
// maxwidth is a key) cannot grow memory without limit: the least recently used entry goes first.
var oembedCache = newTTLCache[map[string]any](1000)

// ttlCache is a small mutex-guarded LRU whose entries also expire.
type ttlCache[V any] struct {
	mu    sync.Mutex
	max   int
	ll    *list.List // front = most recently used
	items map[string]*list.Element
}

type ttlEntry[V any] struct {
	key     string
	val     V
	expires time.Time
}

func newTTLCache[V any](max int) *ttlCache[V] {
	return &ttlCache[V]{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *ttlCache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*ttlEntry[V])
	if !e.expires.After(time.Now()) {
		c.ll.Remove(el)
		delete(c.items, key)
		return zero, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

func (c *ttlCache[V]) Set(key string, val V, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*ttlEntry[V])
		e.val, e.expires = val, time.Now().Add(ttl)
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&ttlEntry[V]{key: key, val: val, expires: time.Now().Add(ttl)})
	for c.ll.Len() > c.max {
		old := c.ll.Back()
		c.ll.Remove(old)
		delete(c.items, old.Value.(*ttlEntry[V]).key)
	}
}

func (c *ttlCache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// fetchOEmbed resolves a URL through its provider, caching results for a day like WordPress' oEmbed cache.
func fetchOEmbed(ctx context.Context, target string, maxWidth string) (map[string]any, error) {
	key := target + "|" + maxWidth
	if v, ok := oembedCache.Get(key); ok {
		return v, nil
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
	req.Header.Set("User-Agent", "Pursae/"+Version)
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
	oembedCache.Set(key, data, 24*time.Hour)
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
	api.registerAppPasswords()
	api.registerMenus()
	api.registerOEmbed()
	return api
}
