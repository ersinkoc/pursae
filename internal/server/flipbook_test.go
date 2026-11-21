package server

import (
	"strings"
	"testing"
)

func TestFlipbookSanitize(t *testing.T) {
	b := flipbookSanitize(flipbook{Title: "  Katalog ", PDFURL: "javascript:alert(1)", Height: 5, Background: "red"})
	if b.Title != "Katalog" || b.PDFURL != "" || b.Height != flipbookDefaultHeight || b.Background != flipbookDefaultBG {
		t.Fatalf("sanitize: %+v", b)
	}
	b = flipbookSanitize(flipbook{PDFURL: "/wp-content/uploads/a.pdf", Height: 480, Background: "#112233"})
	if b.PDFURL != "/wp-content/uploads/a.pdf" || b.Height != 480 || b.Background != "#112233" {
		t.Fatalf("sanitize kept values: %+v", b)
	}
}

func TestFlipbookIsPDF(t *testing.T) {
	for u, want := range map[string]bool{
		"/wp-content/uploads/a.pdf":       true,
		"https://cdn.example.com/b.PDF?x": true,
		"/wp-content/uploads/a.png":       false,
		"":                                false,
	} {
		if got := flipbookIsPDF(u, ""); got != want {
			t.Errorf("flipbookIsPDF(%q) = %v", u, got)
		}
	}
	if !flipbookIsPDF("/x/file", "application/pdf") {
		t.Error("the MIME type should be enough")
	}
}

func TestFlipbookEmbedHTMLEscapes(t *testing.T) {
	b := flipbookNew(`"><script>x</script>`, "/wp-content/uploads/a.pdf", "a.pdf", 3)
	b.ID = 7
	out := flipbookEmbedHTML(b, false)
	if strings.Contains(out, "<script>x") {
		t.Fatalf("title not escaped: %s", out)
	}
	for _, want := range []string{`data-pdf="/wp-content/uploads/a.pdf"`, `data-full="/flipbook/7"`, `data-share="/flipbook/7"`, `data-download="1"`, "--pfb-height:640px", "alignwide"} {
		if !strings.Contains(out, want) {
			t.Errorf("embed lacks %s", want)
		}
	}
	if full := flipbookEmbedHTML(b, true); strings.Contains(full, "data-full=") || strings.Contains(full, "--pfb-height") || !strings.Contains(full, "pfb--full") {
		t.Errorf("full page markup: %s", full)
	}
	if flipbookShortcodeText(7) != `[pursae_flipbook id="7"]` {
		t.Error("shortcode text")
	}
}
