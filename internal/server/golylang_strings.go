package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Languages > Translations: the strings translations table, with the Pro import/export of PO files.

type pllStringRow struct {
	Index        int
	Key          string // the pll_strings.original it is translated under (pllRegistered.Key)
	Name, String string
	Group        string
	Multiline    bool
	Deletable    bool
	Translations []pllStringTr
}

type pllStringTr struct {
	Lang  *pllLanguage
	Value string
}

type pllStringsData struct {
	Tabs       []pllTab
	Rows       []pllStringRow
	Groups     []string
	Group      string
	Search     string
	Langs      []*pllLanguage
	Nonce      string
	IENonce    string
	Pagination pagination
	MT         pllMTView // Machine translation (golylang_mt.go)
	Visual     bool      // "Translate visually" link (golylang_visual.go)
}

const pllStringsPerPage = 20

func (s *Server) pllStringsScreen(c *gin.Context) {
	ctx := c.Request.Context()
	if c.Request.Method == http.MethodPost {
		if a := c.PostForm("pll_action"); (a == "export" || a == "import") && !currentUser(c).Can("manage_options", nil) {
			c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
			return
		}
		switch c.PostForm("pll_action") {
		case "export":
			s.pllExportPO(c)
		case "import":
			s.pllImportPO(c)
		case "mt_fill":
			s.pllMTStringsFill(c)
		default:
			s.pllStringsSave(c)
		}
		return
	}
	d := pllStringsData{Visual: s.pllVisualSupported(), Tabs: pllTabs("mlang_strings"), Group: c.Query("group"), Search: strings.TrimSpace(c.Query("s")),
		Nonce: s.nonce(c, "string-translation"), IENonce: s.nonce(c, "pll-import-export"), MT: s.pllMTViewFor(c)}
	langs := pllTranslatableLangs(currentUser(c), s.pllLanguages(ctx))
	d.Langs = langs
	if cur := s.pllCurrent(c); cur != nil { // the admin bar filter shows one language
		d.Langs = pllTranslatableLangs(currentUser(c), []*pllLanguage{cur})
	}
	all := s.pllStrings(ctx)
	groups := map[string]bool{}
	var list []pllRegistered
	q := strings.ToLower(d.Search)
	for _, r := range all {
		groups[r.Group] = true
		if d.Group != "" && r.Group != d.Group {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.String), q) && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		list = append(list, r)
	}
	for g := range groups {
		d.Groups = append(d.Groups, g)
	}
	sort.Strings(d.Groups)
	page, _ := strconv.Atoi(c.Query("paged"))
	if page < 1 {
		page = 1
	}
	d.Pagination = newPagination(len(list), pllStringsPerPage, page, *c.Request.URL)
	start, end := (page-1)*pllStringsPerPage, page*pllStringsPerPage
	if start > len(list) {
		start = len(list)
	}
	if end > len(list) {
		end = len(list)
	}
	trs := make([]map[string]string, len(d.Langs))
	for j, l := range d.Langs {
		trs[j] = s.pllStringMap(ctx, l.Slug)
	}
	for i, r := range list[start:end] {
		row := pllStringRow{Index: i, Key: r.Key(), Name: r.Name, String: r.String, Group: r.Group, Multiline: r.Multiline || strings.Contains(r.String, "\n"),
			Deletable: r.Source != ""}
		for j, l := range d.Langs {
			row.Translations = append(row.Translations, pllStringTr{l, trs[j][row.Key]})
		}
		d.Rows = append(d.Rows, row)
	}
	var notices []notice
	switch c.Query("pll_msg") {
	case "updated":
		notices = append(notices, notice{Type: "success", Message: "Translations updated.", Dismissible: true})
	case "imported":
		notices = append(notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(c.Query("n")) + " translations imported."), Dismissible: true})
	case "mt_filled":
		notices = append(notices, notice{Type: "success", Message: template.HTML(template.HTMLEscapeString(c.Query("n")) + " strings translated by machine translation."), Dismissible: true})
	case "save_error":
		notices = append(notices, notice{Type: "error", Message: "Some translations could not be saved. Please try again.", Dismissible: true})
	case "import_error":
		notices = append(notices, notice{Type: "error", Message: template.HTML(template.HTMLEscapeString(c.Query("err"))), Dismissible: true})
	}
	s.adminPage(c, "admin?page=mlang_strings", "Strings translations", "languages_page_mlang_strings", "admin/golylang-strings", d, notices...)
}

// pllStringsSave stores the posted translations (empty ones are removed) and, when asked, cleans the
// translations of strings that are no longer registered.
func (s *Server) pllStringsSave(c *gin.Context) {
	ctx := c.Request.Context()
	if !s.checkNonce(c, "string-translation") {
		s.denyNonce(c)
		return
	}
	var saveErr error
	if c.PostForm("action") == "delete" || c.PostForm("action2") == "delete" {
		saveErr = pllLog("deleting strings", s.pllDeleteStrings(ctx, c.PostFormArray("strings[]"))) // golylang_strtable.go
	}
	for i := 0; ; i++ {
		orig, ok := c.GetPostForm("original[" + strconv.Itoa(i) + "]")
		if !ok {
			break
		}
		for _, l := range pllTranslatableLangs(currentUser(c), s.pllLanguages(ctx)) {
			v, ok := c.GetPostForm("translation[" + l.Slug + "][" + strconv.Itoa(i) + "]")
			if !ok {
				continue
			}
			if err := pllLog("saving a string translation", s.pllSetString(ctx, l.Slug, orig, v)); err != nil && saveErr == nil {
				saveErr = err
			}
		}
	}
	if c.PostForm("clean") == "1" {
		if err := pllLog("cleaning strings", s.pllCleanStrings(ctx)); err != nil && saveErr == nil { // golylang_strtable.go
			saveErr = err
		}
	}
	s.pllFlushStrings()
	s.purgeAll(ctx)
	back := "/wp-admin/admin?page=mlang_strings&pll_msg=updated"
	if saveErr != nil {
		back = "/wp-admin/admin?page=mlang_strings&pll_msg=save_error"
	}
	if g := c.PostForm("group"); g != "" {
		back += "&group=" + urlQueryEscape(g)
	}
	c.Redirect(http.StatusFound, back)
}

func urlQueryEscape(v string) string { return template.URLQueryEscaper(v) }

// pllSetString stores one translation; an empty one is deleted.
func (s *Server) pllSetString(ctx context.Context, lang, orig, tr string) error {
	tr = strings.TrimSpace(strings.ReplaceAll(tr, "\r\n", "\n"))
	if tr == "" {
		_, err := s.store.Pool.Exec(ctx, `DELETE FROM pll_strings WHERE lang = $1 AND original = $2`, lang, orig)
		return err
	}
	_, err := s.store.Pool.Exec(ctx, `INSERT INTO pll_strings (lang, original, translation) VALUES ($1, $2, $3)
		ON CONFLICT (lang, original) DO UPDATE SET translation = EXCLUDED.translation`, lang, orig, tr)
	return err
}

// pllExportPO downloads the strings of a language as a PO file (Pro: Export translations).
func (s *Server) pllExportPO(c *gin.Context) {
	ctx := c.Request.Context()
	if !s.checkNonce(c, "pll-import-export") {
		s.denyNonce(c)
		return
	}
	l := s.pllLang(ctx, c.PostForm("lang"))
	if l == nil {
		c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_strings&pll_msg=import_error&err="+urlQueryEscape("Choose a language to export."))
		return
	}
	group := c.PostForm("group")
	var b bytes.Buffer
	fmt.Fprintf(&b, "msgid \"\"\nmsgstr \"\"\n\"Project-Id-Version: %s\\n\"\n\"POT-Creation-Date: %s\\n\"\n\"Language: %s\\n\"\n\"MIME-Version: 1.0\\n\"\n\"Content-Type: text/plain; charset=UTF-8\\n\"\n\"Content-Transfer-Encoding: 8bit\\n\"\n\"X-Generator: Pursae Multi Language %s\\n\"\n\"Site-Reference: %s\\n\"\n\n",
		poEscape(s.settings.String("blogname")), time.Now().UTC().Format("2006-01-02 15:04-0700"), l.Locale, Version, poEscape(s.links(c).Home))
	tr := s.pllStringMap(ctx, l.Slug)
	for _, r := range s.pllStrings(ctx) {
		if group != "" && r.Group != group {
			continue
		}
		ctxt := r.Group // the group; a gettext context (_x) is the msgctxt of its string
		if r.Context != "" {
			ctxt = r.Context
		}
		fmt.Fprintf(&b, "#. %s\nmsgctxt \"%s\"\nmsgid \"%s\"\nmsgstr \"%s\"\n\n", strings.ReplaceAll(r.Name, "\n", " "), poEscape(ctxt), poEscape(r.String), poEscape(tr[r.Key()]))
	}
	c.Header("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(l.Locale, `"`, "")+`.po"`)
	c.Data(http.StatusOK, "text/x-gettext-translation; charset=UTF-8", b.Bytes())
}

// pllImportPO reads a PO file into one language: the Language header picks it, the select is the fallback.
func (s *Server) pllImportPO(c *gin.Context) {
	ctx := c.Request.Context()
	if !s.checkNonce(c, "pll-import-export") {
		s.denyNonce(c)
		return
	}
	fail := func(msg string) {
		c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_strings&pll_msg=import_error&err="+urlQueryEscape(msg))
	}
	fh, err := c.FormFile("po")
	if err != nil {
		fail("Choose a .po file to import.")
		return
	}
	f, err := fh.Open()
	if err != nil {
		fail(err.Error())
		return
	}
	defer f.Close()
	entries, header, err := parsePO(io.LimitReader(f, 8<<20))
	if err != nil {
		fail("This file is not a valid PO file: " + err.Error())
		return
	}
	l := s.pllLang(ctx, c.PostForm("lang"))
	if loc := header["Language"]; loc != "" {
		for _, o := range s.pllLanguages(ctx) {
			if o.Locale == loc || o.Slug == loc {
				l = o
			}
		}
	}
	if l == nil {
		fail("The language of the file is not one of the site's languages.")
		return
	}
	withCtx := map[string]bool{} // the context\x04string keys of strings with a gettext context
	for _, r := range s.pllStrings(ctx) {
		if r.Context != "" {
			withCtx[r.Key()] = true
		}
	}
	n := 0
	for _, e := range entries {
		if e.id == "" || e.str == "" {
			continue
		}
		key := e.id
		if k := e.ctx + "\x04" + e.id; e.ctx != "" && withCtx[k] {
			key = k
		}
		if s.pllSetString(ctx, l.Slug, key, e.str) == nil {
			n++
		}
	}
	s.pllFlushStrings()
	s.purgeAll(ctx)
	c.Redirect(http.StatusFound, "/wp-admin/admin?page=mlang_strings&pll_msg=imported&n="+strconv.Itoa(n))
}

func poEscape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", "").Replace(v)
}

func poUnescape(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
			switch v[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(v[i])
			}
			continue
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

type poEntry struct{ ctx, id, str string }

// parsePO reads msgctxt/msgid/msgstr entries (multi-line strings included) and the header fields.
func parsePO(r io.Reader) ([]poEntry, map[string]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	var entries []poEntry
	var cur poEntry
	var field *string
	seen := false
	flush := func() {
		if seen {
			entries = append(entries, cur)
		}
		cur, field, seen = poEntry{}, nil, false
	}
	quoted := func(s string) (string, bool) {
		s = strings.TrimSpace(s)
		if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
			return "", false
		}
		return poUnescape(s[1 : len(s)-1]), true
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "msgctxt "):
			if field == &cur.str {
				flush()
			}
			v, ok := quoted(line[len("msgctxt "):])
			if !ok {
				return nil, nil, fmt.Errorf("bad msgctxt line")
			}
			cur.ctx, field, seen = v, &cur.ctx, true
		case strings.HasPrefix(line, "msgid "):
			if field == &cur.str {
				flush()
			}
			v, ok := quoted(line[len("msgid "):])
			if !ok {
				return nil, nil, fmt.Errorf("bad msgid line")
			}
			cur.id, field, seen = v, &cur.id, true
		case strings.HasPrefix(line, "msgstr "):
			v, ok := quoted(line[len("msgstr "):])
			if !ok {
				return nil, nil, fmt.Errorf("bad msgstr line")
			}
			cur.str, field = v, &cur.str
		case strings.HasPrefix(line, `"`):
			v, ok := quoted(line)
			if !ok || field == nil {
				return nil, nil, fmt.Errorf("bad continuation line")
			}
			*field += v
		default:
			return nil, nil, fmt.Errorf("unexpected line %q", line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	flush()
	header := map[string]string{}
	var out []poEntry
	for _, e := range entries {
		if e.id == "" && e.ctx == "" {
			for _, hl := range strings.Split(e.str, "\n") {
				if k, v, ok := strings.Cut(hl, ":"); ok {
					header[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 && len(header) == 0 {
		return nil, nil, fmt.Errorf("no entries")
	}
	return out, header, nil
}
