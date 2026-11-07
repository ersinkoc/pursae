package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"wpgo/internal/settings"
)

// --- Pursae Cache ----------------------------------------------------------------------------

func (s *Server) pursaeCacheStatus(c *gin.Context, _ *restRequest) {
	out := map[string]any{"enabled": s.settings.Bool("wpgo_cache_enabled"), "server": s.settings.String("wpgo_cache_server")}
	if cc := s.currentCache(); cc != nil {
		st := cc.Stats()
		out["stats"] = map[string]any{"entries": st.Entries, "bytes": st.Bytes, "hits": st.Hits, "misses": st.Misses, "hit_ratio": st.HitRatio}
	}
	s.warm.mu.Lock()
	out["warmup"] = map[string]any{"running": s.warm.running, "progress": s.warm.last}
	s.warm.mu.Unlock()
	restJSON(c, http.StatusOK, out)
}

// pursaeCachePurge purges everything ("all": true), one URL ("url"), the pages of a post ("post")
// or cache tags ("tags": ["home", "post:12", …]).
func (s *Server) pursaeCachePurge(c *gin.Context, r *restRequest) {
	cc := s.currentCache()
	if cc == nil {
		restJSON(c, http.StatusOK, map[string]any{"purged": 0, "message": "The page cache is not running."})
		return
	}
	ctx := c.Request.Context()
	if all, _ := r.Bool(c, "all"); all {
		n := cc.PurgeAll()
		restJSON(c, http.StatusOK, map[string]any{"purged": n, "message": fmt.Sprintf("Purged all caches (%d pages).", n)})
		return
	}
	if u, _ := r.Str(c, "url"); strings.TrimSpace(u) != "" {
		u = strings.TrimSpace(u)
		pu, err := url.Parse(u)
		if err != nil {
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "Invalid parameter(s): url")
			return
		}
		if pu.Host == "" {
			u = s.links(c).Home + "/" + strings.TrimLeft(pu.Path, "/")
			if pu.RawQuery != "" {
				u += "?" + pu.RawQuery
			}
		}
		n := cc.PurgeURL(u)
		restJSON(c, http.StatusOK, map[string]any{"purged": n, "url": u})
		return
	}
	if id, ok := r.Int(c, "post"); ok && id > 0 {
		p, err := s.store.GetPost(ctx, id)
		if err != nil {
			restErr(c, http.StatusNotFound, "rest_post_invalid_id", "Invalid post ID.")
			return
		}
		s.purgePost(ctx, p)
		restJSON(c, http.StatusOK, map[string]any{"purged": true, "post": id})
		return
	}
	if list, ok := r.Body["tags"].([]any); ok && len(list) > 0 {
		var tags []string
		for _, t := range list {
			if ts, ok := t.(string); ok && ts != "" {
				tags = append(tags, ts)
			}
		}
		cc.PurgeTags(tags...)
		restJSON(c, http.StatusOK, map[string]any{"purged": true, "tags": tags})
		return
	}
	restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Give one of: all, url, post, tags.")
}

// pursaeCacheSettings reads or updates the Pursae Cache options (the keys of its settings screens).
func (s *Server) pursaeCacheSettings(c *gin.Context, r *restRequest) {
	var keys []string
	for _, ks := range cacheOptionKeys {
		keys = append(keys, ks...)
	}
	sort.Strings(keys)
	if r.Method != http.MethodGet {
		values := map[string]any{}
		for _, k := range keys {
			v, ok := r.Body[k]
			if !ok {
				continue
			}
			switch settings.Defaults[k].(type) {
			case bool:
				b, ok := v.(bool)
				if !ok {
					restErr(c, http.StatusBadRequest, "rest_invalid_param", k+" must be a boolean.")
					return
				}
				values[k] = b
			case int:
				f, ok := v.(float64)
				if !ok || f < 0 {
					restErr(c, http.StatusBadRequest, "rest_invalid_param", k+" must be a non-negative integer.")
					return
				}
				values[k] = int(f)
			default:
				sv, ok := v.(string)
				if !ok {
					restErr(c, http.StatusBadRequest, "rest_invalid_param", k+" must be a string.")
					return
				}
				values[k] = strings.TrimSpace(sv)
			}
		}
		if len(values) > 0 {
			if err := s.settings.Set(c, values); err != nil {
				restErr(c, http.StatusInternalServerError, "rest_cannot_update", err.Error())
				return
			}
			s.configureCache()
		}
	}
	out := map[string]any{}
	for _, k := range keys {
		out[k] = s.settings.Get(k)
	}
	restJSON(c, http.StatusOK, out)
}

// --- option groups of the built-in plugins ---------------------------------------------------

// optionGroup is one built-in plugin's settings, stored as one JSON object. load returns the current
// value (a struct); save stores a value of the same type.
type optionGroup struct {
	Plugin, Label string
	load          func(s *Server) any
	save          func(s *Server, ctx context.Context, v []byte) error
}

func groupOf[T any](plugin, label string, load func(s *Server) T, save func(s *Server, ctx context.Context, v T) error) optionGroup {
	return optionGroup{Plugin: plugin, Label: label,
		load: func(s *Server) any { return load(s) },
		save: func(s *Server, ctx context.Context, b []byte) error {
			// Deep-merge the patch into the current value, so fields and nested map entries the update
			// leaves out (a masked secret, say) keep their values.
			var patch map[string]any
			if err := json.Unmarshal(b, &patch); err != nil {
				return err
			}
			cur, _ := json.Marshal(load(s))
			var merged map[string]any
			if err := json.Unmarshal(cur, &merged); err != nil || merged == nil {
				merged = map[string]any{}
			}
			deepMerge(merged, patch)
			mb, _ := json.Marshal(merged)
			v := load(s)
			if err := json.Unmarshal(mb, &v); err != nil {
				return err
			}
			return save(s, ctx, v)
		}}
}

// deepMerge copies src into dst, merging nested objects key by key.
func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				deepMerge(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
}

// optionGroups is the whitelist of /pursae/v1/options/{group}.
var optionGroups = map[string]optionGroup{
	"seo": groupOf(seogoSlug, "Pursae SEO", func(s *Server) *seogoOptions { return s.seogoOpts().clone() },
		func(s *Server, ctx context.Context, v *seogoOptions) error { return s.seogoSave(ctx, v) }),
	"gocdn": groupOf(gocdnSlug, "Pursae CDN", (*Server).gocdnLoad,
		func(s *Server, ctx context.Context, v gocdnSettings) error { return s.gocdnSave(ctx, v) }),
	"security": groupOf(asSlug, "Pursae Security", (*Server).asLoad,
		func(s *Server, ctx context.Context, v asSettings) error { return s.asSave(ctx, v) }),
	"smtp": groupOf(msSlug, "Pursae SMTP", (*Server).msLoadRaw,
		func(s *Server, ctx context.Context, v *msOptions) error { return s.msSave(ctx, v) }),
	"maintenance": groupOf(maintSlug, "Pursae Maintenance", (*Server).maintLoad,
		func(s *Server, ctx context.Context, v maintSettings) error { return s.maintSave(ctx, v) }),
	"headless": groupOf(hgSlug, "Pursae Headless", (*Server).hgLoad,
		func(s *Server, ctx context.Context, v hgSettings) error { return s.hgSaveSettings(ctx, v) }),
}

func (s *Server) pursaeOptionGroups(c *gin.Context, _ *restRequest) {
	names := make([]string, 0, len(optionGroups))
	for k := range optionGroups {
		names = append(names, k)
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, k := range names {
		g := optionGroups[k]
		out = append(out, map[string]any{"group": k, "plugin": g.Plugin, "label": g.Label,
			"_links": map[string]any{"self": []map[string]string{{"href": s.links(c).HomeURL("/wp-json/pursae/v1/options/" + k)}}}})
	}
	restJSON(c, http.StatusOK, out)
}

// pursaeOptionsHandler reads (GET) or merges an update into (POST) one option group. Secrets are
// masked on output; a masked value sent back unchanged keeps the stored secret.
func (s *Server) pursaeOptionsHandler(fixed string) restHandler {
	return func(c *gin.Context, r *restRequest) {
		name := fixed
		if name == "" {
			name = r.Params["group"]
		}
		g, ok := optionGroups[name]
		if !ok {
			restErr(c, http.StatusNotFound, "rest_option_group_not_found", "Unknown option group.", map[string]any{"groups": optionGroupNames()})
			return
		}
		if r.Method != http.MethodGet {
			patch := r.Body
			if v, ok := r.Body["settings"].(map[string]any); ok && len(r.Body) == 1 {
				patch = v
			}
			if patch == nil {
				patch = map[string]any{}
			}
			patch = dropMasked(patch).(map[string]any)
			b, _ := json.Marshal(patch)
			if err := g.save(s, c.Request.Context(), b); err != nil {
				restErr(c, http.StatusBadRequest, "rest_invalid_param", err.Error())
				return
			}
		}
		raw, _ := json.Marshal(g.load(s))
		var out any
		_ = json.Unmarshal(raw, &out)
		restJSON(c, http.StatusOK, map[string]any{"group": name, "plugin": g.Plugin, "settings": maskSecrets(out, "")})
	}
}

func optionGroupNames() []string {
	var out []string
	for k := range optionGroups {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// secretMask replaces stored secrets on output.
const secretMask = "********"

var secretKey = regexp.MustCompile(`(?i)(pass(word)?|secret|token|api[_-]?key|access[_-]?key|private|license|client[_-]?id|signing|webhook|smtp_auth|key$)`)

// maskSecrets replaces non-empty string values whose key looks secret.
func maskSecrets(v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = maskSecrets(x, k)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = maskSecrets(x, key)
		}
		return t
	case string:
		if t != "" && key != "" && secretKey.MatchString(key) {
			return secretMask
		}
	}
	return v
}

// dropMasked removes values equal to the mask, so sending a GET result back keeps the secrets.
func dropMasked(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if xs, ok := x.(string); ok && xs == secretMask {
				delete(t, k)
				continue
			}
			t[k] = dropMasked(x)
		}
		return t
	}
	return v
}

// --- Pursae SEO redirects --------------------------------------------------------------------

func redirectJSON(r *seogoRedirectRow) map[string]any {
	return map[string]any{"id": r.ID, "source": r.Source, "target": r.Target, "code": r.Code, "match_type": r.MatchType,
		"status": r.Status, "category": r.Category, "hits": r.Hits}
}

func (s *Server) pursaeRedirectsList(c *gin.Context, _ *restRequest) {
	rows, err := s.seogoRedirectRows(c.Request.Context(), false)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_query_error", err.Error())
		return
	}
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, redirectJSON(r))
	}
	restJSON(c, http.StatusOK, out)
}

func (s *Server) pursaeRedirectCreate(c *gin.Context, r *restRequest) {
	row := &seogoRedirectRow{Code: 301, MatchType: "exact", Status: "active"}
	row.Source, _ = r.Str(c, "source")
	row.Target, _ = r.Str(c, "target")
	row.Category, _ = r.Str(c, "category")
	row.Source, row.Target, row.Category = strings.TrimSpace(row.Source), strings.TrimSpace(row.Target), strings.TrimSpace(row.Category)
	if id, ok := r.Int(c, "id"); ok {
		row.ID = id
	}
	if code, ok := r.Int(c, "code"); ok {
		row.Code = int(code)
	}
	switch row.Code {
	case 301, 302, 307, 410, 451:
	default:
		restErr(c, http.StatusBadRequest, "rest_invalid_param", "code must be one of 301, 302, 307, 410, 451.")
		return
	}
	if mt, ok := r.Str(c, "match_type"); ok && mt != "" {
		switch mt {
		case "exact", "contains", "start", "end", "regex":
			row.MatchType = mt
		default:
			restErr(c, http.StatusBadRequest, "rest_invalid_param", "match_type must be one of exact, contains, start, end, regex.")
			return
		}
	}
	if st, _ := r.Str(c, "status"); st == "inactive" {
		row.Status = "inactive"
	}
	if row.Source == "" {
		restErr(c, http.StatusBadRequest, "rest_missing_callback_param", "Missing parameter(s): source")
		return
	}
	if err := s.seogoAddRedirect(c.Request.Context(), row); err != nil {
		restErr(c, http.StatusBadRequest, "rest_invalid_param", err.Error())
		return
	}
	restJSON(c, http.StatusCreated, redirectJSON(row))
}

func (s *Server) pursaeRedirectDelete(c *gin.Context, r *restRequest) {
	id, _ := strconv.ParseInt(r.Params["id"], 10, 64)
	tag, err := s.store.Pool.Exec(c.Request.Context(), `DELETE FROM redirects WHERE id = $1`, id)
	if err != nil {
		restErr(c, http.StatusInternalServerError, "rest_cannot_delete", err.Error())
		return
	}
	if tag.RowsAffected() == 0 {
		restErr(c, http.StatusNotFound, "rest_redirect_not_found", "Redirect not found.")
		return
	}
	s.seogoRedirectsChanged()
	restJSON(c, http.StatusOK, map[string]any{"deleted": true, "id": id})
}
