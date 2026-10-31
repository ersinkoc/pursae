package server

// Golementor Custom Icons (port of upstream Pro 4.3.0's modules/assets-manager/asset-types/icons):
// administrators upload an icon font exported from Fontello, IcoMoon or Fontastic as a .zip; the
// archive is read in memory with zip-slip and size limits, recognised by its data file, and only
// its stylesheet and web fonts are written to uploads/gowebeditor/custom-icons/<name>/. The set is
// then listed in the editor's icon library (Icons_Manager additional tabs) and its stylesheet is
// printed on pages whose Golementor content uses one of its icons.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
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
)

const (
	gweIconsAdminPath  = "/wp-admin/web-builder-icons"
	gweIconsUploadsRel = gweUploadsDir + "/custom-icons"
	gweIconsSetting    = "gwe_custom_icon_sets"

	gweIconsMaxZip     = 20 << 20 // uploaded archive
	gweIconsMaxTotal   = 60 << 20 // all extracted entries
	gweIconsMaxEntry   = 15 << 20 // one extracted entry
	gweIconsMaxEntries = 3000
)

// gweIconSet is one uploaded icon set (Custom_Icons' elementor_custom_icon_set_config).
type gweIconSet struct {
	Name          string   `json:"name"`
	Label         string   `json:"label"`
	Type          string   `json:"custom_icon_type"`
	Prefix        string   `json:"prefix"`
	DisplayPrefix string   `json:"displayPrefix"`
	Stylesheet    string   `json:"stylesheet"` // path inside the set directory
	Ver           string   `json:"ver"`
	Icons         []string `json:"icons"`
	Created       int64    `json:"created"`
}

// gweIconAllowedExt are the entries read from an archive (upstream's extract_zip list).
var gweIconAllowedExt = map[string]bool{"css": true, "eot": true, "html": true, "json": true, "otf": true, "svg": true,
	"ttf": true, "txt": true, "woff": true, "woff2": true}

// gweIconWebfontExt are the files written to uploads besides the stylesheet.
var gweIconWebfontExt = map[string]bool{"woff": true, "woff2": true, "ttf": true, "svg": true, "otf": true, "eot": true}

func (s *Server) gweIconSets() []gweIconSet {
	var out []gweIconSet
	switch v := s.settings.Get(gweIconsSetting).(type) {
	case nil:
	case string:
		_ = json.Unmarshal([]byte(v), &out)
	default:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (s *Server) gweSaveIconSets(c *gin.Context, sets []gweIconSet) error {
	b, _ := json.Marshal(sets)
	var v any
	_ = json.Unmarshal(b, &v)
	if v == nil {
		v = []any{}
	}
	return s.settings.Set(c, map[string]any{gweIconsSetting: v})
}

func (s *Server) gweIconSetURL(c *gin.Context, set gweIconSet, file string) string {
	return s.uploadURL(c, gweIconsUploadsRel+"/"+set.Name+"/"+file)
}

// gweIconLibraryConfig is the set's entry in the editor config.icons.libraries (Icon_Set_Base::build_config).
func (s *Server) gweIconLibraryConfig(c *gin.Context, set gweIconSet) map[string]any {
	cfg := map[string]any{
		"name": set.Name, "label": set.Label, "url": s.gweIconSetURL(c, set, set.Stylesheet), "enqueue": false,
		"prefix": set.Prefix, "displayPrefix": set.DisplayPrefix, "labelIcon": "eicon eicon-folder", "ver": set.Ver,
		"custom_icon_type": set.Type, "count": len(set.Icons), "native": false,
	}
	if len(set.Icons) > 25 {
		cfg["fetchJson"] = s.gweIconSetURL(c, set, "e_icons.js")
	} else {
		cfg["icons"] = set.Icons
	}
	return cfg
}

// gweCustomIconsEditorConfig adds the uploaded sets to the editor's icon library and points the
// library's "Custom Icons" link at the admin screen.
func (s *Server) gweCustomIconsEditorConfig(c *gin.Context, cfg map[string]any) {
	cfg["customIconsURL"] = s.links(c).HomeURL(gweIconsAdminPath)
	sets := s.gweIconSets()
	if len(sets) == 0 {
		return
	}
	var icons map[string]any
	switch v := cfg["icons"].(type) {
	case json.RawMessage:
		_ = json.Unmarshal(v, &icons)
	case map[string]any:
		icons = v
	}
	if icons == nil {
		return
	}
	libs, _ := icons["libraries"].([]any)
	for _, set := range sets {
		libs = append(libs, s.gweIconLibraryConfig(c, set))
	}
	icons["libraries"] = libs
	cfg["icons"] = icons
}

var gweClassAttrRe = regexp.MustCompile(`\sclass="([^"]*)"`)

// gweCustomIconsUsed lists the sets whose icons appear in markup.
func gweCustomIconsUsed(sets []gweIconSet, markup ...string) []gweIconSet {
	tokens := map[string]bool{}
	for _, m := range markup {
		for _, a := range gweClassAttrRe.FindAllStringSubmatch(m, -1) {
			for _, t := range strings.Fields(a[1]) {
				tokens[t] = true
			}
		}
	}
	var out []gweIconSet
	for _, set := range sets {
		if set.Prefix == "" {
			continue
		}
		names := make(map[string]bool, len(set.Icons))
		for _, n := range set.Icons {
			names[n] = true
		}
		for t := range tokens {
			if strings.HasPrefix(t, set.Prefix) && names[strings.TrimPrefix(t, set.Prefix)] {
				out = append(out, set)
				break
			}
		}
	}
	return out
}

// gweCustomIconsHead prints the stylesheets of the custom icon sets the page uses (every set in
// the editor preview, where icons are added at any time).
func (s *Server) gweCustomIconsHead(c *gin.Context, results []gweRendered, preview bool) string {
	sets := s.gweIconSets()
	if len(sets) == 0 {
		return ""
	}
	if !preview {
		var markup []string
		for _, r := range results {
			if r.res != nil {
				markup = append(markup, r.res.HTML)
			}
		}
		sets = gweCustomIconsUsed(sets, markup...)
	}
	var sb strings.Builder
	for _, set := range sets {
		sb.WriteString(gweLink("elementor-icons-"+set.Name, s.gweIconSetURL(c, set, set.Stylesheet)+"?ver="+url.QueryEscape(set.Ver)))
	}
	return sb.String()
}

// --- archive handling ----------------------------------------------------------------------------

// gweIconArchive is an extracted archive: clean slash paths (single top folder removed) to bytes.
type gweIconArchive map[string][]byte

func (a gweIconArchive) has(p string) bool { _, ok := a[p]; return ok }

func (a gweIconArchive) hasDir(dir string) bool {
	for p := range a {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return false
}

// gweReadIconZip reads the allowed entries of a zip in memory. Entries with traversal or absolute
// paths, OS X metadata and other file types are skipped; size and count limits bound the work.
func gweReadIconZip(data []byte) (gweIconArchive, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("The file is not a valid zip archive.")
	}
	if len(zr.File) > gweIconsMaxEntries {
		return nil, errors.New("The archive has too many files.")
	}
	files := gweIconArchive{}
	var total int64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			continue
		}
		if strings.HasPrefix(name, "__MACOSX/") || strings.Contains(name, "/__MACOSX/") || strings.Contains(name, "..") ||
			strings.HasPrefix(name, "/") || strings.Contains(name, ":") || strings.ContainsRune(name, 0) {
			continue
		}
		clean := path.Clean(name)
		if clean == "." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			continue
		}
		if base := path.Base(clean); strings.HasPrefix(base, "._") || base == ".DS_Store" {
			continue
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(clean), "."))
		if !gweIconAllowedExt[ext] {
			continue
		}
		if f.UncompressedSize64 > gweIconsMaxEntry {
			return nil, fmt.Errorf("%s is too large.", clean)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, errors.New("The archive could not be read.")
		}
		b, err := io.ReadAll(io.LimitReader(rc, gweIconsMaxEntry+1))
		rc.Close()
		if err != nil {
			return nil, errors.New("The archive could not be read.")
		}
		if len(b) > gweIconsMaxEntry {
			return nil, fmt.Errorf("%s is too large.", clean)
		}
		total += int64(len(b))
		if total > gweIconsMaxTotal {
			return nil, errors.New("The archive is too large once extracted.")
		}
		files[clean] = b
	}
	if len(files) == 0 {
		return nil, errors.New("Could not unzip or empty archive.")
	}
	// One top folder (the usual export layout): its contents are the set.
	top := ""
	for p := range files {
		first, _, ok := strings.Cut(p, "/")
		if !ok {
			top = ""
			break
		}
		if top == "" {
			top = first
		} else if top != first {
			top = ""
			break
		}
	}
	if top != "" {
		out := gweIconArchive{}
		for p, b := range files {
			out[strings.TrimPrefix(p, top+"/")] = b
		}
		files = out
	}
	return files, nil
}

// gweParsedIconSet is a recognised set: its config and the files to write (set-relative path → bytes).
type gweParsedIconSet struct {
	set   gweIconSet
	files map[string][]byte
}

var (
	gweFontasticPrefixRe = regexp.MustCompile(`class\^="([^"]*)"`)
	gweFontasticNameRe   = regexp.MustCompile(`font-family: "([^"]*)"`)
	gweIconNameRe        = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// gweParseIconSet recognises a Fontastic, Fontello or IcoMoon export (upstream's order).
func gweParseIconSet(a gweIconArchive) (*gweParsedIconSet, error) {
	fonts := func(dir string, allowed map[string]bool, out map[string][]byte) {
		for p, b := range a {
			if strings.HasPrefix(p, dir) && !strings.Contains(strings.TrimPrefix(p, dir), "/") &&
				allowed[strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))] {
				out[p] = b
			}
		}
	}
	switch {
	case a.has("icons-reference.html") && a.has("styles.css") && a.hasDir("fonts/"): // Fontastic
		css := string(a["styles.css"])
		pm := gweFontasticPrefixRe.FindStringSubmatch(css)
		nm := gweFontasticNameRe.FindStringSubmatch(css)
		if pm == nil || nm == nil {
			return nil, errors.New("The Fontastic stylesheet has no icon prefix or font name.")
		}
		set := gweIconSet{Type: "Fontastic", Name: nm[1], Prefix: pm[1], Stylesheet: "styles.css"}
		re := regexp.MustCompile(`\.` + regexp.QuoteMeta(set.Prefix) + `(.*)\:before\s\{`)
		for _, m := range re.FindAllStringSubmatch(css, -1) {
			set.Icons = append(set.Icons, m[1])
		}
		files := map[string][]byte{"styles.css": a["styles.css"]}
		fonts("fonts/", map[string]bool{"woff": true, "ttf": true, "svg": true, "eot": true}, files)
		return &gweParsedIconSet{set: set, files: files}, nil
	case a.has("config.json") && a.hasDir("css/") && a.hasDir("font/"): // Fontello
		var cfg struct {
			Name   string `json:"name"`
			Prefix string `json:"css_prefix_text"`
			Glyphs []struct {
				CSS string `json:"css"`
			} `json:"glyphs"`
		}
		if json.Unmarshal(a["config.json"], &cfg) != nil {
			return nil, errors.New("The Fontello config.json could not be read.")
		}
		if cfg.Name == "" {
			cfg.Name = "fontello"
		}
		cssPath := "css/" + cfg.Name + ".css"
		if !a.has(cssPath) {
			return nil, errors.New("The Fontello archive has no " + cssPath + ".")
		}
		set := gweIconSet{Type: "Fontello", Name: cfg.Name, Prefix: cfg.Prefix, Stylesheet: cssPath}
		for _, g := range cfg.Glyphs {
			set.Icons = append(set.Icons, g.CSS)
		}
		css := strings.NewReplacer("margin-left: .2em;", "", "margin-right: .2em;", "").Replace(string(a[cssPath]))
		files := map[string][]byte{cssPath: []byte(css)}
		fonts("font/", map[string]bool{"woff": true, "woff2": true, "ttf": true, "svg": true, "otf": true}, files)
		return &gweParsedIconSet{set: set, files: files}, nil
	case a.has("selection.json") && a.has("style.css") && a.hasDir("fonts/"): // IcoMoon
		var cfg struct {
			Icons []struct {
				Properties struct {
					Name string `json:"name"`
				} `json:"properties"`
			} `json:"icons"`
			Preferences struct {
				FontPref struct {
					Prefix        string `json:"prefix"`
					ClassSelector string `json:"classSelector"`
				} `json:"fontPref"`
			} `json:"preferences"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		if json.Unmarshal(a["selection.json"], &cfg) != nil {
			return nil, errors.New("The IcoMoon selection.json could not be read.")
		}
		set := gweIconSet{Type: "Icomoon", Name: cfg.Metadata.Name, Prefix: cfg.Preferences.FontPref.Prefix,
			DisplayPrefix: strings.ReplaceAll(cfg.Preferences.FontPref.ClassSelector, ".", ""), Stylesheet: "style.css"}
		if set.Name == "" {
			set.Name = "icomoon"
		}
		for _, ic := range cfg.Icons {
			// IcoMoon names a multi-name glyph "a, b": the class uses the first.
			n, _, _ := strings.Cut(ic.Properties.Name, ",")
			set.Icons = append(set.Icons, strings.TrimSpace(n))
		}
		files := map[string][]byte{"style.css": a["style.css"]}
		fonts("fonts/", map[string]bool{"woff": true, "ttf": true, "svg": true, "eot": true}, files)
		return &gweParsedIconSet{set: set, files: files}, nil
	}
	return nil, errors.New("The zip file provided is not supported!")
}

// gweIconSlug is a directory-safe set name.
func gweIconSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		out = "icons"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// gweInstallIconSet validates a parsed set and writes its files; it returns the stored config.
func (s *Server) gweInstallIconSet(p *gweParsedIconSet, label string, existing []gweIconSet) (gweIconSet, error) {
	set := p.set
	if set.Prefix == "" || !gweIconNameRe.MatchString(set.Prefix) {
		return set, errors.New("The icon set has no valid CSS prefix.")
	}
	if set.DisplayPrefix != "" && !gweIconNameRe.MatchString(set.DisplayPrefix) {
		return set, errors.New("The icon set has an invalid class selector.")
	}
	var icons []string
	for _, n := range set.Icons {
		if gweIconNameRe.MatchString(n) {
			icons = append(icons, n)
		}
	}
	if len(icons) == 0 {
		return set, errors.New("The icon set lists no icons.")
	}
	set.Icons = icons
	base := filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(gweIconsUploadsRel))
	if err := os.MkdirAll(base, 0o755); err != nil {
		return set, err
	}
	taken := map[string]bool{}
	for _, e := range existing {
		taken[e.Name] = true
	}
	name := gweIconSlug(set.Name)
	unique := name
	for i := 1; taken[unique] || gweDirExists(filepath.Join(base, unique)); i++ {
		unique = fmt.Sprintf("%s-%d", name, i)
	}
	set.Name = unique
	set.Label = strings.TrimSpace(label)
	if set.Label == "" {
		set.Label = strings.Title(strings.NewReplacer("-", " ", "_", " ").Replace(set.Name)) //nolint:staticcheck // ucwords
	}
	set.Ver = time.Now().UTC().Format("20060102150405")
	set.Created = time.Now().Unix()
	dir := filepath.Join(base, unique)
	write := func(rel string, b []byte) error {
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if r, err := filepath.Rel(dir, target); err != nil || strings.HasPrefix(r, "..") || filepath.IsAbs(r) {
			return errors.New("invalid path in archive")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	}
	for rel, b := range p.files {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))
		if rel != set.Stylesheet && !gweIconWebfontExt[ext] {
			continue
		}
		if err := write(rel, b); err != nil {
			os.RemoveAll(dir)
			return set, err
		}
	}
	if len(set.Icons) > 25 {
		b, _ := json.Marshal(map[string]any{"icons": set.Icons})
		if err := write("e_icons.js", b); err != nil {
			os.RemoveAll(dir)
			return set, err
		}
	}
	return set, nil
}

func gweDirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// --- admin screen ----------------------------------------------------------------------------------

func (s *Server) gweIconsAdmin(c *gin.Context) {
	u := currentUser(c)
	if !u.Can("manage_options", nil) {
		c.String(http.StatusForbidden, "Sorry, you are not allowed to access this page.")
		return
	}
	if c.Request.Method == http.MethodPost {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, gweIconsMaxZip+(1<<20))
		if !s.checkNonce(c, "gwe-custom-icons") {
			s.denyNonce(c)
			return
		}
		msg, err := s.gweIconsAdminPost(c)
		q := ""
		if err != nil {
			q = "?error=" + url.QueryEscape(err.Error())
		} else if msg != "" {
			q = "?message=" + url.QueryEscape(msg)
		}
		c.Redirect(http.StatusFound, gweIconsAdminPath+q)
		return
	}
	esc := html.EscapeString
	nonce := `<input type="hidden" name="_wpnonce" value="` + esc(s.nonce(c, "gwe-custom-icons")) + `" />`
	var b strings.Builder
	b.WriteString(`<p class="description">Upload an icon font exported from Fontello, IcoMoon or Fontastic as a .zip file. Its icons then appear in the icon library of every icon control, and the set's stylesheet loads on pages that use them.</p>`)
	b.WriteString(`<table class="wp-list-table widefat fixed striped"><thead><tr><th>Icon Set</th><th>CSS Prefix</th><th>Icons</th><th>Type</th><th></th></tr></thead><tbody>`)
	sets := s.gweIconSets()
	for _, set := range sets {
		fmt.Fprintf(&b, `<tr><td><strong>%s</strong><br><code>%s</code></td><td><pre>.%s</pre></td><td>%d</td><td>%s</td><td><form method="post">%s<input type="hidden" name="action" value="delete"/><input type="hidden" name="name" value="%s"/><button class="button-link submitdelete" type="submit">Delete</button></form></td></tr>`,
			esc(set.Label), esc(set.Name), esc(set.Prefix), len(set.Icons), esc(set.Type), nonce, esc(set.Name))
	}
	if len(sets) == 0 {
		b.WriteString(`<tr><td colspan="5">No custom icon sets yet.</td></tr>`)
	}
	b.WriteString(`</tbody></table><h2>Add New Icon Set</h2><form method="post" enctype="multipart/form-data">` + nonce + `<input type="hidden" name="action" value="upload"/>`)
	b.WriteString(`<table class="form-table"><tbody><tr><th><label for="gwe-icons-label">Icon Set Name</label></th><td><input id="gwe-icons-label" name="label" class="regular-text" placeholder="Enter Icon Set Name"/></td></tr>`)
	b.WriteString(`<tr><th><label for="gwe-icons-zip">Icon set (.zip)</label></th><td><input id="gwe-icons-zip" type="file" name="zip_upload" accept=".zip,application/zip,application/x-zip,application/x-zip-compressed" required/>`)
	fmt.Fprintf(&b, `<p class="description">Your Fontello, IcoMoon or Fontastic .zip file (at most %d MB).</p></td></tr></tbody></table>`, gweIconsMaxZip>>20)
	b.WriteString(`<p class="submit"><button class="button button-primary" type="submit">Upload</button></p></form>`)

	d := gweProAdminData{Title: "Custom Icons", Body: template.HTML(b.String())}
	for _, t := range gweProTabs {
		if u.Can(t.Cap, nil) || (t.Cap == "unfiltered_html" && u.Can("manage_options", nil)) {
			d.Tabs = append(d.Tabs, gweProTabLink{URL: gweProAdminPath + "?tab=" + t.Tab, Title: t.Title})
		}
		if t.Tab == "fonts" {
			d.Tabs = append(d.Tabs, gweProTabLink{URL: gweIconsAdminPath, Title: "Custom Icons", Active: true})
		}
	}
	var notices []notice
	if m := c.Query("message"); m != "" {
		notices = append(notices, notice{Type: "success", Message: template.HTML(esc(m)), Dismissible: true})
	}
	if e := c.Query("error"); e != "" {
		notices = append(notices, notice{Type: "error", Message: template.HTML(esc(e)), Dismissible: true})
	}
	s.adminPage(c, "web-builder-icons", "Custom Icons ‹ Pursae Web Builder", "toplevel_page_gowebeditor-pro", "admin/gowebeditor-pro", d, notices...)
}

func (s *Server) gweIconsAdminPost(c *gin.Context) (string, error) {
	sets := s.gweIconSets()
	switch c.PostForm("action") {
	case "upload":
		fh, err := c.FormFile("zip_upload")
		if err != nil {
			return "", errors.New("Choose the icon set's .zip file.")
		}
		if !strings.EqualFold(filepath.Ext(fh.Filename), ".zip") {
			return "", errors.New("Only zip files are allowed")
		}
		if fh.Size > gweIconsMaxZip {
			return "", fmt.Errorf("The zip file may be at most %d MB.", gweIconsMaxZip>>20)
		}
		f, err := fh.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(io.LimitReader(f, gweIconsMaxZip+1))
		f.Close()
		if err != nil {
			return "", err
		}
		if len(data) > gweIconsMaxZip {
			return "", fmt.Errorf("The zip file may be at most %d MB.", gweIconsMaxZip>>20)
		}
		arch, err := gweReadIconZip(data)
		if err != nil {
			return "", err
		}
		parsed, err := gweParseIconSet(arch)
		if err != nil {
			return "", err
		}
		set, err := s.gweInstallIconSet(parsed, c.PostForm("label"), sets)
		if err != nil {
			return "", err
		}
		dup := false
		for _, e := range sets {
			if e.Prefix == set.Prefix {
				dup = true
			}
		}
		sets = append(sets, set)
		sort.SliceStable(sets, func(i, j int) bool { return sets[i].Created < sets[j].Created })
		if err := s.gweSaveIconSets(c, sets); err != nil {
			return "", err
		}
		s.purgeAll(c)
		msg := fmt.Sprintf("Icon set “%s” added with %d icons.", set.Label, len(set.Icons))
		if dup {
			msg += " Another set uses the same CSS prefix (." + set.Prefix + "); their icons may conflict."
		}
		return msg, nil
	case "delete":
		name := c.PostForm("name")
		idx := -1
		for i, e := range sets {
			if e.Name == name {
				idx = i
			}
		}
		if idx < 0 || gweIconSlug(name) != name {
			return "", errors.New("Icon set not found.")
		}
		base := filepath.Join(s.cfg.UploadsDir, filepath.FromSlash(gweIconsUploadsRel))
		dir := filepath.Join(base, name)
		if r, err := filepath.Rel(base, dir); err == nil && r == name {
			_ = os.RemoveAll(dir)
		}
		sets = append(sets[:idx], sets[idx+1:]...)
		if err := s.gweSaveIconSets(c, sets); err != nil {
			return "", err
		}
		s.purgeAll(c)
		return "Icon set deleted.", nil
	}
	return "", errors.New("Unknown action.")
}
