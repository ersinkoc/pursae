package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Web Builder › Import HTML site (wp-admin/web-builder-import): the admin screen for the site import
// (webbuilder_siteimport.go, MCP web_builder_import_site), for people without a command line.
//
//  1. Upload: a zip of the design's folder, streamed to a private staging folder
//     (uploads/private/web-builder-imports/<id>/site/) and unpacked with the checks of
//     webbuilder_siteimport_zip.go. Nothing is written to the site yet.
//  2. Analysis: the same webbuilder.ConvertSite the import runs, side-effect free: pages and their
//     proposed addresses, header and footer clusters, missing files, external origins, inline event
//     handlers, CSP warnings, breakpoints; then the options.
//  3. Import: the staged assets become the design kit (stage + commit, as the kit REST routes do),
//     then mcpWBImportSite runs, the very code the MCP tool runs. What the import created is recorded
//     (option wb_site_imports) so it can be removed.
//  4. Compare: each page's original (served from the staging copy) beside the Pursae page.
//  5. Remove: deletes the pages and templates this import created (still carrying the import's
//     _wb_site_kit mark) and the kit when this import created it; nothing else.
//
// Uploaded HTML is served only by wbWizDesign, under a sandbox policy (see there): it can never run
// as the admin origin.

const (
	wbWizPageSlug      = "web-builder-import"
	wbWizURL           = "/wp-admin/" + wbWizPageSlug
	wbWizRoot          = mediaPrivateDir + "/web-builder-imports"
	wbWizImportsOption = "wb_site_imports"
	wbWizDesignRoute   = "/wb-design-preview/"
	wbWizMaxAge        = 24 * time.Hour
	wbWizMaxPageBytes  = 15 << 20
	wbWizNonceUpload   = "wb-site-import-upload"
)

var wbWizIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// wbWizAllowed: the screen and everything behind it need edit_theme_options and unfiltered_html with
// Web Builder on, like the MCP tool.
func (s *Server) wbWizAllowed(c *gin.Context) bool {
	u := currentUser(c)
	return s.wbOn() && u != nil && u.Can(webbuilder.TemplateCap, nil) && u.Can("unfiltered_html", nil)
}

// --- staging ---------------------------------------------------------------------------------

type wbWizPage struct {
	Path    string `json:"path"`
	PostID  int64  `json:"postId"`
	URL     string `json:"url,omitempty"`
	Title   string `json:"title,omitempty"`
	Created bool   `json:"created,omitempty"`
}

type wbWizTpl struct {
	Type  string `json:"type"`
	ID    int64  `json:"id"`
	Pages int    `json:"pages,omitempty"`
}

type wbWizResult struct {
	At        string      `json:"at"`
	Kit       string      `json:"kit"`
	Draft     bool        `json:"draft"`
	Split     bool        `json:"split"`
	Front     int64       `json:"front,omitempty"`
	Pages     []wbWizPage `json:"pages"`
	Templates []wbWizTpl  `json:"templates"`
	Warnings  []string    `json:"warnings,omitempty"`
	Missing   []string    `json:"missing,omitempty"`
	Unmapped  []string    `json:"unmapped,omitempty"`
	Failed    string      `json:"failed,omitempty"`
	Published bool        `json:"published,omitempty"`
	// Bindings: what was bound to content (the menu, single templates, loops) and what was not.
	Bindings []string `json:"bindings,omitempty"`
}

type wbWizMeta struct {
	ID      string       `json:"id"`
	Owner   int64        `json:"owner"`
	Slug    string       `json:"slug"`
	Created string       `json:"created"`
	Files   int          `json:"files"`
	Bytes   int64        `json:"bytes"`
	HTML    []string     `json:"html"`
	Result  *wbWizResult `json:"result,omitempty"`
}

// wbWizRecord is what one import created, kept for "Remove this import".
type wbWizRecord struct {
	ID         string  `json:"id"`
	Kit        string  `json:"kit"`
	KitCreated bool    `json:"kitCreated"`
	Pages      []int64 `json:"pages"`
	Templates  []int64 `json:"templates"`
	PrevShow   string  `json:"prevShow,omitempty"`
	PrevFront  int64   `json:"prevFront,omitempty"`
	FrontSet   bool    `json:"frontSet,omitempty"`
	// Menu: the navigation menu this import created (removed with it).
	Menu int64  `json:"menu,omitempty"`
	At   string `json:"at"`
	By   int64  `json:"by"`
}

func (s *Server) wbWizBase() string {
	return filepath.Join(filepath.Clean(s.cfg.UploadsDir), filepath.FromSlash(wbWizRoot))
}

// wbWizDir is a session's folder ("" for an id that is not one).
func (s *Server) wbWizDir(id string) string {
	if !wbWizIDRE.MatchString(id) {
		return ""
	}
	return filepath.Join(s.wbWizBase(), id)
}

// wbWizLoad reads a session; nil when there is none or it is older than wbWizMaxAge (its folder is
// removed then: the upload, its design preview and its compare step expire together).
func (s *Server) wbWizLoad(id string) *wbWizMeta {
	dir := s.wbWizDir(id)
	if dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil
	}
	var m wbWizMeta
	if json.Unmarshal(b, &m) != nil || m.ID != id {
		return nil
	}
	if at, err := time.Parse(time.RFC3339, m.Created); err != nil || time.Since(at) > wbWizMaxAge {
		_ = os.RemoveAll(dir)
		return nil
	}
	return &m
}

func (s *Server) wbWizSave(m *wbWizMeta) error {
	dir := s.wbWizDir(m.ID)
	if dir == "" {
		return fmt.Errorf("bad id")
	}
	b, _ := json.Marshal(m)
	tmp := filepath.Join(dir, "meta.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "meta.json"))
}

// wbWizSweep removes sessions older than wbWizMaxAge (from their upload time; a folder without a
// session, an upload that failed half way, from its own time). It runs with every visit of the
// screen and hourly (the wb_import_cleanup event).
func (s *Server) wbWizSweep() {
	ents, err := os.ReadDir(s.wbWizBase())
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || !wbWizIDRE.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.wbWizBase(), e.Name(), "meta.json")); err == nil {
			s.wbWizLoad(e.Name()) // removes it when expired
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > wbWizMaxAge {
			_ = os.RemoveAll(filepath.Join(s.wbWizBase(), e.Name()))
		}
	}
}

func wbWizNewID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// wbWizSlugOf is a kit slug from a file name: "Enerji OSGB site.zip" → "enerji-osgb-site".
func wbWizSlugOf(name string) string {
	name = strings.TrimSuffix(path.Base(strings.ReplaceAll(name, "\\", "/")), path.Ext(name))
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 63 {
		slug = strings.Trim(slug[:63], "-")
	}
	if slug == "" {
		return "site"
	}
	return slug
}

// --- records ---------------------------------------------------------------------------------

func (s *Server) wbWizRecords() map[string]*wbWizRecord {
	out := map[string]*wbWizRecord{}
	if raw := s.settings.Get(wbWizImportsOption); raw != nil {
		if b, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(b, &out)
		}
	}
	for id, r := range out {
		if r == nil || !wbWizIDRE.MatchString(id) {
			delete(out, id)
		}
	}
	return out
}

func (s *Server) wbWizSetRecords(c *gin.Context, recs map[string]*wbWizRecord) error {
	return s.settings.Set(c, map[string]any{wbWizImportsOption: recs})
}

// wbWizOwned lists the ids of the pages and templates carrying the kit's import mark, any status.
func (s *Server) wbWizOwned(c *gin.Context, kit string) (pages, templates map[int64]bool) {
	pages, templates = map[int64]bool{}, map[int64]bool{}
	rows, err := s.store.Pool.Query(c, `SELECT id, type FROM posts WHERE type IN ('page', 'post', $1) AND meta->>$2 = $3`,
		webbuilder.TemplatePostType, wbSiteMetaKit, kit)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var typ string
		if rows.Scan(&id, &typ) == nil {
			if typ == "page" || typ == "post" { // a post: a page a collection made a post
				pages[id] = true
			} else {
				templates[id] = true
			}
		}
	}
	return
}

func wbWizNew(after, before map[int64]bool) []int64 {
	var out []int64
	for id := range after {
		if !before[id] {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// --- the screen -------------------------------------------------------------------------------

type wbWizView struct {
	Step      string // start, analysis, result, compare, undo
	URL       string // the screen's own URL
	ID        string
	UploadURL string
	Limits    string
	Recs      []wbWizRecView

	Slug, Kit, Name string
	A               *wbWizAnalysis
	Split, Front    bool
	Draft           bool
	Replace         bool
	Menus           bool            // make a menu of the header's navigation
	OldLinks        bool            // links to the design's original site become this site's
	Collections     map[string]bool // folders ticked as collections
	Nonce           string          // the step's form nonce
	DiscardNonce    string

	R          *wbWizResult
	Compare    []wbWizCmpRow
	Cur        *wbWizCmpRow
	Undo       *wbWizUndoView
	DesignHint string
}

type wbWizRecView struct {
	ID, Kit, At string
	Pages       int
	UndoURL     string
}

type wbWizCmpRow struct {
	Path, Title, DesignURL, PursaeURL, EditURL, ViewURL, Select string
	Current                                                     bool
}

type wbWizUndoView struct {
	Kit       string
	KitGone   bool
	Pages     []string
	Templates []string
	Missing   int
	Nonce     string
	BackURL   string
	Front     bool
}

func (s *Server) wbWizDeny(c *gin.Context) { s.wbTBDeny(c) }

// wbWizScreen is GET and POST wp-admin/web-builder-import.
func (s *Server) wbWizScreen(c *gin.Context) {
	if !s.wbWizAllowed(c) {
		s.wbWizDeny(c)
		return
	}
	s.wbWizSweep()
	action := c.Query("action")
	if c.Request.Method == http.MethodPost {
		switch action {
		case "upload":
			s.wbWizUpload(c)
		case "import", "publish", "undo", "discard":
			s.wbWizPost(c, action)
		default:
			c.String(http.StatusBadRequest, "Unknown action.")
		}
		return
	}
	id := c.Query("import")
	if id == "" {
		s.wbWizStart(c)
		return
	}
	if action == "undo" { // an import's record outlives its upload
		s.wbWizUndoPage(c, id)
		return
	}
	m := s.wbWizLoad(id)
	if m == nil {
		s.wbWizStartWith(c, notice{Type: "error", Message: "That upload is gone (uploads are kept for a day). Upload the zip again.", Dismissible: true})
		return
	}
	switch action {
	case "result":
		s.wbWizResultPage(c, m)
	case "compare":
		s.wbWizComparePage(c, m)
	default:
		s.wbWizAnalysisPage(c, m, nil)
	}
}

func (s *Server) wbWizRender(c *gin.Context, v *wbWizView, notices ...notice) {
	v.URL = wbWizURL
	title := "Import HTML site"
	s.adminPage(c, wbWizPageSlug, title, "web-builder_page_"+wbWizPageSlug, "admin/web-builder-import", v, notices...)
}

func (s *Server) wbWizStart(c *gin.Context) { s.wbWizStartWith(c) }

func (s *Server) wbWizStartWith(c *gin.Context, notices ...notice) {
	v := &wbWizView{Step: "start", Limits: fmt.Sprintf("%d MB, %d files", wbKitMaxTotal>>20, wbKitMaxFiles)}
	v.UploadURL = wbWizURL + "?action=upload&_wpnonce=" + s.nonce(c, wbWizNonceUpload)
	recs := s.wbWizRecords()
	ids := make([]string, 0, len(recs))
	for id := range recs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return recs[ids[i]].At > recs[ids[j]].At })
	for _, id := range ids {
		r := recs[id]
		v.Recs = append(v.Recs, wbWizRecView{ID: id, Kit: r.Kit, At: r.At, Pages: len(r.Pages),
			UndoURL: wbWizURL + "?action=undo&import=" + id})
	}
	s.wbWizRender(c, v, notices...)
}

// --- upload ----------------------------------------------------------------------------------

func (s *Server) wbWizUpload(c *gin.Context) {
	u := currentUser(c)
	// The nonce is in the query string: reading it from the form would buffer the whole upload.
	if !s.checkNonceValue(c, c.Query("_wpnonce"), wbWizNonceUpload) {
		s.denyNonce(c)
		return
	}
	fail := func(msg string) {
		s.wbWizStartWith(c, notice{Type: "error", Message: template.HTML(msg), Dismissible: true})
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, wbKitMaxTotal+(8<<20))
	mr, err := c.Request.MultipartReader()
	if err != nil {
		fail("No file was received.")
		return
	}
	s.wbWizSweep()
	id := wbWizNewID()
	dir := s.wbWizDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail("Could not prepare the staging folder.")
		return
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	var slug, zipName string
	got := false
	for {
		part, err := mr.NextPart()
		if err != nil {
			if err != io.EOF {
				fail("The upload failed: " + err.Error())
				return
			}
			break
		}
		switch {
		case part.FormName() == "kit":
			b, _ := io.ReadAll(io.LimitReader(part, 200))
			slug = strings.TrimSpace(string(b))
		case part.FormName() == "archive" && part.FileName() != "":
			zipName = part.FileName()
			f, err := os.OpenFile(filepath.Join(dir, "upload.zip"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				fail("Could not prepare the staging folder.")
				return
			}
			_, err = io.Copy(f, part)
			f.Close()
			if err != nil {
				fail("The upload failed (is the zip larger than " + itoa(wbKitMaxTotal>>20) + " MB?).")
				return
			}
			got = true
		}
	}
	if !got {
		fail("Choose a .zip file first.")
		return
	}
	if !strings.EqualFold(path.Ext(zipName), ".zip") {
		fail("The file must be a .zip archive.")
		return
	}
	site := filepath.Join(dir, "site")
	if err := os.MkdirAll(site, 0o700); err != nil {
		fail("Could not prepare the staging folder.")
		return
	}
	ex, err := wbZipExtract(filepath.Join(dir, "upload.zip"), site, wbZipDefaultLimits)
	_ = os.Remove(filepath.Join(dir, "upload.zip"))
	if err != nil {
		if ze, isZip := err.(*wbZipError); isZip {
			fail(html.EscapeString(ze.msg))
		} else {
			fail("The zip could not be unpacked.")
		}
		return
	}
	if len(ex.HTML) == 0 {
		fail("The zip holds no HTML pages (.html or .htm files).")
		return
	}
	if !wbKitSlugRE.MatchString(slug) {
		slug = wbWizSlugOf(zipName)
	}
	m := &wbWizMeta{ID: id, Owner: u.ID, Slug: slug, Created: wbTime(time.Now()), Files: len(ex.Assets) + len(ex.HTML), Bytes: ex.Bytes}
	for p := range ex.HTML {
		m.HTML = append(m.HTML, p)
	}
	sort.Strings(m.HTML)
	if err := s.wbWizSave(m); err != nil {
		fail("Could not save the upload.")
		return
	}
	ok = true
	c.Redirect(http.StatusFound, wbWizURL+"?import="+id)
}

// --- analysis ---------------------------------------------------------------------------------

type wbWizPageRow struct {
	Path, URL, Title, Kind, Action string
	Header, Footer                 string
}

type wbWizCluster struct {
	Label    string
	Pages    int
	Examples string
	Active   int
	Parts    int
}

type wbWizAnalysis struct {
	Kit          string
	KitExists    bool
	KitFiles     int
	KitVersion   int
	Pages        []wbWizPageRow
	Placeholders []string
	Headers      []wbWizCluster
	Footers      []wbWizCluster
	Missing      []string
	Unmapped     []string
	Origins      []string
	Handlers     []string
	Warnings     []string
	Breakpoints  []webbuilder.Breakpoint
	HasScripts   bool
	HasFront     bool
	AssetFiles   int
	Creates      int
	Updates      int
	// Binding to content (webbuilder.BindSite): the header menu's items, the folders that can be
	// collections, and what was not bound.
	Menu      string
	Folders   []wbWizFolder
	BindNotes []string
	// Claims: pages at addresses that are archives here (author, category, date); the import keeps
	// them as the design's pages (webbuilder_siteclaims.go).
	Claims []string
	// The design's original site (webbuilder/oldsite.go): its domains, the URLs of it the pages
	// hold, the pages whose forms posted there, the script URLs pointed here; tracking it loads.
	OldHosts    []string
	OldURLs     []string
	OldURLCount int
	OldForms    []string
	OldScripts  int
	Trackers    []string
}

// wbWizFolder is a folder of pages the import can turn into content under single templates.
type wbWizFolder struct {
	Name      string
	Label     string // the folder ("hizmet/") or a set of pretty-permalink pages ("*/index.html (single-post)")
	Posts     bool   // its pages become posts
	Pages     int
	Templates int // single templates its pages would share
	Grouped   int // pages that become content
	Lists     []string
	Check     bool // offered ticked: its pages reproduce under the templates
}

func (s *Server) wbWizSiteDir(id string) string { return filepath.Join(s.wbWizDir(id), "site") }

// wbWizRead lists the staged site: the HTML pages' source and the assets' sizes.
func (s *Server) wbWizRead(id string) (html map[string]string, assets map[string]int64, err error) {
	root := s.wbWizSiteDir(id)
	html, assets = map[string]string{}, map[string]int64{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if webbuilder.IsSitePage(rel) {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			b, err := io.ReadAll(io.LimitReader(f, wbWizMaxPageBytes+1))
			if err != nil {
				return err
			}
			html[rel] = string(b)
			return nil
		}
		if info, err := d.Info(); err == nil {
			assets[rel] = info.Size()
		}
		return nil
	})
	return
}

var wbWizStyleRE = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)

// wbWizAnalyze is the dry run: nothing is written to the site.
func (s *Server) wbWizAnalyze(c *gin.Context, m *wbWizMeta, kit string, split bool) (*wbWizAnalysis, error) {
	pages, assets, err := s.wbWizRead(m.ID)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("the staged site holds no HTML pages")
	}
	if len(pages) > wbSiteMaxPages {
		return nil, fmt.Errorf("the site holds %d pages; at most %d can be imported at once", len(pages), wbSiteMaxPages)
	}
	a := &wbWizAnalysis{Kit: kit, AssetFiles: len(assets)}
	if k := s.wbKitList()[kit]; k != nil {
		a.KitExists, a.KitFiles, a.KitVersion = true, k.Files, k.Version
	}
	paths := make([]string, 0, len(pages))
	for p := range pages {
		paths = append(paths, p)
	}
	plan := webbuilder.PlanSite(append([]string(nil), paths...))
	urls := plan.URLs()
	opts := webbuilder.SiteImportOptions{KitURL: wbKitURLPrefix + kit + "/", KitFiles: map[string]bool{}, URLs: urls, NoSplit: !split,
		SiteURL: strings.TrimSuffix(s.links(c).HomeURL("/"), "/")}
	for p := range assets {
		opts.KitFiles[p] = true
	}
	var list []webbuilder.SitePage
	for _, p := range plan.Pages {
		list = append(list, webbuilder.SitePage{Path: p.Path, HTML: pages[p.Path]})
	}
	opts.OldHosts = webbuilder.DetectOldHosts(list)
	res, err := webbuilder.ConvertSite(list, opts)
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, p := range res.Pages {
		idx[p.Path] = i
	}
	label := func(n int) string { return "#" + itoa(n+1) }
	for _, p := range plan.Pages {
		row := wbWizPageRow{Path: p.Path, URL: urls[p.Path], Kind: "page", Action: "create", Title: wbWizTitle(pages[p.Path], p.Path)}
		switch {
		case p.Front:
			row.Kind = "front"
			a.HasFront = true
		case p.NotFound:
			row.Kind = "404"
			row.URL = ""
		}
		if !p.NotFound && s.wbSiteOwned(c, "page", kit, p.Path) > 0 {
			row.Action = "update"
		}
		if p.NotFound {
			row.Action = "template"
		}
		if i, ok := idx[p.Path]; ok {
			if h := res.Pages[i].Header; h >= 0 {
				row.Header = label(h)
			}
			if f := res.Pages[i].Footer; f >= 0 {
				row.Footer = label(f)
			}
		}
		switch row.Action {
		case "create":
			a.Creates++
		case "update":
			a.Updates++
		}
		a.Pages = append(a.Pages, row)
	}
	folderURL := map[string]string{}
	for _, f := range plan.Folders {
		parent := "/"
		if f.Parent != "" {
			parent = folderURL[f.Parent]
		}
		folderURL[f.Key] = parent + f.Slug + "/"
		if f.Page == "" {
			a.Placeholders = append(a.Placeholders, folderURL[f.Key])
		}
	}
	cluster := func(kind string, list []webbuilder.SiteTemplate) []wbWizCluster {
		var out []wbWizCluster
		for i, t := range list {
			cl := wbWizCluster{Label: kind + " " + label(i), Pages: len(t.Pages), Parts: len(t.Sections)}
			var ex []string
			for _, pi := range t.Pages {
				if len(ex) < 3 {
					ex = append(ex, res.Pages[pi].Path)
				}
			}
			cl.Examples = strings.Join(ex, ", ")
			if t.Rule != nil {
				cl.Active = len(t.Rule.Items)
			}
			out = append(out, cl)
		}
		return out
	}
	a.Headers, a.Footers = cluster("Header", res.Headers), cluster("Footer", res.Footers)
	// Binding to content: what the import would bind (BindSite is pure; the folder ids are stand-ins).
	if split {
		fp := map[string]string{}
		for f := range webbuilder.SiteFolders(res) {
			fp[f] = "1"
		}
		b := webbuilder.BindSite(res, webbuilder.SiteBindOptions{Menus: true, Groups: map[string]bool{"*": true}, FolderParents: fp, PostSets: s.wbSitePostSets(res)})
		if b.Menu != nil {
			var labels []string
			for _, it := range b.Menu.Items {
				labels = append(labels, it.Label)
			}
			a.Menu = strings.Join(labels, " · ")
		}
		folders := webbuilder.SiteFolders(res)
		for k, v := range webbuilder.SiteLeafSets(res) {
			folders[k] = v
		}
		for _, f := range b.Candidates {
			row := wbWizFolder{Name: f, Label: f + "/", Pages: len(folders[f])}
			if parent, ok := webbuilder.SiteLeafParent(f); ok {
				_, kind, _ := strings.Cut(f, ":")
				row.Label = strings.TrimPrefix(parent+"/", "/") + "*/index.html"
				if kind != "" {
					row.Label += " (" + kind + ")"
				}
			}
			for _, g := range b.Groups {
				if g.Folder == f && g.AsPosts {
					row.Posts = true
				}
			}
			for _, g := range b.Groups {
				if g.Folder == f {
					row.Templates++
					row.Grouped += len(g.Posts)
				}
			}
			for _, l := range b.Listings {
				if l.Folder == f {
					row.Lists = append(row.Lists, res.Pages[l.Page].Path)
				}
			}
			row.Check = row.Templates > 0
			a.Folders = append(a.Folders, row)
		}
		a.BindNotes = b.Notes
	}
	a.Missing, a.Unmapped = wbFirst(res.Missing, 60), wbFirst(res.Unmapped, 60)
	if len(res.OldURLs) > 0 || len(res.OldForms) > 0 {
		a.OldHosts, a.OldURLs, a.OldURLCount, a.OldForms, a.OldScripts = opts.OldHosts, wbFirst(res.OldURLs, 40), len(res.OldURLs), res.OldForms, res.OldInScripts
	}
	a.Trackers = res.Trackers
	for _, w := range res.Warnings {
		if strings.Contains(w, "inline event handler") {
			a.Handlers = append(a.Handlers, w)
		} else {
			a.Warnings = append(a.Warnings, w)
		}
	}
	// External origins: the kit files' (scripts, stylesheets) and the pages' own.
	origins := map[string]bool{}
	for _, o := range wbKitOrigins(s.wbWizSiteDir(m.ID), assets) {
		origins[o] = true
	}
	var css []string
	for rel, n := range assets {
		if strings.EqualFold(path.Ext(rel), ".css") && n <= 4<<20 {
			if b, err := os.ReadFile(filepath.Join(s.wbWizSiteDir(m.ID), filepath.FromSlash(rel))); err == nil {
				css = append(css, string(b))
			}
		}
	}
	for _, src := range pages {
		if containsFold(src, "<script") {
			a.HasScripts = true
		}
		for _, re := range []*regexp.Regexp{wbCodeScriptRE, wbCodeLinkRE, wbCodeFrameRE} {
			for _, mm := range re.FindAllStringSubmatch(src, -1) {
				if o := wbHTTPSOrigin(html.UnescapeString(mm[1] + mm[2] + mm[3])); o != "" {
					origins[o] = true
				}
			}
		}
		for _, mm := range wbCodeCSSURLRE.FindAllStringSubmatch(src, -1) {
			if o := wbHTTPSOrigin(mm[1]); o != "" {
				origins[o] = true
			}
		}
		for _, mm := range wbWizStyleRE.FindAllStringSubmatch(src, -1) {
			css = append(css, mm[1])
		}
	}
	for o := range origins {
		a.Origins = append(a.Origins, o)
	}
	sort.Strings(a.Origins)
	a.Origins = wbFirst(a.Origins, 40)
	a.Breakpoints = webbuilder.SuggestBreakpoints(css, 12)
	if a.HasScripts {
		if w := s.wbCodeScriptsWarning(&webbuilder.CodeImport{Head: "<script>"}); w != "" {
			a.Warnings = append(a.Warnings, w)
		}
	}
	if s.activeTheme().Slug != wbHelloSlug {
		a.Warnings = append(a.Warnings, "the header, footer and 404 templates show only with the Hello Pursae theme (activate Pursae Web Builder's theme)")
	}
	for _, p := range plan.Pages {
		if p.NotFound || p.Front {
			continue
		}
		if why := s.wbRouteShadowed(c, urls[p.Path]); why != "" {
			a.Claims = append(a.Claims, urls[p.Path]+" ("+why+")")
		}
	}
	if s.settings.String("permalink_structure") == "" {
		a.Warnings = append(a.Warnings, "the site has plain permalinks (?page_id=…): links between pages work, but the menu cannot mark the current page; choose a pretty permalink structure (Settings › Permalinks) first")
	}
	return a, nil
}

func wbWizTitle(src, p string) string {
	if m := wbSiteTitleRE.FindStringSubmatch(src); m != nil {
		if t := strings.TrimSpace(html.UnescapeString(m[1])); t != "" {
			return truncRunes(t, 200)
		}
	}
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}

func (s *Server) wbWizAnalysisPage(c *gin.Context, m *wbWizMeta, extra []notice) {
	kit := m.Slug
	if q := strings.TrimSpace(c.Query("kit")); wbKitSlugRE.MatchString(q) {
		kit = q
	}
	v := &wbWizView{Step: "analysis", ID: m.ID, Slug: kit, Kit: kit, Name: kit, Split: true, Front: true, Menus: true, OldLinks: true, Collections: map[string]bool{}}
	if k := s.wbKitList()[kit]; k != nil && k.Name != "" {
		v.Name = k.Name
	}
	posted := c.Request.Method == http.MethodPost
	if posted {
		v.Name = strings.TrimSpace(c.PostForm("name"))
		v.Split, v.Front, v.Draft, v.Replace = c.PostForm("templates") == "1", c.PostForm("front") == "1", c.PostForm("status") == "draft", c.PostForm("replace") == "1"
		v.Menus = c.PostForm("menus") == "1"
		v.OldLinks = c.PostForm("old_links") == "rewrite"
		for _, f := range c.PostFormArray("collection") {
			v.Collections[f] = true
		}
	}
	a, err := s.wbWizAnalyze(c, m, kit, true)
	if err != nil {
		s.wbWizStartWith(c, notice{Type: "error", Message: template.HTML("The site could not be read: " + html.EscapeString(err.Error())), Dismissible: true})
		return
	}
	if !posted {
		for _, f := range a.Folders {
			if f.Check {
				v.Collections[f.Name] = true
			}
		}
	}
	if !a.HasFront {
		v.Front = false
	}
	v.A = a
	v.Nonce = s.nonce(c, "wb-site-import-import-"+m.ID)
	v.DiscardNonce = s.nonce(c, "wb-site-import-discard-"+m.ID)
	s.wbWizRender(c, v, extra...)
}

// --- import ----------------------------------------------------------------------------------

func (s *Server) wbWizPost(c *gin.Context, action string) {
	id := c.Query("import")
	if !wbWizIDRE.MatchString(id) {
		c.String(http.StatusBadRequest, "Unknown import.")
		return
	}
	if !s.checkNonce(c, "wb-site-import-"+action+"-"+id) {
		s.denyNonce(c)
		return
	}
	switch action {
	case "undo":
		s.wbWizUndoRun(c, id)
		return
	case "discard":
		if dir := s.wbWizDir(id); dir != "" {
			_ = os.RemoveAll(dir)
		}
		c.Redirect(http.StatusFound, wbWizURL)
		return
	}
	m := s.wbWizLoad(id)
	if m == nil {
		s.wbWizStartWith(c, notice{Type: "error", Message: "That upload is gone. Upload the zip again.", Dismissible: true})
		return
	}
	if action == "publish" {
		s.wbWizPublish(c, m)
		return
	}
	s.wbWizImport(c, m)
}

func (s *Server) wbWizImport(c *gin.Context, m *wbWizMeta) {
	kit := strings.TrimSpace(c.PostForm("kit"))
	again := func(msg string) {
		c.Request.Method = http.MethodPost
		s.wbWizAnalysisPage(c, m, []notice{{Type: "error", Message: template.HTML(html.EscapeString(msg)), Dismissible: true}})
	}
	if !wbKitSlugRE.MatchString(kit) {
		again("The kit slug may hold lowercase letters, digits and dashes only.")
		return
	}
	q := c.Request.URL.Query()
	q.Set("kit", kit)
	c.Request.URL.RawQuery = q.Encode()
	name := truncRunes(strings.TrimSpace(c.PostForm("name")), 120)
	if name == "" {
		name = kit
	}
	split, wantFront, draft := c.PostForm("templates") == "1", c.PostForm("front") == "1", c.PostForm("status") == "draft"
	existing := s.wbKitList()[kit] != nil
	if existing && c.PostForm("replace") != "1" {
		again("A design kit named " + kit + " exists: tick the box to replace its files, or choose another slug.")
		return
	}
	if err := s.mcpNeedBuilder(); err != nil {
		again(err.Error())
		return
	}
	pages, assets, err := s.wbWizRead(m.ID)
	if err != nil {
		again("The staged site could not be read: " + err.Error())
		return
	}
	// 1. The design kit: the staged assets, committed like the kit REST routes do.
	if err := s.wbWizInstallKit(c, m.ID, kit, name, assets); err != nil {
		again("The design kit could not be written: " + err.Error())
		return
	}
	pagesBefore, tplBefore := s.wbWizOwned(c, kit)
	menuBefore := s.wbWizMenuID(c, kit)
	prevShow, prevFront := s.settings.String("show_on_front"), int64(s.settings.Int("page_on_front"))
	// 2. The import: the code of the MCP tool web_builder_import_site.
	paths := make([]string, 0, len(pages))
	for p := range pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	items := make([]any, 0, len(paths))
	for _, p := range paths {
		items = append(items, map[string]any{"path": p, "html": pages[p]})
	}
	status := "publish"
	if draft {
		status = "unpublished"
	}
	var collections []any
	for _, f := range c.PostFormArray("collection") {
		collections = append(collections, f)
	}
	args := map[string]any{"kit": kit, "pages": items, "status": status, "site_name": name, "templates": split, "front_page": wantFront && !draft,
		"menus": c.PostForm("menus") == "1", "collections": collections}
	if c.PostForm("old_links") != "rewrite" {
		args["old_links"] = "keep"
	}
	s.wbWizRESTSession(c)
	out, importErr := mcpWBImportSite(s, c, args)
	// 3. What it made, recorded even when it stopped half way.
	pagesAfter, tplAfter := s.wbWizOwned(c, kit)
	rec := &wbWizRecord{ID: m.ID, Kit: kit, KitCreated: !existing, Pages: wbWizNew(pagesAfter, pagesBefore), Templates: wbWizNew(tplAfter, tplBefore),
		At: wbTime(time.Now()), By: currentUser(c).ID}
	if menuBefore == 0 {
		rec.Menu = s.wbWizMenuID(c, kit)
	}
	res := &wbWizResult{At: rec.At, Kit: kit, Draft: draft, Split: split}
	if importErr != nil {
		res.Failed = importErr.Error()
	}
	if om, ok := out.(map[string]any); ok {
		wbWizFillResult(res, om)
	}
	if fp, _ := out.(map[string]any); fp != nil {
		if id := argInt(fp, "front_page"); id > 0 {
			res.Front = id
			rec.FrontSet, rec.PrevShow, rec.PrevFront = true, prevShow, prevFront
		}
	}
	if len(rec.Pages)+len(rec.Templates) > 0 || rec.KitCreated || rec.Menu > 0 {
		recs := s.wbWizRecords()
		recs[m.ID] = rec
		_ = s.wbWizSetRecords(c, recs)
	}
	m.Slug = kit
	m.Result = res
	_ = s.wbWizSave(m)
	c.Redirect(http.StatusSeeOther, wbWizURL+"?action=result&import="+m.ID)
}

// wbWizRESTSession lets the MCP tool code run for the admin's own session: the tools see the user as
// they do for an agent, and their in-process REST calls authenticate with the session cookie and the
// REST nonce (mcpDispatch forwards both).
func (s *Server) wbWizRESTSession(c *gin.Context) {
	c.Set(restUserKey, currentUser(c))
	c.Request.Header.Set("X-WP-Nonce", s.nonce(c, "wp_rest"))
}

// wbWizInstallKit stages the site's assets as the kit's next version and commits them. The files are
// hard-linked when the filesystem allows (kit files are replaced, never written in place).
func (s *Server) wbWizInstallKit(c *gin.Context, id, slug, name string, assets map[string]int64) error {
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	live := filepath.Join(s.wbKitRoot(), slug)
	stage := filepath.Join(live, wbKitStageDir)
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	src := s.wbWizSiteDir(id)
	for rel := range assets {
		if _, ok := wbKitRel(rel); !ok {
			continue
		}
		from, to := filepath.Join(src, filepath.FromSlash(rel)), filepath.Join(stage, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Link(from, to); err == nil {
			continue
		}
		b, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.WriteFile(to, b, 0o644); err != nil {
			return err
		}
	}
	if k, _, _, msg := s.wbKitCommitStage(c, slug, name, nil); k == nil {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// wbWizFillResult reads the report of mcpWBImportSite.
func wbWizFillResult(res *wbWizResult, out map[string]any) {
	b, _ := json.Marshal(out)
	var r struct {
		Pages []struct {
			Path   string `json:"path"`
			PostID int64  `json:"post_id"`
			URL    string `json:"url"`
		} `json:"pages"`
		Created   []string `json:"pages_created"`
		Templates []struct {
			Type  string `json:"type"`
			ID    int64  `json:"id"`
			Pages int    `json:"pages"`
		} `json:"templates"`
		Warnings []string `json:"warnings"`
		Missing  []string `json:"missing_files"`
		Unmapped []string `json:"unmapped_links"`
		Bindings struct {
			Menu *struct {
				Name     string `json:"name"`
				Location string `json:"location"`
				Items    []any  `json:"items"`
			} `json:"menu"`
			Groups []struct {
				Folder   string   `json:"folder"`
				Template int64    `json:"template"`
				Pages    []string `json:"pages"`
			} `json:"groups"`
			Listings []struct {
				Page   string `json:"page"`
				Folder string `json:"folder"`
				Items  int    `json:"items"`
			} `json:"listings"`
			Notes []string `json:"notes"`
		} `json:"bindings"`
	}
	if json.Unmarshal(b, &r) != nil {
		return
	}
	if m := r.Bindings.Menu; m != nil {
		res.Bindings = append(res.Bindings, fmt.Sprintf("Menu “%s” (%s location, %d items) fills the header's navigation; edit it in Appearance › Menus.", m.Name, m.Location, len(m.Items)))
	}
	for _, g := range r.Bindings.Groups {
		res.Bindings = append(res.Bindings, fmt.Sprintf("%s/: %d pages are content under single template #%d.", g.Folder, len(g.Pages), g.Template))
	}
	for _, l := range r.Bindings.Listings {
		res.Bindings = append(res.Bindings, fmt.Sprintf("%s: the list of %s/ pages is a loop (%d items).", l.Page, l.Folder, l.Items))
	}
	res.Bindings = append(res.Bindings, r.Bindings.Notes...)
	created := map[string]bool{}
	for _, p := range r.Created {
		created[p] = true
	}
	for _, p := range r.Pages {
		res.Pages = append(res.Pages, wbWizPage{Path: p.Path, PostID: p.PostID, URL: p.URL, Created: created[p.Path]})
	}
	for _, t := range r.Templates {
		res.Templates = append(res.Templates, wbWizTpl{Type: t.Type, ID: t.ID, Pages: t.Pages})
	}
	res.Warnings, res.Missing, res.Unmapped = r.Warnings, r.Missing, r.Unmapped
}

// wbWizPublish publishes the pages of a draft import and sets its front page.
func (s *Server) wbWizPublish(c *gin.Context, m *wbWizMeta) {
	if m.Result == nil || len(m.Result.Pages) == 0 {
		s.wbWizAnalysisPage(c, m, nil)
		return
	}
	s.wbWizRESTSession(c)
	var failed []string
	for i, p := range m.Result.Pages {
		out, err := mcpWBPublish(s, c, map[string]any{"post_id": p.PostID})
		if err != nil {
			failed = append(failed, p.Path+": "+err.Error())
			continue
		}
		if om, ok := out.(map[string]any); ok {
			if u, _ := om["url"].(string); u != "" {
				m.Result.Pages[i].URL = u
			}
		}
	}
	notices := []notice{}
	if len(failed) > 0 {
		notices = append(notices, notice{Type: "error", Message: template.HTML(html.EscapeString(strings.Join(wbFirst(failed, 5), "; "))), Dismissible: true})
	} else {
		m.Result.Published, m.Result.Draft = true, false
		wbKitRootLiveReset() // the kit's files answer at the root now (wbKitRootAsset)
		if front := wbWizFrontID(m.Result); front > 0 && c.PostForm("front") == "1" {
			recs := s.wbWizRecords()
			rec := recs[m.ID]
			prevShow, prevFront := s.settings.String("show_on_front"), int64(s.settings.Int("page_on_front"))
			if err := s.mcpREST(c, http.MethodPost, "/wp/v2/settings", map[string]any{"show_on_front": "page", "page_on_front": front}, nil); err == nil {
				m.Result.Front = front
				if rec != nil {
					rec.FrontSet, rec.PrevShow, rec.PrevFront = true, prevShow, prevFront
					_ = s.wbWizSetRecords(c, recs)
				}
			}
		}
		_ = s.wbWizSave(m)
	}
	s.wbWizResultPage(c, m, notices...)
}

// wbWizFrontID is the post of index.html in a result (0: none).
func wbWizFrontID(r *wbWizResult) int64 {
	for _, p := range r.Pages {
		if strings.EqualFold(p.Path, "index.html") || strings.EqualFold(p.Path, "index.htm") {
			return p.PostID
		}
	}
	return 0
}

// --- result and compare -----------------------------------------------------------------------

func (s *Server) wbWizResultPage(c *gin.Context, m *wbWizMeta, extra ...notice) {
	if m.Result == nil {
		s.wbWizAnalysisPage(c, m, nil)
		return
	}
	v := &wbWizView{Step: "result", ID: m.ID, Kit: m.Result.Kit, R: m.Result, Nonce: s.nonce(c, "wb-site-import-publish-"+m.ID)}
	v.Front = wbWizFrontID(m.Result) > 0 && m.Result.Front == 0
	if m.Result.Failed != "" {
		extra = append(extra, notice{Type: "error", Message: template.HTML("The import stopped: " + html.EscapeString(m.Result.Failed)), Dismissible: false})
	}
	s.wbWizRender(c, v, extra...)
}

func (s *Server) wbWizComparePage(c *gin.Context, m *wbWizMeta) {
	if m.Result == nil || len(m.Result.Pages) == 0 {
		s.wbWizAnalysisPage(c, m, nil)
		return
	}
	v := &wbWizView{Step: "compare", ID: m.ID, Kit: m.Result.Kit, R: m.Result}
	cur := c.Query("file")
	for i, p := range m.Result.Pages {
		row := wbWizCmpRow{Path: p.Path, Title: wbWizTitle("", p.Path), EditURL: wbEditURL(p.PostID),
			DesignURL: wbWizDesignRoute + m.ID + "/" + wbWizEscPath(p.Path), Select: wbWizURL + "?action=compare&import=" + m.ID + "&file=" + url.QueryEscape(p.Path)}
		if !m.Result.Draft && p.URL != "" {
			if u, err := url.Parse(p.URL); err == nil {
				row.PursaeURL = u.RequestURI()
				row.ViewURL = p.URL
			}
		}
		row.Current = p.Path == cur || (cur == "" && i == 0)
		v.Compare = append(v.Compare, row)
		if row.Current {
			r := row
			v.Cur = &r
		}
	}
	if v.Cur == nil {
		v.Cur = &v.Compare[0]
		v.Compare[0].Current = true
	}
	s.wbWizRender(c, v)
}

func wbWizEscPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// --- undo -------------------------------------------------------------------------------------

func (s *Server) wbWizUndoPage(c *gin.Context, id string) {
	rec := s.wbWizRecords()[id]
	if rec == nil {
		s.wbWizStartWith(c, notice{Type: "error", Message: "That import has nothing left to remove (it was removed, or it created nothing).", Dismissible: true})
		return
	}
	v := &wbWizView{Step: "undo", ID: id, Kit: rec.Kit}
	u := &wbWizUndoView{Kit: rec.Kit, Nonce: s.nonce(c, "wb-site-import-undo-"+id), BackURL: wbWizURL}
	for _, pid := range rec.Pages {
		if t := s.wbWizOwnedTitle(c, pid, "page", rec.Kit); t != "" {
			u.Pages = append(u.Pages, t)
		} else {
			u.Missing++
		}
	}
	for _, tid := range rec.Templates {
		if t := s.wbWizOwnedTitle(c, tid, webbuilder.TemplatePostType, rec.Kit); t != "" {
			u.Templates = append(u.Templates, t)
		} else {
			u.Missing++
		}
	}
	u.KitGone = rec.KitCreated && s.wbKitList()[rec.Kit] != nil
	u.Front = rec.FrontSet
	v.Undo = u
	s.wbWizRender(c, v)
}

// wbWizOwnedTitle is the title of a post if it still exists, has the type and still carries the
// import's mark of the kit ("" otherwise: the post is not the import's to delete).
func (s *Server) wbWizOwnedTitle(c *gin.Context, id int64, typ, kit string) string {
	var title string
	types := []string{typ}
	if typ == "page" {
		types = append(types, "post") // a page a collection made a post
	}
	err := s.store.Pool.QueryRow(c, `SELECT title FROM posts WHERE id = $1 AND type = ANY($2) AND meta->>$3 = $4`, id, types, wbSiteMetaKit, kit).Scan(&title)
	if err != nil {
		return ""
	}
	if strings.TrimSpace(title) == "" {
		title = "#" + itoa64(id)
	}
	return title
}

func (s *Server) wbWizUndoRun(c *gin.Context, id string) {
	recs := s.wbWizRecords()
	rec := recs[id]
	if rec == nil {
		s.wbWizStartWith(c, notice{Type: "error", Message: "That import has nothing left to remove.", Dismissible: true})
		return
	}
	if c.PostForm("confirm") != "1" {
		s.wbWizUndoPage(c, id)
		return
	}
	deleted, kept := 0, 0
	del := func(ids []int64, typ string) {
		for _, pid := range ids {
			if s.wbWizOwnedTitle(c, pid, typ, rec.Kit) == "" {
				kept++
				continue
			}
			if err := s.store.DeletePost(c, pid); err == nil {
				deleted++
			}
		}
	}
	// A placeholder parent holds the pages below it: children first is not needed, DeletePost moves
	// the survivors up, and every page the import made goes anyway.
	del(rec.Pages, "page")
	del(rec.Templates, webbuilder.TemplatePostType)
	s.wbSiteDropClaims(c, rec.Pages) // their addresses go back to the archives
	// The navigation menu it made, when it is still the import's (same id, same name).
	if rec.Menu > 0 && s.wbWizMenuID(c, rec.Kit) == rec.Menu {
		if err := s.store.DeleteMenu(c, rec.Menu); err == nil {
			deleted++
		}
	}
	if rec.FrontSet && s.settings.String("show_on_front") == "page" && wbWizHas(rec.Pages, int64(s.settings.Int("page_on_front"))) {
		prev := rec.PrevShow
		if prev == "" {
			prev = "posts"
		}
		_ = s.settings.Set(c, map[string]any{"show_on_front": prev, "page_on_front": rec.PrevFront})
	}
	kitMsg := ""
	_ = s.wbKitSetRoot(c, rec.Kit, false) // the kit's files leave the site's root with its pages
	if rec.KitCreated {
		wbKitsMu.Lock()
		if status, code, _ := s.wbKitRemove(c, rec.Kit); code == "" || status == http.StatusNotFound {
			kitMsg = " and its design kit"
		}
		wbKitsMu.Unlock()
	}
	delete(recs, id)
	_ = s.wbWizSetRecords(c, recs)
	s.wbTemplatesChanged(c)
	s.wbPurgePages()
	if dir := s.wbWizDir(id); dir != "" { // the upload goes with what it made
		_ = os.RemoveAll(dir)
	}
	msg := fmt.Sprintf("Removed %d pages and templates%s.", deleted, kitMsg)
	if kept > 0 {
		msg += fmt.Sprintf(" %d were already gone or no longer carry the import's mark and were left alone.", kept)
	}
	s.wbWizStartWith(c, notice{Type: "success", Message: template.HTML(html.EscapeString(msg)), Dismissible: true})
}

// wbWizMenuID is the id of the navigation menu an import of kit makes (0: none).
func (s *Server) wbWizMenuID(c *gin.Context, kit string) int64 {
	menus, err := s.store.Menus(c)
	if err != nil {
		return 0
	}
	for _, m := range menus {
		if m.Name == wbSiteMenuName(kit) {
			return m.ID
		}
	}
	return 0
}

func wbWizHas(list []int64, id int64) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

// --- serving the design ------------------------------------------------------------------------

// wbWizDesign serves the uploaded design from its staging copy, for the compare step:
// /wb-design-preview/<id>/<path>. It is uploaded HTML, JavaScript included, on the admin's own origin,
// so it is served as untrusted content:
//
//   - Content-Security-Policy "sandbox allow-scripts allow-forms allow-popups" without
//     allow-same-origin: the document gets an opaque origin, so its scripts cannot read the
//     site's cookies, call the REST API or the admin as the logged-in user, nor touch the admin
//     page that frames it, and cannot navigate the top window (no allow-top-navigation). Popups
//     keep the sandbox (no allow-popups-to-escape-sandbox). The policy comes with the response, so
//     opening the file in its own tab is sandboxed too;
//   - the rest of the policy lets the design load its own files and https resources (fonts, CDN
//     scripts, images, maps), and forbids base-uri changes and form posts anywhere;
//   - the design's files have no cookie and no session: the staging id (128 random bits) is the
//     credential, like the exact-import page of webbuilder_import.go. A sandboxed frame without
//     allow-same-origin never sends the admin's SameSite=Lax cookie for its own links and fonts, so
//     a login check could not work; the id is only ever shown to an administrator on the screen
//     and the staging folder (with the preview) is removed a day after the upload, with "Discard"
//     or with "Remove this import" (wbWizLoad, wbWizSweep);
//   - files are read only from the session's site folder (cleaned path, no symlinks are ever
//     created there), served with the type of their extension plus nosniff and
//     Access-Control-Allow-Origin: * (fonts requested from an opaque origin need CORS);
//   - noindex, no-referrer, no-store.
func (s *Server) wbWizDesign(c *gin.Context) {
	id := c.Param("id")
	h := c.Writer.Header()
	h.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups; default-src 'none'; "+
		"script-src 'unsafe-inline' 'unsafe-eval' 'self' https:; style-src 'unsafe-inline' 'self' https:; img-src 'self' data: blob: https:; "+
		"font-src 'self' data: https:; media-src 'self' data: blob: https:; connect-src 'self' https:; frame-src https:; "+
		"object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	h.Del("Content-Security-Policy-Report-Only")
	// X-Frame-Options: SAMEORIGIN would block the sandboxed frame (its origin is opaque, so no
	// ancestor "matches"); frame-ancestors 'self' above does the same job for the admin page only.
	h.Del("X-Frame-Options")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Access-Control-Allow-Origin", "*")
	if !wbWizIDRE.MatchString(id) || s.wbWizLoad(id) == nil {
		c.String(http.StatusNotFound, "This design preview is unknown or has expired.")
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(c.Param("path"), "\\", "/")), "/")
	root := s.wbWizSiteDir(id)
	file := filepath.Join(root, filepath.FromSlash(rel))
	if r, err := filepath.Rel(root, file); err != nil || strings.HasPrefix(r, "..") {
		c.String(http.StatusNotFound, "Not found.")
		return
	}
	info, err := os.Lstat(file)
	if err == nil && info.IsDir() {
		file = filepath.Join(file, "index.html")
		info, err = os.Lstat(file)
	}
	if err != nil || !info.Mode().IsRegular() {
		c.String(http.StatusNotFound, "Not found.")
		return
	}
	f, err := os.Open(file)
	if err != nil {
		c.String(http.StatusNotFound, "Not found.")
		return
	}
	defer f.Close()
	ct := mime.TypeByExtension(strings.ToLower(path.Ext(file)))
	if webbuilder.IsSitePage(file) {
		ct = "text/html; charset=utf-8"
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	h.Set("Content-Type", ct)
	http.ServeContent(c.Writer, c.Request, "", info.ModTime(), f)
}

func (s *Server) wbWizRegister(r *gin.Engine) {
	r.GET(wbWizDesignRoute+":id/*path", s.wbOnly(), s.wbWizDesign)
	r.HEAD(wbWizDesignRoute+":id/*path", s.wbOnly(), s.wbWizDesign)
}

// wbWizMenuAllowed: the menu entry is for who may use the screen.
func wbWizMenuAllowed(u *store.User) bool {
	return u != nil && u.Can(webbuilder.TemplateCap, nil) && u.Can("unfiltered_html", nil)
}
