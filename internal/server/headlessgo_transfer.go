package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"wpgo/internal/config"
	"wpgo/internal/settings"
	"wpgo/internal/store"
)

// Entry export and import (the schemas have their own export, hgExportSchemas). The format is
// NDJSON, one entry version per line, so it streams and a single type can be restored from a backup:
//
//	{"pursae":"headless-entries","version":1,"generator":"Pursae Headless 0.1.8"}
//	{"type":"api::article.article","documentId":"...","locale":"en","data":{...},"publishedData":{...}|null,
//	 "publishedAt":"...","createdAt":"...","updatedAt":"...","media":{"12":"https://.../a.jpg"}}
//
// data and publishedData are the stored values: relations to entries are document ids, so they
// survive a move to another site; relations to WordPress posts, pages and users are numeric ids
// (they only mean something on a site with the same posts), and media are Media Library ids, listed
// with their URLs in "media" so a migration can re-upload them. Import needs the schemas first
// (import them with the schema export), skips or overwrites entries that already exist and drops
// media ids that do not exist on the target site (reported as warnings).

const hgTransferFormat = "headless-entries"

type hgTransferLine struct {
	Type          string            `json:"type"`
	DocumentID    string            `json:"documentId"`
	Locale        string            `json:"locale"`
	Data          map[string]any    `json:"data"`
	PublishedData map[string]any    `json:"publishedData"`
	PublishedAt   *time.Time        `json:"publishedAt"`
	CreatedAt     *time.Time        `json:"createdAt,omitempty"`
	UpdatedAt     *time.Time        `json:"updatedAt,omitempty"`
	Media         map[string]string `json:"media,omitempty"`
}

// hgMediaIDs collects the media ids stored in values of schema sc (through components and dynamic zones).
func hgMediaIDs(reg *hgRegistry, sc *hgSchema, data map[string]any, out map[int64]bool, depth int) {
	if depth > 8 {
		return
	}
	for i := range sc.Fields {
		f := &sc.Fields[i]
		v, ok := data[f.Name]
		if !ok || v == nil {
			continue
		}
		switch f.Type {
		case "media":
			if l, ok := v.([]any); ok {
				for _, x := range l {
					if id, ok := hgToInt(x); ok {
						out[id] = true
					}
				}
			}
		case "component":
			comp := reg.Get(f.Component)
			if comp == nil {
				continue
			}
			if f.Repeatable {
				l, _ := v.([]any)
				for _, x := range l {
					if m, ok := x.(map[string]any); ok {
						hgMediaIDs(reg, comp, m, out, depth+1)
					}
				}
			} else if m, ok := v.(map[string]any); ok {
				hgMediaIDs(reg, comp, m, out, depth+1)
			}
		case "dynamiczone":
			l, _ := v.([]any)
			for _, x := range l {
				if m, ok := x.(map[string]any); ok {
					if uid, _ := m["__component"].(string); uid != "" {
						if comp := reg.Get(uid); comp != nil {
							hgMediaIDs(reg, comp, m, out, depth+1)
						}
					}
				}
			}
		}
	}
}

// hgDropMedia removes media ids that are not in keep, in place; it returns how many were removed.
func hgDropMedia(reg *hgRegistry, sc *hgSchema, data map[string]any, keep map[int64]bool, depth int) int {
	if depth > 8 {
		return 0
	}
	dropped := 0
	for i := range sc.Fields {
		f := &sc.Fields[i]
		v, ok := data[f.Name]
		if !ok || v == nil {
			continue
		}
		switch f.Type {
		case "media":
			l, _ := v.([]any)
			var kept []any
			for _, x := range l {
				if id, ok := hgToInt(x); ok && keep[id] {
					kept = append(kept, x)
				} else {
					dropped++
				}
			}
			if len(kept) == 0 {
				delete(data, f.Name)
			} else {
				data[f.Name] = kept
			}
		case "component":
			if comp := reg.Get(f.Component); comp != nil {
				if l, ok := v.([]any); ok {
					for _, x := range l {
						if m, ok := x.(map[string]any); ok {
							dropped += hgDropMedia(reg, comp, m, keep, depth+1)
						}
					}
				} else if m, ok := v.(map[string]any); ok {
					dropped += hgDropMedia(reg, comp, m, keep, depth+1)
				}
			}
		case "dynamiczone":
			l, _ := v.([]any)
			for _, x := range l {
				if m, ok := x.(map[string]any); ok {
					if uid, _ := m["__component"].(string); uid != "" {
						if comp := reg.Get(uid); comp != nil {
							dropped += hgDropMedia(reg, comp, m, keep, depth+1)
						}
					}
				}
			}
		}
	}
	return dropped
}

// hgExportEntries writes the entries of the given types (all content types when uids is empty) as NDJSON.
func (s *Server) hgExportEntries(ctx context.Context, w io.Writer, uids []string) (int, error) {
	reg := s.hgReg(ctx)
	var types []*hgSchema
	if len(uids) == 0 {
		types = reg.Types()
	} else {
		for _, uid := range uids {
			sc := reg.Get(uid)
			if sc == nil || sc.IsComponent() {
				return 0, fmt.Errorf("Unknown content type %q.", uid)
			}
			types = append(types, sc)
		}
	}
	enc := json.NewEncoder(w)
	if err := enc.Encode(map[string]any{"pursae": hgTransferFormat, "version": 1, "generator": "Pursae Headless " + Version}); err != nil {
		return 0, err
	}
	n := 0
	for _, sc := range types {
		rows, err := s.store.Pool.Query(ctx, `SELECT `+hgEntryCols+` FROM hg_entries WHERE type_uid = $1 ORDER BY id`, sc.UID)
		if err != nil {
			return n, err
		}
		var entries []*hgEntry
		for rows.Next() {
			e, err := hgScanEntry(rows)
			if err != nil {
				rows.Close()
				return n, err
			}
			entries = append(entries, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return n, err
		}
		for _, e := range entries {
			ids := map[int64]bool{}
			hgMediaIDs(reg, sc, e.Data, ids, 0)
			if e.Published != nil {
				hgMediaIDs(reg, sc, e.Published, ids, 0)
			}
			var media map[string]string
			if len(ids) > 0 {
				media = map[string]string{}
				for id := range ids {
					if a, err := s.store.GetAttachment(ctx, id); err == nil && a != nil {
						u, _ := s.hgMediaJSON(ctx, a)["url"].(string)
						media[strconv.FormatInt(id, 10)] = u
					}
				}
			}
			ca, ua := e.CreatedAt, e.UpdatedAt
			line := hgTransferLine{Type: sc.UID, DocumentID: e.DocumentID, Locale: e.Locale, Data: e.Data, PublishedData: e.Published,
				PublishedAt: e.PublishedAt, CreatedAt: &ca, UpdatedAt: &ua, Media: media}
			if err := enc.Encode(line); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

type hgImportResult struct {
	Created, Updated, Skipped int
	Warnings                  []string
}

// hgImportEntries reads an NDJSON entry export. With overwrite an existing entry (same type,
// document and locale) is replaced, otherwise it is left as it is. Invalid lines stop the import
// before anything is written for them; everything valid before them stays (each line is atomic).
func (s *Server) hgImportEntries(ctx context.Context, r io.Reader, overwrite bool, userID int64) (hgImportResult, error) {
	var res hgImportResult
	reg := s.hgReg(ctx)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 32<<20)
	lineNo := 0
	headerSeen := false
	warn := func(format string, a ...any) {
		if len(res.Warnings) < 50 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(format, a...))
		}
	}
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &probe); err != nil {
			return res, fmt.Errorf("Line %d is not valid JSON.", lineNo)
		}
		if _, ok := probe["pursae"]; ok {
			var f string
			_ = json.Unmarshal(probe["pursae"], &f)
			if f != hgTransferFormat {
				return res, errors.New("The file is not a Headless entry export.")
			}
			headerSeen = true
			continue
		}
		if !headerSeen {
			return res, errors.New("The file is not a Headless entry export (the header line is missing).")
		}
		var ln hgTransferLine
		if err := json.Unmarshal([]byte(raw), &ln); err != nil {
			return res, fmt.Errorf("Line %d: %v", lineNo, err)
		}
		t := reg.Get(ln.Type)
		if t == nil || t.IsComponent() {
			return res, fmt.Errorf("Line %d: the content type %q does not exist here; import the schemas first.", lineNo, ln.Type)
		}
		if ln.DocumentID == "" || len(ln.DocumentID) > 64 || ln.Data == nil {
			return res, fmt.Errorf("Line %d: documentId and data are required.", lineNo)
		}
		loc := ln.Locale
		if !t.Localized {
			loc = ""
		} else if loc == "" {
			return res, fmt.Errorf("Line %d: the type is localized but the entry has no locale.", lineNo)
		}
		doc := ln.DocumentID
		if t.IsSingle() {
			if d := s.hgSingleDoc(ctx, t.UID); d != "" {
				doc = d
			}
		}
		pub := ln.PublishedData
		if !t.DraftAndPublish && pub == nil {
			pub = ln.Data
		}
		// Keep only attributes the schema knows (and no stray system keys); drop media that is missing here.
		ids := map[int64]bool{}
		for _, d := range []map[string]any{ln.Data, pub} {
			if d != nil {
				for k := range d {
					if t.Field(k) == nil {
						delete(d, k)
						warn("Line %d: unknown attribute %q dropped.", lineNo, k)
					}
				}
				hgMediaIDs(reg, t, d, ids, 0)
			}
		}
		if len(ids) > 0 {
			list := make([]int64, 0, len(ids))
			for id := range ids {
				list = append(list, id)
			}
			sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
			have, _ := s.store.GetAttachments(ctx, list)
			keep := map[int64]bool{}
			for id := range ids {
				if have[id] != nil {
					keep[id] = true
				}
			}
			dropped := 0
			for _, d := range []map[string]any{ln.Data, pub} {
				if d != nil {
					dropped += hgDropMedia(reg, t, d, keep, 0)
				}
			}
			if dropped > 0 {
				warn("Line %d (%s): %d media reference(s) do not exist on this site and were dropped.", lineNo, doc, dropped)
			}
		}
		dj, _ := json.Marshal(ln.Data)
		var pj any
		var pubAt *time.Time
		if pub != nil {
			b, _ := json.Marshal(pub)
			pj = b
			pubAt = ln.PublishedAt
			if pubAt == nil {
				now := time.Now()
				pubAt = &now
			}
		}
		created, updated := time.Now(), time.Now()
		if ln.CreatedAt != nil {
			created = *ln.CreatedAt
		}
		if ln.UpdatedAt != nil {
			updated = *ln.UpdatedAt
		}
		conflict := `DO NOTHING`
		if overwrite {
			conflict = `DO UPDATE SET data = EXCLUDED.data, published_data = EXCLUDED.published_data, published_at = EXCLUDED.published_at,
				updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by, publish_at = NULL, unpublish_at = NULL, schedule_error = ''`
		}
		var inserted bool
		err := s.store.Pool.QueryRow(ctx, `INSERT INTO hg_entries(document_id, type_uid, locale, data, published_data, published_at, created_at, updated_at, created_by, updated_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
			ON CONFLICT (type_uid, document_id, locale) `+conflict+` RETURNING (xmax = 0)`,
			doc, t.UID, loc, dj, pj, pubAt, created, updated, userID).Scan(&inserted)
		switch {
		case err != nil && errors.Is(err, pgx.ErrNoRows):
			res.Skipped++
		case err != nil:
			return res, fmt.Errorf("Line %d: %v", lineNo, err)
		case inserted:
			res.Created++
		default:
			res.Updated++
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	if !headerSeen {
		return res, errors.New("The file is not a Headless entry export (empty).")
	}
	return res, nil
}

// HeadlessCLI is `wpgo headless export|import`: the same code the admin screen uses, for backups,
// staging to production moves and migrations from a shell.
//
//	wpgo headless export [--type uid]... > entries.ndjson
//	wpgo headless import [--overwrite] < entries.ndjson
func HeadlessCLI(ctx context.Context, pool *pgxpool.Pool, args []string, in io.Reader, out io.Writer) error {
	st := settings.New(pool)
	if err := st.Load(ctx); err != nil {
		return err
	}
	s := &Server{cfg: config.Config{}, store: store.New(pool), settings: st}
	if len(args) == 0 {
		return errors.New("usage: wpgo headless export [--type uid]... | import [--overwrite]")
	}
	switch args[0] {
	case "export":
		var uids []string
		for i := 1; i < len(args); i++ {
			if args[i] == "--type" && i+1 < len(args) {
				i++
				uids = append(uids, args[i])
			} else {
				return errors.New("usage: wpgo headless export [--type uid]...")
			}
		}
		_, err := s.hgExportEntries(ctx, out, uids)
		return err
	case "import":
		overwrite := false
		for _, a := range args[1:] {
			if a != "--overwrite" {
				return errors.New("usage: wpgo headless import [--overwrite]")
			}
			overwrite = true
		}
		res, err := s.hgImportEntries(ctx, in, overwrite, 0)
		for _, w := range res.Warnings {
			fmt.Fprintln(out, "warning:", w)
		}
		fmt.Fprintf(out, "%d created, %d updated, %d skipped\n", res.Created, res.Updated, res.Skipped)
		return err
	}
	return errors.New("unknown headless command " + args[0])
}
