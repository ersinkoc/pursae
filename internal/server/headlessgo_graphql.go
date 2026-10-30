package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
)

// A small, read-only GraphQL endpoint over the same data as the REST API:
//
//	{ articles(filters: {title: {containsi: "go"}}, sort: "views:desc", pagination: {pageSize: 5}, locale: "en", status: PUBLISHED) {
//	    documentId title author { name } blocks { __typename ... on ComponentBlocksQuote { text } } } }
//
// Root fields are a collection type's plural API id in camelCase (a list), its singular one with a
// documentId argument (one entry), a single type's singular API id, and <plural>_connection with
// { nodes pageInfo { total page pageSize pageCount } }. Arguments mirror the REST parameters
// (operators without the $). Variables, aliases and inline fragments are supported; mutations,
// named fragments, directives and introspection are not. Permissions are the REST API's.

func (s *Server) hgGraphQL(c *gin.Context, a *hgAuth, st hgSettings) {
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	switch c.Request.Method {
	case http.MethodGet:
		req.Query = c.Query("query")
		if v := c.Query("variables"); v != "" {
			_ = json.Unmarshal([]byte(v), &req.Variables)
		}
	case http.MethodPost:
		raw, _ := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
		if strings.HasPrefix(c.ContentType(), "application/graphql") {
			req.Query = string(raw)
		} else if err := json.Unmarshal(raw, &req); err != nil {
			hgGQLError(c, http.StatusBadRequest, "The body must be JSON: {\"query\": \"...\"}")
			return
		}
	default:
		hgGQLError(c, http.StatusMethodNotAllowed, "Use GET or POST")
		return
	}
	doc, err := hgGQLParse(req.Query)
	if err != nil {
		hgGQLError(c, http.StatusBadRequest, err.Error())
		return
	}
	if doc.op == "mutation" || doc.op == "subscription" {
		hgGQLError(c, http.StatusBadRequest, "Only queries are supported: the GraphQL endpoint is read-only")
		return
	}
	ex := &hgGQLExec{s: s, c: c, a: a, st: st, vars: req.Variables}
	out := map[string]any{}
	for _, f := range doc.sel {
		v, err := ex.root(f)
		if err != nil {
			c.JSON(http.StatusOK, map[string]any{"data": nil, "errors": []any{map[string]any{"message": err.Error(), "path": []string{f.key()}}}})
			return
		}
		out[f.key()] = v
	}
	c.JSON(http.StatusOK, map[string]any{"data": out})
}

func hgGQLError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, map[string]any{"data": nil, "errors": []any{map[string]any{"message": msg}}})
}

// Parsing ---------------------------------------------------------------------------------------------

type hgGQLField struct {
	alias, name string
	args        map[string]any // values; variables are hgGQLVar
	sel         []*hgGQLField
	on          string // inline fragment type condition (name == "...")
}

func (f *hgGQLField) key() string {
	if f.alias != "" {
		return f.alias
	}
	return f.name
}

type hgGQLVar string
type hgGQLEnum string

type hgGQLDoc struct {
	op  string
	sel []*hgGQLField
}

type hgGQLParser struct {
	src string
	pos int
}

func hgGQLParse(src string) (*hgGQLDoc, error) {
	p := &hgGQLParser{src: src}
	doc := &hgGQLDoc{op: "query"}
	p.skip()
	if p.peek() != '{' {
		name := p.name()
		switch name {
		case "query", "mutation", "subscription":
			doc.op = name
		default:
			return nil, fmt.Errorf("Syntax error: expected a query")
		}
		p.skip()
		if isNameStart(p.peek()) {
			p.name()
			p.skip()
		}
		if p.peek() == '(' { // variable definitions: skipped, values come from "variables"
			depth := 0
			for p.pos < len(p.src) {
				ch := p.src[p.pos]
				p.pos++
				if ch == '(' {
					depth++
				} else if ch == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			p.skip()
		}
	}
	sel, err := p.selection(0)
	if err != nil {
		return nil, err
	}
	doc.sel = sel
	p.skip()
	if p.pos < len(p.src) {
		return nil, fmt.Errorf("Syntax error: unexpected %q (only one operation is supported)", p.src[p.pos:min(len(p.src), p.pos+10)])
	}
	return doc, nil
}

func isNameStart(b byte) bool { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func (p *hgGQLParser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

// skip passes whitespace, commas and comments.
func (p *hgGQLParser) skip() {
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if ch == '#' {
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == ',' || ch == 0xEF || ch == 0xBB || ch == 0xBF {
			p.pos++
			continue
		}
		break
	}
}

func (p *hgGQLParser) name() string {
	start := p.pos
	for p.pos < len(p.src) {
		ch := p.src[p.pos]
		if isNameStart(ch) || (ch >= '0' && ch <= '9') {
			p.pos++
			continue
		}
		break
	}
	return p.src[start:p.pos]
}

func (p *hgGQLParser) expect(ch byte) error {
	p.skip()
	if p.peek() != ch {
		return fmt.Errorf("Syntax error: expected %q at %d", ch, p.pos)
	}
	p.pos++
	return nil
}

func (p *hgGQLParser) selection(depth int) ([]*hgGQLField, error) {
	if depth > 15 {
		return nil, fmt.Errorf("The query is nested too deeply")
	}
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	var out []*hgGQLField
	for {
		p.skip()
		if p.peek() == '}' {
			p.pos++
			break
		}
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("Syntax error: unclosed selection set")
		}
		if strings.HasPrefix(p.src[p.pos:], "...") {
			p.pos += 3
			p.skip()
			if p.name() != "on" {
				return nil, fmt.Errorf("Named fragments are not supported; use inline fragments (... on Type)")
			}
			p.skip()
			typ := p.name()
			sel, err := p.selection(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, &hgGQLField{name: "...", on: typ, sel: sel})
			continue
		}
		if !isNameStart(p.peek()) {
			return nil, fmt.Errorf("Syntax error: unexpected %q at %d", p.peek(), p.pos)
		}
		f := &hgGQLField{name: p.name()}
		p.skip()
		if p.peek() == ':' {
			p.pos++
			p.skip()
			f.alias, f.name = f.name, p.name()
			p.skip()
		}
		if p.peek() == '(' {
			p.pos++
			f.args = map[string]any{}
			for {
				p.skip()
				if p.peek() == ')' {
					p.pos++
					break
				}
				k := p.name()
				if k == "" {
					return nil, fmt.Errorf("Syntax error in the arguments of %s", f.name)
				}
				if err := p.expect(':'); err != nil {
					return nil, err
				}
				v, err := p.value(0)
				if err != nil {
					return nil, err
				}
				f.args[k] = v
			}
			p.skip()
		}
		if p.peek() == '@' {
			return nil, fmt.Errorf("Directives are not supported")
		}
		if p.peek() == '{' {
			sel, err := p.selection(depth + 1)
			if err != nil {
				return nil, err
			}
			f.sel = sel
		}
		out = append(out, f)
	}
	return out, nil
}

func (p *hgGQLParser) value(depth int) (any, error) {
	if depth > 20 {
		return nil, fmt.Errorf("The arguments are nested too deeply")
	}
	p.skip()
	ch := p.peek()
	switch {
	case ch == '$':
		p.pos++
		return hgGQLVar(p.name()), nil
	case ch == '"':
		p.pos++
		var b strings.Builder
		for p.pos < len(p.src) && p.src[p.pos] != '"' {
			if p.src[p.pos] == '\\' && p.pos+1 < len(p.src) {
				p.pos++
				switch p.src[p.pos] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteByte(p.src[p.pos])
				}
				p.pos++
				continue
			}
			b.WriteByte(p.src[p.pos])
			p.pos++
		}
		if p.pos >= len(p.src) {
			return nil, fmt.Errorf("Syntax error: unterminated string")
		}
		p.pos++
		return b.String(), nil
	case ch == '[':
		p.pos++
		list := []any{}
		for {
			p.skip()
			if p.peek() == ']' {
				p.pos++
				return list, nil
			}
			if p.pos >= len(p.src) {
				return nil, fmt.Errorf("Syntax error: unclosed list")
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
	case ch == '{':
		p.pos++
		obj := map[string]any{}
		for {
			p.skip()
			if p.peek() == '}' {
				p.pos++
				return obj, nil
			}
			k := p.name()
			if k == "" {
				return nil, fmt.Errorf("Syntax error in an object value")
			}
			if err := p.expect(':'); err != nil {
				return nil, err
			}
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			obj[k] = v
		}
	case ch == '-' || (ch >= '0' && ch <= '9'):
		start := p.pos
		p.pos++
		for p.pos < len(p.src) && strings.IndexByte("0123456789.eE+-", p.src[p.pos]) >= 0 {
			p.pos++
		}
		n, err := strconv.ParseFloat(p.src[start:p.pos], 64)
		if err != nil {
			return nil, fmt.Errorf("Syntax error: bad number")
		}
		return n, nil
	case isNameStart(ch):
		switch n := p.name(); n {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		default:
			return hgGQLEnum(n), nil
		}
	}
	return nil, fmt.Errorf("Syntax error: unexpected %q at %d", ch, p.pos)
}

// Execution ---------------------------------------------------------------------------------------------

type hgGQLExec struct {
	s    *Server
	c    *gin.Context
	a    *hgAuth
	st   hgSettings
	vars map[string]any
}

// resolve replaces variables in an argument value.
func (ex *hgGQLExec) resolve(v any) any {
	switch t := v.(type) {
	case hgGQLVar:
		return ex.vars[string(t)]
	case hgGQLEnum:
		return string(t)
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = ex.resolve(x)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, x := range t {
			out[k] = ex.resolve(x)
		}
		return out
	}
	return v
}

// hgCamel turns a kebab-case API id into camelCase (blog-posts -> blogPosts).
func hgCamel(id string) string {
	parts := strings.Split(id, "-")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// hgPascal is a GraphQL type name: Article, ComponentSharedSeo.
func hgPascal(sc *hgSchema) string {
	src := sc.SingularName
	prefix := ""
	if sc.IsComponent() {
		src, prefix = strings.ReplaceAll(sc.UID, ".", "-"), "Component"
	}
	var b strings.Builder
	b.WriteString(prefix)
	up := true
	for _, r := range src {
		if r == '-' || r == '_' {
			up = true
			continue
		}
		if up {
			b.WriteRune(unicode.ToUpper(r))
			up = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (ex *hgGQLExec) root(f *hgGQLField) (any, error) {
	if f.name == "__typename" {
		return "Query", nil
	}
	if strings.HasPrefix(f.name, "__") {
		return nil, fmt.Errorf("Introspection is not supported")
	}
	reg := ex.s.hgReg(ex.c)
	name, connection := strings.CutSuffix(f.name, "_connection")
	for _, sc := range reg.Types() {
		switch {
		case sc.IsCollection() && hgCamel(sc.PluralName) == name:
			if !ex.a.can(sc.UID, "find") || (ex.a.Kind == "preview" && ex.a.PrevUID != sc.UID) {
				return nil, fmt.Errorf("Forbidden access")
			}
			return ex.list(sc, f, connection)
		case !connection && hgCamel(sc.SingularName) == name:
			if !ex.a.can(sc.UID, map[bool]string{true: "find", false: "findOne"}[sc.IsSingle()]) {
				return nil, fmt.Errorf("Forbidden access")
			}
			return ex.one(sc, f)
		}
	}
	return nil, fmt.Errorf("Cannot query field %q on type \"Query\"", f.name)
}

// params converts GraphQL arguments to REST read parameters.
func (ex *hgGQLExec) params(sc *hgSchema, f *hgGQLField, sel []*hgGQLField) (*hgReadParams, error) {
	p := &hgReadParams{Status: "published"}
	args := map[string]any{}
	for k, v := range f.args {
		args[k] = ex.resolve(v)
	}
	if s, ok := args["status"].(string); ok && s != "" {
		switch strings.ToLower(s) {
		case "draft":
			p.Status = "draft"
		case "published":
		default:
			return nil, fmt.Errorf("Invalid status %q", s)
		}
	}
	if !sc.DraftAndPublish {
		p.Status = "published"
	}
	if p.Status == "draft" && !ex.a.drafts(sc.UID) {
		return nil, fmt.Errorf("Reading drafts needs a token allowed to read them")
	}
	loc, _ := args["locale"].(string)
	l, err := ex.s.hgEntryLocale(ex.c, sc, loc)
	if err != nil {
		return nil, err
	}
	p.Locale = l
	if fl, ok := args["filters"].(map[string]any); ok {
		p.Filters = hgGQLFilters(fl)
	}
	if s := args["sort"]; s != nil {
		p.Sort, err = hgParseSort(hgGQLStrings(s))
		if err != nil {
			return nil, err
		}
	}
	pag := map[string]any{}
	if pm, ok := args["pagination"].(map[string]any); ok {
		for k, v := range pm {
			pag[k] = hgGQLString(v)
		}
	}
	if p.Page, err = hgParsePagination(pag, ex.st.DefaultPageSize, ex.st.MaxPageSize); err != nil {
		return nil, err
	}
	p.Populate = ex.populate(sc, sel)
	return p, nil
}

// hgGQLFilters prefixes operators with $ and turns values into the REST parser's strings.
func hgGQLFilters(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		key := k
		if hgOperators["$"+k] {
			key = "$" + k
		}
		switch t := v.(type) {
		case map[string]any:
			out[key] = hgGQLFilters(t)
		case []any:
			var list []any
			for _, x := range t {
				if xm, ok := x.(map[string]any); ok {
					list = append(list, hgGQLFilters(xm))
				} else {
					list = append(list, hgGQLString(x))
				}
			}
			out[key] = list
		default:
			out[key] = hgGQLString(v)
		}
	}
	return out
}

func hgGQLString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func hgGQLStrings(v any) any {
	if l, ok := v.([]any); ok {
		out := make([]string, len(l))
		for i, x := range l {
			out[i] = hgGQLString(x)
		}
		return out
	}
	return hgGQLString(v)
}

// flat merges inline fragments into one selection.
func hgGQLFlat(sel []*hgGQLField) []*hgGQLField {
	var out []*hgGQLField
	for _, f := range sel {
		if f.name == "..." {
			out = append(out, hgGQLFlat(f.sel)...)
		} else {
			out = append(out, f)
		}
	}
	return out
}

// populate derives what to load from a selection set.
func (ex *hgGQLExec) populate(sc *hgSchema, sel []*hgGQLField) *hgPop {
	pop := &hgPop{Populate: map[string]*hgPop{}}
	reg := ex.s.hgReg(ex.c)
	for _, f := range hgGQLFlat(sel) {
		if f.name == "localizations" && sc.Localized {
			pop.Populate["localizations"] = &hgPop{Populate: map[string]*hgPop{}}
			continue
		}
		fd := sc.Field(f.name)
		if fd == nil || fd.scalar() || fd.Private {
			continue
		}
		var sub *hgPop
		switch fd.Type {
		case "relation":
			if t := reg.Get(fd.Target); t != nil {
				sub = ex.populate(t, f.sel)
			} else {
				sub = &hgPop{Populate: map[string]*hgPop{}}
			}
			if f.args != nil {
				if fl, ok := ex.resolve(f.args["filters"]).(map[string]any); ok {
					sub.Filters = hgGQLFilters(fl)
				}
				if s := ex.resolve(f.args["sort"]); s != nil {
					sub.Sort, _ = hgParseSort(hgGQLStrings(s))
				}
			}
		case "component":
			if comp := reg.Get(fd.Component); comp != nil {
				sub = ex.populate(comp, f.sel)
			}
		case "dynamiczone":
			sub = &hgPop{Populate: map[string]*hgPop{}, On: map[string]*hgPop{}}
			for _, cu := range fd.Components {
				if comp := reg.Get(cu); comp != nil {
					var fsel []*hgGQLField
					for _, x := range f.sel {
						if x.name == "..." && x.on == hgPascal(comp) {
							fsel = append(fsel, x.sel...)
						} else if x.name != "..." {
							fsel = append(fsel, x)
						}
					}
					sub.On[cu] = ex.populate(comp, fsel)
				}
			}
		default:
			sub = &hgPop{Populate: map[string]*hgPop{}}
		}
		if sub != nil {
			pop.Populate[f.name] = sub
		}
	}
	return pop
}

func (ex *hgGQLExec) list(sc *hgSchema, f *hgGQLField, connection bool) (any, error) {
	sel := f.sel
	var nodesSel, pageSel []*hgGQLField
	if connection {
		for _, x := range f.sel {
			switch x.name {
			case "nodes":
				nodesSel = x.sel
			case "pageInfo":
				pageSel = x.sel
			}
		}
		sel = nodesSel
	}
	p, err := ex.params(sc, f, sel)
	if err != nil {
		return nil, err
	}
	o := hgFindOpts{}
	if ex.a.Kind == "preview" {
		o.Doc = ex.a.PrevDoc
	}
	if connection {
		p.Page.WithCount = true
	}
	list, total, err := ex.s.hgFind(ex.c, sc, p, o)
	if err != nil {
		return nil, err
	}
	ser := ex.s.hgSerializer(ex.c, ex.a, p)
	items := []any{}
	for _, e := range list {
		obj, err := ser.entry(sc, e, p.Populate, nil)
		if err != nil {
			return nil, err
		}
		items = append(items, ex.project(sc, obj, sel))
	}
	if !connection {
		return items, nil
	}
	out := map[string]any{}
	for _, x := range f.sel {
		switch x.name {
		case "nodes":
			out[x.key()] = items
		case "pageInfo":
			m := p.Page.meta(total)
			pi := map[string]any{}
			for _, y := range pageSel {
				pi[y.key()] = m[y.name]
			}
			out[x.key()] = pi
		case "__typename":
			out[x.key()] = hgPascal(sc) + "EntityResponseCollection"
		}
	}
	return out, nil
}

func (ex *hgGQLExec) one(sc *hgSchema, f *hgGQLField) (any, error) {
	p, err := ex.params(sc, f, f.sel)
	if err != nil {
		return nil, err
	}
	doc := ""
	if sc.IsSingle() {
		doc = ex.s.hgSingleDoc(ex.c, sc.UID)
	} else {
		doc = hgGQLString(ex.resolve(f.args["documentId"]))
		if doc == "" {
			return nil, fmt.Errorf("The argument documentId is required")
		}
	}
	if ex.a.Kind == "preview" && (ex.a.PrevUID != sc.UID || (doc != ex.a.PrevDoc)) {
		return nil, fmt.Errorf("Forbidden access")
	}
	if doc == "" {
		return nil, nil
	}
	e := ex.s.hgOneEntry(ex.c, sc, doc, p)
	if e == nil {
		return nil, nil
	}
	obj, err := ex.s.hgSerializer(ex.c, ex.a, p).entry(sc, e, p.Populate, nil)
	if err != nil {
		return nil, err
	}
	return ex.project(sc, obj, f.sel), nil
}

// project keeps the selected keys of a serialized object (with aliases and __typename).
func (ex *hgGQLExec) project(sc *hgSchema, obj map[string]any, sel []*hgGQLField) map[string]any {
	reg := ex.s.hgReg(ex.c)
	out := map[string]any{}
	for _, f := range sel {
		if f.name == "..." {
			if sc != nil && f.on == hgPascal(sc) {
				for k, v := range ex.project(sc, obj, f.sel) {
					out[k] = v
				}
			}
			continue
		}
		if f.name == "__typename" {
			if sc != nil {
				out[f.key()] = hgPascal(sc)
			} else if _, ok := obj["mime"]; ok {
				out[f.key()] = "UploadFile"
			}
			continue
		}
		v, ok := obj[f.name]
		if !ok {
			out[f.key()] = nil
			continue
		}
		if len(f.sel) == 0 || sc == nil {
			if len(f.sel) > 0 {
				out[f.key()] = hgGQLProjectAny(v, f.sel)
			} else {
				out[f.key()] = v
			}
			continue
		}
		var sub *hgSchema
		fd := sc.Field(f.name)
		if fd != nil {
			switch fd.Type {
			case "relation":
				sub = reg.Get(fd.Target)
			case "component":
				sub = reg.Get(fd.Component)
			}
		}
		if f.name == "localizations" {
			sub = sc
		}
		switch t := v.(type) {
		case map[string]any:
			out[f.key()] = ex.project(sub, t, f.sel)
		case []any:
			list := make([]any, 0, len(t))
			for _, x := range t {
				m, ok := x.(map[string]any)
				if !ok {
					continue
				}
				s2 := sub
				if fd != nil && fd.Type == "dynamiczone" {
					cu, _ := m["__component"].(string)
					s2 = reg.Get(cu)
				}
				list = append(list, ex.project(s2, m, f.sel))
			}
			out[f.key()] = list
		default:
			out[f.key()] = v
		}
	}
	return out
}

// hgGQLProjectAny projects plain objects (media, WordPress objects) by selection.
func hgGQLProjectAny(v any, sel []*hgGQLField) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for _, f := range hgGQLFlat(sel) {
			out[f.key()] = hgGQLProjectAny(t[f.name], f.sel)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = hgGQLProjectAny(x, sel)
		}
		return out
	}
	return v
}
