package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"wpgo/internal/cli"
	"wpgo/internal/store"
)

// `import`: the WordPress importer (WXR) from the command line, like `wp import`. It runs the
// same importWXRWith the Tools › Import screen's background job runs, then purges the page cache.

func registerImportCLI(r *cli.Registry, e *cliEnv) {
	r.Add(cli.Command{
		Path:  "import",
		Short: "Import content from WordPress eXtended RSS (WXR) files.",
		Long: `Imports posts, pages, comments, terms, menus, attachments and WooCommerce products from WXR export files (Tools ›
Export in WordPress or Pursae), with the same importer as Tools › Import.

--authors decides who owns the imported content:
  (absent)     authors whose login exists here keep it, others go to the importing user
  create       create missing authors as users with the author role
  skip         assign everything to the importing user (--user, default the first administrator)
  <file>       a mapping file: JSON {"old_login": "new_login_or_id"} or CSV lines
               old_login,new_login; a new login that does not exist is created

Attachments are downloaded (public addresses only) unless --no-fetch-attachments is given. The
command writes into WPGO_UPLOADS_DIR: run it where the server's uploads volume is mounted.`,
		Args: []cli.Arg{{Name: "file", Usage: "WXR file to import ('-' reads stdin).", Repeat: true}},
		Flags: []cli.Flag{
			{Name: "authors", Usage: "Author mapping: create, skip or a mapping file (see above)."},
			{Name: "fetch-attachments", Bool: true, Default: "true", Usage: "Download and import file attachments."},
		},
		Examples: []string{
			"import export.xml",
			"import export.xml --authors=create",
			"import export.xml --authors=authors.csv --no-fetch-attachments --user=editor",
		},
		Run: func(c *cli.Ctx) error { return cliImport(c, e) },
	})
}

func cliImport(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	type source struct {
		name string
		data []byte
	}
	var files []source
	for _, arg := range c.Args {
		data, err := c.ReadInput(arg, os.ReadFile)
		if err != nil {
			return cli.Errorf("reading %s: %v", arg, err)
		}
		files = append(files, source{arg, data})
	}
	mode := strings.TrimSpace(c.Flag("authors"))
	var mapping map[string]string
	if mode != "" && mode != "create" && mode != "skip" {
		raw, err := os.ReadFile(mode)
		if err != nil {
			return cli.Errorf("--authors: reading the mapping file: %v", err)
		}
		if mapping, err = parseAuthorMapping(raw); err != nil {
			return cli.Errorf("--authors: %v", err)
		}
	}
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	u, err := e.Actor(c)
	if err != nil {
		return err
	}
	var total importReport
	failed := 0
	for _, f := range files {
		logins, err := wxrAuthorLogins(f.data)
		if err != nil {
			c.Warning("%s: %v", f.name, err)
			failed++
			continue
		}
		authors, err := s.cliWXRAuthors(ctx, mode, mapping, logins)
		if err != nil {
			return err
		}
		c.Log("Importing %s …", f.name)
		rep, err := s.importWXRWith(ctx, u, f.data, wxrImportOptions{Authors: authors, FetchAttachments: c.Bool("fetch-attachments")})
		if err != nil {
			c.Warning("%s: %v", f.name, err)
			failed++
			continue
		}
		for _, line := range rep.Log {
			// The report speaks of the admin form's checkbox; on the command line it is the flag.
			line = strings.Replace(line, "with “Download and import file attachments” checked", "without --no-fetch-attachments", 1)
			c.Log("-- %s", line)
		}
		c.Log("%s: %s", f.name, importSummary(rep))
		addImportReport(&total, rep)
	}
	s.purgeAll(ctx) // as the import job does after importing
	if failed > 0 {
		return cli.Errorf("%d of %d file(s) could not be imported", failed, len(files))
	}
	c.Success("Imported %s.", importSummary(&total))
	return nil
}

// importSummary is a one-line count of what an import created.
func importSummary(r *importReport) string {
	parts := []string{}
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+what)
		}
	}
	add(r.Posts, "posts")
	add(r.Pages, "pages")
	add(r.Blocks, "patterns")
	add(r.Templates, "templates")
	add(r.Terms, "terms")
	add(r.Comments, "comments")
	add(r.Attachments, "attachments")
	add(r.MenuItems, "menu items")
	add(r.Products, "products")
	add(r.Variations, "variations")
	if len(parts) == 0 {
		parts = append(parts, "nothing new")
	}
	s := strings.Join(parts, ", ")
	if r.Skipped > 0 {
		s += fmt.Sprintf(" (%d skipped)", r.Skipped)
	}
	if r.SkippedOrders > 0 || r.SkippedCoupons > 0 {
		s += fmt.Sprintf("; skipped %d orders / %d coupons", r.SkippedOrders, r.SkippedCoupons)
	}
	return s
}

func addImportReport(dst, r *importReport) {
	dst.Posts += r.Posts
	dst.Pages += r.Pages
	dst.Blocks += r.Blocks
	dst.Templates += r.Templates
	dst.Skipped += r.Skipped
	dst.Terms += r.Terms
	dst.Comments += r.Comments
	dst.Attachments += r.Attachments
	dst.MenuItems += r.MenuItems
	dst.Products += r.Products
	dst.Variations += r.Variations
	dst.SkippedOrders += r.SkippedOrders
	dst.SkippedCoupons += r.SkippedCoupons
}

// wxrAuthorLogins lists the author logins declared in a WXR file.
func wxrAuthorLogins(data []byte) ([]string, error) {
	var doc wxrDoc
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("this does not appear to be a WXR file: %v", err)
	}
	out := make([]string, 0, len(doc.Channel.Authors))
	for _, a := range doc.Channel.Authors {
		if a.Login != "" {
			out = append(out, a.Login)
		}
	}
	return out, nil
}

// parseAuthorMapping reads an --authors mapping file: a JSON object {"old": "new"} or CSV lines
// old,new (an optional old_user_login,new_user_login header is skipped).
func parseAuthorMapping(raw []byte) (map[string]string, error) {
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")))
	out := map[string]string{}
	if len(raw) > 0 && raw[0] == '{' {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("invalid JSON mapping: %v", err)
		}
		for k, v := range m {
			switch v := v.(type) {
			case string:
				out[strings.TrimSpace(k)] = strings.TrimSpace(v)
			case float64:
				out[strings.TrimSpace(k)] = strconv.FormatInt(int64(v), 10)
			case nil:
				out[strings.TrimSpace(k)] = ""
			default:
				return nil, fmt.Errorf("mapping for %q must be a login or user ID", k)
			}
		}
		return out, nil
	}
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	for line := 1; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid CSV mapping: %v", err)
		}
		if len(rec) == 0 || (len(rec) == 1 && strings.TrimSpace(rec[0]) == "") {
			continue
		}
		if len(rec) != 2 {
			return nil, fmt.Errorf("line %d: expected old_login,new_login", line)
		}
		old, nu := strings.TrimSpace(rec[0]), strings.TrimSpace(rec[1])
		if line == 1 && strings.EqualFold(old, "old_user_login") {
			continue
		}
		if old != "" {
			out[old] = nu
		}
	}
	return out, nil
}

// cliWXRAuthors turns the --authors mode into the importer's per-login choices.
func (s *Server) cliWXRAuthors(ctx context.Context, mode string, mapping map[string]string, logins []string) (map[string]wxrAuthorChoice, error) {
	out := map[string]wxrAuthorChoice{}
	switch mode {
	case "":
		return out, nil // existing logins keep their content, the rest goes to the importer
	case "skip":
		for _, l := range logins {
			out[l] = wxrAuthorChoice{}
		}
		return out, nil
	case "create":
		for _, l := range logins {
			out[l] = wxrAuthorChoice{NewLogin: l} // an existing user with that login is reused
		}
		return out, nil
	}
	for old, nu := range mapping {
		switch {
		case nu == "":
			out[old] = wxrAuthorChoice{}
		default:
			if id, err := strconv.ParseInt(nu, 10, 64); err == nil && id > 0 {
				if _, err := s.store.UserByID(ctx, id); err != nil {
					return nil, cli.Errorf("--authors: %q maps to unknown user ID %d", old, id)
				}
				out[old] = wxrAuthorChoice{UserID: id}
				continue
			}
			if u, err := s.store.UserByLoginOrEmail(ctx, nu); err == nil {
				out[old] = wxrAuthorChoice{UserID: u.ID}
			} else if errors.Is(err, store.ErrNotFound) {
				out[old] = wxrAuthorChoice{NewLogin: nu}
			} else {
				return nil, err
			}
		}
	}
	return out, nil
}
