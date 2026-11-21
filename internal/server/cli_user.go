package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"

	"wpgo/internal/auth"
	"wpgo/internal/cli"
	"wpgo/internal/store"
	"wpgo/internal/wp"
)

// registerUserCLI registers the user commands. See cli.go for the environment (cliEnv) and conventions.
//
// Nouns: user (+ user meta, user session, user application-password), role, cap.
//
// Every write follows the admin screens and REST: argon2id hashes only (auth.HashPassword; the one
// exception is `user create --user_pass_hash`, which keeps a WordPress hash for migration), a new
// password ends every session and revokes application passwords and OAuth grants, the last
// administrator cannot be removed, Pursae Security audit rows are written (username "cli", or
// "cli:<login>" with --user), and a site-wide page cache purge is announced where the admin purges.
// Serve-only side effects do not run: Wasm plugin hooks (user_register …), ACF-Go user fields and
// the new-user / password-reset e-mails.
func registerUserCLI(r *cli.Registry, e *cliEnv) {
	r.Noun("user", "Manage users, their roles, capabilities, meta, sessions and application passwords.")
	r.Noun("role", "List user roles (roles are defined by Pursae and its plugins).")
	r.Noun("cap", "List the capabilities of a role.")
	registerUserCoreCLI(r, e)
	registerUserAccessCLI(r, e)
}

// cliUserListFields are the default columns of `user list`.
var cliUserListFields = []string{"ID", "user_login", "display_name", "user_email", "user_registered", "roles"}

// cliUserGetFields are the default fields of `user get`.
var cliUserGetFields = []string{"ID", "user_login", "user_email", "display_name", "user_nicename", "user_url", "user_registered",
	"roles", "first_name", "last_name", "nickname", "description", "locale", "last_login_at"}

// cliUserRow is a user with WP-CLI field names.
func cliUserRow(u *store.User) cli.Row {
	r := cli.Row{
		"ID":              u.ID,
		"user_login":      u.Login,
		"user_email":      u.Email,
		"display_name":    u.DisplayName,
		"user_nicename":   u.Nicename,
		"user_url":        u.URL,
		"user_registered": u.RegisteredAt.UTC(), // GMT, like WordPress
		"roles":           strings.Join(u.Roles, ","),
		"locale":          u.Locale,
	}
	for _, k := range []string{"first_name", "last_name", "nickname", "description"} {
		r[k] = u.Profile[k]
	}
	return r
}

// cliUserAuditBase is the audit event identity of a CLI action: "cli", or "cli:<login>" (and the
// actor's ID) when --user was given.
func cliUserAuditBase(c *cli.Ctx, e *cliEnv) asEvent {
	ev := asEvent{Username: "cli"}
	if c.Flag("user") != "" {
		if a, err := e.Actor(c); err == nil {
			ev.Username, ev.UserID = "cli:"+a.Login, a.ID
		}
	}
	return ev
}

func cliUserAudit(c *cli.Ctx, e *cliEnv, s *Server, ev asEvent) {
	base := cliUserAuditBase(c, e)
	ev.Username, ev.UserID = base.Username, base.UserID
	s.asAudit(c.Context, ev)
}

// cliUserPasswordProblem is the input check REST applies to a new password.
func cliUserPasswordProblem(pw string) string {
	switch {
	case pw == "":
		return "Passwords cannot be empty."
	case strings.Contains(pw, `\`):
		return `Passwords cannot contain the "\" character.`
	}
	return ""
}

// cliUserGeneratePassword is a random password for create / reset-password (24 URL-safe characters).
func cliUserGeneratePassword() string { return auth.RandomToken(18) }

// cliUserSetPassword is the password change every admin, REST and reset path runs: store the argon2id
// hash, end every session, revoke application passwords and OAuth grants, and (Pursae Security on)
// drop a pending forced change and the remembered devices and audit it.
func cliUserSetPassword(c *cli.Ctx, e *cliEnv, s *Server, u *store.User, pw string) error {
	ctx := c.Context
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := s.store.SetUserPassword(ctx, u.ID, hash); err != nil {
		return err
	}
	if err := s.auth.DestroyOthers(ctx, u.ID, 0); err != nil {
		return cli.Errorf("password changed, but ending the sessions of %s failed: %v", u.Login, err)
	}
	if err := s.revokeUserCredentials(ctx, u.ID, 0, 0); err != nil {
		return cli.Errorf("password changed, but revoking the application passwords and OAuth grants of %s failed: %v", u.Login, err)
	}
	if s.adminsecOn() {
		if _, err := s.store.Pool.Exec(ctx, `UPDATE users SET meta = meta - 'adminsec_force_password' WHERE id = $1`, u.ID); err != nil {
			return err
		}
		if _, err := s.store.Pool.Exec(ctx, `DELETE FROM adminsec_devices WHERE user_id = $1`, u.ID); err != nil {
			return err
		}
		cliUserAudit(c, e, s, asEvent{Event: "password_changed", Severity: "notice", Object: u.Login, Message: "Password changed for " + u.Login + "."})
	}
	return nil
}

// cliUserListAll pages through QueryUsers: number < 0 means every user.
func cliUserListAll(ctx context.Context, st *store.Store, q store.UserQuery, number int) ([]*store.User, error) {
	if number >= 0 {
		q.PerPage = number
		if number == 0 {
			return nil, nil
		}
		list, _, err := st.QueryUsers(ctx, q)
		return list, err
	}
	const page = 500
	q.PerPage = page
	var out []*store.User
	for q.Page = 1; ; q.Page++ {
		list, total, err := st.QueryUsers(ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, list...)
		if len(list) < page || len(out) >= total {
			return out, nil
		}
	}
}

func registerUserCoreCLI(r *cli.Registry, e *cliEnv) {
	r.Add(cli.Command{
		Path:  "user list",
		Short: "List users.",
		Long: "Lists users with their roles. Fields: ID, user_login, user_email, display_name, user_nicename, user_url,\n" +
			"user_registered, roles, first_name, last_name, nickname, description, locale.",
		Flags: []cli.Flag{
			{Name: "role", Usage: "Only users with this role."},
			{Name: "search", Usage: "Search login, e-mail, display name and URL (substring)."},
			{Name: "include", Usage: "Only these user IDs (comma-separated)."},
			{Name: "exclude", Usage: "Leave out these user IDs (comma-separated)."},
			{Name: "orderby", Usage: "Sort field.", Default: "login", Values: []string{"login", "email", "name", "id", "registered", "posts", "slug", "url"}},
			{Name: "order", Usage: "Sort direction.", Default: "asc", Values: []string{"asc", "desc", "ASC", "DESC"}},
			{Name: "number", Usage: "How many users (-1: all).", Default: "-1"},
			{Name: "paged", Usage: "Page number, with --number.", Default: "1"},
		},
		Examples: []string{"user list", "user list --role=administrator --field=user_login", "user list --search=example.com --format=csv", "user list --format=ids"},
		Run: func(c *cli.Ctx) error {
			st, err := e.Store(c.Context)
			if err != nil {
				return err
			}
			number, err := c.Int("number", -1)
			if err != nil {
				return err
			}
			paged, err := c.Int("paged", 1)
			if err != nil {
				return err
			}
			q := store.UserQuery{Role: c.Flag("role"), Search: c.Flag("search"), OrderBy: c.Flag("orderby"), Order: c.Flag("order"), Page: paged}
			if q.IDs, err = cliUserIDList(c.Flag("include")); err != nil {
				return err
			}
			if q.Exclude, err = cliUserIDList(c.Flag("exclude")); err != nil {
				return err
			}
			list, err := cliUserListAll(c.Context, st, q, number)
			if err != nil {
				return err
			}
			rows := make([]cli.Row, 0, len(list))
			for _, u := range list {
				rows = append(rows, cliUserRow(u))
			}
			return c.Print(rows, cli.View{Fields: cliUserListFields, ID: "ID"})
		},
	})

	r.Add(cli.Command{
		Path:     "user get",
		Short:    "Get a user's details.",
		Args:     []cli.Arg{{Name: "user", Usage: "User ID, e-mail or login."}},
		Examples: []string{"user get admin", "user get 12 --field=user_email", "user get admin@example.com --format=json"},
		Run: func(c *cli.Ctx) error {
			u, err := e.User(c.Context, c.Arg(0))
			if err != nil {
				return err
			}
			row := cliUserRow(u)
			pool, err := e.Pool(c.Context)
			if err != nil {
				return err
			}
			var last *time.Time
			if err := pool.QueryRow(c.Context, `SELECT last_login_at FROM users WHERE id = $1`, u.ID).Scan(&last); err != nil {
				return err
			}
			if last != nil {
				row["last_login_at"] = *last
			} else {
				row["last_login_at"] = ""
			}
			return c.PrintItem(row, cliUserGetFields)
		},
	})

	r.Add(cli.Command{
		Path:  "user exists",
		Short: "Check whether a user exists (exit code 0 or 1).",
		Args:  []cli.Arg{{Name: "user", Usage: "User ID, e-mail or login."}},
		Run: func(c *cli.Ctx) error {
			if _, err := e.User(c.Context, c.Arg(0)); err != nil {
				if strings.HasPrefix(err.Error(), "invalid user") {
					return cli.Exit(cli.ExitFailure)
				}
				return err
			}
			c.Success("User with ID, e-mail or login %s exists.", c.Arg(0))
			return nil
		},
	})

	r.Add(cli.Command{
		Path:  "user create",
		Short: "Create a user.",
		Long: "Creates a user with one role (default: the site's default_role setting). Without --user_pass a random\n" +
			"password is generated and printed once. The Pursae Security password policy applies unless\n" +
			"--skip-password-policy is given. No new-user e-mail is sent and plugin hooks do not run.\n" +
			"--user_pass_hash moves a user from WordPress: the stored hash is kept, so the old password keeps\n" +
			"working (the password policy cannot be checked against a hash).",
		Args: []cli.Arg{{Name: "user-login", Usage: "Username."}, {Name: "user-email", Usage: "E-mail address."}},
		Flags: []cli.Flag{
			{Name: "role", Usage: "Role slug (see `role list`)."},
			{Name: "user_pass", Usage: "Password (prefer the generated one: command lines end up in shell history)."},
			{Name: "user_pass_hash", Usage: "A WordPress user_pass hash to keep as-is (migration): $wp$2y$, $P$/$H$ phpass, $2y$ bcrypt or MD5. It is rehashed to argon2id at the first sign-in."},
			{Name: "display_name", Usage: "Display name (default: the login)."},
			{Name: "first_name", Usage: "First name."},
			{Name: "last_name", Usage: "Last name."},
			{Name: "nickname", Usage: "Nickname (default: the login)."},
			{Name: "description", Usage: "Biographical info."},
			{Name: "user_url", Usage: "Website."},
			{Name: "skip-password-policy", Bool: true, Usage: "Do not apply the Pursae Security password policy."},
			{Name: "porcelain", Bool: true, Usage: "Print only the new user's ID."},
		},
		Examples: []string{"user create bob bob@example.com --role=author", "user create editor1 ed@example.com --role=editor --porcelain",
			"user create alice alice@example.com --user_pass_hash='$wp$2y$10$...'"},
		Run: func(c *cli.Ctx) error { return cliUserCreate(c, e) },
	})

	r.Add(cli.Command{
		Path:  "user update",
		Short: "Update one or more users.",
		Long: "Changes profile fields. --user_pass sets a new password with the same effects as reset-password\n" +
			"(every session ends, application passwords and OAuth grants are revoked). The locale goes to the\n" +
			"users.locale column ('' = site default).",
		Args: []cli.Arg{{Name: "user", Usage: "User ID, e-mail or login.", Repeat: true}},
		Flags: []cli.Flag{
			{Name: "user_email", Usage: "E-mail address (one user only)."},
			{Name: "display_name", Usage: "Display name."},
			{Name: "user_url", Usage: "Website."},
			{Name: "user_nicename", Usage: "Author slug (one user only)."},
			{Name: "first_name", Usage: "First name."},
			{Name: "last_name", Usage: "Last name."},
			{Name: "nickname", Usage: "Nickname."},
			{Name: "description", Usage: "Biographical info."},
			{Name: "locale", Usage: "Locale such as de_DE ('' = site default)."},
			{Name: "user_pass", Usage: "New password."},
			{Name: "skip-password-policy", Bool: true, Usage: "Do not apply the Pursae Security password policy."},
		},
		Examples: []string{"user update bob --display_name='Bob Smith'", "user update 3 4 5 --locale=fr_FR", "user update bob --user_email=bob@new.example"},
		Run:      func(c *cli.Ctx) error { return cliUserUpdate(c, e) },
	})

	r.Add(cli.Command{
		Path:  "user reset-password",
		Short: "Set a new password for one or more users.",
		Long: "Sets a new password (generated unless --password is given; generated passwords are printed).\n" +
			"Every session of the user ends and its application passwords and OAuth grants are revoked, as on\n" +
			"a password change in the admin. Pursae does not mail a reset link from the command line.",
		Args: []cli.Arg{{Name: "user", Usage: "User ID, e-mail or login.", Repeat: true}},
		Flags: []cli.Flag{
			{Name: "password", Usage: "Use this password instead of generating one."},
			{Name: "show-password", Bool: true, Usage: "Also print a password given with --password."},
			{Name: "skip-password-policy", Bool: true, Usage: "Do not apply the Pursae Security password policy."},
			{Name: "porcelain", Bool: true, Usage: "Print only the new password (one user)."},
		},
		Examples: []string{"user reset-password admin", "user reset-password bob --password='correct horse battery staple'"},
		Run:      func(c *cli.Ctx) error { return cliUserResetPassword(c, e) },
	})

	r.Add(cli.Command{
		Path:  "user delete",
		Short: "Delete one or more users.",
		Long: "Deletes users. With --reassign their posts and media go to that user; without it their posts\n" +
			"(of post types that support authors) are deleted. Sessions, application passwords, roles and OAuth\n" +
			"grants go with the user. The last administrator cannot be deleted.",
		Args:     []cli.Arg{{Name: "user", Usage: "User ID, e-mail or login.", Repeat: true}},
		Flags:    []cli.Flag{{Name: "reassign", Usage: "User (ID, e-mail or login) who gets the deleted users' content."}},
		Examples: []string{"user delete bob --reassign=admin --yes", "user delete 12 13 --yes"},
		Run:      func(c *cli.Ctx) error { return cliUserDelete(c, e) },
	})
}

// cliUserIDList parses "1,2,3".
func cliUserIDList(v string) ([]int64, error) {
	var out []int64
	for _, p := range cli.SplitList(v) {
		id, err := cli.ParseID(p)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// cliUserEmailProblem validates an e-mail address the way user-new.php does.
func cliUserEmailProblem(email string) string {
	if _, err := mail.ParseAddress(email); err != nil || email == "" || !validEmail(email) {
		return "The email address is not correct."
	}
	return ""
}

// cliUserCreatePassword is the stored hash of `user create`: a WordPress hash given with
// --user_pass_hash is kept as-is (auth.CheckUserPassword verifies it and the first sign-in rehashes
// it), otherwise the --user_pass or generated password is checked against the policy and hashed.
func cliUserCreatePassword(c *cli.Ctx, s *Server, role, login, email string) (hash, pw string, generated bool, err error) {
	if c.Has("user_pass_hash") {
		if c.Has("user_pass") {
			return "", "", false, cli.Usagef("--user_pass and --user_pass_hash cannot be combined")
		}
		h := strings.TrimSpace(c.Flag("user_pass_hash"))
		if !auth.IsWordPressHash(h) {
			return "", "", false, cli.Errorf("--user_pass_hash is not a supported WordPress password hash ($wp$2y$…, $P$…, $H$…, $2y$… with cost 4-14, or 32-digit MD5)")
		}
		return h, "", false, nil
	}
	pw, generated = c.Flag("user_pass"), false
	if !c.Has("user_pass") {
		pw, generated = cliUserGeneratePassword(), true
	}
	if msg := cliUserPasswordProblem(pw); msg != "" {
		return "", "", false, cli.Errorf("%s", msg)
	}
	if !c.Bool("skip-password-policy") {
		if msg := s.adminsecPasswordProblem([]string{role}, login, email, pw); msg != "" {
			return "", "", false, cli.Errorf("%s (or pass --skip-password-policy)", msg)
		}
	}
	hash, err = auth.HashPassword(pw)
	return hash, pw, generated, err
}

func cliUserCreate(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	login := strings.TrimSpace(c.Arg(0))
	email := strings.TrimSpace(c.Arg(1))
	role := c.Flag("role")
	if role == "" {
		role = s.settings.String("default_role")
	}
	roles, err := s.store.Roles(ctx)
	if err != nil {
		return err
	}
	if !roleListed(roles, role) {
		return cli.Errorf("invalid role: '%s' (see '%s role list')", role, c.Prog)
	}
	if login == "" || !validUsername.MatchString(login) || len(login) > 60 {
		return cli.Errorf("this username is invalid because it uses illegal characters; please enter a valid username")
	}
	if msg := cliUserEmailProblem(email); msg != "" {
		return cli.Errorf("%s", msg)
	}
	lt, et, err := s.store.LoginOrEmailTaken(ctx, login, email)
	if err != nil {
		return err
	}
	if lt {
		return cli.Errorf("the username '%s' is already registered", login)
	}
	if et {
		return cli.Errorf("the e-mail address '%s' is already registered", email)
	}
	hash, pw, generated, err := cliUserCreatePassword(c, s, role, login, email)
	if err != nil {
		return err
	}
	profile := map[string]string{}
	for _, k := range []string{"first_name", "last_name", "nickname", "description"} {
		if c.Has(k) {
			profile[k] = strings.TrimSpace(c.Flag(k))
		}
	}
	id, err := s.store.CreateUser(ctx, store.NewUser{Login: login, Email: email, PasswordHash: hash, Role: role,
		DisplayName: strings.TrimSpace(c.Flag("display_name")), Profile: profile})
	if errors.Is(err, store.ErrLoginTaken) || errors.Is(err, store.ErrEmailTaken) {
		return cli.Errorf("%s", err.Error())
	}
	if err != nil {
		return err
	}
	if c.Has("user_url") {
		if nu, err := s.store.UserByID(ctx, id); err == nil {
			nu.URL = userURL(c.Flag("user_url"))
			if err := s.store.UpdateUserFields(ctx, nu); err != nil {
				return err
			}
		}
	}
	cliUserAudit(c, e, s, asEvent{Event: "user_created", Severity: "notice", Object: login, Message: "User " + login + " created with the role " + role + "."})
	if c.Bool("porcelain") {
		c.Println(id)
		if generated {
			fmt.Fprintf(c.Stderr(), "Password (shown once): %s\n", pw)
		}
		return nil
	}
	c.Success("Created user %d.", id)
	if generated {
		c.Println("Password: " + pw)
	}
	return nil
}

func cliUserUpdate(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	fields := []string{"user_email", "display_name", "user_url", "user_nicename", "first_name", "last_name", "nickname", "description", "locale"}
	changed := false
	for _, f := range fields {
		if c.Has(f) {
			changed = true
		}
	}
	if !changed && !c.Has("user_pass") {
		return cli.Usagef("nothing to update: pass at least one field flag")
	}
	if len(c.Args) > 1 && (c.Has("user_email") || c.Has("user_nicename")) {
		return cli.Usagef("--user_email and --user_nicename must be unique: update one user at a time")
	}
	if c.Has("user_pass") {
		if msg := cliUserPasswordProblem(c.Flag("user_pass")); msg != "" {
			return cli.Errorf("%s", msg)
		}
	}
	if c.Has("locale") {
		if loc := c.Flag("locale"); !validLanguage(loc) {
			return cli.Errorf("invalid locale: '%s'", loc)
		}
	}
	// Resolve and validate every user first, so a bad one changes nothing.
	var users []*store.User
	for _, ref := range c.Args {
		u, err := e.User(ctx, ref)
		if err != nil {
			return err
		}
		if c.Has("user_email") {
			v := strings.TrimSpace(c.Flag("user_email"))
			if msg := cliUserEmailProblem(v); msg != "" {
				return cli.Errorf("%s", msg)
			}
			if !strings.EqualFold(v, u.Email) {
				if _, taken, err := s.store.LoginOrEmailTaken(ctx, "", v); err != nil {
					return err
				} else if taken {
					return cli.Errorf("the e-mail address '%s' is already registered", v)
				}
			}
			u.Email = v
		}
		if c.Has("user_nicename") && c.Flag("user_nicename") != u.Nicename {
			slug := wp.SanitizeTitle(c.Flag("user_nicename"))
			taken, err := s.store.NicenameTaken(ctx, slug, u.ID)
			if err != nil {
				return err
			}
			if taken || slug == "" {
				return cli.Errorf("invalid or taken author slug: '%s'", c.Flag("user_nicename"))
			}
			u.Nicename = slug
		}
		if c.Has("display_name") {
			if dn := strings.TrimSpace(c.Flag("display_name")); dn != "" {
				u.DisplayName = dn
			}
		}
		if c.Has("user_url") {
			u.URL = userURL(c.Flag("user_url"))
		}
		if c.Has("locale") {
			u.Locale = c.Flag("locale")
			if u.Locale == "en_US" {
				u.Locale = ""
			}
		}
		for _, k := range []string{"first_name", "last_name", "nickname", "description"} {
			if c.Has(k) {
				u.Profile[k] = strings.TrimSpace(c.Flag(k))
			}
		}
		if c.Has("user_pass") && !c.Bool("skip-password-policy") {
			if msg := s.adminsecUserPasswordProblem(u, c.Flag("user_pass")); msg != "" {
				return cli.Errorf("%s: %s (or pass --skip-password-policy)", u.Login, msg)
			}
		}
		users = append(users, u)
	}
	for _, u := range users {
		if changed {
			if err := s.store.UpdateUserFields(ctx, u); err != nil {
				if errors.Is(err, store.ErrEmailTaken) || errors.Is(err, store.ErrLoginTaken) {
					return cli.Errorf("%s", err.Error())
				}
				return err
			}
		}
		if c.Has("user_pass") {
			if err := cliUserSetPassword(c, e, s, u, c.Flag("user_pass")); err != nil {
				return err
			}
		}
		c.Success("Updated user %d.", u.ID)
	}
	// Names appear in bylines and comment lists across the site (the profile screen purges too).
	return e.PurgeAll(ctx)
}

func cliUserResetPassword(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	given := c.Has("password")
	if given {
		if msg := cliUserPasswordProblem(c.Flag("password")); msg != "" {
			return cli.Errorf("%s", msg)
		}
	}
	if c.Bool("porcelain") && len(c.Args) > 1 {
		return cli.Usagef("--porcelain takes one user")
	}
	var users []*store.User
	for _, ref := range c.Args {
		u, err := e.User(ctx, ref)
		if err != nil {
			return err
		}
		users = append(users, u)
	}
	for _, u := range users {
		pw := c.Flag("password")
		if !given {
			pw = cliUserGeneratePassword()
		}
		if !c.Bool("skip-password-policy") {
			if msg := s.adminsecUserPasswordProblem(u, pw); msg != "" {
				return cli.Errorf("%s: %s (or pass --skip-password-policy)", u.Login, msg)
			}
		}
		if err := cliUserSetPassword(c, e, s, u, pw); err != nil {
			return err
		}
		show := !given || c.Bool("show-password")
		if c.Bool("porcelain") {
			c.Println(pw)
			continue
		}
		c.Success("Password reset for %s (every session ended, application passwords and OAuth grants revoked).", u.Login)
		if show {
			c.Println("Password: " + pw)
		}
	}
	return nil
}

func cliUserDelete(c *cli.Ctx, e *cliEnv) error {
	ctx := c.Context
	s, err := e.Server(ctx)
	if err != nil {
		return err
	}
	var reassign *store.User
	if c.Has("reassign") {
		if reassign, err = e.User(ctx, c.Flag("reassign")); err != nil {
			return cli.Errorf("invalid --reassign user: %v", err)
		}
	}
	var users []*store.User
	seen := map[int64]bool{}
	for _, ref := range c.Args {
		u, err := e.User(ctx, ref)
		if err != nil {
			return err
		}
		if seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		if reassign != nil && reassign.ID == u.ID {
			return cli.Errorf("cannot reassign the content of %s to the user being deleted", u.Login)
		}
		users = append(users, u)
	}
	if reassign != nil && seen[reassign.ID] {
		return cli.Errorf("the --reassign user is also being deleted")
	}
	for _, u := range users {
		if s.removingLastAdmin(ctx, u, nil) {
			return cli.Errorf("%s is the last administrator and cannot be deleted", u.Login)
		}
	}
	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Login)
	}
	what := "and delete their posts"
	if reassign != nil {
		what = "and give their content to " + reassign.Login
	}
	if err := c.Confirm("Permanently delete %s %s?", strings.Join(names, ", "), what); err != nil {
		return err
	}
	var to int64
	if reassign != nil {
		to = reassign.ID
	}
	for _, u := range users {
		// Checked again: deleting the previous users may have made this one the last administrator.
		if s.removingLastAdmin(ctx, u, nil) {
			return cli.Errorf("%s is the last administrator and cannot be deleted", u.Login)
		}
		if err := s.store.DeleteUser(ctx, u.ID, to); err != nil {
			return err
		}
		cliUserAudit(c, e, s, asEvent{Event: "user_deleted", Severity: "warning", Object: u.Login, Message: "User " + u.Login + " deleted."})
		if to > 0 {
			c.Success("Removed user %d (%s); content reassigned to %d.", u.ID, u.Login, to)
		} else {
			c.Success("Removed user %d (%s).", u.ID, u.Login)
		}
	}
	return e.PurgeAll(ctx)
}

// cliUserReadValue returns a positional value, or stdin when it is "-".
func cliUserReadValue(c *cli.Ctx, v string) (string, error) {
	if v != "-" {
		return v, nil
	}
	b, err := io.ReadAll(c.Stdin())
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
