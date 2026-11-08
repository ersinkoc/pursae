package server

import (
	"context"
	"strconv"
	"strings"

	"wpgo/internal/i18n"
)

// Pursae Multi Language: user content sentinels. Post content, post titles in loops, comment
// bodies and a Web Builder page's own snapshot are translated per post (translations are posts
// of their own), never as strings. While Multi Language is on, a front-end request with a
// language marks them with <!--pll:uc--> … <!--/pll:uc--> (<!--pll:uc post=N--> for the content
// of post N) so the string pass (i18n.TranslateFront) and string capture leave them alone;
// golylangHTML removes every sentinel before the page is written, so visitors never get one.
// While the plugin is off nothing is emitted and pages are byte-identical.

// pllUCOn reports whether this request marks user content (cheap: a context value and an option).
func (s *Server) pllUCOn(ctx context.Context) bool {
	r := pllFrom(ctx)
	if r == nil || !r.Front || r.Lang == nil {
		return false
	}
	if rc := renderFrom(ctx); rc != nil && rc.feed {
		return false
	}
	return s.golylangOn()
}

// pllUC wraps user content html in the sentinels (postID > 0: the content of that post).
func (s *Server) pllUC(ctx context.Context, html string, postID int64) string {
	if strings.TrimSpace(html) == "" || !s.pllUCOn(ctx) {
		return html
	}
	open := i18n.UCOpen
	if postID > 0 {
		open = i18n.UCOpenPost(postID)
	}
	return open + html + i18n.UCClose
}

// pllUCFinish removes the sentinels from a finished page. In visual translation mode (visual)
// the content of a post becomes a <pll-uc data-pll-post="N"> region (display:contents, so the
// layout does not change) the visual editor offers to edit that post's translation for; the other
// sentinels are removed.
func pllUCFinish(page []byte, visual bool) []byte {
	if !i18n.HasUserContentSentinels(page) {
		return page
	}
	if !visual {
		return i18n.StripUserContentSentinels(page)
	}
	var stack []bool // per open sentinel: whether it opened a <pll-uc> element
	return i18n.ReplaceUserContentSentinels(page, func(open bool, post int64) string {
		if open {
			stack = append(stack, post > 0)
			if post > 0 {
				return `<pll-uc data-pll-post="` + strconv.FormatInt(post, 10) + `" style="display:contents">`
			}
			return ""
		}
		if len(stack) == 0 {
			return ""
		}
		el := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if el {
			return "</pll-uc>"
		}
		return ""
	})
}
