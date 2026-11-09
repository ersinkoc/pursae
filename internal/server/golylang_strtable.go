package server

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
)

// Pursae Multi Language: the strings of Languages > Translations. Besides the built-in ones (site
// title, tagline, Pursae Commerce texts, URL slugs) they are the rows of pll_string_sources:
// strings registered with pll_register_string / pll__, theme gettext calls and text settings,
// plugin "translate" calls and text recorded on pages (golylang_capture.go). The option
// golylang_strings, where earlier versions kept registered strings, is still read and is copied
// into the table once.

// pllStringsMax caps the rows the Translations screen loads.
const pllStringsMax = 50000

// pllKeySQL is pllStringKey in SQL: the pll_strings.original of a pll_string_sources row.
const pllKeySQL = `CASE WHEN context <> '' AND domain <> 'pll' THEN context || chr(4) || original ELSE original END`

var pllOptionMigrated sync.Map // *Server => true once golylang_strings was copied into the table

// pllSlugGroup is the Strings screen group of the URL slug bases (never machine translated).
const pllSlugGroup = "URL slugs"

// pllStrings lists the translatable strings: WordPress options, Pursae Commerce texts, URL slugs
// (Translate slugs) and the recorded strings, each once (by translation key; the more precise
// source wins over page text).
func (s *Server) pllStrings(ctx context.Context) []pllRegistered {
	out := []pllRegistered{
		{Name: "Site Title", String: s.settings.String("blogname"), Group: "WordPress"},
		{Name: "Tagline", String: s.settings.String("blogdescription"), Group: "WordPress"},
	}
	out = append(out, s.gooPllStrings()...) // Goocommerce: store notice, e-mails, payment and shipping titles (goo_golylang.go)
	// Pursae SEO: Search Appearance templates, breadcrumb labels (seogo_vars.go)
	out = append(out, s.seogoPllStrings()...)
	if s.pllOpts().TranslateSlugs {
		for _, b := range s.pllSlugBases() {
			out = append(out, pllRegistered{Name: b[0], String: b[1], Group: pllSlugGroup})
		}
	}
	legacy := s.pllOptionStrings()
	s.pllMigrateOptionStrings(ctx, legacy)
	out = append(out, s.pllTableStrings(ctx)...)
	out = append(out, legacy...)
	seen := make(map[string]bool, len(out))
	n := 0
	for _, r := range out {
		k := r.Key()
		if r.String == "" || seen[k] {
			continue
		}
		seen[k] = true
		out[n] = r
		n++
	}
	return out[:n]
}

// pllOptionStrings are the strings of the option golylang_strings (earlier versions).
func (s *Server) pllOptionStrings() []pllRegistered {
	var out []pllRegistered
	raw, _ := s.settings.Get("golylang_strings").([]any)
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		r := pllRegistered{Source: "option"}
		r.Name, _ = m["name"].(string)
		r.String, _ = m["string"].(string)
		r.Group, _ = m["context"].(string)
		r.Multiline, _ = m["multiline"].(bool)
		if r.String != "" {
			out = append(out, r)
		}
	}
	return out
}

// pllMigrateOptionStrings copies the option's strings into pll_string_sources (once per process;
// the insert keeps existing rows).
func (s *Server) pllMigrateOptionStrings(ctx context.Context, legacy []pllRegistered) {
	if len(legacy) == 0 {
		return
	}
	if _, done := pllOptionMigrated.LoadOrStore(s, true); done {
		return
	}
	batch := make([]pllCaptureItem, 0, len(legacy))
	keys := map[string]bool{}
	for _, r := range legacy {
		it := pllCaptureItem{original: pllCaptureClean(r.String), context: pllCaptureClean(r.Name), domain: pllDomainRegistered,
			source: "group:" + pllCaptureClean(r.Group)}
		if it.original == "" || len(it.original) > pllCaptureMaxBytes || len(it.context) > pllCaptureMaxMeta || len(it.source) > pllCaptureMaxMeta || keys[it.key()] {
			continue
		}
		keys[it.key()] = true
		batch = append(batch, it)
	}
	if err := s.pllCaptureWrite(ctx, batch); err != nil {
		pllOptionMigrated.Delete(s)
		slog.Warn("golylang: copy registered strings", "err", err)
	}
}

// pllTableStrings are the rows of pll_string_sources.
func (s *Server) pllTableStrings(ctx context.Context) []pllRegistered {
	rows, err := s.store.Pool.Query(ctx, `SELECT original, context, domain, source, url FROM pll_string_sources
		ORDER BY (source = 'page'), source, original LIMIT $1`, pllStringsMax)
	if err != nil {
		return nil
	}
	defer rows.Close()
	groups := map[string]string{}
	var out []pllRegistered
	for rows.Next() {
		var orig, ctx, dom, src, url string
		if rows.Scan(&orig, &ctx, &dom, &src, &url) != nil {
			continue
		}
		g, ok := groups[src]
		if !ok {
			g = s.pllSourceGroup(src)
			groups[src] = g
		}
		r := pllRegistered{String: orig, Group: g, Source: src, Multiline: strings.Contains(orig, "\n")}
		switch {
		case dom == pllDomainRegistered:
			r.Name = ctx
		case ctx != "":
			r.Name, r.Context = ctx, ctx
		case src == "page":
			r.Name = url
		default:
			r.Name = dom
		}
		if r.Name == "" {
			r.Name = orig
			if len(r.Name) > 40 {
				cut := 40
				for cut > 0 && (r.Name[cut]&0xC0) == 0x80 {
					cut--
				}
				r.Name = r.Name[:cut]
			}
		}
		out = append(out, r)
	}
	return out
}

// pllSourceGroup is the group label of a source: "Theme: <name>", "Plugin: <name>", "Page text".
func (s *Server) pllSourceGroup(source string) string {
	kind, rest, _ := strings.Cut(source, ":")
	switch kind {
	case "page":
		return "Page text"
	case "core":
		return "Pursae"
	case "group":
		if rest == "" {
			return "Pursae Multi Language"
		}
		return rest
	case "theme":
		if t := themeBySlug(rest); t != nil && t.Name != "" {
			return "Theme: " + t.Name
		}
		return "Theme: " + rest
	case "plugin":
		if s.plugins != nil && s.plugins.rt != nil {
			if p, ok := s.plugins.rt.Plugin(rest); ok && p.Manifest() != nil && p.Manifest().Name != "" {
				return "Plugin: " + p.Manifest().Name
			}
		}
		return "Plugin: " + rest
	}
	return source
}

// pllRegisterString is pll_register_string(): the string is recorded under its group (name is the
// label the Translations screen shows) and translated by the string itself.
func (s *Server) pllRegisterString(ctx context.Context, name, str, group string, multiline bool) {
	if str == "" {
		return
	}
	if group == "" {
		group = "Pursae Multi Language"
	}
	_ = multiline // a multi-line string shows a textarea on its own
	s.pllCaptureString(ctx, str, name, pllDomainRegistered, "group:"+group, pllRequestPath(ctx))
}

// pllDeleteStrings removes recorded strings (by translation key) from the list; their
// translations stay until Clean strings.
func (s *Server) pllDeleteStrings(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	del := make(map[string]bool, len(keys))
	for _, k := range keys {
		del[k] = true
	}
	if list, ok := s.settings.Get("golylang_strings").([]any); ok {
		keep := []any{}
		for _, v := range list {
			if m, ok := v.(map[string]any); ok {
				if str, _ := m["string"].(string); del[str] {
					continue
				}
			}
			keep = append(keep, v)
		}
		if len(keep) != len(list) {
			if err := s.settings.Set(ctx, map[string]any{"golylang_strings": keep}); err != nil {
				return err
			}
		}
	}
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM pll_string_sources WHERE `+pllKeySQL+` = ANY($1)`, keys)
	s.pllCaptureForget()
	return err
}

// pllCleanForeignPageStrings removes untranslated page text that is no source string: rows
// recorded on a page of a non-default language (URL /<slug>/… or /language/<slug>/…, under the
// home path; versions before 0.1.9 recorded those) and language names (the switcher). Rows with a
// translation stay: the visual editor lists a string it translated on any page.
func (s *Server) pllCleanForeignPageStrings(ctx context.Context) error {
	langs := s.pllLanguages(ctx)
	var prefixes []string
	base := ""
	if u, err := url.Parse(s.links(ctx).Home); err == nil {
		base = strings.TrimRight(u.Path, "/")
	}
	for _, l := range langs {
		if l.IsDefault || l.Slug == "" {
			continue
		}
		prefixes = append(prefixes, base+"/"+l.Slug, base+"/language/"+l.Slug)
	}
	names := pllLanguageLabels(langs)
	rows, err := s.store.Pool.Query(ctx, `SELECT `+pllKeySQL+`, original, url FROM pll_string_sources ss WHERE source = 'page'
		AND NOT EXISTS (SELECT 1 FROM pll_strings t WHERE t.original = (CASE WHEN ss.context <> '' AND ss.domain <> 'pll'
			THEN ss.context || chr(4) || ss.original ELSE ss.original END))`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var k, orig, u string
		if rows.Scan(&k, &orig, &u) != nil {
			continue
		}
		junk := names[pllCollapseWS(strings.TrimSpace(orig))]
		for _, p := range prefixes {
			if junk = junk || u == p || strings.HasPrefix(u, p+"/"); junk {
				break
			}
		}
		if junk {
			keys = append(keys, k)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	_, err = s.store.Pool.Exec(ctx, `DELETE FROM pll_string_sources WHERE source = 'page' AND `+pllKeySQL+` = ANY($1)`, keys)
	s.pllCaptureForget()
	return err
}

// pllCleanStrings is "Clean strings translations database": untranslated page text recorded on
// pages of other languages and language names are removed (pllCleanForeignPageStrings), then
// recorded strings not seen for 30 days that were never translated, then the translations of
// strings no longer listed.
func (s *Server) pllCleanStrings(ctx context.Context) error {
	if err := s.pllCleanForeignPageStrings(ctx); err != nil {
		return err
	}
	if _, err := s.store.Pool.Exec(ctx, `DELETE FROM pll_string_sources ss WHERE ss.last_seen < now() - interval '30 days'
		AND NOT EXISTS (SELECT 1 FROM pll_strings t WHERE t.original = (CASE WHEN ss.context <> '' AND ss.domain <> 'pll'
			THEN ss.context || chr(4) || ss.original ELSE ss.original END))`); err != nil {
		return err
	}
	keys := []string{} // the strings that are not rows of pll_string_sources
	for _, r := range s.pllStrings(ctx) {
		if r.Source == "" || r.Source == "option" {
			keys = append(keys, r.Key())
		}
	}
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM pll_strings WHERE NOT (original = ANY($1))
		AND original NOT IN (SELECT `+pllKeySQL+` FROM pll_string_sources)`, keys)
	s.pllCaptureForget()
	return err
}
