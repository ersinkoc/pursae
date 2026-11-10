package server

import (
	"context"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// restSettingKind is the schema type of a /wp/v2/settings field (register_setting()'s show_in_rest).
type restSettingKind int

const (
	rsString restSettingKind = iota
	rsInt
	rsBool
)

// restSettingKinds types every restSettingsMap key; anything else is a string.
var restSettingKinds = map[string]restSettingKind{
	"start_of_week": rsInt, "default_category": rsInt, "posts_per_page": rsInt, "page_on_front": rsInt,
	"page_for_posts": rsInt, "site_logo": rsInt, "site_icon": rsInt,
	"use_smilies": rsBool,
}

// restSettingEnums are the fields whose schema has an enum.
var restSettingEnums = map[string][]string{
	"show_on_front":          {"posts", "page"},
	"default_ping_status":    {"open", "closed"},
	"default_comment_status": {"open", "closed"},
}

// restCoerceSetting is rest_sanitize_value_from_schema() for the scalar types: numbers and numeric
// strings for integers, booleans and "true"/"false"/"1"/"0" for booleans, strings for strings.
func restCoerceSetting(kind restSettingKind, v any) (any, bool) {
	switch kind {
	case rsInt:
		switch x := v.(type) {
		case float64:
			if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
				return nil, false
			}
			return int(x), true
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(x))
			return n, err == nil
		}
	case rsBool:
		switch x := v.(type) {
		case bool:
			return x, true
		case float64:
			if x == 0 || x == 1 {
				return x == 1, true
			}
		case string:
			switch strings.ToLower(x) {
			case "true", "1":
				return true, true
			case "false", "0", "":
				return false, true
			}
		}
	default:
		if s, ok := v.(string); ok {
			return s, true
		}
	}
	return nil, false
}

// sanitizeRESTSetting checks one /wp/v2/settings value (name is the REST name) and returns the value
// to store, or a message for rest_invalid_param. It applies the same rules as the Settings screens
// (admin_options.go): an http(s) site URL without the trailing slash, a valid e-mail, a known and
// installed language, a loadable time zone, posts_per_page >= 1 and existing pages / category.
func (s *Server) sanitizeRESTSetting(ctx context.Context, name string, v any) (any, string) {
	val, ok := restCoerceSetting(restSettingKinds[name], v)
	if !ok {
		switch restSettingKinds[name] {
		case rsInt:
			return nil, name + " is not of type integer."
		case rsBool:
			return nil, name + " is not of type boolean."
		}
		return nil, name + " is not of type string."
	}
	if enum, ok := restSettingEnums[name]; ok {
		str := val.(string)
		for _, e := range enum {
			if str == e {
				return str, ""
			}
		}
		return nil, name + " is not one of " + strings.Join(enum, " and ") + "."
	}
	switch name {
	case "url":
		str := strings.TrimSpace(val.(string))
		u, err := url.Parse(str)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, "Invalid URI."
		}
		return strings.TrimRight(str, "/"), ""
	case "email":
		str := strings.TrimSpace(val.(string))
		if !validEmail(str) {
			return nil, "Invalid email address."
		}
		return str, ""
	case "timezone":
		str := strings.TrimSpace(val.(string))
		if _, err := time.LoadLocation(str); err != nil || str == "Local" {
			return nil, "Invalid time zone."
		}
		return str, ""
	case "language":
		str := val.(string)
		if str == "en_US" {
			str = ""
		}
		if !validLanguage(str) {
			return nil, "Invalid language."
		}
		return str, "" // the pack is installed by saveSettings once every value is valid
	case "start_of_week":
		if n := val.(int); n < 0 || n > 6 {
			return nil, "start_of_week must be between 0 (Sunday) and 6 (Saturday)."
		}
	case "posts_per_page":
		if val.(int) < 1 {
			return nil, "posts_per_page must be greater than or equal to 1."
		}
	case "page_on_front", "page_for_posts":
		n := val.(int)
		if n < 0 {
			return nil, name + " must be greater than or equal to 0."
		}
		if n != 0 {
			if p, err := s.store.GetPost(ctx, int64(n)); err != nil || p.Type != "page" {
				return nil, "Invalid page ID."
			}
		}
	case "default_category":
		if t, err := s.store.GetTerm(ctx, int64(val.(int))); err != nil || t.Taxonomy != "category" {
			return nil, "Invalid category ID."
		}
	case "site_logo", "site_icon":
		if val.(int) < 0 {
			return nil, name + " must be greater than or equal to 0."
		}
	}
	return val, ""
}
