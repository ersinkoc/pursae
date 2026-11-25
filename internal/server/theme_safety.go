package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/i18n"
)

// A core update must never silently break or silently override a user's own theme work:
//
//  1. A theme package that no longer loads (a stricter validator, requires_core, a broken file) is
//     remembered with its reason and shown as an admin notice. When it is the active theme the site
//     falls back to the default theme, but the stylesheet option is left alone so the theme comes
//     back as soon as it loads again.
//  2. A bundled theme file edited in the Theme File Editor shadows the embedded original. The hash
//     of the original it was based on is recorded (themeEditBaseFile); when a core update ships a
//     different original, the editor lists the file as "updated by Pursae since you edited it".
//  3. Updating an uploaded package replaces its folder; the files edited on the server are copied
//     to <themes>/.backup/<slug>-<version>-<time>/ first.

// ---- 1. Packages that failed to load ----

type themeLoadFailure struct{ Slug, Name, Reason string }

var (
	themeFailMu sync.Mutex
	// themeFailures is keyed by themes directory (every Server of the process shares the theme
	// registry, but tests and preview sandboxes use their own directories).
	themeFailures = map[string]map[string]themeLoadFailure{}
)

// resetThemeLoadFailures forgets the failures of a themes directory (loadThemePackages starts over).
func resetThemeLoadFailures(dir string) {
	themeFailMu.Lock()
	delete(themeFailures, dir)
	themeFailMu.Unlock()
}

// recordThemeLoadFailure remembers why the package in <dir>/<slug> was skipped.
func recordThemeLoadFailure(dir, slug string, err error) {
	name := slug
	if b, rerr := os.ReadFile(filepath.Join(dir, slug, "theme.json")); rerr == nil {
		var m struct{ Name string }
		if json.Unmarshal(b, &m) == nil && strings.TrimSpace(m.Name) != "" {
			name = strings.TrimSpace(m.Name)
		}
	}
	themeFailMu.Lock()
	defer themeFailMu.Unlock()
	if themeFailures[dir] == nil {
		themeFailures[dir] = map[string]themeLoadFailure{}
	}
	themeFailures[dir][slug] = themeLoadFailure{Slug: slug, Name: name, Reason: err.Error()}
}

// clearThemeLoadFailure is called when the package of slug loaded (installed or updated) again.
func clearThemeLoadFailure(dir, slug string) {
	themeFailMu.Lock()
	delete(themeFailures[dir], slug)
	themeFailMu.Unlock()
}

// themeLoadFailures lists the packages of this site that failed to load, by slug.
func (s *Server) themeLoadFailures() []themeLoadFailure {
	themeFailMu.Lock()
	defer themeFailMu.Unlock()
	var out []themeLoadFailure
	for _, f := range themeFailures[s.themesDir()] {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// themeLoadNotices are the admin notices for packages that failed to load, for users who may switch
// or install themes. They show on the Themes screens and the dashboard.
func (s *Server) themeLoadNotices(c *gin.Context) []notice {
	u := currentUser(c)
	if u == nil || (!u.Can("switch_themes", nil) && !u.Can("install_themes", nil)) {
		return nil
	}
	stylesheet := s.settings.String("stylesheet")
	var out []notice
	for _, f := range s.themeLoadFailures() {
		name, reason := template.HTMLEscapeString(f.Name), template.HTMLEscapeString(f.Reason)
		if f.Slug == stylesheet && s.installedTheme(f.Slug) == nil {
			fallback := template.HTMLEscapeString(s.activeTheme().Name)
			out = append(out, notice{Type: "error", Message: template.HTML(i18n.Sprintf(s.__(c,
				"<strong>Your active theme %1$s could not be loaded:</strong> %2$s. Visitors see %3$s until it is fixed. Your theme choice is kept: %1$s comes back as soon as it loads again."),
				name, reason, fallback))})
			continue
		}
		out = append(out, notice{Type: "warning", Message: template.HTML(i18n.Sprintf(s.__(c,
			"<strong>The theme %1$s could not be loaded:</strong> %2$s"), name, reason))})
	}
	return out
}

// ---- 2. Edited bundled theme files whose original changed ----

// themeEditBaseFile, in <themes>/<slug>/, maps each edited file of a bundled theme (its path in
// the theme, "assets/style.css", "templates/single.html") to the SHA-256 of the embedded original
// the edit was based on. Edited copies saved before it existed have no entry: their base is unknown,
// so they are not flagged (recording today's original on first sight would hide an update that
// shipped together with this check); saving the file again records the base.
const themeEditBaseFile = ".edit-base.json"

var themeEditBaseMu sync.Mutex

func readThemeEditBase(dir, slug string) map[string]string {
	m := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(dir, slug, themeEditBaseFile)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func writeThemeEditBase(dir, slug string, m map[string]string) error {
	p := filepath.Join(dir, slug, themeEditBaseFile)
	if len(m) == 0 {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// recordThemeEditBase notes the embedded original an edited copy of webPath ("themes/<slug>/…") is
// based on; edited=false drops the entry (the copy was removed).
func recordThemeEditBase(o overlayFS, webPath string, edited bool) error {
	rel, ok := strings.CutPrefix(webPath, "themes/")
	if !ok || o.dir == "" {
		return nil
	}
	slug, file, ok := strings.Cut(rel, "/")
	if !ok {
		return nil
	}
	themeEditBaseMu.Lock()
	defer themeEditBaseMu.Unlock()
	m := readThemeEditBase(o.dir, slug)
	if edited {
		orig, err := fs.ReadFile(o.base, webPath)
		if err != nil {
			return nil // no embedded original: nothing to compare with
		}
		m[file] = hashBytes(orig)
	} else {
		if _, had := m[file]; !had {
			return nil
		}
		delete(m, file)
	}
	return writeThemeEditBase(o.dir, slug, m)
}

// themeStaleEdit is an edited bundled theme file whose embedded original changed since the edit.
type themeStaleEdit struct {
	File     string // editor path ("style.css", "templates/single.html")
	ThemeRel string // path in the theme ("assets/style.css")
	Removed  bool   // the original is no longer shipped
}

// themeStaleEdits lists the edited files of a bundled theme that Pursae updated since they were edited.
func (s *Server) themeStaleEdits(slug string) []themeStaleEdit {
	o, ok := s.overlay()
	if !ok || o.dir == "" {
		return nil
	}
	if t := s.installedTheme(slug); t == nil || t.Package != nil {
		return nil
	}
	return staleThemeEdits(o, slug)
}

// staleThemeEdits compares the recorded bases of the edited files of slug with the embedded originals.
func staleThemeEdits(o overlayFS, slug string) []themeStaleEdit {
	themeEditBaseMu.Lock()
	m := readThemeEditBase(o.dir, slug)
	themeEditBaseMu.Unlock()
	var out []themeStaleEdit
	for rel, base := range m {
		webPath := "themes/" + slug + "/" + rel
		if !o.overridden(webPath) {
			continue // restored meanwhile
		}
		file := strings.TrimPrefix(rel, "assets/")
		orig, err := fs.ReadFile(o.base, webPath)
		if err != nil {
			out = append(out, themeStaleEdit{File: file, ThemeRel: rel, Removed: true})
			continue
		}
		if hashBytes(orig) != base {
			out = append(out, themeStaleEdit{File: file, ThemeRel: rel})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// themeEditorStaleNotice is the Theme File Editor's notice for the files of slug that Pursae
// updated since they were edited, with a link to the new original and the Restore action.
func (s *Server) themeEditorStaleNotice(c *gin.Context, slug string) []notice {
	stale := s.themeStaleEdits(slug)
	if len(stale) == 0 {
		return nil
	}
	q := url.QueryEscape
	var b strings.Builder
	b.WriteString("<p><strong>" + template.HTMLEscapeString(s.__(c, "These files were updated by Pursae since you edited them. Your edited copy is still in use; compare it with the new original and restore it if you want the update.")) + "</strong></p><ul>")
	for _, e := range stale {
		b.WriteString(`<li><a href="/wp-admin/theme-editor?theme=` + q(slug) + `&amp;file=` + q(e.File) + `"><code>` + template.HTMLEscapeString(e.File) + `</code></a>`)
		if e.Removed {
			b.WriteString(" &mdash; " + template.HTMLEscapeString(s.__(c, "no longer part of the theme")))
		} else {
			b.WriteString(` &mdash; <a href="/wp-admin/theme-editor?theme=` + q(slug) + `&amp;file=` + q(e.File) + `&amp;original=1">` +
				template.HTMLEscapeString(s.__(c, "View the new original")) + `</a> | <a href="/wp-admin/theme-editor?action=restore&amp;theme=` + q(slug) +
				`&amp;file=` + q(e.File) + `&amp;_wpnonce=` + q(s.nonce(c, "restore-theme-file_"+slug+"_"+e.File)) + `">` +
				template.HTMLEscapeString(s.__(c, "Restore the original")) + `</a>`)
		}
		b.WriteString("</li>")
	}
	b.WriteString("</ul><p>" + template.HTMLEscapeString(s.__(c, "Saving a file again keeps your version and hides it from this list.")) + "</p>")
	return []notice{{Type: "warning", Message: template.HTML(b.String())}}
}

// themeStaleEditsNotices tell users who edit themes, on the Themes screen, which bundled themes
// have edited files that Pursae updated since.
func (s *Server) themeStaleEditsNotices(c *gin.Context) []notice {
	if !currentUser(c).Can("edit_themes", nil) {
		return nil
	}
	var out []notice
	for _, t := range s.installedThemes() {
		if t.Package != nil {
			continue
		}
		if n := len(s.themeStaleEdits(t.Slug)); n > 0 {
			out = append(out, notice{Type: "warning", Message: template.HTML(i18n.Sprintf(s.__(c,
				"Pursae updated %1$s file(s) of %2$s that you edited in the Theme File Editor; your edited copies are still in use. <a href=\"%3$s\">Review them</a>"),
				strconv.Itoa(n), template.HTMLEscapeString(t.Name), "/wp-admin/theme-editor?theme="+url.QueryEscape(t.Slug)))})
		}
	}
	return out
}

// ---- 3. Edited files of a package that is being updated ----

// themeBackupDir holds copies of edited package files taken before an update replaced them.
const themeBackupDir = ".backup"

// themeTemplateEditPrefix marks a template entry in themeEditedFile ("/templates/single.html");
// the other entries are paths under assets/, which never start with a slash.
const themeTemplateEditPrefix = "/"

var (
	themeBackupMu   sync.Mutex
	themeBackupDone = map[string]map[string]themeBackupInfo{} // themes dir → slug → last backup
)

type themeBackupInfo struct {
	Dir   string
	Files []string
}

// backupEditedThemeFiles copies the files of the installed package in pkgDir that were changed in
// the Theme File Editor to <themes>/.backup/<slug>-<version>-<time>/ before the package is replaced.
// It returns the backup directory ("" when nothing was edited).
func backupEditedThemeFiles(themesDir string, pkg *themePackage, slug, version string) (string, []string, error) {
	pkg.editedMu.Lock()
	var edited []string
	for f := range pkg.edited {
		edited = append(edited, f)
	}
	pkg.editedMu.Unlock()
	sort.Strings(edited)
	var rels []string
	for _, e := range edited {
		rel := "assets/" + e
		if t, ok := strings.CutPrefix(e, themeTemplateEditPrefix); ok {
			rel = t
		}
		if filepath.IsAbs(filepath.FromSlash(rel)) || strings.Contains(rel, "..") {
			continue
		}
		if st, err := os.Stat(filepath.Join(pkg.Dir, filepath.FromSlash(rel))); err == nil && !st.IsDir() {
			rels = append(rels, rel)
		}
	}
	if len(rels) == 0 {
		return "", nil, nil
	}
	dst := filepath.Join(themesDir, themeBackupDir, slug+"-"+version+"-"+time.Now().UTC().Format("20060102-150405"))
	for _, rel := range rels {
		if err := copyThemeFile(filepath.Join(pkg.Dir, filepath.FromSlash(rel)), filepath.Join(dst, filepath.FromSlash(rel))); err != nil {
			return "", nil, err
		}
	}
	_ = copyThemeFile(filepath.Join(pkg.Dir, themeEditedFile), filepath.Join(dst, themeEditedFile))
	return dst, rels, nil
}

func copyThemeFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func rememberThemeBackup(themesDir, slug, dir string, files []string) {
	log.Printf("theme %s: %d edited file(s) backed up to %s before the update", slug, len(files), dir)
	themeBackupMu.Lock()
	defer themeBackupMu.Unlock()
	if themeBackupDone[themesDir] == nil {
		themeBackupDone[themesDir] = map[string]themeBackupInfo{}
	}
	themeBackupDone[themesDir][slug] = themeBackupInfo{Dir: dir, Files: files}
}

// themeBackupNotices tell, once, on the Themes screen which edited files were backed up before an update.
func (s *Server) themeBackupNotices(c *gin.Context) []notice {
	u := currentUser(c)
	if !u.Can("switch_themes", nil) && !u.Can("install_themes", nil) {
		return nil
	}
	themeBackupMu.Lock()
	done := themeBackupDone[s.themesDir()]
	delete(themeBackupDone, s.themesDir())
	themeBackupMu.Unlock()
	slugs := make([]string, 0, len(done))
	for slug := range done {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	var out []notice
	for _, slug := range slugs {
		b := done[slug]
		name := slug
		if t := themeBySlug(slug); t != nil {
			name = t.Name
		}
		files := make([]string, len(b.Files))
		for i, f := range b.Files {
			files[i] = "<code>" + template.HTMLEscapeString(f) + "</code>"
		}
		out = append(out, notice{Type: "warning", Dismissible: true, Message: template.HTML(i18n.Sprintf(s.__(c,
			"The update of %1$s replaced files you had edited on this server. Your edited copies were saved to <code>%2$s</code>: %3$s"),
			template.HTMLEscapeString(name), template.HTMLEscapeString(b.Dir), strings.Join(files, ", ")))})
	}
	return out
}
