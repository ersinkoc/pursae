package server

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/cli"
	"wpgo/internal/store"
)

// contentTax is a row of the taxonomies table. The command line validates taxonomies against the
// table, not acfgoRegistry: ACF-Go registers its types in the running server's memory only, and
// the server persists them here.
type contentTax struct {
	Slug         string
	Name         string
	Hierarchical bool
	Public       bool
	Builtin      bool
	ObjectTypes  []string
}

func (t *contentTax) row() cli.Row {
	return cli.Row{"name": t.Slug, "label": t.Name, "object_type": t.ObjectTypes, "hierarchical": t.Hierarchical,
		"public": t.Public, "is_builtin": t.Builtin}
}

const contentTaxCols = `slug, name, hierarchical, public, is_builtin, object_types`

func scanCLITax(row pgx.Row) (*contentTax, error) {
	t := &contentTax{}
	var types []byte
	if err := row.Scan(&t.Slug, &t.Name, &t.Hierarchical, &t.Public, &t.Builtin, &types); err != nil {
		return nil, err
	}
	t.ObjectTypes = contentDecodeObjectTypes(types)
	return t, nil
}

// contentDecodeObjectTypes decodes taxonomies.object_types (a JSON array of post type slugs).
func contentDecodeObjectTypes(b []byte) []string {
	var out []string
	if json.Unmarshal(b, &out) != nil || out == nil {
		return []string{}
	}
	return out
}

// contentTaxonomy loads a taxonomy, or fails with WP-CLI's "Invalid taxonomy" error.
func contentTaxonomy(ctx context.Context, s *Server, slug string) (*contentTax, error) {
	t, err := scanCLITax(s.store.Pool.QueryRow(ctx, `SELECT `+contentTaxCols+` FROM taxonomies WHERE slug = $1`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, cli.Errorf("Invalid taxonomy: '%s'.", slug)
	}
	return t, err
}

// contentPostTypeExists reports whether typ is a row of content_types.
func contentPostTypeExists(ctx context.Context, s *Server, typ string) (bool, error) {
	var ok bool
	err := s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM content_types WHERE slug = $1)`, typ).Scan(&ok)
	return ok, err
}

// contentTermBy decides how a term reference is looked up: by as given, else an all-digit reference
// is an ID and anything else a slug (then a name).
func contentTermBy(ref, by string) string {
	if by != "" {
		return by
	}
	if _, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return "id"
	}
	return "auto"
}

// contentFindTerm resolves a term of taxonomy by ID, slug or name (see contentTermBy).
func contentFindTerm(ctx context.Context, st *store.Store, taxonomy, ref, by string) (*store.Term, error) {
	ref = strings.TrimSpace(ref)
	var t *store.Term
	var err error
	switch contentTermBy(ref, by) {
	case "id":
		id, perr := strconv.ParseInt(ref, 10, 64)
		if perr != nil || id <= 0 {
			return nil, cli.Usagef("%q is not a valid term ID", ref)
		}
		t, err = st.GetTerm(ctx, id)
		if err == nil && t.Taxonomy != taxonomy {
			err = store.ErrNotFound
		}
	case "slug":
		t, err = st.TermBySlug(ctx, taxonomy, ref)
	case "name":
		t, err = st.TermByName(ctx, taxonomy, ref)
	default:
		t, err = st.TermBySlug(ctx, taxonomy, ref)
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			t, err = st.TermByName(ctx, taxonomy, ref)
		}
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return nil, cli.Errorf("Term '%s' not found in taxonomy '%s'.", ref, taxonomy)
	}
	return t, err
}

var contentTermFields = []string{"term_id", "name", "slug", "description", "parent", "count", "taxonomy", "url"}
var contentTermListDefault = []string{"term_id", "name", "slug", "description", "parent", "count"}

func (s *Server) contentTermRow(ctx context.Context, t *store.Term, withURL bool) cli.Row {
	r := cli.Row{"term_id": t.ID, "name": t.Name, "slug": t.Slug, "description": t.Description, "parent": t.ParentID,
		"count": t.Count, "taxonomy": t.Taxonomy}
	if withURL {
		r["url"] = s.links(ctx).TermLink(t)
	}
	return r
}

var contentTermByFlag = cli.Flag{Name: "by", Usage: "How to read <term>: id, slug or name (default: an ID when numeric, else slug, then name).", Values: []string{"id", "slug", "name"}}

func registerTaxonomyCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("post-type", "Inspect post types.")
	r.Add(cli.Command{
		Path:  "post-type list",
		Short: "List the registered post types.",
		Long: `Lists the content_types table: built-in types and the ones Pursae ACF-Go and other modules
register (persisted by the running server). Fields: name, label, hierarchical, public, is_builtin.`,
		Examples: []string{"post-type list", "post-type list --field=name"},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			rows, err := pool.Query(c.Context, `SELECT slug, name, hierarchical, public, is_builtin FROM content_types ORDER BY id`)
			if err != nil {
				return err
			}
			defer rows.Close()
			var out []cli.Row
			for rows.Next() {
				var slug, name string
				var hier, public, builtin bool
				if err := rows.Scan(&slug, &name, &hier, &public, &builtin); err != nil {
					return err
				}
				out = append(out, cli.Row{"name": slug, "label": name, "hierarchical": hier, "public": public, "is_builtin": builtin})
			}
			if err := rows.Err(); err != nil {
				return err
			}
			return c.Print(out, cli.View{Fields: []string{"name", "label", "hierarchical", "public", "is_builtin"}, ID: "name"})
		},
	})

	r.Noun("taxonomy", "Inspect taxonomies.")
	r.Add(cli.Command{
		Path:     "taxonomy list",
		Short:    "List the registered taxonomies.",
		Long:     "Lists the taxonomies table. Fields: name, label, object_type, hierarchical, public, is_builtin.",
		Flags:    []cli.Flag{{Name: "object_type", Usage: "Only taxonomies registered for this post type."}},
		Examples: []string{"taxonomy list", "taxonomy list --object_type=post --format=json"},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			rows, err := pool.Query(c.Context, `SELECT `+contentTaxCols+` FROM taxonomies ORDER BY id`)
			if err != nil {
				return err
			}
			defer rows.Close()
			ot := c.Flag("object_type")
			var out []cli.Row
			for rows.Next() {
				t, err := scanCLITax(rows)
				if err != nil {
					return err
				}
				if ot != "" && !slices.Contains(t.ObjectTypes, ot) {
					continue
				}
				out = append(out, t.row())
			}
			if err := rows.Err(); err != nil {
				return err
			}
			return c.Print(out, cli.View{Fields: []string{"name", "label", "object_type", "hierarchical", "public"}, ID: "name"})
		},
	})
}

func registerTermCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("term", "Manage taxonomy terms (categories, tags, custom taxonomies).")
	taxArg := cli.Arg{Name: "taxonomy", Usage: "The taxonomy (see 'taxonomy list')."}

	r.Add(cli.Command{
		Path:  "term list",
		Short: "List the terms of a taxonomy.",
		Long:  "Fields: " + strings.Join(contentTermFields, ", ") + ".",
		Args:  []cli.Arg{taxArg},
		Flags: []cli.Flag{
			{Name: "search", Usage: "Only terms whose name or slug contains this."},
			{Name: "hide_empty", Usage: "Skip terms without published posts.", Bool: true},
			{Name: "parent", Usage: "Only direct children of this term ID (0: top level)."},
			{Name: "include", Usage: "Only these term IDs, comma-separated."},
			{Name: "orderby", Usage: "Sort field.", Default: "name", Values: []string{"name", "count", "slug", "id", "term_id", "description"}},
			{Name: "order", Usage: "Sort direction.", Default: "ASC", Values: []string{"ASC", "DESC", "asc", "desc"}},
			{Name: "number", Usage: "Page size (0: all).", Default: "0"},
			{Name: "paged", Usage: "Page number (with --number).", Default: "1"},
		},
		Examples: []string{"term list category", "term list post_tag --orderby=count --order=desc --number=10", "term list category --fields=term_id,name,url --format=csv"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			tax, err := contentTaxonomy(ctx, s, c.Arg(0))
			if err != nil {
				return err
			}
			q := store.TermQuery{Taxonomy: tax.Slug, Search: c.Flag("search"), HideEmpty: c.Bool("hide_empty"),
				OrderBy: strings.ToLower(c.Flag("orderby")), Order: c.Flag("order")}
			if q.OrderBy == "term_id" {
				q.OrderBy = "id"
			}
			for _, v := range c.List("include") {
				id, err := cli.ParseID(v)
				if err != nil {
					return err
				}
				q.IDs = append(q.IDs, id)
			}
			number, err := c.Int("number", 0)
			if err != nil {
				return err
			}
			paged, err := c.Int("paged", 1)
			if err != nil {
				return err
			}
			terms, _, err := s.store.QueryTerms(ctx, q)
			if err != nil {
				return err
			}
			if v, ok := c.Lookup("parent"); ok {
				pid, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
				if perr != nil || pid < 0 {
					return cli.Usagef("--parent expects a term ID or 0")
				}
				terms = slices.DeleteFunc(terms, func(t *store.Term) bool { return t.ParentID != pid })
			}
			terms = contentPage(terms, number, paged)
			if c.Format == "count" && c.Field == "" {
				c.Println(len(terms))
				return nil
			}
			withURL := contentWantsField(c, contentTermListDefault, "url")
			rows := make([]cli.Row, 0, len(terms))
			for _, t := range terms {
				rows = append(rows, s.contentTermRow(ctx, t, withURL))
			}
			return c.Print(rows, cli.View{Fields: contentTermListDefault, ID: "term_id"})
		},
	})

	r.Add(cli.Command{
		Path:     "term get",
		Short:    "Get details about a term.",
		Long:     "Fields: " + strings.Join(contentTermFields, ", ") + ".",
		Args:     []cli.Arg{taxArg, {Name: "term", Usage: "Term ID, slug or name (see --by)."}},
		Flags:    []cli.Flag{contentTermByFlag},
		Examples: []string{"term get category 3", "term get post_tag golang --by=slug --format=json"},
		Run: func(c *cli.Ctx) error {
			s, _, t, err := contentLoadTerm(c, e, c.Arg(1))
			if err != nil {
				return err
			}
			return c.PrintItem(s.contentTermRow(c.Context, t, true), contentTermFields)
		},
	})

	r.Add(cli.Command{
		Path:  "term create",
		Short: "Create a term.",
		Long:  "Fails when a term of the same name exists under the same parent. Creating a term purges nothing (as in the admin).",
		Args:  []cli.Arg{taxArg, {Name: "name", Usage: "The term name."}},
		Flags: []cli.Flag{
			{Name: "slug", Usage: "The slug (default: from the name)."},
			{Name: "description", Usage: "The description."},
			{Name: "parent", Usage: "The parent term ID (hierarchical taxonomies)."},
			{Name: "porcelain", Usage: "Print only the term ID.", Bool: true},
		},
		Examples: []string{"term create category News", "term create category 'Go news' --parent=3 --slug=go --porcelain"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			tax, err := contentTaxonomy(ctx, s, c.Arg(0))
			if err != nil {
				return err
			}
			t := &store.Term{Taxonomy: tax.Slug, Name: strings.TrimSpace(c.Arg(1)), Slug: c.Flag("slug"), Description: c.Flag("description")}
			if t.Name == "" {
				return cli.Usagef("a name is required for this term")
			}
			if c.Has("parent") {
				if t.ParentID, err = contentTermParent(c, s, tax); err != nil {
					return err
				}
			}
			if err := s.store.InsertTerm(ctx, t); err != nil {
				if errors.Is(err, store.ErrTermExists) {
					if ex, ferr := s.store.TermByName(ctx, tax.Slug, t.Name); ferr == nil {
						return cli.Errorf("%v (term ID %d)", err, ex.ID)
					}
				}
				return err
			}
			if c.Bool("porcelain") {
				c.Println(t.ID)
				return nil
			}
			c.Success("Created %s %d.", tax.Slug, t.ID)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "term update",
		Short: "Update a term.",
		Long:  "Changes only the fields given, then purges the term's archive and the pages that list it. A changed slug gets a Pursae SEO redirect when automatic redirects are on.",
		Args:  []cli.Arg{taxArg, {Name: "term", Usage: "Term ID, slug or name (see --by)."}},
		Flags: []cli.Flag{
			contentTermByFlag,
			{Name: "name", Usage: "The new name."},
			{Name: "slug", Usage: "The new slug."},
			{Name: "description", Usage: "The new description."},
			{Name: "parent", Usage: "The new parent term ID (0: top level)."},
		},
		Examples: []string{"term update category 3 --name='World news'", "term update post_tag golang --by=slug --slug=go"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, tax, t, err := contentLoadTerm(c, e, c.Arg(1))
			if err != nil {
				return err
			}
			if !c.Has("name") && !c.Has("slug") && !c.Has("description") && !c.Has("parent") {
				return cli.Usagef("need some fields to update")
			}
			old := *t
			if c.Has("name") {
				if t.Name = strings.TrimSpace(c.Flag("name")); t.Name == "" {
					return cli.Usagef("a name is required for this term")
				}
			}
			if c.Has("slug") {
				t.Slug = c.Flag("slug")
			}
			if c.Has("description") {
				t.Description = c.Flag("description")
			}
			if c.Has("parent") {
				if t.ParentID, err = contentTermParent(c, s, tax); err != nil {
					return err
				}
			}
			if err := s.store.UpdateTerm(ctx, t); err != nil {
				return err
			}
			s.seogoTermMoved(ctx, &old, t)
			s.purgeTerm(t, false)
			c.Success("Term %d updated.", t.ID)
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "term delete",
		Short: "Delete terms permanently.",
		Long: `Asks for confirmation (pass --yes in scripts). Children move up to the deleted term's parent;
posts left without a category get the default category, which cannot be deleted.`,
		Args:     []cli.Arg{taxArg, {Name: "term", Usage: "Term ID(s), slug(s) or name(s) (see --by).", Repeat: true}},
		Flags:    []cli.Flag{contentTermByFlag},
		Examples: []string{"term delete post_tag 12 13 --yes", "term delete category old-news --by=slug --yes"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			tax, err := contentTaxonomy(ctx, s, c.Arg(0))
			if err != nil {
				return err
			}
			def := int64(s.settings.Int("default_category"))
			var terms []*store.Term
			failed := 0
			for _, ref := range c.Args[1:] {
				t, err := contentFindTerm(ctx, s.store, tax.Slug, ref, c.Flag("by"))
				if err != nil {
					c.Warning("%v", err)
					failed++
					continue
				}
				if t.Taxonomy == "category" && t.ID == def {
					c.Warning("Cannot delete the default category (%d).", t.ID)
					failed++
					continue
				}
				terms = append(terms, t)
			}
			if len(terms) > 0 {
				if err := c.Confirm("Permanently delete %d %s term(s)?", len(terms), tax.Slug); err != nil {
					return err
				}
			}
			for _, t := range terms {
				if err := s.store.DeleteTerm(ctx, t, def); err != nil {
					c.Warning("term %d: %v", t.ID, err)
					failed++
					continue
				}
				s.purgeTerm(t, true)
				c.Success("Deleted %s %d.", tax.Slug, t.ID)
			}
			if failed > 0 {
				return cli.Exit(cli.ExitFailure)
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:     "term recount",
		Short:    "Recount the published posts of every term of one or more taxonomies.",
		Args:     []cli.Arg{{Name: "taxonomy", Usage: "Taxonomy (or taxonomies).", Repeat: true}},
		Examples: []string{"term recount category post_tag"},
		Run: func(c *cli.Ctx) error {
			ctx := c.Context
			s, err := e.Server(ctx)
			if err != nil {
				return err
			}
			for _, slug := range c.Args {
				tax, err := contentTaxonomy(ctx, s, slug)
				if err != nil {
					return err
				}
				terms, _, err := s.store.QueryTerms(ctx, store.TermQuery{Taxonomy: tax.Slug})
				if err != nil {
					return err
				}
				ids := make([]int64, 0, len(terms))
				for _, t := range terms {
					ids = append(ids, t.ID)
				}
				if err := s.store.RecountTerms(ctx, ids); err != nil {
					return err
				}
				c.Success("Updated %s term count (%d terms).", tax.Slug, len(ids))
			}
			s.purgeTags("widgets")
			return nil
		},
	})
}

// contentLoadTerm resolves the taxonomy (argument 0) and the term ref.
func contentLoadTerm(c *cli.Ctx, e *cliEnv, ref string) (*Server, *contentTax, *store.Term, error) {
	s, err := e.Server(c.Context)
	if err != nil {
		return nil, nil, nil, err
	}
	tax, err := contentTaxonomy(c.Context, s, c.Arg(0))
	if err != nil {
		return nil, nil, nil, err
	}
	t, err := contentFindTerm(c.Context, s.store, tax.Slug, ref, c.Flag("by"))
	return s, tax, t, err
}

// contentTermParent reads --parent for a term of tax: 0, or a term of the same hierarchical taxonomy.
func contentTermParent(c *cli.Ctx, s *Server, tax *contentTax) (int64, error) {
	pid, err := c.Int64("parent", 0)
	if err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, nil
	}
	if !tax.Hierarchical {
		return 0, cli.Usagef("--parent: the %s taxonomy is not hierarchical", tax.Slug)
	}
	if _, err := contentFindTerm(c.Context, s.store, tax.Slug, strconv.FormatInt(pid, 10), "id"); err != nil {
		return 0, err
	}
	return pid, nil
}

// contentPage returns page paged (1-based) of size number from list; number <= 0 returns it all.
func contentPage[T any](list []T, number, paged int) []T {
	if number <= 0 {
		return list
	}
	if paged < 1 {
		paged = 1
	}
	from := (paged - 1) * number
	if from >= len(list) || from < 0 {
		return list[:0]
	}
	return list[from:min(from+number, len(list))]
}
