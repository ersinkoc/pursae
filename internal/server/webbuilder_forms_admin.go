package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
)

// Stored form submissions on Settings › Web Builder: the newest wbFormListPer rows with a delete
// action (selected rows, or all of them).

// wbSubmissionRow is one row of the submissions table.
type wbSubmissionRow struct {
	ID      int64
	Date    string
	Form    string
	PageURL string
	Fields  []wbSubmissionField
	Mailed  bool
	IP      string
}

type wbSubmissionField struct{ Name, Value string }

func (s *Server) wbLoadSubmissions(c *gin.Context, d *wbSettingsData) {
	wb := s.store.WebBuilder()
	n, err := wb.CountSubmissions(c)
	if err != nil {
		return
	}
	d.SubmissionTotal = n
	list, err := wb.Submissions(c, wbFormListPer, 0)
	if err != nil {
		return
	}
	d.SubmissionMore = n > len(list)
	for _, x := range list {
		d.Submissions = append(d.Submissions, wbSubmissionRowOf(x))
	}
}

func wbSubmissionRowOf(x store.WebBuilderSubmission) wbSubmissionRow {
	row := wbSubmissionRow{ID: x.ID, Date: x.CreatedAt.Format(time.DateTime), Form: x.FormTitle, PageURL: x.PageURL, Mailed: x.Mailed, IP: x.IP}
	for _, name := range []string{"name", "email", "phone", "date", "choice", "message"} {
		if v := x.Fields[name]; v != "" {
			row.Fields = append(row.Fields, wbSubmissionField{Name: name, Value: v})
		}
	}
	return row
}

// wbSubmissionsAction deletes the selected submissions (wb_submissions=delete) or all (=delete-all).
func (s *Server) wbSubmissionsAction(c *gin.Context) {
	var err error
	switch c.PostForm("wb_submissions") {
	case "delete":
		var ids []int64
		for _, v := range c.PostFormArray("submission[]") {
			if id, e := strconv.ParseInt(strings.TrimSpace(v), 10, 64); e == nil && id > 0 {
				ids = append(ids, id)
			}
		}
		_, err = s.store.WebBuilder().DeleteSubmissions(c, ids)
	case "delete-all":
		_, err = s.store.WebBuilder().DeleteAllSubmissions(c)
	default:
		c.String(http.StatusBadRequest, "Unknown action.")
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, wbSettingsURL+"&submissions-deleted=1")
}
