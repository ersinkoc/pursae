package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/auth"
	"wpgo/internal/store"
)

// The Import screen runs in three steps like the WordPress importer: upload the WXR file, assign
// the authors and choose whether to download the attachments, then the import runs as a background
// job ("wxr_import") while the screen shows its progress and, at the end, its report. The job can
// be retried (Tools › Scheduled Actions): items that were imported already are found again and
// skipped, so a second run only adds what is missing.

const wxrImportMax = 256 << 20

var wxrTokenRE = regexp.MustCompile(`^[a-f0-9]{32}$`)

// wxrImportDir keeps uploaded files until their job has run. It is not under the uploads directory,
// which the web server serves: an export holds drafts and private content.
func (s *Server) wxrImportDir() string {
	return filepath.Join(filepath.Dir(filepath.Clean(s.cfg.UploadsDir)), "wxr-imports")
}

func (s *Server) wxrImportFile(token string) string {
	return filepath.Join(s.wxrImportDir(), token+".xml")
}

// wxrImportState is what the progress screen shows, kept as a transient for a week.
type wxrImportState struct {
	State  string        `json:"state"` // queued, running, done, failed
	Error  string        `json:"error,omitempty"`
	Report *importReport `json:"report,omitempty"`
}

func wxrStateKey(token string) string { return "wxr_import_" + token }

type wxrImportPayload struct {
	Token   string                     `json:"token"`
	UserID  int64                      `json:"user_id"`
	Fetch   bool                       `json:"fetch"`
	Authors map[string]wxrAuthorChoice `json:"authors"`
}

type wxrAuthorRow struct {
	Index                     int
	Login, Email, DisplayName string
	Selected                  string // "", "new" or a user id
	Exists                    bool
}

type wxrUserOption struct {
	ID    int64
	Label string
}

// importScreen is import.php with the WordPress importer built in.
func (s *Server) importScreen(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("import", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to import content into this site.")
		return
	}
	d := map[string]any{"Nonce": s.nonce(c, "import-upload"), "Step": ""}
	if run := c.Query("run"); run != "" && c.Request.Method == http.MethodGet {
		s.wxrProgress(c, run, d)
		return
	}
	if c.Request.Method == http.MethodPost {
		if c.PostForm("import_id") != "" {
			s.wxrQueue(c, u, d)
			return
		}
		if !s.checkNonce(c, "import-upload") {
			s.denyNonce(c)
			return
		}
		s.wxrUpload(c, d)
	}
	s.adminPage(c, "import", "Import", "import-php", "admin/import", d)
}

// wxrUpload is step one: the file is stored, checked, and the author screen follows.
func (s *Server) wxrUpload(c *gin.Context, d map[string]any) {
	f, _, err := c.Request.FormFile("import")
	if err != nil {
		d["Error"] = "No file was uploaded."
		return
	}
	defer f.Close()
	if err := os.MkdirAll(s.wxrImportDir(), 0o700); err != nil {
		d["Error"] = "Sorry, there has been an error. " + err.Error()
		return
	}
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		d["Error"] = "Sorry, there has been an error. " + err.Error()
		return
	}
	token := hex.EncodeToString(rnd[:])
	path := s.wxrImportFile(token)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		d["Error"] = "Sorry, there has been an error. " + err.Error()
		return
	}
	n, err := io.Copy(out, io.LimitReader(f, wxrImportMax+1))
	out.Close()
	if err != nil || n > wxrImportMax {
		os.Remove(path)
		d["Error"] = "The file is larger than 256 MB."
		return
	}
	doc, err := wxrParseFile(path)
	if err != nil {
		os.Remove(path)
		d["Error"] = "Sorry, there has been an error. " + err.Error()
		return
	}
	ctx := c.Request.Context()
	var rows []wxrAuthorRow
	for i, a := range doc.Channel.Authors {
		row := wxrAuthorRow{Index: i, Login: a.Login, Email: a.Email, DisplayName: a.DisplayName, Selected: "new"}
		if ex, err := s.store.UserByLoginOrEmail(ctx, a.Login); err == nil {
			row.Selected, row.Exists = strconv.FormatInt(ex.ID, 10), true
		}
		rows = append(rows, row)
	}
	var users []wxrUserOption
	if list, err := s.store.Authors(ctx); err == nil {
		for _, au := range list {
			users = append(users, wxrUserOption{ID: au.ID, Label: au.Login + " (" + au.DisplayName + ")"})
		}
	}
	d["Step"] = "authors"
	d["Token"] = token
	d["ImportNonce"] = s.nonce(c, "import-wxr")
	d["Authors"] = rows
	d["Users"] = users
	d["Counts"] = fmt.Sprintf("%d items", len(doc.Channel.Items))
}

func wxrParseFile(path string) (*wxrDoc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var doc wxrDoc
	dec := xml.NewDecoder(f)
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("this does not appear to be a WXR file: %v", err)
	}
	return &doc, nil
}

// wxrQueue is step two: the author choices are read from the form and the job is queued.
func (s *Server) wxrQueue(c *gin.Context, u *store.User, d map[string]any) {
	if !s.checkNonce(c, "import-wxr") {
		s.denyNonce(c)
		return
	}
	token := c.PostForm("import_id")
	if !wxrTokenRE.MatchString(token) {
		d["Error"] = "Invalid import file."
		s.adminPage(c, "import", "Import", "import-php", "admin/import", d)
		return
	}
	if _, err := os.Stat(s.wxrImportFile(token)); err != nil {
		d["Error"] = "The uploaded file is no longer available. Please upload it again."
		s.adminPage(c, "import", "Import", "import-php", "admin/import", d)
		return
	}
	p := wxrImportPayload{Token: token, UserID: u.ID, Fetch: c.PostForm("fetch_attachments") == "1", Authors: map[string]wxrAuthorChoice{}}
	for i, login := range c.PostFormArray("imported_authors") {
		if login == "" {
			continue
		}
		pick := c.PostForm("user_map[" + strconv.Itoa(i) + "]")
		switch {
		case pick == "new":
			nl := strings.TrimSpace(c.PostForm("user_new[" + strconv.Itoa(i) + "]"))
			if nl == "" {
				nl = login
			}
			p.Authors[login] = wxrAuthorChoice{NewLogin: nl}
		case pick == "": // no choice sent: match by login like the old importer
		case pick == "0":
			p.Authors[login] = wxrAuthorChoice{}
		default:
			id, _ := strconv.ParseInt(pick, 10, 64)
			if id > 0 {
				if _, err := s.store.UserByID(c, id); err == nil {
					p.Authors[login] = wxrAuthorChoice{UserID: id}
				}
			}
		}
	}
	_ = s.store.SetTransient(c, wxrStateKey(token), wxrImportState{State: "queued"}, 7*24*time.Hour)
	if err := s.queueJob(c, "wxr_import", p, time.Time{}); err != nil {
		d["Error"] = "Sorry, there has been an error. " + err.Error()
		s.adminPage(c, "import", "Import", "import-php", "admin/import", d)
		return
	}
	c.Redirect(http.StatusSeeOther, "/wp-admin/import?run="+token)
}

// wxrProgress shows a queued or running import and, once it is over, its report.
func (s *Server) wxrProgress(c *gin.Context, token string, d map[string]any) {
	var st wxrImportState
	if !wxrTokenRE.MatchString(token) {
		d["Error"] = "Invalid import."
	} else if ok, _ := s.store.GetTransient(c, wxrStateKey(token), &st); !ok {
		d["Error"] = "This import is not known (or its report has expired)."
	} else {
		switch st.State {
		case "done":
			d["Report"] = st.Report
		case "failed":
			d["Error"] = "The import failed: " + st.Error
		default:
			d["Running"] = true
			d["State"] = st.State
		}
	}
	s.adminPage(c, "import", "Import", "import-php", "admin/import", d)
}

// wxrCreateAuthor creates the user a "create new user" choice asks for (role author, random
// password: the person signs in through Lost your password). An existing login or address is
// reused instead of failing the import.
func (s *Server) wxrCreateAuthor(ctx context.Context, login, email, display string) int64 {
	login = strings.TrimSpace(login)
	if login == "" {
		return 0
	}
	if ex, err := s.store.UserByLoginOrEmail(ctx, login); err == nil {
		return ex.ID
	}
	if email != "" {
		if ex, err := s.store.UserByLoginOrEmail(ctx, email); err == nil {
			return ex.ID
		}
	}
	hash, err := auth.HashPassword(auth.RandomToken(24))
	if err != nil {
		return 0
	}
	id, err := s.store.CreateUser(ctx, store.NewUser{Login: login, Email: email, PasswordHash: hash, DisplayName: wxrDefault(display, login), Role: "author"})
	if err != nil {
		return 0
	}
	return id
}

func wxrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// wxrImportJob is the background run. An error makes the job retry (and shows in Scheduled
// Actions); the file is removed once the import is over.
func (s *Server) wxrImportJob(ctx context.Context, raw map[string]any) error {
	b, _ := json.Marshal(raw)
	var p wxrImportPayload
	if err := json.Unmarshal(b, &p); err != nil || !wxrTokenRE.MatchString(p.Token) {
		return errors.New("invalid import job")
	}
	setState := func(st wxrImportState) {
		_ = s.store.SetTransient(context.WithoutCancel(ctx), wxrStateKey(p.Token), st, 7*24*time.Hour)
	}
	path := s.wxrImportFile(p.Token)
	data, err := os.ReadFile(path)
	if err != nil {
		setState(wxrImportState{State: "failed", Error: "the uploaded file is gone."})
		return nil // retrying cannot bring the file back
	}
	u, err := s.store.UserByID(ctx, p.UserID)
	if err != nil {
		setState(wxrImportState{State: "failed", Error: "the importing user no longer exists."})
		return nil
	}
	setState(wxrImportState{State: "running"})
	rep, err := s.importWXRWith(ctx, u, data, wxrImportOptions{Authors: p.Authors, FetchAttachments: p.Fetch})
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return err
		}
		// A file that is not WXR will not become one on a retry.
		setState(wxrImportState{State: "failed", Error: err.Error()})
		os.Remove(path)
		return nil
	}
	s.purgeAll(ctx)
	os.Remove(path)
	setState(wxrImportState{State: "done", Report: rep})
	return nil
}
