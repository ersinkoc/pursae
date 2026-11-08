package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"wpgo/internal/plugins"
)

// Root of trust for what Pursae Manager signs.
//
// The Manager lists its signing keys in /api/v1/index ("keys"). A key that arrives in the same
// answer as the release it verifies proves nothing: whoever controls that answer (a compromised
// Manager, its domain, a proxy in front of an http Manager) could list a key of their own and sign
// a binary with it. So the binary carries root public keys of its own, and a Manager key verifies a
// core release only when a root key endorsed it:
//
//	{"id": "hub-2026", "public": "<base64 Ed25519>", "endorsement": "<base64 Ed25519 signature>"}
//
// where the endorsement is the root key's signature over hubKeyEndorsement(id, public):
//
//	"pursae-hub-key-v1\n" + id + "\n" + base64(32-byte public key) (no trailing newline)
//
// The root keys are rootKeysBuiltin plus whatever the build passes with
//
//	go build -ldflags "-X wpgo/internal/server.rootKeysLinked=<base64>[,<base64>…]"
//
// A build without any root key (development, `go run`, a fork) cannot verify a release: Dashboard ›
// Updates explains that the site cannot update itself, unless WPGO_TRUST_HUB_KEYS=1 opts in to the
// old behaviour of trusting the keys the Manager lists (the development compose file sets it).
//
// Plugin packages: with a root key pinned, only endorsed Manager keys are added to the trusted
// plugin keys; without one, the listed keys are trusted as before (wpgo_hub_trust_keys), since a
// plugin runs sandboxed and only with the permissions the administrator grants.

// rootKeysBuiltin are the release root keys compiled into every build. Pursae has not published a
// production root key yet; when it does, its base64 public key goes here.
var rootKeysBuiltin = []string{}

// rootKeysLinked is set at link time (-ldflags -X), comma-separated base64 keys.
var rootKeysLinked = ""

// hubKeyEndorsementPrefix is the domain separator of an endorsement signature.
const hubKeyEndorsementPrefix = "pursae-hub-key-v1\n"

// hubKeyEndorsement is the message a root key signs to endorse a Manager key.
func hubKeyEndorsement(id string, pub ed25519.PublicKey) []byte {
	return []byte(hubKeyEndorsementPrefix + id + "\n" + base64.StdEncoding.EncodeToString(pub))
}

// rootKeys are the pinned release root keys of this build.
func rootKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, k := range append(append([]string{}, rootKeysBuiltin...), strings.Split(rootKeysLinked, ",")...) {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if pub, err := plugins.ParsePublicKey(k); err == nil {
			out = append(out, pub)
		}
	}
	return out
}

// trustHubKeysEnv is the explicit opt-in for builds without a root key (WPGO_TRUST_HUB_KEYS=1).
func trustHubKeysEnv() bool { return os.Getenv("WPGO_TRUST_HUB_KEYS") == "1" }

// endorsed reports whether a root key endorsed k.
func (k hubKey) endorsed(roots []ed25519.PublicKey) bool {
	pub, err := plugins.ParsePublicKey(k.Public)
	if err != nil {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.Endorsement))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	msg := hubKeyEndorsement(k.ID, pub)
	for _, r := range roots {
		if ed25519.Verify(r, msg, sig) {
			return true
		}
	}
	return false
}

// errCoreNoRoot is why a build without a root key does not update itself.
var errCoreNoRoot = errors.New("this build has no pinned Pursae release key, so a release announced by Pursae Manager cannot be verified; replace the executable (or pull the new container image) yourself, or set WPGO_TRUST_HUB_KEYS=1 on a development site")

// coreTrustCheck reports why this build cannot verify core releases, if it cannot.
func coreTrustCheck() error {
	if len(rootKeys()) == 0 && !trustHubKeysEnv() {
		return errCoreNoRoot
	}
	return nil
}

// coreReleaseKeys are the Manager keys that may verify a core release: the endorsed ones when a
// root key is pinned, every listed one with the WPGO_TRUST_HUB_KEYS opt-in, otherwise none.
func coreReleaseKeys(keys []hubKey) ([]hubKey, error) {
	if roots := rootKeys(); len(roots) > 0 {
		var out []hubKey
		for _, k := range keys {
			if k.endorsed(roots) {
				out = append(out, k)
			}
		}
		if len(out) == 0 {
			return nil, errors.New("none of the signing keys Pursae Manager lists is endorsed by this build's release key")
		}
		return out, nil
	}
	if trustHubKeysEnv() {
		return keys, nil
	}
	return nil, errCoreNoRoot
}

// pluginHubKeys are the Manager keys added to the trusted plugin keys.
func pluginHubKeys(keys []hubKey) []ed25519.PublicKey {
	roots := rootKeys()
	var out []ed25519.PublicKey
	for _, k := range keys {
		if len(roots) > 0 && !k.endorsed(roots) {
			continue
		}
		if pub, err := plugins.ParsePublicKey(k.Public); err == nil {
			out = append(out, pub)
		}
	}
	return out
}

// Transport rules for Pursae Manager -------------------------------------------------------------

// errHubInsecure refuses a plain-http Manager on a public host.
var errHubInsecure = errors.New("Pursae Manager must be reached over https (http is only allowed for localhost and *.localhost / *.test development hosts); check WPGO_HUB_URL")

// localDevHost reports whether host is a loopback or development name, where http is acceptable.
func localDevHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".test") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// hubURLAllowed checks the scheme of a URL the site sends a Manager request to.
func hubURLAllowed(u *url.URL) error {
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if localDevHost(u.Hostname()) {
			return nil
		}
	}
	return errHubInsecure
}

// hubCredentialHeaders are the headers that identify the site to the Manager.
var hubCredentialHeaders = []string{"X-Pursae-License", "X-Pursae-Site"}

// hubCheckRedirect follows at most 10 redirects, never from https to http on a public host, and
// drops the license and site headers when a redirect leaves the original host (net/http only
// strips Authorization and Cookie itself).
func hubCheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if err := hubURLAllowed(req.URL); err != nil {
		return err
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		for _, h := range hubCredentialHeaders {
			req.Header.Del(h)
		}
	}
	return nil
}
