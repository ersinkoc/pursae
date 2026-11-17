package server

import (
	"encoding/json"
	"reflect"
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
		if got := wbCodeSectionsOf(json.RawMessage(tc.doc)); len(got) != tc.want {
			t.Errorf("%s: %d sections, want %d", tc.doc, len(got), tc.want)
		}
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
