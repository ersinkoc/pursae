package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// wbServerRenderInto draws the snapshot of a publish on the server (webbuilder.ServerRenderer, the
// editor's renderStatic in an embedded JS engine) and puts it into body.Render, so the rest of the
// publish runs exactly as for the editor: same sanitizer, same storage. It answers the error itself.
func (s *Server) wbServerRenderInto(c *gin.Context, p *store.Post, res *webbuilder.DocResult, body *wbPublishBody) bool {
	rr, err := s.wbServerRender(c, p, res)
	if err != nil {
		var re *wbRenderErr
		if errors.As(err, &re) && re.unavailable {
			code := "wb_server_render_unavailable"
			if !errors.Is(re.err, webbuilder.ErrNoServerRender) {
				code = "wb_server_render_failed"
			}
			restErr(c, http.StatusServiceUnavailable, code, "This build cannot render Web Builder pages on the server. Publish the page from the editor.")
			return false
		}
		restErr(c, http.StatusUnprocessableEntity, "wb_server_render_failed", "The page could not be rendered: "+err.Error())
		return false
	}
	body.Render = *rr
	body.UtilityCSS = ""
	return true
}

// wbRenderErr is a failed server render; unavailable: the renderer itself could not start.
type wbRenderErr struct {
	err         error
	unavailable bool
}

func (e *wbRenderErr) Error() string { return e.err.Error() }
func (e *wbRenderErr) Unwrap() error { return e.err }

// wbServerRender draws the snapshot of res on the server.
func (s *Server) wbServerRender(c *gin.Context, p *store.Post, res *webbuilder.DocResult) (*webbuilder.RenderResult, error) {
	r, err := webbuilder.SharedServerRenderer(s.web)
	if err != nil {
		return nil, &wbRenderErr{err: err, unavailable: true}
	}
	lb := s.links(c)
	rr, err := r.Render(res.JSON, webbuilder.ServerRenderOptions{
		PostID:           p.ID,
		SiteURL:          strings.TrimRight(lb.HomeURL("/"), "/"),
		FormActionPrefix: lb.HomeURL("/wp-json/" + webbuilder.RESTNamespace + "/forms/" + itoa64(p.ID) + "/"),
	})
	if err != nil {
		return nil, &wbRenderErr{err: err}
	}
	return rr, nil
}
