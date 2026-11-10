package server

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// OpenAPI and TypeScript generation: both are derived from the content model, so an API client or a
// front end gets typed access without writing the types by hand. GET /headlessgo/v1/openapi.json
// and GET /headlessgo/v1/typescript describe only what the caller may read or write (the same
// permission matrix as the endpoints), so the documents never reveal types the caller cannot use.

// hgSchemaName is the OpenAPI / TypeScript identifier of a type or component.
func hgSchemaName(sc *hgSchema) string {
	name := sc.SingularName
	if sc.IsComponent() {
		name = strings.NewReplacer(".", "-", "_", "-").Replace(sc.UID)
	}
	if name == "" {
		name = strings.NewReplacer(".", "-", ":", "-").Replace(sc.UID)
	}
	var b strings.Builder
	for _, part := range regexp.MustCompile(`[^A-Za-z0-9]+`).Split(name, -1) {
		b.WriteString(hgUpperFirst(part))
	}
	if sc.IsComponent() {
		return "Component" + b.String()
	}
	return b.String()
}

func hgRef(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }

// hgFieldSchema is the JSON schema of the value of a field as the delivery API returns it.
func hgFieldSchema(reg *hgRegistry, f *hgField, input bool) map[string]any {
	var out map[string]any
	switch f.Type {
	case "text", "uid", "password":
		out = map[string]any{"type": "string"}
	case "email":
		out = map[string]any{"type": "string", "format": "email"}
	case "enumeration":
		vals := make([]any, len(f.Enum))
		for i, v := range f.Enum {
			vals[i] = v
		}
		out = map[string]any{"type": "string", "enum": vals}
	case "richtext":
		if f.Format == "markdown" {
			out = map[string]any{"type": "string"}
		} else {
			out = map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
		}
	case "number":
		switch f.Format {
		case "integer", "biginteger":
			out = map[string]any{"type": "integer"}
		default:
			out = map[string]any{"type": "number"}
		}
	case "boolean":
		out = map[string]any{"type": "boolean"}
	case "date":
		out = map[string]any{"type": "string", "format": "date"}
	case "datetime":
		out = map[string]any{"type": "string", "format": "date-time"}
	case "time":
		out = map[string]any{"type": "string"}
	case "json":
		out = map[string]any{}
	case "media":
		if input {
			out = map[string]any{"type": "integer", "description": "Media Library attachment ID"}
			if f.Multiple {
				out = map[string]any{"type": "array", "items": out}
			}
		} else {
			out = map[string]any{"type": "object", "additionalProperties": true}
			if f.Multiple {
				out = map[string]any{"type": "array", "items": out}
			}
		}
	case "relation":
		many := f.Relation == "oneToMany" || f.Relation == "manyToMany"
		var item map[string]any
		switch {
		case input:
			item = map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}}, "description": "documentId (or ID for WordPress targets)"}
		case reg.Get(f.Target) != nil:
			item = hgRef(hgSchemaName(reg.Get(f.Target)))
		default:
			item = map[string]any{"type": "object", "additionalProperties": true}
		}
		out = item
		if many {
			out = map[string]any{"type": "array", "items": item}
		}
	case "component":
		if comp := reg.Get(f.Component); comp != nil {
			out = hgRef(hgSchemaName(comp))
		} else {
			out = map[string]any{"type": "object"}
		}
		if f.Repeatable {
			out = map[string]any{"type": "array", "items": out}
		}
	case "dynamiczone":
		var one []any
		for _, uid := range f.Components {
			if comp := reg.Get(uid); comp != nil {
				one = append(one, hgRef(hgSchemaName(comp)))
			}
		}
		item := map[string]any{"type": "object"}
		if len(one) > 0 {
			item = map[string]any{"oneOf": one}
		}
		out = map[string]any{"type": "array", "items": item}
	default:
		out = map[string]any{}
	}
	if f.Type != "relation" && f.Type != "media" && f.Type != "component" && f.Type != "dynamiczone" && !f.Required && !input {
		out["nullable"] = true
	}
	return out
}

// hgObjectSchema is the JSON schema of a type (or component) object. input drops the system
// attributes and the private/read-only ones are never present on output.
func hgObjectSchema(reg *hgRegistry, sc *hgSchema, input bool) map[string]any {
	props := map[string]any{}
	var required []any
	if !input && !sc.IsComponent() {
		props["id"] = map[string]any{"type": "integer"}
		props["documentId"] = map[string]any{"type": "string"}
		props["createdAt"] = map[string]any{"type": "string", "format": "date-time"}
		props["updatedAt"] = map[string]any{"type": "string", "format": "date-time"}
		props["publishedAt"] = map[string]any{"type": "string", "format": "date-time", "nullable": true}
		if sc.Localized {
			props["locale"] = map[string]any{"type": "string"}
		}
		required = append(required, "id", "documentId")
	}
	if !input && sc.IsComponent() {
		props["id"] = map[string]any{"type": "integer"}
	}
	for i := range sc.Fields {
		f := &sc.Fields[i]
		if f.Private || (f.Type == "password" && !input) || (f.Type == "relation" && f.MappedBy != "" && input) {
			continue
		}
		props[f.Name] = hgFieldSchema(reg, f, input)
		if f.Required && input {
			required = append(required, f.Name)
		}
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}
	if sc.Description != "" {
		out["description"] = sc.Description
	}
	return out
}

// hgVisibleTypes are the content types a caller may use at all (any action).
func (s *Server) hgVisibleTypes(c *gin.Context, a *hgAuth) []*hgSchema {
	var out []*hgSchema
	for _, sc := range s.hgReg(c).Types() {
		for _, act := range []string{"find", "findOne", "create", "update", "delete"} {
			if a.can(sc.UID, act) {
				out = append(out, sc)
				break
			}
		}
	}
	return out
}

func hgParams(sc *hgSchema, list bool) []any {
	q := func(name, desc string, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "in": "query", "description": desc, "schema": schema, "style": "deepObject", "explode": true}
	}
	p := []any{
		q("populate", "Relations, media and components to include (populate=*, populate[author]=true)", map[string]any{"type": "object"}),
		q("fields", "Attributes to return", map[string]any{"type": "array", "items": map[string]any{"type": "string"}}),
	}
	if sc.DraftAndPublish {
		p = append(p, map[string]any{"name": "status", "in": "query", "schema": map[string]any{"type": "string", "enum": []any{"published", "draft"}}})
	}
	if sc.Localized {
		p = append(p, map[string]any{"name": "locale", "in": "query", "schema": map[string]any{"type": "string"}})
	}
	if list {
		p = append(p,
			map[string]any{"name": "_q", "in": "query", "description": "Full-text search over the text attributes", "schema": map[string]any{"type": "string"}},
			q("filters", "Strapi-style filters (filters[title][$containsi]=go)", map[string]any{"type": "object"}),
			map[string]any{"name": "sort", "in": "query", "schema": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
			q("pagination", "pagination[page], pagination[pageSize] or pagination[start], pagination[limit]", map[string]any{"type": "object"}))
	}
	return p
}

// hgOpenAPI builds the OpenAPI 3.0 document for what the caller may do.
func (s *Server) hgOpenAPI(c *gin.Context, a *hgAuth) map[string]any {
	reg := s.hgReg(c)
	schemas := map[string]any{
		"Error": map[string]any{"type": "object", "properties": map[string]any{"data": map[string]any{"nullable": true},
			"error": map[string]any{"type": "object", "properties": map[string]any{"status": map[string]any{"type": "integer"}, "name": map[string]any{"type": "string"},
				"message": map[string]any{"type": "string"}, "details": map[string]any{}}}}},
		"Pagination": map[string]any{"type": "object", "properties": map[string]any{"page": map[string]any{"type": "integer"}, "pageSize": map[string]any{"type": "integer"},
			"pageCount": map[string]any{"type": "integer"}, "total": map[string]any{"type": "integer"}}},
	}
	paths := map[string]any{}
	used := map[string]bool{}
	var useComps func(sc *hgSchema)
	useComps = func(sc *hgSchema) {
		for _, f := range sc.Fields {
			var uids []string
			if f.Type == "component" {
				uids = []string{f.Component}
			}
			if f.Type == "dynamiczone" {
				uids = f.Components
			}
			if f.Type == "relation" {
				uids = []string{f.Target}
			}
			for _, uid := range uids {
				if t := reg.Get(uid); t != nil && !used[uid] {
					used[uid] = true
					useComps(t)
				}
			}
		}
	}
	resp := func(desc string, schema map[string]any) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	errs := map[string]any{"400": resp("Validation error", hgRef("Error")), "401": resp("Unauthorized", hgRef("Error")), "403": resp("Forbidden", hgRef("Error")), "404": resp("Not found", hgRef("Error"))}
	withErrs := func(r map[string]any) map[string]any {
		for k, v := range errs {
			r[k] = v
		}
		return r
	}
	for _, sc := range s.hgVisibleTypes(c, a) {
		name := hgSchemaName(sc)
		used[sc.UID] = true
		useComps(sc)
		body := map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
			"type": "object", "required": []any{"data"}, "properties": map[string]any{"data": hgRef(name + "Input")}}}}}
		one := resp("The entry", map[string]any{"type": "object", "properties": map[string]any{"data": hgRef(name), "meta": map[string]any{"type": "object"}}})
		list := resp("The entries", map[string]any{"type": "object", "properties": map[string]any{
			"data": map[string]any{"type": "array", "items": hgRef(name)}, "meta": map[string]any{"type": "object", "properties": map[string]any{"pagination": hgRef("Pagination")}}}})
		op := func(id, summary string, params []any, r map[string]any) map[string]any {
			m := map[string]any{"operationId": id, "summary": summary, "tags": []any{sc.DisplayName}, "responses": withErrs(map[string]any{"200": r})}
			if len(params) > 0 {
				m["parameters"] = params
			}
			return m
		}
		idParam := map[string]any{"name": "documentId", "in": "path", "required": true, "schema": map[string]any{"type": "string"}}
		if sc.IsSingle() {
			item := map[string]any{}
			if a.can(sc.UID, "findOne") || a.can(sc.UID, "find") {
				item["get"] = op("get"+name, "Get the "+sc.DisplayName, hgParams(sc, false), one)
			}
			if a.can(sc.UID, "update") || a.can(sc.UID, "create") {
				m := op("update"+name, "Update the "+sc.DisplayName, nil, one)
				m["requestBody"] = body
				item["put"] = m
			}
			if a.can(sc.UID, "delete") {
				item["delete"] = op("delete"+name, "Delete the "+sc.DisplayName, nil, map[string]any{"description": "Deleted"})
			}
			if len(item) > 0 {
				paths["/"+sc.SingularName] = item
			}
		} else {
			coll := map[string]any{}
			if a.can(sc.UID, "find") {
				coll["get"] = op("find"+name, "List "+sc.DisplayName+" entries", hgParams(sc, true), list)
			}
			if a.can(sc.UID, "create") {
				m := op("create"+name, "Create a "+sc.DisplayName+" entry", nil, one)
				m["requestBody"] = body
				coll["post"] = m
			}
			if len(coll) > 0 {
				paths["/"+sc.PluralName] = coll
			}
			item := map[string]any{}
			if a.can(sc.UID, "findOne") {
				item["get"] = op("findOne"+name, "Get a "+sc.DisplayName+" entry", append([]any{idParam}, hgParams(sc, false)...), one)
			}
			if a.can(sc.UID, "update") {
				m := op("update"+name, "Update a "+sc.DisplayName+" entry", []any{idParam}, one)
				m["requestBody"] = body
				item["put"] = m
			}
			if a.can(sc.UID, "delete") {
				item["delete"] = op("delete"+name, "Delete a "+sc.DisplayName+" entry", []any{idParam}, map[string]any{"description": "Deleted"})
			}
			if len(item) > 0 {
				paths["/"+sc.PluralName+"/{documentId}"] = item
			}
		}
	}
	for uid := range used {
		sc := reg.Get(uid)
		if sc == nil {
			continue
		}
		schemas[hgSchemaName(sc)] = hgObjectSchema(reg, sc, false)
		if !sc.IsComponent() {
			schemas[hgSchemaName(sc)+"Input"] = hgObjectSchema(reg, sc, true)
		}
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Pursae Headless API", "version": Version, "description": "Generated from the content model; shows only what the caller may use."},
		"servers": []any{map[string]any{"url": "/" + hgNamespace}},
		"paths":   paths,
		"components": map[string]any{
			"schemas":         schemas,
			"securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}},
		},
		"security": []any{map[string]any{}, map[string]any{"bearer": []any{}}},
	}
}

// hgTSType renders a JSON schema produced above as a TypeScript type.
func hgTSType(sch map[string]any) string {
	if ref, ok := sch["$ref"].(string); ok {
		return strings.TrimPrefix(ref, "#/components/schemas/")
	}
	ts := ""
	switch {
	case sch["oneOf"] != nil:
		var parts []string
		for _, o := range sch["oneOf"].([]any) {
			parts = append(parts, hgTSType(o.(map[string]any)))
		}
		ts = strings.Join(parts, " | ")
	case sch["enum"] != nil:
		var parts []string
		for _, v := range sch["enum"].([]any) {
			parts = append(parts, fmt.Sprintf("%q", v))
		}
		ts = strings.Join(parts, " | ")
	default:
		switch sch["type"] {
		case "string":
			ts = "string"
		case "integer", "number":
			ts = "number"
		case "boolean":
			ts = "boolean"
		case "array":
			item := "unknown"
			if m, ok := sch["items"].(map[string]any); ok {
				item = hgTSType(m)
			}
			if strings.Contains(item, " ") {
				item = "(" + item + ")"
			}
			ts = item + "[]"
		case "object":
			ts = "Record<string, unknown>"
		default:
			ts = "unknown"
		}
	}
	if n, _ := sch["nullable"].(bool); n {
		ts += " | null"
	}
	return ts
}

var hgTSIdent = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// hgTypeScript renders the types of the document hgOpenAPI built.
func hgTypeScript(doc map[string]any) string {
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	names := make([]string, 0, len(schemas))
	for n := range schemas {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("// Generated by Pursae Headless from the content model. Do not edit.\n\n")
	for _, n := range names {
		obj := schemas[n].(map[string]any)
		props, _ := obj["properties"].(map[string]any)
		if n == "Error" {
			continue
		}
		req := map[string]bool{}
		if l, ok := obj["required"].([]any); ok {
			for _, r := range l {
				req[r.(string)] = true
			}
		}
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "export interface %s {\n", n)
		for _, k := range keys {
			key := k
			if !hgTSIdent.MatchString(k) {
				key = fmt.Sprintf("%q", k)
			}
			opt := "?"
			if req[k] {
				opt = ""
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", key, opt, hgTSType(props[k].(map[string]any)))
		}
		b.WriteString("}\n\n")
	}
	return b.String()
}

func (s *Server) hgOpenAPIEndpoint(c *gin.Context, a *hgAuth, ts bool) {
	doc := s.hgOpenAPI(c, a)
	if ts {
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(hgTypeScript(doc)))
		return
	}
	hgWriteJSON(c, http.StatusOK, doc)
}
