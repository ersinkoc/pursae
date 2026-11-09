package server

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// bmgrTableInfo describes one table in an archive.
type bmgrTableInfo struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Rows     int64    `json:"rows"`
	Columns  []string `json:"columns"`
}

// bmgrFilesInfo describes one files group in an archive.
type bmgrFilesInfo struct {
	Group string `json:"group"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// bmgrManifest is manifest.json.
type bmgrManifest struct {
	Format     int             `json:"format"`
	Created    time.Time       `json:"created"`
	Kind       string          `json:"kind"` // manual | safety | scheduled
	Version    string          `json:"version"`
	Schema     string          `json:"schema"` // newest applied migration
	SiteURL    string          `json:"site_url"`
	SiteName   string          `json:"site_name"`
	Note       string          `json:"note,omitempty"`
	Categories []string        `json:"categories"`
	UserMode   string          `json:"user_mode,omitempty"` // all | roles | ids
	Users      int             `json:"users,omitempty"`
	Tables     []bmgrTableInfo `json:"tables"`
	Files      []bmgrFilesInfo `json:"files"`
}

// bmgrSelection is what a backup is asked to contain.
type bmgrSelection struct {
	Categories []string
	UserMode   string // all | roles | ids
	Roles      []string
	UserIDs    []int64
	Note       string
	Kind       string
}

// bmgrEntry is a stored archive as the screen lists it.
type bmgrEntry struct {
	Name     string
	Size     int64
	Modified time.Time
	M        *bmgrManifest
	Err      string
}

var bmgrNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}\.zip$`)

// bmgrCompressed are the file types stored as they are: deflating them again only costs time.
var bmgrCompressed = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".heic": true,
	".mp4": true, ".webm": true, ".mov": true, ".mp3": true, ".m4a": true, ".ogg": true, ".zip": true, ".gz": true, ".br": true,
	".7z": true, ".rar": true, ".pdf": true, ".woff": true, ".woff2": true, ".wasm": false}

func bmgrToken() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------- job bookkeeping

func (s *Server) bmgrStart(kind string) (*bmgrJob, error) {
	s.bmgr.mu.Lock()
	defer s.bmgr.mu.Unlock()
	if s.bmgr.busy {
		return nil, errors.New("another backup or restore is still running")
	}
	if s.bmgr.jobs == nil {
		s.bmgr.jobs = map[string]*bmgrJob{}
	}
	j := &bmgrJob{ID: bmgrToken() + bmgrToken(), Kind: kind, State: "running", Phase: "Starting", Started: time.Now()}
	s.bmgr.jobs[j.ID] = j
	s.bmgr.busy = true
	return j, nil
}

func (s *Server) bmgrUpdate(j *bmgrJob, f func(*bmgrJob)) {
	s.bmgr.mu.Lock()
	defer s.bmgr.mu.Unlock()
	f(j)
}

func (s *Server) bmgrLog(j *bmgrJob, format string, a ...any) {
	s.bmgrUpdate(j, func(j *bmgrJob) {
		j.Log = append(j.Log, time.Now().Format("15:04:05")+"  "+fmt.Sprintf(format, a...))
		if len(j.Log) > 300 {
			j.Log = j.Log[len(j.Log)-300:]
		}
	})
}

func (s *Server) bmgrProgress(j *bmgrJob, phase string, pct int) {
	s.bmgrUpdate(j, func(j *bmgrJob) { j.Phase, j.Percent = phase, min(max(pct, 0), 100) })
}

func (s *Server) bmgrFinish(j *bmgrJob, err error) {
	s.bmgr.mu.Lock()
	defer s.bmgr.mu.Unlock()
	j.Finished = time.Now()
	if err != nil {
		j.State, j.Error, j.Phase = "error", err.Error(), "Failed"
	} else {
		j.State, j.Phase, j.Percent = "done", "Done", 100
	}
	s.bmgr.busy = false
}

func (s *Server) bmgrJobByID(id string) *bmgrJob {
	s.bmgr.mu.Lock()
	defer s.bmgr.mu.Unlock()
	j := s.bmgr.jobs[id]
	if j == nil {
		return nil
	}
	cp := *j
	cp.Log = append([]string(nil), j.Log...)
	return &cp
}

// ---------------------------------------------------------------- backup

// bmgrResolve turns a selection into the table set and the files groups it carries.
func (sc *bmgrSchema) resolve(sel bmgrSelection) (set map[string]bool, groups []string) {
	chosen := map[string]bool{}
	for _, k := range sel.Categories {
		chosen[k] = true
	}
	base := map[string]bool{}
	for _, t := range sc.Tables {
		if chosen[bmgrCategoryOf(t)] {
			base[t] = true
		}
	}
	set = sc.closure(base)
	for _, c := range bmgrCategories {
		if chosen[c.Key] && c.Files != "" {
			groups = append(groups, c.Files)
		}
	}
	return set, groups
}

// bmgrRunBackup writes a backup archive and returns its file name.
func (s *Server) bmgrRunBackup(ctx context.Context, j *bmgrJob, sel bmgrSelection) (string, error) {
	dir := s.bmgrDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	sc, err := bmgrLoadSchema(ctx, s.store.Pool)
	if err != nil {
		return "", err
	}
	set, groups := sc.resolve(sel)
	if len(set) == 0 && len(groups) == 0 {
		return "", errors.New("nothing is selected")
	}
	kind := sel.Kind
	if kind == "" {
		kind = "manual"
	}
	prefix := "backup"
	switch kind {
	case "safety":
		prefix = "pre-restore"
	case bmgrScheduleKind:
		prefix = bmgrScheduleKind
	}
	name := fmt.Sprintf("%s-%s-%s.zip", prefix, time.Now().Format("20060102-150405"), bmgrToken())
	tmp := filepath.Join(dir, name+".part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmp)
		}
	}()
	zw := zip.NewWriter(f)

	var ids []int64
	if set["users"] {
		ids, err = s.bmgrUserIDs(ctx, sel)
		if err != nil {
			return "", err
		}
	}
	filters := map[string]string{}
	if sel.UserMode == "roles" || sel.UserMode == "ids" {
		filters = sc.userFilters(set)
	}

	m := bmgrManifest{Format: bmgrFormat, Created: time.Now().UTC(), Kind: kind, Version: Version, SiteURL: bmgrSiteURL(s), SiteName: s.settings.String("blogname"),
		Note: sel.Note, Categories: sel.Categories, UserMode: sel.UserMode}
	if len(filters) > 0 {
		m.Users = len(ids)
	}
	_ = s.store.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), '') FROM schema_migrations`).Scan(&m.Schema)

	// The whole export sees one snapshot of the database.
	tx, err := s.store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var tables []string
	for t := range set {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for i, t := range tables {
		s.bmgrProgress(j, "Database: "+t, i*40/max(len(tables), 1))
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "database/" + t + ".jsonl", Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return "", err
		}
		n, err := bmgrExportTable(ctx, tx, t, filters[t], ids, w)
		if err != nil {
			return "", fmt.Errorf("exporting %s: %w", t, err)
		}
		cols := make([]string, 0, len(sc.Columns[t]))
		for _, c := range sc.Columns[t] {
			cols = append(cols, c.Name)
		}
		m.Tables = append(m.Tables, bmgrTableInfo{Name: t, Category: bmgrCategoryOf(t), Rows: n, Columns: cols})
		s.bmgrLog(j, "%s: %d rows", t, n)
	}
	_ = tx.Rollback(ctx)

	roots := s.bmgrFileRoots()
	for gi, g := range groups {
		root := roots[g]
		info := bmgrFilesInfo{Group: g}
		var total int64
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if fi, e := d.Info(); e == nil {
					total += fi.Size()
				}
			}
			return nil
		})
		var done int64
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if !d.Type().IsRegular() || strings.HasSuffix(p, ".part") {
				return nil // directories, symlinks and half-written files are not backed up
			}
			rel, _ := filepath.Rel(root, p)
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			method := zip.Deflate
			if bmgrCompressed[strings.ToLower(filepath.Ext(p))] {
				method = zip.Store
			}
			hdr, _ := zip.FileInfoHeader(fi)
			hdr.Name, hdr.Method = "files/"+g+"/"+filepath.ToSlash(rel), method
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			src, err := os.Open(p)
			if err != nil {
				return nil
			}
			_, err = io.Copy(w, src)
			src.Close()
			if err != nil {
				return err
			}
			info.Count++
			info.Bytes += fi.Size()
			done += fi.Size()
			if info.Count%25 == 0 {
				s.bmgrProgress(j, "Files: "+g, 40+int((int64(gi)*100+done*100/max(total, 1))*55/int64(max(len(groups), 1)*100)))
			}
			return ctx.Err()
		})
		if err != nil {
			return "", fmt.Errorf("adding %s files: %w", g, err)
		}
		m.Files = append(m.Files, info)
		s.bmgrLog(j, "%s files: %d (%s)", g, info.Count, bmgrHuman(info.Bytes))
	}

	s.bmgrProgress(j, "Finishing", 98)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return "", err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}

// bmgrUserIDs are the users a backup keeps.
func (s *Server) bmgrUserIDs(ctx context.Context, sel bmgrSelection) ([]int64, error) {
	var q string
	var args []any
	switch sel.UserMode {
	case "roles":
		q = `SELECT DISTINCT ur.user_id FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.slug = ANY($1)`
		args = []any{sel.Roles}
	case "ids":
		q = `SELECT id FROM users WHERE id = ANY($1)`
		args = []any{sel.UserIDs}
	default:
		q = `SELECT id FROM users`
	}
	rows, err := s.store.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func bmgrSiteURL(s *Server) string {
	u := strings.TrimRight(s.settings.String("home"), "/")
	if u == "" {
		u = s.cfg.SiteURL
	}
	return u
}

// ---------------------------------------------------------------- listing and reading

// bmgrReadManifest reads manifest.json of an archive file.
func bmgrReadManifest(file string) (*bmgrManifest, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, errors.New("not a valid backup archive")
	}
	defer zr.Close()
	return bmgrManifestOf(&zr.Reader)
}

func bmgrManifestOf(zr *zip.Reader) (*bmgrManifest, error) {
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		var m bmgrManifest
		if err := json.NewDecoder(io.LimitReader(rc, 32<<20)).Decode(&m); err != nil {
			return nil, errors.New("the manifest is damaged")
		}
		if m.Format < 1 || m.Format > bmgrFormat {
			return nil, fmt.Errorf("the archive format %d is not supported by this version", m.Format)
		}
		return &m, nil
	}
	return nil, errors.New("manifest.json is missing: this is not a Pursae backup")
}

// bmgrList is every archive in the backups directory, newest first.
func (s *Server) bmgrList() []bmgrEntry {
	ents, _ := os.ReadDir(s.bmgrDir())
	var out []bmgrEntry
	for _, e := range ents {
		if e.IsDir() || !bmgrNameRe.MatchString(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		be := bmgrEntry{Name: e.Name(), Size: fi.Size(), Modified: fi.ModTime()}
		if m, err := bmgrReadManifest(filepath.Join(s.bmgrDir(), e.Name())); err != nil {
			be.Err = err.Error()
		} else {
			be.M = m
		}
		out = append(out, be)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// bmgrPath resolves a stored archive name; "" when the name is not one of ours.
func (s *Server) bmgrPath(name string) string {
	if !bmgrNameRe.MatchString(name) || name != path.Base(name) {
		return ""
	}
	p := filepath.Join(s.bmgrDir(), name)
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return p
}

// ---------------------------------------------------------------- restore

// bmgrRestoreReq is what a restore is asked to do.
type bmgrRestoreReq struct {
	File       string
	Categories []string
	RewriteURL bool
	Safety     bool
	Me         int64
}

// bmgrPlan is the table set and files groups a restore will touch, and what it warns about.
type bmgrPlan struct {
	Tables map[string]bool
	Groups []string
	Notes  []string
}

// bmgrMakePlan checks the archive against the live schema and works out what a restore replaces.
func (s *Server) bmgrMakePlan(ctx context.Context, m *bmgrManifest, cats []string) (*bmgrPlan, *bmgrSchema, error) {
	sc, err := bmgrLoadSchema(ctx, s.store.Pool)
	if err != nil {
		return nil, nil, err
	}
	var live string
	_ = s.store.Pool.QueryRow(ctx, `SELECT COALESCE(MAX(version), '') FROM schema_migrations`).Scan(&live)
	if m.Schema > live {
		return nil, nil, fmt.Errorf("the backup comes from a newer Pursae (schema %s, this site has %s): update this site first", m.Schema, live)
	}
	chosen := map[string]bool{}
	for _, k := range cats {
		if bmgrCategoryByKey(k) != nil {
			chosen[k] = true
		}
	}
	inArchive := map[string]bool{}
	for _, t := range m.Tables {
		inArchive[t.Name] = true
	}
	plan := &bmgrPlan{Tables: map[string]bool{}}
	for _, t := range m.Tables {
		if chosen[t.Category] && sc.has(t.Name) {
			plan.Tables[t.Name] = true
		} else if chosen[t.Category] {
			plan.Notes = append(plan.Notes, "table "+t.Name+" does not exist on this site and is skipped")
		}
	}
	closed := sc.closure(plan.Tables)
	for t := range closed {
		if plan.Tables[t] {
			continue
		}
		if inArchive[t] {
			plan.Tables[t] = true
			plan.Notes = append(plan.Notes, t+" is restored too: it depends on a table you selected")
		} else if t == "menu_locations" && bmgrLegacyMenuLocations(m) {
			plan.Tables[t] = true // filled from the old menus.location column (bmgrRestoreLegacyMenuLocations)
			plan.Notes = append(plan.Notes, "menu locations are taken from the menus of this older backup")
		} else {
			plan.Tables[t] = true // emptied by the cascade anyway; nothing to put back
			plan.Notes = append(plan.Notes, t+" is not in the backup and is emptied: it depends on a table you selected")
		}
	}
	have := map[string]bool{}
	for _, f := range m.Files {
		have[f.Group] = true
	}
	for _, k := range cats {
		if c := bmgrCategoryByKey(k); c != nil && c.Files != "" && have[c.Files] {
			plan.Groups = append(plan.Groups, c.Files)
		}
	}
	return plan, sc, nil
}

// bmgrLegacyMenuLocations reports whether the archive predates menu_locations and keeps each menu's
// location in a menus.location column (0.1.7 and older).
func bmgrLegacyMenuLocations(m *bmgrManifest) bool {
	legacy := false
	for _, t := range m.Tables {
		switch t.Name {
		case "menu_locations":
			return false
		case "menus":
			legacy = contains(t.Columns, "location")
		}
	}
	return legacy
}

// bmgrRunRestore restores the chosen parts of an archive.
func (s *Server) bmgrRunRestore(ctx context.Context, j *bmgrJob, req bmgrRestoreReq) error {
	file := s.bmgrPath(req.File)
	if file == "" {
		return errors.New("the backup file was not found")
	}
	zr, err := zip.OpenReader(file)
	if err != nil {
		return errors.New("not a valid backup archive")
	}
	defer zr.Close()
	m, err := bmgrManifestOf(&zr.Reader)
	if err != nil {
		return err
	}
	plan, sc, err := s.bmgrMakePlan(ctx, m, req.Categories)
	if err != nil {
		return err
	}
	if len(plan.Tables) == 0 && len(plan.Groups) == 0 {
		return errors.New("nothing to restore: select at least one part")
	}
	for _, n := range plan.Notes {
		s.bmgrLog(j, "note: %s", n)
	}

	if req.Safety {
		s.bmgrProgress(j, "Safety snapshot of this site", 1)
		var cats []string
		for _, c := range bmgrCategories {
			cats = append(cats, c.Key)
		}
		name, err := s.bmgrRunBackup(ctx, j, bmgrSelection{Categories: cats, UserMode: "all", Kind: "safety", Note: "Taken automatically before restoring " + req.File})
		if err != nil {
			return fmt.Errorf("the safety snapshot failed, nothing was restored: %w", err)
		}
		s.bmgrLog(j, "safety snapshot saved as %s", name)
	}

	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	cols := map[string][]string{}
	for _, t := range m.Tables {
		cols[t.Name] = t.Columns
	}
	oldURL, newURL := "", ""
	if req.RewriteURL {
		oldURL, newURL = strings.TrimRight(m.SiteURL, "/"), strings.TrimRight(bmgrSiteURL(s), "/")
		if oldURL != "" && oldURL != newURL {
			s.bmgrLog(j, "rewriting %s to %s", oldURL, newURL)
		}
	}

	// Database: one transaction, all or nothing.
	if len(plan.Tables) > 0 {
		conn, err := s.store.Pool.Acquire(ctx)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			conn.Release()
			return err
		}
		var open []io.Closer
		err = bmgrRestoreDB(ctx, tx, sc, bmgrRestoreOpts{
			Tables: plan.Tables, Columns: cols, OldURL: oldURL, NewURL: newURL, Me: req.Me,
			Open: func(t string) (io.Reader, error) {
				f := byName["database/"+t+".jsonl"]
				if f == nil {
					return nil, nil
				}
				rc, err := f.Open()
				if err == nil {
					open = append(open, rc)
				}
				return rc, err
			},
			Progress: func(i, n int, t string) { s.bmgrProgress(j, "Database: "+t, 5+i*55/max(n, 1)) },
		})
		for _, c := range open {
			c.Close()
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			conn.Release()
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			conn.Release()
			return err
		}
		conn.Release()
		s.bmgrLog(j, "database restored: %d tables", len(plan.Tables))
		if err := s.settings.Load(ctx); err != nil {
			slog.Warn("backup: reloading settings", "err", err)
		}
		s.purgeAll(ctx)
	}

	// Files.
	roots := s.bmgrFileRoots()
	for gi, g := range plan.Groups {
		root := roots[g]
		if root == "" {
			continue
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		prefix := "files/" + g + "/"
		var n int
		for _, f := range zr.File {
			rel, ok := strings.CutPrefix(f.Name, prefix)
			if !ok || f.FileInfo().IsDir() {
				continue
			}
			dst, err := bmgrSafeJoin(root, rel)
			if err != nil {
				return fmt.Errorf("unsafe path in the archive: %s", f.Name)
			}
			if err := bmgrExtract(f, dst); err != nil {
				return fmt.Errorf("restoring %s: %w", f.Name, err)
			}
			n++
			if n%25 == 0 {
				s.bmgrProgress(j, "Files: "+g, 60+gi*35/max(len(plan.Groups), 1))
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		s.bmgrLog(j, "%s files restored: %d", g, n)
	}
	if plan.Tables["plugins"] || contains(plan.Groups, "plugins") {
		s.bmgrLog(j, "restart Pursae to load the restored plugins")
	}
	return nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// bmgrSafeJoin joins an archive path under root, refusing anything that would leave it.
func bmgrSafeJoin(root, rel string) (string, error) {
	if rel == "" || !fs.ValidPath(rel) || strings.ContainsRune(rel, '\\') {
		return "", errors.New("invalid path")
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// bmgrExtract writes one archive file atomically (temp file, then rename).
func bmgrExtract(f *zip.File, dst string) error {
	if f.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	_ = os.Chtimes(tmp, f.Modified, f.Modified)
	return os.Rename(tmp, dst)
}

func bmgrHuman(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
