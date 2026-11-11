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
