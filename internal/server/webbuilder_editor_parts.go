package server

import (
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// wbEditorCodeParts is Boot.codeParts of a page: the header and footer templates Hello Pursae
// draws around it on the site, when they are made of code sections. The element editor draws them
// in the page's frame (locked, with a link that opens the template), so fixed headers, sticky
// footers and the wrappers the parts share look as on the site. nil for a template, a canvas page,
// another theme, or a page without code sections.
//
// The sections go to anyone who can edit the page, so without edit_theme_options (who edits the
// templates themselves) their content bindings are left out: a bound form names its recipients
// (to, cc), redirects and messages there, and the editor draws the locked parts from their markup
// alone.
func (s *Server) wbEditorCodeParts(c *gin.Context, p *store.Post, u *store.User, doc *wbDocument) *webbuilder.CodeParts {
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
	keepBinds := u != nil && u.Can("edit_theme_options", nil)
	part := func(loc string) *webbuilder.CodeTemplate {
		d, id := s.wbPickTemplate(c, loc, q)
		if d == nil {
			return nil
		}
		secs := wbCodeSectionsOf(d.Doc, keepBinds)
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
// section (nil otherwise: the editor's frame draws code markup only). Without keepBinds each
// section's content.binds (webbuilder.ContentBinds) is removed.
func wbCodeSectionsOf(raw json.RawMessage, keepBinds bool) []json.RawMessage {
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
		if !keepBinds {
			if sec = wbWithoutBinds(sec); sec == nil {
				return nil
			}
		}
		out = append(out, sec)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// wbWithoutBinds is a code section's JSON without its content bindings (nil when it is not an
// object); a section without bindings comes back as is.
func wbWithoutBinds(sec json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(sec, &m) != nil || m == nil {
		return nil
	}
	var content map[string]json.RawMessage
	if json.Unmarshal(m["content"], &content) != nil || content == nil {
		return sec
	}
	if _, ok := content[webbuilder.ContentBinds]; !ok {
		return sec
	}
	delete(content, webbuilder.ContentBinds)
	cb, err := json.Marshal(content)
	if err != nil {
		return nil
	}
	m["content"] = cb
	out, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return out
}

// wbCodeHeadOrigins lists the https origins named in the head markup (content.head) of the code
// sections of docs or single sections (at most 24).
func wbCodeHeadOrigins(raws ...json.RawMessage) []string {
	seen := map[string]bool{}
	var out []string
	add := func(head string) {
		for _, m := range wbKitOriginRE.FindAllStringSubmatch(head, -1) {
			o := "https://" + strings.ToLower(m[1])
			if !seen[o] && len(out) < 24 {
				seen[o] = true
				out = append(out, o)
			}
		}
	}
	type sec struct {
		Kind    string            `json:"kind"`
		Content map[string]string `json:"content"`
	}
	for _, raw := range raws {
		if len(raw) == 0 {
			continue
		}
		var d struct {
			Sections []sec `json:"sections"`
		}
		var one sec
		if json.Unmarshal(raw, &d) == nil && d.Sections != nil {
			for _, x := range d.Sections {
				if x.Kind == webbuilder.KindCode {
					add(x.Content["head"])
				}
			}
		} else if json.Unmarshal(raw, &one) == nil && one.Kind == webbuilder.KindCode {
			add(one.Content["head"])
		}
	}
	return out
}
