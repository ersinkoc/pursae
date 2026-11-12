package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
	"wpgo/internal/wp"
)

// Pursae Multi Language: translating a whole post from its source.
//
// One engine serves every content workflow: "Machine translate the copy" of a new translation,
// "Machine translate again" on an existing one, the bulk "Translate to…" action of the posts lists
// and the XLIFF import. They differ only in the pllTr that turns source texts into translations
// (the machine translation provider, or the <source>/<target> pairs of an XLIFF file).
//
//   - Title and excerpt are plain texts.
//   - Block content: the HTML between block comment delimiters (pllMTBlocks), KSES on the way back.
//   - A Pursae Web Builder page has no server renderer (the published snapshot is drawn by the
//     editor), so its snapshot is translated in place: the text units of the stored HTML
//     (golylang_htmlunits.go) are translated, the same translations are written into the text
//     values of the builder document that show them, posts.content is regenerated from the
//     translated document and the pair is published as a new revision of the doc. Markup,
//     classes, CSS, fonts and element attributes never come from the translation.

// pllTr translates texts in order; asHTML says they are HTML fragments.
type pllTr func(texts []string, asHTML bool) ([]string, error)

// pllMTTr is the machine translation provider as a pllTr.
func (s *Server) pllMTTr(ctx context.Context, src, dst *pllLanguage) pllTr {
	return func(texts []string, asHTML bool) ([]string, error) {
		return s.pllMachineTranslate(ctx, texts, src, dst, asHTML)
	}
}

// pllTranslated is a translated post, not yet written.
type pllTranslated struct {
	Title, Excerpt, Content string
	WB                      *store.WebBuilderDoc // the translated builder doc of a Web Builder page
}

var errPllWBNoSnapshot = errors.New("this Web Builder page has no published snapshot yet: open it in Pursae Web Builder and publish it first")

// pllIsBuilder reports whether p is a Web Builder page.
func pllIsBuilder(p *store.Post) bool {
	_, ok := p.Meta[webbuilder.PostMarkerMeta]
	return ok
}

// pllTranslatePlan translates src for the translation dstID with tr. Nothing is written.
func (s *Server) pllTranslatePlan(ctx context.Context, src *store.Post, dstID int64, tr pllTr) (*pllTranslated, error) {
	res, err := tr([]string{src.Title, src.Excerpt}, false)
	if err != nil {
		return nil, err
	}
	if len(res) != 2 {
		return nil, errors.New("the translation service answered a wrong number of texts")
	}
	out := &pllTranslated{Title: wp.KsesPost(strings.TrimSpace(res[0])), Excerpt: wp.KsesPost(res[1]), Content: src.Content}
	if out.Title == "" {
		out.Title = src.Title
	}
	if pllIsBuilder(src) {
		from, err := s.store.WebBuilder().Doc(ctx, src.ID)
		if err == nil {
			d, content, err := s.pllWBPlan(from, src.ID, dstID, out.Title, tr)
			if err != nil {
				return nil, err
			}
			out.WB, out.Content = d, content
			return out, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if out.Content, err = pllMTBlocks(src.Content, func(texts []string) ([]string, error) { return tr(texts, true) }); err != nil {
		return nil, err
	}
	return out, nil
}

// pllTranslateWrite stores a translated post into dst.
func (s *Server) pllTranslateWrite(ctx context.Context, dst *store.Post, t *pllTranslated) error {
	if t.WB != nil {
		wb := s.store.WebBuilder()
		rev, err := wb.DocRev(ctx, dst.ID)
		if err != nil {
			return err
		}
		t.WB.PostID = dst.ID
		if err := wb.PublishDoc(ctx, t.WB, rev); err != nil {
			return err
		}
		if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET meta = meta || jsonb_build_object($2::text, '1') WHERE id = $1`, dst.ID, webbuilder.PostMarkerMeta); err != nil {
			return err
		}
		if dst.Meta == nil {
			dst.Meta = map[string]any{}
		}
		dst.Meta[webbuilder.PostMarkerMeta] = "1"
	}
	if _, err := s.store.Pool.Exec(ctx, `UPDATE posts SET title = $2, excerpt = $3, content = $4, modified_at = now() WHERE id = $1`,
		dst.ID, t.Title, t.Excerpt, t.Content); err != nil {
		return err
	}
	dst.Title, dst.Excerpt, dst.Content = t.Title, t.Excerpt, t.Content
	s.purgeTags("post:" + itoa64(dst.ID))
	return nil
}

// pllTranslateFrom translates src into the existing translation dst.
func (s *Server) pllTranslateFrom(ctx context.Context, src, dst *store.Post, tr pllTr) error {
	t, err := s.pllTranslatePlan(ctx, src, dst.ID, tr)
	if err != nil {
		return err
	}
	return s.pllTranslateWrite(ctx, dst, t)
}

// Web Builder ----------------------------------------------------------------------------------

type pllDictEntry struct{ HTML, Plain string }

// pllWBSkipKey reports whether a section content key never holds text.
func pllWBSkipKey(k string) bool {
	switch k {
	case "html", "icon", "variant", "visual", "video", webbuilder.ContentMenu, webbuilder.ContentLogoSource,
		webbuilder.FormKeyTo, webbuilder.FormKeyCc, webbuilder.FormKeyRedirect:
		return true
	}
	if k == "href" || strings.HasSuffix(k, "Href") || strings.HasSuffix(k, "Image") {
		return true
	}
	for _, p := range []string{"image", "photo", "poster", "avatar"} {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// pllWBPlan translates the builder doc and snapshot of the source post srcID for the post dstID.
// It returns the doc to publish and the fallback content generated from it.
func (s *Server) pllWBPlan(from *store.WebBuilderDoc, srcID, dstID int64, title string, tr pllTr) (*store.WebBuilderDoc, string, error) {
	if strings.TrimSpace(from.HTML) == "" {
		return nil, "", errPllWBNoSnapshot
	}
	retarget := strings.ReplaceAll(wbRetargetForms(from.HTML, srcID, dstID), `data-wb-post="`+itoa64(srcID)+`"`, `data-wb-post="`+itoa64(dstID)+`"`)
	snap, err := pllHTMLParse(retarget)
	if err != nil {
		return nil, "", err
	}
	// One call per mode, in document order.
	for _, asHTML := range []bool{true, false} {
		var texts []string
		var at []int
		for i, u := range snap.Units {
			if u.HTML == asHTML {
				texts, at = append(texts, u.Text), append(at, i)
			}
		}
		if len(texts) == 0 {
			continue
		}
		res, err := tr(texts, asHTML)
		if err != nil {
			return nil, "", err
		}
		if len(res) != len(texts) {
			return nil, "", errors.New("the translation service answered a wrong number of texts")
		}
		for k, i := range at {
			snap.Set(i, res[k])
		}
	}
	// What each visible text became: the dictionary the document is translated with.
	dict := map[string]pllDictEntry{}
	for _, u := range snap.Units {
		if u.Key == "" {
			continue
		}
		var e pllDictEntry
		switch {
		case u.attr != "":
			e.Plain, _ = pllAttr(u.node, u.attr)
		case u.HTML:
			e.HTML, e.Plain = pllInnerHTML(u.node), pllNodeText(u.node)
		default:
			e.Plain = u.node.Data
		}
		if _, ok := dict[u.Key]; !ok || u.attr == "" {
			dict[u.Key] = e
		}
	}
	newHTML := snap.Render()
	if len(newHTML) > webbuilder.MaxSnapshotHTML {
		return nil, "", webbuilder.ErrSnapshotTooLarge
	}

	root := map[string]any{}
	if err := json.Unmarshal(from.Doc, &root); err != nil {
		return nil, "", err
	}
	// Texts the page does not show (the messages of a form): translated on their own.
	var extra []*pllWBLate
	pllWBTranslateSections(root["sections"], dict, &extra)
	if len(extra) > 0 {
		texts := make([]string, len(extra))
		for i, e := range extra {
			texts[i] = e.text
		}
		res, err := tr(texts, false)
		if err != nil {
			return nil, "", err
		}
		if len(res) == len(extra) {
			for i, e := range extra {
				if v := strings.TrimSpace(pllPlainTr(e.text, res[i])); v != "" {
					e.m[e.key] = v
				}
			}
		}
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, "", err
	}
	res, err := webbuilder.ParseDoc(raw, webbuilder.DocOptions{UnfilteredHTML: from.SnapshotUnfiltered})
	if err != nil {
		return nil, "", err
	}
	if !res.Valid() {
		return nil, "", fmt.Errorf("the translated builder document is not valid (%s)", res.Errors[0].Message)
	}
	content := webbuilder.FallbackHTML(&res.Doc)
	page := map[string]any{}
	_ = json.Unmarshal(from.Page, &page)
	if page == nil {
		page = map[string]any{}
	}
	page["title"] = title
	pageJSON, _ := json.Marshal(page)
	d := &store.WebBuilderDoc{PostID: dstID, Doc: res.JSON, Page: pageJSON, HTML: newHTML, CSS: from.CSS, Fonts: from.Fonts,
		Features: from.Features, Renderer: from.Renderer, ContentHash: store.WebBuilderContentHash(content),
		SnapshotUser: from.SnapshotUser, SnapshotUnfiltered: from.SnapshotUnfiltered}
	return d, content, nil
}

type pllWBLate struct {
	m    map[string]any
	key  string
	text string
}

// pllWBTranslateSections rewrites the text values of sections (a decoded DocState.sections) that
// the snapshot showed, with the translations of dict.
func pllWBTranslateSections(sections any, dict map[string]pllDictEntry, late *[]*pllWBLate) {
	list, _ := sections.([]any)
	for _, raw := range list {
		sec, _ := raw.(map[string]any)
		if sec == nil {
			continue
		}
		pllWBTranslateSection(sec, dict, late)
	}
}

func pllWBTranslateSection(sec map[string]any, dict map[string]pllDictEntry, late *[]*pllWBLate) {
	kind, _ := sec["kind"].(string)
	if content, _ := sec["content"].(map[string]any); content != nil {
		for k, v := range content {
			sv, ok := v.(string)
			if !ok || pllWBSkipKey(k) || !hasLetter(sv) {
				continue
			}
			if nv, hit := pllWBValue(sv, dict); hit {
				content[k] = nv
			} else if k == webbuilder.FormKeySuccess || k == webbuilder.FormKeyFailure {
				*late = append(*late, &pllWBLate{m: content, key: k, text: sv})
			}
		}
	}
	items, _ := sec["items"].([]any)
	for _, raw := range items {
		it, _ := raw.(map[string]any)
		if it == nil {
			continue
		}
		node, _ := it["node"].(string)
		title, _ := it["title"].(string)
		text, _ := it["text"].(string)
		// A layout cell's title is its widget type, an image/icon node's title its source.
		cell := kind == webbuilder.KindLayout && (webbuilder.IsCellType(title) || webbuilder.IsDynWidget(title))
		if title != "" && !cell && !(kind == webbuilder.KindCustom && (node == "image" || node == "icon")) && hasLetter(title) {
			if nv, hit := pllWBValue(title, dict); hit {
				it["title"] = nv
			}
		}
		if text != "" && !(kind == webbuilder.KindLayout && title == "image") && hasLetter(text) {
			if nv, hit := pllWBValue(text, dict); hit {
				it["text"] = nv
			}
		}
		if sub, _ := it["sub"].(map[string]any); sub != nil {
			pllWBTranslateSection(sub, dict, late)
		}
	}
}

// pllWBValue translates one document value with dict; hit is false when nothing of it was shown.
func pllWBValue(v string, dict map[string]pllDictEntry) (string, bool) {
	if !webbuilder.IsRich(v) {
		e, ok := dict[pllUnitKey(v)]
		if !ok || strings.TrimSpace(e.Plain) == "" {
			return v, false
		}
		return strings.TrimSpace(e.Plain), true
	}
	d, err := pllHTMLParse(v)
	if err != nil {
		return v, false
	}
	hit := false
	for i, u := range d.Units {
		e, ok := dict[u.Key]
		if !ok {
			continue
		}
		switch {
		case u.HTML && e.HTML != "":
			d.Set(i, e.HTML)
		case u.HTML:
			d.Set(i, html.EscapeString(e.Plain))
		default:
			d.Set(i, e.Plain)
		}
		hit = true
	}
	if !hit {
		return v, false
	}
	return webbuilder.SanitizeRich(d.Render()), true
}

// Source and new translations ------------------------------------------------------------------

// pllSourceOf is the post a translation is translated from: the group's post in the default
// language, else its oldest other member. from (a language slug) picks a member explicitly.
func (s *Server) pllSourceOf(ctx context.Context, p *store.Post, from string) (*store.Post, *pllLanguage) {
	tr := s.pllTranslations(ctx, pllPost, p.ID)
	pick := int64(0)
	if from != "" && tr[from] > 0 && tr[from] != p.ID {
		pick = tr[from]
	}
	if def := defaultSlug(s.pllDefault(ctx)); pick == 0 && tr[def] > 0 && tr[def] != p.ID {
		pick = tr[def]
	}
	if pick == 0 {
		var ids []int64
		for _, id := range tr {
			if id != p.ID {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) > 0 {
			pick = ids[0]
		}
	}
	if pick == 0 {
		return nil, nil
	}
	src, err := s.store.GetPost(ctx, pick)
	if err != nil || src.Status == "trash" {
		return nil, nil
	}
	return src, s.pllLang(ctx, s.pllObjLang(ctx, pllPost, src.ID))
}

// pllNewTranslation creates the draft translation of src in l with the source's content copied
// (Duplicate content), linked into the translation group. exists is true when src already has a
// live translation in l (nothing is created).
func (s *Server) pllNewTranslation(ctx context.Context, src *store.Post, l *pllLanguage, authorID int64) (cp *store.Post, exists bool, err error) {
	s.pllEnsureLang(ctx, src)
	own := s.pllObjLang(ctx, pllPost, src.ID)
	if own == l.Slug {
		return nil, true, nil
	}
	tr := s.pllTranslations(ctx, pllPost, src.ID)
	tr[own] = src.ID
	if id := tr[l.Slug]; id > 0 {
		if got, err := s.store.GetPost(ctx, id); err == nil && got.Status != "trash" {
			return got, true, nil
		}
	}
	cp = &store.Post{Type: src.Type, AuthorID: authorID, Title: src.Title, Slug: src.Slug, Status: "draft",
		Content: src.Content, Excerpt: src.Excerpt, CommentStatus: src.CommentStatus, PingStatus: src.PingStatus,
		Format: src.Format, Meta: map[string]any{}}
	if err := s.store.InsertPost(ctx, cp); err != nil {
		return nil, false, err
	}
	discard := func() {
		if s.store.DeletePost(ctx, cp.ID) != nil {
			_ = s.store.TrashPost(ctx, cp.ID)
			_ = s.pllUnlink(ctx, pllPost, cp.ID)
		}
	}
	if err := s.pllSetLang(ctx, pllPost, cp.ID, l.Slug); err != nil {
		discard()
		return nil, false, err
	}
	tr[l.Slug] = cp.ID
	if err := s.pllSaveTranslations(ctx, pllPost, tr); err != nil {
		discard()
		return nil, false, err
	}
	s.pllCopyForTranslation(ctx, src, cp, l, true)
	if fresh, err := s.store.GetPost(ctx, cp.ID); err == nil {
		cp = fresh
	}
	return cp, false, nil
}

// Background job -------------------------------------------------------------------------------

// pllJob is the one long translation run of a site (bulk translate, "Translate all empty
// strings"). It lives in memory: a restart ends it, and what it saved so far stays saved.
type pllJob struct {
	Label              string
	Total, Done, Fails int
	Err                string
	Running            bool
	Started, Finished  time.Time
}

var pllJobs = struct {
	sync.Mutex
	m map[*Server]*pllJob
}{m: map[*Server]*pllJob{}}

// pllJobInline runs jobs inside the request (tests).
var pllJobInline bool

// pllJobStep reports the progress of a running job: one more item done (or failed with err).
type pllJobStep func(err error)

// pllJobStart runs fn in the background unless a job is already running (false).
func (s *Server) pllJobStart(ctx context.Context, label string, total int, fn func(ctx context.Context, step pllJobStep) error) bool {
	pllJobs.Lock()
	if j := pllJobs.m[s]; j != nil && j.Running {
		pllJobs.Unlock()
		return false
	}
	j := &pllJob{Label: label, Total: total, Running: true, Started: time.Now()}
	pllJobs.m[s] = j
	pllJobs.Unlock()
	step := func(err error) {
		pllJobs.Lock()
		defer pllJobs.Unlock()
		if err != nil {
			j.Fails++
			j.Err = err.Error()
			return
		}
		j.Done++
	}
	run := func(ctx context.Context) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("golylang: translation job panicked", "job", label, "panic", r)
				pllJobs.Lock()
				j.Err = "The run stopped unexpectedly."
				pllJobs.Unlock()
			}
			pllJobs.Lock()
			j.Running, j.Finished = false, time.Now()
			pllJobs.Unlock()
		}()
		if err := fn(ctx, step); err != nil {
			pllJobs.Lock()
			j.Err = err.Error()
			pllJobs.Unlock()
		}
	}
	if pllJobInline {
		run(ctx)
		return true
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Hour)
	go func() {
		defer cancel()
		run(bg)
	}()
	return true
}

// pllJobState is a copy of the site's job, nil when there is none or it ended over an hour ago.
func (s *Server) pllJobState() *pllJob {
	pllJobs.Lock()
	defer pllJobs.Unlock()
	j := pllJobs.m[s]
	if j == nil || (!j.Running && time.Since(j.Finished) > time.Hour) {
		return nil
	}
	cp := *j
	return &cp
}

// pllJobNotices is the admin notice of the running or last job.
func (s *Server) pllJobNotices() []notice {
	if !s.golylangOn() {
		return nil
	}
	j := s.pllJobState()
	if j == nil {
		return nil
	}
	label := template.HTMLEscapeString(j.Label)
	if j.Running {
		return []notice{{Type: "info", Message: template.HTML(fmt.Sprintf("%s is running in the background: %d of %d done. Reload the page to see the progress.", label, j.Done, j.Total))}}
	}
	msg := fmt.Sprintf("%s finished: %d of %d done.", label, j.Done, j.Total)
	typ := "success"
	if j.Fails > 0 || j.Err != "" {
		typ = "error"
		if j.Fails > 0 {
			msg += fmt.Sprintf(" %d failed.", j.Fails)
		}
		if j.Err != "" {
			msg += " Last error: " + template.HTMLEscapeString(j.Err)
		}
	}
	return []notice{{Type: typ, Message: template.HTML(msg), Dismissible: true}}
}

// Posts list: bulk actions ---------------------------------------------------------------------

const (
	pllBulkTranslate = "pll_translate" // "Translate to…"
	pllBulkXLIFF     = "pll_xliff"     // "Export for translation (XLIFF)…"
	pllBulkMax       = 200             // posts of one bulk run
)

// pllBulkOption is one entry the posts list adds to its Bulk actions select.
type pllBulkOption struct{ Value, Label string }

// pllBulkLang is a language of the bulk actions' target select.
type pllBulkLang struct{ Slug, Name string }

// golylangBulkOptions are the bulk actions and target languages Pursae Multi Language adds to a
// posts list of a translated type.
func (s *Server) golylangBulkOptions(c *gin.Context, typ string) ([]pllBulkOption, []pllBulkLang) {
	if !s.golylangOn() || !s.pllTranslatedType(typ) {
		return nil, nil
	}
	var langs []pllBulkLang
	for _, l := range s.pllLanguages(c) {
		langs = append(langs, pllBulkLang{l.Slug, l.Name})
	}
	if len(langs) < 2 {
		return nil, nil
	}
	label := "Translate to…"
	if s.pllMTEnabled() {
		label = "Machine translate to…"
	}
	return []pllBulkOption{{pllBulkTranslate, label}, {pllBulkXLIFF, "Export for translation (XLIFF)…"}}, langs
}

// golylangBulk handles the posts list bulk actions of Pursae Multi Language; false for others.
func (s *Server) golylangBulk(c *gin.Context, typ, action string) bool {
	if action != pllBulkTranslate && action != pllBulkXLIFF {
		return false
	}
	back := "/wp-admin/edit?post_type=" + url.QueryEscape(typ)
	fail := func(msg string) { c.Redirect(http.StatusFound, back+"&pll_error="+url.QueryEscape(msg)) }
	if !s.golylangOn() || !s.pllTranslatedType(typ) {
		fail("Pursae Multi Language does not translate this content type.")
		return true
	}
	u := currentUser(c)
	l := s.pllLang(c, c.Query("pll_to"))
	if l == nil {
		fail("Choose the language to translate into.")
		return true
	}
	var posts []*store.Post
	for _, raw := range bulkPostIDs(c) {
		id, _ := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		p, err := s.store.GetPost(c, id)
		if err != nil || p.Type != typ || p.Status == "trash" || !u.Can("edit_post", p) {
			continue
		}
		posts = append(posts, p)
		if len(posts) >= pllBulkMax {
			break
		}
	}
	if len(posts) == 0 {
		fail("Select the items to translate first.")
		return true
	}
	if action == pllBulkXLIFF {
		s.pllXLIFFExport(c, posts, l) // golylang_xliff.go
		return true
	}
	if !u.Can("edit_"+restBaseFor(typ), nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to create posts of this type.")
		return true
	}
	mt := s.pllMTEnabled()
	label := "Translating " + strconv.Itoa(len(posts)) + " items to " + l.Name
	ok := s.pllJobStart(c.Request.Context(), label, len(posts), func(ctx context.Context, step pllJobStep) error {
		for _, p := range posts {
			if err := ctx.Err(); err != nil {
				return err
			}
			cp, exists, err := s.pllNewTranslation(ctx, p, l, u.ID)
			if err != nil {
				step(err)
				continue
			}
			if exists {
				step(nil) // already translated: nothing to do
				continue
			}
			if mt {
				from := s.pllLang(ctx, s.pllObjLang(ctx, pllPost, p.ID))
				if err := s.pllTranslateFrom(ctx, p, cp, s.pllMTTr(ctx, from, l)); err != nil {
					// The untranslated copy stays as a draft; a provider that is down fails every
					// following post too, so the run stops here.
					step(fmt.Errorf("%q: %w", p.Title, err))
					if !errors.Is(err, errPllWBNoSnapshot) {
						return err
					}
					continue
				}
			}
			step(nil)
		}
		return nil
	})
	if !ok {
		fail("Another translation run is still in progress. Wait until it has finished.")
		return true
	}
	c.Redirect(http.StatusFound, back+"&pll_started=1")
	return true
}

// golylangListNotices are the posts list notices of Pursae Multi Language.
func (s *Server) golylangListNotices(c *gin.Context) []notice {
	var out []notice
	if msg := c.Query("pll_error"); msg != "" {
		out = append(out, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(msg)), Dismissible: true})
	}
	return append(out, s.pllJobNotices()...)
}

// "Machine translate again" --------------------------------------------------------------------

// pllMTAgainURL is the link that translates p again from its source, "" when it has none.
func (s *Server) pllMTAgainURL(c *gin.Context, p *store.Post) string {
	if !s.pllMTEnabled() {
		return ""
	}
	if src, _ := s.pllSourceOf(c, p, ""); src == nil {
		return ""
	}
	return "/wp-admin/admin?page=mlang_translate&pll_action=mt_again&post=" + itoa64(p.ID) + "&_wpnonce=" + s.nonce(c, "pll-mt-again_"+itoa64(p.ID))
}

// pllTranslateScreen is admin?page=mlang_translate: the content translation actions.
func (s *Server) pllTranslateScreen(c *gin.Context) {
	action := c.Query("pll_action")
	if c.Request.Method == http.MethodPost {
		action = c.PostForm("pll_action")
	}
	switch action {
	case "mt_again":
		s.pllMTAgain(c)
	case "xliff_import":
		s.pllXLIFFImport(c) // golylang_xliff.go
	default:
		c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_strings")
	}
}

// pllMTAgain translates an existing translation again from its source: title, excerpt and content
// (or the Web Builder page) are replaced by a fresh machine translation of the source.
func (s *Server) pllMTAgain(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Query("post"), 10, 64)
	if !s.checkNonce(c, "pll-mt-again_"+itoa64(id)) {
		s.denyNonce(c)
		return
	}
	u := currentUser(c)
	p, err := s.store.GetPost(c, id)
	if err != nil || !s.pllTranslatedType(p.Type) || !u.Can("edit_post", p) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	back := "/wp-admin/post?post=" + itoa64(p.ID) + "&action=edit"
	fail := func(msg string) { c.Redirect(http.StatusFound, back+"&pll_mt=error&pll_err="+url.QueryEscape(msg)) }
	if !s.pllMTEnabled() {
		fail(errPllMTOff.Error())
		return
	}
	src, from := s.pllSourceOf(c, p, c.Query("from"))
	to := s.pllLang(c, s.pllObjLang(c, pllPost, p.ID))
	if src == nil || to == nil {
		fail("This item has no translation to translate from.")
		return
	}
	if !u.Can("read_post", src) && !u.Can("edit_post", src) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to read the source of this translation.")
		return
	}
	if !gooikeAllow("pll-mt-copy:"+itoa64(u.ID), pllMTCopyPerMin) {
		fail("Too many machine translations. Please wait a minute.")
		return
	}
	// The previous text stays available as a revision.
	_, _ = s.store.Pool.Exec(c, `INSERT INTO post_revisions(post_id, author_id, title, excerpt, content) VALUES ($1, $2, $3, $4, $5)`,
		p.ID, u.ID, p.Title, p.Excerpt, p.Content)
	if err := s.pllTranslateFrom(c, src, p, s.pllMTTr(c, from, to)); err != nil {
		fail("Machine translation failed: " + err.Error())
		return
	}
	if fresh, err := s.store.GetPost(c, p.ID); err == nil {
		s.afterPostSave(c, fresh)
	}
	c.Redirect(http.StatusFound, back+"&pll_mt=done")
}

// golylangEditNotices are the edit screen notices of "Machine translate again".
func (s *Server) golylangEditNotices(c *gin.Context) []notice {
	switch c.Query("pll_mt") {
	case "done":
		return []notice{{Type: "success", Message: "The translation was replaced by a new machine translation of its source. The previous text is kept as a revision.", Dismissible: true}}
	case "error":
		return []notice{{Type: "error", Message: template.HTML(template.HTMLEscapeString(c.Query("pll_err"))), Dismissible: true}}
	}
	return nil
}
