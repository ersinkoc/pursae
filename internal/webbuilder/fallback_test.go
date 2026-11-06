package webbuilder

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "fallback", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run %s -update)", err, t.Name())
	}
	if string(want) != got {
		t.Fatalf("%s differs\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestFallbackGolden(t *testing.T) {
	r := mustParse(t, string(readSample(t)), DocOptions{UnfilteredHTML: true})
	got := FallbackHTML(&r.Doc) + "\n"
	golden(t, "sample.html", got)

	for _, absent := range []string{"Should not appear", "<script", "<form", "<input", "Write to us", "javascript:", "onload"} {
		if strings.Contains(got, absent) {
			t.Errorf("fallback contains %q", absent)
		}
	}
	for _, want := range []string{"<h1>Build pages <b>faster</b><br>without code</h1>", "<h2>Everything you need</h2>", "<h3>Speed</h3>", "<blockquote>", `<a href="#features">Features</a>`, `<img src="/wp-content/uploads/hero.jpg"`, "<figcaption>Our office</figcaption>", "<h3>Custom title</h3>", "<hr>", "Nested text", `<section id="about">`, "About &lt;script&gt;"} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback lacks %q", want)
		}
	}
	// The fallback is a valid, safe fragment (it is what themes and feeds print).
	nodes, err := parseFragment(got)
	if err != nil || len(nodes) == 0 {
		t.Fatal(err)
	}
	if again, err := renderNodes(nodes); err != nil || strings.Contains(again, "<script") {
		t.Fatal("fallback reparse")
	}
}

func TestFallbackOneH1(t *testing.T) {
	raw := `{"sections":[
		{"id":"a","kind":"hero","name":"","hidden":false,"content":{"title":"One"},"items":[],"style":{"palette":"brand","align":"left","padding":0,"titleSize":1,"hideOn":[]}},
		{"id":"b","kind":"hero","name":"","hidden":false,"content":{"title":"Two"},"items":[],"style":{"palette":"brand","align":"left","padding":0,"titleSize":1,"hideOn":[]}},
		{"id":"c","kind":"text","name":"","hidden":false,"content":{},"items":[],"style":{"palette":"brand","align":"left","padding":0,"titleSize":1,"hideOn":[]}}],"site":{}}`
	r := mustParse(t, raw, DocOptions{})
	got := FallbackHTML(&r.Doc)
	if strings.Count(got, "<h1>") != 1 || !strings.Contains(got, "<h2>Two</h2>") || strings.Contains(got, `id="c"`) {
		t.Fatalf("fallback: %s", got)
	}
}
