package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"wpgo/internal/store"
	"wpgo/internal/webbuilder"
)

// Script trust of design kits: the editor runs a design's scripts in its own origin with the
// author's access (Live edit, Preview; docs/developer/web-builder-code-sections.md, Security
// model), so it asks first. A user who answers "always" for a kit is not asked again for it:
// PUT web-builder/v2/kits/{slug}/trust {trust: bool} stores the answer in the user's own meta,
// and Boot.scriptTrust hands it to the editor.
//
// The stored value is {slug: install id} (wbKit.Install): a commit of the kit (the user's own
// edits, a new version) keeps the trust; a kit deleted and uploaded again under the same slug is
// another installation and is asked about again. An entry whose kit is gone or re-installed is not
// trust: it is never reported, and it is pruned when the user's list is read or written. Deleting a
// kit removes its entries from every user (wbKitTrustDrop); deleting a user takes the meta along.
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

// wbNewKitInstall is a new wbKit.Install value.
func wbNewKitInstall() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b)
}

// wbKitTrustDecode reads a stored trust value: string entries with a valid slug only.
func wbKitTrustDecode(raw []byte) map[string]string {
	out := map[string]string{}
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return out
	}
	for slug, v := range m {
		if id, ok := v.(string); ok && id != "" && wbKitSlugRE.MatchString(slug) {
			out[slug] = id
		}
	}
	return out
}

// wbKitTrusted is the sorted slugs of m that name an existing kit as the installation trusted.
func wbKitTrusted(kits map[string]*wbKit, m map[string]string) []string {
	out := []string{}
	for slug, id := range m {
		if k := kits[slug]; k != nil && k.Install != "" && k.Install == id {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// wbKitTrustPrune removes the entries of m that are not trust (wbKitTrusted); it reports whether
// it removed any.
func wbKitTrustPrune(kits map[string]*wbKit, m map[string]string) bool {
	keep := map[string]bool{}
	for _, slug := range wbKitTrusted(kits, m) {
		keep[slug] = true
	}
	changed := false
	for slug := range m {
		if !keep[slug] {
			delete(m, slug)
			changed = true
		}
	}
	return changed
}

// wbKitTrustRead is user uid's stored trust value.
func (s *Server) wbKitTrustRead(ctx context.Context, uid int64) (map[string]string, error) {
	var raw []byte
	err := s.store.Pool.QueryRow(ctx, `SELECT meta->($2::text) FROM users WHERE id = $1`, uid, wbKitTrustMeta).Scan(&raw)
	if err != nil {
		return nil, err
	}
	return wbKitTrustDecode(raw), nil
}

// wbKitTrustUpdate changes user uid's trust value with fn in one transaction (the row is locked, so
// two writes do not lose each other) and returns the new value. An empty value removes the key.
func (s *Server) wbKitTrustUpdate(ctx context.Context, uid int64, fn func(m map[string]string)) (map[string]string, error) {
	var out map[string]string
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

// wbKitTrustBoot is Boot.scriptTrust for u: the kits u trusts (none without unfiltered_html).
// Stale entries are pruned on the way.
func (s *Server) wbKitTrustBoot(ctx context.Context, u *store.User) []string {
	if u == nil || !u.Can("unfiltered_html", nil) {
		return []string{}
	}
	m, err := s.wbKitTrustRead(ctx, u.ID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("web builder: script trust of user %d: %v", u.ID, err) //nolint:gosec // G706: a numeric user id and a database error, no request text
		}
		return []string{}
	}
	kits := s.wbKitList()
	trusted := wbKitTrusted(kits, m)
	if len(trusted) < len(m) {
		if _, err := s.wbKitTrustUpdate(ctx, u.ID, func(cur map[string]string) { wbKitTrustPrune(kits, cur) }); err != nil {
			log.Printf("web builder: pruning the script trust of user %d: %v", u.ID, err) //nolint:gosec // G706: a numeric user id and a database error, no request text
		}
	}
	return trusted
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

// wbRESTKitTrust is PUT kits/{slug}/trust {trust: bool}: the calling user always runs (true) or is
// asked again about (false) the kit's scripts. It answers {trust, scriptTrust} (Boot.scriptTrust).
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
	raw, ok := wbBody(c, r, 1<<10)
	if !ok {
		return
	}
	var in struct {
		Trust *bool `json:"trust"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Trust == nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "trust must be true or false.", map[string]any{"key": "trust"})
		return
	}
	trust := *in.Trust
	// Kit writes (a delete, a commit creating the kit again) wait, so the install id read here is
	// the kit's until the trust is stored.
	wbKitsMu.Lock()
	defer wbKitsMu.Unlock()
	kits := s.wbKitList()
	k := kits[slug]
	if k == nil {
		restErr(c, http.StatusNotFound, "wb_kit_not_found", "No design kit has that slug.")
		return
	}
	if trust && k.Install == "" {
		// A kit stored before installs were identified: it gets its id now.
		k.Install = wbNewKitInstall()
		if err := s.settings.Set(c, map[string]any{wbKitsOption: kits}); err != nil {
			restErr(c, http.StatusInternalServerError, "db_error", "Could not save the trust.")
			return
		}
	}
	tooMany := false
	m, err := s.wbKitTrustUpdate(c, u.ID, func(m map[string]string) {
		wbKitTrustPrune(kits, m)
		if !trust {
			delete(m, slug)
			return
		}
		if _, ok := m[slug]; !ok && len(m) >= wbKitTrustMax {
			tooMany = true
			return
		}
		m[slug] = k.Install
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
	c.JSON(http.StatusOK, map[string]any{"trust": trust, "scriptTrust": wbKitTrusted(kits, m)})
}
