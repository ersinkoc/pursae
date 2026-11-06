package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"wpgo/internal/webbuilder"
)

// Theme Builder templates (docs/developer/web-builder-contract.md §11.1): posts of type
// webbuilder.TemplatePostType whose doc and snapshot are ordinary web_builder_docs rows. The type,
// display conditions, priority, preview object and starter are post meta, kept as strings like
// WordPress postmeta so they survive a WXR round trip unchanged: _wb_conditions holds a JSON
// array, _wb_priority a decimal. Readers also accept native JSON (an array, a number), which an
// importer or an older writer may leave. Only publish templates are active; draft = disabled.
//
// The store only persists: the condition grammar, limits and capability checks belong to
// internal/webbuilder and the REST layer. These queries ignore the Golylang language filter
// (QueryFilter): the resolver maps templates to translations itself.

// ErrTemplateType is returned when a template is created with a type outside
// webbuilder.TemplateTypes, or as TemplateSingle without conditions (it has no default).
var ErrTemplateType = errors.New("web builder: unknown template type")

// WBTemplate is one template with its Theme Builder meta.
type WBTemplate struct {
	ID         int64
	Title      string
	Type       webbuilder.TemplateType // "" when the meta is missing or unknown
	Status     string                  // publish (active), draft (disabled), trash, …
	Conditions []string                // never nil
	Priority   int                     // clamped to PriorityMin…PriorityMax
	Preview    string                  // preview object, "" = the type's default
	Starter    webbuilder.StarterID
	Dup        bool // _wb_dup: the editor still has to regenerate the section ids
	AuthorID   int64
	AuthorName string // display name, "" when the author is gone
	ModifiedAt time.Time
	CreatedAt  time.Time
}

// WBTemplateFilter narrows Templates. The zero value lists every template except trash and
// auto-drafts, newest first.
type WBTemplateFilter struct {
	Type     webbuilder.TemplateType // "" = every type
	Statuses []string                // nil = everything but trash and auto-draft
	Search   string                  // title contains (case-insensitive)
	Limit    int                     // 0 = no limit
	Offset   int
}

// WBTemplateIndexEntry is one published template as the request-time resolver needs it.
type WBTemplateIndexEntry struct {
	ID         int64
	Type       webbuilder.TemplateType
	Conditions []string
	Priority   int
	ModifiedAt time.Time
	HasHTML    bool // a builder doc with a non-empty snapshot exists; templates without one never render
}

// WBTemplateCounts are the Theme Builder screen's view counts (subsubsub).
type WBTemplateCounts struct {
	All    int                             // every template but trash and auto-draft
	ByType map[webbuilder.TemplateType]int // the same, per type (known types only)
	Trash  int
}

// NewWBTemplate is a template to create as a draft. Zero fields take the type's defaults.
type NewWBTemplate struct {
	Type       webbuilder.TemplateType
	Title      string               // "" = the type label
	Starter    webbuilder.StarterID // "" = DefaultStarters[Type]
	Conditions []string             // nil = DefaultConditions[Type] (required for TemplateSingle)
	Priority   int
	Preview    string // "" = the type's default
	AuthorID   int64
}

const wbTemplateCols = `p.id, p.title, p.status, coalesce(p.author_id,0), coalesce(u.display_name,''),
	p.modified_at, p.created_at, p.meta->'` + webbuilder.MetaTemplateType + `', p.meta->'` + webbuilder.MetaConditions + `',
	p.meta->'` + webbuilder.MetaPriority + `', p.meta->'` + webbuilder.MetaPreview + `', p.meta->'` + webbuilder.MetaStarter + `',
	p.meta->'` + webbuilder.MetaDup + `'`

const wbTemplateFrom = ` FROM posts p LEFT JOIN users u ON u.id = p.author_id WHERE p.type = '` + webbuilder.TemplatePostType + `'`

func scanWBTemplate(row pgx.Row) (*WBTemplate, error) {
	t := &WBTemplate{}
	var typ, conds, prio, preview, starter, dup []byte
	err := row.Scan(&t.ID, &t.Title, &t.Status, &t.AuthorID, &t.AuthorName, &t.ModifiedAt, &t.CreatedAt,
		&typ, &conds, &prio, &preview, &starter, &dup)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Type = wbTemplateType(metaText(typ))
	t.Conditions = wbConditionsMeta(conds)
	t.Priority = wbPriorityMeta(prio)
	t.Preview = metaText(preview)
	t.Starter = webbuilder.StarterID(metaText(starter))
	switch metaText(dup) {
	case "1", "true":
		t.Dup = true
	}
	return t, nil
}

// metaText reads a jsonb meta value as text: a string as is, a number or bool in its JSON
// spelling, null or a missing key as "".
func metaText(raw []byte) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func wbTemplateType(s string) webbuilder.TemplateType {
	t := webbuilder.TemplateType(s)
	if _, ok := webbuilder.TemplateTypeLabels[t]; ok {
		return t
	}
	return ""
}

// wbConditionsMeta decodes _wb_conditions: a string holding a JSON array (as written) or a
// native array. Anything else, and non-string items, are dropped.
func wbConditionsMeta(raw []byte) []string {
	out := []string{}
	if len(raw) == 0 {
		return out
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = []byte(s)
	}
	var items []any
	if json.Unmarshal(raw, &items) != nil {
		return out
	}
	for _, it := range items {
		if v, ok := it.(string); ok {
			out = append(out, v)
		}
	}
	return out
}

// wbPriorityMeta decodes _wb_priority (a decimal string or a number), clamped; bad = 0.
func wbPriorityMeta(raw []byte) int {
	n, err := strconv.ParseFloat(strings.TrimSpace(metaText(raw)), 64)
	if err != nil || math.IsNaN(n) {
		return 0
	}
	return int(math.Max(webbuilder.PriorityMin, math.Min(webbuilder.PriorityMax, math.Trunc(n))))
}

func clampPriority(n int) int {
	return min(max(n, webbuilder.PriorityMin), webbuilder.PriorityMax)
}

func wbConditionsJSON(conds []string) string {
	if conds == nil {
		conds = []string{}
	}
	b, _ := json.Marshal(conds)
	return string(b)
}

// Templates lists templates, newest first (modified_at, then id).
func (w WebBuilderDB) Templates(ctx context.Context, f WBTemplateFilter) ([]*WBTemplate, error) {
	var args []any
	add := bindArg(&args)
	sql := `SELECT ` + wbTemplateCols + wbTemplateFrom
	if len(f.Statuses) > 0 {
		sql += ` AND p.status = ANY(` + add(f.Statuses) + `)`
	} else {
		sql += ` AND p.status NOT IN ('trash','auto-draft')`
	}
	if f.Type != "" {
		sql += ` AND p.meta->>'` + webbuilder.MetaTemplateType + `' = ` + add(string(f.Type))
	}
	if q := strings.TrimSpace(f.Search); q != "" {
		sql += ` AND p.title ILIKE ` + add("%"+likeEscape(q)+"%")
	}
	sql += ` ORDER BY p.modified_at DESC, p.id DESC`
	if f.Limit > 0 {
		sql += ` LIMIT ` + add(f.Limit) + ` OFFSET ` + add(max(f.Offset, 0))
	}
	rows, err := w.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WBTemplate{}
	for rows.Next() {
		t, err := scanWBTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// likeEscape escapes the ILIKE wildcards of a search string (backslash is the default escape).
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Template loads one template in any status; ErrNotFound when id is not a template.
func (w WebBuilderDB) Template(ctx context.Context, id int64) (*WBTemplate, error) {
	return scanWBTemplate(w.q.QueryRow(ctx, `SELECT `+wbTemplateCols+wbTemplateFrom+` AND p.id = $1`, id))
}

// TemplateCounts counts templates per type (trash and auto-drafts excluded) and in the trash.
func (w WebBuilderDB) TemplateCounts(ctx context.Context) (WBTemplateCounts, error) {
	c := WBTemplateCounts{ByType: map[webbuilder.TemplateType]int{}}
	rows, err := w.q.Query(ctx, `SELECT coalesce(meta->>'`+webbuilder.MetaTemplateType+`',''), status = 'trash', count(*)
		FROM posts WHERE type = '`+webbuilder.TemplatePostType+`' AND status <> 'auto-draft' GROUP BY 1, 2`)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var trash bool
		var n int
		if err := rows.Scan(&typ, &trash, &n); err != nil {
			return c, err
		}
		if trash {
			c.Trash += n
			continue
		}
		c.All += n
		if t := wbTemplateType(typ); t != "" {
			c.ByType[t] += n
		}
	}
	return c, rows.Err()
}

// CreateTemplate inserts a draft template with its meta and returns it (with ID and dates).
func (w WebBuilderDB) CreateTemplate(ctx context.Context, n NewWBTemplate) (*WBTemplate, error) {
	label, ok := webbuilder.TemplateTypeLabels[n.Type]
	if !ok {
		return nil, ErrTemplateType
	}
	conds := n.Conditions
	if conds == nil {
		def, ok := webbuilder.DefaultConditions[n.Type]
		if !ok {
			return nil, ErrTemplateType // TemplateSingle: the caller names the post type's rule
		}
		conds = slices.Clone(def)
	}
	title := strings.TrimSpace(n.Title)
	if title == "" {
		title = label
	}
	starter := n.Starter
	if starter == "" {
		starter = webbuilder.DefaultStarters[n.Type]
	}
	t := &WBTemplate{Title: title, Type: n.Type, Status: "draft", Conditions: conds, Priority: clampPriority(n.Priority),
		Preview: n.Preview, Starter: starter, AuthorID: n.AuthorID}
	meta, _ := json.Marshal(map[string]string{
		webbuilder.MetaTemplateType: string(t.Type),
		webbuilder.MetaConditions:   wbConditionsJSON(t.Conditions),
		webbuilder.MetaPriority:     strconv.Itoa(t.Priority),
		webbuilder.MetaPreview:      t.Preview,
		webbuilder.MetaStarter:      string(t.Starter),
	})
	err := w.q.QueryRow(ctx, `INSERT INTO posts(type, author_id, title, status, comment_status, ping_status, meta)
		VALUES ($1, $2, $3, 'draft', 'closed', 'closed', $4) RETURNING id, created_at, modified_at`,
		webbuilder.TemplatePostType, nullable(n.AuthorID), title, meta).Scan(&t.ID, &t.CreatedAt, &t.ModifiedAt)
	if err != nil {
		return nil, err
	}
	if n.AuthorID != 0 {
		_ = w.q.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1`, n.AuthorID).Scan(&t.AuthorName)
	}
	return t, nil
}

// setTemplateMeta merges meta keys into a template's meta; ErrNotFound when id is not a template.
// Like update_post_meta it leaves modified_at alone.
func (w WebBuilderDB) setTemplateMeta(ctx context.Context, id int64, kv map[string]string) error {
	b, _ := json.Marshal(kv)
	tag, err := w.q.Exec(ctx, `UPDATE posts SET meta = meta || $2::jsonb, updated_at = now()
		WHERE id = $1 AND type = '`+webbuilder.TemplatePostType+`'`, id, b)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetTemplateConditions stores a template's display conditions (already validated and
// normalized by the caller) and priority (clamped).
func (w WebBuilderDB) SetTemplateConditions(ctx context.Context, id int64, conditions []string, priority int) error {
	return w.setTemplateMeta(ctx, id, map[string]string{
		webbuilder.MetaConditions: wbConditionsJSON(conditions),
		webbuilder.MetaPriority:   strconv.Itoa(clampPriority(priority)),
	})
}

// SetTemplatePreview stores a template's preview object ("" = the type's default).
func (w WebBuilderDB) SetTemplatePreview(ctx context.Context, id int64, object string) error {
	return w.setTemplateMeta(ctx, id, map[string]string{webbuilder.MetaPreview: object})
}

// SetTemplateDup sets or clears the duplicate marker (_wb_dup = "1"); clearing removes the key.
func (w WebBuilderDB) SetTemplateDup(ctx context.Context, id int64, on bool) error {
	if on {
		return w.setTemplateMeta(ctx, id, map[string]string{webbuilder.MetaDup: "1"})
	}
	tag, err := w.q.Exec(ctx, `UPDATE posts SET meta = meta - $2::text, updated_at = now()
		WHERE id = $1 AND type = '`+webbuilder.TemplatePostType+`'`, id, webbuilder.MetaDup)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PublishedTemplates is the resolver's template index: every published template of a known type
// in one query, in id order. HasHTML tells whether its builder snapshot exists and is non-empty.
func (w WebBuilderDB) PublishedTemplates(ctx context.Context) ([]WBTemplateIndexEntry, error) {
	rows, err := w.q.Query(ctx, `SELECT p.id, coalesce(p.meta->>'`+webbuilder.MetaTemplateType+`',''),
		p.meta->'`+webbuilder.MetaConditions+`', p.meta->'`+webbuilder.MetaPriority+`', p.modified_at,
		coalesce(d.html <> '', false)
		FROM posts p LEFT JOIN web_builder_docs d ON d.post_id = p.id
		WHERE p.type = '`+webbuilder.TemplatePostType+`' AND p.status = 'publish'
		ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WBTemplateIndexEntry{}
	for rows.Next() {
		var e WBTemplateIndexEntry
		var typ string
		var conds, prio []byte
		if err := rows.Scan(&e.ID, &typ, &conds, &prio, &e.ModifiedAt, &e.HasHTML); err != nil {
			return nil, err
		}
		if e.Type = wbTemplateType(typ); e.Type == "" {
			continue
		}
		e.Conditions, e.Priority = wbConditionsMeta(conds), wbPriorityMeta(prio)
		out = append(out, e)
	}
	return out, rows.Err()
}
