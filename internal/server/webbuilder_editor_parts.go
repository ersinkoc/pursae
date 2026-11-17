package server

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// wbEditorCodeParts is Boot.codeParts of a page: the header and footer templates Hello Pursae
// draws around it on the site, when they are made of code sections. The element editor draws them
// in the page's frame (locked, with a link that opens the template), so fixed headers, sticky
// footers and the wrappers the parts share look as on the site. nil for a template, a canvas page,
// another theme, or a page without code sections.
func (s *Server) wbEditorCodeParts(c *gin.Context, p *store.Post, doc *wbDocument) *webbuilder.CodeParts {
	if wbIsTemplate(p) || doc == nil || !s.wbOn() {
		return nil
	}
	if th := s.activeTheme(); th == nil || th.Slug != webbuilder.HelloPursaeTheme {
		return nil
	}
	rawDoc, rawPage := doc.Doc, doc.Page
	if doc.Draft != nil {
		rawDoc, rawPage = doc.Draft.Doc, doc.Draft.Page
	}
	var ds webbuilder.DocState
	var ps webbuilder.PageSettings
	if len(rawDoc) == 0 || json.Unmarshal(rawDoc, &ds) != nil || !webbuilder.HasCode(rawDoc) {
		return nil
	}
	_ = json.Unmarshal(rawPage, &ps)
	if webbuilder.EffectivePageLayout(ps, &ds) == webbuilder.PageLayoutCanvas {
		return nil
	}
	q, err := s.wbPreviewSingular(c, &siteQuery{Preview: true}, p)
	if err != nil || q == nil {
		return nil
	}
	lb := s.links(c)
	part := func(loc string) *webbuilder.CodeTemplate {
		d, id := s.wbPickTemplate(c, loc, q)
		if d == nil {
			return nil
		}
		secs := wbCodeSectionsOf(d.Doc)
		if len(secs) == 0 {
			return nil
		}
		title := ""
		if tp, err := s.store.GetPost(c, id); err == nil {
			title = tp.Title
		}
		return &webbuilder.CodeTemplate{ID: id, Title: title, EditURL: lb.HomeURL(wbEditURL(id)), Sections: secs}
	}
	out := &webbuilder.CodeParts{Header: part(webbuilder.LocationHeader), Footer: part(webbuilder.LocationFooter)}
	if out.Header == nil && out.Footer == nil {
		return nil
	}
	return out
}

// wbCodeSectionsOf is the visible sections of a stored doc when every one of them is a code
// section (nil otherwise: the editor's frame draws code markup only).
func wbCodeSectionsOf(raw json.RawMessage) []json.RawMessage {
	var d struct {
		Sections []json.RawMessage `json:"sections"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &d) != nil {
		return nil
	}
	out := []json.RawMessage{}
	for _, sec := range d.Sections {
		var head struct {
			Kind   string `json:"kind"`
			Hidden bool   `json:"hidden"`
		}
		if json.Unmarshal(sec, &head) != nil {
			return nil
		}
		if head.Hidden {
			continue
		}
		if head.Kind != webbuilder.KindCode {
			return nil
		}
		out = append(out, sec)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
