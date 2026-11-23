package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Script trust of design kits: the editor runs a design's scripts in its own origin with the
// author's access (Live edit, Preview; docs/developer/web-builder-code-sections.md, Security
// model), so it asks first. A user who answers "always" for a kit is not asked again for it:
// PUT web-builder/v2/kits/{slug}/trust {trust: bool, code?: [sha256 hex]} stores the answer in the
// user's own meta, and Boot.scriptTrust / Boot.scriptCode hand it to the editor.
//
// The stored value is {slug: {i: install id, h: code hash, c: [inline code hashes]}}: trust names
// one installation of the kit (wbKit.Install: a kit deleted and uploaded again under the same slug
// is another one) AND the exact executable content it had (wbKit.Code, wbKitCodeHash: every .js,
// .mjs, .wasm and .svg file, path and bytes). A commit that changes any of those files is asked
// about again by every user, the committer included (uploading a design is never consent to run it
// in the editor); a commit that changes only stylesheets, images or fonts keeps the trust. The
// inline code the user approved together with the kit (c: the hashes of the page's inline scripts,
// handlers, javascript: URLs and non-kit script addresses, computed by the editor, trust.ts
// needsOf) is kept with the entry, so a page whose inline code the user accepted with "Always"
// opens Live again, and any other inline code is asked about. An entry whose kit is gone,
// re-installed or changed is not trust: it is never reported, and it is pruned when the user's
// list is read or written; an entry of the old format (a bare install id, before code hashes) is
// not trust either. Deleting a kit removes its entries from every user (wbKitTrustDrop); deleting a
// user takes the meta along.
//
// Only a user with unfiltered_html (who may write the same scripts into a code section) can trust
// a kit, and only for themselves: the route has no user parameter. With an Application Password
// the themes scope is needed too, like every other site-code write (wbUnfiltered). The route needs
// the REST nonce (cookie authentication) and the X-WB-Api header like its siblings, so a
// cross-site request cannot set it.
//
// The list is account configuration, like the editor preferences (_web_builder_prefs), not personal
// data: no privacy exporter or eraser reads it.

// wbKitTrustMeta is the user meta key (protected: "_" keeps it out of the core users REST meta).
const wbKitTrustMeta = "_web_builder_kit_trust"

// wbKitTrustRoute is PUT kits/{slug}/trust (the kit slug pattern of wbRegisterKitREST).
const wbKitTrustRoute = `/web-builder/v2/kits/(?P<slug>[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)/trust`

// wbKitTrustMax bounds the stored list (a site has at most a few dozen kits).
const wbKitTrustMax = 256

// wbKitTrustCodeMax bounds the inline code hashes kept per kit (the newest are kept).
const wbKitTrustCodeMax = 512

// wbTrustHashRE is one inline code hash (lowercase hex SHA-256).
var wbTrustHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// wbKitTrustEntry is one kit's trust: the installation and executable content trusted, and the
// inline code approved with it.
type wbKitTrustEntry struct {
	Install string   `json:"i"`
	Code    string   `json:"h"`
	Inline  []string `json:"c,omitempty"`
}

// wbNewKitInstall is a new wbKit.Install value.
func wbNewKitInstall() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b)
}

// wbKitExecExts are the kit files whose content can run in a page: scripts, modules, WebAssembly
// and SVG (a document with scripts when opened as one).
var wbKitExecExts = map[string]bool{".js": true, ".mjs": true, ".wasm": true, ".svg": true}

// wbKitCodeHash is the hash of a kit's executable content (wbKitExecExts): every such file under
// dir, in path order, length-prefixed path and bytes. files are dir's files (wbKitFiles). A file
// that cannot be read counts by its name and a marker, so the hash still changes with the set.
func wbKitCodeHash(dir string, files map[string]int64) string {
	names := make([]string, 0, len(files))
	for rel := range files {
		if wbKitExecExts[strings.ToLower(path.Ext(rel))] {
			names = append(names, rel)
		}
	}
	sort.Strings(names)
	h := sha256.New()
	var n [8]byte
	put := func(b []byte) {
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	for _, rel := range names {
		put([]byte(rel))
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // G304: a kit file listed by wbKitFiles under the kit's own folder
		if err != nil {
			put([]byte("\x00unreadable"))
			continue
		}
		put(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// wbKitFillCode gives every kit of kits without a code hash (stored before hashes existed) its
// hash from its files; it reports whether it set any. The caller holds wbKitsMu and saves.
func (s *Server) wbKitFillCode(kits map[string]*wbKit) bool {
	changed := false
	for slug, k := range kits {
		if k.Code != "" {
			continue
		}
		dir := filepath.Join(s.wbKitRoot(), slug)
		files, err := wbKitFiles(dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		k.Code = wbKitCodeHash(dir, files)
		changed = true
	}
	return changed
}

// wbKitListCode is wbKitList with every kit's code hash (computed and stored for kits without one).
func (s *Server) wbKitListCode(ctx context.Context) map[string]*wbKit {
	kits := s.wbKitList()
	missing := false
	for _, k := range kits {
		if k.Code == "" {
			missing = true
			break
		}
	}
	if !missing {
		return kits
	}
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	kits = s.wbKitList()
	if s.wbKitFillCode(kits) {
		if err := s.settings.Set(ctx, map[string]any{wbKitsOption: kits}); err != nil {
			log.Printf("web builder: storing the kits' code hashes: %v", err)
		}
	}
	return kits
}

// wbKitTrustDecode reads a stored trust value: object entries with a valid slug, an install id and
// a code hash only (a bare install id is the format before code hashes: not trust).
func wbKitTrustDecode(raw []byte) map[string]*wbKitTrustEntry {
	out := map[string]*wbKitTrustEntry{}
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return out
	}
	for slug, v := range m {
		var e struct {
			Install string `json:"i"`
			Code    string `json:"h"`
			Inline  []any  `json:"c"`
		}
		if !wbKitSlugRE.MatchString(slug) || json.Unmarshal(v, &e) != nil || e.Install == "" || e.Code == "" {
			continue
		}
		var inline []string
		for _, h := range e.Inline {
			if h, ok := h.(string); ok {
				inline = append(inline, h)
			}
		}
		out[slug] = &wbKitTrustEntry{Install: e.Install, Code: e.Code, Inline: wbTrustHashes(inline, nil)}
	}
	return out
}

// wbTrustHashes is old followed by add, valid hashes only, once each, the newest
// wbKitTrustCodeMax kept.
func wbTrustHashes(old, add []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range [][]string{add, old} { // newest first
		for _, h := range l {
			if wbTrustHashRE.MatchString(h) && !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	if len(out) > wbKitTrustCodeMax {
		out = out[:wbKitTrustCodeMax]
	}
	sort.Strings(out)
	return out
}

// wbKitTrustValid reports whether e is trust of kit k: the same installation with the same
// executable content.
func wbKitTrustValid(k *wbKit, e *wbKitTrustEntry) bool {
	return k != nil && e != nil && k.Install != "" && k.Code != "" && e.Install == k.Install && e.Code == k.Code
}

// wbKitTrusted is the sorted slugs of m that are trust of an existing kit (wbKitTrustValid).
func wbKitTrusted(kits map[string]*wbKit, m map[string]*wbKitTrustEntry) []string {
	out := []string{}
	for slug, e := range m {
		if wbKitTrustValid(kits[slug], e) {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// wbKitTrustCode is Boot.scriptCode: the inline code hashes approved with each trusted kit.
func wbKitTrustCode(kits map[string]*wbKit, m map[string]*wbKitTrustEntry) map[string][]string {
	out := map[string][]string{}
	for slug, e := range m {
		if wbKitTrustValid(kits[slug], e) && len(e.Inline) > 0 {
			out[slug] = e.Inline
		}
	}
	return out
}

// wbKitTrustPrune removes the entries of m that are not trust (wbKitTrusted); it reports whether
// it removed any.
func wbKitTrustPrune(kits map[string]*wbKit, m map[string]*wbKitTrustEntry) bool {
	changed := false
	for slug, e := range m {
		if !wbKitTrustValid(kits[slug], e) {
			delete(m, slug)
			changed = true
		}
	}
	return changed
}

// wbKitTrustRead is user uid's stored trust value.
func (s *Server) wbKitTrustRead(ctx context.Context, uid int64) (map[string]*wbKitTrustEntry, error) {
	var raw []byte
	err := s.store.Pool.QueryRow(ctx, `SELECT meta->($2::text) FROM users WHERE id = $1`, uid, wbKitTrustMeta).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return wbKitTrustDecode(raw), nil
}

// wbKitTrustUpdate changes user uid's trust value with fn in one transaction (the row is locked, so
// two writes do not lose each other) and returns the new value. An empty value removes the key.
func (s *Server) wbKitTrustUpdate(ctx context.Context, uid int64, fn func(m map[string]*wbKitTrustEntry)) (map[string]*wbKitTrustEntry, error) {
	var out map[string]*wbKitTrustEntry
	err := pgx.BeginFunc(ctx, s.store.Pool, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT meta->($2::text) FROM users WHERE id = $1 FOR UPDATE`, uid, wbKitTrustMeta).Scan(&raw); err != nil {
			return err
		}
		m := wbKitTrustDecode(raw)
		fn(m)
		out = m
		if len(m) == 0 {
			_, err := tx.Exec(ctx, `UPDATE users SET meta = coalesce(meta, '{}'::jsonb) - $2::text WHERE id = $1`, uid, wbKitTrustMeta)
			return err
		}
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE users SET meta = coalesce(meta, '{}'::jsonb) || jsonb_build_object($2::text, $3::jsonb) WHERE id = $1`,
			uid, wbKitTrustMeta, string(b))
		return err
	})
	return out, err
}

// wbKitTrustBoot is Boot.scriptTrust and Boot.scriptCode for u: the kits u trusts and the inline
// code approved with them (none without unfiltered_html, or with an Application Password without
// the site-code scope). Stale entries are pruned on the way.
// can is wbUnfiltered for the request.
func (s *Server) wbKitTrustBoot(c context.Context, u *store.User, can bool) (trusted []string, code map[string][]string) {
	trusted, code = []string{}, map[string][]string{}
	if u == nil || !can {
		return trusted, code
	}
	m, err := s.wbKitTrustRead(c, u.ID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("web builder: script trust of user %d: %v", u.ID, err) //nolint:gosec // G706: a numeric user id and a database error, no request text
		}
		return trusted, code
	}
	if len(m) == 0 {
		return trusted, code
	}
	kits := s.wbKitListCode(c)
	trusted, code = wbKitTrusted(kits, m), wbKitTrustCode(kits, m)
	if len(trusted) < len(m) {
		if _, err := s.wbKitTrustUpdate(c, u.ID, func(cur map[string]*wbKitTrustEntry) { wbKitTrustPrune(kits, cur) }); err != nil {
			log.Printf("web builder: pruning the script trust of user %d: %v", u.ID, err) //nolint:gosec // G706: a numeric user id and a database error, no request text
		}
	}
	return trusted, code
}

// wbKitTrustDrop removes slug from every user's trust (the kit was deleted).
func (s *Server) wbKitTrustDrop(ctx context.Context, slug string) {
	_, err := s.store.Pool.Exec(ctx, `UPDATE users SET meta = CASE WHEN (meta->($1::text)) - $2::text = '{}'::jsonb
			THEN meta - $1::text ELSE jsonb_set(meta, ARRAY[$1::text], (meta->($1::text)) - $2::text) END
		WHERE jsonb_typeof(meta->($1::text)) = 'object' AND (meta->($1::text)) ? $2::text`, wbKitTrustMeta, slug)
	if err != nil {
		log.Printf("web builder: dropping the script trust of kit %s: %v", slug, err)
	}
}

// wbRESTKitTrust is PUT kits/{slug}/trust {trust: bool, code?: [sha256 hex]}: the calling user
// always runs (true) or is asked again about (false) the kit's scripts; with trust, code adds the
// inline code hashes approved with the kit (at most wbKitTrustCodeMax are kept). It answers
// {trust, scriptTrust, scriptCode} (Boot.scriptTrust, Boot.scriptCode).
func (s *Server) wbRESTKitTrust(c *gin.Context, r *restRequest) {
	u := wbUser(c, "unfiltered_html")
	if u == nil {
		return
	}
	if !wbUnfiltered(c, u) {
		wbForbidden(c, u)
		return
	}
	slug := r.Params["slug"]
	if !wbKitSlugRE.MatchString(slug) {
		restErr(c, http.StatusNotFound, "wb_kit_not_found", "No design kit has that slug.")
		return
	}
	raw, ok := wbBody(c, r, 64<<10)
	if !ok {
		return
	}
	var in struct {
		Trust *bool    `json:"trust"`
		Code  []string `json:"code"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Trust == nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "trust must be true or false.", map[string]any{"key": "trust"})
		return
	}
	if len(in.Code) > wbKitTrustCodeMax {
		restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, "Too many code hashes.", map[string]any{"limit": wbKitTrustCodeMax})
		return
	}
	for _, h := range in.Code {
		if !wbTrustHashRE.MatchString(h) {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "code must be a list of SHA-256 hashes (lowercase hex).", map[string]any{"key": "code"})
			return
		}
	}
	trust := *in.Trust
	// Kit writes (a delete, a commit) wait, so the install id and code hash read here are the
	// kit's until the trust is stored.
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	kits := s.wbKitList()
	k := kits[slug]
	if k == nil {
		restErr(c, http.StatusNotFound, "wb_kit_not_found", "No design kit has that slug.")
		return
	}
	if trust && (k.Install == "" || k.Code == "") {
		// A kit stored before installs (or code hashes) were identified: it gets them now.
		if k.Install == "" {
			k.Install = wbNewKitInstall()
		}
		s.wbKitFillCode(kits)
		if err := s.settings.Set(c, map[string]any{wbKitsOption: kits}); err != nil {
			restErr(c, http.StatusInternalServerError, "db_error", "Could not save the trust.")
			return
		}
	}
	tooMany := false
	m, err := s.wbKitTrustUpdate(c, u.ID, func(m map[string]*wbKitTrustEntry) {
		wbKitTrustPrune(kits, m)
		if !trust {
			delete(m, slug)
			return
		}
		old := m[slug]
		if old == nil && len(m) >= wbKitTrustMax {
			tooMany = true
			return
		}
		var inline []string
		if old != nil {
			inline = old.Inline
		}
		m[slug] = &wbKitTrustEntry{Install: k.Install, Code: k.Code, Inline: wbTrustHashes(inline, in.Code)}
	})
	if err != nil {
		restErr(c, http.StatusInternalServerError, "db_error", "Could not save the trust.")
		return
	}
	if tooMany {
		restErr(c, http.StatusRequestEntityTooLarge, webbuilder.ErrTooLarge, "Too many trusted design kits.", map[string]any{"limit": wbKitTrustMax})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, map[string]any{"trust": trust, "scriptTrust": wbKitTrusted(kits, m), "scriptCode": wbKitTrustCode(kits, m)})
}
