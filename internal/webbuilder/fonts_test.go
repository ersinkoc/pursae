package webbuilder

import (
	"reflect"
	"testing"
)

func TestDocFonts(t *testing.T) {
	r := mustParse(t, string(readSample(t)), DocOptions{})
	got := DocFonts(&r.Doc)
	want := []FontNeed{
		{Family: "Inter", Weights: []int{400, 500, 600, 700}, Italic: true},
		{Family: "Playfair Display", Weights: []int{400, 700}, Italic: true},
		{Family: "Space Grotesk", Weights: []int{400, 600, 700}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DocFonts=%+v\nwant     %+v", got, want)
	}
}

func TestDocFontsPairsAndUnknown(t *testing.T) {
	w := 900.0
	d := DocState{Site: SiteSettings{FontPair: "elegant", HeadingFont: "Arial", BodyFont: "No Such Font"}}
	if got := DocFonts(&d); len(got) != 0 {
		t.Fatalf("system/unknown fonts: %+v", got)
	}
	d = DocState{Site: SiteSettings{FontPair: "elegant", HeadingWeight: &w, BodyFont: "Arial"}}
	got := DocFonts(&d)
	// Instrument Serif ships 400 only (and a 400 italic): 900 snaps to 400.
	if len(got) != 1 || got[0].Family != "Instrument Serif" || !reflect.DeepEqual(got[0].Weights, []int{400}) || !got[0].Italic {
		t.Fatalf("elegant pair: %+v", got)
	}
}

func TestFontCSS2Query(t *testing.T) {
	if q := FontCSS2Query(FontNeed{Family: "Space Grotesk", Weights: []int{400, 700}}); q != "family=Space+Grotesk:wght@400;700&display=swap" {
		t.Fatal(q)
	}
	if q := FontCSS2Query(FontNeed{Family: "Inter", Weights: []int{400}, Italic: true}); q != "family=Inter:ital,wght@0,400;1,400&display=swap" {
		t.Fatal(q)
	}
	if _, ok := LookupFont("'playfair display'"); !ok {
		t.Fatal("LookupFont quoted/case")
	}
}
