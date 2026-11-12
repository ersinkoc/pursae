package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"wpgo/internal/mail"
	"wpgo/web"
)

// Pursae Security › Integrity: the file integrity scan (Wordfence's "core file" scan, for a site
// whose core is one Go binary). It checks
//   - the core: with the web files embedded (production) the binary's SHA-256 is recorded, and a
//     binary that changed while the version stayed the same is reported; with WPGO_WEB_DIR set
//     (development) every file on disk is compared with the copy the binary carries;
//   - the bundled themes: edited copies in the themes directory (Theme File Editor overrides) that
//     differ from the bundled file;
//   - the themes and uploads directories: server-side scripts and executables, which Pursae never
//     writes there (a .php file in uploads is the classic web-shell).
// The daily job (adminsecgo_daily) runs it and e-mails new findings; the screen runs it on demand.

const asIntegrityOption = "adminsec_integrity"

// asIntegrityMaxFiles caps one walk (a huge uploads tree is scanned in part and says so).
const asIntegrityMaxFiles = 250_000

type asIntegrityFinding struct {
	Kind   string `json:"kind"` // modified, missing, added, suspicious, binary
	Area   string `json:"area"` // core, theme, uploads
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

func (f asIntegrityFinding) key() string { return f.Kind + "|" + f.Area + "|" + f.Path }

type asIntegrityReport struct {
	Scanned   time.Time            `json:"scanned"`
	Files     int                  `json:"files"`
	Findings  []asIntegrityFinding `json:"findings"`
	Notes     []string             `json:"notes"`
	Binary    string               `json:"binary"` // SHA-256 of the running executable
	Version   string               `json:"version"`
	Truncated bool                 `json:"truncated"`
}

// asSuspiciousExt are file types Pursae never stores in the themes or uploads directories.
var asSuspiciousExt = map[string]bool{".php": true, ".php3": true, ".php4": true, ".php5": true, ".php7": true, ".php8": true, ".phtml": true,
	".phar": true, ".pht": true, ".phps": true, ".cgi": true, ".pl": true, ".py": true, ".sh": true, ".bash": true, ".exe": true, ".dll": true,
	".so": true, ".jsp": true, ".jspx": true, ".asp": true, ".aspx": true, ".ashx": true, ".htaccess": true, ".user.ini": true}

// asSuspiciousName reports a server-side script, also hidden behind a second extension
// ("shell.php.jpg") or as a dot-file (".htaccess").
func asSuspiciousName(name string) (bool, string) {
	low := strings.ToLower(name)
	if low == ".htaccess" || low == ".user.ini" {
		return true, "server configuration file"
	}
	if asSuspiciousExt[path.Ext(low)] {
		return true, "server-side script or executable"
	}
	for _, part := range strings.Split(low, ".")[1:] {
		if asSuspiciousExt["."+part] {
			return true, "server-side script extension hidden in the name"
		}
	}
	return false, ""
}

func asHashReader(r io.Reader) string {
	h := sha256.New()
	_, _ = io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil))
}

func asHashFS(fsys fs.FS, name string) (string, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return asHashReader(f), nil
}

func asHashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return asHashReader(f), nil
}

// asIntegrityScan runs the scan. prev (may be nil) is the last report: the binary check compares
// against it.
func (s *Server) asIntegrityScan(ctx context.Context, prev *asIntegrityReport) *asIntegrityReport {
	rep := &asIntegrityReport{Scanned: time.Now().UTC(), Version: Version}
	add := func(f asIntegrityFinding) { rep.Findings = append(rep.Findings, f) }
	budget := func() bool {
		rep.Files++
		if rep.Files > asIntegrityMaxFiles {
			rep.Truncated = true
			return false
		}
		return ctx.Err() == nil
	}
	embedded := web.FS("")

	// 1. Core.
	if exe, err := os.Executable(); err == nil {
		if sum, err := asHashFile(exe); err == nil {
			rep.Binary = sum
			if prev != nil && prev.Binary != "" && prev.Binary != sum && prev.Version == Version {
				add(asIntegrityFinding{Kind: "binary", Area: "core", Path: filepath.Base(exe),
					Detail: "The server binary changed since the last scan, but reports the same version (" + Version + "). Reinstall it from an official release."})
			}
		}
	}
	if dir := s.cfg.WebDir; dir != "" {
		rep.Notes = append(rep.Notes, "WPGO_WEB_DIR is set: the core web files are read from "+dir+" and compared with the copies in the binary.")
		disk := os.DirFS(dir)
		seen := map[string]bool{}
		_ = fs.WalkDir(embedded, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !budget() {
				return nil
			}
			seen[p] = true
			want, err1 := asHashFS(embedded, p)
			got, err2 := asHashFS(disk, p)
			switch {
			case err2 != nil:
				add(asIntegrityFinding{Kind: "missing", Area: "core", Path: p, Detail: "In the binary, missing on disk."})
			case err1 == nil && want != got:
				add(asIntegrityFinding{Kind: "modified", Area: "core", Path: p, Detail: "Differs from the copy in the binary."})
			}
			return nil
		})
		_ = fs.WalkDir(disk, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || seen[p] || !budget() {
				return nil
			}
			if bad, why := asSuspiciousName(d.Name()); bad {
				add(asIntegrityFinding{Kind: "suspicious", Area: "core", Path: p, Detail: why + "; not part of Pursae."})
			} else {
				add(asIntegrityFinding{Kind: "added", Area: "core", Path: p, Detail: "Not part of this Pursae version."})
			}
			return nil
		})
	} else {
		rep.Notes = append(rep.Notes, "The core web files (admin, wp-includes, bundled themes) are embedded in the binary: they cannot be changed on disk.")
	}

	// 2. Bundled themes edited on disk, and scripts in the themes directory.
	if ov, ok := s.web.(overlayFS); ok && ov.dir != "" {
		_ = filepath.WalkDir(ov.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !budget() {
				return nil
			}
			rel, _ := filepath.Rel(ov.dir, p)
			rel = filepath.ToSlash(rel)
			if bad, why := asSuspiciousName(d.Name()); bad {
				add(asIntegrityFinding{Kind: "suspicious", Area: "theme", Path: "themes/" + rel, Detail: why + "; Pursae themes never contain one."})
				return nil
			}
			want, err := asHashFS(embedded, "themes/"+rel)
			if err != nil {
				return nil // a theme package's own file, or a new file of a package
			}
			if got, err := asHashFile(p); err == nil && got != want {
				add(asIntegrityFinding{Kind: "modified", Area: "theme", Path: "themes/" + rel, Detail: "An edited copy of a bundled theme file (Theme File Editor or a change on disk)."})
			}
			return nil
		})
	}

	// 3. Uploads.
	if up := s.cfg.UploadsDir; up != "" {
		_ = filepath.WalkDir(up, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !budget() {
				if rep.Truncated {
					return filepath.SkipAll
				}
				return nil
			}
			if bad, why := asSuspiciousName(d.Name()); bad {
				rel, _ := filepath.Rel(up, p)
				add(asIntegrityFinding{Kind: "suspicious", Area: "uploads", Path: "uploads/" + filepath.ToSlash(rel), Detail: why + " in the uploads folder."})
			}
			return nil
		})
	}
	if rep.Truncated {
		rep.Notes = append(rep.Notes, fmt.Sprintf("Only the first %d files were scanned.", asIntegrityMaxFiles))
	}
	sort.Slice(rep.Findings, func(i, j int) bool { return rep.Findings[i].key() < rep.Findings[j].key() })
	return rep
}

func (s *Server) asIntegrityLast() *asIntegrityReport {
	raw := s.settings.Get(asIntegrityOption)
	if raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var r asIntegrityReport
	if json.Unmarshal(b, &r) != nil || r.Scanned.IsZero() {
		return nil
	}
	return &r
}

func (s *Server) asIntegritySave(ctx context.Context, r *asIntegrityReport) error {
	b, _ := json.Marshal(r)
	var m map[string]any
	_ = json.NewDecoder(bytes.NewReader(b)).Decode(&m)
	return s.settings.Set(ctx, map[string]any{asIntegrityOption: m})
}

// asIntegrityRun scans, stores the report and returns it with the findings that are new since
// the previous scan.
func (s *Server) asIntegrityRun(ctx context.Context) (*asIntegrityReport, []asIntegrityFinding) {
	prev := s.asIntegrityLast()
	rep := s.asIntegrityScan(ctx, prev)
	old := map[string]bool{}
	if prev != nil {
		for _, f := range prev.Findings {
			old[f.key()] = true
		}
	}
	var fresh []asIntegrityFinding
	for _, f := range rep.Findings {
		if !old[f.key()] {
			fresh = append(fresh, f)
		}
	}
	if err := s.asIntegritySave(ctx, rep); err != nil {
		return rep, fresh
	}
	return rep, fresh
}

// asIntegrityDaily is the daily job's part: scan, log and e-mail what is new.
func (s *Server) asIntegrityDaily(ctx context.Context) {
	rep, fresh := s.asIntegrityRun(ctx)
	if len(fresh) == 0 {
		return
	}
	s.asAudit(ctx, asEvent{Event: "integrity_findings", Severity: "warning", Object: fresh[0].Path,
		Message: fmt.Sprintf("The integrity scan found %d new issue(s) (%d in all).", len(fresh), len(rep.Findings))})
	st := s.asLoad()
	if !st.AlertIntegrity {
		return
	}
	site := s.settings.String("blogname")
	var b strings.Builder
	fmt.Fprintf(&b, "The daily file integrity scan of %s found %d new issue(s):\n\n", site, len(fresh))
	for i, f := range fresh {
		if i == 20 {
			fmt.Fprintf(&b, "… and %d more.\n", len(fresh)-20)
			break
		}
		fmt.Fprintf(&b, "- [%s, %s] %s: %s\n", f.Area, f.Kind, f.Path, f.Detail)
	}
	fmt.Fprintf(&b, "\nReview them in Pursae Security › Integrity:\n%s\n", strings.TrimRight(s.settings.String("home"), "/")+"/wp-admin/admin?page="+asSlug+"-integrity")
	s.sendMail(ctx, mail.Message{To: []string{s.asAlertTo(st)}, Subject: "[" + site + "] File integrity scan: new findings", Body: b.String()})
}

// asIntegrityPage is the Integrity screen: the last report and "Scan now".
func (s *Server) asIntegrityPage(c *gin.Context, d *asAdminData) (redirected bool, notices []notice) {
	if c.Request.Method == http.MethodPost {
		if !s.checkNonce(c, "adminsec-admin") {
			s.denyNonce(c)
			return true, nil
		}
		_, fresh := s.asIntegrityRun(c.Request.Context())
		s.asAudit(c, asEvent{Event: "integrity_scan", Severity: "info", Message: fmt.Sprintf("Integrity scan run (%d new finding(s)).", len(fresh))})
		c.Redirect(http.StatusFound, "/wp-admin/admin?page="+asSlug+"-integrity&scanned=1")
		return true, nil
	}
	if c.Query("scanned") == "1" {
		notices = append(notices, okNotice("Scan complete.")...)
	}
	d.Integrity = s.asIntegrityLast()
	return false, notices
}
