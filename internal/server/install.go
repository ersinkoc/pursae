package server

import (
	"context"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"wpgo/internal/auth"
	"wpgo/internal/settings"
	"wpgo/internal/store"
)

//go:embed defaults/privacy-policy.html
var privacyPolicyContent string

type installView struct {
	Done                          bool
	NeedToken                     bool // the setup token form (setup_token.go)
	Error                         string
	SiteTitle, UserName, Password string
	Email                         string
	Discourage                    bool
	UpdateEmails                  bool // the owner ticked "Email me Pursae product updates" (off by default, hub_contacts.go)
	Submitted                     bool // re-rendered after a POST: keep the password masked, like WordPress
	// The language step (wp-admin/install.php?step=0): the languages to choose from, the choice,
	// and a failed pack download.
	ChooseLanguage bool
	Languages      []langChoice
	Language       string
	// ErrorHTML is an error that carries WordPress's own markup (the invalid-email message's
	// <code>). Only constant strings go here: anything holding user input or an error's text
	// stays in Error, which the template escapes.
	ErrorHTML template.HTML
	// ErrorField is the id of the input the error is about, so the template marks it
	// aria-invalid and focuses it (WCAG 3.3.1); empty when no single field is at fault.
	ErrorField string
}

var validUsername = regexp.MustCompile(`^[A-Za-z0-9 _.\-@]+$`)

// installLockID is the advisory lock the installer holds (db.Migrate uses 7_356_001).
const installLockID = 7_356_002

// errAlreadyInstalled: the site was installed while this submission waited.
var errAlreadyInstalled = errors.New("the site is already installed")

func (s *Server) installForm(c *gin.Context) {
	if s.settings.Installed() {
		c.Redirect(http.StatusFound, "/wp-login")
		return
	}
	if !s.setupGate(c) {
		return
	}
	lang := c.Query("language")
	if lang == "" { // first the language, like WordPress
		s.renderAdminTemplate(c, http.StatusOK, "admin/install", s.installLanguageStep(""))
		return
	}
	if lang == "en_US" {
		s.rememberLocale(c, "en_US")
	} else if !validLanguage(lang) {
		s.renderAdminTemplate(c, http.StatusOK, "admin/install", s.installLanguageStep(""))
		return
	} else if err := s.installLanguageBounded(c.Request.Context(), lang); err != nil {
		slog.Warn("install language", "locale", lang, "err", err)
		v := s.installLanguageStep("The language pack could not be downloaded. Check that the server can reach translate.wordpress.org, or continue in English.")
		s.renderAdminTemplate(c, http.StatusOK, "admin/install", v)
		return
	} else {
		s.rememberLocale(c, lang)
	}
	c.Set("wpgo.pickedLocale", lang) // render the form in the chosen language
	s.renderAdminTemplate(c, http.StatusOK, "admin/install", installView{Password: auth.RandomToken(14), Language: lang})
}

// installLangTimeout bounds the core pack download of the install screen's language step (the API
// lookup and the zip, with its fallback request): the person installing waits on it with no other
// feedback, and the download client alone allows 90 seconds. Like WordPress, a failed download
// leaves the choice of continuing in English. A variable so tests can shorten it.
var installLangTimeout = 30 * time.Second

// installLanguageBounded is installLanguage under installLangTimeout. The deadline is only the
// install screen's: Settings › General and the REST API share installLanguage without it.
func (s *Server) installLanguageBounded(ctx context.Context, locale string) error {
	ctx, cancel := context.WithTimeout(ctx, installLangTimeout)
	defer cancel()
	return s.installLanguage(ctx, locale)
}

// installLanguageStep is the first install screen: every WordPress.org language.
func (s *Server) installLanguageStep(errMsg string) installView {
	installed, available := s.languageChoices("", false)
	return installView{ChooseLanguage: true, Error: errMsg, Languages: append(installed, available...)}
}

func (s *Server) installSubmit(c *gin.Context) {
	if s.settings.Installed() {
		c.Redirect(http.StatusFound, "/wp-login")
		return
	}
	if !s.setupGate(c) {
		return
	}
	v := installView{
		SiteTitle:    strings.TrimSpace(c.PostForm("weblog_title")),
		UserName:     strings.TrimSpace(c.PostForm("user_name")),
		Password:     c.PostForm("admin_password"),
		Email:        strings.TrimSpace(c.PostForm("admin_email")),
		Discourage:   c.PostForm("blog_public") == "0",
		UpdateEmails: c.PostForm("pursae_update_emails") == "1",
		Submitted:    true,
		Language:     c.PostForm("language"),
	}
	switch {
	case v.UserName == "":
		v.Error, v.ErrorField = "Please provide a valid username.", "user_login"
	case !validUsername.MatchString(v.UserName):
		v.Error, v.ErrorField = "The username you provided has invalid characters.", "user_login"
	case v.Password == "":
		v.Error, v.ErrorField = "You must provide a password.", "pass1"
	case c.PostForm("admin_password2") != "" && c.PostForm("admin_password2") != v.Password:
		v.Error, v.ErrorField = "Your passwords do not match. Please try again.", "pass1"
	case v.Email == "":
		v.Error, v.ErrorField = "You must provide an email address.", "admin_email"
	}
	if v.Error == "" {
		if _, err := mail.ParseAddress(v.Email); err != nil || !strings.Contains(v.Email, ".") {
			// WordPress's exact msgid, markup included, so its translation still matches. It is
			// a constant, so it is safe as HTML; escaped, the <code> tags showed up literally.
			v.ErrorHTML = "Sorry, that is not a valid email address. Email addresses look like <code>username@example.com</code>."
			v.ErrorField = "admin_email"
		}
	}
	if v.Error != "" || v.ErrorHTML != "" {
		s.renderAdminTemplate(c, http.StatusOK, "admin/install", v)
		return
	}
	if v.SiteTitle == "" {
		v.SiteTitle = "My Pursae Site"
	}
	if err := s.install(c, v); err != nil {
		if errors.Is(err, errAlreadyInstalled) { // another request (or instance) installed the site first
			c.Redirect(http.StatusFound, "/wp-login")
			return
		}
		v.Error = "Installation failed: " + err.Error()
		s.renderAdminTemplate(c, http.StatusOK, "admin/install", v)
		return
	}
	if v.Language != "" && v.Language != "en_US" && validLanguage(v.Language) && s.lang.IsInstalled(v.Language) {
		_ = s.settings.Set(c, map[string]any{"WPLANG": v.Language}) // the site's language
		s.syncSearchStemAsync()
	}
	s.preinstallThemesAsync() // the Manager's recommended themes, in the background (theme_library.go)
	s.queueHubCheck(c)        // the site's address is known now: tell the Manager right away (hub.go)
	v.Done = true
	s.renderAdminTemplate(c, http.StatusOK, "admin/install", v)
}

// install is wp_install() + wp_install_defaults(): settings, the admin user,
// "Hello world!", its first comment, the Sample Page, the Privacy Policy draft and
// the default sidebar widgets.
func (s *Server) install(ctx context.Context, v installView) error {
	hash, err := auth.HashPassword(v.Password)
	if err != nil {
		return err
	}
	// Two submissions racing (two tabs, or a stranger next to the owner, on one instance or several)
	// would both pass the Installed() check and each create an administrator: the install runs under a
	// database-wide lock and checks the database itself, not this process's settings cache.
	conn, err := s.store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, installLockID); err != nil {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, installLockID) //nolint:errcheck
	var installed bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM settings WHERE key = 'siteurl')`).Scan(&installed); err != nil {
		return err
	}
	if installed {
		_ = s.settings.Load(ctx)
		return errAlreadyInstalled
	}
	userID, err := s.store.CreateUser(ctx, store.NewUser{
		Login: v.UserName, Email: v.Email, PasswordHash: hash, Role: "administrator",
	})
	if err != nil {
		return err
	}
	// The owner's own choice on the install screen (an unticked box by default): on records the time of
	// consent, off counts as asked so no opt-in notice follows (hub_contacts.go).
	if err := s.setUpdateEmails(ctx, &store.User{ID: userID}, v.UpdateEmails); err != nil {
		return err
	}

	site := s.cfg.SiteURL
	now := time.Now()
	pool := s.store.Pool
	var privacyID int64
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var postID, pageID int64
		if err := tx.QueryRow(ctx, `INSERT INTO posts(type, author_id, slug, title, content, status, published_at, comment_count)
			VALUES ('post', $1, 'hello-world', 'Hello world!', $2, 'publish', $3, 1) RETURNING id`,
			userID, "<!-- wp:paragraph -->\n<p>Welcome to Pursae. This is your first post. Edit or delete it, then start writing!</p>\n<!-- /wp:paragraph -->", now).Scan(&postID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO post_terms(post_id, term_id) SELECT $1, id FROM terms WHERE taxonomy='category' AND slug='uncategorized'`, postID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE terms SET post_count = 1 WHERE taxonomy='category' AND slug='uncategorized'`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO comments(post_id, author_name, author_email, author_url, content, status, created_at)
			VALUES ($1, 'A Pursae Commenter', 'wapuu@wordpress.example', 'https://wordpress.org/', $2, 'approved', $3)`,
			postID, "Hi, this is a comment.\nTo get started with moderating, editing, and deleting comments, please visit the Comments screen in the dashboard.\nCommenters who have an account can pick their avatar from the Media Library on their profile.", now); err != nil {
			return err
		}

		samplePage := "<!-- wp:paragraph -->\n<p>This is an example page. It's different from a blog post because it will stay in one place and will show up in your site navigation (in most themes). Most people start with an About page that introduces them to potential site visitors. It might say something like this:</p>\n<!-- /wp:paragraph -->\n\n" +
			"<!-- wp:quote -->\n<blockquote class=\"wp-block-quote\">\n<!-- wp:paragraph -->\n<p>Hi there! I'm a bike messenger by day, aspiring actor by night, and this is my website. I live in Los Angeles, have a great dog named Jack, and I like pi&#241;a coladas. (And gettin' caught in the rain.)</p>\n<!-- /wp:paragraph -->\n</blockquote>\n<!-- /wp:quote -->\n\n" +
			"<!-- wp:paragraph -->\n<p>...or something like this:</p>\n<!-- /wp:paragraph -->\n\n" +
			"<!-- wp:quote -->\n<blockquote class=\"wp-block-quote\">\n<!-- wp:paragraph -->\n<p>The XYZ Doohickey Company was founded in 1971, and has been providing quality doohickeys to the public ever since. Located in Gotham City, XYZ employs over 2,000 people and does all kinds of awesome things for the Gotham community.</p>\n<!-- /wp:paragraph -->\n</blockquote>\n<!-- /wp:quote -->\n\n" +
			"<!-- wp:paragraph -->\n<p>As a new Pursae user, you should go to <a href=\"" + site + "/wp-admin/\">your dashboard</a> to delete this page and create new pages for your content. Have fun!</p>\n<!-- /wp:paragraph -->"
		if err := tx.QueryRow(ctx, `INSERT INTO posts(type, author_id, slug, title, content, status, published_at, comment_status, ping_status)
			VALUES ('page', $1, 'sample-page', 'Sample Page', $2, 'publish', $3, 'closed', 'open') RETURNING id`,
			userID, samplePage, now).Scan(&pageID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO posts(type, author_id, slug, title, content, status, comment_status, ping_status)
			VALUES ('page', $1, 'privacy-policy', 'Privacy Policy', $2, 'draft', 'closed', 'closed') RETURNING id`,
			userID, strings.ReplaceAll(privacyPolicyContent, "SITE_URL", site)).Scan(&privacyID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO themes(slug, version, status) VALUES ($1, $2, 'active') ON CONFLICT DO NOTHING`, defaultTheme, tt5Version); err != nil {
			return err
		}
		_ = pageID
		return nil
	})
	if err != nil {
		return err
	}

	// Default widgets of a fresh install: block widgets in sidebar-1, as wp_install_defaults() stores
	// them whatever the theme. The default theme shows no widgets; a theme with a sidebar-1 area
	// gets them when it is activated (ensureThemeWidgets).
	if err := s.seedWidgets(ctx); err != nil {
		return err
	}

	values := map[string]any{}
	for k, val := range settings.Defaults {
		values[k] = val
	}
	values["blogname"] = v.SiteTitle
	values["admin_email"] = v.Email
	values["siteurl"] = site
	values["home"] = site
	values["blog_public"] = !v.Discourage
	values["wp_page_for_privacy_policy"] = privacyID
	values["fresh_site"] = true
	if err := s.settings.Set(ctx, values); err != nil {
		return err
	}
	// The site is installed: the setup token, and every cookie that remembers it, stops working.
	_, err = conn.Exec(ctx, `DELETE FROM setup_token`)
	return err
}

func (s *Server) seedWidgets(ctx context.Context) error {
	area, err := s.store.EnsureWidgetArea(ctx, defaultTheme, "sidebar-1", "Sidebar")
	if err != nil {
		return err
	}
	defaults := []string{
		`<!-- wp:search /-->`,
		`<!-- wp:group --><div class="wp-block-group"><!-- wp:heading --><h2>Recent Posts</h2><!-- /wp:heading --><!-- wp:latest-posts /--></div><!-- /wp:group -->`,
		`<!-- wp:group --><div class="wp-block-group"><!-- wp:heading --><h2>Recent Comments</h2><!-- /wp:heading --><!-- wp:latest-comments {"displayAvatar":false,"displayDate":false,"displayExcerpt":false} /--></div><!-- /wp:group -->`,
		`<!-- wp:group --><div class="wp-block-group"><!-- wp:heading --><h2>Archives</h2><!-- /wp:heading --><!-- wp:archives /--></div><!-- /wp:group -->`,
		`<!-- wp:group --><div class="wp-block-group"><!-- wp:heading --><h2>Categories</h2><!-- /wp:heading --><!-- wp:categories /--></div><!-- /wp:group -->`,
	}
	for i, content := range defaults {
		if err := s.store.AddWidget(ctx, area, "block", i, map[string]any{"content": content}); err != nil {
			return err
		}
	}
	return nil
}
