package server

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"wpgo/internal/auth"
	"wpgo/internal/cli"
	"wpgo/internal/db"
	"wpgo/internal/i18n"
)

// registerCoreInstallCLI adds `core install` and `core is-installed` (wp core install /
// wp core is-installed): the install screen (install.go) without a browser or setup token.
func registerCoreInstallCLI(r *cli.Registry, e *cliEnv) {
	r.Add(cli.Command{
		Path:  "core is-installed",
		Short: "Check whether the site is installed (exit 0) or not (exit 1).",
		Long:  "Prints nothing; the exit code answers, like wp core is-installed. Useful in provisioning scripts.",
		Examples: []string{
			"core is-installed && echo installed",
		},
		Run: func(c *cli.Ctx) error {
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			ok, err := siteInstalled(c.Context, pool)
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // not migrated yet: not installed
				ok, err = false, nil
			}
			if err != nil {
				return err
			}
			if !ok {
				return cli.Exit(cli.ExitFailure)
			}
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "core install",
		Short: "Install the site: settings, the administrator and the default content.",
		Long: "Does what the install screen (/wp-admin/install.php) does: stores the default settings, the site\n" +
			"title and address, creates the administrator and the default content (Hello world!, its comment,\n" +
			"the Sample Page, the Privacy Policy draft, the default widgets) and invalidates the setup token.\n" +
			"The database must be migrated first ('migrate', or start the server once).\n\n" +
			"Without --admin_password a random password is generated and printed once. --locale installs the\n" +
			"language pack when needed and makes it the site language. The recommended themes the install\n" +
			"screen downloads in the background are not fetched; install them with 'theme install'.",
		Flags: []cli.Flag{
			{Name: "url", Usage: "The site address (http or https). Default: WPGO_SITE_URL."},
			{Name: "title", Usage: "The site title.", Default: "My Pursae Site"},
			{Name: "admin_user", Usage: "The administrator's login.", Required: true},
			{Name: "admin_email", Usage: "The administrator's e-mail (also the site's admin e-mail).", Required: true},
			{Name: "admin_password", Usage: "The administrator's password; generated when omitted."},
			{Name: "locale", Usage: "Site language, e.g. de_DE (default en_US)."},
			{Name: "discourage-search", Bool: true, Usage: "Ask search engines not to index the site (blog_public=0)."},
			{Name: "update-emails", Bool: true, Usage: "Opt the administrator in to Pursae product update e-mails."},
		},
		Examples: []string{
			"core install --url=https://example.com --title=\"My Site\" --admin_user=admin --admin_email=me@example.com",
			"core install --url=http://localhost:8080 --admin_user=admin --admin_email=me@example.com --admin_password=secret --locale=tr_TR",
		},
		Run: func(c *cli.Ctx) error { return coreInstall(c, e) },
	})
}

func coreInstall(c *cli.Ctx, e *cliEnv) error {
	v := installView{
		SiteTitle:    strings.TrimSpace(c.Flag("title")),
		UserName:     strings.TrimSpace(c.Flag("admin_user")),
		Password:     c.Flag("admin_password"),
		Email:        strings.TrimSpace(c.Flag("admin_email")),
		Discourage:   c.Bool("discourage-search"),
		UpdateEmails: c.Bool("update-emails"),
	}
	// The install screen's checks (installSubmit), as usage errors.
	switch {
	case v.UserName == "":
		return cli.Usagef("please provide a valid username (--admin_user)")
	case !validUsername.MatchString(v.UserName):
		return cli.Usagef("the username '%s' has invalid characters", v.UserName)
	case c.Has("admin_password") && v.Password == "":
		return cli.Usagef("--admin_password is empty")
	}
	if _, err := mail.ParseAddress(v.Email); err != nil || !strings.Contains(v.Email, ".") || strings.ContainsAny(v.Email, "<> ") {
		return cli.Usagef("'%s' is not a valid e-mail address", v.Email)
	}
	if v.SiteTitle == "" {
		v.SiteTitle = "My Pursae Site"
	}
	site := strings.TrimRight(strings.TrimSpace(c.Flag("url")), "/")
	if site == "" {
		site = e.cfg.SiteURL
	}
	if u, err := url.Parse(site); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return cli.Usagef("--url must be an http(s) address, got '%s'", site)
	}
	locale := strings.TrimSpace(c.Flag("locale"))
	if locale != "" && locale != "en_US" && !validLanguage(locale) {
		return cli.Usagef("invalid locale '%s' (see %s language core list)", locale, c.Prog)
	}
	generated := false
	if v.Password == "" {
		v.Password, generated = auth.RandomToken(14), true
	}

	pool, err := e.Pool(c.Context)
	if err != nil {
		return err
	}
	if n, err := db.Pending(c.Context, pool); err != nil {
		return err
	} else if n > 0 {
		return cli.Errorf("%d database migration(s) pending: run '%s migrate' first", n, c.Prog)
	}
	if ok, err := siteInstalled(c.Context, pool); err != nil {
		return err
	} else if ok {
		c.Log("Pursae is already installed.")
		return nil
	}
	s, err := e.Server(c.Context)
	if err != nil {
		return err
	}
	s.cfg.SiteURL = site // install() stores s.cfg.SiteURL as siteurl/home
	if err := s.install(c.Context, v); err != nil {
		if errors.Is(err, errAlreadyInstalled) {
			c.Log("Pursae is already installed.")
			return nil
		}
		return cli.Errorf("installation failed: %v", err)
	}
	if locale != "" && locale != "en_US" {
		mgr := i18n.NewManager(e.langDir(), nil)
		if !mgr.IsInstalled(locale) {
			c.Log("Downloading %s…", locale)
			if err := mgr.Install(c.Context, &i18n.Installer{API: e.cfg.TranslationsAPI}, locale, nil); err != nil {
				c.Warning("the site is installed, but the %s language pack could not be downloaded (%v); it stays in English", locale, err)
			}
		}
		if mgr.IsInstalled(locale) {
			if err := s.settings.Set(c.Context, map[string]any{"WPLANG": locale}); err != nil {
				return err
			}
			if err := mgr.Install(c.Context, &i18n.Installer{API: e.cfg.TranslationsAPI}, locale, langPluginPacks); err != nil {
				c.Warning("plugin translations of %s: %v", locale, err)
			}
		}
	}
	if err := e.PurgeAll(c.Context); err != nil {
		c.Warning("page cache purge: %v", err)
	}
	if generated {
		fmt.Fprintf(c.Stdout(), "Admin password: %s\n", v.Password)
	}
	c.Success("Pursae installed at %s. Log in at %s/wp-login with '%s'.", site, site, v.UserName)
	return nil
}
