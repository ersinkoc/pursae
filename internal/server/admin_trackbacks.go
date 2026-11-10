package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"wpgo/internal/store"
)

// Send Trackbacks (do_trackbacks() / trackback() in WordPress): the URLs typed into the classic
// editor's box are kept in _to_ping; when the post is published each one gets a trackback ping and
// moves to _pinged, whatever the remote answers (as trackback() does).

// postTrackback sends one trackback ping. Private networks are refused at connect time.
func postTrackback(ctx context.Context, target, title, link, blogName, excerpt string) error {
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("trackback: unsupported URL")
	}
	if r := []rune(excerpt); len(r) > 252 { // wp_html_excerpt( ..., 252, '&hellip;' )
		excerpt = string(r[:252]) + "..."
	}
	form := url.Values{"title": {title}, "url": {link}, "blog_name": {blogName}, "excerpt": {excerpt}}
	dialer := &net.Dialer{Timeout: 10 * time.Second,
		Control: publicOnly(func() bool { return allowPrivateFeedHosts }, "the address points at a private network")}
	client := &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DialContext: dialer.DialContext, TLSHandshakeTimeout: 10 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", "Pursae/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return nil
}

// deliverTrackbacks pings every URL in the post's _to_ping and moves them to _pinged.
func (s *Server) deliverTrackbacks(ctx context.Context, postID int64, link string) {
	p, err := s.store.GetPost(ctx, postID)
	if err != nil || p.Status != "publish" {
		return
	}
	raw, _ := p.Meta["_to_ping"].(string)
	pinged, _ := p.Meta["_pinged"].(string)
	done := map[string]bool{}
	for _, u := range strings.Fields(pinged) {
		done[u] = true
	}
	excerpt := p.Excerpt
	if excerpt == "" {
		excerpt = stripTagsSimple(p.Content)
	}
	for _, target := range strings.Fields(raw) {
		if done[target] {
			continue
		}
		_ = postTrackback(ctx, target, p.Title, link, s.settings.String("blogname"), excerpt)
		done[target] = true
		pinged = strings.TrimSpace(pinged + "\n" + target)
	}
	_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET meta = (coalesce(meta, '{}'::jsonb) - '_to_ping') || jsonb_build_object('_pinged', $2::text) WHERE id = $1`, postID, pinged)
}

// queueTrackbacks starts delivery in the background after a classic-editor save of a published post.
func (s *Server) queueTrackbacks(p *store.Post, link string) {
	if p.Status != "publish" {
		return
	}
	if raw, _ := p.Meta["_to_ping"].(string); strings.TrimSpace(raw) == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		s.deliverTrackbacks(ctx, p.ID, link)
	}()
}

// stripTagsSimple drops markup for the trackback excerpt.
func stripTagsSimple(h string) string {
	var b strings.Builder
	in := false
	for _, r := range h {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
