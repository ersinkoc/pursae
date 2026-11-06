package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"wpgo/internal/webbuilder"
)

// TestWebBuilderDistIsCurrent catches a stale committed web-builder dist: BUILD.json must name
// the pinned upstream SHA (builder/UPSTREAM_SHA), exactly the patches in builder/patches with
// their current sha256, and the contract versions; manifest.json must point at embedded files.
// Fix a failure with `make builder` and commit web/web-builder.
func TestWebBuilderDistIsCurrent(t *testing.T) {
	raw, err := fs.ReadFile(embedded, "web-builder/BUILD.json")
	if err != nil {
		t.Fatalf("embedded web-builder/BUILD.json: %v (run make builder)", err)
	}
	var info webbuilder.BuildInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatalf("BUILD.json: %v", err)
	}

	shaFile, err := os.ReadFile(filepath.Join("..", "builder", "UPSTREAM_SHA"))
	if err != nil {
		t.Fatalf("builder/UPSTREAM_SHA: %v", err)
	}
	want := strings.TrimSpace(string(shaFile))
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(want) {
		t.Fatalf("builder/UPSTREAM_SHA is not a commit SHA: %q", want)
	}
	if info.UpstreamSHA != want {
		t.Errorf("BUILD.json upstreamSha = %s, builder/UPSTREAM_SHA = %s: dist is stale, run make builder", info.UpstreamSHA, want)
	}

	patches, err := filepath.Glob(filepath.Join("..", "builder", "patches", "*.patch"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(patches)
	wantPatches := map[string]string{}
	for _, p := range patches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		wantPatches[filepath.Base(p)] = hex.EncodeToString(sum[:])
	}
	for name, sum := range wantPatches {
		if got, ok := info.Patches[name]; !ok {
			t.Errorf("patch %s is not in BUILD.json: dist is stale, run make builder", name)
		} else if got != sum {
			t.Errorf("patch %s changed since the dist was built (BUILD.json %s, file %s): run make builder", name, got, sum)
		}
	}
	for name := range info.Patches {
		if _, ok := wantPatches[name]; !ok {
			t.Errorf("BUILD.json lists patch %s, which is not in builder/patches: run make builder", name)
		}
	}

	if info.APIVersion != webbuilder.APIVersion {
		t.Errorf("BUILD.json apiVersion = %d, webbuilder.APIVersion = %d", info.APIVersion, webbuilder.APIVersion)
	}
	if info.RendererVersion != webbuilder.RendererVersion {
		t.Errorf("BUILD.json rendererVersion = %d, webbuilder.RendererVersion = %d", info.RendererVersion, webbuilder.RendererVersion)
	}

	raw, err = fs.ReadFile(embedded, "web-builder/manifest.json")
	if err != nil {
		t.Fatalf("embedded web-builder/manifest.json: %v", err)
	}
	var m webbuilder.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	mustEmbed := func(dir, name string) {
		t.Helper()
		if name == "" || strings.Contains(name, "..") {
			t.Errorf("manifest.json: bad %s file name %q", dir, name)
			return
		}
		if _, err := fs.Stat(embedded, "web-builder/"+dir+"/"+name); err != nil {
			t.Errorf("manifest.json names web-builder/%s/%s, which is not embedded: %v", dir, name, err)
		}
	}
	mustEmbed("editor", m.Editor.JS)
	if len(m.Editor.CSS) == 0 {
		t.Error("manifest.json: editor has no stylesheet")
	}
	for _, c := range m.Editor.CSS {
		mustEmbed("editor", c)
	}
	mustEmbed("site", m.Site.Runtime)
	if m.Site.CSS != "" {
		mustEmbed("site", m.Site.CSS)
	}
}
