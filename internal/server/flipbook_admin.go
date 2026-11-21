package server

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// The Flipbook screens (wp-admin/admin?page=pursae-flipbook): the list of books with their
// shortcodes, Add New (the Media Library modal, PDFs only), the edit screen with a live preview,
// and a full-screen viewer over the admin. Anyone who may upload files manages flipbooks.

type flipbookRow struct {
	flipbook
	Shortcode  string
	FullURL    string // /flipbook/N
	AbsFullURL string // the share link
	EditURL    string
	DeleteURL  string
	Date       string
}

type flipbookAdminData struct {
	View      string // list, edit
	Items     []flipbookRow
	Edit      flipbookRow
	Nonce     string
	AutoOpen  bool // Add New: the media modal opens on load
	AssetsURL string
	Version   string
}

func (s *Server) flipbookRowOf(c *gin.Context, b flipbook) flipbookRow {
	base := "/wp-admin/admin?page=" + flipbookSlug
	r := flipbookRow{flipbook: b, Shortcode: flipbookShortcodeText(b.ID), FullURL: flipbookPageURL(b.ID),
		EditURL:   base + "&action=edit&id=" + strconv.Itoa(b.ID),
		DeleteURL: base + "&action=delete&id=" + strconv.Itoa(b.ID) + "&_wpnonce=" + s.nonce(c, flipbookNonce+"-delete-"+strconv.Itoa(b.ID))}
	r.AbsFullURL = flipbookAbsURL(s.links(c).HomeURL("/"), r.FullURL)
	if t, err := time.Parse(time.RFC3339, b.Created); err == nil {
		r.Date = t.In(s.loc()).Format("2006/01/02 15:04")
	}
	return r
}

// flipbookAdmin serves admin?page=pursae-flipbook.
func (s *Server) flipbookAdmin(c *gin.Context) {
	u := currentUser(c)
	if !s.flipbookOn() || u == nil || !u.Can("upload_files", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	base := "/wp-admin/admin?page=" + flipbookSlug
	if c.Request.Method == http.MethodPost {
		s.flipbookAdminPost(c, base)
		return
	}
	id, _ := strconv.Atoi(c.Query("id"))
	switch c.Query("action") {
	case "delete":
		if !s.checkNonce(c, flipbookNonce+"-delete-"+strconv.Itoa(id)) {
			s.denyNonce(c)
			return
		}
		err := s.flipbookUpdate(c, func(st *flipbookStore) error {
			st.Items = slices.DeleteFunc(st.Items, func(b flipbook) bool { return b.ID == id })
			return nil
		})
		if err != nil {
			c.Redirect(http.StatusFound, base+"&pfb-error="+url.QueryEscape(err.Error()))
			return
		}
		c.Redirect(http.StatusFound, base+"&pfb-msg=deleted")
		return
	}

	d := flipbookAdminData{View: "list", Nonce: s.nonce(c, flipbookNonce), AssetsURL: flipbookAssets, Version: flipbookVersion}
	screen, title := "admin?page="+flipbookSlug, "Flipbooks"
	if c.Query("action") == "edit" {
		b, ok := s.flipbookGet(id)
		if !ok {
			c.Redirect(http.StatusFound, base+"&pfb-error="+url.QueryEscape("That flipbook does not exist any more."))
			return
		}
		d.View, d.Edit, title = "edit", s.flipbookRowOf(c, b), "Edit Flipbook"
	} else {
		for _, b := range s.flipbookLoad().Items {
			d.Items = append(d.Items, s.flipbookRowOf(c, b))
		}
		slices.Reverse(d.Items) // newest first
		d.AutoOpen = c.Query("action") == "new"
		if d.AutoOpen {
			screen += "&action=new"
		}
	}
	s.enqueueMedia(c, 0)

	var notices []notice
	switch c.Query("pfb-msg") {
	case "created":
		notices = append(notices, notice{Type: "success", Message: "Flipbook created. Copy its shortcode into any post or page.", Dismissible: true})
	case "saved":
		notices = append(notices, notice{Type: "success", Message: "Flipbook saved.", Dismissible: true})
	case "deleted":
		notices = append(notices, notice{Type: "success", Message: "Flipbook deleted. The PDF stays in the Media Library.", Dismissible: true})
	}
	if c.Query("activated") == "1" {
		notices = append(notices, notice{Type: "success", Message: "<strong>Pursae Flipbook is active.</strong> Click <em>Add New</em> and pick a PDF from the Media Library.", Dismissible: true})
	}
	if e := c.Query("pfb-error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(e))})
	}
	s.adminPage(c, screen, title, "toplevel_page_"+flipbookSlug, "admin/flipbook", d, notices...)
}

// flipbookAdminPost creates a book from a picked PDF or saves the edit form.
func (s *Server) flipbookAdminPost(c *gin.Context, base string) {
	if !s.checkNonce(c, flipbookNonce) {
		s.denyNonce(c)
		return
	}
	str := func(k string) string { return strings.TrimSpace(c.PostForm(k)) }
	fail := func(back string, err error) {
		c.Redirect(http.StatusFound, back+"&pfb-error="+url.QueryEscape(err.Error()))
	}
	attID, _ := strconv.ParseInt(str("attachment_id"), 10, 64)
	pdfURL := maintMediaURL(str("pdf_url"))
	if (str("op") == "create" || str("pdf_url") != "") && !flipbookIsPDF(pdfURL, str("mime")) {
		fail(base, errors.New("Please pick a PDF file."))
		return
	}
	switch str("op") {
	case "create":
		title := str("title")
		fileName := path.Base(str("file_name"))
		if title == "" {
			title = strings.TrimSuffix(fileName, path.Ext(fileName))
		}
		var id int
		err := s.flipbookUpdate(c, func(st *flipbookStore) error {
			b := flipbookNew(title, pdfURL, fileName, attID)
			b.ID = st.NextID
			st.NextID++
			st.Items = append(st.Items, b)
			id = b.ID
			return nil
		})
		if err != nil {
			fail(base, err)
			return
		}
		c.Redirect(http.StatusFound, base+"&action=edit&id="+strconv.Itoa(id)+"&pfb-msg=created")
	case "save":
		id, _ := strconv.Atoi(str("id"))
		back := base + "&action=edit&id=" + strconv.Itoa(id)
		err := s.flipbookUpdate(c, func(st *flipbookStore) error {
			for i := range st.Items {
				b := &st.Items[i]
				if b.ID != id {
					continue
				}
				if t := str("title"); t != "" {
					b.Title = t
				}
				if pdfURL != "" {
					b.PDFURL, b.AttachmentID, b.FileName = pdfURL, attID, path.Base(str("file_name"))
				}
				b.Height, _ = strconv.Atoi(str("height"))
				b.Background = str("background")
				b.Download = c.PostForm("download") == "1"
				b.Share = c.PostForm("share") == "1"
				b.Modified = time.Now().UTC().Format(time.RFC3339)
				*b = flipbookSanitize(*b)
				return nil
			}
			return errors.New("That flipbook does not exist any more.")
		})
		if err != nil {
			fail(back, err)
			return
		}
		c.Redirect(http.StatusFound, back+"&pfb-msg=saved")
	default:
		c.Redirect(http.StatusFound, base)
	}
}

// flipbookIsPDF accepts the modal's choice when it is a PDF by MIME type or extension.
func flipbookIsPDF(u, mime string) bool {
	if u == "" {
		return false
	}
	if mime == "application/pdf" {
		return true
	}
	p := u
	if pu, err := url.Parse(u); err == nil {
		p = pu.Path
	}
	return strings.EqualFold(path.Ext(p), ".pdf")
}
