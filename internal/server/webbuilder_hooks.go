package server

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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
// _web_builder marker and stays an ordinary post. It returns log lines for the import report.
func (s *Server) wbImportDocs(ctx context.Context, u *store.User, postIDs []int64, urlMap map[string]string) []string {
	var log []string
	unfiltered := u != nil && u.Can("unfiltered_html", nil)
	rep := wbURLReplacer(urlMap)
	wb := s.store.WebBuilder()
	kept := 0
	for _, id := range postIDs {
		p, err := s.store.GetPost(ctx, id)
		if err != nil || p == nil {
			continue
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
		if hasDoc && s.wbEnabledType(p.Type) {
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
	from := make([]string, 0, len(urlMap))
	for k := range urlMap {
		if k != "" {
			from = append(from, k)
		}
	}
	sort.Slice(from, func(i, j int) bool { return len(from[i]) > len(from[j]) })
	pairs := make([]string, 0, 2*len(from))
	for _, k := range from {
		pairs = append(pairs, k, urlMap[k])
	}
	return strings.NewReplacer(pairs...)
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
