package plugins

import (
	"bytes"
	"context"
	"testing"
)

func TestPackageAssetReturnsIndependentBytes(t *testing.T) {
	p := &Plugin{pkgAssets: map[string][]byte{"a.js": []byte("original")}}
	asset, ok := p.PackageAsset("a.js")
	if !ok {
		t.Fatal("missing asset")
	}
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan string, 1)
	go func() {
		close(ready)
		<-proceed
		got, _ := p.PackageAsset("a.js")
		result <- string(got)
	}()
	<-ready
	asset[0] = 'X'
	close(proceed)
	if got := <-result; got != "original" {
		t.Fatalf("asset changed: %q", got)
	}
}

func TestRestRoutesReturnsIndependentSlices(t *testing.T) {
	p := &Plugin{rest: []RestRoute{{Methods: []string{"GET"}, Headers: []string{"X-Test"}}}}
	view := p.RestRoutes()
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		routes := p.RestRoutes()
		result <- routes[0].Methods[0] == "GET" && routes[0].Headers[0] == "X-Test"
	}()
	<-ready
	view[0].Methods[0] = "POST"
	view[0].Headers[0] = "changed"
	close(proceed)
	if !<-result {
		t.Fatal("returned route aliases the live registration")
	}
}

func TestSettingsPageReturnsIndependentData(t *testing.T) {
	minimum, maximum := 1.0, 10.0
	p := &Plugin{settings: &SettingsPage{PageTitle: "Original", Fields: []SettingsField{{
		Options: []SettingsOption{{Label: "First"}}, Default: map[string]any{"items": []any{"one"}}, Min: &minimum, Max: &maximum,
	}}}}
	view := p.SettingsPage()
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		got := p.SettingsPage()
		f := got.Fields[0]
		result <- got.PageTitle == "Original" && f.Options[0].Label == "First" && f.Default.(map[string]any)["items"].([]any)[0] == "one" && *f.Min == 1 && *f.Max == 10
	}()
	<-ready
	view.PageTitle = "Changed"
	view.Fields[0].Options[0].Label = "Changed"
	view.Fields[0].Default.(map[string]any)["items"].([]any)[0] = "changed"
	*view.Fields[0].Min, *view.Fields[0].Max = 7, 8
	close(proceed)
	if !<-result {
		t.Fatal("returned settings alias the live registration")
	}
}

func TestAssetsReturnsIndependentData(t *testing.T) {
	p := &Plugin{assets: []Asset{{Deps: []string{"one"}, Data: map[string]any{"nested": []any{map[string]any{"key": "first"}}}}}}
	view := p.Assets()
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		got := p.Assets()[0]
		result <- got.Deps[0] == "one" && got.Data["nested"].([]any)[0].(map[string]any)["key"] == "first"
	}()
	<-ready
	view[0].Deps[0] = "changed"
	view[0].Data["nested"].([]any)[0].(map[string]any)["key"] = "changed"
	close(proceed)
	if !<-result {
		t.Fatal("returned asset data aliases the live registration")
	}
}

func TestLoadSnapshotsPackage(t *testing.T) {
	rt := newTestRuntime(t, Config{})
	pkg := buildPackage(t, "internal/plugins/testdata/probe")
	p, err := rt.Load(context.Background(), pkg, pkg.Manifest.Permissions, NewMemoryHost())
	if err != nil {
		t.Fatal(err)
	}
	original, ok := p.PackageAsset("js/probe.js")
	if !ok || len(original) == 0 {
		t.Fatal("missing package asset")
	}
	manifest := p.Manifest()
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		got, ok := p.PackageAsset("js/probe.js")
		_, added := p.PackageAsset("new.js")
		current := p.Manifest()
		result <- ok && bytes.Equal(got, original) && !added && current.Slug == manifest.Slug && len(current.Hooks) == len(manifest.Hooks) && current.Assets["js/probe.js"] == manifest.Assets["js/probe.js"]
	}()
	<-ready
	pkg.Assets["js/probe.js"][0] ^= 0xff
	pkg.Assets["js/probe.js"] = []byte("replaced")
	pkg.Assets["new.js"] = []byte("new")
	pkg.Manifest.Slug = "changed"
	pkg.Manifest.Hooks = nil
	pkg.Manifest.Assets["js/probe.js"] = "changed"
	close(proceed)
	if !<-result {
		t.Fatal("loaded plugin assets or manifest alias the package")
	}
}

func TestManifestReturnsIndependentData(t *testing.T) {
	p := &Plugin{manifest: &Manifest{Slug: "original", Hooks: []string{"one"}, Assets: map[string]string{"a": "hash"}, RequiresPlugins: []string{"dep"}, Permissions: []Permission{PermStorage}, Shortcodes: []string{"short"}}}
	view := p.Manifest()
	ready, proceed := make(chan struct{}), make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		close(ready)
		<-proceed
		got := p.Manifest()
		result <- got.Slug == "original" && got.Hooks[0] == "one" && got.Assets["a"] == "hash" && got.RequiresPlugins[0] == "dep" && got.Permissions[0] == PermStorage && got.Shortcodes[0] == "short"
	}()
	<-ready
	view.Slug = "changed"
	view.Hooks[0] = "other"
	view.Assets["a"] = "bad"
	view.RequiresPlugins[0] = "changed"
	view.Permissions[0] = "changed"
	view.Shortcodes[0] = "changed"
	close(proceed)
	if !<-result {
		t.Fatal("manifest aliases the live plugin")
	}
}
