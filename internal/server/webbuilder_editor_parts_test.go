package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The header / footer templates the element editor draws: visible code sections only, and only
// when every visible section is a code section.
func TestWBCodeSectionsOf(t *testing.T) {
	code := `{"id":"a","kind":"code","content":{"html":"<header data-wbc=\"1\">H</header>"}}`
	hidden := `{"id":"b","kind":"hero","hidden":true,"content":{}}`
	for _, tc := range []struct {
		doc  string
		want int
	}{
		{`{"sections":[` + code + `]}`, 1},
		{`{"sections":[` + code + `,` + hidden + `]}`, 1},
		{`{"sections":[` + code + `,{"id":"c","kind":"hero","content":{}}]}`, 0},
		{`{"sections":[]}`, 0},
		{`nope`, 0},
	} {
		if got := wbCodeSectionsOf(json.RawMessage(tc.doc), true); len(got) != tc.want {
			t.Errorf("%s: %d sections, want %d", tc.doc, len(got), tc.want)
		}
	}
}

// Without edit_theme_options the parts lose their content bindings (a bound form's recipients and
// redirects); the markup stays.
func TestWBCodeSectionsOfBinds(t *testing.T) {
	doc := json.RawMessage(`{"sections":[{"id":"a","kind":"code","content":{"html":"<form data-wbc=\"1\"></form>","binds":"{\"1\":{\"kind\":\"form\",\"to\":\"boss@example.com\",\"cc\":\"x@example.com\",\"redirect\":\"https://example.com/t\"}}"}},{"id":"b","kind":"code","content":{"html":"<p>F</p>"}}]}`)
	kept := wbCodeSectionsOf(doc, true)
	if len(kept) != 2 || !strings.Contains(string(kept[0]), "boss@example.com") {
		t.Fatalf("an edit_theme_options user keeps the bindings: %s", kept)
	}
	got := wbCodeSectionsOf(doc, false)
	if len(got) != 2 {
		t.Fatalf("%d sections", len(got))
	}
	all := string(got[0]) + string(got[1])
	for _, leak := range []string{"boss@example.com", "x@example.com", "binds", "example.com/t"} {
		if strings.Contains(all, leak) {
			t.Errorf("parts leak %q: %s", leak, all)
		}
	}
	var sec struct {
		ID      string            `json:"id"`
		Kind    string            `json:"kind"`
		Content map[string]string `json:"content"`
	}
	if json.Unmarshal(got[0], &sec) != nil || sec.ID != "a" || sec.Kind != "code" || sec.Content["html"] != `<form data-wbc="1"></form>` {
		t.Errorf("markup lost: %s", got[0])
	}
}

// The editor's CSP names the origins of the code sections' head markup (a web font stylesheet).
func TestWBCodeHeadOrigins(t *testing.T) {
	doc := json.RawMessage(`{"sections":[{"kind":"code","content":{"head":"<link rel=\"stylesheet\" href=\"https://fonts.googleapis.com/css2?family=Inter\"/>","html":"<a href=\"https://evil.example/\">x</a>"}},{"kind":"hero","content":{"head":"<script src=\"https://no.example/a.js\"></script>"}}]}`)
	part := json.RawMessage(`{"kind":"code","content":{"head":"<link rel=\"preconnect\" href=\"https://fonts.gstatic.com\" crossorigin/>"}}`)
	got := wbCodeHeadOrigins(doc, nil, part)
	want := []string{"https://fonts.googleapis.com", "https://fonts.gstatic.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("origins = %v, want %v", got, want)
	}
}
