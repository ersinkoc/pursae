package server

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"wpgo/internal/cli"
)

// `import wordpress`: a WP to Pursae package (wp2p_package.go) from the command line, with the same
// importer the Tools › Import › From WordPress screen runs in the background (wp2p_import.go).

func registerWP2PCLI(r *cli.Registry, e *cliEnv) {
	r.Add(cli.Command{
		Path:  "import wordpress",
		Short: "Import a whole WordPress site from a WP to Pursae package.",
		Long: `Imports the package the WP to Pursae WordPress plugin builds (Tools › Move to Pursae › Download a
package, or wp pursae export): users with their password hashes (they keep their passwords), roles and
profiles; posts, pages, comments, terms, menus and media files; the site settings; and the WooCommerce
store: products, tax rates, coupons, orders with refunds and notes, and customers.

The package is encrypted: pass the key the plugin showed with --key, or in the environment variable
PURSAE_WP2P_KEY (keeps it out of the shell history). Importing the same site again adds only what is
missing. The command writes into PURSAE_UPLOADS_DIR: run it where the server's uploads volume is mounted.`,
		Args:  []cli.Arg{{Name: "package", Usage: "The .wp2p package file."}},
		Flags: []cli.Flag{{Name: "key", Usage: "The package key (43 characters) the plugin showed."}},
		Examples: []string{
			"import wordpress example.com-2026-11-22.wp2p --key=Qm9yYS4uLg",
			"PURSAE_WP2P_KEY=Qm9yYS4uLg import wordpress site.wp2p --user=admin",
		},
		Run: func(c *cli.Ctx) error { return cliImportWordPress(c, e) },
	})
	r.Add(cli.Command{
		Path:  "import wordpress-token",
		Short: "Generate a one-time migration token for the WP to Pursae plugin's \"Send to Pursae\".",
		Long: `Prints a migration token, as Tools › Import › From WordPress › Generate a migration token does. Enter it in
the plugin (or: wp pursae push --to=<this site> --token=<token>); it is shown once, works for one import
and expires after 24 hours. The import runs as the --user (default: the first administrator).`,
		Examples: []string{"import wordpress-token", "import wordpress-token --porcelain"},
		Flags:    []cli.Flag{{Name: "porcelain", Bool: true, Usage: "Print only the token."}},
		Run: func(c *cli.Ctx) error {
			s, err := e.Server(c.Context)
			if err != nil {
				return err
			}
			u, err := e.Actor(c)
			if err != nil {
				return err
			}
			token, _, expires, err := s.wp2pNewToken(c.Context, u)
			if err != nil {
				return err
			}
			if c.Bool("porcelain") {
				c.Println(token)
				return nil
			}
			c.Println("Migration token (shown once, valid until " + expires.Local().Format("2006-01-02 15:04") + "): " + token)
			return nil
		},
	})
}

func cliImportWordPress(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	pkg := c.Arg(0)
	if _, err := os.Stat(pkg); err != nil {
		return cli.Errorf("%v", err)
	}
	keyText := c.Flag("key")
	if keyText == "" {
		keyText = os.Getenv("PURSAE_WP2P_KEY")
	}
	if keyText == "" {
		return cli.Usagef("the package key is required: --key=<key> or PURSAE_WP2P_KEY")
	}
	key, err := wp2pParseKey(keyText)
	if err != nil {
		return cli.Errorf("%v", err)
	}
	if err := wp2pCheckPackage(pkg, key); err != nil {
		return cli.Errorf("%v", err)
	}
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	u, err := e.Actor(c)
	if err != nil {
		return err
	}
	// A run of this command that was killed (SIGKILL, a crash) left its row "running" and its extracted
	// files behind: close it and delete them now instead of waiting for the hourly cleanup.
	if ids, err := s.wp2pFailStaleCLI(ctx); err == nil && len(ids) > 0 {
		c.Log("Closed %d earlier import(s) whose command was killed; their extracted files were deleted.", len(ids))
	}
	id := wp2pNewID()
	// cli: the screen does not offer Resume for this row (the package and key are not kept here); the
	// command is run again instead, and importing the same site again adds only what is missing.
	if _, err := s.store.Pool.Exec(ctx, `INSERT INTO wp2p_migrations (id, created_by, state, size, received, cli) VALUES ($1, $2, 'running', $3, $3, true)`,
		id, u.ID, fileSize(pkg)); err != nil {
		return err
	}
	r := &wp2pRun{s: s, id: id, dir: s.wp2pWorkDir(id), pkg: pkg, keepPackage: true, key: &key, u: u,
		logf: func(format string, args ...any) { c.Log(format, args...) }}
	if err := r.run(ctx); err != nil {
		s.wp2pSetState(ctx, id, "failed", err.Error()+" (run the command again to continue)")
		_ = os.RemoveAll(r.dir)
		return cli.Errorf("%v (what was imported stays; run the command again to continue)", err)
	}
	s.wp2pSetState(ctx, id, "done", "")
	rep := r.rep
	for _, n := range rep.Notes {
		c.Log("-- %s", n)
	}
	for _, sk := range rep.Skipped {
		c.Log("-- skipped %s %s: %s", sk.Kind, sk.Item, sk.Reason)
	}
	for _, line := range cliWP2PCounts("unmapped", rep.Unmapped) {
		c.Log("-- %s", line)
	}
	if len(rep.Existing) > 0 {
		c.Log("Already here: %s.", strings.Join(cliWP2PCounts("", rep.Existing), ", "))
	}
	c.Success("Imported %s.", strings.Join(cliWP2PCounts("", rep.Counts), ", "))
	return nil
}

// cliWP2PCounts lists counters as "3 orders" in a stable order.
func cliWP2PCounts(prefix string, m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k, n := range m {
		if n > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		label := strings.ReplaceAll(k, "_", " ")
		if prefix != "" {
			out = append(out, fmt.Sprintf("%s: %s (%d)", prefix, label, m[k]))
		} else {
			out = append(out, fmt.Sprintf("%d %s", m[k], label))
		}
	}
	if len(out) == 0 && prefix == "" {
		out = append(out, "nothing new")
	}
	return out
}
