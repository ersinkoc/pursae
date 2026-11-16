package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/cli"
	"wpgo/internal/store"
)

// registerContentCLI registers the content commands. See cli.go for the environment (cliEnv) and conventions.
//
// Nouns: post (+ post meta, post term, post revision), post-type, taxonomy, term, comment.
//
// Every write runs the same chain as the admin screens and REST: afterPostSave after a post write
// (menus auto-add, Golylang, Pursae SEO redirects and IndexNow, Web Builder, page cache purge through
// wpgo_cache_purge, Pursae CDN HTML purge), purgeTerm after term changes, the post/feed/widgets
// purge after comment changes. Wasm plugin hooks do not fire (the CLI starts no plugin runtime),
// which is wp-cli's --skip-plugins.
func registerContentCLI(r *cli.Registry, e *cliEnv) {
	registerPostCLI(r, e)
	registerPostMetaCLI(r, e)
	registerPostTermCLI(r, e)
	registerPostRevisionCLI(r, e)
	registerTaxonomyCLI(r, e)
	registerTermCLI(r, e)
	registerCommentCLI(r, e)
}

// contentSideEffects is appended to the Long help of every content write.
const contentSideEffects = `Side effects are those of the admin screens: the page cache of running servers is purged
(wpgo_cache_purge), published top-level pages join "auto add" menus, Pursae SEO records
slug-change redirects and pings IndexNow, Pursae Web Builder and Pursae CDN purges run.
Wasm plugin hooks (save_post, transition_post_status ...) do not fire: the command line starts
no plugin runtime, like wp-cli --skip-plugins.`

// contentPostFields are the columns `post list` and `post get` know (WP-CLI names).
var contentPostFields = []string{"ID", "post_title", "post_name", "post_type", "post_status", "post_date", "post_date_gmt",
	"post_modified", "post_modified_gmt", "post_author", "post_parent", "menu_order", "comment_status", "ping_status",
	"comment_count", "post_password", "post_format", "sticky", "featured_image", "post_excerpt", "post_content", "url"}

var contentPostListDefault = []string{"ID", "post_title", "post_name", "post_date", "post_status"}

// postWriteFlags are the fields post create and post update accept.
func postWriteFlags(create bool) []cli.Flag {
	fs := []cli.Flag{
		{Name: "post_title", Usage: "The post title."},
		{Name: "post_content", Usage: "The post content (or pass a <file>, - for stdin)."},
		{Name: "post_excerpt", Usage: "The post excerpt."},
		{Name: "post_status", Usage: "The post status.", Values: []string{"publish", "future", "draft", "pending", "private"}},
		{Name: "post_name", Usage: "The post slug."},
		{Name: "post_author", Usage: "The author: user ID, login or e-mail (default on create: the acting --user)."},
		{Name: "post_parent", Usage: "The parent post ID (0 for none)."},
		{Name: "menu_order", Usage: "The menu order."},
		{Name: "post_date", Usage: "The publish date in the site time zone (YYYY-MM-DD[ HH:MM:SS]) or RFC 3339; empty: floating."},
		{Name: "post_password", Usage: "The password protecting the post (empty removes it)."},
		{Name: "comment_status", Usage: "Whether comments are open.", Values: []string{"open", "closed"}},
		{Name: "ping_status", Usage: "Whether pings are open.", Values: []string{"open", "closed"}},
		{Name: "post_format", Usage: "The post format (standard, aside, chat, gallery, link, image, quote, status, video, audio)."},
		{Name: "page_template", Usage: "The page template (pages; 'default' removes it)."},
		{Name: "featured_image", Usage: "The featured image's media ID (0 removes it)."},
		{Name: "sticky", Usage: "Stick the post to the front page (posts only).", Bool: true},
		{Name: "post_category", Usage: "Categories: comma-separated IDs or slugs (replaces the current ones)."},
		{Name: "tags_input", Usage: "Tags: comma-separated names; missing tags are created (replaces the current ones)."},
		{Name: "meta_input", Usage: "A JSON object of meta keys and values, merged into the post's meta."},
		{Name: "porcelain", Usage: "Print only the post ID.", Bool: true},
	}
	if create {
		fs = append([]cli.Flag{{Name: "post_type", Usage: "The post type.", Default: "post"}}, fs...)
	} else {
		fs = append(fs, cli.Flag{Name: "defer-term-counting", Hidden: true, Bool: true})
	}
	return fs
}

func registerPostCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("post", "Manage posts, pages and custom post types.")

	r.Add(cli.Command{
		Path:  "post list",
		Short: "List posts.",
		Long: `Lists posts of every status but trash and auto-draft, newest first. --post_type=any lists
every type. Media items are not posts here: use 'media list'.

Fields: ` + strings.Join(contentPostFields, ", ") + `.`,
		Flags: []cli.Flag{
			{Name: "post_type", Usage: "Post type(s), comma-separated, or 'any'.", Default: "post"},
			{Name: "post_status", Usage: "Status(es), comma-separated, or 'any' (every status but trash and auto-draft).", Default: "any"},
			{Name: "author", Usage: "Author(s): user ID, login or e-mail, comma-separated."},
			{Name: "s", Usage: "Search the title and content."},
			{Name: "category", Usage: "Category IDs or slugs, comma-separated (posts in any of them)."},
			{Name: "tag", Usage: "Tag IDs or slugs, comma-separated (posts in any of them)."},
			{Name: "post_parent", Usage: "Only children of this post ID (0: top level)."},
			{Name: "post__in", Usage: "Only these post IDs, comma-separated."},
			{Name: "year", Usage: "Published in this year (site time zone)."},
			{Name: "monthnum", Usage: "Published in this month (1-12)."},
			{Name: "day", Usage: "Published on this day of the month."},
			{Name: "orderby", Usage: "Sort field.", Default: "date", Values: []string{"date", "title", "modified", "menu_order", "id", "ID", "comment_count", "author"}},
			{Name: "order", Usage: "Sort direction.", Default: "DESC", Values: []string{"ASC", "DESC", "asc", "desc"}},
			{Name: "posts_per_page", Usage: "Page size; -1 lists every match.", Default: "-1"},
			{Name: "paged", Usage: "Page number (with --posts_per_page).", Default: "1"},
		},
		Examples: []string{
			"post list --post_type=page --fields=ID,post_title,url",
			"post list --post_status=draft --format=ids",
			"post list --category=news --posts_per_page=10 --format=json",
			"post list --post_type=any --format=count",
		},
		Run: func(c *cli.Ctx) error { return contentPostList(c, e) },
	})

	r.Add(cli.Command{
		Path:     "post get",
		Short:    "Get details about a post.",
		Long:     "Fields: " + strings.Join(contentPostFields, ", ") + ", categories, tags.",
		Args:     []cli.Arg{{Name: "id", Usage: "The post ID."}},
		Examples: []string{"post get 12", "post get 12 --field=post_content", "post get 12 --format=json"},
		Run: func(c *cli.Ctx) error {
			s, p, err := contentLoadPost(c, e, 0)
			if err != nil {
				return err
			}
			row := s.contentPostRow(c.Context, p, true)
			terms, err := s.store.TermsForPosts(c.Context, []int64{p.ID}, "")
			if err != nil {
				return err
			}
			cats, tags := []string{}, []string{}
			for _, t := range terms[p.ID] {
				switch t.Taxonomy {
				case "category":
					cats = append(cats, t.Slug)
				case "post_tag":
					tags = append(tags, t.Name)
				}
			}
			row["categories"], row["tags"] = cats, tags
			fields := append(slices.Clone(contentPostFields), "categories", "tags")
			return c.PrintItem(row, fields)
		},
	})

	r.Add(cli.Command{
		Path:     "post exists",
		Short:    "Verify whether a post exists (exit code 0 or 1).",
		Args:     []cli.Arg{{Name: "id", Usage: "The post ID."}},
		Examples: []string{"post exists 12"},
		Run: func(c *cli.Ctx) error {
			id, err := c.ArgInt64(0)
			if err != nil {
				return err
			}
			st, err := e.Store(c.Context)
			if err != nil {
				return err
			}
			if _, err := st.GetPost(c.Context, id); errors.Is(err, store.ErrNotFound) {
				return cli.Exit(cli.ExitFailure)
			} else if err != nil {
				return err
			}
			c.Success("Post with ID %d exists.", id)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "post create",
		Short: "Create a new post.",
		Long: `Creates a post the way the REST API does: a draft unless --post_status says otherwise, the
site's default comment and ping status, the default category for posts. The acting --user is
the author unless --post_author is given. Content is stored as given (no HTML filtering: the
command line acts as an administrator, like wp-cli).

` + contentSideEffects,
		Args:  []cli.Arg{{Name: "file", Usage: "Read the content from this file ('-' for stdin).", Optional: true}},
		Flags: postWriteFlags(true),
		Examples: []string{
			"post create --post_title='Hello' --post_status=publish",
			"post create ./post.html --post_type=page --post_title='About' --porcelain",
			`post create --post_title=Draft --tags_input='go,cms' --meta_input='{"color":"blue"}'`,
		},
		Run: func(c *cli.Ctx) error { return contentPostSave(c, e, 0) },
	})

	r.Add(cli.Command{
		Path:  "post update",
		Short: "Update one or more existing posts.",
		Long: `Changes only the fields given. A change of title, content or excerpt records a revision;
a slug change is remembered for old-slug redirects.

` + contentSideEffects,
		Args:  []cli.Arg{{Name: "id", Usage: "Post ID(s); a final non-numeric argument is a content file ('-' for stdin).", Repeat: true}},
		Flags: postWriteFlags(false),
		Examples: []string{
			"post update 12 --post_title='New title'",
			"post update 12 13 14 --comment_status=closed",
			"post update 12 ./content.html",
		},
		Run: func(c *cli.Ctx) error {
			args := c.Args
			file := ""
			if n := len(args); n > 1 {
				if _, err := strconv.ParseInt(args[n-1], 10, 64); err != nil {
					file, args = args[n-1], args[:n-1]
				}
			}
			ids := make([]int64, 0, len(args))
			for _, a := range args {
				id, err := cli.ParseID(a)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			// The content file is read once: stdin ("-") cannot be read again for the next ID.
			body, err := contentReadFile(c, file)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err := contentPostSaveFile(c, e, id, body); err != nil {
					return err
				}
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "post delete",
		Short: "Delete posts (to the trash, or permanently with --force).",
		Long: `Without --force posts go to the trash; posts already in the trash, and every post with
--force, are deleted permanently (asks for confirmation; pass --yes in scripts). A permanent
delete moves children up to the deleted post's parent and recounts its terms.

` + contentSideEffects,
		Args:     []cli.Arg{{Name: "id", Usage: "Post ID(s).", Repeat: true}},
		Flags:    []cli.Flag{{Name: "force", Usage: "Skip the trash.", Bool: true}, {Name: "defer-term-counting", Hidden: true, Bool: true}},
		Examples: []string{"post delete 12", "post delete 12 13 --force --yes", "post delete $(pursae post list --post_status=trash --format=ids) --yes"},
		Run:      func(c *cli.Ctx) error { return contentPostDelete(c, e) },
	})

	r.Add(cli.Command{
		Path:     "post untrash",
		Short:    "Restore posts from the trash (as drafts, like WordPress 5.6+).",
		Long:     contentSideEffects,
		Args:     []cli.Arg{{Name: "id", Usage: "Post ID(s).", Repeat: true}},
		Examples: []string{"post untrash 12"},
		Run: func(c *cli.Ctx) error {
			ids, err := c.ArgIDs(0)
			if err != nil {
				return err
			}
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			failed := 0
			for _, id := range ids {
				p, err := s.store.GetPost(c.Context, id)
				if err != nil {
					c.Warning("Could not find the post with ID %d.", id)
					failed++
					continue
				}
				if p.Status != "trash" {
					c.Warning("Post %d is not in the trash.", id)
					failed++
					continue
				}
				s.afterPostSave(c.Context, p)
				if err := s.store.UntrashPost(c.Context, id); err != nil {
					return err
				}
				s.pluginPostMoved(c.Context, "untrash", p)
				s.purgePostAgain(c.Context, p)
				s.wbAfterStatusChange(c.Context, p)
				c.Success("Restored post %d (as a draft).", id)
			}
			if failed > 0 {
				return cli.Exit(cli.ExitFailure)
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "post empty-trash",
		Short: "Permanently delete every post in the trash.",
		Long:  "Like the Empty Trash button of the posts screen. Asks for confirmation; pass --yes in scripts.\n\n" + contentSideEffects,
		Flags: []cli.Flag{{Name: "post_type", Usage: "Post type(s), comma-separated, or 'any'.", Default: "post"}},
		Examples: []string{
			"post empty-trash --yes",
			"post empty-trash --post_type=page,post --yes",
		},
		Run: func(c *cli.Ctx) error {
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			types := cli.SplitList(c.Flag("post_type"))
			if slices.Contains(types, "any") {
				types = nil
			}
			rows, err := s.store.Pool.Query(c.Context, `SELECT id FROM posts WHERE status = 'trash'
				AND (cardinality($1::text[]) = 0 OR type = ANY($1)) ORDER BY id`, types)
			if err != nil {
				return err
			}
			var ids []int64
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if len(ids) == 0 {
				c.Success("The trash is empty.")
				return nil
			}
			if err := c.Confirm("Permanently delete %d trashed post(s)?", len(ids)); err != nil {
				return err
			}
			n := 0
			for _, id := range ids {
				p, err := s.store.GetPost(c.Context, id)
				if err != nil || p.Status != "trash" {
					continue
				}
				if err := s.contentDeletePost(c.Context, p, true); err != nil {
					c.Warning("post %d: %v", id, err)
					continue
				}
				n++
			}
			c.Success("Deleted %d of %d trashed post(s).", n, len(ids))
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "post duplicate",
		Short: "Copy a post into a new draft.",
		Long: `Copies content, excerpt, parent, order, format, discussion settings, featured image, meta,
terms, the Pursae Web Builder document and the language (in a translation group of its own),
like the Duplicate row action. The copy is titled "<title> (Copy)" and authored by the acting --user.

` + contentSideEffects,
		Args: []cli.Arg{{Name: "id", Usage: "The post ID."}},
		Flags: []cli.Flag{
			{Name: "post_title", Usage: "Title of the copy (default: '<title> (Copy)')."},
			{Name: "porcelain", Usage: "Print only the new post ID.", Bool: true},
		},
		Examples: []string{"post duplicate 12", "post duplicate 12 --post_title='Landing B' --porcelain"},
		Run: func(c *cli.Ctx) error {
			s, p, err := contentLoadPost(c, e, 0)
			if err != nil {
				return err
			}
			actor, err := e.Actor(c)
			if err != nil {
				return err
			}
			title := p.Title
			if title != "" {
				title += " (Copy)"
			}
			if v, ok := c.Lookup("post_title"); ok {
				title = v
			}
			cp, err := s.store.DuplicatePost(c.Context, p, actor.ID, title)
			if err != nil {
				return err
			}
			s.afterPostSave(c.Context, cp)
			if c.Bool("porcelain") {
				c.Println(cp.ID)
				return nil
			}
			c.Success("Created post %d (a copy of %d).", cp.ID, p.ID)
			return nil
		},
	})
}

// contentLoadPost loads the post whose ID is positional argument i.
func contentLoadPost(c *cli.Ctx, e *cliEnv, i int) (*Server, *store.Post, error) {
	id, err := c.ArgInt64(i)
	if err != nil {
		return nil, nil, err
	}
	s, err := e.Server(c.Context)
	if err != nil {
		return nil, nil, err
	}
	p, err := contentGetPost(c.Context, s.store, id)
	return s, p, err
}

// contentGetPost is GetPost with WP-CLI's not-found message.
func contentGetPost(ctx context.Context, st *store.Store, id int64) (*store.Post, error) {
	p, err := st.GetPost(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, cli.Errorf("Could not find the post with ID %d.", id)
	}
	return p, err
}

// contentPostRow is a post as a WP-CLI record. The url costs queries (ancestors, categories), so it is
// only computed when withURL.
func (s *Server) contentPostRow(ctx context.Context, p *store.Post, withURL bool) cli.Row {
	loc := s.loc()
	d := p.Date()
	r := cli.Row{
		"ID": p.ID, "post_title": p.Title, "post_name": p.Slug, "post_type": p.Type, "post_status": p.Status,
		"post_date": d.In(loc), "post_date_gmt": d.UTC(), "post_modified": p.ModifiedAt.In(loc), "post_modified_gmt": p.ModifiedAt.UTC(),
		"post_author": p.AuthorID, "post_parent": p.ParentID, "menu_order": p.MenuOrder,
		"comment_status": p.CommentStatus, "ping_status": p.PingStatus, "comment_count": p.CommentCount,
		"post_password": p.Password, "post_format": store.CanonicalPostFormat(p.Format), "sticky": p.Sticky,
		"featured_image": p.FeaturedMediaID, "post_excerpt": p.Excerpt, "post_content": p.Content,
	}
	if withURL {
		r["url"] = s.links(ctx).Permalink(p)
	}
	return r
}

// contentWantsField reports whether the output will show field f (--field, --fields or the defaults).
func contentWantsField(c *cli.Ctx, defaults []string, f string) bool {
	if c.Field != "" {
		return strings.EqualFold(c.Field, f)
	}
	fields := c.Fields
	if len(fields) == 0 {
		fields = defaults
	}
	for _, x := range fields {
		if strings.EqualFold(x, f) {
			return true
		}
	}
	return false
}

// contentPostTypes parses --post_type: nil for "any".
func contentPostTypes(v string) ([]string, error) {
	types := cli.SplitList(v)
	if len(types) == 0 || slices.Contains(types, "any") {
		return nil, nil
	}
	if slices.Contains(types, "attachment") {
		return nil, cli.Usagef("media items are not posts in Pursae (separate table and IDs): use 'media list'")
	}
	return types, nil
}

// contentPostStatuses parses --post_status: nil for "any" (every status but trash and auto-draft).
func contentPostStatuses(v string) []string {
	st := cli.SplitList(v)
	if len(st) == 0 || slices.Contains(st, "any") {
		return nil
	}
	return st
}

func contentPostList(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	types, err := contentPostTypes(c.Flag("post_type"))
	if err != nil {
		return err
	}
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	q := store.PostQuery{Types: types, Statuses: contentPostStatuses(c.Flag("post_status")), Search: c.Flag("s"),
		OrderBy: strings.ToLower(c.Flag("orderby")), Order: strings.ToUpper(c.Flag("order")), Loc: s.loc(),
		NoContent: !contentWantsField(c, contentPostListDefault, "post_content") && !contentWantsField(c, contentPostListDefault, "post_excerpt")}
	for _, ref := range c.List("author") {
		u, err := e.User(ctx, ref)
		if err != nil {
			return err
		}
		q.AuthorIDs = append(q.AuthorIDs, u.ID)
	}
	for _, tf := range []struct{ flag, tax string }{{"category", "category"}, {"tag", "post_tag"}} {
		refs := c.List(tf.flag)
		if len(refs) == 0 {
			continue
		}
		var ids []int64
		for _, ref := range refs {
			t, err := contentFindTerm(ctx, s.store, tf.tax, ref, "")
			if err != nil {
				return err
			}
			ids = append(ids, t.ID)
		}
		q.TermClauses = append(q.TermClauses, store.TermClause{IDs: ids})
	}
	if v, ok := c.Lookup("post_parent"); ok {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || n < 0 {
			return cli.Usagef("--post_parent expects a post ID or 0")
		}
		q.ParentID = &n
	}
	for _, v := range c.List("post__in") {
		id, err := cli.ParseID(v)
		if err != nil {
			return err
		}
		q.IDs = append(q.IDs, id)
	}
	if q.Year, err = c.Int("year", 0); err != nil {
		return err
	}
	if q.Month, err = c.Int("monthnum", 0); err != nil {
		return err
	}
	if q.Day, err = c.Int("day", 0); err != nil {
		return err
	}
	if q.PerPage, err = c.Int("posts_per_page", -1); err != nil {
		return err
	}
	if q.PerPage == 0 {
		q.PerPage = -1
	}
	if q.Page, err = c.Int("paged", 1); err != nil {
		return err
	}
	posts, total, err := s.store.QueryPosts(ctx, q)
	if err != nil {
		return err
	}
	if c.Format == "count" && c.Field == "" {
		c.Println(total)
		return nil
	}
	withURL := contentWantsField(c, contentPostListDefault, "url")
	rows := make([]cli.Row, 0, len(posts))
	for _, p := range posts {
		rows = append(rows, s.contentPostRow(ctx, p, withURL))
	}
	return c.Print(rows, cli.View{Fields: contentPostListDefault, ID: "ID"})
}

// contentPostSave creates (id 0) or updates a post, the content file being positional argument 0 on create.
func contentPostSave(c *cli.Ctx, e *cliEnv, id int64) error {
	body, err := contentReadFile(c, c.Arg(0))
	if err != nil {
		return err
	}
	return contentPostSaveFile(c, e, id, body)
}

// contentReadFile reads a content file argument ("-" is stdin); nil when file is "".
func contentReadFile(c *cli.Ctx, file string) (*string, error) {
	if file == "" {
		return nil, nil
	}
	if c.Has("post_content") {
		return nil, cli.Usagef("pass either a content file or --post_content, not both")
	}
	b, err := c.ReadInput(file, os.ReadFile)
	if err != nil {
		return nil, cli.Errorf("reading %s: %v", file, err)
	}
	s := string(b)
	return &s, nil
}

// contentPostSaveFile mirrors the REST savePost (rest_posts.go) for the command line. body is
// the content read from a file argument (nil: none given).
func contentPostSaveFile(c *cli.Ctx, e *cliEnv, id int64, body *string) error {
	ctx := c.Context
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	actor, err := e.Actor(c)
	if err != nil {
		return err
	}
	isNew := id == 0
	var p *store.Post
	if isNew {
		typ := strings.TrimSpace(c.Flag("post_type"))
		if typ == "attachment" {
			return cli.Usagef("media items are not posts in Pursae: use 'media import'")
		}
		if ok, err := contentPostTypeExists(ctx, s, typ); err != nil {
			return err
		} else if !ok {
			return cli.Errorf("Invalid post type: '%s' (see '%s post-type list').", typ, c.Prog)
		}
		p = &store.Post{Type: typ, AuthorID: actor.ID, Status: "draft", Meta: map[string]any{},
			CommentStatus: s.settings.String("default_comment_status"), PingStatus: s.settings.String("default_ping_status")}
	} else {
		if body == nil && !contentHasAnyFlag(c, postWriteFlags(false), "porcelain", "defer-term-counting") {
			return cli.Usagef("need some fields to update")
		}
		if p, err = contentGetPost(ctx, s.store, id); err != nil {
			return err
		}
		if p.Meta == nil {
			p.Meta = map[string]any{}
		}
	}
	wasPublished := p.Status == "publish" || p.Status == "private" || p.Status == "future"

	if body != nil {
		p.Content = *body
	}
	if v, ok := c.Lookup("post_title"); ok {
		p.Title = v
	}
	if v, ok := c.Lookup("post_content"); ok {
		p.Content = v
	}
	if v, ok := c.Lookup("post_excerpt"); ok {
		p.Excerpt = v
	}
	if v, ok := c.Lookup("post_name"); ok {
		p.Slug = v
	}
	if v, ok := c.Lookup("post_password"); ok {
		p.Password = v
	}
	if v, ok := c.Lookup("comment_status"); ok {
		p.CommentStatus = v
	}
	if v, ok := c.Lookup("ping_status"); ok {
		p.PingStatus = v
	}
	if v, ok := c.Lookup("post_format"); ok && v != "" {
		if store.CanonicalPostFormat(v) != v {
			return cli.Usagef("invalid --post_format %q", v)
		}
		p.Format = v
	}
	if c.Has("sticky") {
		if p.Type != "post" {
			return cli.Usagef("--sticky is for posts only")
		}
		p.Sticky = c.Bool("sticky")
	}
	if c.Has("featured_image") {
		mid, err := c.Int64("featured_image", 0)
		if err != nil {
			return err
		}
		if mid > 0 {
			if _, err := s.store.GetAttachment(ctx, mid); err != nil {
				return cli.Errorf("Could not find the media item with ID %d.", mid)
			}
		}
		p.FeaturedMediaID = max(mid, 0)
	}
	if c.Has("post_parent") {
		pid, err := c.Int64("post_parent", 0)
		if err != nil {
			return err
		}
		if pid > 0 {
			if _, err := contentGetPost(ctx, s.store, pid); err != nil {
				return err
			}
		}
		p.ParentID = max(pid, 0)
	}
	if c.Has("menu_order") {
		n, err := c.Int("menu_order", 0)
		if err != nil {
			return err
		}
		p.MenuOrder = n
	}
	if v, ok := c.Lookup("post_author"); ok {
		u, err := e.User(ctx, v)
		if err != nil {
			return err
		}
		p.AuthorID = u.ID
	}
	if v, ok := c.Lookup("meta_input"); ok {
		m, err := contentParseMetaInput(v)
		if err != nil {
			return err
		}
		for k, val := range m {
			if strings.HasPrefix(k, "_") {
				c.Warning("meta key %q is protected (the admin's Custom Fields box hides it).", k)
			}
			p.Meta[k] = val
		}
	}
	if v, ok := c.Lookup("page_template"); ok {
		if p.Type != "page" {
			return cli.Usagef("--page_template is for pages only")
		}
		cur, _ := p.Meta["_wp_page_template"].(string)
		if v != "" && v != "default" && v != cur && !s.isPageTemplate(v) {
			c.Warning("the active theme does not list the page template %q (package themes are not loaded by the command line).", v)
		}
		setPageTemplate(p, v)
	}
	if v, ok := c.Lookup("post_date"); ok {
		v = strings.TrimSpace(v)
		if v == "" {
			if !wasPublished {
				p.PublishedAt = nil
			}
		} else {
			t, ok := contentParsePostDate(v, s.loc())
			if !ok {
				return cli.Usagef("invalid --post_date %q (YYYY-MM-DD[ HH:MM:SS] or RFC 3339)", v)
			}
			p.PublishedAt = &t
		}
	}
	if v, ok := c.Lookup("post_status"); ok && v != "" {
		p.Status = v
	}
	if p.Status == "auto-draft" {
		p.Status = "draft"
	}

	// Terms: resolve before writing so a bad reference changes nothing.
	var catIDs, tagIDs []int64
	setCats, setTags := c.Has("post_category"), c.Has("tags_input")
	if setCats || setTags {
		for _, tf := range []struct {
			on   bool
			tax  string
			flag string
		}{{setCats, "category", "post_category"}, {setTags, "post_tag", "tags_input"}} {
			if !tf.on {
				continue
			}
			tx, err := contentTaxonomy(ctx, s, tf.tax)
			if err != nil {
				return err
			}
			if !slices.Contains(tx.ObjectTypes, p.Type) {
				return cli.Usagef("--%s: the %s taxonomy is not registered for %s", tf.flag, tf.tax, p.Type)
			}
		}
		if setCats {
			for _, ref := range c.List("post_category") {
				t, err := contentFindTerm(ctx, s.store, "category", ref, "")
				if err != nil {
					return err
				}
				catIDs = append(catIDs, t.ID)
			}
		}
	}

	if isNew {
		err = s.store.InsertPost(ctx, p)
	} else {
		err = s.store.UpdatePost(ctx, p, actor.ID)
	}
	if err != nil {
		return cli.Errorf("Could not save the post: %v", err)
	}
	if setCats || (isNew && p.Type == "post") {
		if len(catIDs) == 0 && p.Type == "post" {
			if def := int64(s.settings.Int("default_category")); def > 0 {
				catIDs = []int64{def}
			}
		}
		if err := s.store.SetPostTerms(ctx, p.ID, "category", catIDs); err != nil {
			return err
		}
	}
	if setTags {
		if tagIDs, err = s.store.TermIDsByNames(ctx, "post_tag", c.List("tags_input")); err != nil {
			return err
		}
		if err := s.store.SetPostTerms(ctx, p.ID, "post_tag", tagIDs); err != nil {
			return err
		}
	}
	// Drop the acting user's autosave once the post itself is saved (as REST does).
	_, _ = s.store.Pool.Exec(ctx, `DELETE FROM post_revisions WHERE post_id = $1 AND is_autosave AND author_id = $2`, p.ID, actor.ID)
	s.afterPostSave(ctx, p)
	if c.Bool("porcelain") {
		c.Println(p.ID)
		return nil
	}
	if isNew {
		c.Success("Created post %d.", p.ID)
	} else {
		c.Success("Updated post %d.", p.ID)
	}
	return nil
}

// contentHasAnyFlag reports whether one of flags (other than skip) was given.
func contentHasAnyFlag(c *cli.Ctx, flags []cli.Flag, skip ...string) bool {
	for _, f := range flags {
		if !slices.Contains(skip, f.Name) && c.Has(f.Name) {
			return true
		}
	}
	return false
}

// contentParsePostDate accepts YYYY-MM-DD, YYYY-MM-DD HH:MM[:SS] (site time zone) and RFC 3339.
func contentParsePostDate(v string, loc *time.Location) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if len(v) == 10 {
		v += "T00:00:00"
	} else if len(v) == 16 {
		v += ":00"
	}
	return restParseDate(v, loc)
}

// contentParseMetaInput parses --meta_input: a JSON object.
func contentParseMetaInput(v string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(v), &m); err != nil || m == nil {
		return nil, cli.Usagef("--meta_input expects a JSON object, e.g. '{\"key\":\"value\"}'")
	}
	return m, nil
}

func contentPostDelete(c *cli.Ctx, e *cliEnv) error {
	ids, err := c.ArgIDs(0)
	if err != nil {
		return err
	}
	s, err := e.Server(c.Context)
	if err != nil {
		return err
	}
	force := c.Bool("force")
	posts := make([]*store.Post, 0, len(ids))
	permanent := 0
	failed := 0
	for _, id := range ids {
		p, err := contentGetPost(c.Context, s.store, id)
		if err != nil {
			c.Warning("%v", err)
			failed++
			continue
		}
		posts = append(posts, p)
		if force || p.Status == "trash" {
			permanent++
		}
	}
	if permanent > 0 {
		if err := c.Confirm("Permanently delete %d post(s)? This cannot be undone.", permanent); err != nil {
			return err
		}
	}
	for _, p := range posts {
		perm := force || p.Status == "trash"
		if err := s.contentDeletePost(c.Context, p, perm); err != nil {
			c.Warning("post %d: %v", p.ID, err)
			failed++
			continue
		}
		if perm {
			c.Success("Deleted post %d.", p.ID)
		} else {
			c.Success("Trashed post %d.", p.ID)
		}
	}
	if failed > 0 {
		return cli.Exit(cli.ExitFailure)
	}
	return nil
}

// contentDeletePost trashes or deletes p in the order postsBulkAction uses: purge while the terms
// still exist, the store change, then the plugin transition, a second purge and Web Builder.
func (s *Server) contentDeletePost(ctx context.Context, p *store.Post, permanent bool) error {
	s.afterPostSave(ctx, p)
	action := "trash"
	var err error
	if permanent {
		action = "delete"
		s.pluginPostAction(ctx, "before_delete_post", p)
		err = s.store.DeletePost(ctx, p.ID)
	} else {
		err = s.store.TrashPost(ctx, p.ID)
	}
	if err != nil {
		return err
	}
	s.pluginPostMoved(ctx, action, p)
	s.purgePostAgain(ctx, p)
	s.wbAfterStatusChange(ctx, p)
	return nil
}
