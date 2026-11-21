package server

import (
	"encoding/json"
	"io"
	"slices"
	"sort"
	"strings"

	"wpgo/internal/cli"
	"wpgo/internal/store"
)

// Post meta is the posts.meta jsonb column: one value per key (WordPress allows several), so
// `post meta add` refuses an existing key. Writes go through UpdatePost (row lock, no revision)
// and afterPostSave, like the Custom Fields box.

// contentParseMetaValue reads a meta value: the raw string, or JSON with --format=json.
func contentParseMetaValue(raw, format string) (any, error) {
	if format != "json" {
		return raw, nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, cli.Usagef("the value is not valid JSON: %v", err)
	}
	return v, nil
}

// contentMetaValue reads the <value> argument at index i, or stdin when it is absent and stdin is
// not a terminal (WP-CLI reads STDIN the same way).
func contentMetaValue(c *cli.Ctx, i int) (string, error) {
	if i < len(c.Args) {
		return c.Args[i], nil
	}
	if c.Interactive() {
		return "", cli.Usagef("missing <value>")
	}
	b, err := io.ReadAll(c.Stdin())
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func registerPostMetaCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("post meta", "Manage post custom fields (meta).")
	idArg := cli.Arg{Name: "id", Usage: "The post ID."}

	r.Add(cli.Command{
		Path:  "post meta list",
		Short: "List a post's meta.",
		Long:  "Fields: post_id, meta_key, meta_value. In table and CSV output non-string values are JSON.",
		Args:  []cli.Arg{idArg},
		Flags: []cli.Flag{{Name: "keys", Usage: "Only these keys, comma-separated."}},
		Examples: []string{
			"post meta list 12",
			"post meta list 12 --keys=_thumbnail_id,color --format=json",
		},
		Run: func(c *cli.Ctx) error {
			_, p, err := contentLoadPost(c, e, 0)
			if err != nil {
				return err
			}
			keys := make([]string, 0, len(p.Meta))
			for k := range p.Meta {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			only := c.List("keys")
			structured := c.Format == "json" || c.Format == "yaml"
			rows := make([]cli.Row, 0, len(keys))
			for _, k := range keys {
				if len(only) > 0 && !slices.Contains(only, k) {
					continue
				}
				v := p.Meta[k]
				if !structured {
					v = metaValueString(v)
				}
				rows = append(rows, cli.Row{"post_id": p.ID, "meta_key": k, "meta_value": v})
			}
			return c.Print(rows, cli.View{Fields: []string{"post_id", "meta_key", "meta_value"}, ID: "meta_key"})
		},
	})

	r.Add(cli.Command{
		Path:          "post meta get",
		Short:         "Get a meta value (exit code 1 when the key is not set).",
		Args:          []cli.Arg{idArg, {Name: "key", Usage: "The meta key."}},
		Formats:       []string{"plaintext"},
		DefaultFormat: "plaintext",
		Examples:      []string{"post meta get 12 _thumbnail_id", "post meta get 12 settings --format=json"},
		Run: func(c *cli.Ctx) error {
			_, p, err := contentLoadPost(c, e, 0)
			if err != nil {
				return err
			}
			v, ok := p.Meta[c.Arg(1)]
			if !ok {
				return cli.Exit(cli.ExitFailure)
			}
			if c.Format == "plaintext" || c.Format == "table" || c.Format == "csv" {
				c.Println(metaValueString(v))
				return nil
			}
			return c.PrintValue(v)
		},
	})

	for _, verb := range []string{"add", "update"} {
		short := "Update a meta value (adds the key when missing)."
		if verb == "add" {
			short = "Add a meta value (fails when the key is already set)."
		}
		r.Add(cli.Command{
			Path:  "post meta " + verb,
			Short: short,
			Long: `The value is stored as a string; with --format=json it is parsed as JSON (objects, arrays,
numbers, booleans). Without <value> it is read from stdin. Keys starting with "_" are
protected: the admin's Custom Fields box hides them.

` + contentSideEffects,
			Args:          []cli.Arg{idArg, {Name: "key", Usage: "The meta key."}, {Name: "value", Usage: "The value (default: stdin).", Optional: true}},
			Formats:       []string{"plaintext"},
			DefaultFormat: "plaintext",
			Examples: []string{
				"post meta " + verb + " 12 color blue",
				`post meta ` + verb + ` 12 settings '{"layout":"wide"}' --format=json`,
			},
			Run: func(c *cli.Ctx) error {
				s, p, err := contentLoadPost(c, e, 0)
				if err != nil {
					return err
				}
				key := c.Arg(1)
				if strings.TrimSpace(key) == "" {
					return cli.Usagef("the meta key is empty")
				}
				raw, err := contentMetaValue(c, 2)
				if err != nil {
					return err
				}
				v, err := contentParseMetaValue(raw, c.Format)
				if err != nil {
					return err
				}
				if _, exists := p.Meta[key]; exists && verb == "add" {
					return cli.Errorf("Meta key '%s' already exists on post %d (a key holds one value here; use 'post meta update').", key, p.ID)
				}
				if strings.HasPrefix(key, "_") {
					c.Warning("meta key %q is protected (the admin's Custom Fields box hides it).", key)
				}
				actor, err := e.Actor(c)
				if err != nil {
					return err
				}
				if p.Meta == nil {
					p.Meta = map[string]any{}
				}
				p.Meta[key] = v
				if err := s.store.UpdatePost(c.Context, p, actor.ID); err != nil {
					return err
				}
				s.afterPostSave(c.Context, p)
				if verb == "add" {
					c.Success("Added custom field.")
				} else {
					c.Success("Updated custom field '%s'.", key)
				}
				return nil
			},
		})
	}

	r.Add(cli.Command{
		Path:  "post meta delete",
		Short: "Delete meta keys from a post.",
		Long:  "Asks for confirmation (pass --yes in scripts).\n\n" + contentSideEffects,
		Args:  []cli.Arg{idArg, {Name: "key", Usage: "Meta key(s).", Optional: true, Repeat: true}},
		Flags: []cli.Flag{{Name: "all", Usage: "Delete every meta key of the post.", Bool: true}},
		Examples: []string{
			"post meta delete 12 color --yes",
			"post meta delete 12 --all --yes",
		},
		Run: func(c *cli.Ctx) error {
			s, p, err := contentLoadPost(c, e, 0)
			if err != nil {
				return err
			}
			keys := c.Args[1:]
			all := c.Bool("all")
			if all == (len(keys) > 0) {
				return cli.Usagef("pass meta key(s) or --all")
			}
			if all {
				keys = nil
				for k := range p.Meta {
					keys = append(keys, k)
				}
				sort.Strings(keys)
			}
			var del []string
			for _, k := range keys {
				if _, ok := p.Meta[k]; ok {
					del = append(del, k)
				} else {
					c.Warning("Post %d has no meta key '%s'.", p.ID, k)
				}
			}
			if len(del) == 0 {
				if all {
					c.Success("Post %d has no meta.", p.ID)
					return nil
				}
				return cli.Exit(cli.ExitFailure)
			}
			if err := c.Confirm("Delete %d meta key(s) of post %d (%s)?", len(del), p.ID, strings.Join(del, ", ")); err != nil {
				return err
			}
			actor, err := e.Actor(c)
			if err != nil {
				return err
			}
			for _, k := range del {
				delete(p.Meta, k)
			}
			if err := s.store.UpdatePost(c.Context, p, actor.ID); err != nil {
				return err
			}
			s.afterPostSave(c.Context, p)
			c.Success("Deleted %d custom field(s).", len(del))
			if len(del) < len(keys) {
				return cli.Exit(cli.ExitFailure)
			}
			return nil
		},
	})
}

func registerPostTermCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("post term", "Manage the terms of a post.")
	idTax := []cli.Arg{{Name: "id", Usage: "The post ID."}, {Name: "taxonomy", Usage: "The taxonomy."}}

	r.Add(cli.Command{
		Path:     "post term list",
		Short:    "List a post's terms of one or more taxonomies.",
		Long:     "Fields: term_id, name, slug, taxonomy, parent, count.",
		Args:     []cli.Arg{idTax[0], {Name: "taxonomy", Usage: "Taxonomy (or taxonomies).", Repeat: true}},
		Examples: []string{"post term list 12 category post_tag", "post term list 12 post_tag --field=name"},
		Run: func(c *cli.Ctx) error {
			s, p, err := contentLoadPost(c, e, 0)
			if err != nil {
				return err
			}
			var rows []cli.Row
			for _, slug := range c.Args[1:] {
				tax, err := contentTaxonomy(c.Context, s, slug)
				if err != nil {
					return err
				}
				m, err := s.store.TermsForPosts(c.Context, []int64{p.ID}, tax.Slug)
				if err != nil {
					return err
				}
				for _, t := range m[p.ID] {
					rows = append(rows, s.contentTermRow(c.Context, t, false))
				}
			}
			return c.Print(rows, cli.View{Fields: []string{"term_id", "name", "slug", "taxonomy"}, ID: "term_id"})
		},
	})

	for _, verb := range []string{"set", "add", "remove"} {
		args := append(slices.Clone(idTax), cli.Arg{Name: "term", Usage: "Term ID(s), slug(s) or name(s) (see --by).", Repeat: true})
		flags := []cli.Flag{{Name: "by", Usage: "How to read <term>: id, slug or name (default: an ID when numeric, else slug, then name). With name, missing terms of a non-hierarchical taxonomy are created.", Values: []string{"id", "slug", "name"}}}
		short := map[string]string{
			"set":    "Replace a post's terms of a taxonomy.",
			"add":    "Add terms to a post.",
			"remove": "Remove terms from a post.",
		}[verb]
		if verb == "remove" {
			args[2].Optional = true
			flags = append(flags, cli.Flag{Name: "all", Usage: "Remove every term of the taxonomy.", Bool: true})
		}
		r.Add(cli.Command{
			Path:  "post term " + verb,
			Short: short,
			Long: `Term counts are refreshed. A post left without a category gets the default category (posts).

` + contentSideEffects,
			Args:     args,
			Flags:    flags,
			Examples: []string{"post term " + verb + " 12 post_tag go cms --by=name", "post term " + verb + " 12 category 3"},
			Run: func(c *cli.Ctx) error {
				ctx := c.Context
				s, p, err := contentLoadPost(c, e, 0)
				if err != nil {
					return err
				}
				tax, err := contentTaxonomy(ctx, s, c.Arg(1))
				if err != nil {
					return err
				}
				refs := c.Args[2:]
				if verb == "remove" && c.Bool("all") == (len(refs) > 0) {
					return cli.Usagef("pass term(s) or --all")
				}
				var ids []int64
				by := c.Flag("by")
				if by == "name" && !tax.Hierarchical && verb != "remove" {
					if ids, err = s.store.TermIDsByNames(ctx, tax.Slug, refs); err != nil {
						return err
					}
				} else {
					for _, ref := range refs {
						t, err := contentFindTerm(ctx, s.store, tax.Slug, ref, by)
						if err != nil {
							return err
						}
						ids = append(ids, t.ID)
					}
				}
				cur, err := s.store.TermsForPosts(ctx, []int64{p.ID}, tax.Slug)
				if err != nil {
					return err
				}
				curIDs := make([]int64, 0, len(cur[p.ID]))
				for _, t := range cur[p.ID] {
					curIDs = append(curIDs, t.ID)
				}
				next := contentMergeTermIDs(verb, curIDs, ids, c.Bool("all"))
				if tax.Slug == "category" && p.Type == "post" && len(next) == 0 {
					if def := int64(s.settings.Int("default_category")); def > 0 {
						next = []int64{def}
					}
				}
				if err := s.store.SetPostTerms(ctx, p.ID, tax.Slug, next); err != nil {
					return err
				}
				s.afterPostSave(ctx, p)
				c.Success("%s terms of post %d: %d %s term(s).", map[string]string{"set": "Set", "add": "Added", "remove": "Removed"}[verb], p.ID, len(next), tax.Slug)
				return nil
			},
		})
	}
}

// contentMergeTermIDs computes a post's new term list for post term set/add/remove, keeping the order.
func contentMergeTermIDs(verb string, cur, ids []int64, all bool) []int64 {
	out := []int64{}
	switch verb {
	case "set":
		for _, id := range ids {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	case "add":
		for _, id := range append(slices.Clone(cur), ids...) {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	case "remove":
		if all {
			return out
		}
		for _, id := range cur {
			if !slices.Contains(ids, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

func registerPostRevisionCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("post revision", "Manage post revisions.")

	r.Add(cli.Command{
		Path:  "post revision list",
		Short: "List a post's revisions, newest first.",
		Long:  "Fields: ID, post_parent, post_author, post_title, post_date, is_autosave, post_excerpt, post_content.",
		Args:  []cli.Arg{{Name: "id", Usage: "The post ID."}},
		Flags: []cli.Flag{{Name: "autosaves", Usage: "Include autosaves.", Bool: true}},
		Examples: []string{
			"post revision list 12",
			"post revision list 12 --fields=ID,post_date --format=csv",
		},
		Run: func(c *cli.Ctx) error {
			s, p, err := contentLoadPost(c, e, 0)
			if err != nil {
				return err
			}
			revs, err := s.store.ListRevisions(c.Context, p.ID, c.Bool("autosaves"))
			if err != nil {
				return err
			}
			loc := s.loc()
			rows := make([]cli.Row, 0, len(revs))
			for _, rv := range revs {
				rows = append(rows, cli.Row{"ID": rv.ID, "post_parent": rv.PostID, "post_author": rv.AuthorID,
					"post_title": rv.Title, "post_date": rv.CreatedAt.In(loc), "is_autosave": rv.IsAutosave,
					"post_excerpt": rv.Excerpt, "post_content": rv.Content})
			}
			return c.Print(rows, cli.View{Fields: []string{"ID", "post_title", "post_author", "post_date", "is_autosave"}, ID: "ID"})
		},
	})

	r.Add(cli.Command{
		Path:     "post revision restore",
		Short:    "Restore a revision's title, excerpt and content into its post.",
		Long:     "The restored content is recorded as a new revision, as in the editor.\n\n" + contentSideEffects,
		Args:     []cli.Arg{{Name: "revision-id", Usage: "The revision ID (see 'post revision list')."}},
		Examples: []string{"post revision restore 345"},
		Run: func(c *cli.Ctx) error {
			id, err := c.ArgInt64(0)
			if err != nil {
				return err
			}
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			rv, err := s.store.GetRevision(c.Context, id)
			if err != nil {
				return cli.Errorf("Could not find the revision with ID %d.", id)
			}
			actor, err := e.Actor(c)
			if err != nil {
				return err
			}
			p, err := s.store.RestoreRevision(c.Context, rv, actor.ID)
			if err != nil {
				return err
			}
			s.afterPostSave(c.Context, p)
			c.Success("Restored revision %d into post %d.", rv.ID, p.ID)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "post revision prune",
		Short: "Delete old revisions of every post, keeping the newest N per post.",
		Long: `Like the Clean up revisions maintenance run: autosaves are left alone and the newest revision
of each post always stays. Asks for confirmation (pass --yes in scripts).`,
		Flags:    []cli.Flag{{Name: "keep", Usage: "Revisions to keep per post (default: PURSAE_POST_REVISIONS, else 25)."}},
		Examples: []string{"post revision prune --keep=5 --yes"},
		Run: func(c *cli.Ctx) error {
			keep, err := c.Int("keep", store.RevisionsLimit())
			if err != nil {
				return err
			}
			if keep < 0 {
				return cli.Usagef("--keep must be 0 or more (PURSAE_POST_REVISIONS is negative: pass --keep)")
			}
			st, err := e.Store(c.Context)
			if err != nil {
				return err
			}
			total, excess, err := st.RevisionStats(c.Context, keep)
			if err != nil {
				return err
			}
			if excess == 0 {
				c.Success("Nothing to prune (%d revisions, keeping %d per post).", total, max(keep, 1))
				return nil
			}
			if err := c.Confirm("Delete %d of %d revisions (keeping %d per post)?", excess, total, max(keep, 1)); err != nil {
				return err
			}
			n, err := st.PruneRevisions(c.Context, keep)
			if err != nil {
				return err
			}
			c.Success("Deleted %d revisions.", n)
			return nil
		},
	})
}
