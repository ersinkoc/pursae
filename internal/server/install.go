package server

import (
	"context"
	_ "embed"
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
	Error                         string
	SiteTitle, UserName, Password string
	Email                         string
	Discourage                    bool
	Submitted                     bool // re-rendered after a POST: keep the password masked, like WordPress
}

var validUsername = regexp.MustCompile(`^[A-Za-z0-9 _.\-@]+$`)

func (s *Server) installForm(c *gin.Context) {
	if s.settings.Installed() {
		c.Redirect(http.StatusFound, "/wp-login")
		return
	}
	s.renderAdminTemplate(c, http.StatusOK, "admin/install", installView{Password: auth.RandomToken(14)})
}

func (s *Server) installSubmit(c *gin.Context) {
	if s.settings.Installed() {
		c.Redirect(http.StatusFound, "/wp-login")
		return
	}
	v := installView{
		SiteTitle:  strings.TrimSpace(c.PostForm("weblog_title")),
		UserName:   strings.TrimSpace(c.PostForm("user_name")),
		Password:   c.PostForm("admin_password"),
		Email:      strings.TrimSpace(c.PostForm("admin_email")),
		Discourage: c.PostForm("blog_public") == "0",
		Submitted:  true,
	}
	switch {
	case v.UserName == "":
		v.Error = "Please provide a valid username."
	case !validUsername.MatchString(v.UserName):
		v.Error = "The username you provided has invalid characters."
	case v.Password == "":
		v.Error = "You must provide a password."
	case c.PostForm("admin_password2") != "" && c.PostForm("admin_password2") != v.Password:
		v.Error = "Your passwords do not match. Please try again."
	case v.Email == "":
		v.Error = "You must provide an email address."
	}
	if v.Error == "" {
		if _, err := mail.ParseAddress(v.Email); err != nil || !strings.Contains(v.Email, ".") {
			v.Error = "Sorry, that is not a valid email address. Email addresses look like <code>username@example.com</code>."
		}
	}
	if v.Error != "" {
		s.renderAdminTemplate(c, http.StatusOK, "admin/install", v)
		return
	}
	if v.SiteTitle == "" {
		v.SiteTitle = "My WordPress Go Site"
	}
	if err := s.install(c, v); err != nil {
		v.Error = "Installation failed: " + err.Error()
		s.renderAdminTemplate(c, http.StatusOK, "admin/install", v)
		return
	}
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
	userID, err := s.store.CreateUser(ctx, store.NewUser{
		Login: v.UserName, Email: v.Email, PasswordHash: hash, Role: "administrator",
	})
	if err != nil {
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
			userID, "<!-- wp:paragraph -->\n<p>Welcome to WordPress Go. This is your first post. Edit or delete it, then start writing!</p>\n<!-- /wp:paragraph -->", now).Scan(&postID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO post_terms(post_id, term_id) SELECT $1, id FROM terms WHERE taxonomy='category' AND slug='uncategorized'`, postID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE terms SET post_count = 1 WHERE taxonomy='category' AND slug='uncategorized'`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO comments(post_id, author_name, author_email, author_url, content, status, created_at)
			VALUES ($1, 'A WordPress Go Commenter', 'wapuu@wordpress.example', 'https://wordpress.org/', $2, 'approved', $3)`,
			postID, "Hi, this is a comment.\nTo get started with moderating, editing, and deleting comments, please visit the Comments screen in the dashboard.\nCommenters who have an account can pick their avatar from the Media Library on their profile.", now); err != nil {
			return err
		}

		samplePage := "<!-- wp:paragraph -->\n<p>This is an example page. It's different from a blog post because it will stay in one place and will show up in your site navigation (in most themes). Most people start with an About page that introduces them to potential site visitors. It might say something like this:</p>\n<!-- /wp:paragraph -->\n\n" +
			"<!-- wp:quote -->\n<blockquote class=\"wp-block-quote\">\n<!-- wp:paragraph -->\n<p>Hi there! I'm a bike messenger by day, aspiring actor by night, and this is my website. I live in Los Angeles, have a great dog named Jack, and I like pi&#241;a coladas. (And gettin' caught in the rain.)</p>\n<!-- /wp:paragraph -->\n</blockquote>\n<!-- /wp:quote -->\n\n" +
			"<!-- wp:paragraph -->\n<p>...or something like this:</p>\n<!-- /wp:paragraph -->\n\n" +
			"<!-- wp:quote -->\n<blockquote class=\"wp-block-quote\">\n<!-- wp:paragraph -->\n<p>The XYZ Doohickey Company was founded in 1971, and has been providing quality doohickeys to the public ever since. Located in Gotham City, XYZ employs over 2,000 people and does all kinds of awesome things for the Gotham community.</p>\n<!-- /wp:paragraph -->\n</blockquote>\n<!-- /wp:quote -->\n\n" +
			"<!-- wp:paragraph -->\n<p>As a new WordPress Go user, you should go to <a href=\"" + site + "/wp-admin/\">your dashboard</a> to delete this page and create new pages for your content. Have fun!</p>\n<!-- /wp:paragraph -->"
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
		if _, err := tx.Exec(ctx, `INSERT INTO themes(slug, version, status) VALUES ('twentysixteen', '3.9', 'active') ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		_ = pageID
		return nil
	})
	if err != nil {
		return err
	}

	// Default widgets of a fresh install: block widgets in sidebar-1.
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
	return s.settings.Set(ctx, values)
}

func (s *Server) seedWidgets(ctx context.Context) error {
	area, err := s.store.EnsureWidgetArea(ctx, "twentysixteen", "sidebar-1", "Sidebar")
	if err != nil {
		return err
	}
	if _, err := s.store.EnsureWidgetArea(ctx, "twentysixteen", "sidebar-2", "Content Bottom 1"); err != nil {
		return err
	}
	if _, err := s.store.EnsureWidgetArea(ctx, "twentysixteen", "sidebar-3", "Content Bottom 2"); err != nil {
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
