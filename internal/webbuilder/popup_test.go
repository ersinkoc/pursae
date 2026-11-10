package webbuilder

import (
	"testing"
	"time"
)

func TestPopupSettings(t *testing.T) {
	if got := ParsePopup(""); got != DefaultPopupSettings() {
		t.Errorf("empty = %+v", got)
	}
	if ValidatePopup(DefaultPopupSettings()) != "" {
		t.Error("the defaults are invalid")
	}
	got := ParsePopup(`{"trigger":"click","selector":".open","frequency":"days","days":3,"position":"top","width":500,"delay":0,"scroll":10,"version":2}`)
	if got.Trigger != PopupTriggerClick || got.Selector != ".open" || got.Days != 3 || got.Position != PopupPosTop {
		t.Errorf("parsed = %+v", got)
	}
	// Bad stored values never break a page: the defaults come back.
	for _, raw := range []string{`{`, `{"trigger":"bogus"}`, `{"trigger":"click","selector":"a[href]"}`, `{"width":5}`} {
		if ParsePopup(raw) != DefaultPopupSettings() {
			t.Errorf("%s did not fall back", raw)
		}
	}
	bad := DefaultPopupSettings()
	for name, mut := range map[string]func(*PopupSettings){
		"trigger":   func(s *PopupSettings) { s.Trigger = "hover" },
		"frequency": func(s *PopupSettings) { s.Frequency = "x" },
		"position":  func(s *PopupSettings) { s.Position = "left" },
		"delay":     func(s *PopupSettings) { s.Delay = 301 },
		"scroll":    func(s *PopupSettings) { s.Scroll = 0 },
		"days":      func(s *PopupSettings) { s.Days = 366 },
		"width":     func(s *PopupSettings) { s.Width = 1601 },
		"selector":  func(s *PopupSettings) { s.Trigger, s.Selector = PopupTriggerClick, "div > a" },
	} {
		s := bad
		mut(&s)
		if ValidatePopup(s) == "" {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestPickTemplatesPopups(t *testing.T) {
	t0 := time.Unix(1000, 0)
	cands := []TemplateCandidate{
		{ID: 1, Type: TemplatePopup, Conditions: ParseConditions([]string{"include/general"}), Modified: t0},
		{ID: 2, Type: TemplatePopup, Conditions: ParseConditions([]string{"include/singular/post"}), Modified: t0},
		{ID: 3, Type: TemplatePopup, Conditions: ParseConditions([]string{"include/archive"}), Modified: t0},
		{ID: 4, Type: TemplateHeader, Conditions: ParseConditions([]string{"include/general"}), Modified: t0},
	}
	post := QueryFacts{View: ViewSingular, PostType: "post"}
	got := PickTemplates(LocationPopup, cands, post)
	if len(got) != 2 || cands[got[0]].ID != 2 || cands[got[1]].ID != 1 {
		t.Errorf("single post: %v", got)
	}
	if got := PickTemplates(LocationPopup, cands, QueryFacts{View: ViewArchive, Archive: ArchiveHome}); len(got) != 2 || cands[got[0]].ID != 3 {
		t.Errorf("archive: %v", got)
	}
	if got := PickTemplates(LocationPopup, cands, QueryFacts{View: View404}); len(got) != 1 || cands[got[0]].ID != 1 {
		t.Errorf("404: %v", got)
	}
	// A popup shows on store views too (only main templates are gated).
	if got := PickTemplates(LocationPopup, cands, QueryFacts{View: ViewArchive, Archive: ArchivePostType, Store: true}); len(got) == 0 {
		t.Error("no popup on a store view")
	}
	if TemplateLocation(TemplatePopup) != LocationPopup || PickTemplate(LocationMain, cands, post) != -1 {
		t.Error("a popup fills the main location")
	}
}
