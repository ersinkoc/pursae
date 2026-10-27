package plugins

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
)

var (
	testManifest = []byte(`{"slug":"demo","name":"Demo","version":"1.0.0","permissions":[],"hooks":["init"]}`)
	testWasm     = []byte("\x00asm\x01\x00\x00\x00")
)

func packageBytes(t *testing.T, manifest, wasm []byte, priv ed25519.PrivateKey) []byte {
	t.Helper()
	var buf bytes.Buffer
	extras := map[string][]byte{ReadmeFile: []byte("# Demo"), IconFile: []byte("<svg/>")}
	if err := WritePackage(&buf, manifest, wasm, extras, priv); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func load(data []byte, keys []ed25519.PublicKey, require bool) (*Package, error) {
	return LoadPackage(bytes.NewReader(data), int64(len(data)), keys, require)
}

func TestSignedPackageRoundTrip(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := load(packageBytes(t, testManifest, testWasm, priv), []ed25519.PublicKey{pub}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !pkg.Verified() || !pkg.SignedBy.Equal(pub) || pkg.Manifest.Slug != "demo" ||
		string(pkg.Readme) != "# Demo" || string(pkg.Icon) != "<svg/>" {
		t.Fatalf("unexpected package %+v", pkg)
	}
	if pkg.Digest != PackageDigest(testManifest, testWasm) {
		t.Fatal("digest mismatch")
	}
}

func TestUnsignedPackage(t *testing.T) {
	pub, _, _ := GenerateKey()
	data := packageBytes(t, testManifest, testWasm, nil)
	if _, err := load(data, []ed25519.PublicKey{pub}, true); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("want ErrUnsigned, got %v", err)
	}
	pkg, err := load(data, []ed25519.PublicKey{pub}, false)
	if err != nil || pkg.Signed || pkg.Verified() {
		t.Fatalf("unsigned package should load when signatures are optional: %v", err)
	}
}

func TestTamperedPackageIsRejected(t *testing.T) {
	pub, priv, _ := GenerateKey()
	sig := Sign(priv, testManifest, testWasm)

	// Re-pack with a modified wasm but the original signature.
	tampered := append(append([]byte(nil), testWasm...), 0x00)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{ManifestFile: testManifest, WasmFile: tampered, SignatureFile: sig} {
		w, _ := zw.Create(name)
		w.Write(data)
	}
	zw.Close()
	for _, require := range []bool{true, false} {
		if _, err := load(buf.Bytes(), []ed25519.PublicKey{pub}, require); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("require=%v: want ErrBadSignature, got %v", require, err)
		}
	}

	// Moving bytes between manifest and wasm must not keep the digest.
	if PackageDigest([]byte("ab"), []byte("c")) == PackageDigest([]byte("a"), []byte("bc")) {
		t.Fatal("digest is ambiguous")
	}
}

func TestPackageSignedByUntrustedKey(t *testing.T) {
	trusted, _, _ := GenerateKey()
	_, other, _ := GenerateKey()
	data := packageBytes(t, testManifest, testWasm, other)
	if _, err := load(data, []ed25519.PublicKey{trusted}, true); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
	if _, err := load(data, nil, true); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("no trusted keys: want ErrBadSignature, got %v", err)
	}
}

func TestPackageStructureChecks(t *testing.T) {
	cases := map[string]map[string]string{
		"unexpected file": {ManifestFile: string(testManifest), WasmFile: string(testWasm), "evil.sh": "x"},
		"no plugin.json":  {WasmFile: string(testWasm)},
		"no plugin.wasm":  {ManifestFile: string(testManifest)},
		"not webassembly": {ManifestFile: string(testManifest), WasmFile: "MZ"},
		"bad manifest":    {ManifestFile: `{"slug":"x"}`, WasmFile: string(testWasm)},
	}
	for name, files := range cases {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for n, d := range files {
			w, _ := zw.Create(n)
			w.Write([]byte(d))
		}
		zw.Close()
		if _, err := load(buf.Bytes(), nil, false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := load([]byte("not a zip"), nil, false); err == nil || !strings.Contains(err.Error(), "zip") {
		t.Errorf("non-zip: %v", err)
	}
}

func TestKeyEncoding(t *testing.T) {
	pub, priv, _ := GenerateKey()
	p2, err := ParsePublicKey("# comment\n" + EncodeKey(pub) + "\n")
	if err != nil || !p2.Equal(pub) {
		t.Fatalf("public key round trip: %v", err)
	}
	k2, err := ParsePrivateKey(EncodeKey(priv))
	if err != nil || !k2.Equal(priv) {
		t.Fatalf("private key round trip: %v", err)
	}
	if _, err := ParsePublicKey(EncodeKey(priv)); err == nil {
		t.Fatal("private key accepted as public key")
	}
}
