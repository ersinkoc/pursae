package main

import (
	"context"
	"fmt"
	"io"
	"log"

	"wpgo/internal/config"
	"wpgo/internal/db"
	"wpgo/internal/server"
)

// announceSetupToken prints the install URL, Jupyter style, when this process generated the setup token
// of an uninstalled site. With PURSAE_SETUP_TOKEN the operator already knows the token; when another
// process (or an earlier run) issued it, only its hash is stored, so the log says how to get a new one.
func announceSetupToken(ctx context.Context, cfg config.Config, srv *server.Server) {
	token, err := srv.EnsureSetupToken(ctx)
	switch {
	case err != nil:
		log.Printf("setup token: %v", err)
	case token != "":
		log.Printf("Pursae is not installed yet. To install it, open this URL in your browser:")
		log.Printf("    %s", server.SetupURL(cfg.SiteURL, token))
		log.Printf("The setup token stops working once the site is installed. Run `wpgo setup-token` for a new one.")
	case cfg.SetupToken != "" && !srv.Installed():
		log.Printf("Pursae is not installed yet. Open %s/wp-admin/install and enter PURSAE_SETUP_TOKEN.", cfg.SiteURL)
	case !srv.Installed():
		log.Printf("Pursae is not installed yet. A setup token was already issued; run `wpgo setup-token` to print a new install URL.")
	}
}

// setupToken is `wpgo setup-token`: the install URL with PURSAE_SETUP_TOKEN, or with a newly issued token
// that replaces the stored one (the old link stops working).
func setupToken(ctx context.Context, cfg config.Config, out io.Writer) error {
	if cfg.SetupToken != "" {
		_, err := fmt.Fprintln(out, server.SetupURL(cfg.SiteURL, cfg.SetupToken))
		return err
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	token, err := server.IssueSetupToken(ctx, pool)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, server.SetupURL(cfg.SiteURL, token)); err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "The previous setup token, if any, no longer works. This one stops working once the site is installed.")
	return err
}
