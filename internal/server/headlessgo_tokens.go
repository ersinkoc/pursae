package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"wpgo/internal/auth"
)

// API tokens and preview tokens of HeadlessGo.
//
// An API token is "hg_" + 48 random characters. Only its SHA-256 is stored (the token is shown
// once, on creation or regeneration): tokens are long random strings, so a fast hash is enough
// and lets every request find its token with one indexed lookup. Kinds: read-only (find and
// findOne on every type), full-access (everything, drafts included) and custom (a matrix of
// type × action, plus an optional "may read drafts").
//
// A preview token is "hgp_" + base64url(uid|documentId|locale|expiry) + "." + an HMAC-SHA256 of it
// keyed by the preview secret and the site secret. It reads one document's draft (find, filtered
// to that document, or findOne) until it expires.

const (
	hgTokenPrefix   = "hg_"
	hgPreviewPrefix = "hgp_"
)

type hgToken struct {
	ID          int64
	Name        string
	Description string
	Kind        string
	Permissions map[string][]string
	Drafts      bool
	Hint        string
	ExpiresAt   *time.Time
	LastUsedAt  *time.Time
	CreatedAt   time.Time
}

func (t *hgToken) Expired() bool { return t.ExpiresAt != nil && !time.Now().Before(*t.ExpiresAt) }

func (t *hgToken) KindLabel() string {
	switch t.Kind {
	case "full-access":
		return "Full access"
	case "custom":
		return "Custom"
	}
	return "Read-only"
}

func (t *hgToken) Can(uid, action string) bool { return containsStr(t.Permissions[uid], action) }

func hgTokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

const hgTokenCols = `id, name, description, kind, permissions, drafts, token_hint, expires_at, last_used_at, created_at`

func hgScanToken(row interface{ Scan(...any) error }) (*hgToken, error) {
	t := &hgToken{}
	var perms []byte
	if err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Kind, &perms, &t.Drafts, &t.Hint, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(perms, &t.Permissions)
	if t.Permissions == nil {
		t.Permissions = map[string][]string{}
	}
	return t, nil
}

// hgTokenByValue finds a valid (unexpired) token and records its use (at most once a minute).
func (s *Server) hgTokenByValue(ctx context.Context, tok string) *hgToken {
	if !strings.HasPrefix(tok, hgTokenPrefix) || len(tok) > 200 {
		return nil
	}
	t, err := hgScanToken(s.store.Pool.QueryRow(ctx, `SELECT `+hgTokenCols+` FROM hg_api_tokens WHERE token_hash = $1`, hgTokenHash(tok)))
	if err != nil || t.Expired() {
		return nil
	}
	_, _ = s.store.Pool.Exec(context.WithoutCancel(ctx), `UPDATE hg_api_tokens SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, t.ID)
	return t
}

func (s *Server) hgTokens(ctx context.Context) []*hgToken {
	rows, err := s.store.Pool.Query(ctx, `SELECT `+hgTokenCols+` FROM hg_api_tokens ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*hgToken
	for rows.Next() {
		if t, err := hgScanToken(rows); err == nil {
			out = append(out, t)
		}
	}
	return out
}

func (s *Server) hgTokenByID(ctx context.Context, id int64) *hgToken {
	t, err := hgScanToken(s.store.Pool.QueryRow(ctx, `SELECT `+hgTokenCols+` FROM hg_api_tokens WHERE id = $1`, id))
	if err != nil {
		return nil
	}
	return t
}

// hgSanitizeToken validates a token's settings.
func hgSanitizeToken(t *hgToken, reg *hgRegistry) error {
	t.Name = strings.TrimSpace(t.Name)
	t.Description = strings.TrimSpace(t.Description)
	if t.Name == "" {
		return errors.New("The token needs a name.")
	}
	switch t.Kind {
	case "read-only", "full-access", "custom":
	default:
		t.Kind = "read-only"
	}
	perms := map[string][]string{}
	if t.Kind == "custom" {
		for uid, acts := range t.Permissions {
			if reg.Get(uid) == nil {
				continue
			}
			for _, a := range acts {
				if containsStr(hgActions, a) && !containsStr(perms[uid], a) {
					perms[uid] = append(perms[uid], a)
				}
			}
		}
	}
	t.Permissions = perms
	if t.Kind == "full-access" {
		t.Drafts = true
	}
	return nil
}

// hgCreateToken stores a new token and returns its value (shown once).
func (s *Server) hgCreateToken(ctx context.Context, t *hgToken) (string, error) {
	if err := hgSanitizeToken(t, s.hgReg(ctx)); err != nil {
		return "", err
	}
	var dup bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hg_api_tokens WHERE lower(name) = lower($1))`, t.Name).Scan(&dup)
	if dup {
		return "", errors.New("A token with this name already exists.")
	}
	tok := hgTokenPrefix + auth.RandomToken(36)
	perms, _ := json.Marshal(t.Permissions)
	err := s.store.Pool.QueryRow(ctx, `INSERT INTO hg_api_tokens(name, description, kind, permissions, drafts, token_hash, token_hint, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, t.Name, t.Description, t.Kind, perms, t.Drafts, hgTokenHash(tok), tok[len(tok)-4:], t.ExpiresAt).Scan(&t.ID)
	if err != nil {
		return "", err
	}
	return tok, nil
}

// hgUpdateToken saves a token's name, description, kind and permissions (not its value).
func (s *Server) hgUpdateToken(ctx context.Context, t *hgToken) error {
	if err := hgSanitizeToken(t, s.hgReg(ctx)); err != nil {
		return err
	}
	var dup bool
	_ = s.store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM hg_api_tokens WHERE lower(name) = lower($1) AND id <> $2)`, t.Name, t.ID).Scan(&dup)
	if dup {
		return errors.New("A token with this name already exists.")
	}
	perms, _ := json.Marshal(t.Permissions)
	_, err := s.store.Pool.Exec(ctx, `UPDATE hg_api_tokens SET name = $2, description = $3, kind = $4, permissions = $5, drafts = $6 WHERE id = $1`,
		t.ID, t.Name, t.Description, t.Kind, perms, t.Drafts)
	return err
}

// hgRegenerateToken replaces a token's value; the old one stops working.
func (s *Server) hgRegenerateToken(ctx context.Context, id int64) (string, error) {
	tok := hgTokenPrefix + auth.RandomToken(36)
	tag, err := s.store.Pool.Exec(ctx, `UPDATE hg_api_tokens SET token_hash = $2, token_hint = $3, last_used_at = NULL WHERE id = $1`, id, hgTokenHash(tok), tok[len(tok)-4:])
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "", errHGNotFound
	}
	return tok, nil
}

func (s *Server) hgDeleteToken(ctx context.Context, id int64) error {
	_, err := s.store.Pool.Exec(ctx, `DELETE FROM hg_api_tokens WHERE id = $1`, id)
	return err
}

// Preview -------------------------------------------------------------------------------------------

func (s *Server) hgPreviewKey(st hgSettings) []byte {
	return []byte(st.PreviewSecret + "|" + s.settings.String("auth_secret") + "|headlessgo-preview")
}

// hgPreviewToken signs a short-lived token reading one document's draft.
func (s *Server) hgPreviewToken(st hgSettings, uid, doc, locale string, ttl time.Duration) string {
	payload := uid + "|" + doc + "|" + locale + "|" + strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	m := hmac.New(sha256.New, s.hgPreviewKey(st))
	m.Write([]byte(enc))
	return hgPreviewPrefix + enc + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) hgCheckPreviewToken(st hgSettings, tok string) (uid, doc string, ok bool) {
	body, sig, found := strings.Cut(strings.TrimPrefix(tok, hgPreviewPrefix), ".")
	if !found || st.PreviewSecret == "" {
		return "", "", false
	}
	m := hmac.New(sha256.New, s.hgPreviewKey(st))
	m.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return "", "", false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return "", "", false
	}
	return parts[0], parts[1], true
}

var hgPlaceholderRe = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)

// hgPreviewURL fills a type's preview URL template for an entry: {documentId}, {locale},
// {status}, {secret}, {previewToken}, {uid}, {pluralApiId}, {singularApiId} and any top-level
// scalar field ({slug}). Values are URL-encoded. It returns "" without a template.
func (s *Server) hgPreviewURL(ctx context.Context, sc *hgSchema, e *hgEntry) string {
	if sc.PreviewURL == "" || e == nil {
		return ""
	}
	st := s.hgLoad()
	vals := map[string]string{"documentId": e.DocumentID, "locale": e.Locale, "status": "draft", "secret": st.PreviewSecret,
		"uid": sc.UID, "pluralApiId": sc.PluralName, "singularApiId": sc.SingularName}
	if e.Published != nil && e.Status() == "published" {
		vals["status"] = "published"
	}
	tokenUsed := strings.Contains(sc.PreviewURL, "{previewToken}")
	if tokenUsed {
		vals["previewToken"] = s.hgPreviewToken(st, sc.UID, e.DocumentID, e.Locale, time.Duration(st.PreviewTTL)*time.Minute)
	}
	for _, f := range sc.Fields {
		if f.scalar() && !f.Private {
			if v, ok := e.Data[f.Name]; ok && v != nil {
				switch t := v.(type) {
				case string:
					vals[f.Name] = t
				case float64:
					vals[f.Name] = strconv.FormatFloat(t, 'f', -1, 64)
				case bool:
					vals[f.Name] = strconv.FormatBool(t)
				}
			}
		}
	}
	return hgPlaceholderRe.ReplaceAllStringFunc(sc.PreviewURL, func(m string) string {
		key := m[1 : len(m)-1]
		if v, ok := vals[key]; ok {
			return url.QueryEscape(v)
		}
		return ""
	})
}
