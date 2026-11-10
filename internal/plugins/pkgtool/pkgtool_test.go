package pkgtool

import (
	"os/exec"
	"path/filepath"
	"testing"

	"wpgo/internal/plugins"
)

func TestScaffoldBuildVerify(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	root, _ := filepath.Abs("../../..")
	dir := filepath.Join(t.TempDir(), "demo-plugin")
	if err := Run([]string{"new", "-slug", "demo-plugin", "-dir", dir, "-sdk", root}); err != nil {
		t.Fatal(err)
	}
	if err := Run([]string{"new", "-slug", "demo-plugin", "-dir", dir}); err == nil {
		t.Fatal("scaffolding over an existing plugin must fail")
	}
	if err := Run([]string{"new", "-slug", "Bad Slug", "-dir", dir + "2"}); err == nil {
		t.Fatal("invalid slug must fail")
	}
	// The generated unit test runs on a normal toolchain against the fake host.
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scaffold test failed: %v\n%s", err, out)
	}
	zip := filepath.Join(t.TempDir(), "demo.zip")
	if err := Run([]string{"build", "-dir", dir, "-out", zip}); err != nil {
		t.Fatal(err)
	}
	pkg, err := plugins.LoadPackageFile(zip, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Manifest.Slug != "demo-plugin" {
		t.Fatalf("slug %q", pkg.Manifest.Slug)
	}
}

func TestRunUnknown(t *testing.T) {
	if Run([]string{"nope"}) == nil || Run(nil) == nil {
		t.Fatal("expected errors")
	}
}
