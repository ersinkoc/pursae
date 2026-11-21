package plugins

import (
	"context"
	"strings"
	"testing"
)

const abiManifest = `{"slug":"abi-test","name":"ABI Test","version":"1.0.0","permissions":[],"hooks":[]%s}`

func TestManifestABIField(t *testing.T) {
	for _, tc := range []struct {
		field   string
		want    int // EffectiveABI
		compat  bool
		problem string // ParseManifest error, if any
	}{
		{field: "", want: 1, compat: true},
		{field: `,"requires":"0.5.0","abi":1`, want: 1, compat: true},
		{field: `,"requires":"0.5.0","abi":2`, want: 2, compat: false},
		{field: `,"requires":"0.6.1","abi":2`, want: 2, compat: false},
		// Releases before 0.5.0 reject a manifest with "abi", so it must require 0.5.0 or later.
		{field: `,"abi":1`, problem: `needs "requires" "0.5.0"`},
		{field: `,"requires":"0.1.0","abi":2`, problem: `needs "requires" "0.5.0"`},
		{field: `,"requires":"0.4.9","abi":1`, problem: `needs "requires" "0.5.0"`},
		{field: `,"requires":"0.5.0","abi":0`, problem: "positive integer"},
		{field: `,"requires":"0.5.0","abi":-1`, problem: "positive integer"},
		{field: `,"abi":"1"`, problem: "abi"},
		{field: `,"abi":1.5`, problem: "abi"},
	} {
		m, err := ParseManifest([]byte(strings.Replace(abiManifest, "%s", tc.field, 1)))
		if tc.problem != "" {
			if err == nil || !strings.Contains(err.Error(), tc.problem) {
				t.Errorf("abi%s: err=%v, want one mentioning %q", tc.field, err, tc.problem)
			}
			continue
		}
		if err != nil {
			t.Fatalf("abi%s: %v", tc.field, err)
		}
		if got := m.EffectiveABI(); got != tc.want {
			t.Errorf("abi%s: EffectiveABI=%d, want %d", tc.field, got, tc.want)
		}
		err = m.CheckCompatible("0.6.1")
		if (err == nil) != tc.compat {
			t.Errorf("abi%s: CheckCompatible=%v, want ok=%v", tc.field, err, tc.compat)
		}
		if err != nil && (!strings.Contains(err.Error(), "ABI version 2") || !strings.Contains(err.Error(), "supports ABI version 1")) {
			t.Errorf("unclear refusal: %v", err)
		}
	}
	if !SupportsABI(ABIVersion) || SupportsABI(0) || SupportsABI(2) {
		t.Errorf("SupportedABIs = %v", SupportedABIs)
	}
}

// The runtime refuses to load (activate) a plugin built for an ABI it does not run, before it
// compiles any Wasm.
func TestLoadRefusesUnsupportedABI(t *testing.T) {
	ctx := context.Background()
	abi2 := 2
	rt, err := NewRuntime(ctx, NewRegistry(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	pkg := &Package{Manifest: &Manifest{Slug: "abi-test", Name: "ABI Test", Version: "1.0.0", Requires: "0.5.0", ABI: &abi2}, Wasm: []byte("not wasm")}
	if _, err := rt.Load(ctx, pkg, nil, &translatorHost{}); err == nil || !strings.Contains(err.Error(), "plugin ABI version 2") {
		t.Fatalf("Load: %v, want the ABI refusal", err)
	}
}
