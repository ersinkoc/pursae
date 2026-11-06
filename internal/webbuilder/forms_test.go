package webbuilder

import (
	"strings"
	"testing"
)

func TestFormSchemas(t *testing.T) {
	r := mustParse(t, string(readSample(t)), DocOptions{})
	list := FormSchemas(&r.Doc)
	if len(list) != 1 {
		t.Fatalf("schemas=%+v", list)
	}
	fs := list[0]
	if fs.SectionID != "contact" || fs.Variant != "contact-list" || fs.Title != "Write to us" {
		t.Fatalf("schema=%+v", fs)
	}
	var names []string
	for _, f := range fs.Fields {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "name,email,choice,message" {
		t.Fatalf("fields=%v", names)
	}
	if fs.Fields[0].Label != "Your name" || fs.Fields[2].Label != "Topic" || !fs.Fields[1].Required {
		t.Fatalf("labels=%+v", fs.Fields)
	}
	if _, ok := FindForm(&r.Doc, "hero"); ok {
		t.Fatal("non-form section found")
	}
	if _, ok := FindForm(&r.Doc, "contact"); !ok {
		t.Fatal("form not found")
	}
}

func TestFormSchemaVariants(t *testing.T) {
	s := Section{ID: "f", Kind: KindForm, Content: map[string]string{"variant": "newsletter"}}
	if fs := schemaOf(&s); len(fs.Fields) != 1 || fs.Fields[0].Name != "email" {
		t.Fatalf("newsletter=%+v", fs)
	}
	s.Content["variant"] = "no-such-design"
	if fs := schemaOf(&s); fs.Variant != "default" || len(fs.Fields) != 3 {
		t.Fatalf("unknown=%+v", fs)
	}
	for v, names := range formVariants {
		for _, n := range names {
			ok := false
			for _, f := range FormFields {
				ok = ok || f == n
			}
			if !ok {
				t.Errorf("variant %s uses %q, not in FormFields", v, n)
			}
		}
	}
}

func TestFormValidate(t *testing.T) {
	fs := schemaOf(&Section{ID: "f", Kind: KindForm, Content: map[string]string{"variant": "contact-list"}})
	clean, errs := fs.Validate(map[string]string{
		"name":    "  Ada\r\nBcc: x@y.z ",
		"email":   "ada@example.com",
		"message": "Hello\r\nthere\x00",
		"choice":  strings.Repeat("x", FormOtherMax+1),
		"other":   "ignored",
	})
	if errs["choice"] != FormTooLong || len(errs) != 1 {
		t.Fatalf("errs=%v", errs)
	}
	if clean["name"] != "Ada  Bcc: x@y.z" || clean["message"] != "Hello\nthere" || clean["other"] != "" {
		t.Fatalf("clean=%q", clean)
	}
	_, errs = fs.Validate(map[string]string{"email": "Ada <ada@example.com>", "message": ""})
	if errs["email"] != FormInvalid || errs["message"] != FormRequired {
		t.Fatalf("errs=%v", errs)
	}
	_, errs = fs.Validate(map[string]string{"email": "ada@localhost", "message": "x"})
	if errs["email"] != FormInvalid {
		t.Fatalf("errs=%v", errs)
	}
	_, errs = fs.Validate(map[string]string{})
	if errs["email"] != FormRequired {
		t.Fatalf("errs=%v", errs)
	}
}
