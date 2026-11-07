package server

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Pursae Multi Language inside Pursae Web Builder: the editor's builder menu lists the page's
// language and its translations (open) and the languages still missing (create).
//
//	GET  web-builder/v2/documents/{id}/languages      → wbLanguagesInfo
//	POST web-builder/v2/documents/{id}/translations   {lang} → the new translation (draft, doc copied)
//
// Both answer {"enabled": false} while Pursae Multi Language is off or the post type is not a
// translated one, so the editor shows nothing then.

type wbLangRef struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
	Flag string `json:"flag"` // emoji flag
	RTL  bool   `json:"rtl,omitempty"`
}

type wbLangTranslation struct {
	wbLangRef
	PostID  int64  `json:"postId"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	EditURL string `json:"editUrl"`
	Current bool   `json:"current,omitempty"`
	// ReadOnly: the user may not edit this translation; title, status and editUrl are left empty.
	ReadOnly bool `json:"readOnly,omitempty"`
}

type wbLanguagesInfo struct {
	Enabled      bool                `json:"enabled"`
	CanTranslate bool                `json:"canTranslate"`
	Current      *wbLangRef          `json:"current,omitempty"`
	Translations []wbLangTranslation `json:"translations"`
	Missing      []wbLangRef         `json:"missing"`
}

// wbCanCreateLike reports whether u may create a new post of p's type: the type's create capability
// (edit_pages for pages, edit_posts for the rest), the one the REST create route and the admin "Add
// New" screens test. Being able to edit p is not enough: it may have been granted per user.
func wbCanCreateLike(u *store.User, p *store.Post) bool {
	return u != nil && p != nil && u.Can("edit_"+restBaseFor(p.Type), nil)
}

// wbTranslationOf looks up the live translation id of lang in tr. A translation that is gone or in the
// trash does not count (gone = true, so the language is offered again); a failed read is returned as err.
func (s *Server) wbTranslationOf(c *gin.Context, tr map[string]int64, lang string) (post *store.Post, gone bool, err error) {
	id := tr[lang]
	if id <= 0 {
		return nil, true, nil
	}
	got, err := s.store.GetPost(c, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, true, nil
	case err != nil:
		return nil, false, err
	case got.Status == "trash":
		return got, true, nil
	}
	return got, false, nil
}

func wbLangRefOf(l *pllLanguage) wbLangRef {
	return wbLangRef{Slug: l.Slug, Name: l.Name, Flag: l.FlagEmoji(), RTL: l.RTL}
}

// wbLanguagesFor builds the panel data of p (Theme Builder templates only show their language).
func (s *Server) wbLanguagesFor(c *gin.Context, u *store.User, p *store.Post) wbLanguagesInfo {
	info := wbLanguagesInfo{Translations: []wbLangTranslation{}, Missing: []wbLangRef{}}
	if !s.golylangOn() || !s.pllTranslatedType(p.Type) {
		return info
	}
	langs := s.pllActiveLanguages(c)
	if len(langs) == 0 {
		return info
	}
	info.Enabled = true
	s.pllEnsureLang(c, p)
	cur := s.pllObjLang(c, pllPost, p.ID)
	if l := s.pllLang(c, cur); l != nil {
		ref := wbLangRefOf(l)
		info.Current = &ref
	}
	tr := s.pllTranslations(c, pllPost, p.ID)
	tr[cur] = p.ID
	info.CanTranslate = !wbIsTemplate(p) && wbCanCreateLike(u, p)
	for _, l := range langs {
		t := &store.Post{ID: p.ID, Title: p.Title, Status: p.Status}
		if tr[l.Slug] != p.ID {
			got, gone, err := s.wbTranslationOf(c, tr, l.Slug)
			if err != nil {
				continue
			}
			if gone {
				// No translation, or one that was trashed: it can be created (again).
				if !wbIsTemplate(p) {
					info.Missing = append(info.Missing, wbLangRefOf(l))
				}
				continue
			}
			t = got
		}
		id := tr[l.Slug]
		entry := wbLangTranslation{wbLangRef: wbLangRefOf(l), PostID: id, Title: t.Title, Status: t.Status,
			EditURL: wbEditURL(id), Current: id == p.ID}
		if id != p.ID && !u.Can("edit_post", t) {
			// Listed, so the language does not vanish from the panel, but nothing of the post is shown.
			entry.Title, entry.Status, entry.EditURL, entry.ReadOnly = "", "", "", true
		}
		info.Translations = append(info.Translations, entry)
	}
	return info
}

// wbRESTLanguages is GET documents/{id}/languages.
func (s *Server) wbRESTLanguages(c *gin.Context, r *restRequest) {
	u, p := s.wbPost(c, r)
	if p == nil {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, s.wbLanguagesFor(c, u, p))
}

// wbRESTTranslate is POST documents/{id}/translations: a draft translation of the page in lang that
// starts from the page's builder doc (Pursae Multi Language "Duplicate content"), linked to it.
func (s *Server) wbRESTTranslate(c *gin.Context, r *restRequest) {
	u, p := s.wbPost(c, r)
	if p == nil {
		return
	}
	if wbIsTemplate(p) || !s.golylangOn() || !s.pllTranslatedType(p.Type) {
		restErr(c, http.StatusBadRequest, webbuilder.ErrNotBuilder, "This page cannot be translated.")
		return
	}
	if !wbCanCreateLike(u, p) {
		wbForbidden(c, u)
		return
	}
	slug, _ := r.Body["lang"].(string)
	l := s.pllLang(c, strings.TrimSpace(slug))
	if l == nil || !l.Active {
		restErr(c, http.StatusBadRequest, "wb_bad_language", "Unknown language.")
		return
	}
	s.pllEnsureLang(c, p)
	tr := s.pllTranslations(c, pllPost, p.ID)
	tr[s.pllObjLang(c, pllPost, p.ID)] = p.ID
	// A translation in the trash (or gone) is replaced by the new one; a live one is a conflict.
	if _, gone, err := s.wbTranslationOf(c, tr, l.Slug); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not read the translations.")
		return
	} else if !gone {
		restErr(c, http.StatusConflict, "wb_translation_exists", "This page already has a translation in that language.")
		return
	}
	cp := &store.Post{Type: p.Type, AuthorID: u.ID, Title: p.Title, Slug: p.Slug, Status: "draft",
		Content: p.Content, Excerpt: p.Excerpt, CommentStatus: p.CommentStatus, PingStatus: p.PingStatus,
		Format: p.Format, Meta: map[string]any{}}
	if err := s.store.InsertPost(c, cp); err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not create the translation.")
		return
	}
	tr[l.Slug] = cp.ID
	if err := s.pllSetLang(c, pllPost, cp.ID, l.Slug); err != nil {
		s.wbDiscardCopy(c, cp)
		restErr(c, http.StatusInternalServerError, "db_error", "Could not create the translation.")
		return
	}
	if err := s.pllSaveTranslations(c, pllPost, tr); err != nil {
		s.wbDiscardCopy(c, cp)
		restErr(c, http.StatusInternalServerError, "db_error", "Could not link the translation.")
		return
	}
	s.pllCopyForTranslation(c, p, cp, l, true)
	if fresh, err := s.store.GetPost(c, cp.ID); err == nil {
		cp = fresh
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, wbLangTranslation{wbLangRef: wbLangRefOf(l), PostID: cp.ID, Title: cp.Title,
		Status: cp.Status, EditURL: wbEditURL(cp.ID)})
}

// wbDiscardCopy removes a translation draft that could not be linked, so the editor never opens an
// unlinked copy (and the language stays creatable). Its language row goes with it (ON DELETE CASCADE);
// when the delete fails the draft is trashed and taken out of the group, which the panel treats as missing.
func (s *Server) wbDiscardCopy(c *gin.Context, cp *store.Post) {
	if err := s.store.DeletePost(c, cp.ID); err != nil {
		log.Printf("web builder: discard unlinked translation %d: %v", cp.ID, err)
		_ = s.store.TrashPost(c, cp.ID)
		_ = s.pllUnlink(c, pllPost, cp.ID)
	}
}

// wbPluginPanels is GET documents/{id}/plugins: which built-in plugins give the editor a tab in its
// "Page tools" dialog (Pursae Multi Language, Pursae SEO). The tabs talk to the plugins' own REST
// routes (seogo/v1/meta, documents/{id}/languages), which enforce their own capabilities.
type wbPluginPanels struct {
	Languages bool `json:"languages"`
	SEO       bool `json:"seo"`
	SEOEdit   bool `json:"seoEdit"`
	// SEOAdvanced / SEOSocial: the robots + canonical fields and the social fields are saved only
	// with seogo_onpage_advanced / seogo_onpage_social; the dialog disables them without.
	SEOAdvanced bool `json:"seoAdvanced"`
	SEOSocial   bool `json:"seoSocial"`
}

func (s *Server) wbPanelsFor(c *gin.Context, u *store.User, p *store.Post) wbPluginPanels {
	out := wbPluginPanels{}
	if s.golylangOn() && s.pllTranslatedType(p.Type) && len(s.pllActiveLanguages(c)) > 0 {
		out.Languages = true
	}
	if s.seogoOn() && !wbIsTemplate(p) && s.seogoCan(u, "seogo_onpage_general") {
		out.SEO, out.SEOEdit = true, true
		out.SEOAdvanced = s.seogoCan(u, "seogo_onpage_advanced")
		out.SEOSocial = s.seogoCan(u, "seogo_onpage_social")
	}
	return out
}

func (s *Server) wbRESTPlugins(c *gin.Context, r *restRequest) {
	u, p := s.wbPost(c, r)
	if p == nil {
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, s.wbPanelsFor(c, u, p))
}
