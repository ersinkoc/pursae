package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

// HeadlessGo's Content-Type Builder: collection types, single types and components, described by
// JSON schemas stored in hg_schemas. Entries keep their values in JSONB (hg_entries), so a schema
// change never runs DDL; renamed and removed fields are migrated in the stored entry data
// (hgMigrateEntries).

const (
	hgKindCollection = "collectionType"
	hgKindSingle     = "singleType"
	hgKindComponent  = "component"
)

// hgField is one attribute of a schema.
type hgField struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Long        bool     `json:"long,omitempty"`         // text: a multi-line text area
	Format      string   `json:"format,omitempty"`       // richtext: blocks, markdown; number: integer, decimal
	Enum        []string `json:"enum,omitempty"`         // enumeration values
	Multiple    bool     `json:"multiple,omitempty"`     // media: several files
	Allowed     []string `json:"allowedTypes,omitempty"` // media: images, videos, audios, files
	Relation    string   `json:"relation,omitempty"`     // oneToOne, oneToMany, manyToOne, manyToMany
	Target      string   `json:"target,omitempty"`       // relation: a type uid or wp:post, wp:page, wp:user
	InversedBy  string   `json:"inversedBy,omitempty"`   // relation (owning side): the inverse field on the target type
	MappedBy    string   `json:"mappedBy,omitempty"`     // relation (inverse side): the owning field on the target type; nothing is stored
	TargetField string   `json:"targetField,omitempty"`  // uid: the text field it is derived from
	Component   string   `json:"component,omitempty"`    // component: its uid
	Repeatable  bool     `json:"repeatable,omitempty"`   // component: a list
	Components  []string `json:"components,omitempty"`   // dynamiczone: the allowed components
	Required    bool     `json:"required,omitempty"`
	Unique      bool     `json:"unique,omitempty"`
	Private     bool     `json:"private,omitempty"`
	Min         *float64 `json:"min,omitempty"` // length (text), value (number), count (lists)
	Max         *float64 `json:"max,omitempty"`
	Regex       string   `json:"regex,omitempty"`
	Default     any      `json:"default,omitempty"`
	// RenamedFrom is only sent by the builder: the previous name of a renamed field, so the
	// stored entry values move with it. It is never stored.
	RenamedFrom string `json:"renamedFrom,omitempty"`
	// EnumRenames is only sent by the builder: renamed enumeration values (old -> new), so the
	// stored values follow. It is never stored.
	EnumRenames map[string]string `json:"enumRenames,omitempty"`
}

// hgSchema is a content type or a component.
type hgSchema struct {
	UID             string `json:"uid"`
	Kind            string `json:"kind"`
	DisplayName     string `json:"displayName"`
	SingularName    string `json:"singularName,omitempty"`
	PluralName      string `json:"pluralName,omitempty"`
	Category        string `json:"category,omitempty"` // component
	Description     string `json:"description,omitempty"`
	DraftAndPublish bool   `json:"draftAndPublish,omitempty"`
	// ReviewWorkflow: an entry must be submitted for review and approved (by a user who may edit
	// others' posts) before it can be published. Needs draft & publish.
	ReviewWorkflow bool      `json:"reviewWorkflow,omitempty"`
	Localized      bool      `json:"localized,omitempty"`
	PreviewURL     string    `json:"previewUrl,omitempty"`
	Icon           string    `json:"icon,omitempty"`
	Fields         []hgField `json:"attributes"`
}

func (sc *hgSchema) IsComponent() bool  { return sc.Kind == hgKindComponent }
func (sc *hgSchema) IsSingle() bool     { return sc.Kind == hgKindSingle }
func (sc *hgSchema) IsCollection() bool { return sc.Kind == hgKindCollection }

func (sc *hgSchema) Field(name string) *hgField {
	for i := range sc.Fields {
		if sc.Fields[i].Name == name {
			return &sc.Fields[i]
		}
	}
	return nil
}

// TitleField is the field the admin shows as an entry's title: the first short text field.
func (sc *hgSchema) TitleField() string {
	for _, f := range sc.Fields {
		if (f.Type == "text" && !f.Long) || f.Type == "uid" || f.Type == "email" {
			if !f.Private {
				return f.Name
			}
		}
	}
	return ""
}

// hgFieldTypes are the field types of the builder, in the order it offers them.
var hgFieldTypes = []struct{ Key, Label, Help, Icon string }{
	{"text", "Text", "Short or long text, like a title or a description", "dashicons-editor-textcolor"},
	{"richtext", "Rich text", "Formatted text, as blocks or Markdown", "dashicons-editor-paragraph"},
	{"number", "Number", "Integers or decimals", "dashicons-editor-ol"},
	{"boolean", "Boolean", "Yes or no, true or false", "dashicons-yes-alt"},
	{"date", "Date", "A date picker", "dashicons-calendar-alt"},
	{"datetime", "Date and time", "A date with a time", "dashicons-calendar"},
	{"time", "Time", "A time of day", "dashicons-clock"},
	{"email", "Email", "An e-mail address, validated", "dashicons-email"},
	{"password", "Password", "Stored hashed, never returned by the API", "dashicons-lock"},
	{"enumeration", "Enumeration", "One value from a list", "dashicons-list-view"},
	{"media", "Media", "Images, videos or files from the media library", "dashicons-format-image"},
	{"relation", "Relation", "A link to other entries, posts, pages or users", "dashicons-admin-links"},
	{"json", "JSON", "Data in JSON format", "dashicons-editor-code"},
	{"uid", "UID", "A unique identifier, such as a slug", "dashicons-admin-network"},
	{"component", "Component", "A group of fields you can reuse", "dashicons-screenoptions"},
	{"dynamiczone", "Dynamic zone", "A list of components chosen while editing", "dashicons-layout"},
}

func hgFieldTypeLabel(t string) string {
	for _, ft := range hgFieldTypes {
		if ft.Key == t {
			return ft.Label
		}
	}
	return t
}

var hgRelations = []struct{ Key, Label string }{
	{"oneToOne", "has one (one-to-one)"},
	{"manyToOne", "belongs to one (many-to-one)"},
	{"oneToMany", "has many (one-to-many)"},
	{"manyToMany", "has and belongs to many (many-to-many)"},
}

// hgWPTargets are the WordPress objects a relation can point at.
var hgWPTargets = []struct{ Key, Label string }{{"wp:post", "WordPress posts"}, {"wp:page", "WordPress pages"}, {"wp:user", "WordPress users"}}

// multi reports whether a relation holds a list.
func (f *hgField) multi() bool {
	switch f.Type {
	case "relation":
		return f.Relation == "oneToMany" || f.Relation == "manyToMany"
	case "media":
		return f.Multiple
	case "component":
		return f.Repeatable
	case "dynamiczone":
		return true
	}
	return false
}

// scalar reports whether the field is returned without populate.
func (f *hgField) scalar() bool {
	switch f.Type {
	case "relation", "media", "component", "dynamiczone":
		return false
	}
	return true
}

func (f *hgField) isInteger() bool {
	return f.Type == "number" && (f.Format == "integer" || f.Format == "biginteger")
}

// Schema registry ----------------------------------------------------------------------------------

// hgRegistry is the loaded set of schemas, cached per server and dropped on every schema write.
type hgRegistry struct {
	all   map[string]*hgSchema
	order []*hgSchema // display name order
}

func (r *hgRegistry) Get(uid string) *hgSchema { return r.all[uid] }

// Types are the collection and single types; Components the components.
func (r *hgRegistry) Types() []*hgSchema {
	var out []*hgSchema
	for _, sc := range r.order {
		if !sc.IsComponent() {
			out = append(out, sc)
		}
	}
	return out
}

func (r *hgRegistry) Components() []*hgSchema {
	var out []*hgSchema
	for _, sc := range r.order {
		if sc.IsComponent() {
			out = append(out, sc)
		}
	}
	return out
}

// ByRoute finds the type a delivery API path segment names: a collection type's plural API id or a
// single type's singular one.
func (r *hgRegistry) ByRoute(seg string) *hgSchema {
	for _, sc := range r.order {
		if (sc.IsCollection() && sc.PluralName == seg) || (sc.IsSingle() && sc.SingularName == seg) {
			return sc
		}
	}
	return nil
}

type hgRegState struct {
	mu  sync.Mutex
	reg *hgRegistry
}

var hgRegStates sync.Map // *Server => *hgRegState

func (s *Server) hgRegState() *hgRegState {
	v, _ := hgRegStates.LoadOrStore(s, &hgRegState{})
	return v.(*hgRegState)
}

// hgReg returns the schemas, loading them on first use.
func (s *Server) hgReg(ctx context.Context) *hgRegistry {
	st := s.hgRegState()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.reg != nil {
		return st.reg
	}
	reg := &hgRegistry{all: map[string]*hgSchema{}}
	rows, err := s.store.Pool.Query(ctx, `SELECT schema FROM hg_schemas`)
	if err != nil {
		return reg // not cached: the next call retries
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			continue
		}
		sc := &hgSchema{}
		if json.Unmarshal(raw, sc) == nil && sc.UID != "" {
			reg.all[sc.UID] = sc
			reg.order = append(reg.order, sc)
		}
	}
	sort.Slice(reg.order, func(i, j int) bool {
		a, b := reg.order[i], reg.order[j]
		if strings.ToLower(a.DisplayName) != strings.ToLower(b.DisplayName) {
			return strings.ToLower(a.DisplayName) < strings.ToLower(b.DisplayName)
		}
		return a.UID < b.UID
	})
	st.reg = reg
	return reg
}

func (s *Server) hgRegFlush() {
	st := s.hgRegState()
	st.mu.Lock()
	st.reg = nil
	st.mu.Unlock()
}

// Validation ----------------------------------------------------------------------------------------

var (
	hgAPIIDRe     = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$|^[a-z]$`)
	hgFieldNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	hgCompPartRe  = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
)

// hgReservedFields are attribute names the API uses itself.
var hgReservedFields = map[string]bool{"id": true, "documentId": true, "document_id": true, "createdAt": true, "updatedAt": true,
	"publishedAt": true, "locale": true, "localizations": true, "createdBy": true, "updatedBy": true, "status": true,
	"__component": true, "meta": true, "data": true, "populate": true, "filters": true, "sort": true, "fields": true, "pagination": true}

// hgReservedRoutes are path segments of the delivery API that are not content types.
var hgReservedRoutes = map[string]bool{"wp": true, "graphql": true, "upload": true, "preview": true, "schemas": true, "content-types": true, "tokens": true, "webhooks": true}

// hgSchemaError lists every problem of a schema.
type hgSchemaError []string

func (e hgSchemaError) Error() string { return strings.Join(e, " ") }

// hgNormalizeSchema fills derived values and trims the input.
func hgNormalizeSchema(sc *hgSchema) {
	sc.DisplayName = strings.TrimSpace(sc.DisplayName)
	sc.SingularName = strings.TrimSpace(strings.ToLower(sc.SingularName))
	sc.PluralName = strings.TrimSpace(strings.ToLower(sc.PluralName))
	sc.Category = strings.TrimSpace(strings.ToLower(sc.Category))
	sc.PreviewURL = strings.TrimSpace(sc.PreviewURL)
	if sc.IsComponent() || !sc.DraftAndPublish {
		sc.ReviewWorkflow = false
	}
	if sc.IsComponent() {
		sc.SingularName, sc.PluralName, sc.DraftAndPublish, sc.Localized, sc.PreviewURL = "", "", false, false, ""
		if sc.UID == "" && sc.Category != "" {
			sc.UID = sc.Category + "." + hgSlugify(sc.DisplayName)
		}
	} else if sc.UID == "" {
		sc.UID = sc.SingularName
	}
	if sc.IsSingle() {
		sc.PluralName = ""
	}
	for i := range sc.Fields {
		f := &sc.Fields[i]
		f.Name = strings.TrimSpace(f.Name)
		f.RenamedFrom = strings.TrimSpace(f.RenamedFrom)
		if f.RenamedFrom == f.Name {
			f.RenamedFrom = ""
		}
		var enum []string
		for _, v := range f.Enum {
			if v = strings.TrimSpace(v); v != "" && !containsStr(enum, v) {
				enum = append(enum, v)
			}
		}
		f.Enum = enum
		switch f.Type {
		case "richtext":
			if f.Format != "markdown" {
				f.Format = "blocks"
			}
		case "number":
			switch f.Format {
			case "integer", "biginteger", "decimal", "float":
			default:
				f.Format = "integer"
			}
		default:
			if f.Type != "number" {
				f.Format = ""
			}
		}
		if f.Type != "relation" {
			f.Relation, f.Target = "", ""
		}
		f.InversedBy, f.MappedBy = strings.TrimSpace(f.InversedBy), strings.TrimSpace(f.MappedBy)
		if f.Type != "relation" || sc.IsComponent() || strings.HasPrefix(f.Target, "wp:") {
			f.InversedBy, f.MappedBy = "", ""
		}
		if f.MappedBy != "" {
			// The inverse side stores nothing: its values are the owning entries that point here.
			f.InversedBy, f.Required, f.Unique, f.Default, f.Min, f.Max = "", false, false, nil, nil, nil
		}
		if f.Type != "enumeration" {
			f.EnumRenames = nil
		}
		if f.Type != "component" {
			f.Component, f.Repeatable = "", false
		}
		if f.Type != "dynamiczone" {
			f.Components = nil
		}
		if f.Type != "media" {
			f.Multiple, f.Allowed = false, nil
		}
		if f.Type != "uid" {
			f.TargetField = ""
		}
		if f.Type != "text" {
			f.Long = false
			if f.Type != "uid" && f.Type != "email" {
				f.Regex = ""
			}
		}
		if f.Type == "password" {
			f.Private, f.Unique, f.Default = true, false, nil
		}
		switch f.Type {
		case "component", "dynamiczone", "relation", "media", "json", "password":
			f.Unique = false
		}
		switch f.Type {
		case "component", "dynamiczone", "relation", "media", "json", "boolean":
			f.Default = hgFieldDefault(f)
		}
	}
}

// hgFieldDefault keeps a default only where the builder offers one.
func hgFieldDefault(f *hgField) any {
	if f.Type == "boolean" {
		switch v := f.Default.(type) {
		case bool:
			return v
		case string:
			if v == "true" {
				return true
			}
			if v == "false" {
				return false
			}
		}
		return nil
	}
	return nil
}

func hgSlugify(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	dash := false
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if t, ok := hgTranslit[r]; ok {
				b.WriteString(t)
				dash = false
			} else if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

var hgTranslit = map[rune]string{'ç': "c", 'ğ': "g", 'ı': "i", 'ö': "o", 'ş': "s", 'ü': "u", 'â': "a", 'î': "i", 'û': "u",
	'à': "a", 'á': "a", 'ä': "a", 'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'í': "i", 'ï': "i", 'ñ': "n", 'ó': "o", 'ò': "o",
	'ô': "o", 'ú': "u", 'ù': "u", 'ß': "ss", 'ø': "o", 'å': "a", 'æ': "ae"}

// hgValidateSchema checks one schema against the registry it would join (prev is the stored
// version when it is an update).
func hgValidateSchema(reg *hgRegistry, sc, prev *hgSchema) error {
	var errs hgSchemaError
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }
	if sc.DisplayName == "" {
		add("The display name is required.")
	}
	switch sc.Kind {
	case hgKindCollection, hgKindSingle:
		if !hgAPIIDRe.MatchString(sc.SingularName) {
			add("The singular API ID must be kebab-case: lowercase letters, digits and dashes, starting with a letter.")
		}
		if sc.IsCollection() {
			if !hgAPIIDRe.MatchString(sc.PluralName) {
				add("The plural API ID must be kebab-case: lowercase letters, digits and dashes, starting with a letter.")
			} else if sc.PluralName == sc.SingularName {
				add("The plural API ID must differ from the singular one.")
			}
		}
		if hgReservedRoutes[sc.SingularName] || hgReservedRoutes[sc.PluralName] {
			add("This API ID is reserved.")
		}
		if prev != nil && prev.SingularName != sc.SingularName {
			add("The singular API ID cannot be changed.")
		}
		for _, o := range reg.order {
			if o.UID == sc.UID && prev == nil {
				add("A content type with the API ID %q already exists.", sc.SingularName)
				continue
			}
			if o.UID == sc.UID || o.IsComponent() {
				continue
			}
			for _, n := range []string{o.SingularName, o.PluralName} {
				if n != "" && (n == sc.SingularName || n == sc.PluralName) {
					add("The API ID %q is already used by %s.", n, o.DisplayName)
				}
			}
		}
	case hgKindComponent:
		cat, name, ok := strings.Cut(sc.UID, ".")
		if !ok || !hgCompPartRe.MatchString(cat) || !hgCompPartRe.MatchString(name) {
			add("A component needs a category and a name made of lowercase letters, digits, dashes and underscores.")
		}
		if prev == nil && reg.Get(sc.UID) != nil {
			add("A component %q already exists.", sc.UID)
		}
	default:
		add("Unknown kind %q.", sc.Kind)
	}
	if prev != nil && prev.Kind != sc.Kind {
		add("The kind of a content type cannot be changed.")
	}
	seen := map[string]bool{}
	for _, f := range sc.Fields {
		label := f.Name
		if !hgFieldNameRe.MatchString(f.Name) {
			add("The field name %q is not valid: use letters, digits and underscores, starting with a letter.", f.Name)
			continue
		}
		if hgReservedFields[f.Name] {
			add("The field name %q is reserved.", f.Name)
		}
		if seen[strings.ToLower(f.Name)] {
			add("The field name %q is used twice.", f.Name)
		}
		seen[strings.ToLower(f.Name)] = true
		known := false
		for _, ft := range hgFieldTypes {
			known = known || ft.Key == f.Type
		}
		if !known {
			add("Field %s: unknown type %q.", label, f.Type)
			continue
		}
		if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
			add("Field %s: the minimum is greater than the maximum.", label)
		}
		if f.Regex != "" {
			if _, err := regexp.Compile(f.Regex); err != nil {
				add("Field %s: the regular expression is not valid.", label)
			}
		}
		switch f.Type {
		case "enumeration":
			if len(f.Enum) == 0 {
				add("Field %s: an enumeration needs at least one value.", label)
			}
			if d, ok := f.Default.(string); ok && d != "" && !containsStr(f.Enum, d) {
				add("Field %s: the default value is not one of the values.", label)
			}
		case "relation":
			switch f.Relation {
			case "oneToOne", "oneToMany", "manyToOne", "manyToMany":
			default:
				add("Field %s: choose the kind of relation.", label)
			}
			if strings.HasPrefix(f.Target, "wp:") {
				if f.Target != "wp:post" && f.Target != "wp:page" && f.Target != "wp:user" {
					add("Field %s: unknown WordPress target %q.", label, f.Target)
				}
			} else if t := reg.Get(f.Target); (t == nil || t.IsComponent()) && f.Target != sc.UID {
				add("Field %s: the relation's target %q is not a content type.", label, f.Target)
			}
			if f.InversedBy != "" && (!hgFieldNameRe.MatchString(f.InversedBy) || hgReservedFields[f.InversedBy]) {
				add("Field %s: the inverse field name %q is not valid: use letters, digits and underscores, starting with a letter.", label, f.InversedBy)
			}
			if f.InversedBy != "" && f.Target == sc.UID && f.InversedBy == f.Name {
				add("Field %s: the inverse field needs another name.", label)
			}
		case "uid":
			if f.TargetField != "" {
				tf := sc.Field(f.TargetField)
				if tf == nil || tf.Type != "text" {
					add("Field %s: the attached field must be a text field of this type.", label)
				}
			}
		case "component":
			comp := reg.Get(f.Component)
			if comp == nil || !comp.IsComponent() {
				add("Field %s: choose an existing component.", label)
			} else if sc.IsComponent() && hgComponentReaches(reg, f.Component, sc.UID, 0) {
				add("Field %s: a component cannot contain itself.", label)
			}
		case "dynamiczone":
			if sc.IsComponent() {
				add("Field %s: components cannot contain dynamic zones.", label)
			}
			if len(f.Components) == 0 {
				add("Field %s: a dynamic zone needs at least one component.", label)
			}
			for _, cu := range f.Components {
				if comp := reg.Get(cu); comp == nil || !comp.IsComponent() {
					add("Field %s: unknown component %q.", label, cu)
				}
			}
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// hgComponentReaches reports whether component from (transitively) contains target.
func hgComponentReaches(reg *hgRegistry, from, target string, depth int) bool {
	if from == target {
		return true
	}
	if depth > 20 {
		return true
	}
	sc := reg.Get(from)
	if sc == nil {
		return false
	}
	for _, f := range sc.Fields {
		if f.Type == "component" && hgComponentReaches(reg, f.Component, target, depth+1) {
			return true
		}
	}
	return false
}

// hgSchemaUsers lists the schemas that use a component, or have a relation to a type.
func hgSchemaUsers(reg *hgRegistry, uid string) []*hgSchema {
	var out []*hgSchema
	for _, sc := range reg.order {
		if sc.UID == uid {
			continue
		}
		for _, f := range sc.Fields {
			if (f.Type == "component" && f.Component == uid) || (f.Type == "dynamiczone" && containsStr(f.Components, uid)) ||
				(f.Type == "relation" && f.Target == uid) {
				out = append(out, sc)
				break
			}
		}
	}
	return out
}

// Saving --------------------------------------------------------------------------------------------

// hgSaveSchema validates and stores a schema, then migrates the stored entries: renamed fields move
// their values, removed fields are dropped, fields whose type (or format, cardinality, enumeration
// values…) changed have their values converted (hgConvertValue) — also inside components wherever
// the component is used — and the i18n switch converts the entries' locales. The inverse sides of
// bidirectional relations are kept in step on the other types (hgSyncInverse).
func (s *Server) hgSaveSchema(ctx context.Context, sc *hgSchema) error {
	hgNormalizeSchema(sc)
	reg := s.hgReg(ctx)
	prev := reg.Get(sc.UID)
	renames := map[string]string{} // old -> new
	for _, f := range sc.Fields {
		if f.RenamedFrom != "" && prev != nil && prev.Field(f.RenamedFrom) != nil && sc.Field(f.RenamedFrom) == nil {
			renames[f.RenamedFrom] = f.Name
		}
	}
	others, err := hgSyncInverse(reg, sc, renames)
	if err != nil {
		return err
	}
	if err := hgValidateSchema(reg, sc, prev); err != nil {
		return err
	}
	var removed []string
	convs := map[string]hgConversion{} // new field name -> conversion of its stored values
	if prev != nil {
		for _, pf := range prev.Fields {
			if sc.Field(pf.Name) == nil && renames[pf.Name] == "" {
				removed = append(removed, pf.Name)
			}
		}
		for i := range sc.Fields {
			f := &sc.Fields[i]
			old := prev.Field(f.Name)
			if f.RenamedFrom != "" && renames[f.RenamedFrom] == f.Name {
				old = prev.Field(f.RenamedFrom)
			}
			if old != nil && hgNeedsConversion(old, f) {
				from, to := *old, *f
				convs[f.Name] = hgConversion{from: &from, to: &to}
			}
		}
	}
	for i := range sc.Fields {
		sc.Fields[i].RenamedFrom = ""
		sc.Fields[i].EnumRenames = nil
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		return err
	}
	tx, err := s.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO hg_schemas(uid, kind, schema) VALUES ($1, $2, $3)
		ON CONFLICT (uid) DO UPDATE SET schema = EXCLUDED.schema, updated_at = now()`, sc.UID, sc.Kind, raw); err != nil {
		return err
	}
	for _, o := range others {
		oraw, err := json.Marshal(o)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hg_schemas SET schema = $2, updated_at = now() WHERE uid = $1`, o.UID, oraw); err != nil {
			return err
		}
	}
	if prev != nil && (len(renames) > 0 || len(removed) > 0 || len(convs) > 0) {
		fix := func(obj map[string]any) {
			for o, n := range renames {
				if v, ok := obj[o]; ok {
					obj[n] = v
					delete(obj, o)
				}
			}
			for _, r := range removed {
				delete(obj, r)
			}
			for name, cv := range convs {
				v, ok := obj[name]
				if !ok {
					continue
				}
				if nv, keep := hgConvertValue(cv.from, cv.to, v); keep {
					obj[name] = nv
				} else {
					delete(obj, name)
				}
			}
		}
		if err := hgMigrateEntries(ctx, tx, reg, sc.UID, fix); err != nil {
			return err
		}
	}
	if prev != nil && !sc.IsComponent() && prev.Localized != sc.Localized {
		def := s.hgDefaultLocale(ctx)
		if sc.Localized {
			if _, err := tx.Exec(ctx, `UPDATE hg_entries SET locale = $2 WHERE type_uid = $1 AND locale = ''`, sc.UID, def); err != nil {
				return err
			}
		} else {
			// Only the default locale's version stays: a type without i18n has one version per document.
			if _, err := tx.Exec(ctx, `DELETE FROM hg_entries e WHERE type_uid = $1 AND locale <> $2 AND EXISTS (
				SELECT 1 FROM hg_entries d WHERE d.type_uid = e.type_uid AND d.document_id = e.document_id AND d.locale = $2)`, sc.UID, def); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM hg_entries e WHERE type_uid = $1 AND locale <> $2 AND id <> (
				SELECT min(id) FROM hg_entries d WHERE d.type_uid = e.type_uid AND d.document_id = e.document_id)`, sc.UID, def); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE hg_entries SET locale = '' WHERE type_uid = $1`, sc.UID); err != nil {
				return err
			}
		}
	}
	if prev != nil && !sc.IsComponent() && prev.DraftAndPublish && !sc.DraftAndPublish {
		// Without draft & publish every entry is live: the draft becomes the published version.
		if _, err := tx.Exec(ctx, `UPDATE hg_entries SET published_data = data, published_at = coalesce(published_at, now()) WHERE type_uid = $1`, sc.UID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.hgRegFlush()
	return nil
}

// hgMigrateEntries rewrites stored entry data wherever schema uid's objects live: the entries of
// the type itself, or every component value of uid inside other types' entries.
func hgMigrateEntries(ctx context.Context, tx pgx.Tx, reg *hgRegistry, uid string, fix func(map[string]any)) error {
	sc := reg.Get(uid)
	if sc == nil {
		return nil
	}
	if !sc.IsComponent() {
		return hgRewriteType(ctx, tx, uid, func(obj map[string]any) { fix(obj) })
	}
	// A component: walk every type whose schema reaches it.
	for _, t := range reg.Types() {
		if !hgSchemaReaches(reg, t, uid, 0) {
			continue
		}
		tt := t
		if err := hgRewriteType(ctx, tx, tt.UID, func(obj map[string]any) { hgWalkComponents(reg, tt, obj, uid, fix, 0) }); err != nil {
			return err
		}
	}
	return nil
}

func hgSchemaReaches(reg *hgRegistry, sc *hgSchema, comp string, depth int) bool {
	if depth > 20 {
		return false
	}
	for _, f := range sc.Fields {
		var list []string
		if f.Type == "component" {
			list = []string{f.Component}
		} else if f.Type == "dynamiczone" {
			list = f.Components
		}
		for _, c := range list {
			if c == comp {
				return true
			}
			if cs := reg.Get(c); cs != nil && hgSchemaReaches(reg, cs, comp, depth+1) {
				return true
			}
		}
	}
	return false
}

// hgWalkComponents calls fix on every value of component comp inside obj (an object of schema sc).
func hgWalkComponents(reg *hgRegistry, sc *hgSchema, obj map[string]any, comp string, fix func(map[string]any), depth int) {
	if depth > 20 || obj == nil {
		return
	}
	for _, f := range sc.Fields {
		v, ok := obj[f.Name]
		if !ok {
			continue
		}
		var items []map[string]any
		switch t := v.(type) {
		case map[string]any:
			items = append(items, t)
		case []any:
			for _, x := range t {
				if m, ok := x.(map[string]any); ok {
					items = append(items, m)
				}
			}
		}
		for _, it := range items {
			cu := f.Component
			if f.Type == "dynamiczone" {
				cu, _ = it["__component"].(string)
			} else if f.Type != "component" {
				continue
			}
			cs := reg.Get(cu)
			if cs == nil {
				continue
			}
			hgWalkComponents(reg, cs, it, comp, fix, depth+1)
			if cu == comp {
				fix(it)
			}
		}
	}
}

// hgRewriteType applies fix to the draft and published data of every entry of a type.
func hgRewriteType(ctx context.Context, tx pgx.Tx, uid string, fix func(map[string]any)) error {
	_, err := hgRewriteTypeChanged(ctx, tx, uid, fix, nil)
	return err
}

// hgRewriteTypeChanged is hgRewriteType that reports what changed: after each call of fix, take
// returns what that call changed (nothing: the object is left as it was); only the entries with a
// change are written, and their ids are returned with the collected changes. With a nil take every
// entry is written and nothing is reported.
func hgRewriteTypeChanged(ctx context.Context, tx pgx.Tx, uid string, fix func(map[string]any), take func() []string) (map[int64][]string, error) {
	rows, err := tx.Query(ctx, `SELECT id, data, published_data FROM hg_entries WHERE type_uid = $1`, uid)
	if err != nil {
		return nil, err
	}
	type upd struct {
		id        int64
		data, pub []byte
	}
	var list []upd
	changed := map[int64][]string{}
	for rows.Next() {
		var id int64
		var d, p []byte
		if err := rows.Scan(&id, &d, &p); err != nil {
			rows.Close()
			return nil, err
		}
		var hits []string
		var dm, pm map[string]any
		_ = json.Unmarshal(d, &dm)
		if dm == nil {
			dm = map[string]any{}
		}
		fix(dm)
		if take != nil {
			hits = append(hits, take()...)
		}
		u := upd{id: id}
		u.data, _ = json.Marshal(dm)
		if p != nil {
			_ = json.Unmarshal(p, &pm)
			if pm != nil {
				fix(pm)
				if take != nil {
					hits = append(hits, take()...)
				}
				u.pub, _ = json.Marshal(pm)
			}
		}
		if take != nil && len(hits) == 0 {
			continue
		}
		for _, h := range hits {
			if !containsStr(changed[id], h) {
				changed[id] = append(changed[id], h)
			}
		}
		list = append(list, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, u := range list {
		var pub any
		if u.pub != nil {
			pub = u.pub
		}
		if _, err := tx.Exec(ctx, `UPDATE hg_entries SET data = $2, published_data = $3 WHERE id = $1`, u.id, u.data, pub); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// hgDeleteSchema removes a type (and its entries) or a component. A component still used by
// another schema, or a type other types relate to, cannot be deleted.
func (s *Server) hgDeleteSchema(ctx context.Context, uid string) error {
	reg := s.hgReg(ctx)
	sc := reg.Get(uid)
	if sc == nil {
		return errors.New("This content type does not exist.")
	}
	if users := hgSchemaUsers(reg, uid); len(users) > 0 {
		var names []string
		for _, u := range users {
			names = append(names, u.DisplayName)
		}
		return fmt.Errorf("It is still used by: %s. Remove those fields first.", strings.Join(names, ", "))
	}
	if _, err := s.store.Pool.Exec(ctx, `DELETE FROM hg_schemas WHERE uid = $1`, uid); err != nil {
		return err
	}
	s.hgRegFlush()
	st := s.hgLoad()
	if _, ok := st.Public[uid]; ok {
		delete(st.Public, uid)
		_ = s.hgSaveSettings(ctx, st)
	}
	return nil
}

// Export / import ------------------------------------------------------------------------------------

// hgExport is the JSON document of Export / Import.
type hgExport struct {
	Version    int         `json:"version"`
	Generator  string      `json:"generator"`
	Components []*hgSchema `json:"components"`
	Types      []*hgSchema `json:"contentTypes"`
}

func (s *Server) hgExportSchemas(ctx context.Context) hgExport {
	reg := s.hgReg(ctx)
	return hgExport{Version: 1, Generator: "Pursae Headless " + Version, Components: reg.Components(), Types: reg.Types()}
}

// hgImportSchemas creates or updates every schema of an export: components first (ordered so a
// component comes after the ones it contains), then the types in two passes (without relations,
// then complete) so types may relate to each other.
func (s *Server) hgImportSchemas(ctx context.Context, raw []byte) (int, error) {
	var ex hgExport
	if err := json.Unmarshal(raw, &ex); err != nil {
		return 0, errors.New("The file is not a schema export (invalid JSON).")
	}
	if len(ex.Components) == 0 && len(ex.Types) == 0 {
		return 0, errors.New("The file contains no content types or components.")
	}
	n := 0
	pending := append([]*hgSchema(nil), ex.Components...)
	for len(pending) > 0 {
		var next []*hgSchema
		var lastErr error
		for _, sc := range pending {
			sc.Kind = hgKindComponent
			if err := s.hgSaveSchema(ctx, sc); err != nil {
				lastErr = fmt.Errorf("%s: %v", sc.UID, err)
				next = append(next, sc)
				continue
			}
			n++
		}
		if len(next) == len(pending) {
			return n, lastErr
		}
		pending = next
	}
	for _, sc := range ex.Types {
		hgNormalizeSchema(sc)
		if s.hgReg(ctx).Get(sc.UID) != nil {
			continue
		}
		shell := *sc
		shell.Fields = nil
		for _, f := range sc.Fields {
			if f.Type != "relation" {
				shell.Fields = append(shell.Fields, f)
			}
		}
		if err := s.hgSaveSchema(ctx, &shell); err != nil {
			return n, fmt.Errorf("%s: %v", sc.DisplayName, err)
		}
	}
	// Complete, but without the bidirectional links: inverse fields are recreated from their owning
	// side in the last pass, whatever order the types come in.
	inverse := map[string]map[string]string{} // type uid -> owning field -> inverse field name
	for _, sc := range ex.Types {
		full := *sc
		full.Fields = nil
		for _, f := range sc.Fields {
			if f.MappedBy != "" {
				continue
			}
			if f.InversedBy != "" {
				if inverse[sc.UID] == nil {
					inverse[sc.UID] = map[string]string{}
				}
				inverse[sc.UID][f.Name] = f.InversedBy
				f.InversedBy = ""
			}
			full.Fields = append(full.Fields, f)
		}
		if err := s.hgSaveSchema(ctx, &full); err != nil {
			return n, fmt.Errorf("%s: %v", sc.DisplayName, err)
		}
		n++
	}
	for _, sc := range ex.Types {
		links := inverse[sc.UID]
		cur := s.hgReg(ctx).Get(sc.UID)
		if len(links) == 0 || cur == nil {
			continue
		}
		upd := hgCopySchema(cur)
		for i := range upd.Fields {
			if inv, ok := links[upd.Fields[i].Name]; ok {
				upd.Fields[i].InversedBy = inv
			}
		}
		if err := s.hgSaveSchema(ctx, upd); err != nil {
			return n, fmt.Errorf("%s: %v", sc.DisplayName, err)
		}
	}
	return n, nil
}
