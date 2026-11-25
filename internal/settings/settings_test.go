package settings

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every default is stored as JSONB, so each must marshal.
func TestDefaultsMarshalToJSON(t *testing.T) {
	for k, v := range Defaults {
		if k == "" {
			t.Error("empty option name")
		}
		if _, err := json.Marshal(v); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
}

// Native plugins other than Classic Editor start switched off on a fresh install.
func TestNativePluginsStartOff(t *testing.T) {
	for k, v := range Defaults {
		if !strings.HasPrefix(k, "native_") || k == "native_classic_editor" {
			continue
		}
		if on, ok := v.(bool); !ok || on {
			t.Errorf("%s defaults to %v, want false", k, v)
		}
	}
}

func TestDefaultsKeepWordPressNames(t *testing.T) {
	for _, k := range []string{"blogname", "blogdescription", "siteurl", "home", "admin_email", "default_role", "date_format", "time_format"} {
		if _, ok := Defaults[k]; !ok {
			t.Errorf("missing WordPress option %s", k)
		}
	}
}

func TestGetReturnsIndependentNestedValues(t *testing.T) {
	s := &Store{cache: map[string]any{"nested": map[string]any{"items": []any{map[string]any{"name": "original"}}}}}
	got := s.Get("nested").(map[string]any)
	got["items"].([]any)[0].(map[string]any)["name"] = "changed"
	if name := s.Get("nested").(map[string]any)["items"].([]any)[0].(map[string]any)["name"]; name != "original" {
		t.Fatalf("cached name changed: %v", name)
	}
}

func TestLookupReadsOneMember(t *testing.T) {
	s := &Store{cache: map[string]any{
		"obj":  map[string]any{"on": true, "nested": map[string]any{"k": "v"}},
		"str":  "text",
		"null": nil,
	}}
	if v, ok := s.Lookup("obj", "on"); !ok || v != true {
		t.Fatalf("obj.on = %v, %v", v, ok)
	}
	if v, ok := s.Lookup("obj", "missing"); !ok || v != nil {
		t.Fatalf("obj.missing = %v, %v", v, ok)
	}
	nested, _ := s.Lookup("obj", "nested")
	nested.(map[string]any)["k"] = "changed"
	if v, _ := s.Lookup("obj", "nested"); v.(map[string]any)["k"] != "v" {
		t.Fatal("a caller's change reached the cache")
	}
	for _, k := range []string{"str", "null", "absent"} {
		if v, ok := s.Lookup(k, "on"); ok || v != nil {
			t.Fatalf("%s: %v, %v", k, v, ok)
		}
	}
}

func TestMemoFollowsRevision(t *testing.T) {
	s := &Store{cache: map[string]any{"n": 1.0}}
	var m Memo[int]
	builds := 0
	build := func() int { builds++; return s.Int("n") }
	if v := m.Get(s, build); v != 1 || builds != 1 {
		t.Fatalf("first Get: %d after %d builds", v, builds)
	}
	if v := m.Get(s, build); v != 1 || builds != 1 {
		t.Fatalf("hit: %d after %d builds", v, builds)
	}
	s.mu.Lock()
	s.cache["n"] = 2.0
	s.cacheRevision++
	s.mu.Unlock()
	if v := m.Get(s, build); v != 2 || builds != 2 {
		t.Fatalf("after a revision: %d after %d builds", v, builds)
	}
	other := &Store{cache: map[string]any{"n": 3.0}, cacheRevision: s.Revision()}
	if v := m.Get(other, func() int { return other.Int("n") }); v != 3 {
		t.Fatalf("another store at the same revision got %d", v)
	}
	if _, ok := m.Peek(s); ok {
		t.Fatal("Peek returned another store's value")
	}
}
