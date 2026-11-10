package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/caps"
)

// Role is a row of the roles table.
type Role struct {
	ID   int64
	Slug string
	Name string
}

// Roles lists roles in WordPress order (most capable first).
func (s *Store) Roles(ctx context.Context) ([]Role, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, slug, name FROM roles ORDER BY
		array_position(ARRAY['administrator','editor','author','contributor','subscriber'], slug), name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Slug, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UserQuery filters users.php.
type UserQuery struct {
	Role    string
	Search  string
	OrderBy string // login, email, name, posts, id, registered, slug, url
	Order   string
	Page    int
	PerPage int
	// REST filters (WP_REST_Users_Controller): any of these roles, these ids / not these ids, a nicename,
	// and users with at least one published post (what visitors may list).
	Roles             []string
	IDs               []int64
	Exclude           []int64
	Nicenames         []string
	HasPublishedPosts bool
	// PublicSearch limits Search to id, login, nicename and display name, the columns
	// WP_REST_Users_Controller searches for callers without list_users (email would be an oracle).
	PublicSearch bool
	// Caps keeps users who have every one of these capabilities (roles + allow exceptions,
	// minus deny exceptions, exactly as caps.Resolve), resolved in SQL so the page stays a page.
	Caps []string
	// Offset, when positive, replaces the page-based offset (REST "offset").
	Offset int
}

// likeEscaper escapes LIKE metacharacters so a search term matches literally.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// likeContains is the LIKE/ILIKE pattern "contains term", with term's % and _ matched literally
// (backslash is PostgreSQL's default LIKE escape).
func likeContains(term string) string { return "%" + likeEscaper.Replace(term) + "%" }

// capCondition is the SQL for "user u has cap" with caps.Resolve's precedence: a deny exception
// wins, otherwise a role granting it or an allow exception. Meta capabilities that need a post
// (edit_post, ...) map to do_not_allow without one, as in User.Can(cap, nil).
func capCondition(cap string, arg func(any) string) string {
	var conds []string
	for _, prim := range caps.MapMetaCap(0, cap, nil) {
		if prim == "do_not_allow" {
			return "false"
		}
		p := arg(prim)
		conds = append(conds, `((EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = u.id AND r.capabilities @> jsonb_build_object(`+p+`::text, true))
			OR EXISTS (SELECT 1 FROM user_capabilities uc WHERE uc.user_id = u.id AND uc.capability = `+p+` AND uc.effect = 'allow'))
			AND NOT EXISTS (SELECT 1 FROM user_capabilities uc WHERE uc.user_id = u.id AND uc.capability = `+p+` AND uc.effect = 'deny'))`)
	}
	if len(conds) == 0 {
		return "true"
	}
	return strings.Join(conds, " AND ")
}

// QueryUsers returns one page of users (with roles and capabilities loaded) and the total.
func (s *Store) QueryUsers(ctx context.Context, q UserQuery) ([]*User, int, error) {
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	where = append(where, "true")
	if q.Role != "" {
		where = append(where, `EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = u.id AND r.slug = `+arg(q.Role)+`)`)
	}
	if term := strings.Trim(q.Search, "*"); term != "" {
		p := arg("%" + likeEscaper.Replace(strings.ToLower(term)) + "%")
		like := func(col string) string { return "lower(" + col + ") LIKE " + p + ` ESCAPE '\'` }
		if q.PublicSearch {
			conds := []string{like("u.login"), like("u.nicename"), like("u.display_name")}
			if id, err := strconv.ParseInt(term, 10, 64); err == nil && id > 0 {
				conds = append(conds, "u.id = "+arg(id))
			}
			where = append(where, "("+strings.Join(conds, " OR ")+")")
		} else {
			where = append(where, "("+like("u.login")+" OR "+like("u.email")+" OR "+like("u.display_name")+" OR "+like("u.url")+")")
		}
	}
	for _, cp := range q.Caps {
		if cp = strings.TrimSpace(cp); cp != "" {
			where = append(where, capCondition(cp, arg))
		}
	}
	if len(q.Roles) > 0 {
		where = append(where, `EXISTS (SELECT 1 FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = u.id AND r.slug = ANY(`+arg(q.Roles)+`))`)
	}
	if len(q.IDs) > 0 {
		where = append(where, "u.id = ANY("+arg(q.IDs)+")")
	}
	if len(q.Exclude) > 0 {
		where = append(where, "NOT (u.id = ANY("+arg(q.Exclude)+"))")
	}
	if len(q.Nicenames) > 0 {
		where = append(where, "u.nicename = ANY("+arg(q.Nicenames)+")")
	}
	if q.HasPublishedPosts {
		where = append(where, "EXISTS (SELECT 1 FROM posts p WHERE p.author_id = u.id AND p.status = 'publish' AND p.type IN ('post','page'))")
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users u WHERE `+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "lower(u.login)"
	switch q.OrderBy {
	case "email":
		order = "lower(u.email)"
	case "name":
		order = "lower(u.display_name)"
	case "posts":
		order = "(SELECT count(*) FROM posts p WHERE p.author_id = u.id AND p.type = 'post' AND p.status = 'publish')"
	case "id":
		order = "u.id"
	case "registered":
		order = "u.registered_at"
	case "slug":
		order = "u.nicename"
	case "url":
		order = "lower(u.url)"
	case "include":
		if len(q.IDs) > 0 {
			order = "array_position(" + arg(q.IDs) + "::bigint[], u.id)"
		}
	}
	dir := "ASC"
	if strings.EqualFold(q.Order, "desc") {
		dir = "DESC"
	}
	if q.PerPage <= 0 {
		q.PerPage = 20
	}
	if q.Page < 1 {
		q.Page = 1
	}
	window := limitOffset(q.PerPage, q.Page)
	if q.Offset > 0 {
		window = fmt.Sprintf(" LIMIT %d OFFSET %d", q.PerPage, q.Offset)
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+prefixed("u", userCols)+` FROM users u WHERE `+cond+` ORDER BY `+order+` `+dir+
		`, u.id`+window, args...)
	if err != nil {
		return nil, 0, err
	}
	var list []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		list = append(list, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.loadGrantsBatch(ctx, list); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// RoleCounts is count_users(): users per role plus "all".
func (s *Store) RoleCounts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.Pool.Query(ctx, `SELECT r.slug, count(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id GROUP BY r.slug`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var slug string
		var n int
		if err := rows.Scan(&slug, &n); err != nil {
			rows.Close()
			return nil, err
		}
		out[slug] = n
	}
	rows.Close()
	var all int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&all); err != nil {
		return nil, err
	}
	out["all"] = all
	return out, nil
}

// UserPostCounts is count_many_users_posts() for published posts.
func (s *Store) UserPostCounts(ctx context.Context, ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	rows, err := s.Pool.Query(ctx, `SELECT author_id, count(*) FROM posts WHERE author_id = ANY($1) AND type = 'post' AND status = 'publish' GROUP BY author_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// SetUserRoles replaces a user's roles: one primary role plus extra roles (the multi-role union).
func (s *Store) SetUserRoles(ctx context.Context, userID int64, primary string, extra []string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, userID); err != nil {
			return err
		}
		if primary == "" {
			return nil // "No role for this site"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id, role_id, is_primary) SELECT $1, id, true FROM roles WHERE slug = $2`, userID, primary); err != nil {
			return err
		}
		for _, r := range extra {
			if r == primary {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id, role_id, is_primary) SELECT $1, id, false FROM roles WHERE slug = $2
				ON CONFLICT DO NOTHING`, userID, r); err != nil {
				return err
			}
		}
		return nil
	})
}

// UserCapExceptions returns the per-user allow/deny capability overrides.
func (s *Store) UserCapExceptions(ctx context.Context, userID int64) (map[string]string, error) {
	out := map[string]string{}
	rows, err := s.Pool.Query(ctx, `SELECT capability, effect FROM user_capabilities WHERE user_id = $1 ORDER BY capability`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, e string
		if err := rows.Scan(&c, &e); err != nil {
			return nil, err
		}
		out[c] = e
	}
	return out, rows.Err()
}

// SetUserCapExceptions replaces the per-user overrides (deny always wins in caps.Resolve).
func (s *Store) SetUserCapExceptions(ctx context.Context, userID int64, exc map[string]string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM user_capabilities WHERE user_id = $1`, userID); err != nil {
			return err
		}
		for c, e := range exc {
			if e != "allow" && e != "deny" {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO user_capabilities(user_id, capability, effect) VALUES ($1,$2,$3)`, userID, c, e); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteUser is wp_delete_user(): content is reassigned to another user or deleted.
func (s *Store) DeleteUser(ctx context.Context, id, reassignTo int64) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if reassignTo > 0 {
			if _, err := tx.Exec(ctx, `UPDATE posts SET author_id = $2 WHERE author_id = $1`, id, reassignTo); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE media SET uploader_id = $2 WHERE uploader_id = $1`, id, reassignTo); err != nil {
				return err
			}
		} else {
			// wp_delete_user(): only post types that support "author" (delete_with_user unset) go
			// with the user. Products, variations, patterns, forms and field groups stay; the
			// users FK sets their author_id to NULL below.
			const withUser = `author_id = $1 AND type IN (SELECT slug FROM content_types WHERE supports @> '{"author": true}'::jsonb)`
			var termIDs []int64
			rows, err := tx.Query(ctx, `SELECT DISTINCT term_id FROM post_terms WHERE post_id IN (SELECT id FROM posts WHERE `+withUser+`)`, id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var t int64
				if err := rows.Scan(&t); err != nil {
					rows.Close()
					return err
				}
				termIDs = append(termIDs, t)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM posts WHERE `+withUser, id); err != nil {
				return err
			}
			if len(termIDs) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE terms t SET post_count = (SELECT count(*) FROM post_terms pt
					JOIN posts p ON p.id = pt.post_id WHERE pt.term_id = t.id AND p.status = 'publish')
					WHERE t.id = ANY($1)`, termIDs); err != nil {
					return err
				}
			}
		}
		// OAuth grants and pending codes have no foreign key on the user: a deleted user's tokens must not outlive it.
		for _, q := range []string{`DELETE FROM oauth_tokens WHERE user_id = $1`, `DELETE FROM oauth_codes WHERE user_id = $1`} {
			if _, err := tx.Exec(ctx, q, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
		return err
	})
}

// UpdateUserFields is wp_update_user() for the columns the REST users controller edits.
func (s *Store) UpdateUserFields(ctx context.Context, u *User) error {
	profile, _ := json.Marshal(u.Profile)
	_, err := s.Pool.Exec(ctx, `UPDATE users SET email=$2, display_name=$3, url=$4, profile=$5, nicename=$6, locale=$7, `+clearResetOnEmailChange+` WHERE id=$1`,
		u.ID, u.Email, u.DisplayName, u.URL, profile, u.Nicename, u.Locale)
	return userUniqueErr(err)
}

// NicenameTaken reports whether another user already has this nicename (slug).
func (s *Store) NicenameTaken(ctx context.Context, nicename string, excludeID int64) (bool, error) {
	var taken bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE nicename = $1 AND id <> $2)`, nicename, excludeID).Scan(&taken)
	return taken, err
}

// LoginOrEmailTaken reports whether a username or e-mail address is already registered.
func (s *Store) LoginOrEmailTaken(ctx context.Context, login, email string) (loginTaken, emailTaken bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE lower(login) = lower($1)),
		EXISTS(SELECT 1 FROM users WHERE lower(email) = lower($2))`, login, email).Scan(&loginTaken, &emailTaken)
	return
}
