package server

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// A "Storage" column in the Media Library list: per attachment, whether its files are in the
// bucket and the row actions to offload them, bring them back or remove the local copies.

func gocdnMediaNonce(id int64) string { return "gocdn-media_" + itoa64(id) }

// gocdnMediaColumns adds the Storage column for administrators while the plugin tracks files.
func (s *Server) gocdnMediaColumns(c *gin.Context, d *mediaListData, list []*store.Attachment) {
	if len(list) == 0 || !s.gocdnTracking() || !currentUser(c).Can("manage_options", nil) {
		return
	}
	var paths []string
	for _, a := range list {
		paths = append(paths, gocdnAttachmentFiles(a)...)
	}
	have, err := s.gocdnItemsByPaths(c.Request.Context(), paths)
	if err != nil {
		return
	}
	d.PluginHeads = append(d.PluginHeads, `<th scope="col" class="manage-column column-gocdn">Storage</th>`)
	for i, a := range list {
		if i >= len(d.Rows) {
			break
		}
		files := gocdnAttachmentFiles(a)
		var in, removed int
		var bucket, key string
		for _, f := range files {
			if it := have[f]; it != nil {
				in++
				if it.LocalRemoved {
					removed++
				}
				if f == a.FilePath {
					bucket, key = it.Bucket, it.Key
				}
			}
		}
		link := func(act, label string) string {
			return fmt.Sprintf(`<a href="/wp-admin/gocdn?action=%s&amp;media=%d&amp;_wpnonce=%s">%s</a>`, act, a.ID, s.nonce(c, gocdnMediaNonce(a.ID)), label)
		}
		var cell, acts string
		switch {
		case in == 0:
			cell, acts = "Local only", link("media-offload", "Offload")
		case in < len(files):
			cell, acts = fmt.Sprintf("Partly in the bucket (%d of %d files)", in, len(files)), link("media-offload", "Offload the rest")
		default:
			cell = "In the bucket"
			if removed == in {
				cell += " (no local copy)"
				acts = link("media-download", "Download back")
			} else {
				acts = link("media-remove-local", "Remove local copy") + " | " + link("media-download", "Download back")
			}
			if bucket != "" {
				cell += `<br><code translate="no">` + html.EscapeString(bucket+"/"+key) + `</code>`
			}
		}
		d.Rows[i].PluginCols = append(d.Rows[i].PluginCols, template.HTML(`<td class="gocdn column-gocdn" data-colname="Storage">`+cell+`<div class="row-actions">`+acts+`</div></td>`))
	}
}

// gocdnMediaAction runs a per-attachment action from the Storage column and returns to the list.
func (s *Server) gocdnMediaAction(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Query("media"), 10, 64)
	back := "/wp-admin/upload"
	if !s.checkNonceValue(c, c.Query("_wpnonce"), gocdnMediaNonce(id)) {
		s.denyNonce(c)
		return
	}
	ctx := c.Request.Context()
	a, err := s.store.GetAttachment(ctx, id)
	if err != nil || a == nil {
		c.Redirect(http.StatusFound, back+"?gocdn-media-error="+url.QueryEscape("That attachment no longer exists."))
		return
	}
	st := s.gocdnLoad()
	var msg string
	switch c.Query("action") {
	case "media-offload":
		cl, cerr := s.gocdnClient(st)
		if cerr == nil {
			_, cerr = s.gocdnOffloadAttachment(ctx, st, cl, a)
		}
		err, msg = cerr, "The files were copied to the bucket."
	case "media-download":
		items, ierr := s.gocdnItemsForMedia(ctx, id)
		err = ierr
		for _, it := range items {
			if err == nil && it.LocalRemoved {
				if err = s.gocdnDownload(ctx, st, it); err == nil {
					_, err = s.store.Pool.Exec(ctx, `UPDATE gocdn_items SET local_removed = false WHERE id = $1`, it.ID)
				}
			}
		}
		msg = "The files are back on the server."
	case "media-remove-local":
		s.gocdnRemoveLocal(ctx, id)
		msg = "The local copies were removed."
	default:
		c.Redirect(http.StatusFound, back)
		return
	}
	if err != nil {
		c.Redirect(http.StatusFound, back+"?gocdn-media-error="+url.QueryEscape(err.Error()))
		return
	}
	c.Redirect(http.StatusFound, back+"?gocdn-media-msg="+url.QueryEscape(msg))
}
