package server

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Relation targets that point at WordPress objects: wp:post, wp:page and wp:user, plus
// wp:type:<post type> for any other public post type (Pursae Commerce products, Pursae Custom
// Fields types) and wp:term:<taxonomy> for the terms of a public taxonomy (categories, tags,
// product categories, custom taxonomies).

var hgWPSlugRe = regexp.MustCompile(`^[a-z0-9_-]{1,32}$`)

// hgWPPostType is the post type a post-like target stands for.
func hgWPPostType(target string) (string, bool) {
	switch target {
	case "wp:post":
		return "post", true
	case "wp:page":
		return "page", true
	}
	if t, ok := strings.CutPrefix(target, "wp:type:"); ok && hgWPSlugRe.MatchString(t) {
		return t, true
	}
	return "", false
}

// hgWPTaxonomy is the taxonomy a term target stands for.
func hgWPTaxonomy(target string) (string, bool) {
	if t, ok := strings.CutPrefix(target, "wp:term:"); ok && hgWPSlugRe.MatchString(t) {
		return t, true
	}
	return "", false
}

// hgWPTargetKnown reports whether target is a well-formed WordPress target.
func hgWPTargetKnown(target string) bool {
	if target == "wp:user" {
		return true
	}
	if _, ok := hgWPPostType(target); ok {
		return true
	}
	_, ok := hgWPTaxonomy(target)
	return ok
}

// hgWPTargetLabels are the targets the schema builder offers: the fixed ones, every public post
// type besides posts and pages, and every public taxonomy.
func (s *Server) hgWPTargetList(ctx context.Context) []struct{ Key, Label string } {
	out := append([]struct{ Key, Label string }(nil), hgWPTargets...)
	for _, t := range s.sitemapPostTypes(ctx) {
		if t != "post" && t != "page" {
			out = append(out, struct{ Key, Label string }{"wp:type:" + t, "WordPress " + t + " entries"})
		}
	}
	for _, t := range s.sitemapTaxonomies(ctx) {
		out = append(out, struct{ Key, Label string }{"wp:term:" + t, "WordPress terms: " + t})
	}
	return out
}

// hgCheckWPTargets rejects relation targets that name a post type or taxonomy that is not registered
// and public.
func (s *Server) hgCheckWPTargets(ctx context.Context, sc *hgSchema) error {
	var types, taxes []string
	for _, f := range sc.Fields {
		if f.Type != "relation" {
			continue
		}
		if t, ok := hgWPPostType(f.Target); ok && f.Target != "wp:post" && f.Target != "wp:page" {
			if types == nil {
				types = s.sitemapPostTypes(ctx)
			}
			if !containsStr(types, t) {
				return hgSchemaError{fmt.Sprintf("Field %s: %q is not a public post type.", f.Name, t)}
			}
		}
		if t, ok := hgWPTaxonomy(f.Target); ok {
			if taxes == nil {
				taxes = s.sitemapTaxonomies(ctx)
			}
			if !containsStr(taxes, t) {
				return hgSchemaError{fmt.Sprintf("Field %s: %q is not a public taxonomy.", f.Name, t)}
			}
		}
	}
	return nil
}

func hgIsWPPost(target string) bool { _, ok := hgWPPostType(target); return ok }
func hgIsWPTerm(target string) bool { _, ok := hgWPTaxonomy(target); return ok }
