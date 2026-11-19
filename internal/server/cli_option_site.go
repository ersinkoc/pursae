package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/cache"
	"wpgo/internal/cli"
	"wpgo/internal/links"
	"wpgo/internal/store"
)

// The site commands of the option group: transient, cache, rewrite, maintenance-mode, search-replace.

// --- transient ------------------------------------------------------------------------

// transientChangesetPrefix marks Customizer drafts kept in the transients table (customize.go):
// they are unsaved work, not disposable cache, so `transient delete --all` keeps them by default.
const transientChangesetPrefix = "customize_changeset:"

func registerTransientCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("transient", "Manage transients (the expiring key/value cache in the transients table).")

	r.Add(cli.Command{
		Path:          "transient get",
		Short:         "Get the value of a transient.",
		Long:          "Expired transients count as not set. Objects and arrays print as JSON.",
		Args:          []cli.Arg{{Name: "key", Usage: "The transient key."}},
		Formats:       []string{"plaintext"},
		DefaultFormat: "plaintext",
		Examples:      []string{"transient get wpgo_hub_index --format=json"},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			var raw []byte
			err = pool.QueryRow(c.Context, `SELECT value FROM transients WHERE key = $1 AND (expires_at IS NULL OR expires_at > now())`, c.Arg(0)).Scan(&raw)
			if errors.Is(err, pgx.ErrNoRows) {
				return cli.Errorf("Transient with key \"%s\" is not set.", c.Arg(0))
			}
			if err != nil {
				return err
			}
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			return c.PrintValue(v)
		},
	})

	r.Add(cli.Command{
		Path:  "transient set",
		Short: "Set a transient value.",
		Long:  "The value is stored as a JSON string, or parsed as JSON with --format=json. Without <expiration> (seconds) it never expires.",
		Args: []cli.Arg{{Name: "key", Usage: "The transient key."}, {Name: "value", Usage: "The value."},
			{Name: "expiration", Optional: true, Usage: "Time until expiration, in seconds (0: never)."}},
		Formats:       []string{"plaintext"},
		DefaultFormat: "plaintext",
		Examples:      []string{"transient set sample_key 'test data' 300", `transient set my_cache '{"a":1}' --format=json`},
		Run: func(c *cli.Ctx) error {
			key := c.Arg(0)
			if key == "" {
				return cli.Usagef("the transient key is empty")
			}
			var v any = c.Arg(1)
			if c.Format == "json" {
				if err := json.Unmarshal([]byte(c.Arg(1)), &v); err != nil {
					return cli.Errorf("invalid JSON value: %v", err)
				}
			}
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			var expires *time.Time
			if s := c.Arg(2); s != "" {
				n, err := strconv.ParseInt(s, 10, 64)
				if err != nil || n < 0 {
					return cli.Usagef("<expiration> must be a number of seconds, got %q", s)
				}
				if n > 0 {
					t := time.Now().Add(time.Duration(n) * time.Second)
					expires = &t
				}
			}
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			if _, err := pool.Exec(c.Context, `INSERT INTO transients (key, value, expires_at) VALUES ($1, $2::jsonb, $3)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at, updated_at = now()`, key, raw, expires); err != nil {
				return err
			}
			c.Success("Transient added.")
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "transient delete",
		Short: "Delete a transient, every expired one (--expired) or all of them (--all).",
		Long: "--all keeps Customizer drafts (customize_changeset:*) unless --include-changesets is given: they are\n" +
			"unsaved work, not cache. Caches such as the Pursae Manager catalog are fetched again when needed.",
		Args: []cli.Arg{{Name: "key", Optional: true, Usage: "The transient key."}},
		Flags: []cli.Flag{
			{Name: "expired", Bool: true, Usage: "Delete every expired transient."},
			{Name: "all", Bool: true, Usage: "Delete every transient."},
			{Name: "include-changesets", Bool: true, Usage: "With --all: also delete Customizer drafts."},
		},
		Examples: []string{"transient delete sample_key", "transient delete --expired", "transient delete --all --yes"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			key, all, expired := c.Arg(0), c.Bool("all"), c.Bool("expired")
			n := 0
			if key != "" {
				n++
			}
			if all {
				n++
			}
			if expired {
				n++
			}
			if n != 1 {
				return cli.Usagef("give exactly one of <key>, --expired or --all")
			}
			pool, err := e.Pool(ctx)
			if err != nil {
				return err
			}
			switch {
			case expired:
				tag, err := pool.Exec(ctx, `DELETE FROM transients WHERE expires_at IS NOT NULL AND expires_at < now()`)
				if err != nil {
					return err
				}
				c.Success("%d expired transient(s) deleted.", tag.RowsAffected())
			case all:
				q := `DELETE FROM transients WHERE key NOT LIKE 'customize\_changeset:%' ESCAPE '\'`
				what := "every transient (Customizer drafts are kept)"
				if c.Bool("include-changesets") {
					q, what = `DELETE FROM transients`, "every transient, including unsaved Customizer drafts"
				}
				if err := c.Confirm("Delete %s?", what); err != nil {
					return err
				}
				tag, err := pool.Exec(ctx, q)
				if err != nil {
					return err
				}
				c.Success("%d transient(s) deleted.", tag.RowsAffected())
			default:
				if strings.HasPrefix(key, transientChangesetPrefix) {
					if err := c.Confirm("'%s' is an unsaved Customizer draft. Delete it?", key); err != nil {
						return err
					}
				}
				tag, err := pool.Exec(ctx, `DELETE FROM transients WHERE key = $1`, key)
				if err != nil {
					return err
				}
				if tag.RowsAffected() == 0 {
					c.Warning("Transient with key \"%s\" is not set.", key)
					return cli.Exit(cli.ExitFailure)
				}
				c.Success("Transient deleted.")
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "transient list",
		Short: "List transients and their values.",
		Long:  "Values are truncated in table output; secret-looking values are masked unless --unmask is given.",
		Flags: []cli.Flag{
			{Name: "search", Usage: "Only keys matching this glob (* and ? wildcards)."},
			{Name: "expired", Bool: true, Usage: "Only expired transients."},
			{Name: "unmask", Bool: true, Usage: "Print secret-looking values instead of ********."},
		},
		Examples: []string{"transient list", "transient list --search='wpgo_hub_*' --fields=name,expiration", "transient list --expired --format=count"},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			rows, err := pool.Query(c.Context, `SELECT key, value, expires_at, updated_at FROM transients
				WHERE ($1 = '' OR key LIKE $1 ESCAPE '\') AND (NOT $2 OR (expires_at IS NOT NULL AND expires_at < now()))
				ORDER BY key`, optGlobToLike(c.Flag("search")), c.Bool("expired"))
			if err != nil {
				return err
			}
			defer rows.Close()
			var out []cli.Row
			now := time.Now()
			for rows.Next() {
				var key string
				var raw []byte
				var exp *time.Time
				var updated time.Time
				if err := rows.Scan(&key, &raw, &exp, &updated); err != nil {
					return err
				}
				var v any
				_ = json.Unmarshal(raw, &v)
				if !c.Bool("unmask") {
					v = maskSecrets(v, key)
				}
				if c.Format == "table" && c.Field == "" {
					v = optTruncate(cli.Stringify(v), 60)
				}
				var expiration any = "false"
				if exp != nil {
					expiration = *exp
				}
				out = append(out, cli.Row{"name": key, "value": v, "expiration": expiration,
					"expired": exp != nil && exp.Before(now), "size_bytes": len(raw), "updated_at": updated})
			}
			if err := rows.Err(); err != nil {
				return err
			}
			return c.Print(out, cli.View{Fields: []string{"name", "value", "expiration"}, ID: "name"})
		},
	})
}

// --- cache ----------------------------------------------------------------------------

// cacheDiskDir is where the page cache persists pages (configureCache: <uploads>/../wpgo-cache).
func cacheDiskDir(uploads string) string {
	if uploads == "" {
		return ""
	}
	return filepath.Clean(filepath.Join(uploads, "..", "wpgo-cache"))
}

// cacheDiskClear removes the persisted page files (*.wpc) and temp files (.tmp-*) from dir, like
// cache.diskStore.clear, and returns how many it removed and any removal errors.
func cacheDiskClear(dir string) (int, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	var removeErrs []error
	for _, de := range des {
		name := de.Name()
		if !de.IsDir() && (strings.HasSuffix(name, ".wpc") || strings.HasPrefix(name, ".tmp-")) {
			if err := os.Remove(filepath.Join(dir, name)); err == nil {
				n++
			} else if !errors.Is(err, fs.ErrNotExist) {
				removeErrs = append(removeErrs, err)
			}
		}
	}
	return n, errors.Join(removeErrs...)
}

// cacheDiskStats counts the persisted page files in dir and their size.
func cacheDiskStats(dir string) (files int, bytes int64, err error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	for _, de := range des {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".wpc") {
			continue
		}
		if info, err := de.Info(); err == nil {
			files++
			bytes += info.Size()
		}
	}
	return files, bytes, nil
}

func registerCacheCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("cache", "Purge and inspect the page cache (wpgo-cache) of running servers.")
	cdnHint := "Front proxies set in wpgo_cache_purge_url are purged too; a CDN (Cloudflare …) may need its own purge."

	r.Add(cli.Command{
		Path:  "cache flush",
		Short: "Purge the whole page cache on every running server, and the persisted disk cache.",
		Long: "Announces a purge-all on the wpgo_cache_purge channel: every running server empties its memory and\n" +
			"disk cache and reloads its media index. The persisted pages in <uploads>/../wpgo-cache are also\n" +
			"removed here, so a server that is not running does not load stale pages at its next start.\n" +
			"There is no object cache: this is the only cache to flush.\n\n" + cdnHint,
		Examples: []string{"cache flush"},
		Run: func(c *cli.Ctx) error {
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(c.Context, cliCachePurgeTimeout)
			defer cancel()
			p := cache.Purge{All: true}
			if err := s.notifyCachePurge(ctx, p); err != nil {
				return err
			}
			proxyErr := s.purgeCLIProxy(ctx, p)
			dir := cacheDiskDir(e.Config().UploadsDir)
			if dir != "" {
				n, err := cacheDiskClear(dir)
				if n > 0 {
					c.Log("Removed %d persisted page file(s) from %s.", n, dir)
				}
				if err != nil {
					return fmt.Errorf("disk cache %s: %w", dir, err)
				}
			}
			if proxyErr != nil {
				return proxyErr
			}
			c.Success("The page cache was flushed.")
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "cache purge",
		Short: "Purge one URL, a post's pages, cache tags, the front page or cached 404s.",
		Long:  "A relative --url is taken from the site's home URL. Tags are those the page cache stores (home, post:<id>, term:<id>, feed, widgets, 404 …).\n\n" + cdnHint,
		Flags: []cli.Flag{
			{Name: "url", Usage: "A URL or path (e.g. /about/)."},
			{Name: "post", Usage: "A post ID: its permalink, archives and feeds."},
			{Name: "tag", Usage: "Cache tags, comma-separated."},
			{Name: "front", Bool: true, Usage: "The front page (tag home)."},
			{Name: "404", Bool: true, Usage: "Cached 404 pages (tag 404)."},
		},
		Examples: []string{"cache purge --url=/about/", "cache purge --post=12", "cache purge --tag=home,feed", "cache purge --front --404"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			var tags []string
			tags = append(tags, c.List("tag")...)
			if c.Bool("front") {
				tags = append(tags, "home")
			}
			if c.Bool("404") {
				tags = append(tags, "404")
			}
			u := strings.TrimSpace(c.Flag("url"))
			if len(tags) == 0 && u == "" && !c.Has("post") {
				return cli.Usagef("give --url, --post, --tag, --front or --404 (or use '%s cache flush')", c.Prog)
			}
			if c.Has("post") {
				id, err := cli.ParseID(c.Flag("post"))
				if err != nil {
					return err
				}
				s, err := e.Server(ctx)
				if err != nil {
					return err
				}
				p, err := s.store.GetPost(ctx, id)
				if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
					return cli.Errorf("Could not find the post with ID %d.", id)
				}
				if err != nil {
					return err
				}
				s.purgePost(ctx, p)
				c.Log("Purged the pages of post %d.", id)
			}
			if u != "" {
				s, err := e.Server(ctx)
				if err != nil {
					return err
				}
				abs, err := cacheAbsURL(s.configuredHome(), u)
				if err != nil {
					return cli.Usagef("%v", err)
				}
				if err := e.Purge(ctx, cache.Purge{URL: abs}); err != nil {
					return err
				}
				c.Log("Purged %s.", abs)
			}
			if len(tags) > 0 {
				if err := e.Purge(ctx, cache.Purge{Tags: optUnique(tags)}); err != nil {
					return err
				}
				c.Log("Purged tags: %s.", strings.Join(optUnique(tags), ", "))
			}
			c.Success("Purge sent to running servers.")
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "cache status",
		Short: "Show the page cache settings and the persisted disk cache.",
		Long: "Hit rates and entry counts live only in the server process: see Settings › Pursae Cache or\n" +
			"GET /wp-json/pursae/v1/cache on the running site.",
		Examples: []string{"cache status", "cache status --format=json"},
		Run: func(c *cli.Ctx) error {
			set, err := e.Settings(c.Context)
			if err != nil {
				return err
			}
			item := cli.Row{
				"plugin_active": set.Bool("wpgo_cache_enabled"),
				"serving":       set.Bool("wpgo_cache_serve"),
				"disk":          set.Bool("wpgo_cache_disk"),
				"server_hint":   set.String("wpgo_cache_server"),
				"purge_url":     set.String("wpgo_cache_purge_url"),
			}
			fields := []string{"plugin_active", "serving", "disk", "server_hint", "purge_url"}
			if dir := cacheDiskDir(e.Config().UploadsDir); dir != "" {
				files, size, err := cacheDiskStats(dir)
				if err != nil {
					c.Warning("disk cache %s: %v", dir, err)
				}
				item["disk_dir"], item["disk_files"], item["disk_bytes"] = dir, files, size
				fields = append(fields, "disk_dir", "disk_files", "disk_bytes")
			}
			return c.PrintItem(item, fields)
		},
	})
}

// cacheAbsURL makes a purge URL absolute on home, as the REST purge does (rest_pursae_options.go).
func cacheAbsURL(home, raw string) (string, error) {
	if strings.Contains(raw, "://") {
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			return "", fmt.Errorf("--url must be an http(s) URL or a path")
		}
		return raw, nil
	}
	if home == "" {
		return "", fmt.Errorf("the site has no home URL; pass an absolute --url")
	}
	path, query, _ := strings.Cut(raw, "?")
	u := strings.TrimRight(home, "/") + "/" + strings.TrimLeft(path, "/")
	if query != "" {
		u += "?" + query
	}
	return u, nil
}

// --- rewrite --------------------------------------------------------------------------

func registerRewriteCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("rewrite", "Manage the permalink structure.")

	r.Add(cli.Command{
		Path:  "rewrite structure",
		Short: "Set the permalink structure (and the category and tag bases).",
		Long: "Uses the WordPress tags (%year%, %monthnum%, %day%, %hour%, %minute%, %second%, %post_id%,\n" +
			"%postname%, %category%, %author%). An empty structure or 'plain' gives ?p=123 links; a structure\n" +
			"without any tag is plain too. Links change at once on running servers; the page cache is purged.",
		Args: []cli.Arg{{Name: "permastruct", Usage: "The new structure, e.g. /%postname%/."}},
		Flags: []cli.Flag{
			{Name: "category-base", Usage: "The category base (default: category)."},
			{Name: "tag-base", Usage: "The tag base (default: tag)."},
		},
		Examples: []string{"rewrite structure '/%postname%/'", "rewrite structure '/%year%/%monthnum%/%postname%/' --category-base=topics", "rewrite structure plain"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			set, err := e.Settings(ctx)
			if err != nil {
				return err
			}
			raw := c.Arg(0)
			structure := normalizePermalinkStructure(raw)
			if structure == "" && strings.TrimSpace(raw) != "" && strings.TrimSpace(raw) != "plain" {
				c.Warning("%q has no %%tag%%: using plain permalinks.", raw)
			}
			values := map[string]any{"permalink_structure": structure}
			if c.Has("category-base") {
				values["category_base"] = strings.Trim(c.Flag("category-base"), "/ ")
			}
			if c.Has("tag-base") {
				values["tag_base"] = strings.Trim(c.Flag("tag-base"), "/ ")
			}
			if err := set.Set(ctx, values); err != nil {
				return err
			}
			e.optionAudit(c, "permalink", "Permalink structure set to '"+structure+"' from the command line.")
			if err := e.PurgeAll(ctx); err != nil {
				c.Warning("page cache purge: %v", err)
			}
			if structure == "" {
				c.Success("Rewrite structure set to plain permalinks.")
			} else {
				c.Success("Rewrite structure set to %s.", structure)
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "rewrite flush",
		Short: "Purge the page cache (there are no rewrite rules to rebuild).",
		Long:  "Pursae computes links and routes from permalink_structure on every request, so there is nothing to\nflush but the cached pages.",
		Flags: []cli.Flag{{Name: "hard", Bool: true, Hidden: true, Usage: "Accepted for WP-CLI compatibility; no effect."}},
		Run: func(c *cli.Ctx) error {
			if err := e.PurgeAll(c.Context); err != nil {
				return err
			}
			c.Success("Rewrite rules are computed from permalink_structure; page cache purged.")
			return nil
		},
	})

	r.Add(cli.Command{
		Path:     "rewrite list",
		Short:    "Show the permalink settings with an example URL of each link kind.",
		Examples: []string{"rewrite list", "rewrite list --format=json"},
		Run: func(c *cli.Ctx) error {
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			set := s.settings
			rows := rewriteRows(s.configuredHome(), set.String("permalink_structure"), set.String("category_base"), set.String("tag_base"), s.loc())
			return c.Print(rows, cli.View{Fields: []string{"kind", "setting", "example"}, ID: "kind"})
		},
	})
}

// rewriteRows lists the link kinds with the setting that shapes them and an example URL.
func rewriteRows(home, structure, catBase, tagBase string, loc *time.Location) []cli.Row {
	b := &links.Builder{Home: strings.TrimRight(home, "/"), Structure: structure, CatBase: catBase, TagBase: tagBase, Loc: loc}
	date := time.Date(2026, 1, 15, 9, 30, 0, 0, time.UTC)
	post := &store.Post{ID: 123, Type: "post", Status: "publish", Slug: "sample-post", PublishedAt: &date, CreatedAt: date}
	page := &store.Post{ID: 2, Type: "page", Status: "publish", Slug: "sample-page", PublishedAt: &date, CreatedAt: date}
	show := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	return []cli.Row{
		{"kind": "post", "setting": show(structure, "(plain)"), "example": b.Permalink(post)},
		{"kind": "page", "setting": "", "example": b.Permalink(page)},
		{"kind": "category", "setting": show(catBase, "(category)"), "example": b.TermLink(&store.Term{ID: 5, Taxonomy: "category", Slug: "news"})},
		{"kind": "tag", "setting": show(tagBase, "(tag)"), "example": b.TermLink(&store.Term{ID: 7, Taxonomy: "post_tag", Slug: "go"})},
		{"kind": "author", "setting": "", "example": b.AuthorLink(1, "admin")},
		{"kind": "date", "setting": "", "example": b.MonthLink(2026, 1)},
		{"kind": "feed", "setting": "", "example": b.FeedLink("")},
		{"kind": "search", "setting": "", "example": b.SearchLink("hello")},
	}
}

// --- maintenance-mode -----------------------------------------------------------------

func registerMaintenanceCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("maintenance-mode", "Activate, deactivate or check maintenance mode (Pursae Maintenance).")

	status := func(ctx context.Context) (cli.Row, bool, error) {
		s, err := e.Server(ctx)
		if err != nil {
			return nil, false, err
		}
		on := s.maintOn()
		st := s.maintLoad()
		row := cli.Row{"plugin_active": on, "enabled": st.Enabled, "mode": st.Mode, "start": st.StartDate, "end": "", "auto_disable": st.AutoDisable}
		active := on && s.settings.Installed() && st.Enabled
		reason := ""
		if !on {
			reason = "the Pursae Maintenance plugin is inactive"
		} else if !st.Enabled {
			reason = "maintenance mode is off"
		}
		if start, ok := s.maintStart(st); ok && time.Now().Before(start) && active {
			active, reason = false, "scheduled to start at "+start.Format(cli.TimeLayout)
		}
		if end, ok := s.maintEnd(st); ok {
			row["end"] = st.CountdownEnd
			if st.AutoDisable && !time.Now().Before(end) && active {
				active, reason = false, "the countdown ended (auto-disable)"
			}
		}
		row["active"] = active
		row["note"] = reason
		return row, active, nil
	}

	r.Add(cli.Command{
		Path:     "maintenance-mode status",
		Short:    "Show whether visitors get the maintenance page, and why.",
		Examples: []string{"maintenance-mode status", "maintenance-mode status --format=json"},
		Run: func(c *cli.Ctx) error {
			row, active, err := status(c.Context)
			if err != nil {
				return err
			}
			if c.Format == "table" && len(c.Fields) == 0 && c.Field == "" {
				if active {
					c.Println("Maintenance mode is active (" + cli.Stringify(row["mode"]) + ").")
				} else {
					c.Println("Maintenance mode is not active: " + cli.Stringify(row["note"]) + ".")
				}
				return nil
			}
			return c.PrintItem(row, []string{"active", "plugin_active", "enabled", "mode", "start", "end", "auto_disable", "note"})
		},
	})

	r.Add(cli.Command{
		Path:     "maintenance-mode is-active",
		Short:    "Exit 0 when maintenance mode is active, 1 otherwise.",
		Examples: []string{"maintenance-mode is-active && echo closed"},
		Run: func(c *cli.Ctx) error {
			_, active, err := status(c.Context)
			if err != nil {
				return err
			}
			if !active {
				return cli.Exit(cli.ExitFailure)
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "maintenance-mode activate",
		Short: "Switch maintenance mode on.",
		Long: "Needs the Pursae Maintenance plugin; --activate-plugin switches it on first. Running servers close\n" +
			"the site at once (the gate reads the settings on every request); the page cache is purged.",
		Flags: []cli.Flag{
			{Name: "mode", Usage: "maintenance (503) or coming_soon (200); default: unchanged.", Values: []string{"maintenance", "coming_soon"}},
			{Name: "activate-plugin", Bool: true, Usage: "Activate the Pursae Maintenance plugin when it is inactive."},
		},
		Examples: []string{"maintenance-mode activate", "maintenance-mode activate --mode=coming_soon --activate-plugin"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			if !s.maintOn() {
				if !c.Bool("activate-plugin") {
					return cli.Errorf("the Pursae Maintenance plugin is inactive; activate it first or pass --activate-plugin.")
				}
				if err := s.maintPluginAction(ctx, "activate"); err != nil {
					return err
				}
				c.Log("Activated the Pursae Maintenance plugin.")
			}
			st := s.maintLoad()
			mode := c.Flag("mode")
			if st.Enabled && (mode == "" || mode == st.Mode) {
				c.Warning("Maintenance mode already activated.")
				return nil
			}
			st.Enabled = true
			if mode != "" {
				st.Mode = mode
			}
			if err := s.maintSave(ctx, st); err != nil {
				return err
			}
			if start, ok := s.maintStart(st); ok && time.Now().Before(start) {
				c.Warning("a start date is scheduled: the site closes at %s.", start.Format(cli.TimeLayout))
			}
			c.Success("Activated maintenance mode (%s).", st.Mode)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "maintenance-mode deactivate",
		Short: "Switch maintenance mode off.",
		Long:  "When \"notify on open\" is set, subscribers are e-mailed that the site is open, unless --no-notify is given.",
		Flags: []cli.Flag{
			{Name: "notify", Bool: true, Default: "true", Usage: "E-mail subscribers that the site is open (when the plugin is set to)."},
		},
		Examples: []string{"maintenance-mode deactivate", "maintenance-mode deactivate --no-notify"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			st := s.maintLoad()
			if !st.Enabled {
				c.Warning("Maintenance mode already deactivated.")
				return nil
			}
			if c.Bool("notify") {
				err = s.maintSetMode(ctx, false) // notifies subscribers when set to
			} else {
				st.Enabled = false
				err = s.maintSave(ctx, st)
			}
			if err != nil {
				return err
			}
			if !s.maintOn() {
				c.Log("(The Pursae Maintenance plugin is inactive, so the site was not closed.)")
			}
			c.Success("Deactivated maintenance mode.")
			return nil
		},
	})
}

// --- search-replace -------------------------------------------------------------------

// srColumn is one text-like column search-replace works on.
type srColumn struct {
	Table, Column, Type string
}

// srJSON reports whether the column holds JSON (replaced in its text form, then cast back).
func (c srColumn) srJSON() bool { return c.Type == "json" || c.Type == "jsonb" }

// pgJSONEscape escapes s the way PostgreSQL writes a string inside json/jsonb text (escape_json):
// quote, backslash and control characters only; everything else, '/' and non-ASCII included, stays.
func pgJSONEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// srSelect filters the candidate columns: tables (globs; empty = all), skip-tables, skip-columns
// and include-columns (globs; "col" or "table.col").
func srSelect(cols []srColumn, tables, skipTables, skipColumns, includeColumns []string) []srColumn {
	anyMatch := func(globs []string, names ...string) bool {
		for _, g := range globs {
			for _, n := range names {
				if optGlobMatch(g, n) {
					return true
				}
			}
		}
		return false
	}
	var out []srColumn
	for _, c := range cols {
		if len(tables) > 0 && !anyMatch(tables, c.Table) {
			continue
		}
		if anyMatch(skipTables, c.Table) {
			continue
		}
		full := c.Table + "." + c.Column
		if anyMatch(skipColumns, c.Column, full) {
			continue
		}
		if len(includeColumns) > 0 && !anyMatch(includeColumns, c.Column, full) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// srSQL builds the count and update statements of one column. $1 is the search string and $2 the
// replacement (both already JSON-escaped for JSON columns); extra is an additional WHERE clause.
func srSQL(c srColumn, extra string) (count, update string) {
	t, col := pgx.Identifier{c.Table}.Sanitize(), pgx.Identifier{c.Column}.Sanitize()
	expr := col
	if c.srJSON() {
		expr = col + "::text"
	}
	where := "strpos(" + expr + ", $1) > 0"
	if extra != "" {
		where += " AND " + extra
	}
	count = "SELECT count(*), COALESCE(sum((length(" + expr + ") - length(replace(" + expr + ", $1, ''))) / length($1)), 0) FROM " + t + " WHERE " + where
	set := "replace(" + expr + ", $1, $2)"
	if c.srJSON() {
		set = "(" + set + ")::" + c.Type
	}
	update = "UPDATE " + t + " SET " + col + " = " + set + " WHERE " + where
	return count, update
}

func registerSearchReplaceCLI(r *cli.Registry, e *cliEnv) {
	r.Add(cli.Command{
		Path:  "search-replace",
		Short: "Search and replace a string in the database (e.g. to move the site to a new URL).",
		Long: "Works on every text, varchar, json and jsonb column of the site's tables, except generated columns,\n" +
			"primary keys and tables that only make sense on the running site (sessions, transients, jobs,\n" +
			"schema_migrations). JSON values are searched in their stored text form with the strings JSON-escaped,\n" +
			"so object keys match too. Everything runs in one transaction: an error changes nothing.\n\n" +
			"The options auth_secret and siteurl are skipped unless --all-options is given; change the address\n" +
			"with `option update home <url>` and `option update siteurl <url>`. Running servers reload changed\n" +
			"settings at once; the page cache is purged. Files in the uploads directory are not touched.",
		Args: []cli.Arg{{Name: "old", Usage: "The string to find."}, {Name: "new", Usage: "The replacement."},
			{Name: "table", Optional: true, Repeat: true, Usage: "Only these tables (globs allowed)."}},
		Flags: []cli.Flag{
			{Name: "dry-run", Bool: true, Usage: "Count the replacements without changing anything."},
			{Name: "skip-tables", Usage: "Tables to skip, comma-separated (globs allowed)."},
			{Name: "skip-columns", Usage: "Columns to skip (column or table.column), comma-separated."},
			{Name: "include-columns", Usage: "Only these columns (column or table.column), comma-separated."},
			{Name: "all-options", Bool: true, Usage: "Also replace in the auth_secret and siteurl options."},
			{Name: "report-changed-only", Bool: true, Usage: "Only list columns with replacements."},
		},
		Examples: []string{
			"search-replace 'http://old.example' 'https://new.example' --dry-run",
			"search-replace 'http://old.example' 'https://new.example' --yes",
			"search-replace foo bar posts terms --skip-columns=slug --yes",
			"search-replace 'old.example' 'new.example' --format=count --dry-run",
		},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			old, repl := c.Arg(0), c.Arg(1)
			if old == "" {
				return cli.Usagef("<old> is empty")
			}
			if old == repl {
				return cli.Errorf("<old> and <new> are the same: nothing to replace.")
			}
			pool, err := e.Pool(ctx)
			if err != nil {
				return err
			}
			cols, err := srColumns(ctx, pool)
			if err != nil {
				return err
			}
			if names := c.Args[2:]; len(names) > 0 {
				known := map[string]bool{}
				for _, col := range cols {
					known[col.Table] = true
				}
				for _, n := range names {
					if strings.ContainsAny(n, "*?") {
						continue
					}
					if bmgrNever[n] {
						return cli.Errorf("table '%s' is never searched (it only makes sense on the running site).", n)
					}
					if !known[n] {
						return cli.Errorf("no text columns in table '%s' (or it does not exist).", n)
					}
				}
			}
			cols = srSelect(cols, c.Args[2:], c.List("skip-tables"), c.List("skip-columns"), c.List("include-columns"))
			if len(cols) == 0 {
				return cli.Errorf("no columns to search.")
			}
			dry := c.Bool("dry-run")
			if !dry {
				tables := map[string]bool{}
				for _, col := range cols {
					tables[col.Table] = true
				}
				if err := c.Confirm("Replace %q with %q in %d column(s) of %d table(s)?", old, repl, len(cols), len(tables)); err != nil {
					return err
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)
			var out []cli.Row
			var total, changedRows int64
			settingsChanged := false
			for _, col := range cols {
				extra := ""
				if col.Table == "settings" && !c.Bool("all-options") {
					extra = "key NOT IN ('auth_secret', 'siteurl')"
				}
				a, b := old, repl
				if col.srJSON() {
					a, b = pgJSONEscape(old), pgJSONEscape(repl)
				}
				countSQL, updateSQL := srSQL(col, extra)
				var nrows, n int64
				if err := tx.QueryRow(ctx, countSQL, a).Scan(&nrows, &n); err != nil {
					return fmt.Errorf("%s.%s: %w", col.Table, col.Column, err)
				}
				if n > 0 && !dry {
					if _, err := tx.Exec(ctx, updateSQL, a, b); err != nil {
						return fmt.Errorf("%s.%s: %w (nothing was changed)", col.Table, col.Column, err)
					}
					if col.Table == "settings" {
						settingsChanged = true
					}
				}
				total += n
				changedRows += nrows
				if n == 0 && c.Bool("report-changed-only") {
					continue
				}
				out = append(out, cli.Row{"Table": col.Table, "Column": col.Column, "Type": col.Type, "Replacements": n, "Rows": nrows})
			}
			if !dry {
				if err := tx.Commit(ctx); err != nil {
					return err
				}
			}
			if c.Format == "count" {
				c.Println(strconv.FormatInt(total, 10))
			} else if err := c.Print(out, cli.View{Fields: []string{"Table", "Column", "Replacements", "Type"}, ID: "Table"}); err != nil {
				return err
			}
			// The summary line only goes with the human-readable table: machine formats
			// (count, json, csv, yaml, ids, --field) print nothing but their data.
			human := c.Format == "table" && c.Field == ""
			if dry {
				if human {
					c.Success("%d replacement(s) to be made in %d row(s).", total, changedRows)
				}
				return nil
			}
			if total > 0 {
				if settingsChanged {
					if set, err := e.Settings(ctx); err == nil {
						_ = set.Load(ctx)
					}
				}
				e.optionAudit(c, "search-replace", fmt.Sprintf("search-replace %q -> %q: %d replacement(s) from the command line.", old, repl, total))
				if err := e.PurgeAll(ctx); err != nil {
					c.Warning("page cache purge: %v", err)
				}
			}
			if human {
				c.Success("Made %d replacement(s) in %d row(s).", total, changedRows)
			}
			return nil
		},
	})
}

// srColumns lists the text-like columns of the public base tables: no generated columns, no
// primary key columns, no bmgrNever tables.
func srColumns(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]srColumn, error) {
	rows, err := q.Query(ctx, `SELECT c.table_name, c.column_name, c.data_type
		FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		WHERE c.table_schema = 'public' AND c.is_generated = 'NEVER'
		  AND c.data_type IN ('text', 'character varying', 'character', 'json', 'jsonb')
		  AND NOT EXISTS (
			SELECT 1 FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
			WHERE i.indisprimary AND i.indrelid = (quote_ident(c.table_schema) || '.' || quote_ident(c.table_name))::regclass
			  AND a.attname = c.column_name)
		ORDER BY c.table_name, c.ordinal_position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []srColumn
	for rows.Next() {
		var col srColumn
		if err := rows.Scan(&col.Table, &col.Column, &col.Type); err != nil {
			return nil, err
		}
		if bmgrNever[col.Table] {
			continue
		}
		out = append(out, col)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Table < out[j].Table })
	return out, rows.Err()
}
