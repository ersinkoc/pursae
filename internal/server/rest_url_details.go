package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// GET /wp-block-editor/v1/url-details is WP_REST_URL_Details_Controller: the link preview of the
// block editor's link control. The page is fetched from public addresses only (checked at connect
// time, after DNS and on every redirect: no SSRF through DNS rebinding or a redirect to the private
// network), on the ports wp_http_validate_url() allows (80, 443, 8080), with a timeout and a size cap
// (150 KB like limit_response_size), and its title, description, icon and image are read from the
// document head. Results are cached for an hour in a transient keyed by a hash of the URL.

// urlDetailsAllowPrivate lets tests fetch from httptest servers on the loopback address (any port).
var urlDetailsAllowPrivate atomic.Bool

const (
	urlDetailsMaxBody   = 150 << 10
	urlDetailsCacheTTL  = time.Hour
	urlDetailsTimeout   = 10 * time.Second
	urlDetailsRedirects = 5
)

// errURLDetailsRefused is the refusal of a URL that may not be fetched (private address, port, scheme).
var errURLDetailsRefused = errors.New("a valid URL was not provided")

var urlDetailsClient = &http.Client{
	Timeout: urlDetailsTimeout,
	// No Proxy: ProxyFromEnvironment would make the proxy the only address dialled, so publicOnly
	// would never see the real destination.
	Transport: &http.Transport{
		DialContext: (&net.Dialer{Timeout: urlDetailsTimeout,
			Control: publicOnly(urlDetailsAllowPrivate.Load, "the address points at a private network")}).DialContext,
		TLSHandshakeTimeout:    urlDetailsTimeout,
		ResponseHeaderTimeout:  urlDetailsTimeout,
		MaxResponseHeaderBytes: 64 << 10,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= urlDetailsRedirects {
			return errors.New("too many redirects")
		}
		if !urlDetailsFetchable(req.URL) {
			return errURLDetailsRefused
		}
		return nil
	},
}

// urlDetailsFetchable is wp_http_validate_url() without the DNS part (done at connect time): an
// http(s) URL with a host, without credentials, on port 80, 443 or 8080.
func urlDetailsFetchable(u *url.URL) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return false
	}
	if urlDetailsAllowPrivate.Load() {
		return true
	}
	switch u.Port() {
	case "", "80", "443", "8080":
		return true
	}
	return false
}

type urlDetails struct {
	Title       string `json:"title"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Image       string `json:"image"`
}

func (api *restAPI) urlDetails(c *gin.Context, r *restRequest) {
	u := restCurrent(c)
	if u == nil || !u.Can("edit_posts", nil) {
		forbidden(c, "rest_cannot_view_url_details", "Sorry, you are not allowed to process remote URLs.")
		return
	}
	raw, ok := c.GetQuery("url")
	if !ok {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): url", map[string]any{"params": []string{"url"}})
		return
	}
	raw = strings.TrimSpace(raw)
	pu, err := url.Parse(raw)
	if raw != "" && (err != nil || !urlDetailsFetchable(pu)) {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): url", map[string]any{"params": map[string]string{"url": "Invalid URL."}})
		return
	}
	target := strings.TrimRight(raw, `/\`) // untrailingslashit()
	if target == "" {
		restErr(c, http.StatusNotFound, "rest_invalid_url", "Invalid URL")
		return
	}
	sum := sha256.Sum256([]byte(target)) // WordPress keys it by md5( $url )
	key := "g_url_details_response_" + hex.EncodeToString(sum[:])
	var cached urlDetails
	if found, err := api.s.store.GetTransient(c, key, &cached); err == nil && found {
		restJSON(c, http.StatusOK, cached)
		return
	}
	d, status, code, msg := fetchURLDetails(c.Request.Context(), target, api.s.links(c).Home)
	if code != "" {
		restErr(c, status, code, msg)
		return
	}
	if err := api.s.store.SetTransient(c, key, d, urlDetailsCacheTTL); err != nil {
		slog.Warn("url-details: the result could not be cached", "err", err)
	}
	restJSON(c, http.StatusOK, d)
}

// fetchURLDetails downloads target and reads its details. On failure it returns the REST status,
// error code and message WordPress answers with.
func fetchURLDetails(ctx context.Context, target, home string) (d urlDetails, status int, code, msg string) {
	ctx, cancel := context.WithTimeout(ctx, urlDetailsTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil) //nolint:gosec // G704: fetching user-given URLs is the endpoint's job; urlDetailsClient refuses non-public addresses at connect time
	if err != nil {
		return d, http.StatusBadRequest, "rest_invalid_url", "Invalid URL"
	}
	req.Header.Set("User-Agent", "Pursae-URLDetails/"+Version+" (+"+home+")")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
	resp, err := urlDetailsClient.Do(req) //nolint:gosec // G704: see above (publicOnly dialer, port and scheme checks on every redirect)
	if err != nil {
		slog.Debug("url-details: fetch failed", "url", target, "err", err)
		// wp_safe_remote_get()'s WP_Error carries no status, so WordPress answers 500.
		return d, http.StatusInternalServerError, "http_request_failed", "A valid URL was not provided."
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return d, http.StatusNotFound, "no_response", "URL not found. Response returned a non-200 status code for this URL."
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, urlDetailsMaxBody))
	if err != nil && len(body) == 0 {
		return d, http.StatusInternalServerError, "http_request_failed", "A valid URL was not provided."
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return d, http.StatusNotFound, "no_content", "Unable to retrieve body from response at this URL."
	}
	ct := resp.Header.Get("Content-Type")
	if mt, _, _ := mime.ParseMediaType(ct); mt != "" && mt != "text/html" && mt != "application/xhtml+xml" {
		return d, 0, "", "" // not a web page: nothing to read, like WordPress' regexes finding nothing
	}
	return parseURLDetails(body, ct, resp.Request.URL), 0, "", ""
}

// parseURLDetails reads the document head: the first <title>, the first meta description
// (name="description" or og:description), the first og:image and the first rel="icon" link. URLs
// are made absolute against base (the final URL after redirects) and kept only when http(s).
func parseURLDetails(body []byte, contentType string, base *url.URL) urlDetails {
	var d urlDetails
	rd, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		rd = bytes.NewReader(body)
	}
	z := xhtml.NewTokenizer(rd)
	inTitle, haveTitle := false, false
	var title strings.Builder
	for {
		tt := z.Next()
		switch tt {
		case xhtml.ErrorToken:
			return finishURLDetails(d, title.String())
		case xhtml.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				if inTitle {
					inTitle, haveTitle = false, true
				}
			case "head":
				return finishURLDetails(d, title.String())
			}
		case xhtml.TextToken:
			if inTitle {
				title.Write(z.Text())
			}
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				key := strings.ToLower(string(k))
				if _, dup := attrs[key]; !dup {
					attrs[key] = string(v)
				}
			}
			switch string(name) {
			case "body":
				return finishURLDetails(d, title.String())
			case "title":
				if !haveTitle && tt == xhtml.StartTagToken {
					inTitle = true
				}
			case "meta":
				content, ok := attrs["content"]
				if !ok {
					break
				}
				nameAttr := strings.ToLower(strings.TrimSpace(attrs["name"]))
				prop := strings.ToLower(strings.TrimSpace(attrs["property"]))
				if d.Description == "" && (nameAttr == "description" || nameAttr == "og:description" || prop == "og:description") {
					d.Description = strings.TrimSpace(content)
				}
				if d.Image == "" && (prop == "og:image" || prop == "og:image:url" || nameAttr == "og:image") {
					d.Image = absoluteHTTPURL(base, content)
				}
			case "link":
				if d.Icon == "" && relHasIcon(attrs["rel"]) {
					d.Icon = absoluteHTTPURL(base, attrs["href"])
				}
			}
		}
	}
}

func finishURLDetails(d urlDetails, title string) urlDetails {
	d.Title = strings.Join(strings.Fields(title), " ")
	d.Description = strings.Join(strings.Fields(stripTagsSimple(d.Description)), " ")
	return d
}

// relHasIcon reports whether a rel attribute names an icon ("icon", "shortcut icon").
func relHasIcon(rel string) bool {
	for _, t := range strings.Fields(strings.ToLower(rel)) {
		if t == "icon" {
			return true
		}
	}
	return false
}

// absoluteHTTPURL resolves ref against base, keeping only http(s) results (no javascript:, data:).
func absoluteHTTPURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		r = base.ResolveReference(r)
	}
	if (r.Scheme != "http" && r.Scheme != "https") || r.Host == "" {
		return ""
	}
	return r.String()
}
