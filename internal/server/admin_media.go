package server

import (
	"fmt"
	"html/template"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/media"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

type mediaRow struct {
	ID                     int64
	Title, FileName, Thumb string
	IsIcon                 bool
	EditURL, DeleteURL     string
	ViewURL, FileURL       string
	Author, AuthorURL      string
	ParentTitle, ParentURL string
	Date                   string
	CanEdit                bool
	PluginCols             []template.HTML // Pursae Multi Language: one cell per language
}

type mediaListData struct {
	Rows        []mediaRow
	Search      string
	Filter      string
	Month       string
	Months      []monthOption
	Pagination  pagination
	BulkNonce   string
	Grid        bool
	PluginHeads []template.HTML // Pursae Multi Language: one column per language
}

// ColSpan is the column count of the list table (the empty row spans it).
func (d mediaListData) ColSpan() int { return 6 + len(d.PluginHeads) }

func (s *Server) mediaMode(c *gin.Context) string {
	u := currentUser(c)
	if m := c.Query("mode"); m == "grid" || m == "list" {
		_ = s.setUserMeta(c, u, "media_library_mode", m)
		return m
	}
	if m, ok := u.Meta["media_library_mode"].(string); ok && m != "" {
		return m
	}
	return "grid"
}

// mediaLibrary is upload.php in grid (wp.media "manage" frame) or list mode.
func (s *Server) mediaLibrary(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("upload_files", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to upload files.")
		return
	}
	if a := c.Query("action"); a == "delete" || c.Query("action2") == "delete" {
		s.mediaBulkDelete(c)
		return
	}
	var notices []notice
	if n, _ := strconv.Atoi(c.Query("deleted")); n > 0 {
		msg := "Media file permanently deleted."
		if n > 1 {
			msg = strconv.Itoa(n) + " media files permanently deleted."
		}
		notices = append(notices, notice{Type: "success", Message: template.HTML(msg), Dismissible: true})
	}
	if s.mediaMode(c) == "grid" {
		s.enqueueMedia(c, 0)
		a := s.pageAssets(c)
		a.Script("media-grid", "media")
		queryVars := "{}"
		if item := c.Query("item"); item != "" {
			queryVars = `{"item":"` + template.JSEscapeString(item) + `"}`
		}
		a.AddBefore("media-grid", `var _wpMediaGridSettings = {"adminUrl":"\/wp-admin\/","queryVars":`+queryVars+`};`)
		s.adminPage(c, "upload", "Media Library", "upload-php", "admin/media-grid", mediaListData{Search: c.Query("s"), Grid: true}, notices...)
		return
	}

	ctx := c.Request.Context()
	page, _ := strconv.Atoi(c.Query("paged"))
	if page < 1 {
		page = 1
	}
	q := store.AttachmentQuery{Search: strings.TrimSpace(c.Query("s")), Page: page, PerPage: 20,
		OrderBy: c.DefaultQuery("orderby", "date"), Order: c.DefaultQuery("order", "desc")}
	filter := c.Query("attachment-filter")
	switch {
	case strings.HasPrefix(filter, "post_mime_type:"):
		q.MimePrefix = []string{strings.TrimPrefix(filter, "post_mime_type:")}
	case filter == "detached":
		zero := int64(0)
		q.PostID = &zero
	case filter == "mine":
		q.AuthorID = u.ID
	}
	if m := c.Query("m"); len(m) == 6 {
		q.Year, _ = strconv.Atoi(m[:4])
		q.Month, _ = strconv.Atoi(m[4:])
	}
	list, total, err := s.store.QueryAttachments(ctx, q)
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	d := mediaListData{Search: q.Search, Filter: filter, Month: c.Query("m"), BulkNonce: s.nonce(c, "bulk-media")}
	months, _ := s.store.AttachmentMonths(ctx)
	for _, m := range months {
		d.Months = append(d.Months, monthOption{fmt.Sprintf("%d%02d", m.Year, m.Month), time.Month(m.Month).String() + " " + strconv.Itoa(m.Year)})
	}
	loc := s.loc()
	// Uploaders (display fields only) and parent posts load with one query each.
	var userIDs, parentIDs []int64
	for _, a := range list {
		userIDs = append(userIDs, a.UploaderID)
		if a.PostID != 0 {
			parentIDs = append(parentIDs, a.PostID)
		}
	}
	authors, _ := s.store.UserNames(ctx, userIDs)
	parents, _ := s.store.PostsByIDs(ctx, parentIDs)
	for _, a := range list {
		r := mediaRow{ID: a.ID, Title: a.Title, FileName: path.Base(a.FilePath), FileURL: s.uploadURL(c, a.FilePath),
			EditURL: "/wp-admin/post?post=" + itoa64(a.ID) + "&action=edit", ViewURL: s.links(c).HomeURL("/?attachment_id=" + itoa64(a.ID)),
			DeleteURL: "/wp-admin/post?action=delete&post=" + itoa64(a.ID) + "&_wpnonce=" + s.nonce(c, "delete-post_"+itoa64(a.ID)),
			Date:      wp.FormatDate(a.CreatedAt.In(loc), "Y/m/d"), CanEdit: a.UploaderID == u.ID || u.Can("edit_others_posts", nil)}
		if r.Title == "" {
			r.Title = "(no title)"
		}
		switch {
		case a.Variant("thumbnail") != nil:
			r.Thumb = s.uploadURL(c, a.Variant("thumbnail").FilePath)
		case strings.HasPrefix(a.Mime, "image/"):
			r.Thumb = r.FileURL
		default:
			r.Thumb, r.IsIcon = mimeIcon(a.Mime), true
		}
		if author := authors[a.UploaderID]; author != nil {
			r.Author, r.AuthorURL = author.DisplayName, "/wp-admin/upload?author="+itoa64(author.ID)
		}
		if a.PostID != 0 {
			if p := parents[a.PostID]; p != nil {
				r.ParentTitle, r.ParentURL = defaultStr(p.Title, "(no title)"), "/wp-admin/post?post="+itoa64(p.ID)+"&action=edit"
			}
		}
		d.Rows = append(d.Rows, r)
	}
	s.golylangMediaColumns(c, &d, list)
	d.Pagination = newPagination(total, 20, page, *c.Request.URL)
	s.adminPage(c, "upload", "Media Library", "upload-php", "admin/media-list", d, notices...)
}

func (s *Server) mediaBulkDelete(c *gin.Context) {
	u := currentUser(c)
	if !s.checkNonce(c, "bulk-media") {
		s.denyNonce(c)
		return
	}
	n := 0
	for _, raw := range c.QueryArray("media[]") {
		id, _ := strconv.ParseInt(raw, 10, 64)
		a, err := s.store.GetAttachment(c, id)
		if err != nil || (a.UploaderID != u.ID && !u.Can("delete_others_posts", nil)) {
			continue
		}
		s.deleteAttachmentFiles(a)
		if s.store.DeleteAttachment(c, id) == nil {
			n++
		}
	}
	c.Redirect(http.StatusFound, "/wp-admin/upload?mode=list&deleted="+strconv.Itoa(n))
}

type mediaNewData struct {
	Nonce        string
	UploaderInit template.JS
	MaxSize      string
	Error        string
	LargeW       int
	LargeH       int
}

// mediaNew is media-new.php: the drag-and-drop uploader with the browser uploader fallback.
func (s *Server) mediaNew(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("upload_files", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to upload files.")
		return
	}
	d := mediaNewData{Nonce: s.nonce(c, "media-form"), MaxSize: media.HumanSize(maxUploadBytes),
		LargeW: s.settings.Int("large_size_w"), LargeH: s.settings.Int("large_size_h")}
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "media-form") {
			s.denyNonce(c)
			return
		}
		if _, err := s.saveUpload(c, "async-upload", 0); err != nil {
			d.Error = err.Error()
		} else {
			c.Redirect(http.StatusFound, "/wp-admin/upload")
			return
		}
	}
	init := fmt.Sprintf(`{"browse_button":"plupload-browse-button","container":"plupload-upload-ui","drop_element":"drag-drop-area","file_data_name":"async-upload","url":"\/wp-admin\/async-upload","filters":{"max_file_size":"%db"},"multipart_params":{"post_id":0,"_wpnonce":"%s","type":"","tab":"","short":"1"}}`,
		maxUploadBytes, d.Nonce)
	d.UploaderInit = template.JS(init)
	s.pageAssets(c).Script("plupload-handlers", "image-edit")
	s.adminPage(c, "media-new", "Upload Media", "media-new-php", "admin/media-new", d)
}

type attachmentEditData struct {
	A                     *store.Attachment
	Nonce                 string
	FileURL, FileName     string
	FileType, FileSize    string
	Dimensions, Uploaded  string
	Uploader, UploaderURL string
	DeleteURL, ViewURL    string
	Preview               string
	IsImage               bool
	PluginBoxes           template.HTML // Pursae Multi Language: Translations box
}

// attachmentEdit is post.php?action=edit for an attachment.
func (s *Server) attachmentEdit(c *gin.Context, a *store.Attachment, notices ...notice) {
	d := attachmentEditData{A: a, Nonce: s.nonce(c, "update-post_"+itoa64(a.ID)), FileURL: s.uploadURL(c, a.FilePath),
		FileName: path.Base(a.FilePath), FileType: a.Mime, FileSize: media.HumanSize(a.FileSize),
		Uploaded: wp.FormatDate(a.CreatedAt.In(s.loc()), "F j, Y"), IsImage: strings.HasPrefix(a.Mime, "image/"),
		DeleteURL: "/wp-admin/post?action=delete&post=" + itoa64(a.ID) + "&_wpnonce=" + s.nonce(c, "delete-post_"+itoa64(a.ID)),
		ViewURL:   s.links(c).HomeURL("/?attachment_id=" + itoa64(a.ID))}
	d.FileType = strings.ToUpper(strings.SplitN(a.Mime, "/", 2)[len(strings.SplitN(a.Mime, "/", 2))-1])
	if a.Width > 0 {
		d.Dimensions = fmt.Sprintf("%d by %d pixels", a.Width, a.Height)
	}
	d.Preview = d.FileURL
	if v := a.Variant("large"); v != nil {
		d.Preview = s.uploadURL(c, v.FilePath)
	} else if v := a.Variant("medium_large"); v != nil {
		d.Preview = s.uploadURL(c, v.FilePath)
	}
	if author, err := s.store.UserByID(c, a.UploaderID); err == nil {
		d.Uploader, d.UploaderURL = author.DisplayName, "/wp-admin/upload?author="+itoa64(author.ID)
	}
	if c.Query("message") == "4" {
		notices = append(notices, notice{Type: "success", Message: "Media file updated.", Dismissible: true})
	}
	d.PluginBoxes = s.golylangMediaBox(c, a)
	s.pageAssets(c).Script("clipboard", "wp-a11y")
	s.adminPage(c, "upload", "Edit Media", "post-php post-type-attachment", "admin/attachment-edit", d, notices...)
}

func (s *Server) attachmentSave(c *gin.Context, a *store.Attachment) {
	u := currentUser(c)
	if !s.checkNonce(c, "update-post_"+itoa64(a.ID)) {
		s.denyNonce(c)
		return
	}
	if a.UploaderID != u.ID && !u.Can("edit_others_posts", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to edit this item.")
		return
	}
	a.Title = strings.TrimSpace(c.PostForm("post_title"))
	a.Caption = c.PostForm("excerpt")
	a.Description = c.PostForm("content")
	a.Alt = c.PostForm("_wp_attachment_image_alt")
	ksesAttachmentText(u, a)
	if err := s.store.UpdateAttachment(c, a); err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	s.golylangMediaSaved(c, a) // pll_i18n[lang][field]
	c.Redirect(http.StatusFound, "/wp-admin/post?post="+itoa64(a.ID)+"&action=edit&message=4")
}
