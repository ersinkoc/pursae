// Package store is the data access layer: one file per table group, plain SQL with pgx.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/caps"
	"wpgo/internal/wp"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

type User struct {
	ID           int64
	Login        string
	Email        string
	PasswordHash string
	Nicename     string
	DisplayName  string
	URL          string
	Locale       string
	Profile      map[string]string
	Meta         map[string]any
	RegisteredAt time.Time
	Roles        []string // role slugs, primary first
	Caps         caps.Grants
}

// Can checks a capability, mapping meta capabilities against post.
func (u *User) Can(cap string, post caps.Post) bool {
	if u == nil {
		return false
	}
	return caps.Can(u.ID, u.Caps, cap, post)
}

func (u *User) PrimaryRole() string {
	if u == nil || len(u.Roles) == 0 {
		return ""
	}
	return u.Roles[0]
}

const userCols = `id, login, email, password_hash, nicename, display_name, url, locale, profile, meta, registered_at`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	var profile, meta []byte
	err := row.Scan(&u.ID, &u.Login, &u.Email, &u.PasswordHash, &u.Nicename, &u.DisplayName, &u.URL, &u.Locale, &profile, &meta, &u.RegisteredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Profile = map[string]string{}
	_ = json.Unmarshal(profile, &u.Profile)
	u.Meta = map[string]any{}
	_ = json.Unmarshal(meta, &u.Meta)
	return u, nil
}

// loadGrants fills Roles and Caps from user_roles, roles and user_capabilities.
func (s *Store) loadGrants(ctx context.Context, u *User) error {
	return s.loadGrantsBatch(ctx, []*User{u})
}

// loadGrantsBatch is loadGrants for a page of users: two queries in all, whatever the page size.
func (s *Store) loadGrantsBatch(ctx context.Context, list []*User) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(list))
	byID := make(map[int64][]*User, len(list))
	for _, u := range list {
		if _, seen := byID[u.ID]; !seen {
			ids = append(ids, u.ID)
		}
		byID[u.ID] = append(byID[u.ID], u)
		u.Roles = nil
	}
	type grant struct {
		roles    []string
		roleCaps []map[string]bool
		exc      map[string]string
	}
	grants := make(map[int64]*grant, len(ids))
	for _, id := range ids {
		grants[id] = &grant{exc: map[string]string{}}
	}
	// is_primary DESC within each user keeps Roles[0] the primary role.
	rows, err := s.Pool.Query(ctx, `SELECT ur.user_id, r.slug, r.capabilities FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id WHERE ur.user_id = ANY($1) ORDER BY ur.user_id, ur.is_primary DESC, r.id`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var uid int64
		var slug string
		var raw []byte
		if err := rows.Scan(&uid, &slug, &raw); err != nil {
			rows.Close()
			return err
		}
		m := map[string]bool{}
		_ = json.Unmarshal(raw, &m)
		g := grants[uid]
		g.roles = append(g.roles, slug)
		g.roleCaps = append(g.roleCaps, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = s.Pool.Query(ctx, `SELECT user_id, capability, effect FROM user_capabilities WHERE user_id = ANY($1)`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var uid int64
		var c, e string
		if err := rows.Scan(&uid, &c, &e); err != nil {
			rows.Close()
			return err
		}
		grants[uid].exc[c] = e
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, g := range grants {
		resolved := caps.Resolve(g.roleCaps, g.exc)
		for _, u := range byID[id] {
			u.Roles = append([]string(nil), g.roles...)
			u.Caps = resolved
		}
	}
	return nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	u, err := scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	return u, s.loadGrants(ctx, u)
}

// UserByLoginOrEmail finds a user the way the login form does: by username, or by email when it contains "@".
func (s *Store) UserByLoginOrEmail(ctx context.Context, login string) (*User, error) {
	// ORDER BY id: rows that differ only by case (stored before the checks were case-insensitive)
	// resolve to the same, oldest account every time.
	byLogin := `SELECT ` + userCols + ` FROM users WHERE lower(login) = lower($1) ORDER BY id LIMIT 1`
	q := byLogin
	if strings.Contains(login, "@") {
		q = `SELECT ` + userCols + ` FROM users WHERE lower(email) = lower($1) ORDER BY id LIMIT 1`
	}
	u, err := scanUser(s.Pool.QueryRow(ctx, q, login))
	if errors.Is(err, ErrNotFound) && q != byLogin {
		// The e-mail wins, so nobody can shadow another account's address with a username;
		// a username that itself contains "@" is found second (wp_authenticate_email_password
		// falls back to the login the same way).
		u, err = scanUser(s.Pool.QueryRow(ctx, byLogin, login))
	}
	if err != nil {
		return nil, err
	}
	return u, s.loadGrants(ctx, u)
}

func (s *Store) UserByNicename(ctx context.Context, nicename string) (*User, error) {
	return scanUser(s.Pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE nicename = $1`, nicename))
}

type NewUser struct {
	Login, Email, PasswordHash, DisplayName, Role string
	Profile                                       map[string]string
}

// CreateUser inserts a user with one primary role.
func (s *Store) CreateUser(ctx context.Context, nu NewUser) (int64, error) {
	if nu.DisplayName == "" {
		nu.DisplayName = nu.Login
	}
	if nu.Profile == nil {
		nu.Profile = map[string]string{}
	}
	if _, ok := nu.Profile["nickname"]; !ok {
		nu.Profile["nickname"] = nu.Login
	}
	profile, _ := json.Marshal(nu.Profile)
	nicename := wp.SanitizeTitle(nu.Login)
	if nicename == "" {
		nicename = "user"
	}
	var id int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		base := nicename
		for i := 2; ; i++ {
			var taken bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE nicename=$1)`, nicename).Scan(&taken); err != nil {
				return err
			}
			if !taken {
				break
			}
			nicename = base + "-" + strconv.Itoa(i)
		}
		if err := tx.QueryRow(ctx, `INSERT INTO users(login, email, password_hash, nicename, display_name, profile)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			nu.Login, nu.Email, nu.PasswordHash, nicename, nu.DisplayName, profile).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO user_roles(user_id, role_id, is_primary)
			SELECT $1, id, true FROM roles WHERE slug = $2`, id, nu.Role)
		return err
	})
	return id, err
}

func (s *Store) UpdateUserProfile(ctx context.Context, u *User) error {
	profile, _ := json.Marshal(u.Profile)
	_, err := s.Pool.Exec(ctx, `UPDATE users SET email=$2, display_name=$3, url=$4, profile=$5 WHERE id=$1`,
		u.ID, u.Email, u.DisplayName, u.URL, profile)
	return err
}

func (s *Store) SetUserPassword(ctx context.Context, userID int64, hash string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, userID, hash)
	return err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UserNames returns display names for the given ids.
func (s *Store) UserNames(ctx context.Context, ids []int64) (map[int64]*User, error) {
	out := map[int64]*User{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+userCols+` FROM users WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, rows.Err()
}

// Authors lists users who may be assigned as post authors (edit_posts via any role).
func (s *Store) Authors(ctx context.Context) ([]*User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT `+prefixed("u", userCols)+` FROM users u
		JOIN user_roles ur ON ur.user_id = u.id JOIN roles r ON r.id = ur.role_id
		WHERE (r.capabilities->>'edit_posts')::boolean ORDER BY u.display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// prefixed qualifies a column list with a table alias, including columns inside coalesce(...).
func prefixed(alias, cols string) string {
	return colRe.ReplaceAllStringFunc(cols, func(word string) string {
		if strings.HasSuffix(word, "(") {
			return word // function name such as coalesce(
		}
		return alias + "." + word
	})
}

// colRe matches identifiers in our column lists, with a trailing "(" for function calls.
var colRe = regexp.MustCompile(`[a-z_][a-z0-9_]*\(?`)
