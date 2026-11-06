package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Pursae Web Builder hooks into the rest of the CMS (contract §6.3, §6.4):
//
//   - drift: a save from outside the builder that changes posts.content away from the fallback the
//     builder wrote marks the doc detached (wbAfterPostSave, called from afterPostSave and the few
//     save paths that bypass it); the next builder publish with reattach clears the flag
//     (store.PublishDoc);
//   - WXR: the export carries the doc and page settings as post meta, the import turns them back
//     into a docs row without a snapshot (renderer 0, so Regenerate re-renders it; until then the
//     imported posts.content fallback shows);
//   - Pursae Multi Language: a translation created with Duplicate content starts from the source's doc;
//   - WPGo Cache: builder pages carry the "web-builder" tag.
//
// None of these depend on the plugin being active: the data must stay consistent while it is off,
// so that turning it back on never shows a page that was edited meanwhile.

// WXR post meta keys of the doc (contract §6.4). They exist only inside export files.
const (
	wbWXRDocMeta      = "_web_builder_doc"      // DocState JSON
	wbWXRPageMeta     = "_web_builder_page"     // PageSettings JSON
	wbWXRDetachedMeta = "_web_builder_detached" // "1" when the exported page was detached
)

// wbCacheTag labels every public page rendered from a builder doc.
const wbCacheTag = "web-builder"

// Drift -----------------------------------------------------------------------------------------

// wbAfterPostSave runs after every post save (afterPostSave): a post whose stored posts.content no
// longer hashes to its doc's content_hash is marked detached.
func (s *Server) wbAfterPostSave(ctx context.Context, p *store.Post) {
	if p == nil || p.ID <= 0 {
		return
	}
	_, _ = s.wbDetectDrift(ctx, p.ID)
	if wbIsTemplate(p) {
		// Published, unpublished, trashed or renamed: the template index and every cached page
		// that may show it are stale (webbuilder_templates.go).
		s.wbTemplatesChanged(ctx)
	}
}

// wbDetectDrift compares the stored posts.content (not an in-memory copy, which a caller may have
// changed after the save) with content_hash and sets detached when they differ. It never clears the
// flag and reports whether this call detached the doc. A post without a doc costs one indexed
// UPDATE that touches no row.
func (s *Server) wbDetectDrift(ctx context.Context, postID int64) (bool, error) {
	tag, err := s.store.Pool.Exec(ctx, `UPDATE web_builder_docs d SET detached = true
		FROM posts p
		WHERE d.post_id = $1 AND p.id = d.post_id AND NOT d.detached
			AND d.content_hash <> encode(sha256(convert_to(p.content, 'UTF8')), 'hex')`, postID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// WXR -------------------------------------------------------------------------------------------

// wbExportMeta is the WXR <wp:postmeta> rows that carry a post's builder doc (empty when it has
// none). postmeta is the exporter's row formatter. The snapshot is not exported: it is rendered
// output, and the importing site renders its own.
func (s *Server) wbExportMeta(ctx context.Context, postID int64, postmeta func(key, value string) string) string {
	d, err := s.store.WebBuilder().Doc(ctx, postID)
	if err != nil || d == nil {
		return ""
	}
	out := postmeta(wbWXRDocMeta, string(d.Doc))
	if len(d.Page) > 0 && string(d.Page) != "null" {
		out += postmeta(wbWXRPageMeta, string(d.Page))
	}
	if d.Detached {
		out += postmeta(wbWXRDetachedMeta, "1")
	}
	return out
}

// wbImportDocs turns the doc meta of freshly imported posts into web_builder_docs rows. It runs
// after the importer's URL backfill: urlMap (old attachment URL → ours) is applied to the doc too,
// and content_hash is taken from the final posts.content. The doc is validated and, without
// unfiltered_html, html sections and custom CSS are stripped, like a publish. The row has no
// snapshot (renderer 0): the front shows the fallback until Regenerate renders it. The transport
// keys are always removed from posts.meta; a post whose doc is missing or invalid also loses the
// _web_builder marker and stays an ordinary post. Theme Builder templates also get their display
// conditions and preview object remapped to the imported ids (wbImportTemplateMeta). It returns
// log lines for the import report.
func (s *Server) wbImportDocs(ctx context.Context, u *store.User, postIDs []int64, urlMap map[string]string, ids wbImportIDs) []string {
	var log []string
	unfiltered := u != nil && u.Can("unfiltered_html", nil)
	rep := wbURLReplacer(urlMap)
	wb := s.store.WebBuilder()
	kept, templates := 0, 0
	for _, id := range postIDs {
		p, err := s.store.GetPost(ctx, id)
		if err != nil || p == nil {
			continue
		}
		if wbIsTemplate(p) {
			templates++
			log = append(log, s.wbImportTemplateMeta(ctx, u, p, ids)...)
		}
		rawDoc, hasDoc := p.Meta[wbWXRDocMeta].(string)
		_, hasMarker := p.Meta[webbuilder.PostMarkerMeta]
		_, hasPage := p.Meta[wbWXRPageMeta]
		_, hasDetached := p.Meta[wbWXRDetachedMeta]
		if !hasDoc && !hasMarker && !hasPage && !hasDetached {
			continue
		}
		rawPage, _ := p.Meta[wbWXRPageMeta].(string)
		detached := p.Meta[wbWXRDetachedMeta] == "1"
		keep := false
		if hasDoc && (s.wbEnabledType(p.Type) || wbIsTemplate(p)) {
			res, err := webbuilder.ParseDoc([]byte(rep.Replace(rawDoc)), webbuilder.DocOptions{UnfilteredHTML: unfiltered, PathPrefix: "/doc"})
			if err == nil && res.Valid() {
				d := &store.WebBuilderDoc{PostID: p.ID, Doc: res.JSON, Page: wbImportPage(rep.Replace(rawPage)),
					Renderer: 0, ContentHash: store.WebBuilderContentHash(p.Content), SnapshotUnfiltered: unfiltered}
				if u != nil {
					d.SnapshotUser = u.ID
				}
				if err := wb.PublishDoc(ctx, d, 0); err == nil {
					keep = true
					kept++
					if detached {
						_ = wb.SetDetached(ctx, p.ID, true)
					}
				}
			}
			if !keep {
				log = append(log, "The Pursae Web Builder design of “"+p.Title+"” could not be imported; the page keeps its content.")
			} else if wbIsTemplate(p) {
				log = append(log, "The template “"+p.Title+"” was imported; open it in Pursae Web Builder and publish it to show it on the site.")
			}
		}
		drop := []string{wbWXRDocMeta, wbWXRPageMeta, wbWXRDetachedMeta}
		if !keep {
			drop = append(drop, webbuilder.PostMarkerMeta)
		}
		_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta - $2::text[] WHERE id = $1`, p.ID, drop)
		if keep {
			_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || jsonb_build_object($2::text, '1') WHERE id = $1`,
				p.ID, webbuilder.PostMarkerMeta)
		}
	}
	if kept > 0 {
		s.wbPurgePages()
	}
	if templates > 0 {
		s.wbTemplatesChanged(ctx)
	}
	return log
}

// wbImportIDs are the importer's id maps (ids in the file → ours), for the ids inside template
// display conditions and preview objects.
type wbImportIDs struct {
	Posts, Terms, Users map[int64]int64
}

// wbImportTemplateMeta remaps the ids in an imported template's display conditions and preview
// object; a rule whose target was not imported is dropped and logged, a preview object that names
// nothing imported goes back to the type's default. A template imported by a user without
// edit_theme_options is saved as a draft (disabled).
func (s *Server) wbImportTemplateMeta(ctx context.Context, u *store.User, p *store.Post, ids wbImportIDs) []string {
	var log []string
	wb := s.store.WebBuilder()
	t, err := wb.Template(ctx, p.ID)
	if err != nil {
		return nil
	}
	mapID := func(m map[int64]int64, id int64) (int64, bool) {
		n, ok := m[id]
		return n, ok && n > 0
	}
	rules := make([]string, 0, len(t.Conditions))
	for _, raw := range t.Conditions {
		c, err := webbuilder.ParseCondition(strings.TrimSpace(raw))
		if err != nil {
			log = append(log, "The display condition “"+raw+"” of the template “"+p.Title+"” is not valid and was dropped.")
			continue
		}
		if c.ID != 0 {
			var m map[int64]int64
			switch c.Scope {
			case webbuilder.ScopeEntry, webbuilder.ScopeChildOf:
				m = ids.Posts
			case webbuilder.ScopeInTerm, webbuilder.ScopeTaxonomy:
				m = ids.Terms
			case webbuilder.ScopeByAuthor, webbuilder.ScopeAuthor:
				m = ids.Users
			}
			n, ok := mapID(m, c.ID)
			if !ok {
				log = append(log, "The display condition “"+raw+"” of the template “"+p.Title+"” was dropped: what it names was not imported.")
				continue
			}
			c.ID = n
		}
		rules = append(rules, c.String())
	}
	rules, _ = webbuilder.NormalizeConditions(rules)
	if len(rules) > webbuilder.MaxConditions {
		rules = rules[:webbuilder.MaxConditions]
	}
	_ = wb.SetTemplateConditions(ctx, p.ID, rules, t.Priority)
	preview := ""
	if pv, err := wbParsePreview(t.Preview); err == nil {
		preview = t.Preview
		var n int64
		ok := true
		switch pv.Kind {
		case "post":
			n, ok = mapID(ids.Posts, pv.ID)
			preview = webbuilder.PreviewPost + itoa64(n)
		case "term":
			n, ok = mapID(ids.Terms, pv.ID)
			preview = webbuilder.PreviewTerm + pv.Name + ":" + itoa64(n)
		case "author":
			n, ok = mapID(ids.Users, pv.ID)
			preview = webbuilder.PreviewAuthor + itoa64(n)
		}
		if !ok {
			preview = ""
		}
	}
	_ = wb.SetTemplatePreview(ctx, p.ID, preview)
	if (u == nil || !u.Can(webbuilder.TemplateCap, nil)) && p.Status != "draft" {
		_, _ = s.store.Pool.Exec(ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, p.ID)
	}
	return log
}

// wbImportPage is the imported PageSettings JSON, {} when it is missing or not an object.
func wbImportPage(raw string) json.RawMessage {
	var obj map[string]any
	if raw == "" || json.Unmarshal([]byte(raw), &obj) != nil || obj == nil {
		return json.RawMessage(`{}`)
	}
	b, _ := json.Marshal(obj)
	return b
}

// wbURLReplacer replaces old URLs with new ones, longest first (so photo.jpg is not replaced
// inside photo-300x200.jpg's own mapping).
func wbURLReplacer(urlMap map[string]string) *strings.Replacer {
	return wxrURLReplacer(urlMap) // the same pairs as the content backfill, image-size stems included
}

// Copies -----------------------------------------------------------------------------------------

// wbCopyDoc gives dst a copy of src's builder doc and snapshot (Pursae Multi Language "Duplicate
// content"). Call it after dst's content was copied from src: content_hash is taken from dst's
// stored content, and a source that was detached stays detached in the copy. The snapshot's form
// actions are pointed at dst. dst gets the _web_builder marker. A src without a doc, or a dst that
// already has one, is left alone.
func (s *Server) wbCopyDoc(ctx context.Context, src, dst *store.Post) {
	if src == nil || dst == nil || src.ID == dst.ID {
		return
	}
	if err := s.wbCopyDocErr(ctx, src.ID, dst); err == nil {
		if dst.Meta == nil {
			dst.Meta = map[string]any{}
		}
		dst.Meta[webbuilder.PostMarkerMeta] = "1"
	}
}

var errWBNoCopy = errors.New("web builder: nothing to copy")

func (s *Server) wbCopyDocErr(ctx context.Context, srcID int64, dst *store.Post) error {
	wb := s.store.WebBuilder()
	from, err := wb.Doc(ctx, srcID)
	if err != nil {
		return err
	}
	if rev, err := wb.DocRev(ctx, dst.ID); err != nil || rev != 0 {
		return errWBNoCopy
	}
	fresh, err := s.store.GetPost(ctx, dst.ID)
	if err != nil {
		return err
	}
	page := map[string]any{}
	_ = json.Unmarshal(from.Page, &page)
	if page == nil {
		page = map[string]any{}
	}
	page["title"], page["slug"] = fresh.Title, fresh.Slug
	pageJSON, _ := json.Marshal(page)
	d := &store.WebBuilderDoc{PostID: dst.ID, Doc: from.Doc, Page: pageJSON,
		HTML: wbRetargetForms(from.HTML, srcID, dst.ID),
		CSS:  from.CSS, Fonts: from.Fonts, Features: from.Features, Renderer: from.Renderer,
		ContentHash:  store.WebBuilderContentHash(fresh.Content),
		SnapshotUser: from.SnapshotUser, SnapshotUnfiltered: from.SnapshotUnfiltered}
	if err := wb.PublishDoc(ctx, d, 0); err != nil {
		return err
	}
	if from.Detached {
		_ = wb.SetDetached(ctx, dst.ID, true)
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || jsonb_build_object($2::text, '1') WHERE id = $1`,
		dst.ID, webbuilder.PostMarkerMeta); err != nil {
		return err
	}
	s.purgeTags("post:" + itoa64(dst.ID))
	return nil
}

// wbRetargetForms points the form actions of a snapshot (…/web-builder/v2/forms/{src}/{section},
// the only form action the sanitizer keeps, §8.2) at another post.
func wbRetargetForms(html string, srcID, dstID int64) string {
	from := "/" + webbuilder.RESTNamespace + "/forms/" + itoa64(srcID) + "/"
	if !strings.Contains(html, from) {
		return html
	}
	return strings.ReplaceAll(html, from, "/"+webbuilder.RESTNamespace+"/forms/"+itoa64(dstID)+"/")
}

// Cache -----------------------------------------------------------------------------------------

// wbCacheTags are the WPGo Cache tags of a public page rendered from a builder doc ("web-builder"),
// so a change that affects every builder page (the plugin's settings, a Regenerate run, an import)
// purges them with wbPurgePages. The post's own "post:N" tag comes from cacheTags.
func (s *Server) wbCacheTags(_ *gin.Context, q *siteQuery) []string {
	if q == nil || q.Post == nil || q.Feed != "" {
		return nil
	}
	if v, _ := q.Post.Meta[webbuilder.PostMarkerMeta].(string); v != "1" {
		return nil
	}
	return []string{wbCacheTag}
}

// wbPurgePages drops every cached builder page.
func (s *Server) wbPurgePages() { s.purgeTags(wbCacheTag) }
