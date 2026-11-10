package pkgtool

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"wpgo/internal/plugins"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

const manifestTmpl = `{
  "slug": %q,
  "name": %q,
  "version": "0.1.0",
  "description": "",
  "author": "",
  "requires": "0.1.0",
  "license": "GPL-2.0-or-later",
  "permissions": ["content:filter"],
  "hooks": ["the_content"]
}
`

const mainTmpl = `// Command %[1]s is a Pursae plugin.
package main

import (
	"wpgo/sdk/go/wpgo"
)

func init() {
	wpgo.AddFilter("the_content", 10, appendNote)
}

func appendNote(_ wpgo.Context, html string, _ []any) string {
	return html + "<p>" + wpgo.T("Hello from %[1]s.") + "</p>"
}

func main() {}
`

const testTmpl = `package main

import (
	"strings"
	"testing"

	"wpgo/sdk/go/wpgo"
	"wpgo/sdk/go/wpgo/wpgotest"
)

func TestAppendNote(t *testing.T) {
	wpgotest.New(t) // fake host: options, storage, posts, HTTP stubs ...
	got := appendNote(wpgo.Context{}, "<p>Hi</p>", nil)
	if !strings.HasPrefix(got, "<p>Hi</p>") || !strings.Contains(got, "Hello from") {
		t.Fatalf("unexpected content %q", got)
	}
}
`

// scaffold writes plugin.json, main.go, main_test.go, readme.md and go.mod for a new plugin.
func scaffold(args []string) error {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	slug := fs.String("slug", "", "plugin slug (lowercase letters, digits, dashes)")
	dir := fs.String("dir", "", "target directory (default ./SLUG)")
	sdk := fs.String("sdk", "../pursae", "path to the Pursae checkout that provides the wpgo module")
	name := fs.String("name", "", "display name (default: the slug, title-cased)")
	fs.Parse(args)
	if fs.NArg() == 1 && *slug == "" {
		*slug = fs.Arg(0)
	}
	if !slugRe.MatchString(*slug) {
		return errors.New("new: -slug must be lowercase letters, digits and dashes")
	}
	if *dir == "" {
		*dir = *slug
	}
	if *name == "" {
		parts := strings.Split(*slug, "-")
		for i, p := range parts {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
		*name = strings.Join(parts, " ")
	}
	if _, err := os.Stat(filepath.Join(*dir, plugins.ManifestFile)); err == nil {
		return fmt.Errorf("new: %s already has a plugin.json", *dir)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		plugins.ManifestFile: fmt.Sprintf(manifestTmpl, *slug, *name),
		"main.go":            fmt.Sprintf(mainTmpl, *slug),
		"main_test.go":       testTmpl,
		plugins.ReadmeFile:   "# " + *name + "\n\nDescribe what the plugin does.\n",
		"go.mod":             fmt.Sprintf("module example.com/%s\n\ngo 1.27\n\nrequire wpgo v0.0.0\n\nreplace wpgo => %s\n", *slug, *sdk),
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(*dir, n), []byte(c), 0o644); err != nil {
			return err
		}
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = *dir
	if out, err := tidy.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: go mod tidy failed (run it yourself): %v\n%s", err, out)
	}
	fmt.Printf("created %s (edit plugin.json permissions, then: wpgo plugin build -dir %s)\n", *dir, *dir)
	return nil
}

// build compiles the plugin in -dir to Wasm with the Go toolchain and packs it.
func build(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	dir := fs.String("dir", ".", "plugin directory (plugin.json + main package)")
	key := fs.String("key", "", "private key file (omit for an unsigned package)")
	out := fs.String("out", "", "output zip (default SLUG-VERSION.zip in -dir)")
	fs.Parse(args)
	manifest, err := os.ReadFile(filepath.Join(*dir, plugins.ManifestFile))
	if err != nil {
		return err
	}
	m, err := plugins.ParseManifest(manifest)
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "wpgo-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	wasm := filepath.Join(tmp, "plugin.wasm")
	cmd := exec.Command("go", "build", "-buildmode=c-shared", "-o", wasm, ".")
	cmd.Dir = *dir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build: go build failed: %w", err)
	}
	if *out == "" {
		*out = filepath.Join(*dir, m.Slug+"-"+m.Version+".zip")
	}
	packArgs := []string{"-dir", *dir, "-wasm", wasm, "-out", *out}
	if *key != "" {
		packArgs = append(packArgs, "-key", *key)
	}
	return pack(packArgs)
}
