package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Generic verification paths of the core self-update that hold whatever keys are trusted: a body that
// does not match the published size or SHA-256 and a signature that is malformed or does not verify
// are refused before the new binary is ever run, and nothing is left behind.
func TestCoreArtifactRejections(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	keys := []hubKey{{ID: "test-key", Public: base64.StdEncoding.EncodeToString(pub)}, {ID: "broken", Public: "not-a-key"}}
	bin := []byte("\x7fELF a test pursae binary")

	tried := false
	prev := coreTryBinary
	coreTryBinary = func(string, string) error { tried = true; return nil }
	t.Cleanup(func() { coreTryBinary = prev })

	dir := t.TempDir()
	target := filepath.Join(dir, "pursae")
	for _, tc := range []struct {
		name string
		art  func() *coreArtifact
		body []byte
		want string
	}{
		{"short body", func() *coreArtifact { return signedCore("9.9.9", "linux", "amd64", bin, priv, "test-key") }, bin[:len(bin)-1], "bytes"},
		{"longer body", func() *coreArtifact { return signedCore("9.9.9", "linux", "amd64", bin, priv, "test-key") }, append(append([]byte{}, bin...), 0), "bytes"},
		{"sha256 of other bytes", func() *coreArtifact {
			a := signedCore("9.9.9", "linux", "amd64", bin, priv, "test-key")
			a.Size = 0 // no size published: the checksum still decides
			return a
		}, []byte("\x7fELF a test pursae binarY"), "SHA-256"},
		{"empty sha256", func() *coreArtifact {
			a := signedCore("9.9.9", "linux", "amd64", bin, priv, "test-key")
			a.SHA256 = ""
			return a
		}, bin, "SHA-256"},
		{"signature not base64", func() *coreArtifact {
			a := signedCore("9.9.9", "linux", "amd64", bin, priv, "test-key")
			a.Signature = "%%%"
			return a
		}, bin, "malformed"},
		{"truncated signature", func() *coreArtifact {
			a := signedCore("9.9.9", "linux", "amd64", bin, priv, "test-key")
			raw, _ := base64.StdEncoding.DecodeString(a.Signature)
			a.Signature = base64.StdEncoding.EncodeToString(raw[:32])
			return a
		}, bin, "malformed"},
		{"signed by another key", func() *coreArtifact { return signedCore("9.9.9", "linux", "amd64", bin, otherPriv, "test-key") }, bin, "does not verify"},
		{"flipped signature bit", func() *coreArtifact {
			a := signedCore("9.9.9", "linux", "amd64", bin, priv, "test-key")
			raw, _ := base64.StdEncoding.DecodeString(a.Signature)
			raw[10] ^= 1
			a.Signature = base64.StdEncoding.EncodeToString(raw)
			return a
		}, bin, "does not verify"},
		{"invalid public key", func() *coreArtifact { return signedCore("9.9.9", "linux", "amd64", bin, priv, "broken") }, bin, ""},
	} {
		if err := os.WriteFile(target, []byte("old binary"), 0o700); err != nil {
			t.Fatal(err)
		}
		err := installCoreArtifact(target, "9.9.9", tc.art(), keys, bytes.NewReader(tc.body), func() error {
			t.Errorf("%s: reached the rename", tc.name)
			return nil
		})
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
		} else if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q, want it to mention %q", tc.name, err, tc.want)
		}
		if b, _ := os.ReadFile(target); string(b) != "old binary" {
			t.Errorf("%s: the executable was replaced", tc.name)
		}
		if m, _ := filepath.Glob(filepath.Join(dir, ".*")); len(m) > 0 {
			t.Errorf("%s: temporary files left: %v", tc.name, m)
		}
	}
	if tried {
		t.Error("a binary that failed verification was run")
	}
}

func TestCoreArtifactHelpers(t *testing.T) {
	var none *hubCore
	if none.artifactFor("linux", "amd64") != nil {
		t.Error("artifactFor on no release")
	}
	c := &hubCore{Artifacts: []coreArtifact{{OS: "linux", Arch: "amd64", URL: "a"}, {OS: "darwin", Arch: "arm64", URL: "b"}}}
	if a := c.artifactFor("darwin", "arm64"); a == nil || a.URL != "b" {
		t.Errorf("artifactFor darwin/arm64 = %+v", a)
	}
	if c.artifactFor("windows", "amd64") != nil {
		t.Error("artifactFor of a platform without a binary")
	}
	// The signed message is fixed (pursae-manager/API.md) and takes the checksum in lower case.
	if got := string(coreSignedMessage("1.2.3", "linux", "arm64", "ABCDEF")); got != "pursae-core-v1\n1.2.3\nlinux-arm64\nabcdef" {
		t.Errorf("coreSignedMessage = %q", got)
	}
	// A directory is never replaced.
	d := t.TempDir()
	if err := coreReplaceable(d); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Errorf("directory as executable: %v", err)
	}
	if err := coreReplaceable(filepath.Join(d, "missing")); err == nil {
		t.Error("a missing executable is replaceable")
	}
}
