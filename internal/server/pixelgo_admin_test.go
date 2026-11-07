package server

import (
	"os"
	"testing"
)

// The admin template must parse together with the rest of the admin templates.
func TestPixelAdminTemplateParses(t *testing.T) {
	tp := &templates{s: &Server{}}
	if _, err := parseTree(os.DirFS("../../web"), "admin/templates", tp.funcs()); err != nil {
		t.Fatal(err)
	}
}

func TestPixelSanitizeAdminInputs(t *testing.T) {
	st := pixelDefaults()
	st.Platforms["meta"] = pixelPlatformCfg{Enabled: true, IDs: []string{"123456789012345", "bad", "123456789012345"}, Token: "a b", ServerSide: true}
	st.Custom = []pixelCustomEvent{{Name: "<x>", Trigger: "click", Selector: ".a"}, {Name: "Buy", Trigger: "click", Selector: ".a{"}, {Name: "Buy Now", Trigger: "click", Selector: ".buy", Number: 9}}
	st = pixelSanitize(st)
	if got := st.Platforms["meta"]; len(got.IDs) != 1 || got.Token != "" || got.ServerSide {
		t.Errorf("meta sanitized wrong: %+v", got)
	}
	if len(st.Custom) != 1 || st.Custom[0].Name != "Buy Now" || st.Custom[0].Number != 0 || st.Custom[0].ID == "" {
		t.Errorf("custom sanitized wrong: %+v", st.Custom)
	}
}
